import { Component, ElementRef, EventEmitter, HostListener, Input, OnDestroy, OnInit, Output, ViewChild } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { HttpClient } from '@angular/common/http';
import { firstValueFrom } from 'rxjs';
import {
  NbAlertModule, NbButtonModule, NbCardModule, NbCheckboxModule, NbIconModule,
  NbInputModule, NbSpinnerModule, NbToastrService,
} from '@nebular/theme';
import { ConfirmService } from '../../../services/confirm.service';
import {
  NEDARIM_FRAME_SRC, NEDARIM_ORIGIN, isValidIsraeliId, parseNedarimMessage, startPaymentMessage,
} from './nedarim-frame';
import { DomSanitizer, SafeResourceUrl } from '@angular/platform-browser';

export type AddonId = 'no_credit' | 'no_ads';

interface AddonCharge {
  charged_on: string;
  amount_gross: number;
  document_number: string | null;
  document_url: string | null;
}

interface AddonSubscription {
  id: string;
  addons: AddonId[];
  source: 'owner' | 'comp';
  status: 'active' | 'cancelled' | 'ended';
  monthly_gross: number;
  card_last4: string | null;
  next_charge_date: string | null;
  paid_until: string | null;
  created_at: string;
  cancelled_at: string | null;
  charges: AddonCharge[];
}

interface AddonsStatus {
  price: number;
  termsVersion: string;
  entitlement: Record<AddonId, string | null>;
  subscriptions: AddonSubscription[];
}

interface Started {
  subscriptionId: string;
  transactionId: string;
  key: string;
  amountGross: number;
}

type Step = 'choose' | 'pay' | 'processing' | 'done' | 'pending';

export const ADDONS: { id: AddonId; title: string; icon: string; text: string }[] = [
  {
    id: 'no_credit', title: 'הסרת הקרדיט', icon: 'eye-off-outline',
    text: 'השורה "הוקם ע״י" והקישור לפתיחת ערוץ לא יוצגו בראש הערוץ.',
  },
  {
    id: 'no_ads', title: 'ערוץ ללא פרסומות', icon: 'slash-outline',
    text: 'בערוץ לא יוצגו פרסומות של המערכת: לא הפרסומת שבמסגרת, ולא פרסומות מגנט שהמערכת קובעת.',
  },
];

const ADDON_TITLE: Record<AddonId, string> = { no_credit: 'הסרת הקרדיט', no_ads: 'ערוץ ללא פרסומות' };

/**
 * "שדרוגים": the channel's paid add-ons (backend/channel_addons.go). The
 * owner ticks one or both, accepts the purchase terms, and joins a monthly
 * standing order in Nedarim's iframe. Below, quietly, "המנויים שלי": what the
 * channel pays for, its receipts, and the cancel button.
 */
@Component({
  selector: 'app-channel-addons',
  standalone: true,
  imports: [
    FormsModule, NbCardModule, NbButtonModule, NbCheckboxModule, NbIconModule,
    NbInputModule, NbSpinnerModule, NbAlertModule,
  ],
  templateUrl: './addons.component.html',
  styleUrl: './addons.component.scss',
})
export class AddonsComponent implements OnInit, OnDestroy {
  @Input() slug!: string;
  @Output() accessDenied = new EventEmitter<void>();

  readonly addons = ADDONS;
  readonly title = ADDON_TITLE;
  readonly frameSrc: SafeResourceUrl;

  status?: AddonsStatus;
  loading = true;
  loadFailed = false;
  loadError = '';

  selected: Record<AddonId, boolean> = { no_credit: false, no_ads: false };
  need = { phone: true, address: false };
  zeout = '';
  phone = '';
  payerName = '';
  taxId = '';
  street = '';
  city = '';
  acceptedTerms = false;
  touched = false;

  step: Step = 'choose';
  starting = false;
  session?: Started;
  frameReady = false;
  frameHeight = 420;
  hint = '';
  showSubs = false;
  cancelling = '';

  @ViewChild('payFrame') private payFrame?: ElementRef<HTMLIFrameElement>;
  private pollTimer = 0;

  constructor(
    private http: HttpClient,
    private toastr: NbToastrService,
    private confirm: ConfirmService,
    sanitizer: DomSanitizer,
  ) {
    this.frameSrc = sanitizer.bypassSecurityTrustResourceUrl(NEDARIM_FRAME_SRC);
  }

  ngOnInit() {
    this.load();
    this.http.get<{ need: { phone: boolean; address: boolean } }>(`/api/channel/${this.slug}/admin/addons/buyer-form`)
      .subscribe({ next: (r) => { if (r?.need) this.need = r.need; }, error: () => {} });
  }

  ngOnDestroy() {
    clearTimeout(this.pollTimer);
  }

  async load() {
    this.loading = true;
    this.loadFailed = false;
    try {
      this.status = await firstValueFrom(this.http.get<AddonsStatus>(`/api/channel/${this.slug}/admin/addons`));
    } catch (err: any) {
      this.loadFailed = true;
      if (err?.status === 403 || err?.status === 401) this.accessDenied.emit();
      this.loadError = err?.error?.message || 'לא הצלחנו לטעון את השדרוגים';
    } finally {
      this.loading = false;
    }
  }

  get price(): number {
    return this.status?.price ?? 15;
  }

  /** Until when the add-on is on, or null when it is off. */
  activeUntil(id: AddonId): Date | null {
    const v = this.status?.entitlement?.[id];
    if (!v) return null;
    const d = new Date(v);
    return d.getTime() > Date.now() ? d : null;
  }

  /** The owner's own standing order that renews this add-on, if any. */
  renewing(id: AddonId): AddonSubscription | undefined {
    return this.status?.subscriptions.find((s) => s.source === 'owner' && s.status === 'active' && s.addons.includes(id));
  }

  canBuy(id: AddonId): boolean {
    return !this.renewing(id);
  }

  toggle(id: AddonId) {
    if (this.canBuy(id)) this.selected[id] = !this.selected[id];
  }

  get chosen(): AddonId[] {
    return this.addons.map((a) => a.id).filter((id) => this.selected[id] && this.canBuy(id));
  }

  get total(): number {
    return this.chosen.length * this.price;
  }

  get zeoutOk(): boolean {
    return isValidIsraeliId(this.zeout);
  }

  get phoneOk(): boolean {
    const p = this.phone.replace(/[\s-]/g, '');
    return /^(\+972|0)5\d{8}$/.test(p) || /^(\+972|0)[2-9]\d{7,8}$/.test(p);
  }

  get addressOk(): boolean {
    return !this.need.address || (!!this.street.trim() && !!this.city.trim());
  }

  get taxIdOk(): boolean {
    return !this.taxId.trim() || /^\d{8,9}$/.test(this.taxId.trim());
  }

  get formOk(): boolean {
    return this.chosen.length > 0 && this.zeoutOk && this.phoneOk && this.addressOk && this.taxIdOk && this.acceptedTerms;
  }

  async start() {
    this.touched = true;
    if (!this.formOk || this.starting || !this.status) return;
    this.starting = true;
    try {
      this.session = await firstValueFrom(this.http.post<Started>(`/api/channel/${this.slug}/admin/addons/start`, {
        addons: this.chosen,
        zeout: this.zeout.trim(),
        phone: this.phone.trim(),
        payerName: this.payerName.trim(),
        taxId: this.taxId.trim(),
        street: this.street.trim(),
        city: this.city.trim(),
        acceptedTerms: this.acceptedTerms,
        termsVersion: this.status.termsVersion,
      }));
      this.frameReady = false;
      this.hint = '';
      this.step = 'pay';
    } catch (err: any) {
      this.toastr.danger('', err?.error?.message || 'לא הצלחנו לפתוח את התשלום. נסו שוב');
      if (err?.status === 409 && err?.error?.error === 'terms_changed') this.load();
    } finally {
      this.starting = false;
    }
  }

  backToChoose() {
    this.step = 'choose';
    this.session = undefined;
  }

  private sendStart() {
    const win = this.payFrame?.nativeElement.contentWindow;
    if (!this.session || !win) return;
    win.postMessage(startPaymentMessage(this.session.transactionId, this.session.key), NEDARIM_ORIGIN);
  }

  @HostListener('window:message', ['$event'])
  onFrameMessage(ev: MessageEvent) {
    if (this.step !== 'pay' || ev.source !== this.payFrame?.nativeElement.contentWindow) return;
    const m = parseNedarimMessage(ev);
    if (!m) return;
    switch (m.type) {
      case 'ready':
        this.frameReady = true;
        this.sendStart();
        break;
      case 'height':
        this.frameHeight = m.px + 15;
        break;
      case 'back':
        this.sendStart();
        break;
      case 'pending':
        this.hint = 'ממתינים לאישור התשלום…';
        break;
      case 'result':
        if (m.ok) this.waitForActivation();
        else this.hint = m.message || 'התשלום לא עבר. אפשר לנסות שוב.';
        break;
    }
  }

  /** The iframe said OK; the add-on is on once Magnet has Nedarim's callback. */
  private waitForActivation(tries = 0) {
    this.step = 'processing';
    const id = this.session?.subscriptionId;
    clearTimeout(this.pollTimer);
    this.http.get<AddonsStatus>(`/api/channel/${this.slug}/admin/addons`).subscribe({
      next: (st) => {
        this.status = st;
        if (st.subscriptions.some((s) => s.id === id && s.status === 'active')) {
          this.step = 'done';
          this.selected = { no_credit: false, no_ads: false };
          this.acceptedTerms = false;
          return;
        }
        this.retry(tries);
      },
      error: () => this.retry(tries),
    });
  }

  private retry(tries: number) {
    if (tries >= 20) {
      this.step = 'pending';
      return;
    }
    this.pollTimer = window.setTimeout(() => this.waitForActivation(tries + 1), 3000);
  }

  finish() {
    this.step = 'choose';
    this.session = undefined;
    this.load();
  }

  async cancel(sub: AddonSubscription) {
    if (this.cancelling) return;
    const until = this.periodEndText(sub);
    const ok = await this.confirm.ask({
      title: 'לבטל את המנוי?',
      message: `הוראת הקבע תימחק ולא יהיו חיובים נוספים. ${until ? `השדרוג ימשיך לפעול עד ${until}, ואז יוסר מהערוץ.` : 'השדרוג יוסר מהערוץ בסוף התקופה ששולמה.'}`,
      confirmLabel: 'ביטול המנוי',
      status: 'danger',
      icon: 'close-circle-outline',
    });
    if (!ok) return;
    this.cancelling = sub.id;
    try {
      const r = await firstValueFrom(this.http.post<{ nedarimDeleted: boolean | null }>(
        `/api/channel/${this.slug}/admin/addons/cancel`, { subscriptionId: sub.id }));
      if (r?.nedarimDeleted === false) {
        this.toastr.warning('', 'המנוי בוטל. המחיקה בנדרים תושלם בשעה הקרובה, ולא תחויבו');
      } else {
        this.toastr.success('', 'המנוי בוטל. לא יהיו חיובים נוספים');
      }
      await this.load();
    } catch (err: any) {
      this.toastr.danger('', err?.error?.message || 'המנוי לא בוטל. נסו שוב');
    } finally {
      this.cancelling = '';
    }
  }

  subTitle(sub: AddonSubscription): string {
    return sub.addons.map((a) => ADDON_TITLE[a]).join(' + ');
  }

  subState(sub: AddonSubscription): string {
    if (sub.source === 'comp') return 'ניתן במתנה מהנהלת המערכת';
    if (sub.status === 'active') return 'פעיל, מתחדש כל חודש';
    if (sub.status === 'cancelled') return 'בוטל';
    return 'הופסק';
  }

  periodEndText(sub: AddonSubscription): string {
    if (!sub.paid_until) return '';
    const d = new Date(sub.paid_until);
    return d.getTime() > Date.now() ? this.formatDate(sub.paid_until) : '';
  }

  formatDate(iso: string | Date | null): string {
    if (!iso) return '';
    const d = iso instanceof Date ? iso : new Date(iso);
    return isNaN(d.getTime()) ? '' : d.toLocaleDateString('he-IL');
  }

  formatIls(n: number): string {
    return `${Number(n).toLocaleString('he-IL', { maximumFractionDigits: 2 })} ₪`;
  }
}
