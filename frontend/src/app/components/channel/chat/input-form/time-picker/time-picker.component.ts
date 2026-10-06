import { Component } from '@angular/core';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule, NbCardModule, NbDatepickerModule, NbDialogRef, NbIconModule, NbInputModule, NbTooltipModule,
} from '@nebular/theme';
import { MessageTimePipe } from '../../../../../pipes/message-time.pipe';

@Component({
  selector: 'app-time-picker',
  imports: [
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbIconModule,
    NbDatepickerModule,
    NbTooltipModule,
    FormsModule,
    MessageTimePipe,
  ],
  templateUrl: './time-picker.component.html',
  styleUrl: './time-picker.component.scss'
})
export class TimePickerComponent {

  constructor(
    private dialogRef: NbDialogRef<TimePickerComponent>
  ) { }

  date: Date | undefined = undefined;

  /** One-tap choices for the common cases; the picker below stays for anything else. */
  readonly presets: { label: string; at: () => Date }[] = [
    { label: 'בעוד שעה', at: () => new Date(Date.now() + 60 * 60 * 1000) },
    // Past 20:00 the "evening" slot is already tomorrow's; the label says so.
    { label: new Date().getHours() < 20 ? 'הערב ב-20:00' : 'מחר ב-20:00', at: () => this.nextAt(20) },
    { label: 'מחר ב-08:00', at: () => this.nextAt(8, true) },
  ];

  timeChange(event: Date) {
    this.date = event;
  }

  pickPreset(preset: { at: () => Date }) {
    this.date = preset.at();
  }

  isPreset(preset: { at: () => Date }): boolean {
    return !!this.date && Math.abs(this.date.getTime() - preset.at().getTime()) < 60 * 1000;
  }

  // The next occurrence of HH:00 — today if still ahead, otherwise tomorrow.
  private nextAt(hour: number, tomorrow = false): Date {
    const d = new Date();
    d.setHours(hour, 0, 0, 0);
    if (tomorrow || d.getTime() <= Date.now()) d.setDate(d.getDate() + 1);
    return d;
  }

  // Cancel closes with undefined on purpose: the composer keeps whatever
  // schedule it already had, rather than silently turning the message live.
  close(ok: boolean = false) {
    ok ? this.dialogRef.close(this.date) : this.dialogRef.close();
  }
}
