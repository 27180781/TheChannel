import { Component, OnDestroy, OnInit } from '@angular/core';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { Title } from '@angular/platform-browser';
import { NbButtonModule, NbCardModule, NbIconModule, NbLayoutModule, NbMenuItem, NbMenuModule } from '@nebular/theme';
import { Subscription } from 'rxjs';
import { SlugService } from '../../services/slug.service';
import { ChatService } from '../../services/chat.service';
import { AuthService } from '../../services/auth.service';
import { NotificationsService } from '../../services/notifications.service';
import { ChannelInfoFormComponent } from '../channel/channel-info-form/channel-info-form.component';
import { SettingsComponent } from '../admin/settings/settings.component';
import { PrivilegDashboardComponent } from '../admin/privileg-dashboard/privileg-dashboard.component';
import { StatisticsComponent } from '../admin/statistics/statistics.component';
import { EmojisComponent } from '../admin/emojis/emojis.component';
import { MagnetAdsComponent } from '../admin/magnet-ads/magnet-ads.component';
import { StorageComponent } from '../admin/storage/storage.component';
import { ReportsComponent } from '../admin/reports/reports.component';
import { GuideComponent } from '../admin/guide/guide.component';
import { SupportBoxComponent } from '../support/support-box.component';

type Access = 'moderator' | 'owner';

export interface ManageSection {
  /** URL segment: /channel/:slug/manage/:id */
  id: string;
  title: string;
  icon: string;
  access: Access;
  /** One sentence shown under the title, in the user's words. */
  description: string;
}

/**
 * The channel's management screen as a page (/channel/:slug/manage/:tab):
 * deep-linkable, back-button friendly and full width on a phone, instead of
 * the dialog the header used to open over the feed. Sections are keyed by id
 * and routed, so the menu's selected state follows the URL.
 */
@Component({
  selector: 'app-manage-channel',
  standalone: true,
  imports: [
    RouterLink,
    NbLayoutModule,
    NbMenuModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    ChannelInfoFormComponent,
    SettingsComponent,
    PrivilegDashboardComponent,
    StatisticsComponent,
    EmojisComponent,
    MagnetAdsComponent,
    StorageComponent,
    ReportsComponent,
    GuideComponent,
    SupportBoxComponent,
  ],
  templateUrl: './manage-channel.component.html',
  styleUrl: './manage-channel.component.scss',
})
export class ManageChannelComponent implements OnInit, OnDestroy {
  slug = '';
  tab = 'info';
  channelName = '';
  menu: NbMenuItem[] = [];
  sections: ManageSection[] = [];

  readonly allSections: ManageSection[] = [
    { id: 'info', title: 'פרטי הערוץ', icon: 'info-outline', access: 'moderator', description: 'שם, תיאור, לוגו וקישור ליצירת קשר — מה שהקוראים רואים בראש הערוץ.' },
    { id: 'settings', title: 'הגדרות', icon: 'settings-2-outline', access: 'owner', description: 'מי יכול לקרוא, אילו יכולות פעילות, והתראות.' },
    { id: 'users', title: 'צוות והרשאות', icon: 'people-outline', access: 'owner', description: 'מי כותב, מי מנהל ומי בעלים.' },
    { id: 'statistics', title: 'סטטיסטיקות', icon: 'bar-chart-outline', access: 'moderator', description: 'צפיות, קוראים ופעילות לאורך זמן.' },
    { id: 'emojis', title: 'תגובות אימוג\'י', icon: 'smiling-face-outline', access: 'moderator', description: 'אילו אימוג\'ים הקוראים יכולים להגיב בהם.' },
    { id: 'magnet-ads', title: 'פרסומות ממגנט', icon: 'pricetags-outline', access: 'owner', description: 'שילוב פרסומות של מגנט ADS בתוך הערוץ.' },
    { id: 'storage', title: 'אחסון', icon: 'hard-drive-outline', access: 'owner', description: 'כמה מקום הקבצים תופסים וניקוי אוטומטי.' },
    { id: 'reports', title: 'דיווחים', icon: 'alert-triangle-outline', access: 'moderator', description: 'הודעות שקוראים סימנו כבעייתיות.' },
    { id: 'guide', title: 'מדריך למנהל', icon: 'book-open-outline', access: 'moderator', description: 'כל מה שאפשר לעשות בערוץ, צעד אחר צעד.' },
    { id: 'support', title: 'פנייה לתמיכה', icon: 'email-outline', access: 'moderator', description: 'שאלה, תקלה או בקשה להנהלת המערכת.' },
  ];

  private paramSub?: Subscription;

  constructor(
    private route: ActivatedRoute,
    private router: Router,
    private slugService: SlugService,
    private chatService: ChatService,
    private authService: AuthService,
    private notificationsService: NotificationsService,
    private titleService: Title,
  ) {}

  get section(): ManageSection | undefined {
    return this.sections.find(s => s.id === this.tab || (this.tab.startsWith('reports') && s.id === 'reports'));
  }

  ngOnInit(): void {
    this.paramSub = this.route.paramMap.subscribe(params => {
      const slug = params.get('slug') ?? '';
      const tab = params.get('tab');
      if (slug !== this.slug) {
        this.slug = slug;
        // The admin sub-components read the channel through SlugService and
        // ChatService, exactly as the dialog did.
        this.slugService.slug = slug;
        this.chatService.channelInfo = undefined;
        this.notificationsService.reset();
        this.chatService.updateChannelInfo()
          .then(() => {
            this.channelName = this.chatService.channelInfo?.name || slug;
            this.titleService.setTitle(`ניהול · ${this.channelName}`);
          })
          .catch(() => { this.channelName = slug; });
        this.buildMenu();
      }
      if (!tab) {
        this.router.navigate(['/channel', slug, 'manage', this.sections[0]?.id ?? 'info'], { replaceUrl: true });
        return;
      }
      this.tab = tab;
    });
  }

  ngOnDestroy(): void {
    this.paramSub?.unsubscribe();
  }

  private canAccess(access: Access): boolean {
    const user = this.authService.userInfo;
    if (user?.globalRole === 'super_admin') return true;
    const role = user?.channelRoles?.[this.slug];
    if (access === 'owner') return role === 'owner';
    return role === 'owner' || role === 'moderator';
  }

  private buildMenu(): void {
    this.sections = this.allSections.filter(s => this.canAccess(s.access));
    this.menu = this.sections.map(s => {
      const item: NbMenuItem = { title: s.title, icon: s.icon, link: `/channel/${this.slug}/manage/${s.id}` };
      if (s.id === 'reports') {
        item.children = [
          { title: 'פתוחים', link: `/channel/${this.slug}/manage/reports` },
          { title: 'סגורים', link: `/channel/${this.slug}/manage/reports-closed` },
          { title: 'כל הדיווחים', link: `/channel/${this.slug}/manage/reports-all` },
        ];
      }
      return item;
    });
  }
}
