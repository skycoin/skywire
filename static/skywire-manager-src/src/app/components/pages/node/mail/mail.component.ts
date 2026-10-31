import { ChangeDetectionStrategy, ChangeDetectorRef, Component, OnDestroy, OnInit } from '@angular/core';
import { EMPTY, Subscription, catchError, skip, switchMap } from 'rxjs';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { ApiService } from 'src/app/services/api.service';
import { LiveService } from 'src/app/services/live.service';
import { SnackbarService } from 'src/app/services/snackbar.service';

/** One line of a folder listing (skymail.Summary). */
interface MailSummary {
  id: string;
  folder: string;
  from: string;
  to: string;
  subject: string;
  date: string;
  seen: boolean;
  size: number;
  peer_pk?: string;
  from_verified: boolean;
  queue?: MailQueue;
}

/** Where an Outbox message stands (skymail.Queued). */
interface MailQueue {
  pending: string[];
  attempts: number;
  last_error?: string;
  next_attempt: string;
  give_up?: string;
  handed?: string[];
  carried_for?: string;
  parked?: number;
}

/** One recipient of a send (skymail.RcptResult). */
interface SendReport {
  rcpt: string;
  error?: string;
  queued?: boolean;
  via?: string;
}

/** One message as rendered for reading (skymail.Rendered). */
interface MailMessage {
  from: string;
  to: string;
  cc?: string;
  subject: string;
  date: string;
  message_id?: string;
  from_verified: boolean;
  relay_pk?: string;
  text: string;
  from_html?: boolean;
  attachments?: { name: string; content_type: string; size: number }[];
}

interface Draft {
  to: string;
  cc: string;
  subject: string;
  body: string;
  inReplyTo: string;
  files: File[];
}

/**
 * The visor's mailbox (pkg/skymail): mail to <anything>@<pk>.skynet or .dmsg,
 * delivered visor to visor over skywire. The same mailbox the desk's mail
 * window and `skywire cli mail` use, through the hypervisor's mail routes.
 */
@Component({
  selector: 'app-mail',
  templateUrl: './mail.component.html',
  styleUrls: ['./mail.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class MailComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node!: Node;
  status: any = null;
  statusError = '';
  folder = 'INBOX';
  list: MailSummary[] = [];
  loadingList = false;
  view: 'list' | 'message' | 'compose' | 'settings' = 'list';
  openId = '';
  openQueue: MailQueue | null = null;
  message: MailMessage | null = null;
  draft: Draft = this.emptyDraft();
  sending = false;
  sendReport: SendReport[] = [];
  whitelistText = '';
  helpersText = '';
  relayForText = '';

  private nodeSub?: Subscription;
  private followSub?: Subscription;

  constructor(
    private api: ApiService,
    private live: LiveService,
    private snackbar: SnackbarService,
    private cdr: ChangeDetectorRef,
  ) {
    super();
  }

  override ngOnInit() {
    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      // The tab stays mounted when the dashboard moves to another visor,
      // so a new PK starts the page over rather than keep the last one's.
      const changed = !!node && node.localPk !== this.node?.localPk;
      this.node = node;
      if (changed) {
        this.status = null;
        this.list = [];
        this.folder = 'INBOX';
        this.view = 'list';
        this.message = null;
        this.openQueue = null;
        this.loadStatus();
        this.loadList();
        this.follow();
      }
      this.cdr.markForCheck();
    });

    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.nodeSub?.unsubscribe();
    this.followSub?.unsubscribe();
  }

  /**
   * New mail arrives without the page asking, so the status and the open
   * folder are followed quietly while the list is shown. The first result
   * is skipped because loadStatus and loadList have just fetched it.
   */
  private follow() {
    this.followSub?.unsubscribe();
    this.followSub = new Subscription();
    const listUrl = `${this.base}/${this.folder}`;
    this.followSub.add(this.live.watch(this.base, 15000, () => this.api.get(this.base)).pipe(
      skip(1),
      switchMap(res => res.pipe(catchError((err: any) => {
        if (this.view === 'list') {
          this.statusError = this.errText(err);
          this.cdr.markForCheck();
        }

        return EMPTY;
      }))),
    ).subscribe((st: any) => {
      if (this.view === 'list') {
        this.applyStatus(st);
      }
      this.cdr.markForCheck();
    }));
    this.followSub.add(this.live.watch(listUrl, 15000, () => this.api.get(listUrl)).pipe(
      skip(1),
      switchMap(res => res.pipe(catchError(() => EMPTY))),
    ).subscribe((l: MailSummary[]) => {
      this.list = l || [];
      if (this.view === 'message' && this.folder === 'Outbox') {
        this.openQueue = this.list.find(m => m.id === this.openId)?.queue || null;
      }
      this.cdr.markForCheck();
    }));
  }

  private applyStatus(st: any) {
    this.status = st;
    this.statusError = '';
    this.whitelistText = (st?.whitelist || []).join('\n');
    this.helpersText = this.listText(st?.helpers, st?.helpers_default);
    this.relayForText = this.listText(st?.relay_for, st?.relay_for_default);
    this.cdr.markForCheck();
  }

  private get base(): string {
    return `visors/${this.node.localPk}/mail`;
  }

  loadStatus() {
    this.api.get(this.base).subscribe({
      next: (st: any) => this.applyStatus(st),
      error: (err: any) => {
        this.statusError = this.errText(err);
        this.cdr.markForCheck();
      },
    });
  }

  showFolder(folder: string) {
    const changed = folder !== this.folder;
    this.folder = folder;
    this.view = 'list';
    this.loadList();
    if (changed) {
      this.follow();
    }
  }

  loadList(quiet = false) {
    if (!quiet) {
      this.loadingList = true;
    }
    this.api.get(`${this.base}/${this.folder}`).subscribe({
      next: (l: MailSummary[]) => {
        this.list = l || [];
        this.loadingList = false;
        this.cdr.markForCheck();
      },
      error: (err: any) => {
        this.loadingList = false;
        if (!quiet) {
          this.snackbar.showError(this.errText(err));
        }
        this.cdr.markForCheck();
      },
    });
  }

  open(m: MailSummary) {
    this.openId = m.id;
    this.openQueue = m.queue || null;
    this.message = null;
    this.view = 'message';
    this.api.get(`${this.base}/${this.folder}/${encodeURIComponent(m.id)}`).subscribe({
      next: (msg: MailMessage) => {
        this.message = msg;
        m.seen = true;
        this.cdr.markForCheck();
      },
      error: (err: any) => {
        this.snackbar.showError(this.errText(err));
        this.view = 'list';
        this.cdr.markForCheck();
      },
    });
  }

  /** A same-origin link the browser downloads with the session cookie. */
  attachmentHref(n: number): string {
    return `${this.api.apiPrefix}${this.base}/${this.folder}/${encodeURIComponent(this.openId)}/attachments/${n}`;
  }

  rawHref(): string {
    return `${this.api.apiPrefix}${this.base}/${this.folder}/${encodeURIComponent(this.openId)}/raw`;
  }

  /** Asks the visor to try an Outbox message now, or all of them with no id. */
  retry(id = '') {
    this.api.post(`${this.base}/retry`, { id: id }).subscribe({
      next: () => {
        this.snackbar.showDone('Trying again now.');
        // The attempt runs in the background; the list shows how it went.
        setTimeout(() => {
          this.refreshOutbox();
          this.cdr.markForCheck();
        }, 3000);
      },
      error: (err: any) => this.snackbar.showError(this.errText(err)),
    });
  }

  private refreshOutbox() {
    this.loadStatus();
    this.api.get(`${this.base}/Outbox`).subscribe({
      next: (l: MailSummary[]) => {
        if (this.folder !== 'Outbox') {
          return;
        }
        this.list = l || [];
        if (this.view === 'message') {
          const m = this.list.find(x => x.id === this.openId);
          if (!m) {
            this.snackbar.showDone('Delivered.');
            this.showFolder('Outbox');
          }
          this.openQueue = m?.queue || null;
        }
        this.cdr.markForCheck();
      },
    });
  }

  remove() {
    const outbox = this.folder === 'Outbox';
    if (!confirm(outbox ? 'Stop trying to deliver this message and delete it?' : 'Delete this message?')) {
      return;
    }
    this.api.delete(`${this.base}/${this.folder}/${encodeURIComponent(this.openId)}`).subscribe({
      next: () => {
        this.snackbar.showDone(outbox ? 'Delivery cancelled.' : 'Message deleted.');
        this.showFolder(this.folder);
        this.loadStatus();
      },
      error: (err: any) => this.snackbar.showError(this.errText(err)),
    });
  }

  compose() {
    this.draft = this.emptyDraft();
    this.sendReport = [];
    this.view = 'compose';
  }

  reply() {
    if (!this.message) {
      return;
    }
    const subject = this.message.subject || '';
    this.draft = this.emptyDraft();
    this.draft.to = this.folder === 'Sent' ? this.message.to : this.message.from;
    this.draft.subject = /^re:/i.test(subject) ? subject : 'Re: ' + subject;
    this.draft.inReplyTo = this.message.message_id || '';
    this.draft.body = '\n\n' + (this.message.text || '').trimEnd().split('\n').map(l => '> ' + l).join('\n');
    this.sendReport = [];
    this.view = 'compose';
  }

  pickFiles(ev: Event) {
    const input = ev.target as HTMLInputElement;
    this.draft.files = this.draft.files.concat(Array.from(input.files || []));
    input.value = '';
  }

  dropFile(i: number) {
    this.draft.files.splice(i, 1);
  }

  async send() {
    const to = this.addrs(this.draft.to);
    if (!to.length) {
      this.snackbar.showError('Add at least one recipient.');

      return;
    }
    this.sending = true;
    this.cdr.markForCheck();
    try {
      const attachments = await Promise.all(this.draft.files.map(async f => {
return {
        name: f.name,
        content_type: f.type || 'application/octet-stream',
        data: await this.base64(f),
      }
}));
      const body = {
        to: to, cc: this.addrs(this.draft.cc), subject: this.draft.subject, body: this.draft.body,
        in_reply_to: this.draft.inReplyTo || undefined, attachments: attachments,
      };
      this.api.post(`${this.base}/send`, body).subscribe({
        next: (res: any) => {
          this.sending = false;
          this.sendReport = res?.recipients || [];
          const failed = this.sendReport.filter(r => r.error && !r.queued);
          const queued = this.sendReport.filter(r => r.queued);
          if (queued.length) {
            this.loadStatus();
          }
          if (!failed.length && queued.length) {
            this.snackbar.showDone('Queued. This visor keeps trying until the recipient is online.');
            this.showFolder('Outbox');
          } else if (!failed.length) {
            this.snackbar.showDone('Sent.');
            this.showFolder('Sent');
          }
          this.cdr.markForCheck();
        },
        error: (err: any) => {
          this.sending = false;
          this.snackbar.showError(this.errText(err));
          this.cdr.markForCheck();
        },
      });
    } catch (e: any) {
      this.sending = false;
      this.snackbar.showError(String(e));
      this.cdr.markForCheck();
    }
  }

  toggleEnabled() {
    const enable = !(this.status?.enabled);
    this.api.put(`${this.base}/settings`, { enable: enable }).subscribe({
      next: () => {
        this.snackbar.showDone(enable ? 'Mail turned on.' : 'Mail turned off.');
        this.loadStatus();
      },
      error: (err: any) => this.snackbar.showError(this.errText(err)),
    });
  }

  saveWhitelist() {
    const pks = this.whitelistText.split(/[\s,]+/).map(s => s.trim()).filter(Boolean);
    this.api.put(`${this.base}/whitelist`, { pks: pks }).subscribe({
      next: () => {
        this.snackbar.showDone(pks.length ? 'Whitelist saved.' : 'Whitelist cleared: anyone may deliver.');
        this.loadStatus();
      },
      error: (err: any) => this.snackbar.showError(this.errText(err)),
    });
  }

  /** Empty means the default, "none" an empty list. */
  private listText(pks: string[] | undefined, def: boolean): string {
    if (def) {
      return '';
    }

    return pks?.length ? pks.join('\n') : 'none';
  }

  saveHelpers() {
    const body: any = {};
    const put = (text: string, key: string) => {
      const t = text.trim();
      if (!t) {
        body[key + '_default'] = true;
      } else if (t.toLowerCase() === 'none') {
        body[key] = [];
      } else {
        body[key] = t.split(/[\s,]+/).filter(Boolean);
      }
    };
    put(this.helpersText, 'helpers');
    put(this.relayForText, 'relay_for');
    this.api.put(`${this.base}/settings`, body).subscribe({
      next: () => {
        this.snackbar.showDone('Helpers saved.');
        this.loadStatus();
      },
      error: (err: any) => this.snackbar.showError(this.errText(err)),
    });
  }

  short(pk: string): string {
    return pk && pk.length > 16 ? pk.slice(0, 8) + '…' + pk.slice(-6) : pk;
  }

  copy(text: string) {
    navigator.clipboard?.writeText(text).then(() => {
      this.snackbar.showDone('Copied.');
      this.cdr.markForCheck();
    });
  }

  size(n: number): string {
    if (n < 1024) {
      return n + ' B';
    }
    if (n < 1 << 20) {
      return (n / 1024).toFixed(1) + ' KB';
    }

    return (n / (1 << 20)).toFixed(1) + ' MB';
  }

  private addrs(s: string): string[] {
    return s.split(/[\s,;]+/).map(a => a.trim()).filter(Boolean);
  }

  private base64(f: File): Promise<string> {
    return new Promise((resolve, reject) => {
      const r = new FileReader();
      r.onload = () => resolve(String(r.result).replace(/^data:[^,]*,/, ''));
      r.onerror = () => reject(r.error);
      r.readAsDataURL(f);
    });
  }

  private emptyDraft(): Draft {
    return { to: '', cc: '', subject: '', body: '', inReplyTo: '', files: [] };
  }

  private errText(err: any): string {
    return err?.error?.error || err?.error || err?.message || String(err);
  }
}
