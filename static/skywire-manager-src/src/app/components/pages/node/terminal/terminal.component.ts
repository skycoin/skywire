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
  // Last PK we built the iframe URL for. NodeComponent.currentNode
  // emits on every polling refresh (~every few seconds); rebuilding
  // the SafeResourceUrl on each tick produces a fresh object that
  // Angular's change detection treats as a new src — the iframe
  // reloads, killing the websocket and the active shell session.
  // Only rebuild when the visor PK actually changes.
  private boundPk = '';

  private nodeSub: Subscription;

  constructor(private sanitizer: DomSanitizer) { super(); }

  ngOnInit() {
    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      this.node = node;
      if (node && node.localPk && node.localPk !== this.boundPk) {
        const url = '/pty/' + node.localPk;
        this.iframeUrl = this.sanitizer.bypassSecurityTrustResourceUrl(url);
        // Same-origin so window.location.origin is fine; the
        // hypervisor handler answers /pty/<pk> on the same port.
        this.fullWindowUrl = window.location.origin + url;
        this.boundPk = node.localPk;
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
