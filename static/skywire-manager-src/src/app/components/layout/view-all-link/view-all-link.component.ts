import { Component, Input } from '@angular/core';

/**
 * Link that is shown at the bottom of the tables to let the user see the rest of the
 * data. It does nothing by itself.
 */
@Component({
  selector: 'app-view-all-link',
  templateUrl: './view-all-link.component.html',
  styleUrls: ['./view-all-link.component.scss']
})
export class ViewAllLinkComponent {
  /**
   * Total number of elements available to show in the table.
   */
  @Input() numberOfElements = 0;
  /**
   * Array with the parts of the route that must be openned by the link. This array must
   * the same that would be usend in the "routerLink" property of an <a> tag.
   */
  @Input() linkParts = [''];
  /**
   * Object with the params to be sent in the query string when navigating.
   */
  @Input() queryParams = {};
}
