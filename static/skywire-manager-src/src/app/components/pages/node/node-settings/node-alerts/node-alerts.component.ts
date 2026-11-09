import { Component, OnInit, OnDestroy, ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { Subscription } from 'rxjs';

import { NodeComponent } from '../../node.component';
import { SnackbarService } from '../../../../../services/snackbar.service';
import { AlertsService, AlertsSettings, AlertsStatus, AlertsResult, alertDelays } from '../../../../../services/alerts.service';
import { processServiceError } from '../../../../../utils/errors';

/** The form, as the template binds it. */
export interface AlertsForm {
  emails: string;
  delay: string;
  webhook: string;
  from: string;
  useRelay: boolean;
  host: string;
  port: string;
  username: string;
  password: string;
  starttls: boolean;
}

const emailRe = /^[^\s@,]+@[^\s@,]+$/;

export function splitList(v: string): string[] {
  return v.split(/[\s,]+/).map(s => s.trim()).filter(s => s);
}

/** The form for a status read from the visor. */
export function formFor(st: AlertsStatus): AlertsForm {
  const s = st.settings || {};
  const r = s.smtp;

  return {
    emails: (s.email_to || []).join(', '),
    delay: st.offline_after || '5m0s',
    webhook: s.webhook || '',
    from: s.from || '',
    useRelay: !!r,
    host: r ? r.host : '',
    port: r && r.port ? String(r.port) : '',
    username: r ? r.username || '' : '',
    password: '',
    starttls: r ? !!r.starttls : true,
  };
}

/** What is wrong with the form, or '' when the visor would accept it. */
export function formError(f: AlertsForm): string {
  for (const e of splitList(f.emails)) {
    if (!emailRe.test(e)) {
      return `"${e}" is not an email address`;
    }
  }
  if (f.webhook.trim() && !/^https?:\/\/[^\s/]+/.test(f.webhook.trim())) {
    return 'The webhook must be an http or https URL';
  }
  if (f.from.trim() && !emailRe.test(f.from.trim())) {
    return 'The sender must be an email address';
  }
  if (f.useRelay) {
    if (!f.host.trim()) {
      return 'The relay needs a host';
    }
    const p = f.port.trim();
    if (p && (!/^\d+$/.test(p) || Number(p) > 65535)) {
      return 'The relay port must be a number up to 65535';
    }
  }

  return '';
}

/**
 * The settings to save. The watch list and disk warning are kept as read;
 * an empty password keeps the relay's current one (pkg/alerts.Merge).
 */
export function settingsFor(f: AlertsForm, cur: AlertsSettings): AlertsSettings {
  const s: AlertsSettings = {
    email_to: splitList(f.emails),
    offline_after: f.delay,
    webhook: f.webhook.trim(),
    from: f.from.trim(),
    watch: cur.watch || [],
    smtp: null,
    disk_warn_percent: cur.disk_warn_percent || 0,
  };
  if (f.useRelay) {
    s.smtp = {
      host: f.host.trim(),
      port: f.port.trim() ? Number(f.port.trim()) : 0,
      username: f.username.trim(),
      password: f.password,
      starttls: f.starttls,
    };
  }

  return s;
}

/**
 * Offline alerts for one visor: the addresses it mails when a visor it
 * watches goes offline and is back, the delay, an optional relay and
 * webhook. Saved on the visor at once, no restart.
 */
@Component({
  selector: 'app-node-alerts',
  templateUrl: './node-alerts.component.html',
  styleUrls: ['../node-settings.component.scss', './node-alerts.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class NodeAlertsComponent implements OnInit, OnDestroy {
  nodeKey = '';
  loading = true;
  loadError = '';
  status: AlertsStatus | null = null;
  form: AlertsForm = formFor({ settings: {}, enabled: false, offline_after: '5m0s', watched: [] });
  delays = alertDelays;
  saving = false;
  testing = false;
  testResults: AlertsResult[] | null = null;

  private sub: Subscription | null = null;

  constructor(
    private alertsService: AlertsService,
    private snackbarService: SnackbarService,
    private changeDetectorRef: ChangeDetectorRef,
  ) {}

  ngOnInit() {
    this.nodeKey = NodeComponent.getCurrentNodeKey();
    this.load();
  }

  ngOnDestroy() {
    if (this.sub) {
      this.sub.unsubscribe();
    }
  }

  load() {
    this.loading = true;
    if (this.sub) {
      this.sub.unsubscribe();
    }
    this.sub = this.alertsService.get(this.nodeKey).subscribe({
      next: st => this.show(st),
      error: err => {
        this.loading = false;
        this.loadError = processServiceError(err).originalServerErrorMsg || 'Could not read the alert settings.';
        this.changeDetectorRef.markForCheck();
      },
    });
  }

  private show(st: AlertsStatus) {
    this.status = st;
    this.form = formFor(st);
    if (!this.delays.some(d => d.value === this.form.delay)) {
      this.delays = alertDelays.concat([{ value: this.form.delay, label: this.form.delay }]);
    }
    this.loadError = '';
    this.loading = false;
    this.changeDetectorRef.markForCheck();
  }

  get error(): string {
    return formError(this.form);
  }

  get passwordPlaceholder(): string {
    const r = this.status && this.status.settings.smtp;

    return r && r.password_set ? 'set, type to replace' : '';
  }

  get offlineCount(): number {
    return (this.status?.watched || []).filter(w => !w.online).length;
  }

  save() {
    if (this.error || !this.status) {
      return;
    }
    this.saving = true;
    this.alertsService.set(this.nodeKey, settingsFor(this.form, this.status.settings)).subscribe({
      next: st => {
        this.saving = false;
        this.show(st);
        this.snackbarService.showDone('Alert settings saved.');
      },
      error: err => {
        this.saving = false;
        this.snackbarService.showError(err);
        this.changeDetectorRef.markForCheck();
      },
    });
  }

  sendTest() {
    this.testing = true;
    this.testResults = null;
    this.alertsService.test(this.nodeKey).subscribe({
      next: res => {
        this.testing = false;
        this.testResults = res || [];
        this.changeDetectorRef.markForCheck();
      },
      error: err => {
        this.testing = false;
        this.snackbarService.showError(err);
        this.changeDetectorRef.markForCheck();
      },
    });
  }
}
