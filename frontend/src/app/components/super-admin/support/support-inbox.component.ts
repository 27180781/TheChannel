import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbAlertModule, NbButtonModule, NbCardModule, NbIconModule,
  NbInputModule, NbSelectModule, NbToastrService,
} from '@nebular/theme';
import {
  SupportService, SupportStatus, SupportTicket,
} from '../../../services/support.service';

/**
 * The operator's inbox: every ticket from every channel, plus the ones sent
 * from the public landing page by people with no account at all.
 *
 * Ordered by last activity, so a thread the requester just added to comes back
 * to the top rather than staying buried at its creation time.
 */
@Component({
  selector: 'app-support-inbox',
  standalone: true,
  imports: [
    CommonModule, FormsModule, NbCardModule, NbButtonModule,
    NbInputModule, NbIconModule, NbAlertModule, NbSelectModule,
  ],
  template: `
    <nb-card>
      <nb-card-header class="d-flex justify-content-between align-items-center">
        <h5 class="mb-0">
          פניות למערכת
          @if (openCount) { <span class="badge-open">{{ openCount }} ממתינות</span> }
        </h5>
        <div class="filters">
          @for (f of filters; track f.value) {
            <button nbButton size="tiny" [status]="filter === f.value ? 'primary' : 'basic'"
                    (click)="filter = f.value">{{ f.label }}</button>
          }
          <button nbButton size="tiny" status="basic" (click)="reload()" [disabled]="loading">
            <nb-icon icon="refresh-outline"></nb-icon>
          </button>
        </div>
      </nb-card-header>

      <nb-card-body>
        @if (loading) {
          <p class="text-center">טוען...</p>
        } @else if (visible().length === 0) {
          <p class="text-center text-muted">אין פניות להצגה</p>
        } @else {
          @for (t of visible(); track t.id) {
            <div class="ticket">
              <div class="ticket__head" (click)="toggle(t.id)">
                <div class="ticket__main">
                  <span class="ticket__subject">{{ t.subject }}</span>
                  <span class="ticket__from">
                    {{ t.name }} · {{ t.email }}
                    @if (!t.authenticated) { <span class="tag tag--anon">אורח</span> }
                    @if (t.channelSlug) { <span class="tag">{{ t.channelSlug }}</span> }
                  </span>
                </div>
                <div class="ticket__side">
                  <span class="ticket__status" [class]="'st-' + t.status">{{ statusText(t.status) }}</span>
                  <span class="ticket__date">{{ t.updatedAt | date:'dd/MM HH:mm' }}</span>
                </div>
              </div>

              @if (openId === t.id) {
                <div class="thread">
                  @for (m of t.messages; track $index) {
                    <div class="msg" [class.msg--admin]="m.author === 'admin'">
                      <div class="msg__meta">
                        {{ m.authorName }} · {{ m.createdAt | date:'dd/MM/yyyy HH:mm' }}
                      </div>
                      <div class="msg__body">{{ m.body }}</div>
                    </div>
                  }

                  <textarea nbInput fullWidth rows="4" [(ngModel)]="replyBody"
                            [ngModelOptions]="{standalone: true}"
                            placeholder="תשובה לפונה" maxlength="5000" class="mt-2"></textarea>

                  <div class="actions mt-2">
                    <button nbButton size="small" status="primary"
                            [disabled]="busy" (click)="reply(t)">שליחת תשובה</button>
                    @if (t.status !== 'closed') {
                      <button nbButton size="small" status="basic"
                              [disabled]="busy" (click)="setStatus(t, 'closed')">סגירת פנייה</button>
                    } @else {
                      <button nbButton size="small" status="basic"
                              [disabled]="busy" (click)="setStatus(t, 'open')">פתיחה מחדש</button>
                    }
                  </div>

                  @if (!t.authenticated) {
                    <p class="hint mt-2 mb-0">
                      הפנייה נשלחה ללא התחברות. התשובה תופיע לפונה בדפדפן שממנו נשלחה.
                    </p>
                  }
                </div>
              }
            </div>
          }
        }
      </nb-card-body>
    </nb-card>
  `,
  styles: [`
    .filters { display: flex; gap: 0.25rem; flex-wrap: wrap; }
    .badge-open { font-size: 0.72rem; margin-inline-start: 0.5rem; padding: 0.1rem 0.5rem;
                  border-radius: 1rem; background: var(--color-warning-transparent-200, #fff3cd);
                  color: var(--color-warning-700, #8a6d3b); }

    .ticket { border-bottom: 1px solid var(--divider-color); padding: 0.6rem 0; }
    .ticket:last-child { border-bottom: 0; }
    .ticket__head { display: flex; justify-content: space-between; gap: 0.75rem;
                    cursor: pointer; align-items: flex-start; }
    .ticket__main { display: flex; flex-direction: column; min-width: 0; }
    .ticket__subject { font-weight: 600; }
    .ticket__from { font-size: 0.75rem; color: var(--text-hint-color, #8f9bb3);
                    overflow-wrap: anywhere; }
    .ticket__side { display: flex; flex-direction: column; align-items: flex-end; gap: 0.2rem;
                    white-space: nowrap; }
    .ticket__date { font-size: 0.7rem; color: var(--text-hint-color, #8f9bb3); }
    .ticket__status { font-size: 0.72rem; padding: 0.1rem 0.5rem; border-radius: 1rem; }

    .tag { font-size: 0.68rem; padding: 0 0.35rem; border-radius: 0.25rem;
           background: var(--background-basic-color-3, #e9ecef); margin-inline-start: 0.3rem; }
    .tag--anon { background: var(--color-info-transparent-200, #cff4fc); }

    .st-open { background: var(--color-warning-transparent-200, #fff3cd); color: var(--color-warning-700, #8a6d3b); }
    .st-answered { background: var(--color-success-transparent-200, #d1e7dd); color: var(--color-success-700, #0a3622); }
    .st-closed { background: var(--background-basic-color-3, #e9ecef); color: var(--text-hint-color, #6c757d); }

    .thread { margin-top: 0.5rem; }
    .msg { padding: 0.5rem 0.75rem; border-radius: 0.5rem; margin-bottom: 0.4rem;
           background: var(--background-basic-color-2, #f7f9fc); }
    .msg--admin { background: var(--color-primary-transparent-100, #edf3ff);
                  border-inline-start: 3px solid var(--color-primary-default, #3366ff); }
    .msg__meta { font-size: 0.72rem; color: var(--text-hint-color, #8f9bb3); margin-bottom: 0.2rem; }
    /* Plain text via interpolation, never markdown: a ticket body must not be
       able to inject markup into this panel. */
    .msg__body { white-space: pre-wrap; word-break: break-word; }
    .actions { display: flex; gap: 0.5rem; flex-wrap: wrap; }
    .hint { font-size: 0.75rem; color: var(--text-hint-color, #8f9bb3); }
  `],
})
export class SupportInboxComponent implements OnInit {
  tickets: SupportTicket[] = [];
  loading = true;
  busy = false;
  openId = '';
  replyBody = '';

  filter: 'all' | SupportStatus = 'open';
  readonly filters: { value: 'all' | SupportStatus; label: string }[] = [
    { value: 'open', label: 'ממתינות' },
    { value: 'answered', label: 'נענו' },
    { value: 'closed', label: 'סגורות' },
    { value: 'all', label: 'הכל' },
  ];

  constructor(
    private support: SupportService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.reload();
  }

  async reload(): Promise<void> {
    this.loading = true;
    try {
      this.tickets = await this.support.adminList();
    } catch {
      this.tickets = [];
      this.toastr.danger('', 'שגיאה בטעינת הפניות');
    } finally {
      this.loading = false;
    }
  }

  visible(): SupportTicket[] {
    return this.filter === 'all'
      ? this.tickets
      : this.tickets.filter(t => t.status === this.filter);
  }

  get openCount(): number {
    return this.tickets.filter(t => t.status === 'open').length;
  }

  toggle(id: string): void {
    this.openId = this.openId === id ? '' : id;
    this.replyBody = '';
  }

  async reply(t: SupportTicket): Promise<void> {
    const body = this.replyBody.trim();
    if (!body) return;
    this.busy = true;
    try {
      this.replace(await this.support.adminReply(t.id, body));
      this.replyBody = '';
      this.toastr.success('', 'התשובה נשלחה');
    } catch {
      this.toastr.danger('', 'שגיאה בשליחת התשובה');
    } finally {
      this.busy = false;
    }
  }

  async setStatus(t: SupportTicket, status: SupportStatus): Promise<void> {
    this.busy = true;
    try {
      this.replace(await this.support.adminSetStatus(t.id, status));
    } catch {
      this.toastr.danger('', 'שגיאה בעדכון הסטטוס');
    } finally {
      this.busy = false;
    }
  }

  statusText(s: string): string {
    switch (s) {
      case 'open': return 'ממתינה למענה';
      case 'answered': return 'נענתה';
      case 'closed': return 'סגורה';
      default: return s;
    }
  }

  /** Swap the server's updated copy in without reloading the whole inbox. */
  private replace(updated: SupportTicket): void {
    const i = this.tickets.findIndex(x => x.id === updated.id);
    if (i >= 0) this.tickets[i] = updated;
  }
}
