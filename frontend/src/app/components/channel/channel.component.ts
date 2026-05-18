import { Component, ElementRef, OnInit, Renderer2, RendererStyleFlags2, ViewChild } from '@angular/core';
import { AdvertisingComponent } from "./advertising/advertising.component";

import { Ad, AdsService } from '../../services/ads.service';
import {
  NbButtonModule,
  NbIconModule,
  NbLayoutModule,
  NbListModule,
  NbMenuModule,
  NbSidebarModule,
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

@Component({
  selector: 'app-channel',
  imports: [
    AdvertisingComponent,
    NbLayoutModule,
    InputFormComponent,
    ChannelHeaderComponent,
    NbButtonModule,
    NbIconModule,
    NbMenuModule,
    NbSidebarModule,
    NbListModule,
    ChatComponent
],
  templateUrl: './channel.component.html',
  styleUrl: './channel.component.scss'
})
export class ChannelComponent implements OnInit {

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
  ) { }

  ad: Ad = { src: '', width: 0 };
  userInfo?: User;
  slugReady = false;

  async ngOnInit(): Promise<void> {
    let slug = this.route.snapshot.paramMap.get('slug');

    if (!slug) {
      const user = await this._authService.loadUserInfo();
      const roles = user?.channelRoles;
      const firstSlug = roles ? Object.keys(roles)[0] : '';
      if (firstSlug) {
        this.router.navigate(['/channel', firstSlug], { replaceUrl: true });
      }
      return;
    }

    this.slugService.slug = slug;
    this.chatService.channelInfo = undefined;
    this.adminService.clearCache();
    this.slugReady = true;

    this.adsService.getAds().then(ad => {
      this.ad = ad;
    });
    this._authService.loadUserInfo().then(res => {
      this.userInfo = res;
    });
  }

  hasAnyRole(user: User | undefined): boolean {
    if (!user?.channelRoles) return false;
    return Object.keys(user.channelRoles).length > 0;
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
