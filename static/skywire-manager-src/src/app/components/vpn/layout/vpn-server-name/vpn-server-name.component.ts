import { Component, Input } from '@angular/core';

/**
 * Shows the name of a server. It includes the custom name, the original name and the
 * icons for any special condition. The text will take the size of the parent css.
 */
@Component({
  selector: 'app-vpn-server-name',
  templateUrl: './vpn-server-name.component.html',
  styleUrls: ['./vpn-server-name.component.scss']
})
export class VpnServerNameComponent {
  // Special conditions.
  @Input() isCurrentServer = false;
  @Input() isFavorite = false;
  @Input() isBlocked = false;
  @Input() isInHistory = false;
  @Input() hasPassword = false;
  // Names.
  @Input() name = '';
  @Input() customName = '';
  // Text that will be shown if there is no name.
  @Input() defaultName = '';
  // The icons will be positioned for big text.
  @Input() adjustIconsForBigText = false;
}
