export type SettingFieldType = 'boolean' | 'text' | 'number' | 'url' | 'textarea' | 'password';

export interface SettingFieldSchema {
  key: string;
  label: string;
  description?: string;
  type: SettingFieldType;
  placeholder?: string;
  default?: string | number | boolean;
  hideWhen?: { key: string; equals: any };
}

export interface SettingsCategorySchema {
  id: string;
  title: string;
  icon?: string;
  description?: string;
  fields: SettingFieldSchema[];
}

export const SETTINGS_SCHEMA: SettingsCategorySchema[] = [
  {
    id: 'general',
    title: 'הגדרות כלליות',
    icon: 'settings-2-outline',
    fields: [
      {
        key: 'max_file_size',
        label: 'הגבלת גודל קובץ להעלאה (MB)',
        description: 'גודל מקסימלי בקבצים שניתן להעלות לערוץ. ברירת מחדל: 100 MB.',
        type: 'number',
        placeholder: '100',
        default: 100,
      },
    ],
  },
  {
    id: 'security',
    title: 'אבטחה ו-API',
    icon: 'shield-outline',
    fields: [
      {
        key: 'api_secret_key',
        label: 'מפתח API ליבוא הודעות',
        description: 'מפתח סודי שיש לכלול בכותרת X-API-Key בעת קריאות יבוא הודעות.',
        type: 'password',
        placeholder: 'מפתח סודי חזק',
      },
    ],
  },
  {
    id: 'storage',
    title: 'אחסון ומדיה',
    icon: 'hard-drive-outline',
    fields: [
      {
        key: 'tinypng_api_key',
        label: 'מפתח API של TinyPNG לכיווץ תמונות',
        description: 'כשמוגדר מפתח, תמונות PNG/JPEG/WebP ייכוצו אוטומטית לפני העלאה לאחסון ויחסכו מקום. קבלו מפתח חינמי ב-tinypng.com.',
        type: 'password',
        placeholder: 'YOUR_TINYPNG_API_KEY',
      },
    ],
  },
  {
    id: 'ads',
    title: 'פרסומות',
    icon: 'pricetags-outline',
    fields: [
      {
        key: 'ad-iframe-src',
        label: 'קישור HTML של פרסומת להטמעה',
        description: 'הכנסת קישור תפעיל הצגת מסגרת פרסומת בערוץ.',
        type: 'url',
        placeholder: 'https://ad.example.com/banner.html',
      },
      {
        key: 'ad-iframe-width',
        label: 'רוחב חלון הפרסומת (פיקסלים)',
        description: 'רוחב מומלץ: 300.',
        type: 'number',
        placeholder: '300',
      },
    ],
  },
  {
    id: 'webhook',
    title: 'וובהוק (Webhook)',
    icon: 'link-2-outline',
    description: 'שליחת התראה לשרת חיצוני בעת יצירה, עדכון או מחיקה של הודעות.',
    fields: [
      {
        key: 'webhook_url',
        label: 'כתובת ה-Webhook',
        description: 'כתובת ה-URL שאליה תישלח בקשת POST בעת שינוי בהודעות.',
        type: 'url',
        placeholder: 'https://example.com/webhook',
      },
      {
        key: 'webhook_verify_token',
        label: 'טוקן אימות',
        description: 'טוקן סודי שיישלח עם כל בקשה לאימות שהבקשה הגיעה ממערכת זו (מומלץ).',
        type: 'password',
        placeholder: 'your-secret-token',
      },
    ],
  },
  {
    id: 'notifications',
    title: 'התראות דחיפה (Push)',
    icon: 'bell-outline',
    description: 'שליחת התראה לנייד/דפדפן בעת פרסום הודעה חדשה. תשתית ה-FCM מוגדרת על ידי מנהל המערכת.',
    fields: [
      {
        key: 'on_notification',
        label: 'הפעלת התראות דחיפה בערוץ זה',
        description: 'מנויי הערוץ יקבלו התראת Push בכל הודעה חדשה.',
        type: 'boolean',
      },
    ],
  },
];

// The single boolean parser for stored setting values — the backend accepts
// booleans, numbers and strings, so every reader must handle all three.
export function toBool(v: any): boolean {
  if (typeof v === 'boolean') return v;
  if (typeof v === 'number') return v !== 0;
  if (typeof v === 'string') {
    const s = v.toLowerCase().trim();
    return s === '1' || s === 'true' || s === 'yes' || s === 'on';
  }
  return false;
}

// Keys that used to be stored on the channel but are no longer part of the form.
// They are still recognised so they are not shown as raw "extra settings", and
// they are dropped from the payload on the next save.
export const LEGACY_SETTING_KEYS = [
  // Moved to a per-user localStorage preference — the server never read it.
  'enter_sends_message',
  // Saved by an old owner UI but consumed by nothing on the backend.
  'magnet_api_key',
  // Managed by the super-admin channel-features screen, not channel settings.
  'require_auth',
  'require_auth_for_view_files',
  'count_views',
  'contact_us',
];

export function getAllKnownKeys(): Set<string> {
  const keys = new Set<string>();
  for (const cat of SETTINGS_SCHEMA) {
    for (const f of cat.fields) keys.add(f.key);
  }
  for (const k of LEGACY_SETTING_KEYS) keys.add(k);
  keys.add('regex-replace');
  return keys;
}
