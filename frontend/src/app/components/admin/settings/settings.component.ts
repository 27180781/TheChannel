import { Component, OnInit } from '@angular/core';
import { AdminService } from '../../../services/admin.service';
import { AdsService } from '../../../services/ads.service';
import {
  NbAlertModule,
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
  PASSTHROUGH_SETTING_KEYS,
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
    NbAlertModule,
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
  // Settings another tab owns (magnet ads): never shown, re-sent as loaded.
  private passthrough: Setting[] = [];

  setInProgress: boolean = false;
  // Save stays disabled until the server copy arrived: the form starts empty,
  // and settings/set replaces the whole blob (no merge), so a save after a
  // failed load wiped api_secret_key, webhook_*, regex rules and magnet_*.
  loaded = false;
  loadFailed = false;
  // Super-admin lock on the iframe ad: the ad-iframe-* fields still save, but
  // the public endpoint serves the global src/width and they are ignored.
  adsLocked = false;

  private static readonly AD_IFRAME_KEYS = new Set(['ad-iframe-src', 'ad-iframe-width']);

  constructor(
    private adminService: AdminService,
    private adsService: AdsService,
    private tostService: NbToastrService,
  ) { }

  ngOnInit(): void {
    this.loadSettings();
    // The public ads endpoint is the only place the lock is visible to an
    // owner. A failure here just leaves the hint off; the form still loads.
    this.adsService.getAds()
      .then(ad => this.adsLocked = !!ad?.locked)
      .catch(() => { /* hint only */ });
  }

  loadSettings() {
    this.loadFailed = false;
    this.adminService.getSettings()
      .then(settings => {
        this.loadFromSettings(settings || []);
        this.loaded = true;
      })
      .catch(() => {
        this.loadFailed = true;
        this.tostService.danger('', 'אין הרשאה לצפות בהגדרות');
      });
  }

  /** Only the two iframe-ad fields are frozen by the ads lock. */
  isFieldLocked(field: SettingFieldSchema): boolean {
    return this.adsLocked && SettingsComponent.AD_IFRAME_KEYS.has(field.key);
  }

  private loadFromSettings(settings: Setting[]) {
    const known = getAllKnownKeys();
    const passthroughKeys = new Set<string>(PASSTHROUGH_SETTING_KEYS);
    this.values = {};
    this.regexRules = [];
    this.extraSettings = [];
    this.passthrough = [];

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

      // Checked before `known`: a known key with no form field is a legacy one
      // and is dropped on save, which is exactly what must not happen here.
      if (passthroughKeys.has(s.key)) {
        this.passthrough.push(s);
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

    // Re-appended verbatim so a save here never drops the magnet tab's keys.
    out.push(...this.passthrough);

    return out;
  }

  saveSettings() {
    if (!this.loaded) return;
    this.setInProgress = true;
    const payload = this.buildSettingsArray();
    this.adminService.setSettings(payload)
      .then(() => this.tostService.success('', 'השינויים נשמרו בהצלחה!'))
      .catch((err) => this.tostService.danger('', this.saveErrorText(err)))
      .finally(() => this.setInProgress = false);
  }

  /**
   * validateSettings on the server answers 400 with a plain-text English
   * reason that names the offending key ("webhook_url: must be an absolute
   * http(s) URL", "regex-replace: invalid pattern: …", "max_file_size: must be
   * between 1 and 512 MB"). Matched on here, never shown as-is.
   */
  private saveErrorText(err: any): string {
    const text = typeof err?.error === 'string' ? err.error : '';
    if (err?.status === 400) {
      if (text.includes('webhook_url')) return 'כתובת ה-Webhook חייבת להתחיל ב-http:// או https://';
      if (text.includes('ad-iframe-src')) return 'כתובת ה-iframe חייבת להתחיל ב-http:// או https://';
      if (text.includes('regex')) return 'אחד מכללי ההחלפה (regex) אינו תקין';
      if (text.includes('max_file_size')) return 'גודל הקובץ המרבי חייב להיות בין 1 ל-512 MB';
    }
    return 'שגיאה בשמירת השינויים';
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
