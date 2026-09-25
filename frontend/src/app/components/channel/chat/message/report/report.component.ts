import { Component, OnInit } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { NbDialogRef, NbCardModule, NbButtonModule, NbInputModule, NbToastrService } from '@nebular/theme';
import { ChatService } from '../../../../../services/chat.service';

@Component({
  selector: 'app-report',
  imports: [
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    FormsModule
  ],
  templateUrl: './report.component.html',
  styleUrl: './report.component.scss'
})
export class ReportComponent implements OnInit{
  messageId: number | undefined;
  reason: string = '';

  constructor(
    public dialogRef: NbDialogRef<ReportComponent>,
    private chatService: ChatService,
    private toastrService: NbToastrService
  ) { }

  ngOnInit() {
    this.messageId = this.dialogRef.componentRef.instance.messageId;
  }

  reportMessage() {
    if (!this.messageId || !this.reason.trim()) {
      return;
    }

    this.chatService.reportMessage(this.messageId, this.reason)
      .then(() => {
        this.toastrService.success('', 'הדיווח נשלח בהצלחה!');
        this.dialogRef.close();
      })
      .catch((err) => this.toastrService.danger('', reportErrorMessage(err)));
  }
}

/**
 * The server caps a reason at 500 BYTES (report.go); the input's maxlength of
 * 250 characters is the Hebrew ceiling, but emoji weigh four bytes each, so a
 * reason can still overflow and come back 400. A 400 for anything else (the
 * message was deleted meanwhile) keeps the generic text. The body is a plain
 * "reason too long" line, which HttpClient hands over as `error.text` when
 * it fails to parse it as JSON.
 */
function reportErrorMessage(err: any): string {
  const body = typeof err?.error === 'string' ? err.error : String(err?.error?.text ?? '');
  switch (err?.status) {
    case 400: return body.includes('too long') ? 'הסיבה ארוכה מדי' : 'אירעה שגיאה בעת שליחת הדיווח';
    case 429: return 'יותר מדי דיווחים, נסו שוב מאוחר יותר';
    default: return 'אירעה שגיאה בעת שליחת הדיווח';
  }
}
