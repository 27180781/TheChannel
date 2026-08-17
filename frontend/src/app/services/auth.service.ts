import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { firstValueFrom } from 'rxjs';
import { ResponseResult } from '../models/response-result.model';
import { User } from '../models/user.model';
interface GoogleAuthValues {
  googleOauthUrl: string;
  googleOauthScope: string;
  googleClientId: string;
}

@Injectable({
  providedIn: 'root'
})
export class AuthService {
  public userInfo?: User;

  constructor(
    private _http: HttpClient,
  ) { }

  async loginWithGoogle() {
    try {
      const googleAuthValues: GoogleAuthValues = await firstValueFrom(this._http.get<GoogleAuthValues>('/auth/google'));
      const state = crypto.randomUUID()
      const params = new URLSearchParams({
        client_id: googleAuthValues.googleClientId,
        redirect_uri: window.location.origin + '/login',
        scope: googleAuthValues.googleOauthScope,
        state: state,
        response_type: 'code',
        access_type: 'offline',
      });

      localStorage.setItem('google_oauth_state', state);
      window.location.href = `${googleAuthValues.googleOauthUrl}?${params.toString()}`;
    } catch (err: any) {
      throw err;
    }
  }

  async login(code: string) {
    try {
      let res = await firstValueFrom(this._http.post<ResponseResult>('/auth/login', { code }));
      return res.success;
    } catch (err: any) {
      this.userInfo = undefined;
      throw err;
    }
  }

  async logout() {
    let res = await firstValueFrom(this._http.post<ResponseResult>('/auth/logout', {}));
    if (res.success) {
      this.userInfo = undefined;
    }
    return res.success;
  }

  async loadUserInfo() {
    try {
      this.userInfo = this.userInfo || await firstValueFrom(this._http.get<User>('/api/user-info'))
    } catch (err: any) {
      this.userInfo = undefined;
      throw err;
    }
    return this.userInfo;
  }

  /**
   * loadUserInfo() memoises `userInfo`, so after something changes the user's
   * roles server-side (opening a channel makes them its owner) a plain re-read
   * hands back the stale object. Drop the cache first and refetch.
   */
  async reloadUserInfo() {
    this.userInfo = undefined;
    return this.loadUserInfo();
  }

  // Fire-and-forget visit registration: the channel-scoped user-info handler is
  // what writes the viewer into channel:<slug>:registered_emails, which feeds
  // the participant counter. Errors (e.g. anonymous visitor) are irrelevant.
  registerChannelVisit(slug: string): void {
    firstValueFrom(this._http.get<User>(`/api/channel/${slug}/user-info`)).catch(() => { });
  }
}
