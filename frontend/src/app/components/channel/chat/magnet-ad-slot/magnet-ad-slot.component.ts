import {
  AfterViewInit,
  Component,
  ElementRef,
  Input,
  NgZone,
  OnDestroy,
  ViewChild,
} from '@angular/core';
import { NbUserModule } from '@nebular/theme';
import { MagnetAdsService } from '../../../../services/magnet-ads.service';
import { ChatService } from '../../../../services/chat.service';

/**
 * The frame's content script posts this after every layout change so the
 * host can size the frame to the ad. Its origin is opaque ("null"), so the
 * host matches the message by its source window, never by origin.
 */
const AD_SIZE_MESSAGE = 'magnet-ad-size';

/** Height the frame starts at, so an ad script sees a viewport to render into. */
const INITIAL_FRAME_HEIGHT = 250;

/** Time the snippet gets to render something before the slot is collapsed. */
const EMPTY_SLOT_GRACE_MS = 5000;

@Component({
  selector: 'app-magnet-ad-slot',
  standalone: true,
  imports: [NbUserModule],
  templateUrl: './magnet-ad-slot.component.html',
  styleUrl: './magnet-ad-slot.component.scss',
})
export class MagnetAdSlotComponent implements AfterViewInit, OnDestroy {
  @Input() slotKey: string = '';

  @ViewChild('host', { static: true }) hostRef!: ElementRef<HTMLDivElement>;

  private observer?: IntersectionObserver;
  private rendered = false;
  private collapseTimer?: any;
  private frame?: HTMLIFrameElement;
  private reportedHeight: number | null = null;
  private readonly onMessage = (event: MessageEvent) => this.handleMessage(event);
  collapsed = false;

  constructor(
    private magnet: MagnetAdsService,
    public chatService: ChatService,
    private zone: NgZone,
  ) {}

  ngAfterViewInit(): void {
    if (typeof IntersectionObserver === 'undefined') {
      this.render();
      return;
    }

    this.observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting && !this.rendered) {
            this.render();
            this.observer?.disconnect();
            break;
          }
        }
      },
      { rootMargin: '200px 0px' },
    );
    this.observer.observe(this.hostRef.nativeElement);
  }

  ngOnDestroy(): void {
    this.observer?.disconnect();
    if (this.collapseTimer) clearTimeout(this.collapseTimer);
    window.removeEventListener('message', this.onMessage);
  }

  private render(): void {
    if (this.rendered) return;
    this.rendered = true;

    const settings = this.magnet.getSettings();
    const snippet = settings?.snippet?.trim();
    if (!snippet) {
      this.collapse();
      return;
    }

    const host = this.hostRef.nativeElement;
    host.innerHTML = '';

    // The snippet is channel-owner content, and any signed-in user can own a
    // channel. It used to be injected into this document with its scripts
    // re-created, so it ran as first-party code on the app's origin, with
    // every viewer's session: an owner could read the super admin's global
    // settings or grant themselves roles when the super admin opened the
    // channel. A sandboxed srcdoc frame (no allow-same-origin) gives the
    // snippet an opaque origin: its script still runs and its ad still
    // renders, but it cannot reach this document, the session cookie or
    // the API.
    const frame = document.createElement('iframe');
    frame.className = 'magnet-ad-frame';
    frame.setAttribute('sandbox', 'allow-scripts allow-popups allow-popups-to-escape-sandbox allow-top-navigation-by-user-activation');
    frame.setAttribute('referrerpolicy', 'strict-origin-when-cross-origin');
    frame.setAttribute('title', 'פרסומת');
    frame.setAttribute('scrolling', 'no');
    frame.style.width = '100%';
    frame.style.height = `${INITIAL_FRAME_HEIGHT}px`;
    frame.style.border = '0';
    frame.style.display = 'block';
    frame.srcdoc = this.frameDocument(snippet);

    this.frame = frame;
    window.addEventListener('message', this.onMessage);
    host.appendChild(frame);

    this.collapseTimer = setTimeout(() => this.collapseIfEmpty(), EMPTY_SLOT_GRACE_MS);
  }

  /**
   * The document the frame runs. The base href keeps relative URLs in the
   * snippet resolving against this site, as they did inline, and target
   * _blank sends ad clicks to a new tab, since the sandbox blocks
   * navigating this one without a user gesture. The trailing script
   * reports the rendered height so the host can size the frame.
   */
  private frameDocument(snippet: string): string {
    const base = `${location.origin}/`;
    return `<!doctype html><html lang="he" dir="rtl"><head><meta charset="utf-8">` +
      `<base href="${base}" target="_blank">` +
      `<style>html,body{margin:0;padding:0;background:transparent;overflow:hidden}</style>` +
      `</head><body>${snippet}` +
      `<script>(function(){` +
      `var last=-1;` +
      `function h(){var b=document.body;return Math.ceil(Math.max(b?b.scrollHeight:0,b?b.offsetHeight:0));}` +
      `function report(){var v=h();if(v!==last){last=v;parent.postMessage({type:${JSON.stringify(AD_SIZE_MESSAGE)},height:v},'*');}}` +
      `if(window.ResizeObserver){var ro=new ResizeObserver(report);ro.observe(document.documentElement);if(document.body)ro.observe(document.body);}` +
      `new MutationObserver(report).observe(document.documentElement,{childList:true,subtree:true,attributes:true});` +
      `window.addEventListener('load',report);setInterval(report,1000);report();` +
      `})();</script></body></html>`;
  }

  private handleMessage(event: MessageEvent): void {
    // Only this slot's own frame may size it. The frame's origin is "null",
    // so the source window is the only reliable identity.
    if (!this.frame || event.source !== this.frame.contentWindow) return;
    const data = event.data;
    if (!data || data.type !== AD_SIZE_MESSAGE) return;
    const height = Number(data.height);
    if (!Number.isFinite(height) || height < 0) return;

    this.reportedHeight = height;
    if (height > 0) {
      // Cap what an ad may grow to: the slot sits inside the chat, not over it.
      this.frame.style.height = `${Math.min(height, 1200)}px`;
    }
  }

  private collapseIfEmpty(): void {
    // No report yet, or a reported height of zero: the snippet rendered
    // nothing visible, so the slot disappears instead of leaving a gap.
    if (!this.reportedHeight) {
      this.zone.run(() => this.collapse());
    }
  }

  private collapse(): void {
    this.collapsed = true;
    window.removeEventListener('message', this.onMessage);
    this.frame = undefined;
    const host = this.hostRef.nativeElement;
    host.innerHTML = '';
  }
}
