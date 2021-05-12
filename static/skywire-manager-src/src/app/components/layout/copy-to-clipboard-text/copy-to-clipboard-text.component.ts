import { Component, Input } from '@angular/core';

import { SnackbarService } from '../../../services/snackbar.service';

/**
 * Shows a text that can be copied by clicking on it. An icon is shown at the end of the text,
 * to indicate the user that the text can be copied by clicking on it. This component can show
 * truncated text, case in which a tooltip allows the user to see the complete text.
 */
@Component({
  selector: 'app-copy-to-clipboard-text',
  templateUrl: './copy-to-clipboard-text.component.html',
  styleUrls: ['./copy-to-clipboard-text.component.scss']
})
export class CopyToClipboardTextComponent {
  @Input() text: string;
  /**
   * Indicates if the text must be truncated to show an specific amount of characters only,
   * if it is too long.
   */
  @Input() public short = false;
  /**
   * Indicates if the text must be truncated at the end, if it is too long.
   */
  @Input() public shortSimple = false;
  /**
   * Number of characters at the left and right of the text that will be shown if "short" is
   * "true". Example: if the text is "Hello word" and this var is set to 2, the component will
   * show "He...rd". If the text has a length less than shortTextLength * 2, the whole text
   * is shown.
   */
  @Input() shortTextLength = 5;

  constructor(
    private snackbarService: SnackbarService,
  ) {}

  public onCopyToClipboardClicked() {
    this.snackbarService.showDone('copy.copied');
  }
}
