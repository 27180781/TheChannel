import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import {
  NbCardModule,
  NbLayoutModule,
  NbMenuItem,
  NbMenuModule,
  NbMenuService,
  NbSidebarModule,
} from '@nebular/theme';
import { ChannelsListComponent } from './channels/channels-list.component';
import { ChannelFeaturesComponent } from './channels/channel-features.component';
import { ChannelUsersComponent } from './channels/channel-users.component';
import { GlobalAdsComponent } from './global-ads/global-ads.component';
import { GlobalMagnetComponent } from './global-magnet/global-magnet.component';
import { GlobalUsersComponent } from './global-users/global-users.component';
import { GlobalSettingsComponent } from './global-settings/global-settings.component';
import { SuperAdminStatisticsComponent } from './statistics/super-admin-statistics.component';

type ViewName = 'channels' | 'channel-features' | 'channel-users' | 'ads' | 'magnet' | 'users' | 'settings' | 'statistics';

@Component({
  selector: 'app-super-admin-panel',
  standalone: true,
  imports: [
    CommonModule,
    NbLayoutModule,
    NbSidebarModule,
    NbMenuModule,
    NbCardModule,
    ChannelsListComponent,
    ChannelFeaturesComponent,
    ChannelUsersComponent,
    GlobalAdsComponent,
    GlobalMagnetComponent,
    GlobalUsersComponent,
    GlobalSettingsComponent,
    SuperAdminStatisticsComponent,
  ],
  templateUrl: './super-admin-panel.component.html',
  styleUrl: './super-admin-panel.component.scss',
})
export class SuperAdminPanelComponent implements OnInit {
  selectedView: ViewName = 'channels';
  selectedChannelSlug = '';

  readonly MENU_TAG = 'super-admin-menu';

  readonly VIEW_CHANNELS: ViewName = 'channels';
  readonly VIEW_CHANNEL_FEATURES: ViewName = 'channel-features';
  readonly VIEW_CHANNEL_USERS: ViewName = 'channel-users';
  readonly VIEW_ADS: ViewName = 'ads';
  readonly VIEW_MAGNET: ViewName = 'magnet';
  readonly VIEW_USERS: ViewName = 'users';
  readonly VIEW_SETTINGS: ViewName = 'settings';
  readonly VIEW_STATISTICS: ViewName = 'statistics';

  navigationMenu: NbMenuItem[] = [
    { title: 'ערוצים', icon: 'list-outline', selected: true },
    { title: 'פרסומות iframe', icon: 'film-outline' },
    { title: 'פרסומות מגנט', icon: 'pricetags-outline' },
    { title: 'משתמשים', icon: 'people-outline' },
    { title: 'הגדרות גלובליות', icon: 'settings-2-outline' },
    { title: 'סטטיסטיקות', icon: 'bar-chart-outline' },
  ];

  constructor(private menuService: NbMenuService) {}

  ngOnInit(): void {
    this.menuService.onItemClick().subscribe(event => {
      this.navigationMenu.forEach(item => (item.selected = false));
      event.item.selected = true;

      switch (event.item.icon) {
        case 'list-outline':
          this.selectedView = this.VIEW_CHANNELS;
          this.selectedChannelSlug = '';
          break;
        case 'film-outline':
          this.selectedView = this.VIEW_ADS;
          break;
        case 'pricetags-outline':
          this.selectedView = this.VIEW_MAGNET;
          break;
        case 'people-outline':
          this.selectedView = this.VIEW_USERS;
          break;
        case 'settings-2-outline':
          this.selectedView = this.VIEW_SETTINGS;
          break;
        case 'bar-chart-outline':
          this.selectedView = this.VIEW_STATISTICS;
          break;
      }
    });
  }

  onEditFeatures(slug: string) {
    this.selectedChannelSlug = slug;
    this.selectedView = this.VIEW_CHANNEL_FEATURES;
  }

  onManageUsers(slug: string) {
    this.selectedChannelSlug = slug;
    this.selectedView = this.VIEW_CHANNEL_USERS;
  }

  backToChannels() {
    this.selectedView = this.VIEW_CHANNELS;
    this.selectedChannelSlug = '';
  }
}
