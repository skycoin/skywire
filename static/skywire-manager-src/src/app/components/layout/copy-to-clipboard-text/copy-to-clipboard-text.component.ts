import {Component, Input} from '@angular/core';
import { SnackbarService } from '../../../services/snackbar.service';

@Component({
  selector: 'app-copy-to-clipboard-text',
  templateUrl: './copy-to-clipboard-text.component.html',
  styleUrls: ['./copy-to-clipboard-text.component.css']
})
export class CopyToClipboardTextComponent {
  @Input() public short = false;
  @Input() text: string;
  @Input() shortTextLength = 5;
  // @ViewChild(MatMenuTrigger) trigger: MatMenuTrigger;

  constructor(
    private snackbarService: SnackbarService,
  ) {}

  // @HostListener('click') onClick() {
  //   this.trigger.openMenu();
  // }

  public onCopyToClipboardClicked() {
    this.snackbarService.showDone('copy.copied');
  }
}
