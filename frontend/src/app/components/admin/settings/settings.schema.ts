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
        key: 'custom_title',
        label: 'כותרת מותאמת אישית',
        description: 'כותרת שתשמש לקידום האתר בתוצאות חיפוש (SEO).',
        type: 'text',
        placeholder: 'לדוגמא: הערוץ החדשותי שלי',
      },
      {
        key: 'contact_us',
        label: 'קישור ליצירת קשר',
        description: 'הזנת קישור תפעיל כפתור "צור קשר" שיפנה לקישור זה.',
        type: 'url',
        placeholder: 'https://example.com/contact',
      },
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
    id: 'auth',
    title: 'הזדהות ואבטחה',
    icon: 'shield-outline',
    fields: [
      {
        key: 'require_auth',
        label: 'חיוב הזדהות לכניסה לערוץ',
        description: 'משתמשים יחויבו להתחבר לפני שיוכלו לצפות בערוץ.',
        type: 'boolean',
      },
      {
        key: 'require_auth_for_view_files',
        label: 'חיוב הזדהות לצפייה בתמונות וסרטונים',
        description: 'גם אם הערוץ פתוח לצפייה, ניתן לחייב הזדהות לפני צפייה בקבצים.',
        type: 'boolean',
      },
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
    id: 'views',
    title: 'מונה צפיות',
    icon: 'eye-outline',
    fields: [
      {
        key: 'count_views',
        label: 'הפעלת ספירת צפיות בהודעות',
        description: 'מציג ליד כל הודעה את מספר הצפיות שנספרו עבורה.',
        type: 'boolean',
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
    description: 'מבוסס על שירות Firebase Cloud Messaging (FCM) של גוגל.',
    fields: [
      {
        key: 'on_notification',
        label: 'הפעלת התראות דחיפה',
        description: 'יש להפעיל ולהזין את כל הפרטים מ-Firebase שבהמשך.',
        type: 'boolean',
      },
      {
        key: 'project_domain',
        label: 'דומיין הפרויקט (להפניית לחיצה על התראה)',
        description: 'כתובת ה-URL שאליה ינותב המשתמש כשילחץ על התראה.',
        type: 'url',
        placeholder: 'https://example.com',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'vapid',
        label: 'מפתח VAPID',
        description: 'Cloud Messaging > Web Push certificates > Key pair',
        type: 'password',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_api_key',
        label: 'apiKey',
        description: 'General > SDK setup and configuration > apiKey',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_auth_domain',
        label: 'authDomain',
        description: 'General > SDK setup and configuration > authDomain',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_project_id',
        label: 'projectId',
        description: 'General > SDK setup and configuration > projectId',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_storage_bucket',
        label: 'storageBucket',
        description: 'General > SDK setup and configuration > storageBucket',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_messaging_sender_id',
        label: 'messagingSenderId',
        description: 'General > SDK setup and configuration > messagingSenderId',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_app_id',
        label: 'appId',
        description: 'General > SDK setup and configuration > appId',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_measurement_id',
        label: 'measurementId',
        description: 'General > SDK setup and configuration > measurementId',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
    ],
  },
  {
    id: 'fcm_json',
    title: 'חשבון שירות FCM (Service Account)',
    icon: 'file-text-outline',
    description: 'שדות אלה מגיעים מקובץ ה-JSON שנוצר תחת serviceaccounts > Generate new private key. ניתן להדביק את כל קובץ ה-JSON בשדה הראשון לשם מילוי אוטומטי של כל השדות.',
    fields: [
      {
        key: 'fcm_json_type',
        label: 'type',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_json_project_id',
        label: 'project_id',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_json_private_key_id',
        label: 'private_key_id',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_json_private_key',
        label: 'private_key',
        type: 'textarea',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_json_client_email',
        label: 'client_email',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_json_client_id',
        label: 'client_id',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_json_auth_uri',
        label: 'auth_uri',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_json_token_uri',
        label: 'token_uri',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_json_auth_provider_x509_cert_url',
        label: 'auth_provider_x509_cert_url',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_json_client_x509_cert_url',
        label: 'client_x509_cert_url',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
      },
      {
        key: 'fcm_json_universe_domain',
        label: 'universe_domain',
        type: 'text',
        hideWhen: { key: 'on_notification', equals: false },
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
