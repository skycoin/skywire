import { ChangeDetectionStrategy, ChangeDetectorRef, Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { ApiService } from 'src/app/services/api.service';
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
  message: MailMessage | null = null;
  draft: Draft = this.emptyDraft();
  sending = false;
  sendReport: { rcpt: string; error?: string; via?: string }[] = [];
  whitelistText = '';

  private nodeSub?: Subscription;
  private refresh?: ReturnType<typeof setInterval>;

  constructor(
    private api: ApiService,
    private snackbar: SnackbarService,
    private cdr: ChangeDetectorRef,
  ) {
    super();
  }

  override ngOnInit() {
    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      const first = !this.node;
      this.node = node;
      if (first && node) {
        this.loadStatus();
        this.loadList();
      }
      this.cdr.markForCheck();
    });
    // New mail arrives without the page asking; refresh the folder quietly.
    this.refresh = setInterval(() => {
      if (this.view === 'list') {
        this.loadStatus();
        this.loadList(true);
      }
    }, 15000);

    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.nodeSub?.unsubscribe();
    if (this.refresh) {
      clearInterval(this.refresh);
    }
  }

  private get base(): string {
    return `visors/${this.node.localPk}/mail`;
  }

  loadStatus() {
    this.api.get(this.base).subscribe({
      next: (st: any) => {
        this.status = st;
        this.statusError = '';
        this.whitelistText = (st?.whitelist || []).join('\n');
        this.cdr.markForCheck();
      },
      error: (err: any) => {
        this.statusError = this.errText(err);
        this.cdr.markForCheck();
      },
    });
  }

  showFolder(folder: string) {
    this.folder = folder;
    this.view = 'list';
    this.loadList();
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

  remove() {
    if (!confirm('Delete this message?')) {
      return;
    }
    this.api.delete(`${this.base}/${this.folder}/${encodeURIComponent(this.openId)}`).subscribe({
      next: () => {
        this.snackbar.showDone('Message deleted.');
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
          const failed = this.sendReport.filter(r => r.error);
          if (!failed.length) {
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

  copy(text: string) {
    navigator.clipboard?.writeText(text).then(() => this.snackbar.showDone('Copied.'));
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
