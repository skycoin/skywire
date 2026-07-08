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
  private voiceTimer: any = null;
  // Remember which ringing calls we've already surfaced, so re-polls don't
  // re-prompt (native auto-answer never rings; explicit-answer mode does).
  private voiceRingSeen = new Set<string>();

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
        this.startVoicePoll();
      }
    });
    return super.ngOnInit();
  }

  ngOnDestroy() {
    if (this.nodeSub) { this.nodeSub.unsubscribe(); }
    this.disconnectSSE();
    this.stopGroupPoll();
    this.stopVoicePoll();
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
    if (!this.node || this.es || this.pollTimer) { return; }
    // In-browser wasm visor → use the in-process skychat hooks (poll), not SSE.
    const sv = (window as any).skywireVisor;
    this.wasmChat = (this.node as any).arch === 'wasm' && !!sv && typeof sv.skychatMessages === 'function';
    if (this.wasmChat) { this.connectWasm(sv); return; }
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
    const poll = () => {
      let arr: any[];
      // skychatMessages() returns the JSON of the message buffer, which is the
      // string "null" (not "[]") when the buffer is a nil slice — JSON.parse
      // gives null, so coalesce to [] or the poll would bail and never connect.
      try { arr = JSON.parse(sv.skychatMessages() || '[]') || []; } catch {
        this.connected = false; this.errorText = 'chat hook error'; this.cdr.markForCheck(); return;
      }
      if (!Array.isArray(arr)) { return; }
      this.connected = true; this.errorText = '';
      // Rebuild on any length change — simple + robust to buffer trims (the
      // buffer is small). Newest last, so the tail stays scrolled.
      if (arr.length !== this.lastPollLen) {
        this.captureScroll();
        this.messages = arr.slice(-500).map(m => ({
          peer: m.from || '', direction: m.out ? 'out' : 'in', text: m.text || '', ts: m.ts || Date.now(),
        }));
        this.lastPollLen = arr.length;
        this.cdr.markForCheck();
      }
    };
    poll();
    this.pollTimer = setInterval(poll, 1500);
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
    // In-browser wasm visor: send through the in-process skychat hook. The sent
    // message is buffered with out:true, so the poll loop renders it — no manual
    // push needed here.
    if (this.wasmChat) {
      const sv = (window as any).skywireVisor;
      Promise.resolve(sv.skychatSend(recipient, text))
        .then(() => { this.message = ''; })
        .catch((e: any) => this.snackbar.showError(this.groupErrMsg(e)))
        .finally(() => { this.sending = false; this.cdr.markForCheck(); });
      return;
    }
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
    // wasm skychat has no /history proxy; the poll loop seeds messages instead.
    if (!this.node || !this.historyAvailable || this.wasmChat) { return; }
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

  pickRecipient(pk: string) {
    this.toPK = pk;
  }

  private captureScroll() {
    if (!this.logEl) { this.wasAtBottom = true; return; }
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
    if (this.chatMode === mode) { return; }
    this.chatMode = mode;
    if (mode === 'groups') {
      this.refreshGroups();
      this.startGroupPoll();
    } else {
      this.stopGroupPoll();
    }
  }

  private startGroupPoll() {
    if (this.groupTimer) { return; }
    this.groupTimer = setInterval(() => {
      this.refreshGroups();
      if (this.selectedGroup) { this.pollGroupMessages(); }
    }, 2000);
  }

  private stopGroupPoll() {
    if (this.groupTimer) { clearInterval(this.groupTimer); this.groupTimer = null; }
  }

  /** Refresh the room list from whichever transport this node uses. */
  refreshGroups() {
    if (!this.node) { return; }
    const sv = this.groupSv;
    const done = (arr: any[]) => {
      if (!Array.isArray(arr)) { return; }
      this.groups = arr.map((g: any): GroupView => ({
        id: g.id, name: g.name, mode: g.mode, role: g.role,
        members: typeof g.members === 'number' ? g.members : (Array.isArray(g.members) ? g.members.length : 0),
        status: g.status,
      }));
      // Keep the selection object fresh (member counts change).
      if (this.selectedGroup) {
        const upd = this.groups.find(g => g.id === this.selectedGroup!.id);
        if (upd) { this.selectedGroup = upd; }
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
      .catch((e) => { this.groupError = this.groupErrMsg(e); this.cdr.markForCheck(); });
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
    if (!this.node || !this.selectedGroup) { return; }
    const id = this.selectedGroup.id;
    const me = this.node.localPk;
    const done = (arr: any[]) => {
      if (!Array.isArray(arr)) { return; }
      const sent = this.sentByGroup[id] || [];
      // Rebuild when the incoming count changed OR we have local sends to
      // interleave (send count is small, so this is cheap).
      if (arr.length === this.lastGroupMsgLen && sent.length === 0) { return; }
      this.captureScroll();
      const incoming: GroupMsg[] = arr.map((m: any): GroupMsg => ({
        group_id: m.group_id, from: m.from || m.sender_pk || '',
        text: m.text || '', ts: m.ts || Date.now(),
        out: (m.from || m.sender_pk || '') === me,
      }));
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
      .then((arr) => done((arr || []).map((m: any) => ({
        group_id: m.group_id, from: m.sender_pk || m.from || '', text: m.text || '',
        ts: m.ts ? (typeof m.ts === 'number' ? m.ts : Date.parse(m.ts)) : Date.now(),
      }))))
      .catch(() => { /* transient */ });
  }

  createGroup() {
    if (!this.node || this.groupSending) { return; }
    const name = this.newGroupName.trim();
    if (!name) { this.snackbar.showError('Room name required'); return; }
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
      : this.groupApi('', 'POST', { name, mode: this.newGroupMode }).then((r: any) => after(r.info?.id, r.invite));
    p.catch((e: any) => this.snackbar.showError(this.groupErrMsg(e)))
      .finally(() => { this.groupSending = false; this.cdr.markForCheck(); });
  }

  joinGroup() {
    if (!this.node || this.groupSending) { return; }
    const invite = this.joinInvite.trim();
    if (!invite) { this.snackbar.showError('Invite link required'); return; }
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
      : this.groupApi('/join', 'POST', { invite }).then(after);
    p.catch((e: any) => this.snackbar.showError(this.groupErrMsg(e)))
      .finally(() => { this.groupSending = false; this.cdr.markForCheck(); });
  }

  sendGroup() {
    if (!this.node || this.groupSending || !this.selectedGroup) { return; }
    const text = this.groupMessage.trim();
    if (!text) { return; }
    const id = this.selectedGroup.id;
    this.groupSending = true;
    const sv = this.groupSv;
    const p = sv
      ? Promise.resolve(sv.skychatGroupSend(id, text))
      : this.groupApi('/send', 'POST', { id, text });
    p.then(() => {
      this.groupMessage = '';
      // Optimistic local echo (the data plane won't echo our own message back).
      (this.sentByGroup[id] = this.sentByGroup[id] || []).push({
        group_id: id, from: this.node.localPk, text, ts: Date.now(), out: true,
      });
      this.lastGroupMsgLen = -1; // force the next poll to rebuild+interleave
      this.pollGroupMessages();
    })
      .catch((e: any) => this.snackbar.showError(this.groupErrMsg(e)))
      .finally(() => { this.groupSending = false; this.cdr.markForCheck(); });
  }

  addMember() {
    if (!this.node || !this.selectedGroup) { return; }
    const pk = this.addMemberPk.trim();
    if (pk.length !== 66 || !/^[0-9a-fA-F]+$/.test(pk)) {
      this.snackbar.showError('Member must be a 66-char hex public key');
      return;
    }
    const id = this.selectedGroup.id;
    const sv = this.groupSv;
    const p = sv
      ? Promise.resolve(sv.skychatGroupAddMember(id, pk))
      : this.groupApi('/add-member', 'POST', { id, pk });
    p.then(() => { this.addMemberPk = ''; this.refreshGroups(); this.snackbar.showDone('Member added'); })
      .catch((e: any) => this.snackbar.showError(this.groupErrMsg(e)))
      .finally(() => this.cdr.markForCheck());
  }

  /** Mint a fresh invite link for the selected room and reveal it. */
  showInvite() {
    if (!this.node || !this.selectedGroup) { return; }
    const id = this.selectedGroup.id;
    const sv = this.groupSv;
    // wasm exposes invite only at create; re-mint over the bridge is native-only.
    // For wasm we fall back to create-time link (already shown) — nothing to do.
    if (sv) {
      this.snackbar.showError('Invite is shown when the room is created (wasm visor)');
      return;
    }
    this.groupApi(`/invite?group_id=${encodeURIComponent(id)}`, 'GET')
      .then((r: any) => { this.inviteLink = r.invite || ''; this.cdr.markForCheck(); })
      .catch((e: any) => this.snackbar.showError(this.groupErrMsg(e)));
  }

  copyInvite() {
    if (!this.inviteLink) { return; }
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
    if (this.voiceTimer || !this.node) { return; }
    this.pollVoice();
    this.voiceTimer = setInterval(() => this.pollVoice(), 1800);
  }

  private stopVoicePoll() {
    if (this.voiceTimer) { clearInterval(this.voiceTimer); this.voiceTimer = null; }
  }

  /** Normalize active-call ids from either transport (wasm returns a JSON
   *  string, native an array). */
  private parseIds(raw: any): string[] {
    let arr = raw;
    if (typeof raw === 'string') { try { arr = JSON.parse(raw || '[]'); } catch { arr = []; } }
    return Array.isArray(arr) ? arr.map((x: any) => String(x)) : [];
  }

  /** Normalize ringing calls: wasm gives [{id,from}] (as a JSON string),
   *  native gives ["<id> from <pk>"]. */
  private parseIncoming(raw: any): IncomingCall[] {
    let arr = raw;
    if (typeof raw === 'string') { try { arr = JSON.parse(raw || '[]'); } catch { arr = []; } }
    if (!Array.isArray(arr)) { return []; }
    return arr.map((x: any): IncomingCall => {
      if (x && typeof x === 'object') { return { id: String(x.id || ''), from: String(x.from || '') }; }
      const s = String(x);
      const m = s.match(/^(\S+)\s+from\s+(\S+)/);
      return m ? { id: m[1], from: m[2] } : { id: s, from: '' };
    }).filter(c => c.id);
  }

  private pollVoice() {
    if (!this.node) { return; }
    const sv = this.voiceSv;
    if (sv) {
      Promise.resolve(sv.skychatVoiceActive()).then((r: any) => { this.voiceActive = this.parseIds(r); this.cdr.markForCheck(); }).catch(() => { /* hook error */ });
      Promise.resolve(sv.skychatVoiceIncoming()).then((r: any) => { this.voiceIncoming = this.parseIncoming(r); this.cdr.markForCheck(); }).catch(() => { /* hook error */ });
      return;
    }
    this.voiceApi('/active', 'GET')
      .then((r) => { this.voiceActive = this.parseIds(r); this.voiceAvailable = true; this.cdr.markForCheck(); })
      .catch((e) => { if (e?.originalError?.status === 503) { this.voiceAvailable = false; this.cdr.markForCheck(); } });
    this.voiceApi('/incoming', 'GET')
      .then((r) => { this.voiceIncoming = this.parseIncoming(r); this.cdr.markForCheck(); })
      .catch(() => { /* transient / disabled — /active already flags availability */ });
  }

  /** Place a call to the composed recipient PK. */
  voiceCall() {
    if (this.voiceBusy || this.inCall) { return; }
    const peer = this.toPK.trim();
    if (peer.length !== 66 || !/^[0-9a-fA-F]+$/.test(peer)) {
      this.snackbar.showError('Recipient must be a 66-char hex public key');
      return;
    }
    this.voiceBusy = true;
    const sv = this.voiceSv;
    const done = () => { this.voiceBusy = false; this.pollVoice(); this.cdr.markForCheck(); };
    const p = sv
      ? Promise.resolve(sv.skychatVoiceCall(peer))
      : this.voiceApi('/call', 'POST', { peer });
    p.then(() => { this.snackbar.showDone('Call connected'); done(); })
      .catch((e: any) => { this.snackbar.showError(this.groupErrMsg(e)); done(); });
  }

  /** Hang up an active call (defaults to the first). */
  voiceHangup(id?: string) {
    const callID = id || this.voiceActive[0];
    if (!callID) { return; }
    const sv = this.voiceSv;
    const done = () => { this.pollVoice(); this.cdr.markForCheck(); };
    const p = sv
      ? Promise.resolve(sv.skychatVoiceHangup(callID))
      : this.voiceApi('/hangup', 'POST', { call_id: callID });
    p.then(done).catch((e: any) => { this.snackbar.showError(this.groupErrMsg(e)); done(); });
  }

  /** Answer a ringing inbound call. */
  voiceAnswer(id: string) {
    if (this.voiceBusy) { return; }
    this.voiceBusy = true;
    this.voiceRingSeen.add(id);
    const sv = this.voiceSv;
    const done = () => { this.voiceBusy = false; this.pollVoice(); this.cdr.markForCheck(); };
    const p = sv
      ? Promise.resolve(sv.skychatVoiceAnswer(id))
      : this.voiceApi('/answer', 'POST', { call_id: id });
    p.then(() => { this.snackbar.showDone('Call answered'); done(); })
      .catch((e: any) => { this.snackbar.showError(this.groupErrMsg(e)); done(); });
  }

  /** Decline a ringing inbound call. */
  voiceDecline(id: string) {
    this.voiceRingSeen.add(id);
    const sv = this.voiceSv;
    const done = () => { this.pollVoice(); this.cdr.markForCheck(); };
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
