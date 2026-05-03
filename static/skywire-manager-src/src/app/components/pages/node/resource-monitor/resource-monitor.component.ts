import { Component, Input, OnDestroy, OnInit, ChangeDetectorRef } from '@angular/core';
import { Subscription, timer } from 'rxjs';

import { NodeService } from '../../../../services/node.service';

/**
 * Resource Monitor — psutil-style live view of host + visor process
 * resource utilization. Backed by the visor's HostStats (gopsutil)
 * and RuntimeStats endpoints, polled on a 1s cadence.
 *
 * Design notes:
 *   - Two tabs: "Host" (CPU%, mem%, disk%, net rate) and "Process"
 *     (heap MB, goroutines, GC count, num threads).
 *   - 60-second rolling window of samples per metric. Older samples
 *     drop off the front.
 *   - Network is rate-derived: HostStats returns cumulative byte
 *     totals; we diff successive samples to get Bps. Same shape
 *     for tx/rx; one chart with two series would be nicer than
 *     two separate charts but the existing line-chart component
 *     only takes a single series — keeping symmetry simple.
 *   - The panel collapses by default to keep the visor detail
 *     page light; toggling it open starts polling.
 */

interface HostStats {
  hostname?: string;
  os?: string;
  platform?: string;
  arch?: string;
  uptime_seconds?: number;
  cpu_percent: number;
  cpu_count: number;
  cpu_logical_count: number;
  mem_total: number;
  mem_used: number;
  mem_available: number;
  mem_percent: number;
  swap_total?: number;
  swap_used?: number;
  swap_percent?: number;
  disk_total?: number;
  disk_used?: number;
  disk_free?: number;
  disk_percent?: number;
  net_bytes_sent: number;
  net_bytes_recv: number;
  net_packets_sent?: number;
  net_packets_recv?: number;
  process?: {
    pid: number;
    cpu_percent: number;
    mem_rss: number;
    mem_vms?: number;
    num_threads?: number;
    num_fds?: number;
    start_time_ms?: number;
    open_conns?: number;
  };
}

interface RuntimeStats {
  num_goroutine: number;
  num_cpu: number;
  gomaxprocs: number;
  go_version: string;
  mem_alloc: number;
  mem_total_alloc: number;
  mem_sys: number;
  mem_heap_alloc: number;
  mem_heap_sys: number;
  num_gc: number;
}

const WINDOW = 60; // samples kept (≈ 60s at 1s polling)

@Component({
  selector: 'app-resource-monitor',
  templateUrl: './resource-monitor.component.html',
  styleUrls: ['./resource-monitor.component.scss'],
  standalone: false,
})
export class ResourceMonitorComponent implements OnInit, OnDestroy {
  @Input() nodeKey: string;

  // UI state
  expanded = false;
  activeTab: 'host' | 'process' = 'host';

  // Latest absolute snapshots (for the "current value" labels next
  // to each chart).
  host: HostStats | null = null;
  proc: RuntimeStats | null = null;
  procCPUPercent = 0;
  procMemRssMB = 0;

  // Rolling windows. Each is a number[] of length up to WINDOW.
  cpuPctSeries: number[] = [];
  memPctSeries: number[] = [];
  diskPctSeries: number[] = [];
  netRxBpsSeries: number[] = [];
  netTxBpsSeries: number[] = [];
  goroutinesSeries: number[] = [];
  heapMbSeries: number[] = [];
  gcSeries: number[] = []; // GCs/sec (derived)
  threadsSeries: number[] = [];

  // Diff state for rate-derived series.
  private prevNetRx = 0;
  private prevNetTx = 0;
  private prevGc = 0;
  private prevSampleAtMs = 0;
  private firstHostSample = true;
  private firstProcSample = true;

  private hostSub: Subscription;
  private procSub: Subscription;
  private pollSub: Subscription;

  constructor(
    private nodeService: NodeService,
    private cdr: ChangeDetectorRef,
  ) {}

  ngOnInit() {
    // Start polling lazily — the panel is collapsed by default.
  }

  ngOnDestroy() {
    this.stopPolling();
  }

  toggleExpanded() {
    this.expanded = !this.expanded;
    if (this.expanded) {
      this.startPolling();
    } else {
      this.stopPolling();
    }
  }

  setTab(tab: 'host' | 'process') {
    this.activeTab = tab;
  }

  /** Format a byte count as a human-readable string (B/K/M/G). */
  fmtBytes(n: number): string {
    if (n == null || isNaN(n)) { return '-'; }
    if (n >= 1e9) { return (n / 1e9).toFixed(1) + 'G'; }
    if (n >= 1e6) { return (n / 1e6).toFixed(1) + 'M'; }
    if (n >= 1e3) { return (n / 1e3).toFixed(1) + 'K'; }
    return n.toFixed(0) + 'B';
  }

  fmtBps(n: number): string {
    if (n == null || isNaN(n) || n < 0) { return '0B/s'; }
    return this.fmtBytes(n) + '/s';
  }

  fmtUptime(s?: number): string {
    if (!s) { return '-'; }
    const d = Math.floor(s / 86400);
    const h = Math.floor((s % 86400) / 3600);
    const m = Math.floor((s % 3600) / 60);
    if (d > 0) { return `${d}d ${h}h`; }
    if (h > 0) { return `${h}h ${m}m`; }
    return `${m}m`;
  }

  private startPolling() {
    this.stopPolling();
    // Reset diff state so the first sample after re-open doesn't
    // produce a giant rate jump.
    this.firstHostSample = true;
    this.firstProcSample = true;
    this.prevSampleAtMs = 0;

    // Tick every second. Each tick fires both probes in parallel.
    this.pollSub = timer(0, 1000).subscribe(() => {
      this.pollHost();
      this.pollProc();
    });
  }

  private stopPolling() {
    if (this.pollSub) { this.pollSub.unsubscribe(); this.pollSub = null; }
    if (this.hostSub) { this.hostSub.unsubscribe(); this.hostSub = null; }
    if (this.procSub) { this.procSub.unsubscribe(); this.procSub = null; }
  }

  private pollHost() {
    if (!this.nodeKey) { return; }
    if (this.hostSub) { this.hostSub.unsubscribe(); }
    this.hostSub = this.nodeService.getHostStats(this.nodeKey).subscribe(
      (s: HostStats) => this.onHostStats(s),
      // Silent error — keep polling; the snackbar would scream once
      // per second on a broken visor connection.
      () => {},
    );
  }

  private pollProc() {
    if (!this.nodeKey) { return; }
    if (this.procSub) { this.procSub.unsubscribe(); }
    this.procSub = this.nodeService.getRuntimeStats(this.nodeKey).subscribe(
      (s: RuntimeStats) => this.onRuntimeStats(s),
      () => {},
    );
  }

  private onHostStats(s: HostStats) {
    this.host = s;
    this.procCPUPercent = s.process ? s.process.cpu_percent : 0;
    this.procMemRssMB = s.process ? s.process.mem_rss / 1024 / 1024 : 0;

    const nowMs = Date.now();
    let dtSec = (nowMs - this.prevSampleAtMs) / 1000;
    if (dtSec <= 0 || dtSec > 5) { dtSec = 1; } // clamp on cold-start
    this.prevSampleAtMs = nowMs;

    // Gauges — push the absolute values straight in.
    this.push(this.cpuPctSeries, s.cpu_percent);
    this.push(this.memPctSeries, s.mem_percent);
    this.push(this.diskPctSeries, s.disk_percent || 0);
    this.push(this.threadsSeries, s.process ? s.process.num_threads || 0 : 0);

    // Rates — diff cumulative counters. Skip the first sample so we
    // don't emit a spike based on an uninitialized previous value.
    if (this.firstHostSample) {
      this.firstHostSample = false;
    } else {
      const rx = Math.max(0, s.net_bytes_recv - this.prevNetRx) / dtSec;
      const tx = Math.max(0, s.net_bytes_sent - this.prevNetTx) / dtSec;
      this.push(this.netRxBpsSeries, rx);
      this.push(this.netTxBpsSeries, tx);
    }
    this.prevNetRx = s.net_bytes_recv;
    this.prevNetTx = s.net_bytes_sent;
    this.cdr.markForCheck();
  }

  private onRuntimeStats(s: RuntimeStats) {
    this.proc = s;
    this.push(this.goroutinesSeries, s.num_goroutine);
    this.push(this.heapMbSeries, s.mem_heap_alloc / 1024 / 1024);
    if (this.firstProcSample) {
      this.firstProcSample = false;
    } else {
      // GCs/sec since last sample (typically very small, sometimes 0).
      this.push(this.gcSeries, Math.max(0, s.num_gc - this.prevGc));
    }
    this.prevGc = s.num_gc;
    this.cdr.markForCheck();
  }

  private push(arr: number[], v: number) {
    arr.push(v);
    if (arr.length > WINDOW) { arr.shift(); }
  }
}
