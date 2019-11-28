import { Component, Input } from '@angular/core';

/**
 * Big loading animation that is shown when the contents of a page or modal window
 * can not be shown before getting some data from the backend. It tries to be shown
 * in the middle of its container, but the container must allow it.
 */
@Component({
  selector: 'app-loading-indicator',
  templateUrl: './loading-indicator.component.html',
  styleUrls: ['./loading-indicator.component.scss']
})
export class LoadingIndicatorComponent {
  @Input() showWhite = true;
}
