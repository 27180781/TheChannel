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
      {
        key: 'enter_sends_message',
        label: 'Enter שולח הודעה',
        description: 'לחיצה על Enter תשלח את ההודעה. לחיצה על Ctrl+Enter תוריד שורה.',
        type: 'boolean',
        default: false,
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

export const FCM_JSON_KEY_MAP: Record<string, string> = {
  type: 'fcm_json_type',
  project_id: 'fcm_json_project_id',
  private_key_id: 'fcm_json_private_key_id',
  private_key: 'fcm_json_private_key',
  client_email: 'fcm_json_client_email',
  client_id: 'fcm_json_client_id',
  auth_uri: 'fcm_json_auth_uri',
  token_uri: 'fcm_json_token_uri',
  auth_provider_x509_cert_url: 'fcm_json_auth_provider_x509_cert_url',
  client_x509_cert_url: 'fcm_json_client_x509_cert_url',
  universe_domain: 'fcm_json_universe_domain',
};

export function getAllKnownKeys(): Set<string> {
  const keys = new Set<string>();
  for (const cat of SETTINGS_SCHEMA) {
    for (const f of cat.fields) keys.add(f.key);
  }
  keys.add('regex-replace');
  return keys;
}
