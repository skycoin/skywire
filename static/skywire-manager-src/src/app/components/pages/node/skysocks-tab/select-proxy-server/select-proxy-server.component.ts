import { Component, Inject, OnInit, ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Observable } from 'rxjs';

import { ProxyDiscoveryService } from 'src/app/services/proxy-discovery.service';
import { ProxyDiscoveryEntry, Transport } from 'src/app/app.datatypes';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { countriesList } from 'src/app/utils/countries-list';
import { ServerType } from '../../apps/app-flags-form/app-flags';

export interface SelectServerData {
  clientName: string;
  // Defaults to proxy servers.
  serviceType?: ServerType;
  // The visor's transports. A direct one gives a server's latency.
  transports?: Transport[];
}

export interface ServerCountry {
  code: string;
  name: string;
  count: number;
}

/** Fastest measured latency of a direct transport to each remote. */
export function latencyByPk(transports: Transport[] = []): Map<string, number> {
  const out = new Map<string, number>();
  transports.forEach(t => {
    const ms = t.latencyMs || 0;
    if (ms > 0 && (!out.has(t.remotePk) || ms < out.get(t.remotePk)!)) {
      out.set(t.remotePk, ms);
    }
  });

  return out;
}

/** Measured servers first by latency, the rest by location. */
export function sortServers(servers: ProxyDiscoveryEntry[], latency: Map<string, number>): ProxyDiscoveryEntry[] {
  return [...servers].sort((a, b) => {
    const la = latency.get(a.pk);
    const lb = latency.get(b.pk);
    if (la !== undefined || lb !== undefined) {
      return la === undefined ? 1 : lb === undefined ? -1 : la - lb;
    }

    return (a.location || '~').localeCompare(b.location || '~') || a.pk.localeCompare(b.pk);
  });
}

/**
 * Lists the skysocks or VPN servers in the service discovery and returns the
 * public key the operator picks, or one entered by hand, as the dialog result.
 */
@Component({
  selector: 'app-select-proxy-server',
  templateUrl: './select-proxy-server.component.html',
  styleUrls: ['./select-proxy-server.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class SelectProxyServerComponent implements OnInit {
  servers: ProxyDiscoveryEntry[] = [];
  filtered: ProxyDiscoveryEntry[] = [];
  countries: ServerCountry[] = [];
  latency = new Map<string, number>();
  loading = true;
  loadError = false;
  filterText = '';
  country = '';
  manualPk = '';

  /** Opens the picker and emits the chosen key, or nothing when cancelled. */
  static openDialog(dialog: MatDialog, data: SelectServerData): Observable<string | undefined> {
    return dialog.open(SelectProxyServerComponent, { width: '600px', data: data }).afterClosed();
  }

  constructor(
    private dialogRef: MatDialogRef<SelectProxyServerComponent>,
    @Inject(MAT_DIALOG_DATA) public data: SelectServerData,
    private discovery: ProxyDiscoveryService,
    private snackbar: SnackbarService,
    private cdr: ChangeDetectorRef,
  ) {}

  get isVpn(): boolean {
    return this.data.serviceType === 'vpn';
  }

  ngOnInit(): void {
    this.latency = latencyByPk(this.data.transports);
    this.discovery.getServices(!this.isVpn).subscribe({
      next: (list: ProxyDiscoveryEntry[]) => {
        this.servers = sortServers(list || [], this.latency);
        this.countries = this.countriesOf(this.servers);
        this.applyFilter();
        this.loading = false;
        this.cdr.markForCheck();
      },
      error: () => {
        this.loading = false;
        this.loadError = true;
        this.cdr.markForCheck();
      },
    });
  }

  private countriesOf(servers: ProxyDiscoveryEntry[]): ServerCountry[] {
    const counts = new Map<string, number>();
    servers.forEach(s => {
      const code = (s.country || '').toUpperCase();
      if (code) {
        counts.set(code, (counts.get(code) || 0) + 1);
      }
    });

    return [...counts.entries()]
      .map(([code, count]) => {
        return { code: code, name: (countriesList as any)[code] || code, count: count };
      })
      .sort((a, b) => a.name.localeCompare(b.name));
  }

  applyFilter(): void {
    const t = this.filterText.trim().toLowerCase();
    this.filtered = this.servers.filter(s =>
      (!this.country || (s.country || '').toUpperCase() === this.country) &&
      (!t ||
        (s.pk || '').toLowerCase().includes(t) ||
        (s.location || '').toLowerCase().includes(t) ||
        (s.country || '').toLowerCase().includes(t)));
  }

  countryFlag(code?: string): string {
    return code ? 'assets/img/big-flags/' + code.toLowerCase() + '.png' : '';
  }

  select(server: ProxyDiscoveryEntry): void {
    this.dialogRef.close(server.pk);
  }

  useManual(): void {
    const pk = this.manualPk.trim();
    if (!/^[0-9a-fA-F]{66}$/.test(pk)) {
      this.snackbar.showError('skysocks-tab.select-server.invalid-pk');

      return;
    }
    this.dialogRef.close(pk);
  }

  close(): void {
    this.dialogRef.close();
  }
}
