import { Component, Input } from '@angular/core';

/**
 * Allows to show truncated text. If the text is truncated, a tooltip allows the user
 * to see the complete text.
 */
@Component({
  selector: 'app-truncated-text',
  templateUrl: './truncated-text.component.html',
  styleUrls: ['./truncated-text.component.scss']
})
export class TruncatedTextComponent {
  /**
   * Indicates if the text must be truncated if it is too long.
   */
  @Input() short = false;
  /**
   * Allow to deactivate the tooltip.
   */
  @Input() showTooltip = true;
  @Input() text: string;
  /**
   * Number of characters at the left and right of the text that will be shown if "short" is
   * "true". Example: if the text is "Hello word" and this var is set to 2, the component will
   * show "He...rd". If the text has a length less than shortTextLength * 2, the whole text
   * is shown.
   */
  @Input() shortTextLength = 5;

  get shortText() {
    if (this.text.length > this.shortTextLength * 2) {
      const lastTextIndex = this.text.length;
      const prefix = this.text.slice(0, this.shortTextLength);
      const sufix = this.text.slice((lastTextIndex - this.shortTextLength), lastTextIndex);

      return `${prefix}...${sufix}`;
    } else {
      return this.text;
    }
  }
}
