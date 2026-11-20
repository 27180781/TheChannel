import { Component, OnDestroy, OnInit } from '@angular/core';
import { Router } from '@angular/router';
import { NbButtonModule, NbIconModule, NbToastrService } from '@nebular/theme';
import { Subscription } from 'rxjs';
import { ActivityService, Celebration } from '../../services/activity.service';
import { ShareService } from '../../services/share.service';

/** How long a celebration stays up unless the user closes it or acts on it. */
const SHOW_MS = 9000;

/**
 * The "הידד!" card: shown over any screen when the channel reaches something
 * worth cheering (first post, first readers, a new teammate). One host, in
 * app.component, fed by ActivityService.celebration$. Polite live region, so
 * a screen reader hears it without losing focus; the confetti is decoration
 * and stops for anyone who asked the system for less motion.
 */
@Component({
  selector: 'app-celebration',
  standalone: true,
  imports: [NbButtonModule, NbIconModule],
  template: `
    <div class="celebrate-live" aria-live="polite">
      @if (current; as c) {
        <div class="celebrate" role="status">
          <div class="celebrate__confetti" aria-hidden="true">
            @for (p of pieces; track $index) {
              <i [style.--x]="p.x" [style.--d]="p.d" [style.--c]="p.c" [style.--r]="p.r"></i>
            }
          </div>
          <button type="button" class="celebrate__close" (click)="close()" aria-label="סגירה">
            <nb-icon icon="close-outline"></nb-icon>
          </button>
          @if (c.emoji) { <div class="celebrate__emoji" aria-hidden="true">{{ c.emoji }}</div> }
          <h2 class="celebrate__title">{{ c.title }}</h2>
          <p class="celebrate__text">{{ c.text }}</p>
          @if (c.action; as a) {
            <button nbButton status="primary" size="small" type="button" (click)="act(c)">
              @if (a.icon) { <nb-icon [icon]="a.icon"></nb-icon> }
              {{ a.label }}
            </button>
          }
        </div>
      }
    </div>
  `,
  styles: [`
    :host { position: fixed; inset-inline: 0; top: calc(var(--chrome-height, 64px) + 1rem); z-index: 1100; pointer-events: none; display: flex; justify-content: center; padding: 0 1rem; }
    .celebrate-live { width: 100%; max-width: 420px; }
    .celebrate {
      pointer-events: auto; position: relative; overflow: hidden; text-align: center;
      background: var(--background-basic-color-1); color: var(--text-basic-color);
      border: 1px solid var(--color-primary-300); border-radius: 18px;
      box-shadow: 0 18px 48px -18px rgba(29, 79, 215, .45), 0 2px 6px rgba(0, 0, 0, .08);
      padding: 1.4rem 1.25rem 1.2rem; animation: pop .45s cubic-bezier(.2, 1.4, .4, 1) both;
    }
    .celebrate__emoji { font-size: 2.6rem; line-height: 1; margin-bottom: .5rem; animation: bounce 1.2s ease-in-out .3s 2; }
    .celebrate__title { font-size: 1.25rem; font-weight: 800; margin: 0 0 .35rem; color: var(--color-primary-600); }
    .celebrate__text { margin: 0 0 1rem; font-size: .95rem; line-height: 1.55; color: var(--text-hint-color); }
    .celebrate__text:last-child { margin-bottom: 0; }
    .celebrate__close { position: absolute; top: .5rem; inset-inline-end: .5rem; background: none; border: 0; padding: .25rem; cursor: pointer; color: var(--text-hint-color); border-radius: 50%; z-index: 1; }
    .celebrate__close:hover { background: var(--background-basic-color-2); }
    .celebrate__confetti { position: absolute; inset: 0; pointer-events: none; }
    .celebrate__confetti i {
      position: absolute; top: -12px; left: var(--x); width: 8px; height: 12px; border-radius: 2px;
      background: var(--c); transform: rotate(var(--r)); opacity: 0;
      animation: fall 1.8s ease-in var(--d) forwards;
    }
    @keyframes pop { from { opacity: 0; transform: translateY(-14px) scale(.94); } to { opacity: 1; transform: none; } }
    @keyframes bounce { 0%, 100% { transform: none; } 40% { transform: translateY(-8px) scale(1.08); } }
    @keyframes fall { 0% { opacity: 1; transform: translateY(0) rotate(var(--r)); } 100% { opacity: 0; transform: translateY(220px) rotate(calc(var(--r) + 300deg)); } }
    @media (prefers-reduced-motion: reduce) {
      .celebrate, .celebrate__emoji { animation: none; }
      .celebrate__confetti { display: none; }
    }
  `],
})
export class CelebrationComponent implements OnInit, OnDestroy {
  current?: Celebration;
  pieces: { x: string; d: string; c: string; r: string }[] = [];

  private sub?: Subscription;
  private timer?: ReturnType<typeof setTimeout>;

  constructor(
    private activity: ActivityService,
    private share: ShareService,
    private router: Router,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.sub = this.activity.celebration$.subscribe(c => this.show(c));
  }

  ngOnDestroy(): void {
    this.sub?.unsubscribe();
    clearTimeout(this.timer);
  }

  close(): void {
    clearTimeout(this.timer);
    this.current = undefined;
  }

  async act(c: Celebration): Promise<void> {
    const a = c.action;
    if (!a) return;
    if (a.kind === 'link' && a.route) {
      this.close();
      this.router.navigate(a.route);
      return;
    }
    if (a.kind === 'share' && c.slug) {
      const url = this.share.channelUrl(c.slug);
      const text = this.share.inviteText(c.channelName ?? '', url);
      const outcome = await this.share.share({ title: c.channelName, text, url });
      if (outcome === 'unsupported' && await this.share.copy(url)) {
        this.toastr.success('', 'הקישור הועתק — אפשר להדביק אותו בכל מקום');
      }
      this.close();
    }
  }

  private show(c: Celebration): void {
    clearTimeout(this.timer);
    const colors = ['#1d4fd7', '#f5b301', '#22c55e', '#ef4444', '#a855f7', '#06b6d4'];
    this.pieces = Array.from({ length: 26 }, (_, i) => ({
      x: `${(i * 37) % 100}%`,
      d: `${(i % 9) * 0.07}s`,
      c: colors[i % colors.length],
      r: `${(i * 53) % 180}deg`,
    }));
    this.current = c;
    this.timer = setTimeout(() => this.close(), SHOW_MS);
  }
}
