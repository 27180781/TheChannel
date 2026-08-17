import { Component, ElementRef, OnDestroy, OnInit, Renderer2, RendererStyleFlags2, ViewChild } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { AdvertisingComponent } from "./advertising/advertising.component";

import { Ad, AdsService } from '../../services/ads.service';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbInputModule,
  NbLayoutModule,
  NbListModule,
  NbMenuModule,
  NbSidebarModule,
  NbSpinnerModule,
  NbToastrService,
} from "@nebular/theme";
import { InputFormComponent } from "./chat/input-form/input-form.component";
import { AuthService } from "../../services/auth.service";
import { ChannelHeaderComponent } from "./channel-header/channel-header.component";
import { ChatComponent } from "./chat/chat.component";
import { User } from '../../models/user.model';
import { ActivatedRoute, Router } from '@angular/router';
import { SlugService } from '../../services/slug.service';
import { AdminService } from '../../services/admin.service';
import { ChatService } from '../../services/chat.service';
import { MagnetAdsService } from '../../services/magnet-ads.service';
import { ChannelStatusService } from '../../services/channel-status.service';
import { NotificationsService } from '../../services/notifications.service';
import { CreateChannelFormComponent } from '../channel-create/create-channel-form.component';
import { PlatformAttributionComponent } from './platform-attribution/platform-attribution.component';
import { Subscription } from 'rxjs';

@Component({
  selector: 'app-channel',
  imports: [
    FormsModule,
    AdvertisingComponent,
    NbLayoutModule,
    NbCardModule,
    NbInputModule,
    NbSpinnerModule,
    InputFormComponent,
    ChannelHeaderComponent,
    NbButtonModule,
    NbIconModule,
    NbMenuModule,
    NbSidebarModule,
    NbListModule,
    ChatComponent,
    CreateChannelFormComponent,
    PlatformAttributionComponent
  ],
  templateUrl: './channel.component.html',
  styleUrl: './channel.component.scss'
})
export class ChannelComponent implements OnInit, OnDestroy {

  @ViewChild('inputForm', { static: false })
  set inputForm(element: ElementRef) {
    if (element) {
      setTimeout(() => {
        this.updateInputBottomOffset();
      }, 0);
    }
  }

  constructor(
    private adsService: AdsService,
    private _authService: AuthService,
    private renderer: Renderer2,
    private el: ElementRef,
    private route: ActivatedRoute,
    private router: Router,
    private slugService: SlugService,
    private adminService: AdminService,
    private chatService: ChatService,
    private magnetAds: MagnetAdsService,
    private channelStatus: ChannelStatusService,
    private notificationsService: NotificationsService,
    private toastr: NbToastrService,
  ) { }

  ad: Ad = { src: '', width: 0 };
  userInfo?: User;
  slugReady = false;
  noChannel = false;
  private paramSub?: Subscription;

  /**
   * Slug of the channel an operator switched off, raised centrally by
   * channelDisabledInterceptor from the 403 {"error":"channel_disabled"} that
   * every /api/channel/{slug}/* call answers. Null while the channel is fine.
   */
  get channelDisabled(): string | null {
    return this.channelStatus.disabledSlug();
  }

  readonly onboardingSteps = [
    { icon: 'edit-2-outline', title: 'בוחרים שם וכתובת', text: 'הכתובת נבדקת מול השרת בזמן אמת.' },
    { icon: 'flash-outline', title: 'פותחים בלחיצה', text: 'הערוץ נוצר מיד ואתם הבעלים שלו.' },
    { icon: 'paper-plane-outline', title: 'מתחילים לשדר', text: 'מפרסמים הודעה ראשונה ומזמינים קוראים.' },
  ];

  ngOnInit(): void {
    this.paramSub = this.route.paramMap.subscribe(params => {
      const slug = params.get('slug');
      this.initChannel(slug);
    });
  }

  ngOnDestroy(): void {
    this.paramSub?.unsubscribe();
  }

  private async initChannel(slug: string | null): Promise<void> {
    this.slugReady = false;
    this.noChannel = false;
    // A flag raised for the previous channel must not follow us to the next one.
    this.channelStatus.reset();
    // Yield to Angular's change detection so the @if (slugReady) block
    // actually destroys ChatComponent before we reinitialise with the new slug.
    // Without this, false→true in the same synchronous frame is collapsed and
    // the child is never torn down, leaving stale state (isVisible, messages, etc).
    await Promise.resolve();

    if (!slug) {
      // Every branch below must end in a rendered state. An unguarded await
      // here used to throw for a visitor without a session, leaving
      // slugReady=false and noChannel=false — a template that renders nothing
      // at all, i.e. a blank white page.
      let user: User | undefined;
      try {
        user = (await this._authService.loadUserInfo()) ?? undefined;
      } catch {
        user = undefined;
      }

      if (!user) {
        // No session: there is no "my channels" list to show, so send the
        // visitor to the login page instead of rendering an empty shell.
        try {
          localStorage.setItem('returnUrl', '/channel');
        } catch {
          // Storage unavailable — the login page just falls back to /channel.
        }
        this.router.navigate(['/login']);
        return;
      }

      const roles = user.channelRoles;
      const firstSlug = roles ? Object.keys(roles)[0] : '';
      if (firstSlug) {
        this.router.navigate(['/channel', firstSlug], { replaceUrl: true });
      } else {
        this.userInfo = user;
        this.noChannel = true;
      }
      return;
    }

    this.slugService.slug = slug;
    this.chatService.clearCache();
    this.adminService.clearCache();
    this.notificationsService.reset();
    // Drop any in-progress edit/compose state from the previous channel, otherwise
    // the replayed BehaviorSubject value makes the input form post the old message
    // id into the new channel.
    this.adminService.setEditMessage(undefined);
    this.magnetAds.clearCache();
    this.slugReady = true;

    this.adsService.getAds().then(ad => {
      this.ad = ad;
    });
    this._authService.loadUserInfo().then(res => {
      this.userInfo = res;
      // Count this signed-in viewer as a channel participant (fire-and-forget).
      this._authService.registerChannelVisit(slug);
    }).catch(() => {
      // Anonymous visitor on a public channel — read-only view.
      this.userInfo = undefined;
    });
  }

  /**
   * The channel now exists and the session owns it. `loadUserInfo` memoises the
   * cached User, so a plain re-read would hand back an object without the new
   * owner role and the composer would stay hidden — force the refresh first.
   */
  async onChannelCreated(slug: string): Promise<void> {
    try {
      this.userInfo = await this._authService.reloadUserInfo();
    } catch {
      // The channel exists either way; the channel view refetches user-info.
    }
    this.router.navigate(['/channel', slug]);
  }

  backToMyChannels(): void {
    this.channelStatus.reset();
    this.router.navigate(['/channel']);
  }

  async logout(): Promise<void> {
    if (await this._authService.logout()) {
      this.router.navigate(['/login']);
    } else {
      this.toastr.danger('', 'שגיאה בהתנתקות');
    }
  }

  // A role on some other channel must not open the composer here — gate on the
  // role the user holds on the channel currently being viewed.
  hasAnyRole(user: User | undefined): boolean {
    if (user?.globalRole === 'super_admin') return true;
    const role = user?.channelRoles?.[this.slugService.slug];
    return role === 'owner' || role === 'moderator' || role === 'writer';
  }

  onInputHeightChanged() {
    this.updateInputBottomOffset();
  }

  updateInputBottomOffset() {
    let inputForm = document.getElementById('inputForm');
    let h = inputForm?.clientHeight;
    this.renderer.setStyle(this.el.nativeElement, '--input-height', `${h}px`, RendererStyleFlags2.DashCase);
  }
}
