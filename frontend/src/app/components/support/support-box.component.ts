import { Component, Input, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbAlertModule, NbButtonModule, NbCardModule, NbFormFieldModule,
  NbIconModule, NbInputModule, NbSpinnerModule, NbToastrService,
} from '@nebular/theme';
import { SupportService, SupportTicket } from '../../services/support.service';

/**
 * "Contact the operator" — the same box on the public landing page and inside
 * a channel's admin panel.
 *
 * The two surfaces differ only in what the sender has to type: signed in, the
 * name and email come from the session (the server ignores them in the body
 * either way), so the form is just a subject and a message. Anonymous, the
 * email is required, because it is the only way the operator knows who asked.
 *
 * Existing threads are listed underneath so a reply is read in the same place
 * it was sent from. A signed-in user's threads come from the server by session
 * email; an anonymous sender's come from the tokens this browser kept.
 */
@Component({
  selector: 'app-support-box',
  standalone: true,
  imports: [
    CommonModule, FormsModule, NbCardModule, NbButtonModule, NbInputModule,
    NbFormFieldModule, NbIconModule, NbAlertModule, NbSpinnerModule,
  ],
  template: `
    <nb-card class="support-box">
      <nb-card-header>
        <h5 class="mb-0">{{ title }}</h5>
        @if (subtitle) { <p class="support-sub">{{ subtitle }}</p> }
      </nb-card-header>

      <nb-card-body>
        @if (sent) {
          <nb-alert status="success" class="mb-3">
            <strong>הפנייה נשלחה.</strong>
            @if (signedIn) {
              נחזור אליך בהקדם. התשובה תופיע כאן, במסך הזה.
            } @else {
              נחזור אליך בהקדם. שמור את הדף הזה — התשובה תופיע כאן, באותו דפדפן.
            }
          </nb-alert>
        }

        @if (error) { <nb-alert status="danger" class="mb-3">{{ error }}</nb-alert> }

        <form (ngSubmit)="submit()" #f="ngForm">
          @if (!signedIn) {
            <div class="row g-2 mb-2">
              <div class="col-12 col-md-6">
                <input nbInput fullWidth type="email" name="email" [(ngModel)]="email"
                       placeholder="כתובת אימייל (חובה)" required maxlength="254">
              </div>
              <div class="col-12 col-md-6">
                <input nbInput fullWidth type="text" name="name" [(ngModel)]="name"
                       placeholder="שם (לא חובה)" maxlength="100">
              </div>
            </div>
          }

          <input nbInput fullWidth type="text" name="subject" [(ngModel)]="subject"
                 placeholder="נושא הפנייה" required maxlength="200" class="mb-2">

          <textarea nbInput fullWidth name="body" [(ngModel)]="body" rows="5"
                    placeholder="במה נוכל לעזור?" required maxlength="5000"
                    class="mb-2"></textarea>

          <button nbButton status="primary" type="submit" [disabled]="submitting">
            @if (submitting) { <nb-icon icon="loader-outline"></nb-icon> שולח... }
            @else { שליחת פנייה }
          </button>
        </form>
      </nb-card-body>
    </nb-card>

    @if (tickets.length) {
      <nb-card class="support-box">
        <nb-card-header><h5 class="mb-0">הפניות שלי</h5></nb-card-header>
        <nb-card-body>
          @for (t of tickets; track t.id) {
            <div class="ticket">
              <div class="ticket__head" (click)="toggle(t.id)">
                <span class="ticket__subject">{{ t.subject }}</span>
                <span class="ticket__status" [class]="'st-' + t.status">
                  {{ statusText(t.status) }}
                </span>
              </div>

              @if (openId === t.id) {
                <div class="thread">
                  @for (m of t.messages; track $index) {
                    <div class="msg" [class.msg--admin]="m.author === 'admin'">
                      <div class="msg__meta">
                        {{ m.author === 'admin' ? 'הנהלת המערכת' : m.authorName }}
                        · {{ m.createdAt | date:'dd/MM/yyyy HH:mm' }}
                      </div>
                      <div class="msg__body">{{ m.body }}</div>
                    </div>
                  }

                  @if (t.status !== 'closed') {
                    <textarea nbInput fullWidth rows="3" [(ngModel)]="replyBody"
                              [ngModelOptions]="{standalone: true}"
                              placeholder="הוספת הודעה לפנייה" maxlength="5000"
                              class="mt-2"></textarea>
                    <button nbButton size="small" status="primary" class="mt-2"
                            [disabled]="replying" (click)="sendReply(t)">שליחה</button>
                  } @else {
                    <p class="text-muted mt-2 mb-0">הפנייה נסגרה.</p>
                  }
                </div>
              }
            </div>
          }
        </nb-card-body>
      </nb-card>
    }
  `,
  styles: [`
    .support-box { margin-bottom: 1rem; }
    .support-sub { margin: 0.25rem 0 0; font-size: 0.85rem; color: var(--text-hint-color); }

    .ticket { border-bottom: 1px solid var(--divider-color); padding: 0.5rem 0; }
    .ticket:last-child { border-bottom: 0; }
    .ticket__head { display: flex; justify-content: space-between; align-items: center;
                    gap: 0.5rem; cursor: pointer; }
    .ticket__subject { font-weight: 600; }
    .ticket__status { font-size: 0.75rem; padding: 0.1rem 0.5rem; border-radius: 1rem;
                      white-space: nowrap; }
    .st-open { background: var(--color-warning-transparent-200, #fff3cd); color: var(--color-warning-700, #8a6d3b); }
    .st-answered { background: var(--color-success-transparent-200, #d1e7dd); color: var(--color-success-700, #0a3622); }
    .st-closed { background: var(--background-basic-color-3, #e9ecef); color: var(--text-hint-color, #6c757d); }

    .thread { margin-top: 0.5rem; }
    .msg { padding: 0.5rem 0.75rem; border-radius: 0.5rem; margin-bottom: 0.4rem;
           background: var(--background-basic-color-2, #f7f9fc); }
    .msg--admin { background: var(--color-primary-transparent-100, #edf3ff);
                  border-inline-start: 3px solid var(--color-primary-default, #3366ff); }
    .msg__meta { font-size: 0.72rem; color: var(--text-hint-color, #8f9bb3); margin-bottom: 0.2rem; }
    /* Bodies are plain text, rendered by interpolation — never markdown, so a
       ticket cannot inject markup into the operator's inbox. */
    .msg__body { white-space: pre-wrap; word-break: break-word; }
  `],
})
export class SupportBoxComponent implements OnInit {
  @Input() title = 'פנייה למערכת';
  @Input() subtitle = '';
  /** Set when the box is shown inside a channel, so the operator sees which. */
  @Input() channelSlug = '';
  /** Drives which fields are asked for; the server decides identity regardless. */
  @Input() signedIn = false;

  subject = '';
  body = '';
  name = '';
  email = '';

  submitting = false;
  sent = false;
  error = '';

  tickets: SupportTicket[] = [];
  openId = '';
  replyBody = '';
  replying = false;

  constructor(
    private support: SupportService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.loadTickets();
  }

  async submit(): Promise<void> {
    this.error = '';
    if (!this.subject.trim() || !this.body.trim()) {
      this.error = 'יש למלא נושא ותוכן.';
      return;
    }
    if (!this.signedIn && !this.email.trim()) {
      this.error = 'יש להזין כתובת אימייל כדי שנוכל לחזור אליך.';
      return;
    }

    this.submitting = true;
    try {
      await this.support.createTicket({
        subject: this.subject.trim(),
        body: this.body.trim(),
        name: this.name.trim(),
        email: this.email.trim(),
        channelSlug: this.channelSlug,
      });
      this.sent = true;
      this.subject = '';
      this.body = '';
      await this.loadTickets();
    } catch (err: any) {
      this.error = this.errorText(err);
    } finally {
      this.submitting = false;
    }
  }

  toggle(id: string): void {
    this.openId = this.openId === id ? '' : id;
    this.replyBody = '';
  }

  async sendReply(t: SupportTicket): Promise<void> {
    const body = this.replyBody.trim();
    if (!body) return;
    this.replying = true;
    try {
      const updated = await this.support.reply(t.id, body, this.tokenFor(t.id));
      // Replace in place so the open thread updates without a full reload.
      const i = this.tickets.findIndex(x => x.id === t.id);
      if (i >= 0) this.tickets[i] = updated;
      this.replyBody = '';
    } catch (err: any) {
      this.toastr.danger('', this.errorText(err));
    } finally {
      this.replying = false;
    }
  }

  statusText(s: string): string {
    switch (s) {
      case 'open': return 'ממתין למענה';
      case 'answered': return 'נענתה';
      case 'closed': return 'נסגרה';
      default: return s;
    }
  }

  private tokenFor(id: string): string | undefined {
    return this.support.anonTickets().find(t => t.id === id)?.token;
  }

  /**
   * Signed in, the server finds the threads by session email. Anonymous, they
   * are fetched one by one with the tokens this browser kept — a ticket opened
   * elsewhere is simply not listed, which is the point of the token.
   */
  private async loadTickets(): Promise<void> {
    if (this.signedIn) {
      try {
        this.tickets = await this.support.myTickets();
      } catch {
        this.tickets = [];
      }
      return;
    }

    const stored = this.support.anonTickets();
    const loaded = await Promise.all(
      stored.map(s => this.support.getTicket(s.id, s.token).catch(() => null)),
    );
    // A ticket that has expired server-side drops out rather than erroring.
    this.tickets = loaded.filter((t): t is SupportTicket => t !== null);
  }

  private errorText(err: any): string {
    const text = typeof err?.error === 'string' && err.error ? err.error : '';
    switch (err?.status) {
      case 429: return 'נשלחו יותר מדי פניות בזמן קצר. נסה שוב בעוד מספר דקות.';
      case 400: return text || 'הפרטים שהוזנו אינם תקינים.';
      case 409: return 'הפנייה נסגרה ולא ניתן להוסיף לה הודעות.';
      case 404: return 'הפנייה לא נמצאה.';
      default: return 'שגיאה בשליחה. נסה שוב.';
    }
  }
}
