import { Component, Input } from '@angular/core';
import TimeUtils from '../../../utils/timeUtils';

@Component({
  selector: 'app-refresh-button',
  templateUrl: './refresh-button.component.html',
  styleUrls: ['./refresh-button.component.scss']
})
export class RefreshButtonComponent {
  @Input() set secondsSinceLastUpdate(val: number) {
    this.updateTextElements = TimeUtils.getElapsedTimeElements(val);
  }
  @Input() showLoading: boolean;
  @Input() showAlert: boolean;
  @Input() refeshRate = -1;

  updateTextElements = ['seconds', '', ''];
}
