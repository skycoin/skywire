import { Component, Inject, OnInit, ChangeDetectionStrategy } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';

import { ProxyDiscoveryService } from 'src/app/services/proxy-discovery.service';
import { ProxyDiscoveryEntry } from 'src/app/app.datatypes';
import { SnackbarService } from 'src/app/services/snackbar.service';

/**
 * Dialog that lists the public skysocks (proxy) servers from the service
 * discovery and lets the operator pick one as a skysocks-client's exit — the
 * HV-UI equivalent of `skywire cli proxy list` + selecting. Modeled on the VPN
 * server-list, but reused here for the proxy client. Returns the chosen PK (or a
 * manually-entered one) via the dialog result; the caller applies it with
 * changeAppSettings(..., { pk }).
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
  loading = true;
  loadError = false;
  filterText = '';
  manualPk = '';

  constructor(
    private dialogRef: MatDialogRef<SelectProxyServerComponent>,
    @Inject(MAT_DIALOG_DATA) public data: { clientName: string },
    private discovery: ProxyDiscoveryService,
    private snackbar: SnackbarService,
  ) {}

  ngOnInit(): void {
    this.discovery.getServices(true).subscribe({
      next: (list: ProxyDiscoveryEntry[]) => {
        this.servers = list || [];
        this.applyFilter();
        this.loading = false;
      },
      error: () => {
        this.loading = false;
        this.loadError = true;
      },
    });
  }

  applyFilter(): void {
    const t = this.filterText.trim().toLowerCase();
    this.filtered = !t
      ? this.servers
      : this.servers.filter((s) =>
          (s.pk || '').toLowerCase().includes(t) ||
          (s.location || '').toLowerCase().includes(t) ||
          (s.country || '').toLowerCase().includes(t));
  }

  countryFlag(code?: string): string {
    return code ? 'assets/img/big-flags/' + code.toLowerCase() + '.png' : '';
  }

  select(server: ProxyDiscoveryEntry): void {
    this.dialogRef.close(server.pk);
  }

  useManual(): void {
    const pk = this.manualPk.trim();
    // A skywire PK is 66 hex chars. Validate lightly so an obvious typo is caught
    // before it reaches the app config.
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
