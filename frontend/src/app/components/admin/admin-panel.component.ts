import { Component, OnInit } from "@angular/core";
import { NbCardModule, NbLayoutModule, NbMenuItem, NbMenuModule, NbMenuService, NbSidebarModule } from "@nebular/theme";
import { ChatService } from "../../services/chat.service";
import { EmojisComponent } from "./emojis/emojis.component";
import { SettingsComponent } from "./settings/settings.component";
import { PrivilegDashboardComponent } from "./privileg-dashboard/privileg-dashboard.component";
import { ChannelInfoFormComponent } from "../channel/channel-info-form/channel-info-form.component";
import { ReportsComponent } from "./reports/reports.component";
import { StatisticsComponent } from "./statistics/statistics.component";
import { MagnetAdsComponent } from "./magnet-ads/magnet-ads.component";
import { StorageComponent } from "./storage/storage.component";

@Component({
  selector: 'admin-dashboard',
  imports: [
    NbLayoutModule,
    NbSidebarModule,
    NbMenuModule,
    NbCardModule,
    EmojisComponent,
    SettingsComponent,
    PrivilegDashboardComponent,
    ChannelInfoFormComponent,
    ReportsComponent,
    StatisticsComponent,
    MagnetAdsComponent,
    StorageComponent
],
  templateUrl: './admin-panel.component.html',
  styleUrls: ['./admin-panel.component.scss']
})
export class AdminPanelComponent implements OnInit {

  readonly info = "info";
  readonly settings = "settings";
  readonly users = "users";
  readonly emojis = "emojis";
  readonly openReports = "open-reports";
  readonly closedReports = "closed-reports";
  readonly allReports = "all-reports";
  readonly statistics = "statistics";
  readonly magnetAds = "magnet-ads";
  readonly storage = "storage";

  selectedMenuItem = this.info;

  navigationMenu: NbMenuItem[] = [
    {
      title: 'פרטי ערוץ',
      icon: 'info-outline',
      selected: true
    },
    {
      title: 'הגדרות',
      icon: 'settings-2-outline',
    },
    {
      title: 'הרשאות',
      icon: 'shield-outline',
    },
    {
      title: 'סטטיסטיקות',
      icon: 'bar-chart-outline',
    },
    {
      title: "אימוג'ים",
      icon: 'smiling-face-outline',
    },
    {
      title: 'שילוב פרסומות ממגנט',
      icon: 'pricetags-outline',
    },
    {
      title: 'אחסון',
      icon: 'hard-drive-outline',
    },
    {
      title: 'דיווחים',
      icon: 'alert-triangle-outline',
      children: [
        {
          title: 'פתוחים',
          icon: 'alert-triangle-outline',
        },
        {
          title: 'סגורים',
          icon: 'checkmark-circle-outline',
        },
        {
          title: 'כל הדיווחים',
          icon: 'list-outline',
        }
      ],
    }
  ];

  get channelSlug(): string {
    return this.chatService.channelInfo?.slug ?? '';
  }

  constructor(
    private menuService: NbMenuService,
    private chatService: ChatService
  ) { }

  ngOnInit(): void {
    this.handleMenuItemClick()
  }

  handleMenuItemClick() {
    this.menuService.onItemClick().subscribe((event) => {
      this.navigationMenu.forEach((item) => item.selected = false);
      event.item.selected = true;

      switch (event.item.icon) {
        case 'info-outline':
          this.selectedMenuItem = this.info;
          break;
        case 'settings-2-outline':
          this.selectedMenuItem = this.settings;
          break;
        case 'shield-outline':
          this.selectedMenuItem = this.users;
          break;
        case 'smiling-face-outline':
          this.selectedMenuItem = this.emojis;
          break;
        case 'alert-triangle-outline':
          this.selectedMenuItem = this.openReports;
          break;
        case 'checkmark-circle-outline':
          this.selectedMenuItem = this.closedReports;
          break;
        case 'list-outline':
          this.selectedMenuItem = this.allReports;
          break;
        case 'bar-chart-outline':
          this.selectedMenuItem = this.statistics;
          break;
        case 'pricetags-outline':
          this.selectedMenuItem = this.magnetAds;
          break;
        case 'hard-drive-outline':
          this.selectedMenuItem = this.storage;
          break;
      }
    });
  }
}