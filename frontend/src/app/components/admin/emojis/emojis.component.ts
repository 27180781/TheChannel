import { Component, OnInit } from '@angular/core';
import { ChatService } from '../../../services/chat.service';
import { NbToastrService, NbCardModule, NbButtonModule, NbIconModule, NbListModule } from '@nebular/theme';
import { AdminService } from '../../../services/admin.service';
import { PickerModule } from '@ctrl/ngx-emoji-mart';

@Component({
  selector: 'app-emojis',
  imports: [
    NbCardModule,
    NbButtonModule,
    PickerModule,
    NbIconModule,
    NbListModule
],
  templateUrl: './emojis.component.html',
  styleUrl: './emojis.component.scss'
})
export class EmojisComponent implements OnInit {
  emojis: string[] | undefined = [];

  constructor(
    private chatService: ChatService,
    private adminService: AdminService,
    private toastrService: NbToastrService
  ) { }

  ngOnInit(): void {
    this.chatService.getEmojisList(true)
      .then(emojis => this.emojis = emojis)
      .catch(() => {
        this.toastrService.danger('', 'שגיאה בהגדרת אימוגים');
        this.emojis = undefined;
      });
  }

  setEmojis() {
    // A failed load leaves emojis undefined; saving then would wipe the
    // channel's stored list. An intentionally emptied list ([]) still saves.
    if (!this.emojis) {
      this.toastrService.warning('', 'רשימת האימוגים לא נטענה — לא ניתן לשמור');
      return;
    }

    this.adminService.setEmojis(this.emojis)
      .then(() => {
        this.toastrService.success('', 'אימוגים הוגדרו בהצלחה');
      })
      .catch(() => {
        this.toastrService.danger('', 'שגיאה בהגדרת אימוגים');
      });
  }

  addEmoji(event: any) {
    const emoji = event.emoji.native;
    if (!this.emojis?.includes(emoji)) this.emojis?.push(emoji)
  }

  removeEmoji(emoji: number) {
    this.emojis?.splice(emoji, 1);
  }
}
