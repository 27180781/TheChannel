import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbInputModule,
  NbRadioModule,
  NbToastrService,
  NbToggleModule,
} from '@nebular/theme';
import { AdminService } from '../../../services/admin.service';
import { Setting } from '../../../models/setting.model';

type MagnetMode = 'by_messages' | 'by_time';

const MAGNET_KEYS = [
  'magnet_enabled',
  'magnet_snippet',
  'magnet_mode',
  'magnet_per_messages',
  'magnet_min_time_seconds',
  'magnet_per_seconds',
  'magnet_min_messages_since',
] as const;

@Component({
  selector: 'app-magnet-ads',
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbInputModule,
    NbToggleModule,
    NbRadioModule,
  ],
  templateUrl: './magnet-ads.component.html',
  styleUrl: './magnet-ads.component.scss',
})
export class MagnetAdsComponent implements OnInit {
  enabled = false;
  snippet = '';
  mode: MagnetMode = 'by_messages';
  perMessages = 5;
  minTimeSeconds = 0;
  perSeconds = 60;
  minMessagesSince = 0;

  otherSettings: Setting[] = [];
  inProgress = false;

  constructor(
    private adminService: AdminService,
    private toast: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.adminService.getSettings().then(settings => this.load(settings || []));
  }

  private load(settings: Setting[]) {
    this.otherSettings = [];
    const known = new Set<string>(MAGNET_KEYS);

    for (const s of settings) {
      if (!known.has(s.key)) {
        this.otherSettings.push(s);
        continue;
      }
      const v = s.value;
      switch (s.key) {
        case 'magnet_enabled':
          this.enabled = this.toBool(v);
          break;
        case 'magnet_snippet':
          this.snippet = v == null ? '' : String(v);
          break;
        case 'magnet_mode':
          this.mode = (String(v) === 'by_time' ? 'by_time' : 'by_messages');
          break;
        case 'magnet_per_messages':
          this.perMessages = this.toInt(v, 5);
          break;
        case 'magnet_min_time_seconds':
          this.minTimeSeconds = this.toInt(v, 0);
          break;
        case 'magnet_per_seconds':
          this.perSeconds = this.toInt(v, 60);
          break;
        case 'magnet_min_messages_since':
          this.minMessagesSince = this.toInt(v, 0);
          break;
      }
    }
  }

  private toBool(v: any): boolean {
    if (typeof v === 'boolean') return v;
    if (typeof v === 'number') return v !== 0;
    if (typeof v === 'string') {
      const s = v.toLowerCase().trim();
      return s === '1' || s === 'true' || s === 'yes' || s === 'on';
    }
    return false;
  }

  private toInt(v: any, fallback: number): number {
    if (v === null || v === undefined || v === '') return fallback;
    const n = parseInt(String(v), 10);
    return isNaN(n) ? fallback : n;
  }

  save() {
    this.inProgress = true;
    const out: Setting[] = [...this.otherSettings];

    if (this.enabled) out.push({ key: 'magnet_enabled', value: '1' as any });
    if (this.snippet?.trim()) out.push({ key: 'magnet_snippet', value: this.snippet as any });
    out.push({ key: 'magnet_mode', value: this.mode as any });

    if (this.mode === 'by_messages') {
      if (this.perMessages > 0) out.push({ key: 'magnet_per_messages', value: String(this.perMessages) as any });
      if (this.minTimeSeconds > 0) out.push({ key: 'magnet_min_time_seconds', value: String(this.minTimeSeconds) as any });
    } else {
      if (this.perSeconds > 0) out.push({ key: 'magnet_per_seconds', value: String(this.perSeconds) as any });
      if (this.minMessagesSince > 0) out.push({ key: 'magnet_min_messages_since', value: String(this.minMessagesSince) as any });
    }

    this.adminService.setSettings(out)
      .then(() => this.toast.success('', 'הגדרות מגנט נשמרו בהצלחה'))
      .catch(() => this.toast.danger('', 'שגיאה בשמירת ההגדרות'))
      .finally(() => this.inProgress = false);
  }
}
