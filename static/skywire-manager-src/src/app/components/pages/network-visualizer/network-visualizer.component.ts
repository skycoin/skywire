import { AfterViewInit, Component, ElementRef, OnDestroy, OnInit, ViewChild } from '@angular/core';
import { Router } from '@angular/router';
import { forkJoin, Subscription, interval, startWith } from 'rxjs';
import { switchMap, catchError } from 'rxjs/operators';
import { of } from 'rxjs';
import { Network } from 'vis-network/standalone';
import { DataSet } from 'vis-data/standalone';

import { NodeService } from '../../../services/node.service';
import { ApiService } from '../../../services/api.service';
import { TabButtonData } from '../../layout/top-bar/top-bar.component';
import { homeTabsData } from 'src/app/utils/home-tabs';
import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * NetworkVisualizerComponent — the in-Angular network visualizer (the former
 * standalone Go-wasm /tp-viz page). Renders a vis-network force-graph of the
 * skywire network from data the visor already serves, so it works in BOTH the
 * native HV UI and the serverless wasm-visor (the old /tp-viz/ page escaped the
 * SPA in the wasm serving):
 *   - NODES: nodeService.getNetworkView() (per-PK: country/version/status/tp-counts)
 *   - EDGES: api.get('network/transports') — TPD transports, edges[0]<->edges[1]
 *
 * First pass is a read-only graph (status-coloured nodes, type-coloured edges,
 * click-a-node -> that visor's detail). Geo grouping, physics tuning and
 * transport add/remove (the old /api/tps/*) are phased follow-ups.
 */
interface NetworkEntry {
  pk: string;
  country?: string;
  version?: string;
  services?: string;
  stcpr: number; sudph: number; dmsg: number; stcp: number; total: number;
  ut_status?: string;
}
interface TransportMetric { id: string; type: string; live: boolean; edges?: string[]; }

@Component({
  selector: 'app-network-visualizer',
  templateUrl: './network-visualizer.component.html',
  styleUrls: ['./network-visualizer.component.scss'],
  standalone: false,
})
export class NetworkVisualizerComponent extends PageBaseComponent implements OnInit, AfterViewInit, OnDestroy {
  @ViewChild('graph', { static: false }) graphEl!: ElementRef<HTMLDivElement>;

  tabsData: TabButtonData[] = [];
  loading = true;
  error: string | null = null;
  lastUpdated: Date | null = null;
  showOnlineOnly = false;
  nodeCount = 0;
  edgeCount = 0;

  private network: Network | null = null;
  private nodes = new DataSet<any>();
  private edges = new DataSet<any>();
  private sub?: Subscription;
  private viewReady = false;
  private pending: { entries: NetworkEntry[]; tps: TransportMetric[] } | null = null;

  constructor(private nodeService: NodeService, private api: ApiService, private router: Router) {
    super();
    this.tabsData = homeTabsData();
  }

  ngOnInit() {
    this.sub = interval(300000)
      .pipe(
        startWith(0),
        switchMap(() =>
          forkJoin({
            net: this.nodeService.getNetworkView().pipe(catchError(() => of({ entries: [] }))),
            tps: this.api.get('network/transports?days=1').pipe(catchError(() => of([]))),
          }),
        ),
      )
      .subscribe({
        next: (r: any) => this.onData(r?.net?.entries || [], r?.tps || []),
        error: (e) => { this.loading = false; this.error = e?.message || 'Failed to load network data'; },
      });
    return super.ngOnInit();
  }

  ngAfterViewInit() {
    this.viewReady = true;
    if (this.pending) { this.render(this.pending.entries, this.pending.tps); this.pending = null; }
  }

  refreshNow() {
    this.loading = this.nodeCount === 0;
    forkJoin({
      net: this.nodeService.getNetworkView(true).pipe(catchError(() => of({ entries: [] }))),
      tps: this.api.get('network/transports?days=1').pipe(catchError(() => of([]))),
    }).subscribe((r: any) => this.onData(r?.net?.entries || [], r?.tps || []));
  }

  private onData(entries: NetworkEntry[], tps: TransportMetric[]) {
    this.loading = false;
    this.error = null;
    this.lastUpdated = new Date();
    if (!this.viewReady) { this.pending = { entries, tps }; return; }
    this.render(entries, tps);
  }

  private statusColor(s?: string): string {
    if (s === 'online') { return '#3f8cff'; }
    if (s === 'offline') { return '#e05757'; }
    return '#888888'; // not in UT / unknown
  }

  private render(entries: NetworkEntry[], tps: TransportMetric[]) {
    const byPK = new Map<string, NetworkEntry>();
    for (const e of entries) { byPK.set(e.pk, e); }

    const wantOnline = this.showOnlineOnly;
    const include = (pk: string) => {
      if (!wantOnline) { return true; }
      const e = byPK.get(pk);
      return !!e && (e.ut_status || '') === 'online';
    };

    const nodeIds = new Set<string>();
    const nodeArr: any[] = [];
    const addNode = (pk: string) => {
      if (nodeIds.has(pk) || !include(pk)) { return; }
      nodeIds.add(pk);
      const e = byPK.get(pk);
      const title = e
        ? `${pk}\n${e.country || '?'} · ${e.version || '?'}\ntransports: ${e.total || 0} (stcpr ${e.stcpr || 0}, sudph ${e.sudph || 0}, dmsg ${e.dmsg || 0})\nstatus: ${e.ut_status || 'unknown'}`
        : pk;
      nodeArr.push({ id: pk, label: pk.substring(0, 6) + '…', color: this.statusColor(e?.ut_status), title });
    };

    for (const e of entries) { addNode(e.pk); }

    const edgeColor = (t: TransportMetric) => {
      if (!t.live) { return { color: '#b04a4a', opacity: 0.5 }; }
      switch ((t.type || '').toLowerCase()) {
        case 'dmsg': return { color: '#9d7cff' };
        case 'stcpr': case 'stcp': return { color: '#3f8cff' };
        case 'sudph': return { color: '#3fb6a8' };
        default: return { color: '#9aa0a6' };
      }
    };
    const edgeArr: any[] = [];
    for (const t of tps) {
      if (!t.edges || t.edges.length < 2) { continue; }
      const [a, b] = t.edges;
      addNode(a); addNode(b);
      if (!nodeIds.has(a) || !nodeIds.has(b)) { continue; }
      edgeArr.push({ id: t.id, from: a, to: b, color: edgeColor(t), title: `${t.type} ${t.live ? '' : '(offline)'}`.trim() });
    }

    this.nodes.clear(); this.edges.clear();
    this.nodes.add(nodeArr); this.edges.add(edgeArr);
    this.nodeCount = nodeArr.length;
    this.edgeCount = edgeArr.length;

    if (!this.network) {
      this.network = new Network(this.graphEl.nativeElement, { nodes: this.nodes, edges: this.edges }, {
        nodes: { shape: 'dot', size: 12, font: { color: '#bbb', size: 11, face: 'monospace' }, borderWidth: 0 },
        edges: { width: 1, smooth: false },
        physics: { barnesHut: { gravitationalConstant: -8000, springLength: 120, springConstant: 0.02 }, stabilization: { iterations: 200 } },
        interaction: { hover: true, tooltipDelay: 120 },
      });
      this.network.on('doubleClick', (p: any) => {
        if (p?.nodes?.length) { this.router.navigate(['/nodes', p.nodes[0], 'info']); }
      });
    }
  }

  toggleOnline() { this.showOnlineOnly = !this.showOnlineOnly; this.refreshNow(); }

  ngOnDestroy(): void {
    if (this.sub) { this.sub.unsubscribe(); }
    if (this.network) { this.network.destroy(); this.network = null; }
  }
}
