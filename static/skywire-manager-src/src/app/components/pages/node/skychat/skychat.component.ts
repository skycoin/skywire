import { Component, OnDestroy, OnInit, ViewChild, ElementRef, AfterViewChecked, ChangeDetectorRef } from '@angular/core';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { ApiService } from 'src/app/services/api.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { environment } from 'src/environments/environment';

/**
 * Skychat tab inside the visor detail page. Talks to the local
 * skychat HTTP server through the hypervisor's reverse-proxy at
 *   /api/visors/<pk>/skychat/proxy/<rest>
 * which forwards to localhost:<skychat-port>/<rest>. Same-origin
 * (the hvui's port) so no CORS dance, and the proxy attaches an
 * internal token that bypasses any password gate skychat has
 * configured for its standalone :8001 surface.
 *
 * Minimal feature set:
 *   - Live message stream via SSE (works whether or not persistence
 *     is enabled in skychat).
 *   - Compose box: paste a peer PK + write a message + send.
 *   - Message log groups successive messages from the same peer.
 *   - Optional history fetch when --persist is enabled on skychat
 *     (silently skips when the backend returns 503).
 */

interface ChatMessage {
  peer: string;       // remote PK
  direction: 'in' | 'out';
  text: string;
  // Local sender timestamp for outgoing; SSE doesn't carry one for
  // incoming (we capture arrival time client-side).
  ts: number;
}

@Component({
  selector: 'app-skychat',
  templateUrl: './skychat.component.html',
  styleUrls: ['./skychat.component.scss'],
  standalone: false,
})
export class SkychatComponent extends PageBaseComponent implements OnInit, OnDestroy, AfterViewChecked {
  @ViewChild('logEl') logEl: ElementRef<HTMLDivElement>;

  node: Node;
  // Bound to the compose form.
  toPK = '';
  message = '';
  network = 'skynet';
  sending = false;
  // Display state.
  messages: ChatMessage[] = [];
  connected = false;
  errorText = '';
  // Skychat returns 503 for /history* unless --persist is on. We
  // detect the case once and stop trying.
  historyAvailable = true;

  // Track scroll-tail like the runtime-logs viewer.
  private wasAtBottom = true;
  private es: EventSource | null = null;
  private nodeSub: any;

  // --- Password gate management state. ----------------------------
  // Whether the password section is expanded.
  pwOpen = false;
  // Whether a password is currently set on the visor (drives copy /
  // which fields are shown).
  pwIsSet = false;
  // Form fields. oldPassword is required when pwIsSet, ignored otherwise.
  pwOld = '';
  pwNew = '';
  pwConfirm = '';
  // In-flight indicator for the apply / clear action.
  pwBusy = false;

  // Distinct peers seen so far, in last-touched order. Drives the
  // sidebar list. Recomputed lazily when messages change.
  get peers(): string[] {
    const seen: string[] = [];
    const have = new Set<string>();
    // Iterate newest-first so the most recently active peer is on top.
    for (let i = this.messages.length - 1; i >= 0; i--) {
      const pk = this.messages[i].peer;
      if (!pk || have.has(pk)) { continue; }
      have.add(pk);
      seen.push(pk);
    }
    return seen;
  }

  constructor(
    private api: ApiService,
    private snackbar: SnackbarService,
    private cdr: ChangeDetectorRef,
  ) {
    super();
  }

  ngOnInit() {
    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      const wasUnset = !this.node;
      this.node = node;
      if (wasUnset) {
        this.connectSSE();
        this.tryLoadPeers();
        this.refreshPasswordState();
      }
    });
    return super.ngOnInit();
  }

  ngOnDestroy() {
    if (this.nodeSub) { this.nodeSub.unsubscribe(); }
    this.disconnectSSE();
  }

  ngAfterViewChecked() {
    if (this.wasAtBottom && this.logEl) {
      const el = this.logEl.nativeElement;
      el.scrollTop = el.scrollHeight;
    }
  }

  /** Build the proxy URL for a skychat path. */
  private proxyUrl(path: string): string {
    const apiPrefix = !environment.production && location.protocol.indexOf('http:') !== -1 ? 'http-api' : 'api';
    return `/${apiPrefix}/visors/${this.node.localPk}/skychat/proxy/${path.replace(/^\/+/, '')}`;
  }

  private connectSSE() {
    if (!this.node || this.es) { return; }
    try {
      this.es = new EventSource(this.proxyUrl('sse'));
      this.es.onopen = () => { this.connected = true; this.errorText = ''; this.cdr.markForCheck(); };
      this.es.onerror = () => { this.connected = false; this.errorText = 'Disconnected — retrying…'; this.cdr.markForCheck(); };
      this.es.onmessage = (ev) => this.handleSSE(ev.data);
    } catch (e: any) {
      this.errorText = `SSE setup failed: ${e?.message || e}`;
    }
  }

  private disconnectSSE() {
    if (this.es) {
      this.es.close();
      this.es = null;
    }
  }

  /** Skychat /sse emits a stringified JSON {sender, message} payload
   *  per data: line. Capture arrival as 'in' regardless of who sent
   *  — sender is the peer's PK which is what we want to display. */
  private handleSSE(raw: string) {
    let data: any = null;
    try { data = JSON.parse(raw); } catch { /* ignore */ }
    if (!data || typeof data !== 'object') { return; }
    const msg: ChatMessage = {
      peer: data.sender || data.from || '',
      direction: 'in',
      text: typeof data.message === 'string' ? data.message : (data.text || ''),
      ts: Date.now(),
    };
    if (!msg.peer || !msg.text) { return; }
    this.captureScroll();
    this.messages.push(msg);
    if (this.messages.length > 500) { this.messages.shift(); }
    this.cdr.markForCheck();
  }

  /** Send the composed message. */
  send() {
    if (this.sending) { return; }
    const recipient = this.toPK.trim();
    const text = this.message.trim();
    if (!recipient || !text) { return; }
    if (recipient.length !== 66 || !/^[0-9a-fA-F]+$/.test(recipient)) {
      this.snackbar.showError('Recipient must be a 66-char hex public key');
      return;
    }
    this.sending = true;
    fetch(this.proxyUrl('message'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ recipient, message: text, network: this.network }),
    }).then(async (resp) => {
      if (!resp.ok) {
        const body = await resp.text();
        throw new Error(body || `HTTP ${resp.status}`);
      }
      this.captureScroll();
      this.messages.push({ peer: recipient, direction: 'out', text, ts: Date.now() });
      if (this.messages.length > 500) { this.messages.shift(); }
      this.message = '';
      this.cdr.markForCheck();
    }).catch((err) => {
      this.snackbar.showError(err?.message || String(err));
    }).finally(() => {
      this.sending = false;
      this.cdr.markForCheck();
    });
  }

  /** Try to seed the message list from skychat's history. Silently
   *  skips when persistence isn't enabled (skychat returns 503). */
  private tryLoadPeers() {
    if (!this.node || !this.historyAvailable) { return; }
    fetch(this.proxyUrl('history?limit=100'))
      .then(async (resp) => {
        if (resp.status === 503) {
          this.historyAvailable = false;
          return null;
        }
        if (!resp.ok) { throw new Error(`HTTP ${resp.status}`); }
        return resp.json();
      })
      .then((rows: any) => {
        if (!Array.isArray(rows)) { return; }
        const seeded: ChatMessage[] = rows.map((m: any): ChatMessage => ({
          peer: m.peer || m.sender || '',
          direction: m.direction === 'out' ? 'out' : 'in',
          text: m.message || m.text || '',
          ts: m.timestamp || m.ts || Date.now(),
        })).filter((m: ChatMessage) => m.peer && m.text);
        // Prepend so existing live tail keeps its order.
        this.messages = seeded.concat(this.messages);
        this.cdr.markForCheck();
      })
      .catch(() => { /* network glitch — live SSE will pick up new traffic anyway */ });
  }

  /** Truncate a 66-char PK for the display column. */
  shortPK(pk: string): string {
    if (!pk || pk.length < 14) { return pk || ''; }
    return pk.slice(0, 8) + '…' + pk.slice(-4);
  }

  pickRecipient(pk: string) {
    this.toPK = pk;
  }

  private captureScroll() {
    if (!this.logEl) { this.wasAtBottom = true; return; }
    const el = this.logEl.nativeElement;
    this.wasAtBottom = (el.scrollHeight - el.scrollTop - el.clientHeight) < 40;
  }

  // --- Password gate management ----------------------------------

  togglePassword() {
    this.pwOpen = !this.pwOpen;
    if (this.pwOpen) {
      this.refreshPasswordState();
    } else {
      this.resetPasswordForm();
    }
  }

  private refreshPasswordState() {
    if (!this.node) { return; }
    this.api.get(`visors/${this.node.localPk}/skychat/password`).subscribe(
      (resp: any) => {
        this.pwIsSet = !!(resp && resp.set);
        this.cdr.markForCheck();
      },
      () => { /* leave previous state — the form still works */ },
    );
  }

  private resetPasswordForm() {
    this.pwOld = '';
    this.pwNew = '';
    this.pwConfirm = '';
  }

  private validateNewPassword(): string | null {
    if (this.pwNew.length < 6 || this.pwNew.length > 64) {
      return 'skychat.password.errors.length';
    }
    if (this.pwNew !== this.pwConfirm) {
      return 'skychat.password.errors.mismatch';
    }
    return null;
  }

  applyPassword() {
    if (!this.node || this.pwBusy) { return; }
    const err = this.validateNewPassword();
    if (err) { this.snackbar.showError(err); return; }
    this.pwBusy = true;
    this.api.put(`visors/${this.node.localPk}/skychat/password`, {
      old_password: this.pwIsSet ? this.pwOld : '',
      new_password: this.pwNew,
    }).subscribe(
      () => {
        this.pwBusy = false;
        this.pwIsSet = true;
        this.resetPasswordForm();
        this.snackbar.showDone('skychat.password.saved');
        this.cdr.markForCheck();
      },
      (e: any) => {
        this.pwBusy = false;
        this.snackbar.showError(e?.originalError?.error?.error || e?.message || String(e));
        this.cdr.markForCheck();
      },
    );
  }

  clearPassword() {
    if (!this.node || this.pwBusy || !this.pwIsSet) { return; }
    if (!this.pwOld) { this.snackbar.showError('skychat.password.errors.old-required'); return; }
    this.pwBusy = true;
    this.api.delete(`visors/${this.node.localPk}/skychat/password?old_password=${encodeURIComponent(this.pwOld)}`).subscribe(
      () => {
        this.pwBusy = false;
        this.pwIsSet = false;
        this.resetPasswordForm();
        this.snackbar.showDone('skychat.password.cleared');
        this.cdr.markForCheck();
      },
      (e: any) => {
        this.pwBusy = false;
        this.snackbar.showError(e?.originalError?.error?.error || e?.message || String(e));
        this.cdr.markForCheck();
      },
    );
  }
}
