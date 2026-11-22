import { DOCUMENT } from '@angular/common';
import { Inject, Injectable } from '@angular/core';

/**
 * The tab icon. On a channel page it is the channel's profile picture, served
 * by GET /favicon.ico?slug=<slug> (backend/files.go getFavicon), which falls
 * back to the platform icon itself when the channel has no picture or may not
 * show it anonymously. The server already writes that link into the page it
 * sends for /channel/<slug> (backend/seo.go); this keeps it right when the
 * visitor moves between channels inside the app, which reloads nothing.
 */
@Injectable({ providedIn: 'root' })
export class FaviconService {
  private static readonly DEFAULT_ICO = '/assets/favicon.ico';
  private static readonly DEFAULT_SVG = '/assets/icon.svg';
  private static readonly DEFAULT_TOUCH = '/assets/apple-touch-icon.png';

  constructor(@Inject(DOCUMENT) private doc: Document) { }

  /**
   * Shows this channel's picture. `logoUrl` only versions the address, so a
   * new picture is fetched instead of the browser's cached copy of the old.
   */
  setChannel(slug: string, logoUrl?: string): void {
    if (!slug) {
      this.reset();
      return;
    }
    let href = '/favicon.ico?slug=' + encodeURIComponent(slug);
    const version = (logoUrl || '').split('/').pop()?.split('?')[0] || '';
    if (version) href += '&v=' + encodeURIComponent(version.slice(0, 64));
    this.apply(href, null, href);
  }

  /** Back to the platform icon (leaving a channel). */
  reset(): void {
    this.apply(FaviconService.DEFAULT_ICO, FaviconService.DEFAULT_SVG, FaviconService.DEFAULT_TOUCH);
  }

  /**
   * Browsers prefer an SVG icon over any other, so on a channel the SVG link
   * is removed (not just left at the default) and restored on the way out.
   */
  private apply(ico: string, svg: string | null, touch: string): void {
    const head = this.doc.head;
    if (!head) return;
    this.link('link[rel="icon"]:not([type="image/svg+xml"])', 'icon', ico);
    const svgLink = head.querySelector<HTMLLinkElement>('link[rel="icon"][type="image/svg+xml"]');
    if (svg) {
      if (svgLink) svgLink.href = svg;
      else this.link('link[rel="icon"][type="image/svg+xml"]', 'icon', svg, 'image/svg+xml');
    } else {
      svgLink?.remove();
    }
    this.link('link[rel="apple-touch-icon"]', 'apple-touch-icon', touch);
  }

  private link(selector: string, rel: string, href: string, type?: string): void {
    let el = this.doc.head.querySelector<HTMLLinkElement>(selector);
    if (!el) {
      el = this.doc.createElement('link');
      el.rel = rel;
      if (type) el.type = type;
      this.doc.head.appendChild(el);
    }
    // The server-written channel icon is typed image/x-icon; the picture may
    // be a PNG or JPEG, and a wrong type makes some browsers skip the link.
    if (!type) el.removeAttribute('type');
    if (el.getAttribute('href') !== href) el.setAttribute('href', href);
  }
}
