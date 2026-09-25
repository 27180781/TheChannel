import { Component, EventEmitter, OnInit, Output } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbInputModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService, ChannelData } from '../../../services/super-admin.service';
import { SLUG_PATTERN } from '../../../services/channel.service';

@Component({
  selector: 'app-channels-list',
  standalone: true,
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbIconModule,
  ],
  templateUrl: './channels-list.component.html',
  styleUrl: './channels-list.component.scss',
})
export class ChannelsListComponent implements OnInit {
  @Output() editFeatures = new EventEmitter<string>();
  @Output() manageUsers = new EventEmitter<string>();
  @Output() manageStorage = new EventEmitter<string>();

  channels: ChannelData[] = [];
  showCreateForm = false;
  newSlug = '';
  newName = '';
  newOwnerEmail = '';
  creating = false;
  // Slugs whose DELETE is in flight: the button stays clickable otherwise, and
  // a second click sent another DELETE that answered 404 once the first had
  // finished — "deleted" and "failed to delete" toasts for the same channel.
  deleting = new Set<string>();

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.loadChannels();
  }

  loadChannels() {
    this.superAdminService.getChannels()
      .then(channels => this.channels = channels)
      .catch(() => this.toastr.danger('', 'שגיאה בטעינת ערוצים'));
  }

  createChannel() {
    // The same normalisation the self-service form applies as the user types:
    // the server's slug regex is lowercase-only, so " My Channel" was refused
    // with an English 400 while the operator saw nothing wrong with it.
    this.newSlug = this.newSlug.trim().toLowerCase().replace(/\s+/g, '-').replace(/[^a-z0-9-]/g, '');
    if (!this.newSlug || !this.newName || !this.newOwnerEmail) {
      this.toastr.warning('', 'יש למלא את כל השדות');
      return;
    }
    if (!SLUG_PATTERN.test(this.newSlug)) {
      this.toastr.warning('', 'מזהה לא תקין — 3 עד 50 תווים, אותיות אנגליות קטנות, ספרות ומקפים');
      return;
    }
    this.creating = true;
    this.superAdminService.createChannel(this.newSlug, this.newName, this.newOwnerEmail)
      .then(channel => {
        this.channels.push(channel);
        this.showCreateForm = false;
        this.newSlug = '';
        this.newName = '';
        this.newOwnerEmail = '';
        this.toastr.success('', 'הערוץ נוצר בהצלחה');
      })
      .catch((err) => this.toastr.danger('', this.createErrorText(err)))
      .finally(() => this.creating = false);
  }

  /**
   * The server answers failures as plain English text (http.Error). This path
   * says "Invalid slug…", "Slug is reserved", "Invalid owner email", "Channel
   * already exists"; the self-service path it mirrors says "invalid slug",
   * "slug is reserved", "slug already taken", "another channel is already
   * being created". Matched case-insensitively, never shown as-is.
   */
  private createErrorText(err: any): string {
    const text = (typeof err?.error === 'string' ? err.error : '').toLowerCase();
    switch (err?.status) {
      case 409:
        return text.includes('already being created')
          ? 'יצירת ערוץ אחר עדיין בתהליך, נסו שוב בעוד רגע'
          : 'המזהה (slug) כבר תפוס, בחרו מזהה אחר';
      case 400:
        if (text.includes('reserved')) return 'המזהה (slug) שמור למערכת, בחרו מזהה אחר';
        if (text.includes('slug')) return 'מזהה לא תקין — 3 עד 50 תווים, אותיות אנגליות קטנות, ספרות ומקפים';
        if (text.includes('email')) return 'כתובת המייל של הבעלים אינה תקינה';
        if (text.includes('too long')) return 'שם הערוץ ארוך מדי (עד 80 תווים)';
        if (text.includes('name is required')) return 'יש להזין שם לערוץ';
        return 'הפרטים שהוזנו אינם תקינים';
      default:
        return 'שגיאה ביצירת הערוץ';
    }
  }

  deleteChannel(slug: string) {
    if (this.deleting.has(slug)) return;
    // Say what is actually destroyed — the server wipes the whole tenant.
    if (!confirm(`האם למחוק את הערוץ "${slug}"?\n\nכל ההודעות, הקבצים וההרשאות של הערוץ יימחקו לצמיתות ולא ניתן יהיה לשחזר אותם.`)) return;
    this.deleting.add(slug);
    this.superAdminService.deleteChannel(slug)
      .then(() => {
        this.channels = this.channels.filter(c => c.slug !== slug);
        this.toastr.success('', 'הערוץ נמחק בהצלחה');
      })
      .catch(() => this.toastr.danger('', 'שגיאה במחיקת הערוץ'))
      .finally(() => this.deleting.delete(slug));
  }

  cancelCreate() {
    this.showCreateForm = false;
    this.newSlug = '';
    this.newName = '';
    this.newOwnerEmail = '';
  }
}
