import { Component, Input } from '@angular/core';

/**
 * Base component for all the modal windows. Its main function is to show the title bar.
 */
@Component({
  selector: 'app-dialog',
  templateUrl: './dialog.component.html',
  styleUrls: ['./dialog.component.scss']
})
export class DialogComponent {
  @Input() headline: string;
  /**
   * Hides the close button.
   */
  @Input() disableDismiss: boolean;
  /**
   * If true, this control adds the contents of the modal window inside a scrollable container.
   * If false, the contents must include its own scrollable container.
   */
  @Input() includeScrollableArea = true;
  /**
   * If true, vertical margins will be added to the content.
   */
  @Input() includeVerticalMargins = true;
}
