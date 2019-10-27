import {Component, Input, OnInit} from '@angular/core';
import { SnackbarService } from '../../../services/snackbar.service';

@Component({
  selector: 'app-copy-to-clipboard-text',
  templateUrl: './copy-to-clipboard-text.component.html',
  styleUrls: ['./copy-to-clipboard-text.component.css']
})
export class CopyToClipboardTextComponent implements OnInit {
  @Input() public short = false;
  @Input() text: string;
  @Input() shortTextLength = 6;
  // @ViewChild(MatMenuTrigger) trigger: MatMenuTrigger;
  tooltipText: string;

  get shortText() {
    const lastTextIndex = this.text.length,
      prefix = this.text.slice(0, this.shortTextLength),
      sufix = this.text.slice((lastTextIndex - this.shortTextLength), lastTextIndex);

    return `${prefix}...${sufix}`;
  }

  constructor(
    private snackbarService: SnackbarService,
  ) {}

  ngOnInit() {
    if (this.short) {
      this.tooltipText = 'copy.click-to-see';
    } else {
      this.tooltipText = 'copy.click-to-copy';
    }
  }

  // @HostListener('click') onClick() {
  //   this.trigger.openMenu();
  // }

  public onCopyToClipboardClicked() {
    this.snackbarService.showDone('copy.copied');
  }
}
