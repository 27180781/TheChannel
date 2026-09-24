import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { NbToastrService } from '@nebular/theme';
import { firstValueFrom } from 'rxjs';
import { FirebaseApp, FirebaseOptions, getApp, getApps, initializeApp } from 'firebase/app';
import { getMessaging, onMessage, getToken, isSupported } from 'firebase/messaging';
import { ResponseResult } from '../models/response-result.model';
import { SlugService } from './slug.service';


interface NotificationsConfig {
  enableNotifications: boolean,
  vapid?: string,
  firebaseConfig?: FirebaseOptions,
}

@Injectable({
  providedIn: 'root'
})
export class NotificationsService {
  public initialized = false;
  private app: FirebaseApp | null = null;
  private messaging: any;
  public config: NotificationsConfig | null = null;

  constructor(
    private http: HttpClient,
    private tostrService: NbToastrService,
    private slugService: SlugService,
  ) { }

  // Cleared on channel switch: the push toggle is per channel, so the state
  // fetched for the previous channel must not leak into the next one.
  reset() {
    this.initialized = false;
    this.config = null;
    this.app = null;
    this.messaging = undefined;
  }

  async init() {
    // Called at bootstrap too, before any channel slug is resolved.
    if (!this.slugService.slug) return;
    if (this.initialized) return;

    await firstValueFrom(this.http.get<NotificationsConfig>(`/api/channel/${this.slugService.slug}/notifications-config`))
      .then((config) => {
        this.config = config;
      });

    if (!this.config) return;

    if (this.config.enableNotifications) {
      if (!this.config.firebaseConfig) return;

      // Safari on iOS outside a home-screen app, and any browser without
      // service workers or the Push API, has no Messaging at all: getMessaging()
      // throws there, and since nobody awaits init() the throw surfaced as an
      // unhandled rejection on every channel load. Ask first, and keep the bell
      // hidden where it could never work.
      if (!(await isSupported().catch(() => false))) return;

      try {
        // initializeApp() refuses a second default app with different options;
        // reuse the one from a previous channel visit instead of tripping it.
        this.app = getApps().length ? getApp() : initializeApp(this.config.firebaseConfig);
        this.messaging = getMessaging(this.app);
      } catch (err) {
        console.warn('Push notifications unavailable in this browser', err);
        return;
      }
      this.initialized = true;

      onMessage(this.messaging, (payload) => {
        //this.tostrService.success("", 'התראה חדשה!');
      });
      return;
    }
    return;
  }


  async requestPermission() {
    if (typeof Notification === 'undefined' || !this.messaging) {
      this.tostrService.warning("", 'הדפדפן הזה אינו תומך בהתראות');
      return;
    }

    // Permission is per browser origin, and every channel on this platform
    // shares one origin. Being 'granted' therefore only says the browser may
    // show notifications — it says nothing about THIS channel, whose
    // subscription list is separate. Enabling push on one channel and then
    // tapping the bell on another used to answer "already subscribed" while
    // never registering the device with the second channel at all.
    if (Notification.permission === 'granted') {
      this.subscribeThisDevice('ההתראות לערוץ זה פעילות');
      return;
    }

    // Once denied, the browser answers 'denied' straight away without asking
    // again — so without this the bell simply did nothing when tapped.
    if (Notification.permission === 'denied') {
      this.tostrService.warning("", 'ההתראות חסומות בהגדרות הדפדפן לאתר זה');
      return;
    }

    Notification.requestPermission()
      .then((permission) => {
        if (permission === 'granted') {
          this.subscribeThisDevice('התראות הופעלו בהצלחה!');
        }
      });
  }

  /**
   * Registers this device's push token with the current channel. Safe to
   * repeat: the server keeps one entry per token.
   */
  private subscribeThisDevice(successMessage: string): void {
    getToken(this.messaging, {
      vapidKey: this.config?.vapid,
    })
      .then((currentToken) => {
        if (currentToken) {
          this.subscribeNotifications(currentToken)
            .then((success) => {
              if (success) {
                this.tostrService.success("", successMessage);
              } else {
                this.tostrService.danger("", 'שגיאה בהגדרת התראות!');
              }
            })
            .catch(() => {
              this.tostrService.danger("", 'שגיאה בהגדרת התראות!');
            });
        } else {
          this.tostrService.danger("", 'שגיאה בהגדרת התראות!');
        }
      })
      .catch(() => {
        this.tostrService.danger("", 'שגיאה בהגדרת התראות!');
      });
  }

  async subscribeNotifications(token: string): Promise<boolean> {
    if (!token) return false;
    const res = await firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slugService.slug}/notifications-subscribe`, { token }));
    return res.success;
  }

}