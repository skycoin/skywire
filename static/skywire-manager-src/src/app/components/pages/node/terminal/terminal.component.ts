import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription } from 'rxjs';
import { DomSanitizer, SafeResourceUrl } from '@angular/platform-browser';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * Per-visor Terminal tab. Iframes the dmsgpty UI that the
 * hypervisor already serves at /pty/<pk> (that route handles both
 * the HTML page and the WebSocket upgrade for terminal I/O), so
 * the tab is essentially a thin frame wrapper plus an "open in a
 * new window" link for full-screen use.
 *
 * The /pty/<pk> route works for both the local visor (CLI socket)
 * and remote visors (DMSG) — same UX either way.
 */
@Component({
  selector: 'app-terminal',
  templateUrl: './terminal.component.html',
  styleUrls: ['./terminal.component.scss'],
  standalone: false,
})
export class TerminalComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  iframeUrl: SafeResourceUrl | null = null;
  fullWindowUrl = '';

  private nodeSub: Subscription;

  constructor(private sanitizer: DomSanitizer) { super(); }

  ngOnInit() {
    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      this.node = node;
      if (node) {
        const url = '/pty/' + node.localPk;
        this.iframeUrl = this.sanitizer.bypassSecurityTrustResourceUrl(url);
        // Same-origin so window.location.origin is fine; the
        // hypervisor handler answers /pty/<pk> on the same port.
        this.fullWindowUrl = window.location.origin + url;
      }
    });
    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.nodeSub?.unsubscribe();
  }

  openFullWindow() {
    if (!this.fullWindowUrl) { return; }
    window.open(this.fullWindowUrl, '_blank', 'noopener noreferrer');
  }
}
