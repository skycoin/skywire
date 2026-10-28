import { ChangeDetectionStrategy, ChangeDetectorRef, Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { ApiService, RequestOptions, RequestTypes } from 'src/app/services/api.service';
import { SnackbarService } from 'src/app/services/snackbar.service';

/** One contact (pkg/contacts.Contact). */
interface Contact {
  uid: string;
  name: string;
  keys?: string[];
  mail?: string[];
  payments?: { coin: string; address: string }[];
  groups?: string[];
  notes?: string;
}

/** The edit form: list fields as one entry per line. */
interface Form {
  uid: string;
  name: string;
  keys: string;
  mail: string;
  payments: string;
  groups: string;
  notes: string;
}

/**
 * The visor's contact book (pkg/contacts), the same one `skywire cli contacts`
 * uses and skychat reads names from, through the hypervisor's contact routes.
 */
@Component({
  selector: 'app-contacts',
  templateUrl: './contacts.component.html',
  styleUrls: ['./contacts.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ContactsComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node!: Node;
  list: Contact[] = [];
  loading = false;
  loadError = '';
  filter = '';
  view: 'list' | 'edit' = 'list';
  form: Form = this.emptyForm();
  saving = false;

  private nodeSub?: Subscription;

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
        this.load();
      }
      this.cdr.markForCheck();
    });

    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.nodeSub?.unsubscribe();
  }

  private get base(): string {
    return `visors/${this.node.localPk}/contacts`;
  }

  /** Contacts whose name, key, mail address or group contains the filter. */
  get shown(): Contact[] {
    const f = this.filter.trim().toLowerCase();
    if (!f) {
      return this.list;
    }

    return this.list.filter(c => [c.name, ...(c.keys || []), ...(c.mail || []), ...(c.groups || [])]
      .some(s => s.toLowerCase().includes(f)));
  }

  load() {
    this.loading = true;
    this.api.get(this.base).subscribe({
      next: (l: Contact[]) => {
        this.list = l || [];
        this.loading = false;
        this.loadError = '';
        this.cdr.markForCheck();
      },
      error: (err: any) => {
        this.loading = false;
        this.loadError = this.errText(err);
        this.cdr.markForCheck();
      },
    });
  }

  add() {
    this.form = this.emptyForm();
    this.view = 'edit';
  }

  edit(c: Contact) {
    this.form = {
      uid: c.uid,
      name: c.name,
      keys: (c.keys || []).join('\n'),
      mail: (c.mail || []).join('\n'),
      payments: (c.payments || []).map(p => p.coin + ':' + p.address).join('\n'),
      groups: (c.groups || []).join(', '),
      notes: c.notes || '',
    };
    this.view = 'edit';
  }

  cancel() {
    this.view = 'list';
  }

  save() {
    const payments = this.lines(this.form.payments).map(l => {
      const i = l.indexOf(':');

      return i > 0 ? { coin: l.slice(0, i).trim().toLowerCase(), address: l.slice(i + 1).trim() } : null;
    });
    if (payments.includes(null)) {
      this.snackbar.showError('contacts.bad-payment', null, true);

      return;
    }
    const c: Contact = {
      uid: this.form.uid,
      name: this.form.name.trim(),
      keys: this.lines(this.form.keys),
      mail: this.lines(this.form.mail),
      payments: payments as { coin: string; address: string }[],
      groups: this.form.groups.split(',').map(g => g.trim()).filter(Boolean),
      notes: this.form.notes.trim(),
    };
    this.saving = true;
    this.api.post(this.base, c).subscribe({
      next: () => {
        this.saving = false;
        this.snackbar.showDone('contacts.saved');
        this.view = 'list';
        this.load();
      },
      error: (err: any) => {
        this.saving = false;
        this.snackbar.showError(this.errText(err));
        this.cdr.markForCheck();
      },
    });
  }

  remove() {
    if (!confirm('Delete ' + this.form.name + '?')) {
      return;
    }
    this.api.delete(`${this.base}/${encodeURIComponent(this.form.uid)}`).subscribe({
      next: () => {
        this.snackbar.showDone('contacts.deleted');
        this.view = 'list';
        this.load();
      },
      error: (err: any) => this.snackbar.showError(this.errText(err)),
    });
  }

  /** A same-origin link the browser downloads with the session cookie. */
  exportHref(): string {
    return `${this.api.apiPrefix}${this.base}/export`;
  }

  importFile(ev: Event) {
    const input = ev.target as HTMLInputElement;
    const file = input.files?.[0];
    input.value = '';
    if (!file) {
      return;
    }
    file.text().then(vcf => {
      // RawJson sends the string as it is; the route reads the body as vCard.
      const opts = new RequestOptions();
      opts.requestType = RequestTypes.RawJson;
      this.api.post(`${this.base}/import`, vcf, opts).subscribe({
        next: (res: any) => {
          this.snackbar.showDone('contacts.imported', { n: res?.imported || 0 });
          this.load();
        },
        error: (err: any) => this.snackbar.showError(this.errText(err)),
      });
      this.cdr.markForCheck();
    });
  }

  copy(text: string) {
    navigator.clipboard?.writeText(text).then(() => {
      this.snackbar.showDone('contacts.copied');
      this.cdr.markForCheck();
    });
  }

  private lines(s: string): string[] {
    return s.split('\n').map(l => l.trim()).filter(Boolean);
  }

  private emptyForm(): Form {
    return { uid: '', name: '', keys: '', mail: '', payments: '', groups: '', notes: '' };
  }

  private errText(err: any): string {
    return err?.error?.error || err?.error || err?.message || String(err);
  }
}
