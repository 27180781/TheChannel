import { Component, EventEmitter, HostListener, Input, OnDestroy, OnInit, Output } from '@angular/core';
import { Subscription } from 'rxjs';

import {
  NbButtonModule,
  NbContextMenuModule,
  NbDialogService,
  NbIconModule,
  NbMenuItem,
  NbMenuService,
  NbPopoverModule,
  NbPosition,
  NbToastrService,
  NbTooltipModule,
  NbUserModule
} from "@nebular/theme";
import { filter } from "rxjs";
import type Viewer from 'viewerjs';
import { Router } from '@angular/router';
import { Title } from '@angular/platform-browser';
import { AuthService } from '../../../services/auth.service';
import { ChatService } from '../../../services/chat.service';
import { NotificationsService } from '../../../services/notifications.service';
import { SlugService } from '../../../services/slug.service';
import { MyChannelsService } from '../../../services/my-channels.service';
import { ShareService } from '../../../services/share.service';
import { SupportBoxComponent } from '../../support/support-box.component';
import { User } from '../../../models/user.model';
import { MyChannel } from '../../../models/my-channel.model';

/** What a menu entry does; carried in NbMenuItem.data so the icon can change freely. */
type MenuAction =
  | 'logout' | 'manage' | 'super-admin' | 'support' | 'create' | 'hub' | 'switch'
  | 'copy' | 'whatsapp' | 'share';

interface MenuData {
  action: MenuAction;
  slug?: string;
}

/** The switcher lists at most this many channels; the rest live on the hub page. */
const MAX_SWITCHER_CHANNELS = 8;

@Component({
  selector: 'app-channel-header',
  imports: [
    NbButtonModule,
    NbIconModule,
    NbUserModule,
    NbContextMenuModule,
    NbTooltipModule,
    NbPopoverModule,
  ],
  templateUrl: './channel-header.component.html',
  styleUrl: './channel-header.component.scss'
})
export class ChannelHeaderComponent implements OnInit, OnDestroy {
  private menuSub?: Subscription;

  @Input()
  set userInfo(user: User | undefined) {
    this._userInfo = user;
    // The switcher needs the user's channels; one cached request per user.
    if (user) this.loadMyChannels();
  }

  get userInfo() {
    return this._userInfo;
  }

  private _userInfo?: User;

  /** The signed-in user's channels, for the switcher. Empty until loaded or when the request failed. */
  private myChannels: MyChannel[] = [];
  private myChannelsFor?: User;

  // Derived lazily rather than in the input setter: the user object is cached by
  // AuthService, so switching channels re-emits the very same reference and the
  // setter never runs again — the menu would keep the previous channel's answer.
  // Rebuilt only when the entries actually change, so the context menu directive
  // is not handed a fresh array on every change detection pass.
  get userMenu(): NbMenuItem[] {
    const user = this._userInfo;
    const slug = this._slugService.slug;
    const role = user?.channelRoles?.[slug];
    // Only the role on the channel being viewed counts; a super_admin is granted
    // everything by the backend even with an empty channelRoles map.
    const isSuperAdmin = user?.globalRole === 'super_admin';
    // Every section of the manage page is moderator level or above, so a writer
    // would open it onto an empty sidebar.
    const canManageChannel = !!user && (isSuperAdmin || role === 'owner' || role === 'moderator');
    const key = `${canManageChannel}|${isSuperAdmin}|${slug}|${this.myChannels.map(c => c.slug + ':' + c.name).join(',')}`;

    if (this._menuKey !== key) {
      this._menuKey = key;
      const channels = this.myChannels.slice(0, MAX_SWITCHER_CHANNELS);
      const data = (action: MenuAction, extra: Partial<MenuData> = {}): MenuData => ({ action, ...extra });
      this._userMenu = [
        ...(channels.length ? [
          { title: 'הערוצים שלי', group: true, icon: 'grid-outline' } as NbMenuItem,
          ...channels.map(c => ({
            title: c.name?.trim() || c.slug,
            icon: c.slug === slug ? 'checkmark-circle-2-outline' : 'radio-outline',
            // A router link: Nebular navigates and closes the menu itself.
            link: `/channel/${c.slug}`,
            pathMatch: 'full' as const,
            selected: c.slug === slug,
            data: data('switch', { slug: c.slug }),
          } as NbMenuItem)),
          { title: 'כל הערוצים…', icon: 'list-outline', link: '/channel', pathMatch: 'full' as const, data: data('hub') } as NbMenuItem,
        ] : []),
        ...(canManageChannel ? [{
          title: 'ניהול הערוץ',
          icon: 'settings-2-outline',
          data: data('manage'),
        }] : []),
        {
          title: 'פתיחת ערוץ חדש',
          icon: 'plus-outline',
          data: data('create'),
        },
        ...(isSuperAdmin ? [{
          title: 'פאנל מנהל-על',
          icon: 'shield-outline',
          data: data('super-admin'),
        }] : []),
        // For every signed-in user, not only those who can open the manage
        // page: the support box otherwise lives on the landing page (which
        // redirects anyone signed in) and in a manage section (moderator and
        // above), so a writer or a plain reader had no way to reach it at all.
        {
          title: 'פנייה לתמיכה',
          icon: 'question-mark-circle-outline',
          data: data('support'),
        },
        {
          title: 'התנתק',
          icon: 'log-out-outline',
          data: data('logout'),
        }
      ];
    }

    return this._userMenu;
  }

  private _userMenu: NbMenuItem[] = [];
  private _menuKey?: string;

  /** "Share the channel" menu: copy, WhatsApp, and the OS share sheet where it exists. */
  readonly shareMenu: NbMenuItem[];
  protected readonly NbPosition = NbPosition;

  @Output()
  userInfoChange: EventEmitter<User> = new EventEmitter<User>();

  userMenuTag = 'user-menu';
  shareMenuTag = 'share-menu';
  isSmallScreen = false;

  constructor(
    public chatService: ChatService,
    public _authService: AuthService,
    private contextMenuService: NbMenuService,
    private toastrService: NbToastrService,
    private router: Router,
    public notificationsService: NotificationsService,
    private titleService: Title,
    private dialogService: NbDialogService,
    private _slugService: SlugService,
    private myChannelsService: MyChannelsService,
    private shareService: ShareService,
  ) {
    this.shareMenu = [
      { title: 'העתקת הקישור', icon: 'copy-outline', data: { action: 'copy' } as MenuData },
      { title: 'שיתוף בוואטסאפ', icon: 'message-circle-outline', data: { action: 'whatsapp' } as MenuData },
      ...(shareService.canShare
        ? [{ title: 'שיתוף…', icon: 'share-outline', data: { action: 'share' } as MenuData }]
        : []),
    ];
  }

  @HostListener('window:resize')
  onResize() {
    this.updateScreenSize();
  }

  ngOnInit() {
    this.chatService.updateChannelInfo()
      .then(() => this.titleService.setTitle(this.chatService.channelInfo?.name || 'הערוץ'));

    // One subscription for both menus, keyed on the item's data.action — the
    // icon used to be the key, which broke the moment an icon was redesigned.
    this.menuSub = this.contextMenuService.onItemClick()
      .pipe(filter(({ tag }) => tag === this.userMenuTag || tag === this.shareMenuTag))
      .subscribe(({ item }) => this.onMenuAction(item.data as MenuData | undefined));

    this.updateScreenSize();
  }

  ngOnDestroy() {
    this.menuSub?.unsubscribe();
    // ViewerJS appends its container to document.body — destroy it with the header.
    this.v?.destroy();
  }

  private onMenuAction(data: MenuData | undefined) {
    switch (data?.action) {
      case 'logout':
        this.logout();
        break;
      case 'manage':
        this.router.navigate(['/channel', this._slugService.slug, 'manage']);
        break;
      case 'super-admin':
        this.router.navigate(['/super-admin']);
        break;
      case 'support':
        this.openSupport();
        break;
      case 'create':
        // The hub page owns the create flow (and the "you already own 5"
        // message); the query parameter is a hint it may use to open it at once.
        this.router.navigate(['/channel'], { queryParams: { new: '1' } });
        break;
      case 'copy':
        this.shareService.copy(this.channelUrl).then(ok => {
          if (ok) this.toastrService.success('', 'הקישור לערוץ הועתק — אפשר להדביק ולשלוח');
        });
        break;
      case 'whatsapp':
        window.open(this.shareService.whatsappUrl(this.inviteText), '_blank', 'noopener,noreferrer');
        break;
      case 'share':
        this.shareService.share({ title: this.channelName, text: this.inviteText, url: this.channelUrl })
          .then(outcome => {
            if (outcome === 'unsupported') {
              // No share sheet after all (or it failed): fall back to the clipboard.
              return this.shareService.copy(this.channelUrl).then(ok => {
                if (ok) this.toastrService.success('', 'הקישור לערוץ הועתק — אפשר להדביק ולשלוח');
              });
            }
            return undefined;
          });
        break;
      // 'switch' and 'hub' carry a router link; Nebular navigates on its own.
    }
  }

  /**
   * The user's channels for the switcher. The service caches per user, so this
   * is one request per session; a failure just leaves the group out of the
   * menu — every other entry still works.
   */
  loadMyChannels() {
    const user = this._userInfo;
    if (!user || this.myChannelsFor === user) return;
    this.myChannelsFor = user;
    this.myChannelsService.list()
      .then(list => {
        if (this._userInfo !== user) return;
        this.myChannels = list;
      })
      .catch(() => {
        // Allow a retry on the next open of the menu.
        if (this.myChannelsFor === user) this.myChannelsFor = undefined;
        this.myChannels = [];
      });
  }

  get channelName(): string {
    return this.chatService.channelInfo?.name?.trim() || '';
  }

  get channelUrl(): string {
    return this.shareService.channelUrl(this._slugService.slug);
  }

  private get inviteText(): string {
    return this.shareService.inviteText(this.channelName, this.channelUrl);
  }

  /** First letter of the channel name, for the avatar fallback. */
  get channelInitial(): string {
    return this.channelName.charAt(0) || '?';
  }

  /**
   * The guard and the landing page record where the visitor was before
   * sending them to Google; this button did not, so a reader who signed in
   * from /channel/foo came back to /channel (the "my channel" page, or the
   * onboarding form) with no way back to the channel they were reading. Worse,
   * a stale returnUrl from an earlier guard redirect sent them somewhere else
   * entirely. Record the current URL first, the same way the guard does.
   */
  async login() {
    try {
      localStorage.setItem('returnUrl', this.router.url);
    } catch {
      // Storage unavailable — the login page falls back to /channel.
    }
    try {
      await this._authService.loginWithGoogle();
    } catch {
      // GET /auth/google failed; without this the click did nothing at all.
      this.toastrService.danger("", "ההתחברות אינה זמינה כרגע, נסו שוב מאוחר יותר");
    }
  }

  async logout() {
    if (await this._authService.logout()) {
      this.userInfo = undefined;
      this.userInfoChange.emit(undefined);
      try {
        await this._authService.loadUserInfo();
        // Still logged in somehow — go to root and let AuthGuard decide
        this.router.navigate(['/']);
      } catch (err: any) {
        if (err.status === 401) {
          this.router.navigate(['/login']);
        }
      }
    } else {
      this.toastrService.danger("", "שגיאה בהתנתקות");
    }
  }

  private v?: Viewer;

  async viewLargeImage(event: MouseEvent) {
    // The click lands on the button wrapping the picture (keyboard users too),
    // so the picture is looked up rather than taken from the event target.
    const target = (event.currentTarget as HTMLElement | null)?.querySelector('img') as HTMLImageElement | null;
    if (target) {
      if (!this.v) {
        // Fetched on the first click only: every reader paid for the viewer
        // on page load, and most never open the logo.
        const { default: ViewerCtor } = await import('viewerjs');
        if (this.v) { (this.v as Viewer).update(); (this.v as Viewer).show(); return; }
        this.v = new ViewerCtor(target, {
          toolbar: false,
          transition: true,
          navbar: false,
          title: false
        });
      } else {
        // The logo src may have changed since the viewer cached it.
        this.v.update();
      }
      this.v.show();
    }
  }

  updateScreenSize() {
    this.isSmallScreen = window.innerWidth < 768;
  }

  /** The same box the manage page shows in its 'פנייה לתמיכה' section, as a dialog. */
  openSupport() {
    this.dialogService.open(SupportBoxComponent, {
      closeOnBackdropClick: true,
      context: {
        signedIn: true,
        channelSlug: this._slugService.slug,
        dialogMode: true,
        title: 'פנייה לתמיכה',
        subtitle: 'שאלה, תקלה או בקשה — הפנייה מגיעה להנהלת המערכת, והתשובה תופיע כאן.',
      },
    });
  }

  openContactUs() {
    const url = this.chatService.channelInfo?.contact_us?.trim();
    if (!url) return;
    // contact_us is operator-set free text. window.open on a javascript: value
    // would execute it in the opened window with this origin, so only http(s)
    // and mailto are honoured; anything else is ignored. noopener/noreferrer
    // also stop the opened page from reaching back through window.opener.
    const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(url);
    if (scheme && !/^(https?|mailto)$/i.test(scheme[1])) return;
    window.open(url, '_blank', 'noopener,noreferrer');
  }
}
