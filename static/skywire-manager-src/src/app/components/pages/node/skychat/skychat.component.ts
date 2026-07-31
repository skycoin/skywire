import { Component, OnDestroy, OnInit, ViewChild, ElementRef, AfterViewChecked, ChangeDetectorRef } from '@angular/core';
import { firstValueFrom } from 'rxjs';

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
  // id is the message's own envelope id (stable, addressable). replyTo is the
  // id of the message this one quotes (a threaded reply). Both empty for plain
  // pre-envelope messages. /sse carries reply_to_id, /history + the wasm
  // skychatMessages() buffer carry reply_to.
  id?: string;
  replyTo?: string;
  // deleted-for-everyone: rendered as a "message deleted" placeholder.
  deleted?: boolean;
  // Delivery-status tick on our OWN (direction==='out') DM bubbles, advanced by
  // dm-status receipts: 'sent' (✓) → 'received' (✓✓, peer's app acked) →
  // 'read' (✓✓ blue, peer's UI displayed it). Undefined on incoming bubbles
  // and on the wasm path (which doesn't surface per-message receipts yet).
  status?: string;
}

/** One federated group/room as surfaced by the group list. */
interface GroupView {
  id: string;
  name: string;
  mode: string;       // 'public' | 'private'
  role: string;       // 'owner' | 'member'
  members: number;
  status: string;     // 'active' | 'pending' | 'left' | 'revoked'
}

/** One ringing inbound voice call awaiting an answer. */
interface IncomingCall {
  id: string;         // call id (to answer/decline)
  from: string;       // caller PK hex ('' when the transport doesn't carry it)
}

/** One group message (any sender) in a room's log. */
interface GroupMsg {
  group_id: string;
  from: string;       // sender PK hex
  text: string;
  ts: number;         // unix ms
  out?: boolean;      // sender === this visor
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
  // Quoted-reply compose target (the message the next send quotes), or null.
  replyTarget: ChatMessage | null = null;
  // id -> text index for resolving a reply's quoted-parent snippet in O(1)
  // from the template, kept in sync as messages arrive.
  private msgById = new Map<string, string>();
  // A dm-status receipt can race ahead of the /message response that first tells
  // us our own bubble's wire id. Stash such orphans by id (bounded) and claim
  // them once the outgoing bubble lands. See applyReceipt / claimOrphanReceipt.
  private orphanReceipts = new Map<string, string>();
  connected = false;
  errorText = '';
  // Skychat returns 503 for /history* unless --persist is on. We
  // detect the case once and stop trying.
  historyAvailable = true;

  // Track scroll-tail like the runtime-logs viewer.
  private wasAtBottom = true;
  private es: EventSource | null = null;
  private nodeSub: any;

  // In-browser wasm visor: skychat runs in-process and is reached through the
  // skywireVisor.skychat* JS hooks (poll skychatMessages, send via skychatSend)
  // instead of the native SSE proxy — which the wasm core doesn't serve, so the
  // EventSource would just sit "Disconnected — retrying…". Set on connect.
  private wasmChat = false;
  private pollTimer: any = null;
  private lastPollLen = -1;
  // ids of inbound wasm messages we've already sent a read receipt for, so the
  // per-poll ack sweep doesn't re-send on every tick.
  private wasmReadSent = new Set<string>();

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

  // --- Group chat state. -----------------------------------------
  // 'dm' = the existing 1:1 view; 'groups' = federated rooms.
  chatMode: 'dm' | 'groups' = 'dm';
  groups: GroupView[] = [];
  selectedGroup: GroupView | null = null;
  groupMessages: GroupMsg[] = [];
  groupMessage = '';
  groupSending = false;
  groupError = '';
  // Create/join panel state.
  showCreate = false;
  showJoin = false;
  newGroupName = '';
  newGroupMode: 'public' | 'private' = 'public';
  joinInvite = '';
  // Group-detail state.
  addMemberPk = '';
  inviteLink = '';
  private groupTimer: any = null;
  private lastGroupMsgLen = -1;
  // Locally-echoed sent messages per group id. The federated data plane does
  // NOT echo a sender its own messages (each member subscribes to the OTHER
  // members' feeds), so we keep an optimistic local copy and merge it into the
  // polled room log by timestamp.
  private sentByGroup: { [id: string]: GroupMsg[] } = {};

  // --- Voice call state. -----------------------------------------
  // Mirrors the DM/group transport split: a native visor drives the voice
  // HTTP bridge (/skychat/voice/*), a wasm visor the in-process
  // skywireVisor.skychatVoice* hooks. Both surface the same shapes here.
  voiceActive: string[] = [];                 // active call ids
  voiceIncoming: IncomingCall[] = [];         // ringing inbound calls
  voiceBusy = false;                          // a call/answer is in flight
  voiceAvailable = true;                      // false when the visor has no voice (503)
  micMuted = false;                           // our mic muted (peer hears silence)
  spkMuted = false;                           // caller muted (we hear nothing)
  private callStartMs = 0;                    // wall-clock when the current call went active
  private voiceTimer: any = null;
  // Remember which ringing calls we've already surfaced, so re-polls don't
  // re-prompt (native auto-answer never rings; explicit-answer mode does).
  private voiceRingSeen = new Set<string>();

  // --- In-call audio visualization -------------------------------
  // Default: a Telegram-style scrolling level meter (peer + you). Toggle to a
  // scrolling spectrogram (FFT heatmap of the received audio). Data comes from
  // the call's audio tap — over the HTTP bridge on a native visor, the
  // skychatVoiceLevels/Audio JS hooks on a wasm visor.
  @ViewChild('vizCanvas') vizCanvas?: ElementRef<HTMLCanvasElement>;
  @ViewChild('vizBig') vizBig?: ElementRef<HTMLCanvasElement>;        // incoming (peer)
  @ViewChild('vizBigSent') vizBigSent?: ElementRef<HTMLCanvasElement>; // outgoing (you)
  showSpectrogram = false;   // small in-bar canvas: spectrogram vs level meter
  bigViz = false;            // expand a large spectrogram into the message area
  private vizTimer: any = null;
  private lvlSent: number[] = [];   // rolling recent RMS history (0..1)
  private lvlRecv: number[] = [];
  private readonly lvlMax = 96;     // history length (columns)

  // Distinct peers seen so far, in last-touched order. Drives the
  // sidebar list. Recomputed lazily when messages change.
  get peers(): string[] {
    const seen: string[] = [];
    const have = new Set<string>();
    // Iterate newest-first so the most recently active peer is on top.
    for (let i = this.messages.length - 1; i >= 0; i--) {
      const pk = this.messages[i].peer;
      if (!pk || have.has(pk)) {
 continue; 
}
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
    // Deep-link peer preselect (?peer=<pk>): the wasm desktop's ☰ Chat window
    // and ?chat=<pk> deep links open this tab with the conversation target
    // filled in. Hash routing keeps the query inside location.hash, so parse
    // it directly rather than via ActivatedRoute (which only sees the primary
    // outlet's params for the embedded case).
    const hashQuery = (window.location.hash.split('?')[1] || '');
    const peerParam = new URLSearchParams(hashQuery).get('peer') || '';
    if (/^[0-9a-fA-F]{66}$/.test(peerParam)) {
      this.toPK = peerParam;
    }

    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      const wasUnset = !this.node;
      this.node = node;
      if (wasUnset) {
        this.connectSSE();
        this.tryLoadPeers();
        this.refreshPasswordState();
        this.startVoicePoll();
      }
    });

    return super.ngOnInit();
  }

  ngOnDestroy() {
    if (this.nodeSub) {
 this.nodeSub.unsubscribe(); 
}
    this.disconnectSSE();
    this.stopGroupPoll();
    this.stopVoicePoll();
    this.stopViz();
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
    if (!this.node || this.es || this.pollTimer) {
 return; 
}
    // In-browser wasm visor → use the in-process skychat hooks (poll), not SSE.
    const sv = (window as any).skywireVisor;
    this.wasmChat = (this.node as any).arch === 'wasm' && !!sv && typeof sv.skychatMessages === 'function';
    if (this.wasmChat) {
 this.connectWasm(sv);

 return; 
}
    try {
      this.es = new EventSource(this.proxyUrl('sse'));
      this.es.onopen = () => {
 this.connected = true; this.errorText = ''; this.cdr.markForCheck(); 
};
      this.es.onerror = () => {
 this.connected = false; this.errorText = 'Disconnected — retrying…'; this.cdr.markForCheck(); 
};
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
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
  }

  /** In-browser wasm skychat: poll skychatMessages() (JSON [{from,text,ts,out}],
   *  newest last) and mirror it into the message list. The in-process skychat
   *  rides dmsg:1, so force the network label to dmsg. */
  private connectWasm(sv: any) {
    this.network = 'dmsg';
    const handle = (raw: any) => {
      let arr: any[];
      // skychatMessages() returns the JSON of the message buffer, which is the
      // string "null" (not "[]") when the buffer is a nil slice — JSON.parse
      // gives null, so coalesce to [] or the poll would bail and never connect.
      try {
 arr = JSON.parse(raw || '[]') || []; 
} catch {
        this.connected = false; this.errorText = 'chat hook error'; this.cdr.markForCheck();

 return;
      }
      if (!Array.isArray(arr)) {
 return; 
}
      this.connected = true; this.errorText = '';
      // Rebuild on any length change OR when a message flips deleted/receipt
      // status in place (delete-for-everyone blanks a message, a received/read
      // receipt advances a tick — neither changes the count). Combined signature
      // = length*1e6 + deleted-count*1e3 + receipt-weight; the buffer is small so
      // this stays cheap. Newest last, so the tail stays scrolled.
      const delCount = arr.reduce((n, m) => n + (m.deleted ? 1 : 0), 0);
      const statWeight = arr.reduce((n, m) => n + (m.status === 'read' ? 2 : (m.status === 'received' ? 1 : 0)), 0);
      const sig = arr.length * 1000000 + delCount * 1000 + statWeight;
      if (sig !== this.lastPollLen) {
        this.captureScroll();
        this.messages = arr.slice(-500).map(m => {
return {
          peer: m.from || '', direction: m.out ? 'out' : 'in', text: m.text || '', ts: m.ts || Date.now(),
          id: m.id || '', replyTo: m.reply_to || '', deleted: !!m.deleted, status: m.status || '',
        }
});
        this.rebuildMsgIndex();
        this.lastPollLen = sig;
        this.cdr.markForCheck();
        // Ack inbound messages we haven't yet, so the sender's tick turns blue.
        // Track sent ids so we don't re-send the receipt on every poll.
        for (const m of this.messages) {
          if (m.direction === 'in' && m.id && !m.deleted && !this.wasmReadSent.has(m.id)) {
            this.wasmReadSent.add(m.id);
            Promise.resolve(sv.skychatReadReceipt(m.peer, m.id)).catch(() => { /* peer offline */ });
          }
        }
      }
    };
    // Under the SharedWorker architecture skywireVisor is a MessagePort proxy,
    // so skychatMessages() returns a Promise (not the raw JSON string the Go
    // side returns synchronously in the single-file build). Promise.resolve()
    // normalizes both — calling JSON.parse on the Promise object is what threw
    // "chat hook error" before, same fix as the group hooks.
    const poll = () => {
      Promise.resolve(sv.skychatMessages())
        .then(handle)
        .catch(() => {
 this.connected = false; this.errorText = 'chat hook error'; this.cdr.markForCheck(); 
});
    };
    poll();
    this.pollTimer = setInterval(poll, 1500);
  }

  /** Skychat /sse emits a stringified JSON {sender, message} payload
   *  per data: line. Capture arrival as 'in' regardless of who sent
   *  — sender is the peer's PK which is what we want to display. */
  private handleSSE(raw: string) {
    let data: any = null;
    try {
 data = JSON.parse(raw);
} catch { /* ignore */ }
    if (!data || typeof data !== 'object') {
 return;
}
    // dm-status control events (delivery/read receipts + delete-for-everyone)
    // carry no message body; handle them separately so they aren't dropped by
    // the empty-text guard below. 'deleted' tombstones the bubble by its wire id
    // (whoever authored it); 'received'/'read' advance our own bubble's tick.
    if (data.channel === 'dm-status') {
      if (data.status === 'deleted' && data.id) {
        const dm = this.messages.find(m => m.id && m.id === data.id);
        if (dm) {
          dm.deleted = true;
          dm.text = '';
          this.cdr.markForCheck();
        }
      } else if ((data.status === 'received' || data.status === 'read') && data.id) {
        this.applyReceipt(data.id, data.status);
      }

      return;
    }
    const msg: ChatMessage = {
      peer: data.sender || data.from || '',
      direction: 'in',
      text: typeof data.message === 'string' ? data.message : (data.text || ''),
      ts: Date.now(),
      id: data.id || '',
      replyTo: data.reply_to_id || '',
    };
    if (!msg.peer || !msg.text) {
 return;
}
    this.captureScroll();
    this.indexMsg(msg);
    this.messages.push(msg);
    if (this.messages.length > 500) {
 this.messages.shift();
}
    this.cdr.markForCheck();
    // The bubble is now on screen — send a read receipt so the sender's tick
    // advances to 'read'. Best-effort; the wasm path has no /read-receipt proxy.
    if (msg.id && msg.peer && !this.wasmChat) {
      this.sendReadReceipt(msg.peer, msg.id);
    }
  }

  /** POST /read-receipt so the peer's bubble advances to 'read'. Best-effort. */
  private sendReadReceipt(peer: string, id: string) {
    fetch(this.proxyUrl('read-receipt'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ pk: peer, ids: [id] }),
    }).catch(() => { /* peer may be offline; the tick just stays at 'received' */ });
  }

  /** Apply a delivery receipt to our own bubble by wire id. Never downgrades a
   *  bubble that already reached 'read' (a late 'received' is ignored). If the
   *  bubble isn't known yet (receipt raced the /message response), stash it. */
  private applyReceipt(id: string, status: string) {
    const m = this.messages.find(x => x.id === id && x.direction === 'out');
    if (!m) {
      this.orphanReceipts.set(id, status);
      if (this.orphanReceipts.size > 64) {
        this.orphanReceipts.delete(this.orphanReceipts.keys().next().value as string);
      }

      return;
    }
    if (m.status === 'read') {
      return;
    }
    m.status = status;
    this.cdr.markForCheck();
  }

  /** Claim a receipt that arrived before this outgoing bubble had its id. */
  private claimOrphanReceipt(m: ChatMessage) {
    if (!m.id) {
      return;
    }
    const status = this.orphanReceipts.get(m.id);
    if (!status) {
      return;
    }
    this.orphanReceipts.delete(m.id);
    m.status = status;
    this.cdr.markForCheck();
  }

  /** Tick glyph for an outgoing bubble's delivery status (WhatsApp-style). */
  tickGlyph(status?: string): string {
    return (status === 'received' || status === 'read') ? '✓✓' : '✓';
  }

  /** Human-readable tooltip for the delivery tick. */
  tickTitle(status?: string): string {
    if (status === 'read') {
      return 'Read';
    }
    if (status === 'received') {
      return 'Received';
    }

    return 'Sent';
  }

  /** Send the composed message. */
  send() {
    if (this.sending) {
 return; 
}
    const recipient = this.toPK.trim();
    const text = this.message.trim();
    if (!recipient || !text) {
 return; 
}
    if (recipient.length !== 66 || !/^[0-9a-fA-F]+$/.test(recipient)) {
      this.snackbar.showError('Recipient must be a 66-char hex public key');

      return;
    }
    this.sending = true;
    // In-browser wasm visor: send through the in-process skychat hook. The sent
    // message is buffered with out:true, so the poll loop renders it — no manual
    // push needed here.
    const replyToId = this.replyTarget ? (this.replyTarget.id || '') : '';
    if (this.wasmChat) {
      const sv = (window as any).skywireVisor;
      // 3rd arg = network. Honor the UI's network selector exactly like the
      // native path does (it was hardcoded 'dmsg' here — a wasm/native
      // divergence). The default is skynet: a routed transport survives dmsg
      // session churn, and measured FASTER than the direct dmsg stream when a
      // route is already up (7ms vs 178ms in live wasm→native testing).
      // 4th arg = reply_to (the quoted message's id); the sent message is
      // buffered with reply_to + out:true, so the poll loop renders it threaded.
      Promise.resolve(sv.skychatSend(recipient, text, this.network, replyToId))
        .then(() => {
 this.message = ''; this.cancelReply();
})
        .catch((e: any) => this.snackbar.showError(this.groupErrMsg(e)))
        .finally(() => {
 this.sending = false; this.cdr.markForCheck();
});

      return;
    }
    // receipts:true wraps the send in an id'd envelope and returns {ok,id} at
    // once, so we can track this bubble as the peer's received/read receipts
    // arrive later on the dm-status stream.
    const payload: any = { recipient: recipient, message: text, network: this.network, receipts: true };
    if (replyToId) {
 payload.reply_to = replyToId;
}
    fetch(this.proxyUrl('message'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }).then(async (resp) => {
      if (!resp.ok) {
        const body = await resp.text();
        throw new Error(body || `HTTP ${resp.status}`);
      }
      const res = await resp.json().catch(() => ({} as any));
      const wireId = (res && res.id) || '';
      this.captureScroll();
      const out: ChatMessage = { peer: recipient, direction: 'out', text: text, ts: Date.now(), id: wireId, replyTo: replyToId, status: 'sent' };
      this.messages.push(out);
      this.indexMsg(out);
      this.claimOrphanReceipt(out);
      if (this.messages.length > 500) {
 this.messages.shift();
}
      this.message = '';
      this.cancelReply();
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
    // wasm skychat has no /history proxy; the poll loop seeds messages instead.
    if (!this.node || !this.historyAvailable || this.wasmChat) {
 return; 
}
    fetch(this.proxyUrl('history?limit=100'))
      .then(async (resp) => {
        if (resp.status === 503) {
          this.historyAvailable = false;

          return null;
        }
        if (!resp.ok) {
 throw new Error(`HTTP ${resp.status}`); 
}

        return resp.json();
      })
      .then((rows: any) => {
        if (!Array.isArray(rows)) {
 return; 
}
        const seeded: ChatMessage[] = rows.map((m: any): ChatMessage => {
return {
          peer: m.peer || m.sender || '',
          direction: (m.outgoing || m.direction === 'out') ? 'out' : 'in',
          text: m.message || m.text || '',
          ts: m.timestamp || m.ts || Date.now(),
          id: m.id || '',
          replyTo: m.reply_to || '',
        }
}).filter((m: ChatMessage) => m.peer && m.text);
        // Prepend so existing live tail keeps its order.
        this.messages = seeded.concat(this.messages);
        this.rebuildMsgIndex();
        this.cdr.markForCheck();
      })
      .catch(() => { /* network glitch — live SSE will pick up new traffic anyway */ });
  }

  pickRecipient(pk: string) {
    this.toPK = pk;
  }

  private indexMsg(m: ChatMessage) {
    if (m.id) {
 this.msgById.set(m.id, m.text);
}
  }

  private rebuildMsgIndex() {
    this.msgById.clear();
    for (const m of this.messages) {
      if (m.id) {
 this.msgById.set(m.id, m.text);
}
    }
  }

  /** Quoted-parent snippet for a reply, resolved by id (O(1) from the index). */
  replyParentText(id: string): string {
    const t = this.msgById.get(id);
    if (!t) {
 return '…';
}

    return t.length > 80 ? t.slice(0, 80) + '…' : t;
  }

  /** Arm the next send as a quoted reply to m (id-bearing messages only). */
  setReplyTo(m: ChatMessage) {
    if (!m || !m.id) {
 return;
}
    this.replyTarget = m;
    this.cdr.markForCheck();
  }

  /** Delete one of our own messages for everyone. Sends a chat-delete (wasm hook
   *  or the native /delete proxy) and tombstones the bubble optimistically; the
   *  peer drops it on their side via the controller's OnDelete. */
  deleteMsg(m: ChatMessage) {
    if (!m || !m.id) {
 return;
}
    const pk = m.peer;
    if (this.wasmChat) {
      const sv = (window as any).skywireVisor;
      Promise.resolve(sv.skychatDelete(pk, m.id)).catch(() => {});
    } else {
      fetch(this.proxyUrl('delete'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ pk: pk, id: m.id }),
      }).catch(() => {});
    }
    m.deleted = true;
    m.text = '';
    this.cdr.markForCheck();
  }

  /** Disarm the reply compose state. */
  cancelReply() {
    this.replyTarget = null;
    this.cdr.markForCheck();
  }

  private captureScroll() {
    if (!this.logEl) {
 this.wasAtBottom = true;

 return; 
}
    const el = this.logEl.nativeElement;
    this.wasAtBottom = (el.scrollHeight - el.scrollTop - el.clientHeight) < 40;
  }

  // --- Group chat -------------------------------------------------
  // Transport split mirrors the DM path: an in-browser wasm visor drives
  // the in-process skywireVisor.skychatGroup* JS hooks; a native visor
  // goes over the hypervisor group HTTP bridge (/skychat/groups*). Both
  // return the same shapes so the UI below is transport-agnostic.

  /** window.skywireVisor when this node is an in-browser wasm visor with the
   *  group hooks present, else null. */
  private get groupSv(): any {
    const sv = (window as any).skywireVisor;
    if (this.node && (this.node as any).arch === 'wasm' && sv && typeof sv.skychatGroupList === 'function') {
      return sv;
    }

    return null;
  }

  /** Native group-bridge call over ApiService so CSRF/auth are handled
   *  (the hypervisor group POST routes enforce the CSRF token, unlike the
   *  DM /skychat/proxy passthrough). Returns a Promise to unify with the
   *  wasm hooks. */
  private groupApi(path: string, method: 'GET' | 'POST', body?: any): Promise<any> {
    const url = `visors/${this.node.localPk}/skychat/groups${path}`;
    const obs = method === 'POST' ? this.api.post(url, body || {}) : this.api.get(url);

    return firstValueFrom(obs);
  }

  /** Extract a human message from an ApiService error. */
  private groupErrMsg(e: any): string {
    return e?.originalError?.error?.error || e?.error?.error || e?.message || String(e);
  }

  switchMode(mode: 'dm' | 'groups') {
    if (this.chatMode === mode) {
 return; 
}
    this.chatMode = mode;
    if (mode === 'groups') {
      this.refreshGroups();
      this.startGroupPoll();
    } else {
      this.stopGroupPoll();
    }
  }

  private startGroupPoll() {
    if (this.groupTimer) {
 return; 
}
    this.groupTimer = setInterval(() => {
      this.refreshGroups();
      if (this.selectedGroup) {
 this.pollGroupMessages(); 
}
    }, 2000);
  }

  private stopGroupPoll() {
    if (this.groupTimer) {
 clearInterval(this.groupTimer); this.groupTimer = null; 
}
  }

  /** Refresh the room list from whichever transport this node uses. */
  refreshGroups() {
    if (!this.node) {
 return; 
}
    const sv = this.groupSv;
    const done = (arr: any[]) => {
      if (!Array.isArray(arr)) {
 return; 
}
      this.groups = arr.map((g: any): GroupView => {
return {
        id: g.id, name: g.name, mode: g.mode, role: g.role,
        members: typeof g.members === 'number' ? g.members : (Array.isArray(g.members) ? g.members.length : 0),
        status: g.status,
      }
});
      // Keep the selection object fresh (member counts change).
      if (this.selectedGroup) {
        const upd = this.groups.find(g => g.id === this.selectedGroup!.id);
        if (upd) {
 this.selectedGroup = upd; 
}
      }
      this.groupError = '';
      this.cdr.markForCheck();
    };
    if (sv) {
      // Under the SharedWorker architecture skywireVisor is a MessagePort
      // proxy, so the hooks return Promises (not the raw JSON string the Go
      // side returns synchronously). Promise.resolve() normalizes both.
      Promise.resolve(sv.skychatGroupList())
        .then((raw: any) => done(JSON.parse(raw || '[]') || []))
        .catch(() => { /* hook error — leave list as-is */ });

      return;
    }
    this.groupApi('', 'GET')
      .then(done)
      .catch((e) => {
 this.groupError = this.groupErrMsg(e); this.cdr.markForCheck(); 
});
  }

  selectGroup(g: GroupView) {
    this.selectedGroup = g;
    this.groupMessages = [];
    this.lastGroupMsgLen = -1;
    this.inviteLink = '';
    this.addMemberPk = '';
    this.pollGroupMessages();
    this.startGroupPoll();
  }

  /** Poll the selected room's message ring (full snapshot each tick — the
   *  ring is bounded, so rebuild-on-change is simplest + robust). */
  private pollGroupMessages() {
    if (!this.node || !this.selectedGroup) {
 return; 
}
    const id = this.selectedGroup.id;
    const me = this.node.localPk;
    const done = (arr: any[]) => {
      if (!Array.isArray(arr)) {
 return; 
}
      const sent = this.sentByGroup[id] || [];
      // Rebuild when the incoming count changed OR we have local sends to
      // interleave (send count is small, so this is cheap).
      if (arr.length === this.lastGroupMsgLen && sent.length === 0) {
 return; 
}
      this.captureScroll();
      const incoming: GroupMsg[] = arr.map((m: any): GroupMsg => {
return {
        group_id: m.group_id, from: m.from || m.sender_pk || '',
        text: m.text || '', ts: m.ts || Date.now(),
        out: (m.from || m.sender_pk || '') === me,
      }
});
      // Drop optimistic echoes that the backend has since surfaced (the feed
      // can return our own send back), so a sent message isn't shown twice.
      const inKeys = new Set(incoming.map(m => m.from + '|' + m.text));
      const sentUnique = sent.filter(m => !inKeys.has(m.from + '|' + m.text));
      this.groupMessages = incoming.concat(sentUnique).sort((a, b) => a.ts - b.ts).slice(-500);
      this.lastGroupMsgLen = arr.length;
      this.cdr.markForCheck();
    };
    const sv = this.groupSv;
    if (sv) {
      // SharedWorker proxy → the hook returns a Promise; normalize.
      Promise.resolve(sv.skychatGroupMessages(id))
        .then((raw: any) => done(JSON.parse(raw || '[]') || []))
        .catch(() => { /* hook error */ });

      return;
    }
    // Native: since_ms omitted → full ring for this group.
    this.groupApi(`/messages?group_id=${encodeURIComponent(id)}`, 'GET')
      .then((arr) => done((arr || []).map((m: any) => {
return {
        group_id: m.group_id, from: m.sender_pk || m.from || '', text: m.text || '',
        ts: m.ts ? (typeof m.ts === 'number' ? m.ts : Date.parse(m.ts)) : Date.now(),
      }
})))
      .catch(() => { /* transient */ });
  }

  createGroup() {
    if (!this.node || this.groupSending) {
 return; 
}
    const name = this.newGroupName.trim();
    if (!name) {
 this.snackbar.showError('Room name required');

 return; 
}
    this.groupSending = true;
    const sv = this.groupSv;
    const after = (id: string, invite: string) => {
      this.newGroupName = '';
      this.showCreate = false;
      this.inviteLink = invite || '';
      this.refreshGroups();
      this.snackbar.showDone('Room created');
    };
    const p = sv
      ? Promise.resolve(sv.skychatGroupCreate(name, this.newGroupMode)).then((r: any) => after(r.id, r.invite))
      : this.groupApi('', 'POST', { name: name, mode: this.newGroupMode }).then((r: any) => after(r.info?.id, r.invite));
    p.catch((e: any) => this.snackbar.showError(this.groupErrMsg(e)))
      .finally(() => {
 this.groupSending = false; this.cdr.markForCheck(); 
});
  }

  joinGroup() {
    if (!this.node || this.groupSending) {
 return; 
}
    const invite = this.joinInvite.trim();
    if (!invite) {
 this.snackbar.showError('Invite link required');

 return; 
}
    this.groupSending = true;
    const sv = this.groupSv;
    const after = () => {
      this.joinInvite = '';
      this.showJoin = false;
      this.refreshGroups();
      this.snackbar.showDone('Joined room');
    };
    const p = sv
      ? Promise.resolve(sv.skychatGroupJoin(invite)).then(after)
      : this.groupApi('/join', 'POST', { invite: invite }).then(after);
    p.catch((e: any) => this.snackbar.showError(this.groupErrMsg(e)))
      .finally(() => {
 this.groupSending = false; this.cdr.markForCheck(); 
});
  }

  sendGroup() {
    if (!this.node || this.groupSending || !this.selectedGroup) {
 return; 
}
    const text = this.groupMessage.trim();
    if (!text) {
 return; 
}
    const id = this.selectedGroup.id;
    this.groupSending = true;
    const sv = this.groupSv;
    const p = sv
      ? Promise.resolve(sv.skychatGroupSend(id, text))
      : this.groupApi('/send', 'POST', { id: id, text: text });
    p.then(() => {
      this.groupMessage = '';
      // Optimistic local echo (the data plane won't echo our own message back).
      (this.sentByGroup[id] = this.sentByGroup[id] || []).push({
        group_id: id, from: this.node.localPk, text: text, ts: Date.now(), out: true,
      });
      this.lastGroupMsgLen = -1; // force the next poll to rebuild+interleave
      this.pollGroupMessages();
    })
      .catch((e: any) => this.snackbar.showError(this.groupErrMsg(e)))
      .finally(() => {
 this.groupSending = false; this.cdr.markForCheck(); 
});
  }

  addMember() {
    if (!this.node || !this.selectedGroup) {
 return; 
}
    const pk = this.addMemberPk.trim();
    if (pk.length !== 66 || !/^[0-9a-fA-F]+$/.test(pk)) {
      this.snackbar.showError('Member must be a 66-char hex public key');

      return;
    }
    const id = this.selectedGroup.id;
    const sv = this.groupSv;
    const p = sv
      ? Promise.resolve(sv.skychatGroupAddMember(id, pk))
      : this.groupApi('/add-member', 'POST', { id: id, pk: pk });
    p.then(() => {
 this.addMemberPk = ''; this.refreshGroups(); this.snackbar.showDone('Member added'); 
})
      .catch((e: any) => this.snackbar.showError(this.groupErrMsg(e)))
      .finally(() => this.cdr.markForCheck());
  }

  /** Mint a fresh invite link for the selected room and reveal it. */
  showInvite() {
    if (!this.node || !this.selectedGroup) {
 return; 
}
    const id = this.selectedGroup.id;
    const sv = this.groupSv;
    // wasm exposes invite only at create; re-mint over the bridge is native-only.
    // For wasm we fall back to create-time link (already shown) — nothing to do.
    if (sv) {
      this.snackbar.showError('Invite is shown when the room is created (wasm visor)');

      return;
    }
    this.groupApi(`/invite?group_id=${encodeURIComponent(id)}`, 'GET')
      .then((r: any) => {
 this.inviteLink = r.invite || ''; this.cdr.markForCheck(); 
})
      .catch((e: any) => this.snackbar.showError(this.groupErrMsg(e)));
  }

  copyInvite() {
    if (!this.inviteLink) {
 return; 
}
    try {
      navigator.clipboard.writeText(this.inviteLink);
      this.snackbar.showDone('Invite copied');
    } catch { /* clipboard blocked — user can select the text */ }
  }

  shortPk(pk: string): string {
    return pk && pk.length > 12 ? pk.slice(0, 6) + '…' + pk.slice(-4) : pk;
  }

  // --- Voice calls -----------------------------------------------
  // Opus voice over the encrypted mesh (dmsg/skynet), same media plane as
  // the wasm visor. Native audio flows only when the visor runs with
  // SKYWIRE_VOICE_AUDIO=mic|monitor; otherwise the call still connects
  // (silent), so the control plane is always demonstrable from here.

  /** window.skywireVisor when this node is an in-browser wasm visor with the
   *  voice hooks present, else null (native → HTTP bridge). */
  private get voiceSv(): any {
    const sv = (window as any).skywireVisor;
    if (this.node && (this.node as any).arch === 'wasm' && sv && typeof sv.skychatVoiceCall === 'function') {
      return sv;
    }

    return null;
  }

  /** Native voice-bridge call over ApiService (CSRF/auth handled). */
  private voiceApi(path: string, method: 'GET' | 'POST', body?: any): Promise<any> {
    const url = `visors/${this.node.localPk}/skychat/voice${path}`;
    const obs = method === 'POST' ? this.api.post(url, body || {}) : this.api.get(url);

    return firstValueFrom(obs);
  }

  /** True while at least one call is up (drives the Call/Hang-up toggle). */
  get inCall(): boolean {
    return this.voiceActive.length > 0;
  }

  private startVoicePoll() {
    if (this.voiceTimer || !this.node) {
 return; 
}
    this.pollVoice();
    this.voiceTimer = setInterval(() => this.pollVoice(), 1800);
  }

  private stopVoicePoll() {
    if (this.voiceTimer) {
 clearInterval(this.voiceTimer); this.voiceTimer = null; 
}
  }

  /** Normalize active-call ids from either transport (wasm returns a JSON
   *  string, native an array). */
  private parseIds(raw: any): string[] {
    let arr = raw;
    if (typeof raw === 'string') {
 try {
 arr = JSON.parse(raw || '[]'); 
} catch {
 arr = []; 
} 
}

    return Array.isArray(arr) ? arr.map((x: any) => String(x)) : [];
  }

  /** Normalize ringing calls: wasm gives [{id,from}] (as a JSON string),
   *  native gives ["<id> from <pk>"]. */
  private parseIncoming(raw: any): IncomingCall[] {
    let arr = raw;
    if (typeof raw === 'string') {
 try {
 arr = JSON.parse(raw || '[]'); 
} catch {
 arr = []; 
} 
}
    if (!Array.isArray(arr)) {
 return []; 
}

    return arr.map((x: any): IncomingCall => {
      if (x && typeof x === 'object') {
 return { id: String(x.id || ''), from: String(x.from || '') }; 
}
      const s = String(x);
      const m = s.match(/^(\S+)\s+from\s+(\S+)/);

      return m ? { id: m[1], from: m[2] } : { id: s, from: '' };
    }).filter(c => c.id);
  }

  /** Track call start/end so the duration timer + mute state reset cleanly. */
  private syncCallState() {
    if (this.voiceActive.length && !this.callStartMs) {
      this.callStartMs = Date.now();
      this.micMuted = false;
      this.spkMuted = false;
      // Defer so the *ngIf'd canvas is in the DOM before the first draw.
      setTimeout(() => this.startViz(), 300);
    } else if (!this.voiceActive.length && this.callStartMs) {
      this.callStartMs = 0;
      this.micMuted = false;
      this.spkMuted = false;
      this.stopViz();
    }
  }

  private pollVoice() {
    if (!this.node) {
 return; 
}
    const sv = this.voiceSv;
    if (sv) {
      Promise.resolve(sv.skychatVoiceActive()).then((r: any) => {
 this.voiceActive = this.parseIds(r); this.syncCallState(); this.cdr.markForCheck(); 
}).catch(() => { /* hook error */ });
      Promise.resolve(sv.skychatVoiceIncoming()).then((r: any) => {
 this.voiceIncoming = this.parseIncoming(r); this.cdr.markForCheck(); 
}).catch(() => { /* hook error */ });

      return;
    }
    this.voiceApi('/active', 'GET')
      .then((r) => {
 this.voiceActive = this.parseIds(r); this.voiceAvailable = true; this.syncCallState(); this.cdr.markForCheck(); 
})
      .catch((e) => {
 if (e?.originalError?.status === 503) {
 this.voiceAvailable = false; this.cdr.markForCheck(); 
} 
});
    this.voiceApi('/incoming', 'GET')
      .then((r) => {
 this.voiceIncoming = this.parseIncoming(r); this.cdr.markForCheck(); 
})
      .catch(() => { /* transient / disabled — /active already flags availability */ });
  }

  /** mm:ss since the current call went active ('' when not in a call). */
  get callDuration(): string {
    if (!this.callStartMs) {
 return ''; 
}
    const s = Math.max(0, Math.floor((Date.now() - this.callStartMs) / 1000));
    const mm = Math.floor(s / 60), ss = s % 60;

    return `${mm}:${ss < 10 ? '0' : ''}${ss}`;
  }

  /** Push the current mute state to the active call (both directions). */
  private voiceMute() {
    const id = this.voiceActive[0];
    if (!id) {
 return; 
}
    const sv = this.voiceSv;
    const p = sv
      ? Promise.resolve(sv.skychatVoiceMute ? sv.skychatVoiceMute(id, this.micMuted, this.spkMuted) : null)
      : this.voiceApi('/mute', 'POST', { call_id: id, mic: this.micMuted, speaker: this.spkMuted });
    p.catch((e: any) => this.snackbar.showError(this.groupErrMsg(e)));
  }

  /** Toggle our mic (the peer hears silence while muted). */
  toggleMicMute() {
    this.micMuted = !this.micMuted;
    this.voiceMute();
    this.cdr.markForCheck();
  }

  /** Toggle the caller's audio (we hear silence while muted). */
  toggleSpeakerMute() {
    this.spkMuted = !this.spkMuted;
    this.voiceMute();
    this.cdr.markForCheck();
  }

  private voiceLevels(id: string): Promise<{ sent: number; recv: number }> {
    const sv = this.voiceSv;
    if (sv && typeof sv.skychatVoiceLevels === 'function') {
      return Promise.resolve(sv.skychatVoiceLevels(id)).then((r: any) => JSON.parse(r || '{}'));
    }

    return this.voiceApi(`/levels?call=${encodeURIComponent(id)}`, 'GET');
  }

  private voiceAudioData(id: string): Promise<{ sent: number[]; recv: number[] }> {
    const sv = this.voiceSv;
    if (sv && typeof sv.skychatVoiceAudio === 'function') {
      return Promise.resolve(sv.skychatVoiceAudio(id)).then((r: any) => JSON.parse(r || '{}'));
    }

    return this.voiceApi(`/audio?call=${encodeURIComponent(id)}`, 'GET');
  }

  toggleSpectrogram() {
    this.showSpectrogram = !this.showSpectrogram;
    this.clearCanvas(this.vizCanvas?.nativeElement);
    this.cdr.markForCheck();
  }

  /** Expand a large spectrogram into the message-log area (toggle off = messages). */
  toggleBigViz() {
    this.bigViz = !this.bigViz;
    this.cdr.markForCheck();
  }

  private clearCanvas(c?: HTMLCanvasElement) {
    if (c) {
 c.getContext('2d')?.clearRect(0, 0, c.width, c.height); 
}
  }

  private startViz() {
    if (this.vizTimer) {
 return; 
}
    this.lvlSent = []; this.lvlRecv = [];
    this.vizTimer = setInterval(() => this.tickViz(), 100);
  }

  private stopViz() {
    if (this.vizTimer) {
 clearInterval(this.vizTimer); this.vizTimer = null; 
}
    this.lvlSent = []; this.lvlRecv = [];
  }

  private tickViz() {
    const id = this.voiceActive[0];
    if (!id) {
 return; 
}
    // The small in-bar canvas shows spectrogram or level meter; the big canvas
    // (message-area) is always a spectrogram. Fetch PCM if either wants it.
    const needAudio = this.showSpectrogram || this.bigViz;
    const needLevels = !this.showSpectrogram;
    if (needLevels) {
      this.voiceLevels(id).then((l) => {
        this.lvlRecv.push(Math.min(1, (l && l.recv) || 0));
        this.lvlSent.push(Math.min(1, (l && l.sent) || 0));
        if (this.lvlRecv.length > this.lvlMax) {
 this.lvlRecv.shift(); 
}
        if (this.lvlSent.length > this.lvlMax) {
 this.lvlSent.shift(); 
}
        this.drawLevels(this.vizCanvas?.nativeElement);
      }).catch(() => { /* transient */ });
    }
    if (needAudio) {
      this.voiceAudioData(id).then((d) => {
        const recv = (d && d.recv) || [], sent = (d && d.sent) || [];
        if (this.showSpectrogram) {
 this.drawSpectrogram(this.vizCanvas?.nativeElement, recv); 
}
        if (this.bigViz) {
          this.drawSpectrogram(this.vizBig?.nativeElement, recv);      // incoming (peer)
          this.drawSpectrogram(this.vizBigSent?.nativeElement, sent);  // outgoing (you)
        }
      }).catch(() => { /* transient */ });
    }
  }

  /** Telegram-style dual level meter: peer (green) foreground, you (violet) behind. */
  private drawLevels(c?: HTMLCanvasElement) {
    if (!c) {
 return; 
}
    const ctx = c.getContext('2d');
    if (!ctx) {
 return; 
}
    const W = c.width, H = c.height, n = this.lvlMax, bw = W / n;
    ctx.clearRect(0, 0, W, H);
    const bar = (hist: number[], color: string, wf: number) => {
      ctx.fillStyle = color;
      for (let i = 0; i < hist.length; i++) {
        const v = hist[i];
        const h = Math.max(1, Math.pow(v, 0.6) * H); // perceptual boost
        const x = i * bw;
        ctx.fillRect(x + bw * (1 - wf) / 2, (H - h) / 2, Math.max(1, bw * wf - 1), h);
      }
    };
    bar(this.lvlSent, 'rgba(157,124,255,0.55)', 1.0);  // you
    bar(this.lvlRecv, 'rgba(158,206,106,0.95)', 0.6);  // peer (prominent)
  }

  /** Scrolling FFT heatmap (newest column on the right), matched to the
   *  audioprism-go spectrogram: Hann window, magnitude = |FFT| (no N scaling),
   *  ScaleLog 20·log10, normalized over 0..45 dB, heat colormap. 0..12 kHz with
   *  low frequencies at the bottom. Silence → 0 dB → black. */
  private drawSpectrogram(c: HTMLCanvasElement | undefined, pcm: number[]) {
    const N = 2048;
    if (!c || pcm.length < N) {
 return; 
}
    const ctx = c.getContext('2d');
    if (!ctx) {
 return; 
}
    const W = c.width, H = c.height, col = 2;
    const start = pcm.length - N;
    const re = new Float64Array(N), im = new Float64Array(N);
    for (let i = 0; i < N; i++) {
      re[i] = (pcm[start + i] / 32768) * (0.5 - 0.5 * Math.cos((2 * Math.PI * i) / (N - 1)));
    }
    fft(re, im);
    ctx.drawImage(c, -col, 0); // scroll left, new column on the right
    // 0..12 kHz = the lower quarter of the N/2 bins at 48 kHz (Nyquist 24 kHz).
    const maxBin = N / 4;
    for (let y = 0; y < H; y++) {
      const bin = Math.floor((1 - y / H) * (maxBin - 1));
      const mag = Math.hypot(re[bin], im[bin]);
      const dB = 20 * Math.log10(mag + 1e-10);
      const t = Math.max(0, Math.min(1, dB / 45)); // magMin 0, magMax 45
      ctx.fillStyle = heat(t);
      ctx.fillRect(W - col, y, col, 1);
    }
  }

  /** Place a call to the composed recipient PK. */
  voiceCall() {
    if (this.voiceBusy || this.inCall) {
 return; 
}
    const peer = this.toPK.trim();
    if (peer.length !== 66 || !/^[0-9a-fA-F]+$/.test(peer)) {
      this.snackbar.showError('Recipient must be a 66-char hex public key');

      return;
    }
    this.voiceBusy = true;
    const sv = this.voiceSv;
    const done = () => {
 this.voiceBusy = false; this.pollVoice(); this.cdr.markForCheck(); 
};
    const p = sv
      ? Promise.resolve(sv.skychatVoiceCall(peer))
      : this.voiceApi('/call', 'POST', { peer: peer });
    p.then(() => {
 this.snackbar.showDone('Call connected'); done(); 
})
      .catch((e: any) => {
 this.snackbar.showError(this.groupErrMsg(e)); done(); 
});
  }

  /** Hang up an active call (defaults to the first). */
  voiceHangup(id?: string) {
    const callID = id || this.voiceActive[0];
    if (!callID) {
 return; 
}
    const sv = this.voiceSv;
    const done = () => {
 this.pollVoice(); this.cdr.markForCheck(); 
};
    const p = sv
      ? Promise.resolve(sv.skychatVoiceHangup(callID))
      : this.voiceApi('/hangup', 'POST', { call_id: callID });
    p.then(done).catch((e: any) => {
 this.snackbar.showError(this.groupErrMsg(e)); done(); 
});
  }

  /** Answer a ringing inbound call. */
  voiceAnswer(id: string) {
    if (this.voiceBusy) {
 return; 
}
    this.voiceBusy = true;
    this.voiceRingSeen.add(id);
    const sv = this.voiceSv;
    const done = () => {
 this.voiceBusy = false; this.pollVoice(); this.cdr.markForCheck(); 
};
    const p = sv
      ? Promise.resolve(sv.skychatVoiceAnswer(id))
      : this.voiceApi('/answer', 'POST', { call_id: id });
    p.then(() => {
 this.snackbar.showDone('Call answered'); done(); 
})
      .catch((e: any) => {
 this.snackbar.showError(this.groupErrMsg(e)); done(); 
});
  }

  /** Decline a ringing inbound call. */
  voiceDecline(id: string) {
    this.voiceRingSeen.add(id);
    const sv = this.voiceSv;
    const done = () => {
 this.pollVoice(); this.cdr.markForCheck(); 
};
    const p = sv
      ? Promise.resolve(sv.skychatVoiceDecline ? sv.skychatVoiceDecline(id) : null)
      : this.voiceApi('/decline', 'POST', { call_id: id });
    p.then(done).catch(() => done());
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
    if (!this.node) {
 return; 
}
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
    if (!this.node || this.pwBusy) {
 return; 
}
    const err = this.validateNewPassword();
    if (err) {
 this.snackbar.showError(err);

 return; 
}
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
    if (!this.node || this.pwBusy || !this.pwIsSet) {
 return; 
}
    if (!this.pwOld) {
 this.snackbar.showError('skychat.password.errors.old-required');

 return; 
}
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

// --- Small DSP helpers for the in-call spectrogram ----------------

/** In-place iterative radix-2 Cooley–Tukey FFT (length must be a power of 2). */
function fft(re: Float64Array, im: Float64Array): void {
  const n = re.length;
  for (let i = 1, j = 0; i < n; i++) {
    let bit = n >> 1;
    for (; j & bit; bit >>= 1) {
 j ^= bit; 
}
    j ^= bit;
    if (i < j) {
      [re[i], re[j]] = [re[j], re[i]];
      [im[i], im[j]] = [im[j], im[i]];
    }
  }
  for (let len = 2; len <= n; len <<= 1) {
    const ang = (-2 * Math.PI) / len;
    const wRe = Math.cos(ang), wIm = Math.sin(ang);
    for (let i = 0; i < n; i += len) {
      let curRe = 1, curIm = 0;
      for (let k = 0; k < len / 2; k++) {
        const a = i + k, b = i + k + len / 2;
        const tRe = re[b] * curRe - im[b] * curIm;
        const tIm = re[b] * curIm + im[b] * curRe;
        re[b] = re[a] - tRe; im[b] = im[a] - tIm;
        re[a] += tRe; im[a] += tIm;
        const nRe = curRe * wRe - curIm * wIm;
        curIm = curRe * wIm + curIm * wRe;
        curRe = nRe;
      }
    }
  }
}

/** audioprism-go's exact heat colormap (ValueToPixelHeat): black→blue→cyan→
 *  green→yellow→red→white. value 0 → black, so silence renders black. */
function heat(v: number): string {
  v = Math.max(0, Math.min(1, v));
  const n = (x: number, a: number, c: number) => {
 const t = (x - a) / (c - a);

 return t < 0 ? 0 : t > 1 ? 1 : t; 
};
  let r = 0, g = 0, b: number;
  if (v < 1 / 5) {
 b = Math.round(255 * n(v, 0, 1 / 5)); 
} else if (v < 2 / 5) {
 const c = Math.round(255 * n(v, 1 / 5, 2 / 5)); r = 0; g = c; b = 255 - c; 
} else if (v < 3 / 5) {
 r = Math.round(255 * n(v, 2 / 5, 3 / 5)); g = 255; b = 0; 
} else if (v < 4 / 5) {
 r = 255; g = Math.round(255 - 255 * n(v, 3 / 5, 4 / 5)); b = 0; 
} else {
 const c = Math.round(255 * n(v, 4 / 5, 1)); r = 255; g = c; b = c; 
}

  return `rgb(${r},${g},${b})`;
}
