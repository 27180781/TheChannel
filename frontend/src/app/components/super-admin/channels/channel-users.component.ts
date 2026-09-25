import { Component, Input, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbInputModule,
  NbSelectModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService, ChannelUser } from '../../../services/super-admin.service';

/** The backend normalises emails (trim + lowercase) before comparing. */
function sameEmail(a: string, b: string): boolean {
  return a.trim().toLowerCase() === b.trim().toLowerCase();
}

// Deliberately loose, and the same shape the owner-side privileges screen
// checks: one '@' with something on both sides and a dot after it.
const EMAIL_SHAPE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

@Component({
  selector: 'app-channel-users',
  standalone: true,
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbIconModule,
    NbSelectModule,
  ],
  templateUrl: './channel-users.component.html',
})
export class ChannelUsersComponent implements OnInit {
  @Input() slug!: string;

  users: ChannelUser[] = [];
  // Removed users are sent with an empty role so the server actually revokes them —
  // an email that is simply missing from the payload is never touched.
  removedUsers: ChannelUser[] = [];
  saving = false;
  addingUser = false;
  newEmail = '';
  newRole: 'owner' | 'moderator' | 'writer' | '' = 'moderator';

  roleOptions: { value: string; label: string }[] = [
    { value: 'owner', label: 'בעלים' },
    { value: 'moderator', label: 'מנהל' },
    { value: 'writer', label: 'כותב' },
  ];

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.loadUsers();
  }

  loadUsers() {
    this.superAdminService.getChannelUsers(this.slug)
      .then(users => {
        this.users = [...users];
        this.removedUsers = [];
      })
      .catch(() => this.toastr.danger('', 'שגיאה בטעינת משתמשי הערוץ'));
  }

  addUser() {
    const email = this.newEmail.trim();
    if (!email) {
      this.toastr.warning('', 'יש להזין כתובת אימייל');
      return;
    }
    // A typo is caught while the operator is still looking at it; stored, it
    // would never match a Google login and the row would just sit there.
    if (!EMAIL_SHAPE.test(email)) {
      this.toastr.warning('', 'כתובת המייל אינה תקינה');
      return;
    }
    if (this.users.some(u => sameEmail(u.email, email))) {
      this.toastr.warning('', 'המשתמש כבר ברשימה — שנו את התפקיד שלו בשורה הקיימת');
      return;
    }
    // Remove-then-re-add before saving is how a role change is done here; the
    // queued revoke must not survive, or (applied after the grant) it would
    // silently strip the user while the screen still shows them with a role.
    this.removedUsers = this.removedUsers.filter(u => !sameEmail(u.email, email));
    this.users.push({ email, role: this.newRole as any });
    this.newEmail = '';
    this.newRole = 'moderator';
    this.addingUser = false;
  }

  removeUser(index: number) {
    const [removed] = this.users.splice(index, 1);
    if (removed?.email) this.removedUsers.push({ email: removed.email, role: '' });
  }

  save() {
    this.saving = true;
    // The server applies changes in order and the last one for an email wins,
    // so revokes go first and a revoke of an email that is also granted is
    // dropped — a grant must never be undone by an earlier removal.
    const removals = this.removedUsers.filter(
      r => !this.users.some(u => sameEmail(u.email, r.email)),
    );
    this.superAdminService.setChannelUsers(this.slug, [...removals, ...this.users])
      .then(() => {
        this.removedUsers = [];
        this.toastr.success('', 'המשתמשים נשמרו בהצלחה');
      })
      .catch((err) => {
        // The server refuses a malformed address with 400 'invalid email' —
        // name the field rather than a generic failure.
        const text = typeof err?.error === 'string' ? err.error.toLowerCase() : '';
        this.toastr.danger('', err?.status === 400 && text.includes('invalid email')
          ? 'כתובת מייל לא תקינה'
          : 'שגיאה בשמירת המשתמשים');
      })
      .finally(() => this.saving = false);
  }

  getRoleLabel(role: string): string {
    const opt = this.roleOptions.find(o => o.value === role);
    return opt ? opt.label : role;
  }
}
