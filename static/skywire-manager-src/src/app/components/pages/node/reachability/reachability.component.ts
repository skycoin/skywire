import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { ApiService } from 'src/app/services/api.service';

/**
 * Per-visor Reachability tab. Three actions, all driven by HTTP
 * wrappers around RPC/dmsghttp clients the visor already runs:
 *
 *   - Skywire-route ping  → POST /visors/<pk>/ping-target
 *   - DMSG ping           → POST /visors/<pk>/dmsg-ping-target
 *   - /health over DMSG   → GET  /visors/<pk>/health-fetch?target=<pk>
 *
 * The whole tab is a single form with a target PK + tries/size, and
 * three "run" buttons that trigger one of the actions and append a
 * result row. Results stay in the page until the user clears them.
 */

interface PingResult {
  target: string;
  latencies_ms: number[];
  min_ms: number;
  max_ms: number;
  mean_ms: number;
  success_count: number;
}
interface HealthResult {
  target: string;
  status_url: string;
  status: number;
  latency_ms: number;
  body: string;
  error?: string;
}

interface ResultRow {
  kind: 'skynet-ping' | 'dmsg-ping' | 'health';
  target: string;
  startedAt: Date;
  ok: boolean;
  summary: string;
  detail: string;
}

@Component({
  selector: 'app-reachability',
  templateUrl: './reachability.component.html',
  styleUrls: ['./reachability.component.scss'],
  standalone: false,
})
export class ReachabilityComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;

  // Form state
  target = '';
  tries = 5;
  size = 1;
  inFlight: 'skynet-ping' | 'dmsg-ping' | 'health' | null = null;

  // Result log (most recent first).
  results: ResultRow[] = [];

  private nodeSub: Subscription;
  private actionSub: Subscription;

  constructor(private api: ApiService) { super(); }

  ngOnInit() {
    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      this.node = node;
    });
    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.nodeSub?.unsubscribe();
    this.actionSub?.unsubscribe();
  }

  clearResults() { this.results = []; }

  runSkynetPing() { this.runPing('skynet-ping', `visors/${this.node.localPk}/ping-target`); }
  runDmsgPing()   { this.runPing('dmsg-ping',   `visors/${this.node.localPk}/dmsg-ping-target`); }

  /** Skywire-route or DMSG ping (the URL is the only difference). */
  private runPing(kind: 'skynet-ping' | 'dmsg-ping', endpoint: string) {
    if (!this.node || !this.target.trim()) { return; }
    this.inFlight = kind;
    const startedAt = new Date();
    const target = this.target.trim();
    this.actionSub = this.api.post(endpoint, {
      target,
      tries: Number(this.tries) || 5,
      size: Number(this.size) || 1,
    }).subscribe({
      next: (r: PingResult) => {
        this.inFlight = null;
        const ok = r.success_count > 0;
        const summary = ok
          ? `${r.success_count}/${r.latencies_ms.length} OK — mean ${r.mean_ms}ms (${r.min_ms}–${r.max_ms}ms)`
          : `0/${r.latencies_ms.length} successful`;
        const detail = `latencies: [${r.latencies_ms.join(', ')}] ms`;
        this.results.unshift({ kind, target, startedAt, ok, summary, detail });
      },
      error: (err: any) => {
        this.inFlight = null;
        const msg = err?.error?.error || err?.message || 'request failed';
        this.results.unshift({
          kind, target, startedAt, ok: false,
          summary: 'error',
          detail: msg,
        });
      },
    });
  }

  /** Fetch /health over DMSG. */
  runHealth() {
    if (!this.node || !this.target.trim()) { return; }
    this.inFlight = 'health';
    const startedAt = new Date();
    const target = this.target.trim();
    this.actionSub = this.api.get(
      `visors/${this.node.localPk}/health-fetch?target=${encodeURIComponent(target)}`,
    ).subscribe({
      next: (r: HealthResult) => {
        this.inFlight = null;
        const ok = r.status === 200 && !r.error;
        const summary = r.error
          ? `error after ${r.latency_ms}ms`
          : `HTTP ${r.status} — ${r.latency_ms}ms`;
        // Try to parse the body as JSON for a friendlier summary;
        // fall back to the raw string if it isn't.
        let detail = r.body || '';
        try {
          const parsed = JSON.parse(r.body || '{}');
          detail = JSON.stringify(parsed, null, 2);
        } catch { /* leave raw */ }
        if (r.error) { detail = r.error; }
        this.results.unshift({
          kind: 'health', target, startedAt, ok,
          summary,
          detail,
        });
      },
      error: (err: any) => {
        this.inFlight = null;
        const msg = err?.error?.error || err?.message || 'request failed';
        this.results.unshift({
          kind: 'health', target, startedAt, ok: false,
          summary: 'error',
          detail: msg,
        });
      },
    });
  }

  trackResult(_idx: number, r: ResultRow): string {
    return r.startedAt.toISOString() + r.kind + r.target;
  }
}
