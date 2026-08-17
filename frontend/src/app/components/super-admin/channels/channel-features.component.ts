import { Component, Input, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbToggleModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService, ChannelFeatures } from '../../../services/super-admin.service';

interface FeatureConfig {
  key: keyof ChannelFeatures;
  label: string;
  /** Renders the row as a warning block — turning it on takes the channel down. */
  destructive?: boolean;
  description?: string;
}

@Component({
  selector: 'app-channel-features',
  standalone: true,
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbToggleModule,
  ],
  templateUrl: './channel-features.component.html',
  styleUrl: './channel-features.component.scss',
})
export class ChannelFeaturesComponent implements OnInit {
  @Input() slug!: string;

  features: ChannelFeatures = {
    reactions: false,
    fileUploads: false,
    reports: false,
    ads: false,
    notifications: false,
    requireAuth: false,
    requireAuthFiles: false,
    countViews: false,
    scheduledMessages: false,
    webhook: false,
    magnetLockedByAdmin: false,
    adsLockedByAdmin: false,
    disabled: false,
  };

  saving = false;

  featureConfigs: FeatureConfig[] = [
    { key: 'reactions', label: 'תגובות' },
    { key: 'fileUploads', label: 'העלאת קבצים' },
    { key: 'reports', label: 'דיווחים' },
    { key: 'ads', label: 'פרסומות iframe' },
    { key: 'notifications', label: 'התראות' },
    { key: 'requireAuth', label: 'דרוש כניסה לצפייה' },
    { key: 'requireAuthFiles', label: 'דרוש כניסה לקבצים' },
    { key: 'countViews', label: 'ספירת צפיות' },
    { key: 'scheduledMessages', label: 'הודעות מתוזמנות' },
    { key: 'webhook', label: 'Webhook' },
    {
      key: 'disabled',
      label: 'השבתת הערוץ',
      destructive: true,
      description: 'הערוץ יפסיק להיות זמין לכל המשתמשים, כולל הבעלים, ויוצג במקומו מסך "הערוץ מושבת".',
    },
    // magnetLockedByAdmin / adsLockedByAdmin are managed by the global
    // ads/magnet locked-channel lists — the single source of truth. The values
    // loaded from the channel are passed back through save() untouched.
  ];

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.loadFeatures();
  }

  loadFeatures() {
    this.superAdminService.getChannel(this.slug)
      .then(channel => {
        // Merge over the defaults: an older backend omits newer flags entirely,
        // and an undefined value would leave the toggle unbound.
        this.features = { ...this.features, ...channel.features };
      })
      .catch(() => this.toastr.danger('', 'שגיאה בטעינת תכונות הערוץ'));
  }

  save() {
    this.saving = true;
    this.superAdminService.updateChannelFeatures(this.slug, this.features)
      .then(() => this.toastr.success('', 'התכונות נשמרו בהצלחה'))
      .catch(() => this.toastr.danger('', 'שגיאה בשמירת התכונות'))
      .finally(() => this.saving = false);
  }
}
