import { Component, Input } from '@angular/core';

@Component({
  selector: 'app-truncated-text',
  templateUrl: './truncated-text.component.html',
  styleUrls: ['./truncated-text.component.scss']
})
export class TruncatedTextComponent {
  @Input() short = false;
  @Input() showTooltip = true;
  @Input() text: string;
  @Input() shortTextLength = 6;

  get shortText() {
    if (this.text.length > this.shortTextLength * 2) {
      const lastTextIndex = this.text.length,
        prefix = this.text.slice(0, this.shortTextLength),
        sufix = this.text.slice((lastTextIndex - this.shortTextLength), lastTextIndex);

      return `${prefix}...${sufix}`;
    } else {
      return this.text;
    }
  }
}
