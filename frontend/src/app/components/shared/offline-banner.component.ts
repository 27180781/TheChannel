import { Component, NgZone, OnDestroy, OnInit } from '@angular/core';
import { NbIconModule } from '@nebular/theme';

/** How long the "back online" confirmation stays before it fades away. */
const RECONNECTED_MS = 3000;

type State = 'online' | 'offline' | 'reconnected';

/**
 * A pill at the bottom of every screen (channel readers and the admin alike)
 * while the browser has no connection. Without it a dropped connection looked
 * like a frozen channel: new messages simply stopped arriving and a post or
 * reply failed with a generic error. When the connection returns it says so
 * for a moment, then leaves.
 *
 * Bottom-center so it never covers the header or the celebration card, and
 * not a toast because it must stay for as long as the outage lasts.
 * `navigator.onLine` only knows whether the device has a network at all, so a
 * captive Wi-Fi reads as online; a missed alert there is better than a false
 * "offline" on a working connection.
 */
@Component({
  selector: 'app-offline-banner',
  standalone: true,
  imports: [NbIconModule],
  template: `
    <div class="offline-live" role="status" aria-live="polite">
      @if (state !== 'online') {
        <div class="offline" [class.offline--back]="state === 'reconnected'">
          <nb-icon [icon]="state === 'offline' ? 'wifi-off-outline' : 'checkmark-circle-2-outline'" aria-hidden="true"></nb-icon>
          @if (state === 'offline') {
            <span><strong>אין חיבור לאינטרנט.</strong> הודעות חדשות יופיעו כשהחיבור יחזור.</span>
          } @else {
            <span>החיבור לאינטרנט חזר</span>
          }
        </div>
      }
    </div>
  `,
  styles: [`
    :host { position: fixed; inset-inline: 0; bottom: calc(1rem + env(safe-area-inset-bottom, 0px)); z-index: 1200; pointer-events: none; display: flex; justify-content: center; padding: 0 1rem; }
    .offline {
      pointer-events: auto; display: flex; align-items: center; gap: .6rem; max-width: 440px;
      padding: .7rem 1.2rem; border-radius: 999px; font-size: .92rem; line-height: 1.45;
      background: #1f2a44; color: #fff;
      box-shadow: 0 12px 32px -12px rgba(15, 23, 42, .55), 0 2px 6px rgba(0, 0, 0, .12);
      animation: rise .3s ease-out both;
    }
    .offline strong { font-weight: 800; }
    .offline nb-icon { flex-shrink: 0; font-size: 1.3rem; color: #fbbf24; }
    .offline--back { background: var(--color-success-600, #15803d); }
    .offline--back nb-icon { color: #fff; }
    @keyframes rise { from { opacity: 0; transform: translateY(12px); } to { opacity: 1; transform: none; } }
    @media (prefers-reduced-motion: reduce) { .offline { animation: none; } }
  `],
})
export class OfflineBannerComponent implements OnInit, OnDestroy {
  state: State = typeof navigator !== 'undefined' && navigator.onLine === false ? 'offline' : 'online';
  private timer?: ReturnType<typeof setTimeout>;

  private readonly goOffline = () => this.zone.run(() => {
    clearTimeout(this.timer);
    this.state = 'offline';
  });

  private readonly goOnline = () => this.zone.run(() => {
    clearTimeout(this.timer);
    // Only confirm a return when we had told the user it was gone.
    this.state = this.state === 'offline' ? 'reconnected' : 'online';
    this.timer = setTimeout(() => this.zone.run(() => (this.state = 'online')), RECONNECTED_MS);
  });

  constructor(private zone: NgZone) {}

  ngOnInit(): void {
    window.addEventListener('offline', this.goOffline);
    window.addEventListener('online', this.goOnline);
  }

  ngOnDestroy(): void {
    window.removeEventListener('offline', this.goOffline);
    window.removeEventListener('online', this.goOnline);
    clearTimeout(this.timer);
  }
}
