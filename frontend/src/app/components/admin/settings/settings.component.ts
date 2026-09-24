import { Component, OnInit } from '@angular/core';
import { AdminService } from '../../../services/admin.service';
import {
  NbButtonModule,
  NbCardModule,
  NbToastrService,
  NbIconModule,
  NbInputModule,
  NbToggleModule,
  NbAccordionModule,
  NbTooltipModule,
} from "@nebular/theme";

import { FormsModule } from '@angular/forms';
import { CommonModule } from '@angular/common';
import { Setting } from '../../../models/setting.model';
import {
  SETTINGS_SCHEMA,
  SettingsCategorySchema,
  SettingFieldSchema,
  getAllKnownKeys,
  toBool,
} from './settings.schema';

interface RegexRule {
  pattern: string;
  replace: string;
}

interface ExtraSetting {
  key: string;
  value: string;
}

@Component({
  selector: 'app-settings',
  imports: [
    CommonModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbInputModule,
    NbToggleModule,
    NbAccordionModule,
    NbTooltipModule,
    FormsModule,
  ],
  templateUrl: './settings.component.html',
  styleUrl: './settings.component.scss'
})
export class SettingsComponent implements OnInit {

  schema: SettingsCategorySchema[] = SETTINGS_SCHEMA;
  values: Record<string, any> = {};
  regexRules: RegexRule[] = [];
  extraSettings: ExtraSetting[] = [];

  setInProgress: boolean = false;

  constructor(
    private adminService: AdminService,
    private tostService: NbToastrService,
  ) { }

  ngOnInit(): void {
    this.adminService.getSettings()
      .then(settings => this.loadFromSettings(settings || []))
      .catch(() => this.tostService.danger('', 'אין הרשאה לצפות בהגדרות'));
  }

  private loadFromSettings(settings: Setting[]) {
    const known = getAllKnownKeys();
    this.values = {};
    this.regexRules = [];
    this.extraSettings = [];

    for (const cat of this.schema) {
      for (const f of cat.fields) {
        if (f.type === 'boolean') this.values[f.key] = false;
        else this.values[f.key] = '';
      }
    }

    for (const s of settings) {
      if (s.key === 'regex-replace') {
        const raw = String(s.value ?? '');
        const idx = regexRuleSeparator(raw);
        if (idx >= 0) {
          this.regexRules.push({
            pattern: unescapeRuleHash(raw.substring(0, idx)),
            replace: raw.substring(idx + 1),
          });
        } else if (raw) {
          this.regexRules.push({ pattern: unescapeRuleHash(raw), replace: '' });
        }
        continue;
      }

      if (known.has(s.key)) {
        const field = this.findField(s.key);
        if (field?.type === 'boolean') {
          this.values[s.key] = toBool(s.value);
        } else {
          this.values[s.key] = s.value === undefined || s.value === null ? '' : String(s.value);
        }
      } else {
        this.extraSettings.push({
          key: s.key,
          value: s.value === undefined || s.value === null ? '' : String(s.value),
        });
      }
    }
  }

  private findField(key: string): SettingFieldSchema | undefined {
    for (const cat of this.schema) {
      const f = cat.fields.find(x => x.key === key);
      if (f) return f;
    }
    return undefined;
  }

  isFieldVisible(field: SettingFieldSchema): boolean {
    if (!field.hideWhen) return true;
    return this.values[field.hideWhen.key] !== field.hideWhen.equals;
  }

  addRegexRule() {
    this.regexRules.push({ pattern: '', replace: '' });
  }

  removeRegexRule(i: number) {
    this.regexRules.splice(i, 1);
  }

  addExtraSetting() {
    this.extraSettings.push({ key: '', value: '' });
  }

  removeExtraSetting(i: number) {
    this.extraSettings.splice(i, 1);
  }

  private buildSettingsArray(): Setting[] {
    const out: Setting[] = [];

    for (const cat of this.schema) {
      for (const f of cat.fields) {
        const v = this.values[f.key];
        if (f.type === 'boolean') {
          if (v === true) out.push({ key: f.key, value: '1' as any });
        } else if (f.type === 'number') {
          if (v !== '' && v !== null && v !== undefined) {
            out.push({ key: f.key, value: String(v) as any });
          }
        } else {
          if (v !== '' && v !== null && v !== undefined) {
            out.push({ key: f.key, value: String(v) as any });
          }
        }
      }
    }

    for (const r of this.regexRules) {
      const p = (r.pattern || '').trim();
      if (!p) continue;
      out.push({ key: 'regex-replace', value: `${escapeRuleHash(p)}#${r.replace ?? ''}` as any });
    }

    for (const e of this.extraSettings) {
      const k = (e.key || '').trim();
      if (!k) continue;
      out.push({ key: k, value: e.value ?? '' as any });
    }

    return out;
  }

  saveSettings() {
    this.setInProgress = true;
    const payload = this.buildSettingsArray();
    this.adminService.setSettings(payload)
      .then(() => this.tostService.success('', 'השינויים נשמרו בהצלחה!'))
      .catch(() => this.tostService.danger('', 'שגיאה בשמירת השינויים'))
      .finally(() => this.setInProgress = false);
  }
}

/**
 * A rule is stored as "<pattern>#<replacement>", and '#' is also an ordinary
 * regex character (a hashtag rule is the obvious case). Splitting at the first
 * bare '#' turned "#(\S+)#**#$1**" into an EMPTY pattern, which the server then
 * applied between every two characters of every message. So a '#' inside the
 * pattern is stored escaped as "\#" — which the regex engine reads as a
 * literal '#' — and the separator is the first '#' that is not escaped.
 * The helpers below mirror splitRegexRule in the backend.
 */
function regexRuleSeparator(raw: string): number {
  for (let i = 0; i < raw.length; i++) {
    if (raw[i] === '\\') { i++; continue; }
    if (raw[i] === '#') return i;
  }
  return -1;
}

function escapeRuleHash(pattern: string): string {
  let out = '';
  for (let i = 0; i < pattern.length; i++) {
    if (pattern[i] === '\\' && i + 1 < pattern.length) { out += pattern[i] + pattern[i + 1]; i++; continue; }
    out += pattern[i] === '#' ? '\\#' : pattern[i];
  }
  return out;
}

function unescapeRuleHash(pattern: string): string {
  return pattern.replace(/\\#/g, '#');
}
