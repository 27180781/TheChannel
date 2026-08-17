import { Component, Input, Optional } from '@angular/core';
import {
  NbAccordionModule,
  NbAlertModule,
  NbButtonModule,
  NbCardModule,
  NbDialogRef,
  NbIconModule,
} from '@nebular/theme';

/**
 * The owner's manual. Pure content — it holds no state and calls no endpoint,
 * so it can be dropped into the admin panel as a tab and opened as a dialog
 * from the post-creation screen without any wiring.
 *
 * Everything documented here was read off the real components; when a screen
 * changes, this file changes with it.
 */
@Component({
  selector: 'app-guide',
  standalone: true,
  imports: [
    NbAccordionModule,
    NbCardModule,
    NbIconModule,
    NbButtonModule,
    NbAlertModule,
  ],
  templateUrl: './guide.component.html',
  styleUrl: './guide.component.scss',
})
export class GuideComponent {
  /**
   * Set by the caller that opens the guide as its own dialog. It is not derived
   * from the injected NbDialogRef: the admin panel is itself a dialog, so a
   * ref is in scope even when the guide is only a tab inside it — and closing
   * that ref would close the whole panel.
   */
  @Input() dialogMode = false;

  constructor(@Optional() private dialogRef: NbDialogRef<GuideComponent> | null) { }

  /** Example URL built from the real origin — the domain is never hardcoded. */
  readonly channelUrlExample = `${window.location.origin}/channel/my-channel`;

  close(): void {
    if (this.dialogMode) this.dialogRef?.close();
  }
}
