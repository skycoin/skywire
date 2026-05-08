import { Component } from '@angular/core';

import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * Routed component for /nodes/:key/terminal. The actual terminal UI
 * (iframe + controls) lives in NodeComponent so the iframe DOM
 * element survives tab switches without reloading the dmsgpty
 * session. This component exists only as the route target and
 * intentionally renders nothing.
 */
@Component({
  selector: 'app-terminal',
  templateUrl: './terminal.component.html',
  styleUrls: ['./terminal.component.scss'],
  standalone: false,
})
export class TerminalComponent extends PageBaseComponent {}
