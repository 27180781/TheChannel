import { Component, EventEmitter, Input, OnDestroy, OnInit, Output } from '@angular/core';
import { FormsModule } from '@angular/forms';
import {
  NbAlertModule,
  NbButtonModule,
  NbCardModule,
  NbFormFieldModule,
  NbIconModule,
  NbInputModule,
} from '@nebular/theme';
import { Subject, Subscription, of } from 'rxjs';
import { catchError, debounceTime, distinctUntilChanged, switchMap } from 'rxjs/operators';
import {
  ChannelService,
  SLUG_PATTERN,
  SlugAvailability,
  slugifyChannelName,
} from '../../services/channel.service';
import { ChannelRequestService } from '../../services/channel-request.service';

type SlugState = 'empty' | 'checking' | 'available' | 'unavailable';

/**
 * The channel-opening card, shared by both hosts of the flow:
 *  - `instant`  — a signed-in user with no channel; POSTs /api/channels/create
 *                 and the owner is taken from the session.
 *  - `request`  — the signed-out fallback on the landing page; still POSTs the
 *                 old /api/channel-request so people who prefer not to sign in
 *                 keep a path in.
 * Both share the slug derivation, the live availability check and the URL
 * preview, which is the whole reason this is one component and not two forms.
 */
@Component({
  selector: 'app-create-channel-form',
  standalone: true,
  imports: [
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbFormFieldModule,
    NbIconModule,
    NbAlertModule,
  ],
  templateUrl: './create-channel-form.component.html',
  styleUrl: './create-channel-form.component.scss',
})
export class CreateChannelFormComponent implements OnInit, OnDestroy {
  /** `instant` creates the channel now; `request` files a request for review. */
  @Input() variant: 'instant' | 'request' = 'instant';
  @Input() title = 'פתיחת ערוץ';
  @Input() subtitle = '';

  /** Emitted with the new slug once the channel actually exists. */
  @Output() created = new EventEmitter<string>();

  name = '';
  slug = '';
  description = '';

  // `request` variant only — the anonymous applicant's contact details.
  reqName = '';
  reqEmail = '';

  slugState: SlugState = 'empty';
  slugMessage = '';
  submitting = false;
  submitted = false;
  formError = '';

  /** Host shown in the URL preview, e.g. `example.com/channel/my-slug`. */
  readonly host = window.location.host;

  // True once the user edits the slug by hand: from then on the name no longer
  // overwrites it.
  private slugTouched = false;
  private readonly slugChecks$ = new Subject<string>();
  private slugSub?: Subscription;

  constructor(
    private channelService: ChannelService,
    private channelRequestService: ChannelRequestService,
  ) {}

  ngOnInit(): void {
    this.slugSub = this.slugChecks$.pipe(
      debounceTime(400),
      distinctUntilChanged(),
      switchMap(slug => this.channelService.checkSlugAvailability(slug).pipe(
        // A failed check must not wedge the field in "checking" forever; treat
        // it as "no opinion" and let the server have the last word on submit.
        catchError(() => of<SlugAvailability | null>(null)),
      )),
    ).subscribe(result => {
      if (!result) {
        this.slugState = 'empty';
        this.slugMessage = '';
        return;
      }
      this.slugState = result.available ? 'available' : 'unavailable';
      this.slugMessage = result.available
        ? 'הכתובת פנויה'
        : this.reasonText(result.reason);
    });
  }

  ngOnDestroy(): void {
    this.slugSub?.unsubscribe();
    this.slugChecks$.complete();
  }

  onNameChange(): void {
    if (this.slugTouched) return;
    const suggestion = slugifyChannelName(this.name);
    // A Hebrew name slugifies to nothing — leave whatever the user has rather
    // than wiping a slug they may already be happy with.
    if (!suggestion) return;
    this.slug = suggestion;
    this.queueSlugCheck();
  }

  onSlugChange(): void {
    this.slugTouched = true;
    // Normalise as they type so the field can never hold an illegal character.
    this.slug = this.slug.toLowerCase().replace(/\s+/g, '-').replace(/[^a-z0-9-]/g, '');
    this.queueSlugCheck();
  }

  get slugValid(): boolean {
    return SLUG_PATTERN.test(this.slug);
  }

  get canSubmit(): boolean {
    if (this.submitting || !this.name.trim() || !this.slugValid) return false;
    if (this.slugState === 'unavailable') return false;
    if (this.variant === 'request') {
      return !!this.reqName.trim() && !!this.reqEmail.trim() && !!this.description.trim();
    }
    return true;
  }

  async submit(): Promise<void> {
    if (!this.canSubmit) {
      this.formError = this.name.trim()
        ? 'יש למלא את כל השדות ולבחור כתובת תקינה'
        : 'יש להזין שם לערוץ';
      return;
    }

    this.formError = '';
    this.submitting = true;
    try {
      if (this.variant === 'request') {
        // /api/channel-request has no field for the channel's display name, so
        // fold it into the description rather than silently dropping what the
        // applicant typed — the reviewer needs it to name the channel.
        await this.channelRequestService.submitRequest(
          this.reqName.trim(),
          this.reqEmail.trim(),
          this.slug,
          `שם הערוץ המבוקש: ${this.name.trim()}\n\n${this.description.trim()}`,
        );
        this.submitted = true;
      } else {
        const channel = await this.channelService.createChannel(
          this.slug, this.name.trim(), this.description.trim(),
        );
        this.submitted = true;
        this.created.emit(channel.slug);
      }
    } catch (err: any) {
      this.handleError(err);
    } finally {
      this.submitting = false;
    }
  }

  private handleError(err: any): void {
    // Every one of these endpoints answers failures as plain text (http.Error),
    // which Angular hands back as a string in err.error.
    const text = typeof err?.error === 'string' && err.error
      ? err.error
      : (err?.error?.message || '');

    switch (err?.status) {
      case 409:
        // Inline on the slug field — that is the field they have to change.
        this.slugState = 'unavailable';
        this.slugMessage = 'ה-slug תפוס, בחר אחר';
        this.formError = '';
        return;
      case 403:
        this.formError = 'הגעת למכסה המרבית של 5 ערוצים לחשבון. כדי לפתוח ערוץ נוסף יש למחוק אחד מהערוצים הקיימים.';
        return;
      case 429:
        this.formError = 'נשלחו יותר מדי בקשות בזמן קצר. המתן מספר דקות ונסה שוב.';
        return;
      case 400:
        this.formError = text || 'הפרטים שהוזנו אינם תקינים.';
        return;
      case 401:
        this.formError = 'פג תוקף ההתחברות. יש להתחבר מחדש כדי לפתוח ערוץ.';
        return;
      default:
        this.formError = text || 'שגיאה בפתיחת הערוץ. נסה שוב.';
    }
  }

  private queueSlugCheck(): void {
    this.formError = '';
    if (!this.slug) {
      this.slugState = 'empty';
      this.slugMessage = '';
      return;
    }
    if (!this.slugValid) {
      this.slugState = 'unavailable';
      this.slugMessage = 'כתובת לא תקינה — 3 עד 50 תווים, אותיות אנגליות קטנות, ספרות ומקפים';
      return;
    }
    if (this.variant === 'request') {
      // /api/channels/slug-available sits behind checkLogin, so a signed-out
      // applicant would only collect 401s. The format check is all we can
      // honestly offer them here.
      this.slugState = 'empty';
      this.slugMessage = 'הפורמט תקין — הזמינות תיבדק בעת אישור הבקשה';
      return;
    }
    this.slugState = 'checking';
    this.slugMessage = '';
    this.slugChecks$.next(this.slug);
  }

  private reasonText(reason: SlugAvailability['reason']): string {
    switch (reason) {
      case 'taken': return 'ה-slug תפוס, בחר אחר';
      case 'reserved': return 'הכתובת שמורה למערכת, בחר אחרת';
      case 'invalid': return 'כתובת לא תקינה — אותיות אנגליות קטנות, ספרות ומקפים בלבד';
      default: return 'הכתובת אינה זמינה';
    }
  }
}
