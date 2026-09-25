import { Component, Input, OnInit } from '@angular/core';
import { formatBytes, storageLevelStatus } from '../../../utils/storage-format';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbCardModule, NbButtonModule, NbToggleModule,
  NbProgressBarModule, NbToastrService, NbAlertModule, NbIconModule
} from '@nebular/theme';
import { HttpClient } from '@angular/common/http';
import { firstValueFrom } from 'rxjs';

interface StorageInfo {
  usedBytes: number;
  quotaBytes: number;
  usedPercent: number;
  autoCleanup: boolean;
  level: 'ok' | 'warning' | 'critical';
}

@Component({
  selector: 'app-channel-storage',
  standalone: true,
  imports: [
    CommonModule, FormsModule,
    NbCardModule, NbButtonModule, NbToggleModule,
    NbProgressBarModule, NbAlertModule, NbIconModule
  ],
  template: `
    <nb-card>
      <nb-card-header>אחסון</nb-card-header>
      <nb-card-body>
        @if (info) {
          <div class="mb-3">
            <div class="d-flex justify-content-between mb-1">
              <span>שימוש: {{ formatBytes(info.usedBytes) }}</span>
              <span>מתוך: {{ info.quotaBytes ? formatBytes(info.quotaBytes) : 'ללא הגבלה' }}</span>
            </div>
            <nb-progress-bar
              [value]="percentDisplay()"
              [status]="progressStatus()"
              [displayValue]="true">
            </nb-progress-bar>
          </div>

          @if (info.level === 'critical') {
            <nb-alert status="danger" closable class="mb-3">
              <nb-icon icon="alert-triangle-outline"></nb-icon>
              שטח האחסון כמעט מלא! פנה מקום או הפעל ניקוי אוטומטי.
            </nb-alert>
          } @else if (info.level === 'warning') {
            <nb-alert status="warning" closable class="mb-3">
              <nb-icon icon="alert-circle-outline"></nb-icon>
              שטח האחסון מתמלא ({{ percentDisplay() }}%).
            </nb-alert>
          }

          <div class="d-flex align-items-center gap-3">
            <nb-toggle
              [(ngModel)]="info.autoCleanup"
              (change)="saveAutoCleanup()">
              ניקוי אוטומטי של מדיה ישנה
            </nb-toggle>
            <small class="text-muted">
              כשהאחסון עומד להיגמר, מוחק קבצים ישנים אוטומטית כדי לפנות מקום
            </small>
          </div>
        } @else {
          <p>טוען...</p>
        }
      </nb-card-body>
    </nb-card>
  `
})
export class StorageComponent implements OnInit {
  @Input() slug!: string;

  info?: StorageInfo;

  constructor(private http: HttpClient, private toastr: NbToastrService) {}

  ngOnInit() {
    this.load();
  }

  async load() {
    try {
      this.info = await firstValueFrom(
        this.http.get<StorageInfo>(`/api/channel/${this.slug}/admin/storage`)
      );
    } catch {
      this.toastr.danger('שגיאה בטעינת מידע אחסון', 'שגיאה');
    }
  }

  async saveAutoCleanup() {
    if (!this.info) return;
    try {
      await firstValueFrom(this.http.post(
        `/api/channel/${this.slug}/admin/storage/auto-cleanup`,
        { enabled: this.info.autoCleanup }
      ));
      this.toastr.success('הגדרות נשמרו', 'אחסון');
    } catch {
      // ngModel flipped the switch before the request went out; put it back so
      // the toggle does not keep claiming a state the server never stored.
      this.info.autoCleanup = !this.info.autoCleanup;
      this.toastr.danger('שגיאה בשמירה', 'שגיאה');
    }
  }

  // The server sends the raw float division and nb-progress-bar prints
  // `{{ value }}%` verbatim: 2.3456789012345% on a quiet channel, and once a
  // quota is lowered below current usage, 130% spilling past the bar. A quota
  // of 0 means unlimited, so there is nothing to be a percentage of.
  percentDisplay(): number {
    if (!this.info || !this.info.quotaBytes) return 0;
    return Math.min(100, Math.round(this.info.usedPercent));
  }

  progressStatus(): string {
    // Before info loads there is no level yet; keep the neutral colour.
    if (!this.info) return 'primary';
    return storageLevelStatus(this.info.level);
  }

  formatBytes = formatBytes;
}
