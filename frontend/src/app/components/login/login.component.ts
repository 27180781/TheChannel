import { Component, OnInit } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { AuthService } from '../../services/auth.service';


@Component({
  selector: 'app-login',
  imports: [FormsModule],
  templateUrl: './login.component.html',
  styleUrl: './login.component.scss'
})
export class LoginComponent implements OnInit {
  code: string = '';
  checkUserInfo: boolean = false;
  status!: 'failed';

  constructor(
    private _authService: AuthService,
    private _route: ActivatedRoute,
    private router: Router
  ) { }

  async ngOnInit() {
    this.checkUserInfo = true;

    try {
      await this._authService.loadUserInfo();
      if (this._authService.userInfo) {
        this.checkUserInfo = false;
        this.redirectAfterLogin();
        return;
      }
    } catch {
      // Not logged in — check for OAuth callback params
    }

    this.checkUserInfo = false;

    this._route.queryParams.subscribe(params => {
      if (params['error']) {
        // Google sends the user back with error=access_denied when they cancel
        // the consent screen (and other error codes for a misconfigured app).
        // Only params['code'] used to be inspected, so this showed the login
        // button again as if nothing had happened.
        this.clearOauthState();
        this.status = 'failed';
        return;
      }
      if (params['code'] && params['state'] !== localStorage.getItem('google_oauth_state')) {
        // Google sent us back but the anti-CSRF state does not match what this
        // browser stored (storage cleared, a second tab, a replayed link).
        // Previously this fell through silently and the page just showed the
        // login button again, as if nothing had happened.
        this.status = 'failed';
        return;
      }
      if (params['code'] && params['state'] === localStorage.getItem('google_oauth_state')) {
        this.code = params['code'];
        this.checkUserInfo = true;
        // The state is single-use: once the code is consumed the callback URL
        // must stop matching, otherwise the Back button (after a logout) lands
        // on this history entry and re-posts the already-spent code, which the
        // server rejects with 500 and the user sees a failure they did not cause.
        this.clearOauthState();
        this._authService.login(this.code).then(async () => {
          await this._authService.loadUserInfo();
          this.redirectAfterLogin();
        }).catch(() => {
          this.code = '';
          this.status = 'failed';
          this.checkUserInfo = false;
          alert('התחברות נכשלה, נסה שוב');
        });
      }
    });
  }

  private clearOauthState() {
    try {
      localStorage.removeItem('google_oauth_state');
    } catch {
      // Storage unavailable — nothing was stored to begin with.
    }
  }

  private redirectAfterLogin() {
    // Consumed for every role, before any early return: the super-admin branch
    // used to return first, leaving the stored URL behind for the next
    // (non-admin) login on the same browser, which was then dropped into a
    // channel it never asked for.
    const returnUrl = localStorage.getItem('returnUrl');
    localStorage.removeItem('returnUrl');
    const hasReturnUrl = !!returnUrl && !returnUrl.startsWith('/login');

    if (this._authService.userInfo?.globalRole === 'super_admin') {
      // A super admin who signed in from a channel page wants that page back,
      // not the panel; the panel is only the default.
      if (hasReturnUrl) {
        this.router.navigateByUrl(returnUrl!);
      } else {
        this.router.navigate(['/super-admin']);
      }
      return;
    }

    if (hasReturnUrl) {
      this.router.navigateByUrl(returnUrl!);
      return;
    }

    this.router.navigate(['/channel']);
  }

  login() {
    this._authService.loginWithGoogle();
  }
}
