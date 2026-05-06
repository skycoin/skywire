import {
  Component, OnInit, OnDestroy, OnChanges, SimpleChanges, Input, Output, EventEmitter,
} from '@angular/core';
import { UntypedFormBuilder, UntypedFormGroup, UntypedFormArray, UntypedFormControl } from '@angular/forms';
import { Subscription } from 'rxjs';
import { take } from 'rxjs/operators';

import { AppsService } from 'src/app/services/apps.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { NodeComponent } from '../../node.component';
import { Application, Node } from 'src/app/app.datatypes';

/**
 * Universal app-settings panel. Replaces all the per-app dialog
 * components (skysocks-settings, skysocks-client-settings,
 * skychat-settings, user-app-settings, the daemon-specific one).
 *
 * Edits the four persistent properties any app has:
 *   - Args (shell-like string, parsed server-side)
 *   - Env (KEY=VALUE list)
 *   - Launcher mode (default / internal / external)
 *   - Autostart
 *
 * Plus a "Show flags" disclosure that fetches `<binary> --help`
 * on demand, and a "Last start error" panel that surfaces
 * app.detailed_status when status is Errored.
 *
 * Renders inline below the matching app row — no dialog backdrop,
 * no focus stealing, multiple panels can be open at once.
 */
@Component({
  selector: 'app-app-settings',
  templateUrl: './app-settings.component.html',
  styleUrls: ['./app-settings.component.scss'],
  standalone: false,
})
export class AppSettingsComponent implements OnInit, OnChanges, OnDestroy {
  @Input() app!: Application;
  @Output() saved = new EventEmitter<void>();
  @Output() cancelled = new EventEmitter<void>();

  form: UntypedFormGroup;
  saving = false;
  saveError = '';
  // "Show flags" disclosure state: collapsed by default; the help
  // text is fetched lazily on first open and cached until the
  // panel closes.
  showHelp = false;
  helpLoading = false;
  helpError = '';
  helpText = '';

  private subs: Subscription[] = [];

  constructor(
    private appsService: AppsService,
    private formBuilder: UntypedFormBuilder,
    private snackbarService: SnackbarService,
  ) {}

  // Tracks which app the form is bound to so we can distinguish
  // "different app entirely" (rebuild form, reset help) from "same
  // app, fresh poll snapshot" (no-op — would otherwise clobber the
  // operator's in-progress edits every couple of seconds).
  private boundAppName = '';

  // Apps whose binary or name is registered via launcher.RegisterApp
  // (in-process AppFunc). Only these can run in launcher "internal"
  // mode — skycoin-daemon / skycoin-web are cobra subcommands of
  // the skywire binary, not in-process functions, so internal mode
  // isn't available for them. Multi-instance entries match by their
  // family prefix (skysocks-client-2 → skysocks-client).
  private static readonly internalCapable = new Set<string>([
    'skysocks', 'skysocks-client',
    'vpn-client', 'vpn-server',
    'skychat',
    'skynet', 'skynet-client',
  ]);

  /** True iff the launcher's "internal" mode is meaningful for this
   *  app — i.e. an in-process RunFunc is registered for it. */
  get internalModeAvailable(): boolean {
    if (!this.app) { return false; }
    const candidates = [this.app.name];
    const bin = (this.app as any).binary as string | undefined;
    if (bin) { candidates.push(bin); }
    for (const c of candidates) {
      if (AppSettingsComponent.internalCapable.has(c)) { return true; }
      // Multi-instance: skysocks-client-2 → skysocks-client family.
      for (const base of AppSettingsComponent.internalCapable) {
        if (c.startsWith(base + '-')) { return true; }
      }
    }
    return false;
  }

  ngOnInit() {
    this.buildForm();
    this.boundAppName = this.app?.name || '';
  }

  ngOnChanges(changes: SimpleChanges) {
    if (!changes['app']) { return; }
    const next = (this.app && this.app.name) || '';
    if (next === this.boundAppName) {
      // Same app rebinding from the parent's poll loop. Don't
      // rebuild the form (would clobber typed edits) and don't
      // reset help (would collapse the disclosure repeatedly).
      return;
    }
    this.boundAppName = next;
    this.buildForm();
    // Different app — reset help state.
    this.showHelp = false;
    this.helpText = '';
    this.helpError = '';
  }

  ngOnDestroy(): void {
    this.subs.forEach(s => s.unsubscribe());
  }

  /**
   * Joins string args back into a shell-like string for the text
   * field. We don't quote here — the operator can edit raw and the
   * server-side parser handles re-tokenization. Tokens with
   * whitespace get wrapped in quotes for round-trip.
   */
  private argsToString(args: string[] | undefined): string {
    if (!args || args.length === 0) { return ''; }
    return args.map(a => /[\s"']/.test(a) ? `"${a.replace(/"/g, '\\"')}"` : a).join(' ');
  }

  private buildForm() {
    const env = ((this.app && (this.app as any).env) as string[] | undefined) || [];
    const envControls = env.map(e => new UntypedFormControl(e));
    const mode = (this.app && (this.app as any).launcher_mode) as string | undefined;

    this.form = this.formBuilder.group({
      args: [this.argsToString(this.app?.args)],
      env: this.formBuilder.array(envControls),
      launcher_mode: [mode || ''],
      autostart: [!!(this.app && (this.app as any).autostart)],
    });
  }

  get envArray(): UntypedFormArray {
    return this.form.get('env') as UntypedFormArray;
  }

  addEnvRow() {
    this.envArray.push(new UntypedFormControl(''));
  }

  removeEnvRow(i: number) {
    this.envArray.removeAt(i);
  }

  toggleHelp() {
    this.showHelp = !this.showHelp;
    if (this.showHelp && !this.helpText && !this.helpLoading) {
      this.fetchHelp();
    }
  }

  private fetchHelp() {
    this.helpLoading = true;
    this.helpError = '';
    NodeComponent.currentNode.pipe(take(1)).subscribe((node: Node) => {
      if (!node) { this.helpLoading = false; return; }
      const sub = this.appsService.getAppHelp(node.localPk, this.app.name).subscribe({
        next: (resp: any) => {
          this.helpLoading = false;
          this.helpText = (resp && resp.help) || '';
        },
        error: (e: any) => {
          this.helpLoading = false;
          this.helpError = (e && e.message) ? e.message : 'Failed to fetch help';
        },
      });
      this.subs.push(sub);
    });
  }

  cancel() {
    this.cancelled.emit();
  }

  save() {
    if (this.saving) { return; }
    this.saving = true;
    this.saveError = '';
    NodeComponent.currentNode.pipe(take(1)).subscribe((node: Node) => {
      this.doSave(node);
    });
  }

  private doSave(node: Node) {
    if (!node) {
      this.saveError = 'No active node';
      this.saving = false;
      return;
    }

    // Filter out blank env rows. Anything left is sent verbatim;
    // server-side V1 just stores it as KEY=VALUE strings.
    const env: string[] = (this.envArray.value || [])
      .map((s: string) => (s || '').trim())
      .filter((s: string) => s.length > 0);

    const body: any = {
      args: (this.form.get('args').value || '').trim(),
      env_full: env,
      launcher_mode: this.form.get('launcher_mode').value || '',
      autostart: !!this.form.get('autostart').value,
    };

    const sub = this.appsService.setAppFullConfig(node.localPk, this.app.name, body).subscribe({
      next: () => {
        this.saving = false;
        this.snackbarService.showDone('app-settings.saved');
        this.saved.emit();
      },
      error: (e: any) => {
        this.saving = false;
        // Server returns errors as plain strings or {message}; surface whichever we get.
        this.saveError = (e && e.message) ? e.message : (typeof e === 'string' ? e : 'Failed to save');
      },
    });
    this.subs.push(sub);
  }

  // Surface the visor's last-known DetailedStatus when status is Errored.
  // The AppStatus enum: 0=stopped, 1=running, 2=errored, 3=starting.
  get isErrored(): boolean { return this.app && this.app.status === 2; }
  get lastError(): string {
    if (!this.isErrored) { return ''; }
    return (this.app as any).detailed_status || (this.app as any).detailedStatus || 'errored';
  }
}
