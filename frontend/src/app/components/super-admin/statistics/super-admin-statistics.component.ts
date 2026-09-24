import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService } from '../../../services/super-admin.service';

@Component({
  selector: 'app-super-admin-statistics',
  standalone: true,
  imports: [
    CommonModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
  ],
  templateUrl: './super-admin-statistics.component.html',
})
export class SuperAdminStatisticsComponent implements OnInit {
  magnetStats: any = null;
  loadingStats = true;
  resetting = false;

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.loadMagnetStats();
  }

  loadMagnetStats() {
    this.loadingStats = true;
    this.superAdminService.getMagnetStats()
      .then(stats => this.magnetStats = stats)
      .catch(() => this.toastr.warning('', 'לא ניתן לטעון סטטיסטיקות מגנט'))
      .finally(() => this.loadingStats = false);
  }

  resetStatistics() {
    // The reset clears the recorded peak AND every channel's monthly
    // connection-history series (the graphs on the owners' statistics
    // screens). The prompt used to name only the peak.
    if (!confirm('האם אתה בטוח שברצונך לאפס את הסטטיסטיקות? פעולה זו מוחקת את שיא החיבורים ואת היסטוריית החיבורים של כל הערוצים (הגרפים במסכי הסטטיסטיקה), ולא ניתן לשחזר אותה.')) return;
    this.resetting = true;
    this.superAdminService.resetStatistics()
      .then(() => this.toastr.success('', 'שיא החיבורים אופס בהצלחה'))
      .catch(() => this.toastr.danger('', 'שגיאה באיפוס הסטטיסטיקות'))
      .finally(() => this.resetting = false);
  }

  /**
   * The upstream response is nested (site, clicks, earnings objects). Only
   * the top level used to be listed, so those rows read "[object Object]";
   * nested values are flattened to dotted keys.
   */
  getStatEntries(): { key: string; value: any }[] {
    if (!this.magnetStats || typeof this.magnetStats !== 'object') return [];
    const rows: { key: string; value: any }[] = [];
    const walk = (value: any, prefix: string) => {
      if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
        for (const [k, v] of Object.entries(value)) {
          walk(v, prefix ? `${prefix}.${k}` : k);
        }
        return;
      }
      rows.push({ key: prefix, value: Array.isArray(value) ? JSON.stringify(value) : value });
    };
    walk(this.magnetStats, '');
    return rows;
  }
}
