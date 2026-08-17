import { Component, EventEmitter, OnDestroy, OnInit, Input, Output } from '@angular/core';
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

type SlugState = 'empty' | 'checking' | 'available' | 'unavailable';

/**
 * The channel-opening card: a signed-in user with no channel POSTs
 * /api/channels/create and the owner is taken from the session — the only way
 * a channel is opened.
 *
 * Once the channel exists the card switches to its `created` state: the full
 * channel URL with a copy button, a short first-run guide, and the button that
 * finally enters the channel. Entering is deliberately a user action rather
 * than an automatic redirect, so there is a moment to copy the link.
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
  @Input() title = 'פתיחת ערוץ';
  @Input() subtitle = '';

  /**
   * Emitted with the new slug when the user asks to enter the channel — the
   * host refreshes the cached user (the new owner role) and navigates.
   */
  @Output() created = new EventEmitter<string>();

  name = '';
  slug = '';
  description = '';

  slugState: SlugState = 'empty';
  slugMessage = '';
  submitting = false;
  formError = '';

  /** Set once the channel exists; switches the card to the success screen. */
  createdSlug = '';
  copied = false;
  copyFailed = false;
  entering = false;

  /** Host shown in the URL preview, e.g. `example.com/channel/my-slug`. */
  readonly host = window.location.host;

  readonly nextSteps = [
    {
      icon: 'share-outline',
      title: 'שיתוף הקישור',
      text: 'זה מה ששולחים לאנשים כדי שיצטרפו לערוץ.',
    },
    {
      icon: 'paper-plane-outline',
      title: 'פרסום הודעה',
      text: 'תיבת הכתיבה נמצאת בתחתית מסך הערוץ.',
    },
    {
      icon: 'people-outline',
      title: 'הוספת כותבים ומנהלים',
      text: 'דרך פאנל הניהול, במסך ההרשאות.',
    },
    {
      icon: 'brush-outline',
      title: 'עיצוב הערוץ',
      text: 'שם, תיאור ולוגו — גם הם בפאנל הניהול.',
    },
  ];

  // True once the user edits the slug by hand: from then on the name no longer
  // overwrites it.
  private slugTouched = false;
  private readonly slugChecks$ = new Subject<string>();
  private slugSub?: Subscription;
  private copyResetTimer?: ReturnType<typeof setTimeout>;

  constructor(private channelService: ChannelService) {}

  /** The link handed to readers — never hardcode the domain. */
  get channelUrl(): string {
    return `${window.location.origin}/channel/${this.createdSlug}`;
  }

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
    if (this.copyResetTimer) clearTimeout(this.copyResetTimer);
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
    return this.slugState !== 'unavailable';
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
      const channel = await this.channelService.createChannel(
        this.slug, this.name.trim(), this.description.trim(),
      );
      this.createdSlug = channel.slug;
    } catch (err: any) {
      this.handleError(err);
    } finally {
      this.submitting = false;
    }
  }

  async copyUrl(): Promise<void> {
    this.copyFailed = false;
    try {
      if (!navigator.clipboard?.writeText) throw new Error('clipboard unavailable');
      await navigator.clipboard.writeText(this.channelUrl);
      this.copied = true;
    } catch {
      // Insecure context or a browser that refuses the API — the URL is right
      // there and selectable, so just say so instead of failing silently.
      this.copyFailed = true;
      this.copied = false;
    }
    if (this.copyResetTimer) clearTimeout(this.copyResetTimer);
    this.copyResetTimer = setTimeout(() => {
      this.copied = false;
      this.copyFailed = false;
    }, 2500);
  }

  enterChannel(): void {
    if (!this.createdSlug) return;
    this.entering = true;
    this.created.emit(this.createdSlug);
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
