import { Component, Input } from '@angular/core';

@Component({
  selector: 'app-refresh-button',
  templateUrl: './refresh-button.component.html',
  styleUrls: ['./refresh-button.component.scss']
})
export class RefreshButtonComponent {
  @Input() set secondsSinceLastUpdate(val: number) {
    if (val < 60) {
      this.updateTextElements = ['seconds', ''];
    } else if (val >= 60 && val < 120) {
      this.updateTextElements = ['minute', ''];
    } else if (val >= 120 && val < 3600) {
      this.updateTextElements = ['minutes', Math.floor(val / 60).toString()];
    } else if (val >= 3600 && val < 7200) {
      this.updateTextElements = ['hour', ''];
    } else {
      this.updateTextElements = ['hours', Math.floor(val / 3600).toString()];
    }
  }
  @Input() showLoading: boolean;
  @Input() showAlert: boolean;
  @Input() refeshRate = -1;

  updateTextElements = ['seconds', ''];
}
