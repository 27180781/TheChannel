import { Component, EventEmitter, OnInit, Output } from '@angular/core';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule, NbCardModule, NbIconModule, NbInputModule, NbSpinnerModule, NbToastrService,
  NbToggleModule, NbTooltipModule,
} from '@nebular/theme';
import { AdminService } from '../../../services/admin.service';
import { AuthService } from '../../../services/auth.service';
import { ChatService } from '../../../services/chat.service';

// Same normalisation as normEmail on the server.
function normalizeEmail(email: string | undefined): string {
  return (email ?? '').trim().toLowerCase();
}

const EMAIL_SHAPE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
/** Mirrors maxAllowedViewers in viewers.go. */
const MAX_VIEWERS = 5000;

/**
 * "מי יכול לצפות": the owner locks the channel to a list of emails. Only an
 * account signed in with one of them (or the channel's staff) can read it;
 * the link alone, or any other sign-in, is refused by the server.
 */
@Component({
  selector: 'app-channel-viewers',
  standalone: true,
  imports: [
    FormsModule, NbCardModule, NbButtonModule, NbIconModule, NbInputModule, NbSpinnerModule,
    NbToggleModule, NbTooltipModule,
  ],
  templateUrl: './viewers.component.html',
  styleUrl: './viewers.component.scss',
})
export class ViewersComponent implements OnInit {
  @Output() accessDenied = new EventEmitter<void>();

  enabled = false;
  emails: string[] = [];
  /** Addresses added since the last save. */
  pending = new Set<string>();

  /** The paste box: one or many addresses, separated by lines, commas or spaces. */
  draft = '';
  addError = '';
  filter = '';

  loading = true;
  loadFailed = false;
  saving = false;
  private snapshot = '';

  constructor(
    private adminService: AdminService,
    private authService: AuthService,
    private chatService: ChatService,
    private toast: NbToastrService,
  ) {}

  get dirty(): boolean {
    return this.state() !== this.snapshot;
  }

  get myEmail(): string {
    return normalizeEmail(this.authService.userInfo?.email);
  }

  get shown(): string[] {
    const f = normalizeEmail(this.filter);
    return f ? this.emails.filter(e => e.includes(f)) : this.emails;
  }

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loading = true;
    this.loadFailed = false;
    this.adminService.getViewers()
      .then(v => this.apply(v.enabled, v.emails ?? []))
      .catch(err => {
        this.loadFailed = true;
        if (err?.status === 403 || err?.status === 401) {
          this.accessDenied.emit();
          return;
        }
        this.toast.danger('', 'לא הצלחנו לטעון את רשימת המורשים — נסו שוב');
      })
      .finally(() => this.loading = false);
  }

  addDraft(): void {
    this.addError = '';
    const parts = this.draft.split(/[\s,;]+/).map(normalizeEmail).filter(Boolean);
    if (!parts.length) {
      this.addError = 'יש להזין כתובת מייל';
      return;
    }
    const bad = parts.filter(p => !EMAIL_SHAPE.test(p));
    if (bad.length) {
      this.addError = bad.length === 1
        ? `הכתובת ${bad[0]} אינה תקינה`
        : `${bad.length} כתובות אינן תקינות, למשל ${bad[0]}`;
      return;
    }
    const known = new Set(this.emails);
    const fresh = [...new Set(parts)].filter(p => !known.has(p));
    if (!fresh.length) {
      this.addError = parts.length === 1 ? 'הכתובת כבר ברשימה' : 'כל הכתובות כבר ברשימה';
      return;
    }
    if (this.emails.length + fresh.length > MAX_VIEWERS) {
      this.addError = `אפשר עד ${MAX_VIEWERS.toLocaleString('he-IL')} כתובות ברשימה`;
      return;
    }
    fresh.forEach(e => this.pending.add(e));
    this.emails = [...fresh, ...this.emails];
    this.draft = '';
  }

  remove(email: string): void {
    this.emails = this.emails.filter(e => e !== email);
    this.pending.delete(email);
  }

  save(): void {
    if (this.saving) return;
    this.saving = true;
    this.adminService.setViewers({ enabled: this.enabled, emails: this.emails })
      .then(v => {
        this.apply(v.enabled, v.emails ?? []);
        // The channel's header and info form read viewers_only off /info.
        if (this.chatService.channelInfo) {
          this.chatService.channelInfo.viewers_only = v.enabled;
        }
        this.toast.success('', v.enabled ? 'הערוץ נעול למורשים בלבד' : 'ההגדרות נשמרו');
      })
      .catch(err => {
        const text = typeof err?.error === 'string' ? err.error : '';
        if (err?.status === 400 && text.includes('invalid email')) {
          this.toast.danger('', 'אחת הכתובות ברשימה אינה תקינה');
        } else if (err?.status === 403 || err?.status === 401) {
          this.toast.danger('', 'רק בעלי הערוץ יכולים לשנות את ההגדרה');
        } else {
          this.toast.danger('', 'השמירה נכשלה — נסו שוב');
        }
      })
      .finally(() => this.saving = false);
  }

  private apply(enabled: boolean, emails: string[]): void {
    this.enabled = enabled;
    this.emails = [...emails].sort();
    this.pending.clear();
    this.snapshot = this.state();
  }

  private state(): string {
    return JSON.stringify([this.enabled, [...this.emails].sort()]);
  }
}
