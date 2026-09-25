import { uploadErrorMessage } from '../../../services/upload-error';
import { Component, OnInit } from '@angular/core';
import { NbCardModule, NbDialogRef, NbButtonModule, NbSpinnerModule, NbInputModule, NbToastrService, NbPopoverModule } from '@nebular/theme';
import { FormsModule } from '@angular/forms';
import { HttpEventType } from '@angular/common/http';
import { Channel } from '../../../models/channel.model';
import { AdminService } from '../../../services/admin.service';
import { ChatService, Attachment, ChatFile } from '../../../services/chat.service';


@Component({
  selector: 'app-channel-info-form',
  imports: [
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbSpinnerModule,
    NbInputModule,
    NbPopoverModule,
  ],
  templateUrl: './channel-info-form.component.html',
  styleUrl: './channel-info-form.component.scss'
})
export class ChannelInfoFormComponent implements OnInit {

  constructor(
    private chatService: ChatService,
    private adminService: AdminService,
    private toastrService: NbToastrService,
  ) { }

  ngOnInit(): void {
    this.channel = { ...this.chatService.channelInfo };
  }

  attachment!: Attachment;
  channel: Channel = {};
  isSending: boolean = false;

  editChannelInfo() {
    if (!this.channel.name?.trim()) {
      this.toastrService.warning("", "יש להזין שם לערוץ");
      return;
    }
    // While the logo upload is in flight logoUrl still holds the data: preview,
    // which the server rejects (400 'invalid logo URL') with no hint which
    // field is wrong — wait for the server-issued URL instead of posting it.
    if (this.attachment?.uploading) {
      this.toastrService.warning("", "הלוגו עדיין בהעלאה, המתינו לסיום ונסו שוב");
      return;
    }
    this.isSending = true;
    this.chatService.editChannelInfo(
      this.channel.name.trim(),
      this.channel.description || '',
      this.channel.logoUrl || '',
      (this.channel.contact_us || '').trim(),
    ).subscribe({
      next: () => {
        this.isSending = false;
        this.toastrService.success("", "עריכת פרטי ערוץ בוצעה בהצלחה");
        this.chatService.updateChannelInfo();
      },
      error: (err) => {
        this.isSending = false;
        // The server refuses a contact link that is not http(s)/mailto — say
        // which field, rather than a generic failure.
        const text = typeof err?.error === 'string' ? err.error : '';
        if (err?.status === 400 && text.includes('contact')) {
          this.toastrService.danger("", "קישור צור קשר חייב להתחיל ב-https://‎ או ב-mailto:");
          return;
        }
        if (err?.status === 400 && text.includes('too long')) {
          this.toastrService.danger("", "השם או התיאור ארוכים מדי (עד 80 תווים לשם ועד 2000 לתיאור)");
          return;
        }
        // 'invalid logo URL': a data: preview or a non-http(s) address reached
        // the save — picking the logo again is the only way out.
        if (err?.status === 400 && text.includes('logo')) {
          this.toastrService.danger("", "כתובת הלוגו אינה תקינה, בחרו את הלוגו מחדש");
          return;
        }
        this.toastrService.danger("", "עריכת פרטי ערוץ נכשלה");
      }
    });
  };

  onFileSelected(event: Event) {
    const input = event.target as HTMLInputElement;

    if (input.files) {
      this.attachment = { file: input.files[0] }
      // Kept so a failed upload can put the previous logo back: the data: URL
      // preview set below is only ever replaced on success, and leaving it in
      // place made every later save of this dialog fail on 'invalid logo URL'.
      const previousLogoUrl = this.channel.logoUrl;
      const reader = new FileReader();
      reader.readAsDataURL(this.attachment.file);
      reader.onload = (event) => {
        if (event.target) {
          this.channel.logoUrl = event.target.result as string;
        }
      }

      this.uploadFile(this.attachment, previousLogoUrl);
    }
  }

  async uploadFile(attachment: Attachment, previousLogoUrl?: string) {
    try {
      const formData = new FormData();
      if (!attachment.file) return;
      formData.append('file', attachment.file);

      attachment.uploading = true;

      this.adminService.uploadFile(formData).subscribe({
        next: (event) => {
          if (event.type === HttpEventType.UploadProgress) {
            attachment.uploadProgress = Math.round((event.loaded / (event.total || 1)) * 100);
          } else if (event.type === HttpEventType.Response) {
            const uploadedFile: ChatFile | null = event.body || null;
            attachment.uploading = false;
            attachment.uploadProgress = 0;
            if (!uploadedFile) return;
            this.channel.logoUrl = uploadedFile.url;
          }
        },
        error: (error) => {
          this.toastrService.danger("", uploadErrorMessage(error.status));
          attachment.uploading = false;
          // Drop the data: preview — only a server URL may reach the save.
          this.channel.logoUrl = previousLogoUrl;
        },
      });

    } catch (error) {
      this.toastrService.danger("", "שגיאה בהעלאת קובץ");
    }
  }
}