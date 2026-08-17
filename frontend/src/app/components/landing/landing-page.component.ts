import { Component, OnInit } from '@angular/core';
import { RouterLink, Router } from '@angular/router';
import {
  NbCardModule, NbButtonModule, NbIconModule, NbLayoutModule,
} from '@nebular/theme';
import { AuthService } from '../../services/auth.service';
import { CreateChannelFormComponent } from '../channel-create/create-channel-form.component';

interface LandingStep {
  icon: string;
  title: string;
  text: string;
}

interface LandingFeature {
  icon: string;
  title: string;
  text: string;
}

@Component({
  selector: 'app-landing-page',
  standalone: true,
  imports: [
    RouterLink,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbLayoutModule,
    CreateChannelFormComponent,
  ],
  styleUrl: './landing-page.component.scss',
  template: `
    <!-- nb-layout must wrap the page (and sit outside the isChecking guard) —
         it is what applies the nb-theme-* class that every Nebular CSS variable
         hangs off. Without it the whole page renders unstyled. -->
    <nb-layout>
      <nb-layout-column>
    @if (!isChecking) {
    <nav class="topbar">
      <span class="topbar__brand">TheChannel</span>
      <a nbButton status="primary" size="small" [routerLink]="['/login']">התחברות</a>
    </nav>

    <!-- Hero -->
    <section class="hero">
      <div class="hero__inner">
        <span class="hero__badge">
          <nb-icon icon="flash-outline"></nb-icon>
          פתיחה מיידית — בלי המתנה לאישור
        </span>
        <h1 class="hero__title">הערוץ שלכם באוויר תוך שנייה</h1>
        <p class="hero__lead">
          ערוץ תוכן משלכם בעברית: שידור הודעות, תמונות וקבצים לקהל שלכם,
          עם פאנל ניהול פשוט, סטטיסטיקות והתראות — הכול תחת כתובת אישית משלכם.
        </p>
        <div class="hero__actions">
          <button nbButton status="primary" size="large" class="hero__cta" (click)="startCreate()">
            <nb-icon icon="plus-circle-outline"></nb-icon>
            פתחו ערוץ עכשיו
          </button>
          <a nbButton ghost status="control" size="large" href="#request" (click)="scrollToForm($event)">
            מעדיפים לא להתחבר?
          </a>
        </div>
        <p class="hero__note">ההתחברות נדרשת רק כדי שהערוץ יירשם על שמכם. אין עלות.</p>
      </div>
    </section>

    <!-- How it works -->
    <section class="section">
      <div class="wrap">
        <h2 class="section__title">שלושה צעדים, דקה אחת</h2>
        <div class="steps">
          @for (step of steps; track step.title; let i = $index) {
            <div class="step">
              <span class="step__num">{{ i + 1 }}</span>
              <nb-icon [icon]="step.icon" class="step__icon"></nb-icon>
              <h3 class="step__title">{{ step.title }}</h3>
              <p class="step__text">{{ step.text }}</p>
            </div>
          }
        </div>
      </div>
    </section>

    <!-- What you get -->
    <section class="section section--alt">
      <div class="wrap">
        <h2 class="section__title">מה מקבלים?</h2>
        <div class="features">
          @for (feature of features; track feature.title) {
            <div class="feature">
              <nb-icon [icon]="feature.icon" class="feature__icon"></nb-icon>
              <div>
                <h3 class="feature__title">{{ feature.title }}</h3>
                <p class="feature__text">{{ feature.text }}</p>
              </div>
            </div>
          }
        </div>
      </div>
    </section>

    <!-- Secondary path: request a channel without signing in -->
    <section class="section" id="request">
      <div class="wrap wrap--narrow">
        <div class="request-head">
          <h2 class="section__title">אין לכם חשבון גוגל?</h2>
          <p class="request-head__text">
            אפשר גם להגיש בקשה ידנית. הבקשה נבדקת על ידי צוות המערכת ולוקחת עד 24 שעות —
            הדרך המהירה היא <button type="button" class="linklike" (click)="startCreate()">התחברות ופתיחה מיידית</button>.
          </p>
        </div>
        <app-create-channel-form
          variant="request"
          title="בקשה לפתיחת ערוץ"
          subtitle="נשיב לכם במייל לאחר בדיקת הבקשה">
        </app-create-channel-form>
      </div>
    </section>

    <footer class="footer">
      <span>TheChannel &copy; 2024</span>
    </footer>
    } <!-- end @if (!isChecking) -->
      </nb-layout-column>
    </nb-layout>
  `,
})
export class LandingPageComponent implements OnInit {
  isChecking = true;

  readonly steps: LandingStep[] = [
    { icon: 'person-add-outline', title: 'מתחברים', text: 'התחברות מהירה עם חשבון גוגל — כדי שהערוץ יירשם על שמכם.' },
    { icon: 'edit-2-outline', title: 'בוחרים שם וכתובת', text: 'שם הערוץ והכתובת שלו, עם בדיקת זמינות בזמן אמת.' },
    { icon: 'flash-outline', title: 'משדרים', text: 'הערוץ נפתח מיד ואתם כבר בפנים. בלי בקשות ובלי המתנה לאישור.' },
  ];

  readonly features: LandingFeature[] = [
    { icon: 'brush-outline', title: 'עיצוב מותאם אישית', text: 'לוגו, שם ותיאור — הערוץ נראה שלכם.' },
    { icon: 'settings-2-outline', title: 'ניהול קל ופשוט', text: 'פאנל ניהול בעברית לכותבים, מנהלים והרשאות.' },
    { icon: 'hard-drive-outline', title: 'אחסון מדיה', text: 'העלאת תמונות, סרטונים וקבצים ישירות לערוץ.' },
    { icon: 'bell-outline', title: 'התראות לקוראים', text: 'התראות דחיפה לכל הודעה חדשה שתפרסמו.' },
    { icon: 'bar-chart-outline', title: 'סטטיסטיקות', text: 'צפיות, משתתפים ומעקב אחרי מה שעובד.' },
    { icon: 'globe-outline', title: 'ממשק בעברית', text: 'מבנה RTL מלא ותמיכה מושלמת בעברית.' },
  ];

  constructor(
    private authService: AuthService,
    private router: Router,
  ) {}

  async ngOnInit() {
    try {
      const user = await this.authService.loadUserInfo();
      if (user) {
        if (user.globalRole === 'super_admin') {
          this.router.navigate(['/super-admin']);
        } else {
          this.router.navigate(['/channel']);
        }
        return;
      }
    } catch {
      // Not logged in — show the landing page
    }
    this.isChecking = false;
  }

  /**
   * Instant creation needs a session (the backend takes the owner from it), so
   * the primary CTA sends visitors through login and back to /channel, where a
   * user with no channel lands on the creation form.
   * `returnUrl` lives in localStorage — that is what LoginComponent reads.
   */
  startCreate(): void {
    localStorage.setItem('returnUrl', '/channel');
    this.router.navigate(['/login'], { queryParams: { returnUrl: '/channel' } });
  }

  scrollToForm(event: Event) {
    event.preventDefault();
    document.getElementById('request')?.scrollIntoView({ behavior: 'smooth' });
  }
}
