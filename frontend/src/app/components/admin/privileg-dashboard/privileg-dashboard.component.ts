import { Component, OnInit } from '@angular/core';
import { AdminService, ChannelUser } from '../../../services/admin.service';
import { NbButtonModule, NbCardModule, NbInputModule, NbToastrService, NbIconModule, NbSelectModule } from "@nebular/theme";
import { FormsModule } from '@angular/forms';
import { AuthService } from '../../../services/auth.service';
import { SlugService } from '../../../services/slug.service';

// Mirrors normEmail on the server, so the duplicate check here sees what the
// server will actually merge on.
function normalizeEmail(email: string | undefined): string {
  return (email ?? '').trim().toLowerCase();
}

// Deliberately loose: one '@' with something on both sides and a dot after it.
const EMAIL_SHAPE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

@Component({
  selector: 'app-privileg-dashboard',
  imports: [
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    FormsModule,
    NbIconModule,
    NbSelectModule,
  ],
  templateUrl: './privileg-dashboard.component.html',
  styleUrl: './privileg-dashboard.component.scss'
})
export class PrivilegDashboardComponent implements OnInit {

  constructor(
    private adminService: AdminService,
    private toastService: NbToastrService,
    public authService: AuthService,
    private slugService: SlugService,
  ) { }

  usersList: ChannelUser[] = [];
  // Removed users are sent with an empty role so the server actually revokes them —
  // an email that is simply missing from the payload is never touched.
  removedUsers: ChannelUser[] = [];
  addingNewUser = false;
  newUserEmail = '';
  newUserRole: 'moderator' | 'writer' = 'writer';

  readonly roleOptions = [
    { value: 'moderator', label: 'מנהל' },
    { value: 'writer', label: 'כותב' },
  ];

  get isOwner(): boolean {
    const roles = this.authService.userInfo?.channelRoles;
    if (this.authService.userInfo?.globalRole === 'super_admin') return true;
    return roles?.[this.slugService.slug] === 'owner';
  }

  ngOnInit(): void {
    this.adminService.getChannelUsers()
      .then(list => {
        this.usersList = list;
        this.removedUsers = [];
      })
      .catch(() => this.toastService.danger('', 'שגיאה בטעינת המשתמשים'));
  }

  saveChanges() {
    // The server applies the list in order, last write wins — so a removal that
    // trailed a re-grant of the same address ("delete alice, add alice back as
    // writer") revoked her. Removals go first, and one whose address is being
    // granted again is dropped altogether.
    const granted = new Set(this.usersList.map(u => normalizeEmail(u.email)));
    const removals = this.removedUsers.filter(u => !granted.has(normalizeEmail(u.email)));
    this.adminService.setChannelUsers([...removals, ...this.usersList])
      .then(() => {
        this.removedUsers = [];
        this.toastService.success('', 'השינויים נשמרו בהצלחה!');
      })
      .catch(() => this.toastService.danger('', 'שגיאה בשמירת השינויים'));
  }

  deleteUser(index: number) {
    if (!confirm('האם אתה בטוח שברצונך למחוק את המשתמש הזה?')) return;
    const [removed] = this.usersList.splice(index, 1);
    if (removed?.email) this.removedUsers.push({ email: removed.email, role: '' });
  }

  saveNewUser() {
    // The server normalises and silently skips a blank address, and stores an
    // invalid one for good — where it can never match a Google login. Catch the
    // typo here, while the owner is still looking at it.
    const email = normalizeEmail(this.newUserEmail);
    if (!email) {
      this.toastService.warning('', 'יש להזין כתובת מייל');
      return;
    }
    if (!EMAIL_SHAPE.test(email)) {
      this.toastService.warning('', 'כתובת המייל אינה תקינה');
      return;
    }
    if (this.usersList.some(u => normalizeEmail(u.email) === email)) {
      this.toastService.warning('', 'כתובת המייל כבר קיימת ברשימה');
      return;
    }
    this.usersList.push({ email, role: this.newUserRole });
    this.newUserEmail = '';
    this.newUserRole = 'writer';
    this.addingNewUser = false;
  }

  resetNewUser() {
    this.newUserEmail = '';
    this.newUserRole = 'writer';
    this.addingNewUser = false;
  }

  getRoleLabel(role: string): string {
    return this.roleOptions.find(o => o.value === role)?.label ?? role;
  }
}
