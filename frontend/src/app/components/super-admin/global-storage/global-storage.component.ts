import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbCardModule, NbButtonModule, NbInputModule,
  NbFormFieldModule, NbIconModule, NbToastrService
} from '@nebular/theme';
import { SuperAdminService } from '../../../services/super-admin.service';

@Component({
  selector: 'app-global-storage',
  standalone: true,
  imports: [
    CommonModule, FormsModule,
    NbCardModule, NbButtonModule, NbInputModule,
    NbFormFieldModule, NbIconModule
  ],
  template: `
    <nb-card>
      <nb-card-header>הגדרות אחסון גלובליות</nb-card-header>
      <nb-card-body>
        <p class="text-muted mb-3">
          ברירת המחדל הגלובלית לנפח אחסון לכל ערוץ. ניתן לשנות לכל ערוץ בנפרד מרשימת הערוצים.
        </p>
        <div class="d-flex align-items-center gap-3">
          <nb-form-field>
            <nb-icon nbPrefix icon="hard-drive-outline"></nb-icon>
            <input nbInput type="number" min="1" step="1"
                  [(ngModel)]="defaultQuotaGb"
                  placeholder="נפח ברירת מחדל (GB)">
          </nb-form-field>
          <span class="text-muted">GB לכל ערוץ</span>
          <button nbButton status="primary" (click)="save()" [disabled]="saving || quotaInvalid">
            {{ saving ? 'שומר...' : 'שמור' }}
          </button>
        </div>
        @if (quotaInvalid) {
          <p class="text-danger mt-2 mb-0"><small>יש להזין נפח ברירת מחדל של 1 GB לפחות.</small></p>
        }
      </nb-card-body>
    </nb-card>
  `
})
export class GlobalStorageComponent implements OnInit {
  // null when the number input is cleared (ngModel posts null, which the
  // server reads as 0).
  defaultQuotaGb: number | null = 5;
  saving = false;

  /**
   * A stored 0 is not "use the default": the upload path treats an effective
   * quota of 0 as unlimited, so an empty or 0 field silently lifted every
   * channel's limit. min="1" on the input is only a hint.
   */
  get quotaInvalid(): boolean {
    const gb = Number(this.defaultQuotaGb);
    return this.defaultQuotaGb === null || this.defaultQuotaGb === undefined
      || !Number.isFinite(gb) || gb < 1;
  }

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService
  ) {}

  async ngOnInit() {
    try {
      const cfg = await this.superAdminService.getGlobalStorageConfig();
      this.defaultQuotaGb = cfg.defaultQuotaGb;
    } catch {
      this.toastr.danger('שגיאה בטעינת הגדרות אחסון', 'שגיאה');
    }
  }

  async save() {
    if (this.quotaInvalid) return;
    this.saving = true;
    try {
      await this.superAdminService.setGlobalStorageConfig(Number(this.defaultQuotaGb));
      this.toastr.success('הגדרות אחסון נשמרו', 'אחסון');
    } catch {
      this.toastr.danger('שגיאה בשמירה', 'שגיאה');
    } finally {
      this.saving = false;
    }
  }
}
