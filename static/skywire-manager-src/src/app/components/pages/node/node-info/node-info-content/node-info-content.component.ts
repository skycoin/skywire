import { Component, Input, OnDestroy, NgZone } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { Subscription, interval } from 'rxjs';

import { Node } from '../../../../../app.datatypes';
import { EditLabelComponent } from 'src/app/components/layout/edit-label/edit-label.component';
import { NodeComponent } from '../../node.component';
import TimeUtils, { ElapsedTime } from 'src/app/utils/timeUtils';
import { LabeledElementTypes, StorageService } from 'src/app/services/storage.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { ApiService, RequestOptions, RequestTypes } from 'src/app/services/api.service';

/**
 * Shows the basic info of a node. The reward-address management,
 * transports summary + toggles, and router/min-hops control used to
 * live here too — those moved out to their own dedicated tabs
 * (Rewards / Transports / Routing) so the Info surface stays a
 * read-only identity card.
 */
@Component({
    selector: 'app-node-info-content',
    templateUrl: './node-info-content.component.html',
    styleUrls: ['./node-info-content.component.scss'],
    standalone: false
})
export class NodeInfoContentComponent implements OnDestroy {
  @Input() set nodeInfo(val: Node) {
    this.node = val;
    // Reset the live-timer baseline whenever new data arrives.
    // baselineSeconds is the canonical secondsOnline from the
    // backend; nodeFetchedAt is the wall-clock moment that value
    // applied. The 1Hz tick (see ctor) advances from there.
    this.baselineSecondsOnline = val.secondsOnline ?? 0;
    this.nodeFetchedAt = Date.now();
    this.recomputeTimeOnline();
    this.fetchPorts(val.localPk);
  }

  node: Node;
  timeOnline: ElapsedTime;

  // Live-counter state for "Time online". The backend refreshes the
  // visor's seconds_online roughly every 10s; the local 1Hz tick
  // fills the gaps so the operator sees the counter advance rather
  // than freezing on the last refresh. Drift between ticks is
  // corrected on the next backend refresh (nodeInfo setter).
  private baselineSecondsOnline = 0;
  private nodeFetchedAt = 0;
  private timeTickSub?: Subscription;
  ports: { name: string, value: string }[] = [];
  showPorts = false;
  rawConfig = '';

  // Collapsible Runtime Configuration section (matches Ports pattern).
  showConfigSection = false;

  // Collapsible DMSG sessions section. Used to be a top-level
  // tab; folded here to keep the per-visor tab bar from
  // overflowing. Lazy-mounts the existing DmsgSettingsComponent.
  showDmsgSettings = false;

  // Collapsible Router Settings section. Wraps the visor-wide knobs
  // that 'cli proxy start --local-route' / '--existing-tp' / '--mux'
  // / '--min-hops' set; UI parity with the CLI for visor-wide
  // dial-time options. Lazy-loaded on first open via fetchRouterSettings.
  showRouterSettings = false;
  routerSettingsLoaded = false;
  routerSettings = {
    force_local_routes: false,
    existing_tp_only: false,
    mux_routes: 1,
    min_hops: 0,
  };
  routerSettingsSaving = false;
  routerSettingsSaveMsg = '';
  routerSettingsSaveErr = '';

  // Edit-mode state for the runtime-config editor.
  editingConfig = false;
  configDraft = '';
  configError = '';   // client-side parse error
  configSaving = false;
  configSaveMsg = ''; // success message (e.g. "saved; restart required")
  configSaveErr = ''; // server-side save error

  constructor(
    private dialog: MatDialog,
    public storageService: StorageService,
    private snackbarService: SnackbarService,
    private apiService: ApiService,
    private ngZone: NgZone,
  ) {
    // 1Hz tick for the live online-time counter. Runs outside
    // Angular's zone so the periodic emission doesn't cause an
    // app-wide change-detection sweep — we re-enter the zone only
    // when we have a new value to render. Negligible CPU.
    this.ngZone.runOutsideAngular(() => {
      this.timeTickSub = interval(1000).subscribe(() => {
        this.ngZone.run(() => this.recomputeTimeOnline());
      });
    });
  }

  ngOnDestroy() {
    this.timeTickSub?.unsubscribe();
  }

  private recomputeTimeOnline() {
    if (!this.nodeFetchedAt) { return; }
    const elapsedSeconds = this.baselineSecondsOnline
      + Math.floor((Date.now() - this.nodeFetchedAt) / 1000);
    this.timeOnline = TimeUtils.getElapsedTime(elapsedSeconds);
  }

  /**
   * True when `pk` matches the hypervisor that's serving this UI.
   * Used by the template to render ★ next to the local hypervisor
   * in the per-visor "Connected hypervisors" list.
   */
  isLocalHypervisor(pk: string): boolean {
    return !!pk && pk === this.storageService.getLocalHypervisorPk();
  }

  showEditLabelDialog() {
    let labelInfo =  this.storageService.getLabelInfo(this.node.localPk);
    if (!labelInfo) {
      labelInfo = {
        id: this.node.localPk,
        label: '',
        identifiedElementType: LabeledElementTypes.Node,
      };
    }

    EditLabelComponent.openDialog(this.dialog, labelInfo).afterClosed().subscribe((changed: boolean) => {
      if (changed) {
        NodeComponent.refreshCurrentDisplayedData();
      }
    });
  }

  hasDmsgServer() {
    if (!this.node || this.node.dmsgServerPk.replace(/0/g, '').length === 0) {
      return false;
    }
    return true;
  }

  /**
   * Formats latency from nanoseconds to ms string.
   */
  formatLatency(latencyNs: number): string {
    const ms = latencyNs / 1000000;
    if (ms < 10) {
      return ms.toFixed(2) + 'ms';
    }
    return Math.round(ms) + 'ms';
  }

  private fetchPorts(pk: string) {
    this.apiService.get(`visors/${pk}/ports`).subscribe((result: any) => {
      if (result && typeof result === 'object') {
        this.ports = Object.entries(result).map(([name, value]) => ({
          name: name,
          value: JSON.stringify(value),
        }));
      } else {
        this.ports = [];
      }
    }, () => {
      this.ports = [];
    });
  }

  /** Collapsible router-settings section toggle. Lazy-loads on
   *  first open. */
  onRouterSettingsToggle() {
    this.showRouterSettings = !this.showRouterSettings;
    if (this.showRouterSettings && !this.routerSettingsLoaded) {
      this.fetchRouterSettings();
    }
  }

  private fetchRouterSettings() {
    if (!this.node) { return; }
    this.apiService.get(`visors/${this.node.localPk}/router-settings`).subscribe(
      (result: any) => {
        this.routerSettings = {
          force_local_routes: !!result.force_local_routes,
          existing_tp_only: !!result.existing_tp_only,
          mux_routes: typeof result.mux_routes === 'number' ? result.mux_routes : 1,
          min_hops: typeof result.min_hops === 'number' ? result.min_hops : 0,
        };
        this.routerSettingsLoaded = true;
      },
      () => { this.snackbarService.showError('common.loading-error'); },
    );
  }

  saveRouterSettings() {
    if (!this.node || this.routerSettingsSaving) { return; }
    this.routerSettingsSaving = true;
    this.routerSettingsSaveMsg = '';
    this.routerSettingsSaveErr = '';
    this.apiService.put(
      `visors/${this.node.localPk}/router-settings`,
      this.routerSettings,
    ).subscribe(
      (result: any) => {
        this.routerSettingsSaving = false;
        this.routerSettings = {
          force_local_routes: !!result.force_local_routes,
          existing_tp_only: !!result.existing_tp_only,
          mux_routes: typeof result.mux_routes === 'number' ? result.mux_routes : 1,
          min_hops: typeof result.min_hops === 'number' ? result.min_hops : 0,
        };
        this.routerSettingsSaveMsg = 'Saved';
      },
      (err: any) => {
        this.routerSettingsSaving = false;
        this.routerSettingsSaveErr = (err && err.message) ? err.message :
          (typeof err === 'string' ? err : 'Save failed');
      },
    );
  }

  /** Collapsible config section toggle. Fetches the runtime config
   *  the first time the section is opened, then caches it. */
  onConfigToggle() {
    this.showConfigSection = !this.showConfigSection;
    if (this.showConfigSection && !this.rawConfig) {
      this.apiService.get(`visors/${this.node.localPk}/runtime-config`).subscribe(
        (result: any) => { this.rawConfig = JSON.stringify(result, null, 2); },
        () => { this.snackbarService.showError('common.loading-error'); },
      );
    }
  }

  /** Enter edit mode: copy the rendered config into the draft
   *  buffer so the user starts from the live state. */
  startEditConfig() {
    this.editingConfig = true;
    this.configDraft = this.rawConfig;
    this.configError = '';
    this.configSaveErr = '';
    this.configSaveMsg = '';
  }

  /** Exit edit mode without saving. Discards the draft. */
  cancelEditConfig() {
    this.editingConfig = false;
    this.configDraft = '';
    this.configError = '';
    this.configSaveErr = '';
  }

  /** Live JSON validation as the user types. Empty error string
   *  means the draft parses cleanly. */
  onConfigDraftChange(next: string) {
    this.configDraft = next;
    this.configSaveMsg = '';
    this.configSaveErr = '';
    if (!next.trim()) {
      this.configError = 'Config is empty';
      return;
    }
    try {
      JSON.parse(next);
      this.configError = '';
    } catch (e: any) {
      this.configError = e?.message || 'Invalid JSON';
    }
  }

  /** PUT the draft to the visor. Server validates again
   *  (strict decode + SK/PK consistency); on success we update
   *  rawConfig so the read-only pre re-renders the saved state. */
  saveConfig() {
    if (this.configError || this.configSaving) { return; }
    this.configSaving = true;
    this.configSaveErr = '';
    this.configSaveMsg = '';
    this.apiService.put(
      `visors/${this.node.localPk}/runtime-config`,
      this.configDraft,
      new RequestOptions({ requestType: RequestTypes.RawJson }),
    ).subscribe(
      (resp: any) => {
        this.configSaving = false;
        this.rawConfig = this.configDraft;
        this.editingConfig = false;
        this.configSaveMsg = (resp && resp.restart_required)
          ? 'Saved. Restart the visor for changes to take effect.'
          : 'Saved.';
      },
      (err: any) => {
        this.configSaving = false;
        this.configSaveErr = err?.originalError?.error?.error || err?.message || 'Save failed';
      },
    );
  }
}
