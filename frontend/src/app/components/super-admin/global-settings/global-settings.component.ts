import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbInputModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService, Setting } from '../../../services/super-admin.service';

@Component({
  selector: 'app-global-settings',
  standalone: true,
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbIconModule,
  ],
  templateUrl: './global-settings.component.html',
})
export class GlobalSettingsComponent implements OnInit {
  settings: Setting[] = [];
  saving = false;
  loading = true;
  newKey = '';
  newValue = '';
  addingNew = false;

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.superAdminService.getGlobalSettings()
      .then(settings => this.settings = [...(settings || [])])
      .catch(() => this.toastr.danger('', 'שגיאה בטעינת הגדרות גלובליות'))
      .finally(() => this.loading = false);
  }

  removeSetting(index: number) {
    this.settings.splice(index, 1);
  }

  addSetting() {
    const key = this.newKey.trim();
    if (!key) return;
    this.settings.push({ key, value: this.newValue });
    this.newKey = '';
    this.newValue = '';
    this.addingNew = false;
  }

  save() {
    this.saving = true;
    this.superAdminService.setGlobalSettings(this.settings)
      .then(() => this.toastr.success('', 'ההגדרות נשמרו בהצלחה'))
      .catch(() => this.toastr.danger('', 'שגיאה בשמירת ההגדרות'))
      .finally(() => this.saving = false);
  }
}
