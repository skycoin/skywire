import { Component, Input } from '@angular/core';

import TimeUtils, { ElapsedTime } from '../../../utils/timeUtils';

/**
 * Button for refreshing the data. It normally is in the tab bar. It also shows how long has
 * it been since the data was updated for the last time.
 */
@Component({
  selector: 'app-refresh-button',
  templateUrl: './refresh-button.component.html',
  styleUrls: ['./refresh-button.component.scss']
})
export class RefreshButtonComponent {
  @Input() set secondsSinceLastUpdate(val: number) {
    this.elapsedTime = TimeUtils.getElapsedTime(val);
  }
  @Input() showLoading: boolean;
  /**
   * Shows an alert icon if there was an error updating the data. It also activates
   * a tooltip in which he user can see how often the system retries to get the data.
   */
  @Input() showAlert: boolean;
  /**
   * How often the system automatically refreshes the data, in seconds.
   */
  @Input() refeshRate = -1;

  elapsedTime: ElapsedTime;
}
