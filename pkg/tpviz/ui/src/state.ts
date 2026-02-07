// Centralized mutable state — single source of truth
// Every other module imports what it needs from here.

import { DataSet } from 'vis-data';
import { Network } from 'vis-network';
import type {
    Transport, UptimeEntry, LocalVisorData, DMSGData, IPGroupsData,
    VisorServiceEntry, ConnectionInfo, GroupBoundary, GroupCentroid, SatelliteOrbit,
} from './types';

// vis-network instances
export let network: Network | null = null;
export let nodesDataset: DataSet<any> | null = null;
export let edgesDataset: DataSet<any> | null = null;

export function setNetwork(n: Network | null) { network = n; }
export function setNodesDataset(ds: DataSet<any> | null) { nodesDataset = ds; }
export function setEdgesDataset(ds: DataSet<any> | null) { edgesDataset = ds; }

// Transport + uptime data
export let allData: Transport[] | null = null;
export let uptimeData: UptimeEntry[] | null = null;
export let servicesData: Record<string, { services?: string[]; country?: string }> | null = null;

export function setAllData(d: Transport[] | null) { allData = d; }
export function setUptimeData(d: UptimeEntry[] | null) { uptimeData = d; }
export function setServicesData(d: Record<string, { services?: string[]; country?: string }> | null) { servicesData = d; }

// Visor tracking
export let visorConnections: Record<string, ConnectionInfo[]> = {};
export let onlineVisors = new Set<string>();
export let offlineVisors = new Set<string>();
export let visorServices: Record<string, VisorServiceEntry> = {};
export let visorVersions: Record<string, string> = {};

export function setVisorConnections(v: Record<string, ConnectionInfo[]>) { visorConnections = v; }
export function setOnlineVisors(s: Set<string>) { onlineVisors = s; }
export function setOfflineVisors(s: Set<string>) { offlineVisors = s; }
export function setVisorServices(v: Record<string, VisorServiceEntry>) { visorServices = v; }
export function setVisorVersions(v: Record<string, string>) { visorVersions = v; }

// Auto-refresh state
export let nextRefreshTime: number | null = null;
export let countdownInterval: ReturnType<typeof setInterval> | null = null;
export let serverCacheAge: number | null = null;
export let isRefreshing = false;

export function setNextRefreshTime(t: number | null) { nextRefreshTime = t; }
export function setCountdownInterval(i: ReturnType<typeof setInterval> | null) { countdownInterval = i; }
export function setServerCacheAge(a: number | null) { serverCacheAge = a; }
export function setIsRefreshing(r: boolean) { isRefreshing = r; }

// IP groups
export let ipGroupsData: IPGroupsData | null = null;
export let ipGroupsEnabled = false;

export function setIPGroupsData(d: IPGroupsData | null) { ipGroupsData = d; }
export function setIPGroupsEnabled(e: boolean) { ipGroupsEnabled = e; }

// DMSG
export let dmsgData: DMSGData | null = null;
export let dmsgEntries = new Set<string>();

export function setDmsgData(d: DMSGData | null) { dmsgData = d; }
export function setDmsgEntries(s: Set<string>) { dmsgEntries = s; }

// Local visor
export let localVisorData: LocalVisorData | null = null;
export let localVisorWS: WebSocket | null = null;
export let wsReconnectAttempts = 0;

export function setLocalVisorData(d: LocalVisorData | null) { localVisorData = d; }
export function setLocalVisorWS(ws: WebSocket | null) { localVisorWS = ws; }
export function setWSReconnectAttempts(n: number) { wsReconnectAttempts = n; }

// Selection state
export let selectedNodeId: string | null = null;
export let routeDestinations = new Set<string>();
export let lastNewNodeIds = new Set<string>();
export let selectedClusterId: string | null = null;
export let groupNodeMap = new Map<string, Set<string>>();

export function setSelectedNodeId(id: string | null) { selectedNodeId = id; }
export function setRouteDestinations(s: Set<string>) { routeDestinations = s; }
export function setLastNewNodeIds(s: Set<string>) { lastNewNodeIds = s; }
export function setSelectedClusterId(id: string | null) { selectedClusterId = id; }

// Physics
export let userPhysicsDisabled = false;
export function setUserPhysicsDisabled(d: boolean) { userPhysicsDisabled = d; }

// Satellite nodes (orbiting unknown-country nodes)
export let satelliteNodeIds = new Set<string>();
export let satelliteOrbitAnimId: number | null = null;
export let satelliteOrbits = new Map<string, SatelliteOrbit>();
export let orbitCenter = { x: 0, y: 0 };
export let orbitBaseRadius = 500;

export function setSatelliteNodeIds(s: Set<string>) { satelliteNodeIds = s; }
export function setSatelliteOrbitAnimId(id: number | null) { satelliteOrbitAnimId = id; }
export function setOrbitCenter(c: { x: number; y: number }) { orbitCenter = c; }
export function setOrbitBaseRadius(r: number) { orbitBaseRadius = r; }

// TPS state
export let tpsRunning = false;
export let tpsPK = '';
export let tpsPickMode: string | null = null;
export let tpsGroupPickMode: string | null = null;
export let tpsOverlayEdges = new Map<string, any>();
export let dmsgHealthPickMode = false;
export let localTpPickMode = false;

export function setTpsRunning(r: boolean) { tpsRunning = r; }
export function setTpsPK(pk: string) { tpsPK = pk; }
export function setTpsPickMode(m: string | null) { tpsPickMode = m; }
export function setTpsGroupPickMode(m: string | null) { tpsGroupPickMode = m; }
export function setDmsgHealthPickMode(m: boolean) { dmsgHealthPickMode = m; }
export function setLocalTpPickMode(m: boolean) { localTpPickMode = m; }

// Multi-hop
export let mhHops: string[] = [];
export let mhPickMode = false;

export function setMhHops(h: string[]) { mhHops = h; }
export function setMhPickMode(m: boolean) { mhPickMode = m; }

// Grouping
export let groupingMode: string | null = null;
export let groupBoundaries: GroupBoundary[] = [];
export let groupForceInterval: ReturnType<typeof setInterval> | null = null;
export let dualCountryCentroids: Map<string, GroupCentroid> | null = null;
export let currentGroupKeyFn: ((nodeId: string) => string) | null = null;
export let fixedGroupCentroids: Map<string, GroupCentroid> | null = null;
export let originalPositions: Record<string, { x: number; y: number }> | null = null;

export function setGroupingMode(m: string | null) { groupingMode = m; }
export function setGroupBoundaries(b: GroupBoundary[]) { groupBoundaries = b; }
export function setGroupForceInterval(i: ReturnType<typeof setInterval> | null) { groupForceInterval = i; }
export function setDualCountryCentroids(c: Map<string, GroupCentroid> | null) { dualCountryCentroids = c; }
export function setCurrentGroupKeyFn(fn: ((nodeId: string) => string) | null) { currentGroupKeyFn = fn; }
export function setFixedGroupCentroids(c: Map<string, GroupCentroid> | null) { fixedGroupCentroids = c; }
export function setOriginalPositions(p: Record<string, { x: number; y: number }> | null) { originalPositions = p; }

// Flow animation
export let flowParticles: any[] = [];
export let flowAnimationId: number | null = null;
export let lastFlowUpdate = 0;

export function setFlowParticles(p: any[]) { flowParticles = p; }
export function setFlowAnimationId(id: number | null) { flowAnimationId = id; }

// Route tracking
export let previousRouteIds = new Set<string>();
export let highlightedRouteTimeout: ReturnType<typeof setTimeout> | null = null;
export let localVisorRefreshInterval: ReturnType<typeof setInterval> | null = null;

export function setPreviousRouteIds(s: Set<string>) { previousRouteIds = s; }
export function setHighlightedRouteTimeout(t: ReturnType<typeof setTimeout> | null) { highlightedRouteTimeout = t; }
export function setLocalVisorRefreshInterval(i: ReturnType<typeof setInterval> | null) { localVisorRefreshInterval = i; }

// IP group color cache
export const ipGroupColors: Record<number, string> = {};
