import { Component, OnInit } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Title } from '@angular/platform-browser';
import { RouterLink } from '@angular/router';
import { FormsModule } from '@angular/forms';
import { firstValueFrom } from 'rxjs';
import {
  NbButtonModule, NbIconModule, NbInputModule, NbLayoutModule, NbSpinnerModule,
} from '@nebular/theme';

/** One card, as GET /api/public/channels returns it (backend/public_channels.go). */
export interface PublicChannelCard {
  slug: string;
  name: string;
  description: string;
  logoUrl: string;
}

/**
 * "ערוצים ציבוריים": the channels whose managers ticked "הצגה ברשימת הערוצים
 * הציבוריים" in their channel's details, each with a link straight into it.
 * Part of the public site, next to the landing page, so it shares its top
 * bar, sections and footer (landing-page.component.scss).
 */
@Component({
  selector: 'app-public-channels',
  standalone: true,
  imports: [RouterLink, FormsModule, NbButtonModule, NbIconModule, NbInputModule, NbLayoutModule, NbSpinnerModule],
  styleUrls: ['../landing/landing-page.component.scss', './public-channels.component.scss'],
  template: `
    <nb-layout>
      <nb-layout-column>
    <nav class="topbar" aria-label="ניווט ראשי">
      <a class="topbar__brand topbar__home" [routerLink]="['/']">
        <span class="brand-mark" aria-hidden="true"><nb-icon aria-hidden="true" icon="radio-outline"></nb-icon></span>
        {{ siteName }}
      </a>
      <span class="topbar__links">
        <a nbButton ghost status="basic" size="small" [routerLink]="['/']">דף הבית</a>
        <a nbButton ghost status="primary" size="small" [routerLink]="['/login']">
          <nb-icon aria-hidden="true" icon="log-in-outline"></nb-icon>
          התחברות
        </a>
      </span>
    </nav>

    <header class="dir-hero">
      <div class="wrap">
        <span class="dir-hero__icon" aria-hidden="true"><nb-icon icon="compass-outline"></nb-icon></span>
        <h1 class="dir-hero__title">ערוצים ציבוריים</h1>
        <p class="dir-hero__lead">
          ערוצי עדכונים פתוחים לכולם: בוחרים ערוץ ונכנסים לקרוא, בלי אפליקציה ובלי הרשמה.
        </p>
        @if (channels.length > 6) {
          <div class="dir-search">
            <nb-icon icon="search-outline" aria-hidden="true" class="dir-search__icon"></nb-icon>
            <input nbInput fullWidth type="search" [(ngModel)]="query" placeholder="חיפוש ערוץ לפי שם או תיאור"
                   aria-label="חיפוש ערוץ" autocomplete="off">
          </div>
        }
      </div>
    </header>

    <section class="section dir-list" aria-label="רשימת הערוצים">
      <div class="wrap">
        @if (loading) {
          <div class="dir-state" [nbSpinner]="true" nbSpinnerStatus="primary" nbSpinnerMessage="טוענים את הערוצים…"></div>
        } @else if (failed) {
          <div class="dir-state">
            <p>לא הצלחנו לטעון את רשימת הערוצים.</p>
            <button nbButton status="primary" size="small" type="button" (click)="load()">
              <nb-icon icon="refresh-outline" aria-hidden="true"></nb-icon>
              לנסות שוב
            </button>
          </div>
        } @else if (!channels.length) {
          <div class="dir-state">
            <p>עדיין אין כאן ערוצים. בעלי ערוצים יכולים להוסיף את הערוץ שלהם מתוך "פרטי הערוץ" בניהול.</p>
          </div>
        } @else if (!filtered.length) {
          <div class="dir-state"><p>לא נמצא ערוץ שמתאים לחיפוש.</p></div>
        } @else {
          <ul class="dir-grid">
            @for (ch of filtered; track ch.slug) {
              <li class="dir-card">
                <a class="dir-card__link" [routerLink]="['/channel', ch.slug]" [attr.aria-label]="'כניסה לערוץ ' + ch.name">
                  <span class="dir-card__logo" aria-hidden="true">
                    @if (ch.logoUrl && !brokenLogos.has(ch.slug)) {
                      <img [src]="ch.logoUrl" alt="" width="56" height="56" loading="lazy" (error)="brokenLogos.add(ch.slug)">
                    } @else {
                      {{ initialOf(ch) }}
                    }
                  </span>
                  <span class="dir-card__body">
                    <span class="dir-card__name">{{ ch.name }}</span>
                    @if (ch.description) {
                      <span class="dir-card__desc">{{ ch.description }}</span>
                    }
                  </span>
                  <span class="dir-card__cta">
                    כניסה לערוץ
                    <nb-icon icon="arrow-back-outline" aria-hidden="true"></nb-icon>
                  </span>
                </a>
              </li>
            }
          </ul>
        }
      </div>
    </section>

    <section class="section section--alt cta" aria-labelledby="dir-cta-title">
      <div class="wrap wrap--narrow cta__inner">
        <h2 class="section__title" id="dir-cta-title">רוצים שגם הערוץ שלכם יופיע כאן?</h2>
        <p class="cta__text">
          פותחים ערוץ בחינם, ובמסך "פרטי הערוץ" מסמנים "הצגה ברשימת הערוצים הציבוריים".
        </p>
        <a nbButton status="primary" size="large" class="cta__button" [routerLink]="['/']">
          <nb-icon aria-hidden="true" icon="flash-outline"></nb-icon>
          לפתיחת ערוץ
        </a>
      </div>
    </section>

    <footer class="footer">
      <span class="footer__brand">{{ siteName }}</span>
      <span class="footer__sep" aria-hidden="true">·</span>
      <a class="footer__link" [routerLink]="['/']">דף הבית</a>
      <span class="footer__sep" aria-hidden="true">·</span>
      <a class="footer__link" href="/privacy">מדיניות פרטיות</a>
      <span class="footer__sep" aria-hidden="true">·</span>
      <a class="footer__link" href="/terms">תנאי שימוש</a>
    </footer>
      </nb-layout-column>
    </nb-layout>
  `,
})
export class PublicChannelsComponent implements OnInit {
  readonly siteName =
    document.querySelector('meta[name="application-name"]')?.getAttribute('content') || 'הערוץ';

  channels: PublicChannelCard[] = [];
  loading = true;
  failed = false;
  query = '';
  /** Cards whose logo failed to load fall back to the initial. */
  readonly brokenLogos = new Set<string>();

  constructor(private http: HttpClient, private titleService: Title) {}

  ngOnInit(): void {
    // The same title the server sends for /channels (backend/public_channels.go).
    this.titleService.setTitle(`ערוצים ציבוריים · ${this.siteName}`);
    this.load();
  }

  async load(): Promise<void> {
    this.loading = true;
    this.failed = false;
    try {
      const res = await firstValueFrom(this.http.get<{ channels: PublicChannelCard[] }>('/api/public/channels'));
      this.channels = Array.isArray(res?.channels) ? res.channels : [];
    } catch {
      this.failed = true;
    } finally {
      this.loading = false;
    }
  }

  get filtered(): PublicChannelCard[] {
    const q = this.query.trim().toLowerCase();
    if (!q) return this.channels;
    return this.channels.filter(c =>
      c.name.toLowerCase().includes(q) || (c.description || '').toLowerCase().includes(q));
  }

  initialOf(ch: PublicChannelCard): string {
    return (ch.name || ch.slug || '?').trim().charAt(0).toUpperCase();
  }
}
