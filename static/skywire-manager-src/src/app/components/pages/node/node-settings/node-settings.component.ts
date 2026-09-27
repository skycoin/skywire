import { Component, OnInit, OnDestroy, ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { Subscription } from 'rxjs';

import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { SnackbarService } from '../../../../services/snackbar.service';
import { ClipboardService } from '../../../../services/clipboard.service';
import { SkyenvService, SkyenvState, SkyenvEdits, SkyenvFlag, skyenvRedacted } from '../../../../services/skyenv.service';
import { processServiceError } from '../../../../utils/errors';

/**
 * One skywire.conf variable. autoconfig often has two flags for one variable
 * (--ishv / --no-ishv); the row is the variable, and edits go through one of
 * its flags, preferably the positive one. The desk's settings window builds
 * the same rows (pkg/wasmhv/deskhost/settings_helpers.go).
 */
export interface SettingRow {
  key: string;
  flag: string;
  // flag is a --no-X flag, so a bool is sent inverted.
  negate: boolean;
  format: string;
  help: string;
  default: string;
  note: string;
  set: boolean;
  // The file's value as the form shows it; lists comma-separated.
  value: string;
  // The visor reports only that it is set.
  secret: boolean;
}

export interface SettingChange {
  key: string;
  from: string;
  to: string;
}

/** Variables whose value never leaves the visor (visorapi.SkyenvSecret). */
const secretKeys = new Set(['SK', 'DMSGWEBSK', 'VPNROUTERPASSPHRASE']);

/**
 * One row per variable, in the order autoconfig registers its flags. The
 * secret key is left out: the visor refuses to change it from here.
 */
export function settingRows(st: SkyenvState): SettingRow[] {
  const rows: SettingRow[] = [];
  const at = new Map<string, number>();
  for (const f of st.flags || []) {
    if (!f.env_key || f.env_key === 'SK') {
      continue;
    }
    const i = at.get(f.env_key);
    if (i === undefined) {
      at.set(f.env_key, rows.length);
      rows.push(rowFor(f, st.values || {}));
    } else if (rows[i].negate && !f.env_negate) {
      rows[i] = rowFor(f, st.values || {});
    }
  }

  return rows;
}

function rowFor(f: SkyenvFlag, vals: { [key: string]: string }): SettingRow {
  const key = f.env_key!;
  const r: SettingRow = {
    key: key, flag: f.name, negate: !!f.env_negate, format: f.env_format || 'string',
    help: flagHelp(f.description), default: f.env_default || '', note: f.env_note || '',
    set: false, value: '', secret: false,
  };
  if (key in vals) {
    const v = vals[key];
    r.set = true;
    r.secret = v === skyenvRedacted;
    r.value = r.format === 'bashArray' ? v.split(/\s+/).filter(s => s).join(',') : v;
  }

  return r;
}

// Every description ends "— writes X in skywire.conf"; the page shows the variable.
function flagHelp(desc: string): string {
  const i = desc.indexOf(' — writes ');

  return i >= 0 ? desc.substring(0, i).trim() : desc;
}

export function defaultShown(r: SettingRow): string {
  return r.default === '' ? '(default)' : `(default ${r.default})`;
}

function shown(r: SettingRow): string {
  return r.set ? r.value : defaultShown(r);
}

function normalizeList(v: string): string {
  return v.split(/[\s,]+/).filter(s => s).join(',');
}

function parseBool(v: string): boolean | null {
  switch (v.toLowerCase()) {
    case 'true': case 'on': case 'yes': case '1':
      return true;
    case 'false': case 'off': case 'no': case '0':
      return false;
  }

  return null;
}

/**
 * Compares the form (variable → value, '' meaning back to the default) with
 * the rows it was built from, and returns the edits for PUT skyenv with a
 * preview line per change.
 */
export function settingsEdits(rows: SettingRow[], form: { [key: string]: string }): { edits: SkyenvEdits, changes: SettingChange[] } {
  const edits: SkyenvEdits = { set: {}, unset: [] };
  const changes: SettingChange[] = [];
  for (const r of rows) {
    if (!(r.key in form)) {
      continue;
    }
    let v = (form[r.key] || '').trim();
    if (v === '') {
      // A variable set to nothing (WSPEERS=('')) shows as an empty field;
      // leaving it empty is not a change.
      if (r.set && r.value !== '') {
        edits.unset!.push(r.flag);
        changes.push({ key: r.key, from: shown(r), to: defaultShown(r) });
      }
      continue;
    }
    if (r.format === 'bashArray') {
      v = normalizeList(v);
    }
    if (r.set && v === r.value || r.secret && v === skyenvRedacted) {
      continue;
    }
    let send = v;
    if (r.format === 'bool') {
      const b = parseBool(v);
      if (b === null) {
        throw new Error(`${r.key}: "${v}" is not on or off`);
      }
      if (r.set && b === (r.value === 'true')) {
        continue;
      }
      v = String(b);
      send = String(b !== r.negate);
    }
    edits.set![r.flag] = send;
    const to = r.secret || secretKeys.has(r.key) ? '(changed)' : v;
    changes.push({ key: r.key, from: shown(r), to: to });
  }

  return { edits: edits, changes: changes };
}

// A single-quoted shell word unless it needs none.
function shellWord(s: string): string {
  return /^[A-Za-z0-9_.,:/@=+-]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`;
}

/**
 * The `skywire autoconfig` command that makes the same edits, for a visor
 * that cannot write its own conf file. autoconfig has no unset, so an unset
 * sets the default instead; it also regenerates skywire.json and restarts
 * the visor, which applies the change.
 */
export function autoconfigCommand(rows: SettingRow[], edits: SkyenvEdits): string {
  const byFlag = new Map(rows.map(r => [r.flag, r]));
  const args: string[] = [];
  for (const [flag, v] of Object.entries(edits.set || {})) {
    args.push(shellWord(`--${flag}=${v}`));
  }
  for (const flag of edits.unset || []) {
    const r = byFlag.get(flag);
    let v = r ? r.default : '';
    if (r && r.format === 'bool' && r.negate) {
      v = String(v !== 'true');
    }
    args.push(shellWord(`--${flag}=${v}`));
  }

  return 'sudo skywire autoconfig ' + args.join(' ');
}

@Component({
  selector: 'app-node-settings',
  templateUrl: './node-settings.component.html',
  styleUrls: ['./node-settings.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class NodeSettingsComponent extends PageBaseComponent implements OnInit, OnDestroy {
  nodeKey = '';
  loading = true;
  loadError = '';
  state: SkyenvState | null = null;
  rows: SettingRow[] = [];
  // Variable → value in the form; '' is back to the default.
  form: { [key: string]: string } = {};

  filter = '';
  setOnly = false;

  // Review step: the changes the form amounts to.
  reviewing = false;
  changes: SettingChange[] = [];
  edits: SkyenvEdits = {};
  command = '';
  saving = false;
  saved = false;

  private sub: Subscription | null = null;

  constructor(
    private skyenvService: SkyenvService,
    private snackbarService: SnackbarService,
    private clipboardService: ClipboardService,
    private changeDetectorRef: ChangeDetectorRef,
  ) {
    super();
  }

  override ngOnInit() {
    this.nodeKey = NodeComponent.getCurrentNodeKey();
    this.load();

    return super.ngOnInit();
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
    this.sub = this.skyenvService.get(this.nodeKey).subscribe({
      next: st => this.show(st),
      error: err => {
        this.loading = false;
        this.loadError = processServiceError(err).originalServerErrorMsg || 'Could not read the settings.';
        this.changeDetectorRef.markForCheck();
      },
    });
  }

  private show(st: SkyenvState) {
    this.state = st;
    this.loadError = '';
    // Rows set in the file first, each group in autoconfig's order.
    const rows = settingRows(st);
    this.rows = rows.filter(r => r.set).concat(rows.filter(r => !r.set));
    this.form = {};
    for (const r of this.rows) {
      this.form[r.key] = r.set ? r.value : '';
    }
    this.loading = false;
    this.reviewing = false;
    this.changeDetectorRef.markForCheck();
  }

  visible(r: SettingRow): boolean {
    if (this.setOnly && !r.set) {
      return false;
    }
    const q = this.filter.trim().toLowerCase();

    return !q || (r.key + ' ' + r.flag + ' ' + r.help).toLowerCase().includes(q);
  }

  get visibleCount(): number {
    return this.rows.filter(r => this.visible(r)).length;
  }

  placeholder(r: SettingRow): string {
    if (r.secret) {
      return 'set — type to replace';
    }

    return r.default !== '' ? r.default : '';
  }

  review() {
    try {
      const { edits, changes } = settingsEdits(this.rows, this.form);
      this.edits = edits;
      this.changes = changes;
      this.command = this.state && !this.state.writable ? autoconfigCommand(this.rows, edits) : '';
    } catch (e: any) {
      this.snackbarService.showError(e.message);

      return;
    }
    if (this.changes.length === 0) {
      this.snackbarService.showWarning('Nothing has changed.');

      return;
    }
    this.saved = false;
    this.reviewing = true;
    this.changeDetectorRef.markForCheck();
  }

  back() {
    this.reviewing = false;
    this.changeDetectorRef.markForCheck();
  }

  save() {
    this.saving = true;
    this.skyenvService.set(this.nodeKey, this.edits).subscribe({
      next: st => {
        this.saving = false;
        this.show(st);
        this.saved = true;
        this.reviewing = true;
        this.changeDetectorRef.markForCheck();
      },
      error: err => {
        this.saving = false;
        this.snackbarService.showError(err);
        this.changeDetectorRef.markForCheck();
      },
    });
  }

  done() {
    this.saved = false;
    this.reviewing = false;
    this.changeDetectorRef.markForCheck();
  }

  copyCommand(cmd: string) {
    if (this.clipboardService.copy(cmd)) {
      this.snackbarService.showDone('copy.copied');
    }
  }
}
