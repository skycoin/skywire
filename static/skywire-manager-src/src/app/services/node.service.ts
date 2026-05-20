import { Injectable } from '@angular/core';
import { Observable, map } from 'rxjs';
import BigNumber from 'bignumber.js';

import { StorageService } from './storage.service';
import { Node } from '../app.datatypes';
import { ApiService, RequestOptions, ResponseTypes } from './api.service';

/**
 * Known statuses the API returns in the health property of the visors.
 */
export enum KnownHealthStatuses {
  Connecting = 'connecting',
  Unhealthy = 'unhealthy',
  Healthy = 'healthy',
}

/**
 * Keys for saving custom settings for the calls to the updater API endpoints.
 */
// TODO: remove after removing the old updater.
export enum UpdaterStorageKeys {
  /**
   * If has a value, at least one of the other keys have a value.
   */
  UseCustomSettings = 'updaterUseCustomSettings',
  Channel = 'updaterChannel',
  Version = 'updaterVersion',
  ArchiveURL = 'updaterArchiveURL',
  ChecksumsURL = 'updaterChecksumsURL',
}

/**
 * One section of the hypervisor's visor tree — typically one
 * hypervisor's directly-connected visors. Returned by getNodesTree()
 * to preserve the structural information the flat getNodes() loses.
 *
 * - For the local hypervisor's section, `viaChain` is empty.
 * - For each sub-hypervisor section, `viaChain` is the path of
 *   hypervisor PKs from the local one down to (but not including)
 *   `hypervisorPk`. Used by the UI to render a breadcrumb above the
 *   section's table.
 * - `subError` is set when the sub-hypervisor query failed; `nodes`
 *   is empty in that case. The UI renders a placeholder row with
 *   the error.
 */
export interface NodeSection {
  hypervisorPk: string;
  viaChain: string[];
  nodes: Node[];
  subError?: string;
}

/**
 * Allows to work with the nodes.
 */
@Injectable({
  providedIn: 'root'
})
export class NodeService {
  constructor(
    private apiService: ApiService,
    private storageService: StorageService,
  ) { }

  /**
   * Gets the list of the nodes connected to the hypervisor (flat —
   * does not preserve sub-hypervisor grouping). Backed by
   * /api/visors-summary. Use getNodesTree() if you need the
   * structural information for rendering per-sub-hypervisor tables.
   */
  public getNodes(): Observable<Node[]> {
    let nodes: Node[] = [];

    return this.apiService.get('visors-summary').pipe(map((result: any[]) => {
      // Save the visor list.
      if (result) {
        result.forEach(response => {
          const node = new Node();

          // Basic data.
          node.online = response.online;
          // Cached-summary metadata (set when the hypervisor served
          // a stale row because the live RPC failed). Both undefined
          // on live rows and on never-seen placeholders.
          node.offlineSince = response.offline_since;
          node.lastSeenAt = response.last_seen_at;
          node.isStale = !!response.offline_since;
          node.localPk = response.overview.local_pk;
          node.version = response.overview.build_info.version;
          node.configVersion = response.config_version;
          node.os = response.overview.build_info.os;
          node.arch = response.overview.build_info.arch;
          node.autoconnectTransports = response.public_autoconnect;
          node.isPublic = response.is_public;
          node.buildTag = response.build_tag ? response.build_tag : '';
          node.rewardsAddress = response.reward_address;

          // Ip.
          if (response.overview && response.overview.local_ip && (response.overview.local_ip as string).trim()) {
            node.ip = response.overview.local_ip;
          } else {
            node.ip = null;
          }

          // Public IP.
          if (response.overview && response.overview.public_ip && (response.overview.public_ip as string).trim()) {
            node.publicIp = response.overview.public_ip;
          } else {
            node.publicIp = null;
          }

          // Symmetric NAT status.
          node.isSymmeticNat = response.overview.is_symmetic_nat;

          // Geolocation data.
          if (response.overview.country_code) {
            node.countryCode = response.overview.country_code;
          }
          if (response.overview.region_name) {
            node.regionName = response.overview.region_name;
          }
          if (response.overview.city_name) {
            node.cityName = response.overview.city_name;
          }
          if (response.overview.latitude) {
            node.latitude = response.overview.latitude;
          }
          if (response.overview.longitude) {
            node.longitude = response.overview.longitude;
          }

          // Label.
          const labelInfo = this.storageService.getLabelInfo(node.localPk);
          node.label = labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(node);

          // Offline rows from the hypervisor cache still carry the
          // visor's last-known fields (version, transports, dmsg
          // servers, etc.) — keep parsing them so the UI can dim the
          // row but show the data the user just lost. Truly absent
          // visors (no cache hit) come through as offline rows with
          // no overview/health/dmsg_stats; gate the per-section
          // accesses below on the field existing.

          // Health data.
          if (response.health) {
            node.health = {
              servicesHealth: response.health.services_health,
              uptimeTrackerHealth: response.health.uptime_tracker_health,
              autoconnectHealth: response.health.autoconnect_health,
              transportabilityHealth: response.health.transportability_health,
            };
          }

          // DMSG info. dmsg_stats may be null on cached-offline rows
          // captured before the visor reported any DMSG client data.
          node.dmsgServerPk = response.dmsg_stats ? response.dmsg_stats.server_public_key : '';
          node.connectedDmsgServers = response.connected_dmsg_servers || [];
          node.roundTripPing = response.dmsg_stats ? this.nsToMs(response.dmsg_stats.round_trip) : '';

          // Parse new dmsg_servers with per-server latencies
          if (response.dmsg_servers && Array.isArray(response.dmsg_servers)) {
            node.dmsgServers = response.dmsg_servers.map((s: any) => {
return {
              pk: s.pk,
              latency: s.latency || 0
            }
});
          }

          // Transports.
          node.transports = [];
          if (response.overview && response.overview.transports) {
            (response.overview.transports as any[]).forEach(transport => {
              node.transports.push({
                id: transport.id,
                localPk: transport.local_pk,
                remotePk: transport.remote_pk,
                type: transport.type,
                recv: transport.log ? transport.log.recv : 0,
                sent: transport.log ? transport.log.sent : 0,
                latencyMs: transport.latency_ms || 0,
              });
            });
          }

          // Apps (for services column).
          node.apps = [];
          if (response.overview && response.overview.apps) {
            (response.overview.apps as any[]).forEach(app => {
              node.apps.push({
                name: app.name,
                autostart: app.auto_start,
                port: app.port,
                status: app.status,
                detailedStatus: app.detailed_status,
                args: app.args,
              });
            });
          }

          // Check if is hypervisor.
          node.isHypervisor = response.is_hypervisor;

          nodes.push(node);
        });
      }

      // Create lists with the nodes returned by the api.
      const obtainedNodes = new Map<string, Node>();
      const nodesToRegisterInLocalStorageAsOnline: string[] = [];
      const ipsToRegisterInLocalStorageAsOnline: string[] = [];
      nodes.forEach(node => {
        obtainedNodes.set(node.localPk, node);
        if (node.online) {
          nodesToRegisterInLocalStorageAsOnline.push(node.localPk);
          ipsToRegisterInLocalStorageAsOnline.push(node.ip);
        }
      });

      // Save all online nodes.
      this.storageService.includeVisibleLocalNodes(nodesToRegisterInLocalStorageAsOnline, ipsToRegisterInLocalStorageAsOnline);

      const missingSavedNodes: Node[] = [];
      this.storageService.getSavedLocalNodes().forEach(node => {
        // If the backend did not return a saved node, add it to the response as an offline node.
        if (!obtainedNodes.has(node.publicKey) && !node.hidden) {
          const newNode: Node = new Node();
          newNode.localPk = node.publicKey;
          const labelInfo = this.storageService.getLabelInfo(node.publicKey);
          newNode.label = labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(newNode);
          newNode.online = false;
          newNode.dmsgServerPk = '';
          newNode.roundTripPing = '';

          missingSavedNodes.push(newNode);
        }

        // If the backend returned a node, informed that it is offline and the saved data indicates
        // that the user deleted it from the node list in the past, remove it from the response.
        if (obtainedNodes.has(node.publicKey) && !obtainedNodes.get(node.publicKey).online && node.hidden) {
          obtainedNodes.delete(node.publicKey);
        }
      });

      nodes = [];
      obtainedNodes.forEach(value => nodes.push(value));
      nodes = nodes.concat(missingSavedNodes);

      return nodes;
    }));
  }

  /**
   * Gets the tree of nodes per hypervisor. Returns one
   * NodeSection per hypervisor reachable from the local one — local
   * hypervisor first, then each sub-hypervisor that the local one is
   * directly connected to.
   *
   * Powers the main node list UI's per-sub-hypervisor table rendering
   * (#2633 / #2640). Backed by /api/visors-tree-summary.
   *
   * Visor entries replicate across sections by design: a visor
   * connected to multiple hypervisors appears in every relevant
   * section. Section dedup is on the backend (mutual hypervisor
   * pairs render the shared sub-hypervisor once).
   *
   * Reuses getNodes()'s mapping: each section's `visors` array has
   * the same shape as the /visors-summary array, so the per-row
   * parsing is identical. This wrapper just groups the rows into
   * sections.
   */
  public getNodesTree(): Observable<NodeSection[]> {
    return this.apiService.get('visors-tree-summary').pipe(map((result: any) => {
      const sections: NodeSection[] = [];
      if (!result || !result.sections) {
        return sections;
      }
      for (const section of result.sections as any[]) {
        const nodeSection: NodeSection = {
          hypervisorPk: section.hypervisor_pk,
          viaChain: section.via_chain || [],
          nodes: section.visors ? this.parseVisorsResponse(section.visors) : [],
        };
        if (section.sub_error) {
          nodeSection.subError = section.sub_error;
        }
        sections.push(nodeSection);
      }
      return sections;
    }));
  }

  /**
   * Parses a /visors-summary-shaped array into Node[]. Same logic as
   * getNodes()'s pipe but exposed as a private helper so
   * getNodesTree() can run it per section without duplicating the
   * ~170 lines of field mapping.
   *
   * The savedLocalNodes / hidden-node merge that getNodes() does
   * AFTER parsing isn't repeated here — sections are per-hypervisor
   * and the saved-nodes invariant applies to the hypervisor-wide
   * view, not the per-section one. Callers that need that merge
   * should use getNodes() directly.
   */
  private parseVisorsResponse(result: any[]): Node[] {
    const out: Node[] = [];
    if (!result) {
      return out;
    }
    // Mirrors the per-element mapping inside getNodes()'s pipe. The
    // duplication is intentional and tracked: if the field mapping
    // grows substantially, extract to a shared helper. For now,
    // duplicating ~170 lines is the smallest risk-bounded change
    // (refactoring getNodes()'s pipe is a separate concern).
    result.forEach(response => {
      const node = new Node();

      // Basic data.
      node.online = response.online;
      node.offlineSince = response.offline_since;
      node.lastSeenAt = response.last_seen_at;
      node.isStale = !!response.offline_since;
      node.localPk = response.overview ? response.overview.local_pk : '';
      node.version = response.overview && response.overview.build_info ? response.overview.build_info.version : '';
      node.configVersion = response.config_version;
      node.os = response.overview && response.overview.build_info ? response.overview.build_info.os : '';
      node.arch = response.overview && response.overview.build_info ? response.overview.build_info.arch : '';
      node.autoconnectTransports = response.public_autoconnect;
      node.isPublic = response.is_public;
      node.buildTag = response.build_tag ? response.build_tag : '';
      node.rewardsAddress = response.reward_address;

      if (response.overview && response.overview.local_ip && (response.overview.local_ip as string).trim()) {
        node.ip = response.overview.local_ip;
      } else {
        node.ip = null;
      }
      if (response.overview && response.overview.public_ip && (response.overview.public_ip as string).trim()) {
        node.publicIp = response.overview.public_ip;
      } else {
        node.publicIp = null;
      }
      if (response.overview) {
        node.isSymmeticNat = response.overview.is_symmetic_nat;
        if (response.overview.country_code) { node.countryCode = response.overview.country_code; }
        if (response.overview.region_name) { node.regionName = response.overview.region_name; }
        if (response.overview.city_name) { node.cityName = response.overview.city_name; }
        if (response.overview.latitude) { node.latitude = response.overview.latitude; }
        if (response.overview.longitude) { node.longitude = response.overview.longitude; }
      }

      const labelInfo = this.storageService.getLabelInfo(node.localPk);
      node.label = labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(node);

      // Sub-hypervisor visors carry projected Summary fields — health,
      // dmsg_stats, route_groups etc. stay zero/empty. The existing
      // optional-chaining below handles that gracefully (same path
      // taken for offline cached rows in getNodes()).
      if (response.health) {
        node.health = {
          servicesHealth: response.health.services_health,
          uptimeTrackerHealth: response.health.uptime_tracker_health,
          autoconnectHealth: response.health.autoconnect_health,
          transportabilityHealth: response.health.transportability_health,
        };
      }
      out.push(node);
    });
    return out;
  }

  /**
   * Converts a ns value to a ms string. It includes 2 decimals is the final value is less than 10.
   * @param time Value to convert.
   */
  private nsToMs(time: number) {
    let value = new BigNumber(time).dividedBy(1000000);

    if (value.isLessThan(10)) {
      value = value.decimalPlaces(2);
    } else {
      value = value.decimalPlaces(0);
    }

    return value.toString(10);
  }

  /**
   * Gets the details of a specific node.
   */
  public getNode(nodeKey: string): Observable<Node> {
    // Get the node data.
    return this.apiService.get(`visors/${nodeKey}/summary`).pipe(
      map((response: any) => {
        const node = new Node();

        // Basic data.
        node.localPk = response.overview.local_pk;
        node.version = response.overview.build_info.version;
        node.configVersion = response.config_version;
        node.os = response.overview.build_info.os;
        node.arch = response.overview.build_info.arch;
        node.secondsOnline = Math.floor(Number.parseFloat(response.uptime));
        node.minHops = response.min_hops;
        node.buildTag = response.build_tag;
        node.skybianBuildVersion = response.skybian_build_version;
        node.connectedDmsgServers = response.connected_dmsg_servers || [];
        node.isSymmeticNat = response.overview.is_symmetic_nat;
        node.publicIp = response.overview.public_ip;
        node.autoconnectTransports = response.public_autoconnect;
        node.isPublic = response.is_public;
        node.rewardsAddress = response.reward_address;

        // Hypervisor relationships. `hypervisors` are the PKs the
        // visor was configured to report to (from skywire-config);
        // `connected_hypervisor` are the sessions the visor reports
        // as actively connected right now. Both are surfaced on the
        // Info tab so the operator can see which hypervisor(s) own
        // this visor and which ones are currently online.
        node.hypervisors = Array.isArray(response.overview.hypervisors)
          ? (response.overview.hypervisors as string[])
          : [];
        node.connectedHypervisors = Array.isArray(response.overview.connected_hypervisor)
          ? (response.overview.connected_hypervisor as string[])
          : [];

        // Geolocation data.
        if (response.overview.country_code) {
          node.countryCode = response.overview.country_code;
        }
        if (response.overview.region_name) {
          node.regionName = response.overview.region_name;
        }
        if (response.overview.city_name) {
          node.cityName = response.overview.city_name;
        }
        if (response.overview.latitude) {
          node.latitude = response.overview.latitude;
        }
        if (response.overview.longitude) {
          node.longitude = response.overview.longitude;
        }

        // Ip.
        if (response.overview.local_ip && (response.overview.local_ip as string).trim()) {
          node.ip = response.overview.local_ip;
        } else {
          node.ip = null;
        }

        // Label.
        const labelInfo = this.storageService.getLabelInfo(node.localPk);
        node.label = labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(node);

        // Health info.
        node.health = {
          servicesHealth: response.health.services_health,
          uptimeTrackerHealth: response.health.uptime_tracker_health,
          autoconnectHealth: response.health.autoconnect_health,
          transportabilityHealth: response.health.transportability_health,
        };

        // Transports.
        node.transports = [];
        if (response.overview.transports) {
          (response.overview.transports as any[]).forEach(transport => {
            node.transports.push({
              id: transport.id,
              localPk: transport.local_pk,
              remotePk: transport.remote_pk,
              type: transport.type,
              recv: transport.log.recv,
              sent: transport.log.sent,
              latencyMs: transport.latency_ms || 0,
            });
          });
        }

        // Persistent Transports.
        node.persistentTransports = [];
        if (response.persistent_transports) {
          (response.persistent_transports as any[]).forEach(persistentTransport => {
            node.persistentTransports.push({
              pk: persistentTransport.pk,
              type: persistentTransport.type,
            });
          });
        }

        // Routes.
        node.routes = [];
        if (response.routes) {
          (response.routes as any[]).forEach(route => {
            // Basic data.
            node.routes.push({
              key: route.key,
              rule: route.rule,
            });

            if (route.rule_summary) {
              // Rule summary.
              node.routes[node.routes.length - 1].ruleSummary = {
                keepAlive: route.rule_summary.keep_alive,
                ruleType: route.rule_summary.rule_type,
                keyRouteId: route.rule_summary.key_route_id,
              };

              // App fields, if any.
              if (route.rule_summary.app_fields && route.rule_summary.app_fields.route_descriptor) {
                node.routes[node.routes.length - 1].appFields = {
                  routeDescriptor: {
                    dstPk: route.rule_summary.app_fields.route_descriptor.dst_pk,
                    dstPort: route.rule_summary.app_fields.route_descriptor.dst_port,
                    srcPk: route.rule_summary.app_fields.route_descriptor.src_pk,
                    srcPort: route.rule_summary.app_fields.route_descriptor.src_port,
                  },
                };
              }

              // Forward fields, if any.
              if (route.rule_summary.forward_fields) {
                node.routes[node.routes.length - 1].forwardFields = {
                  nextRid: route.rule_summary.forward_fields.next_rid,
                  nextTid: route.rule_summary.forward_fields.next_tid,
                };

                if (route.rule_summary.forward_fields.route_descriptor) {
                  node.routes[node.routes.length - 1].forwardFields.routeDescriptor = {
                    dstPk: route.rule_summary.forward_fields.route_descriptor.dst_pk,
                    dstPort: route.rule_summary.forward_fields.route_descriptor.dst_port,
                    srcPk: route.rule_summary.forward_fields.route_descriptor.src_pk,
                    srcPort: route.rule_summary.forward_fields.route_descriptor.src_port,
                  };
                }
              }

              // Intermediary forward fields, if any.
              if (route.rule_summary.intermediary_forward_fields) {
                node.routes[node.routes.length - 1].intermediaryForwardFields = {
                  nextRid: route.rule_summary.intermediary_forward_fields.next_rid,
                  nextTid: route.rule_summary.intermediary_forward_fields.next_tid,
                };
              }
            }
          });
        }

        // Apps.
        node.apps = [];
        if (response.overview.apps) {
          (response.overview.apps as any[]).forEach(app => {
            node.apps.push({
              name: app.name,
              status: app.status,
              port: app.port,
              autostart: app.auto_start,
              detailedStatus: app.detailed_status,
              args: app.args,
            });
          });
        }

        let dmsgServerFound = false;
        if (response.dmsg_stats) {
          node.dmsgServerPk = response.dmsg_stats.server_public_key;
          node.roundTripPing = this.nsToMs(response.dmsg_stats.round_trip);

          dmsgServerFound = true;
        }

        if (!dmsgServerFound) {
          node.dmsgServerPk = '-';
          node.roundTripPing = '-1';
        }

        // Parse dmsg_servers with per-server latencies
        if (response.dmsg_servers && Array.isArray(response.dmsg_servers)) {
          node.dmsgServers = response.dmsg_servers.map((s: any) => {
return {
            pk: s.pk,
            latency: s.latency || 0
          }
});
        }

        // Check if is hypervisor (local visor)
        node.isHypervisor = response.is_hypervisor;

        return node;
      })
    );
  }

  /**
   * Sets the rewards address of the node.
   */
  setRewardsAddress(nodeKey: string, address: string) {
    const data = {
      reward_address: address,
    };

    return this.apiService.put(`visors/${nodeKey}/reward`, data);
  }

  /**
   * Gets the rewards address of the node.
   */
  getRewardsAddress(nodeKey: string) {
    return this.apiService.get(`visors/${nodeKey}/reward`);
  }

  /**
   * Gets the runtime logs of the node.
   */
  getRuntimeLogs(nodeKey: string) {
    return this.apiService.get(`visors/${nodeKey}/runtime-logs`);
  }

  /**
   * Diff-streaming variant of getRuntimeLogs. Pass the previous
   * response's `latest` as `since` to receive only newly-arrived
   * entries; pass 0 for the full buffer. Returns the visor's
   * RuntimeLogsDelta shape: { entries, latest, dropped }.
   */
  getRuntimeLogsSince(nodeKey: string, since: number) {
    return this.apiService.get(`visors/${nodeKey}/runtime-logs?since=${since}`);
  }

  /** Visor process Go-runtime stats (heap, GC, goroutines). */
  getRuntimeStats(nodeKey: string) {
    return this.apiService.get(`visors/${nodeKey}/runtime-stats`);
  }

  /** Host system + visor process resource snapshot (CPU%, mem, disk, net). */
  getHostStats(nodeKey: string) {
    return this.apiService.get(`visors/${nodeKey}/host-stats`);
  }

  /**
   * Aggregated SD/TPD/UT network view — the same combined table
   * `skywire cli sd` prints. Hypervisor-scope (no nodeKey arg);
   * cached on the visor for 5min by default. Pass refresh=true to
   * force the visor to re-aggregate immediately.
   */
  getNetworkView(refresh = false) {
    const q = refresh ? '?refresh=true' : '';
    return this.apiService.get(`network-view${q}`);
  }

  /**
   * Embedded mainnet reward rules — same content `skywire cli
   * reward rules` prints. Returned as raw markdown text.
   */
  getRewardRules() {
    return this.apiService.get(`reward-rules`, new RequestOptions({ responseType: ResponseTypes.Text }));
  }

  /**
   * Removes the rewards address of the node.
   */
  deleteRewardsAddress(nodeKey: string) {
    return this.apiService.delete(`visors/${nodeKey}/reward`);
  }

  /**
   * Gets the resolving proxy status for a visor.
   */
  getProxies(nodeKey: string): Observable<any> {
    return this.apiService.get(`visors/${nodeKey}/proxies`);
  }

  /**
   * Enables or disables a resolving proxy.
   */
  setProxyEnabled(nodeKey: string, kind: string, enable: boolean): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/proxies/set`, { kind, enable });
  }

  /**
   * Sets the upstream SOCKS5 address for a resolving proxy.
   */
  setProxyUpstream(nodeKey: string, kind: string, addr: string): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/proxies/upstream`, { kind, addr });
  }

  /**
   * Gets the list of skynet-forwarded ports.
   */
  getSkynetPorts(nodeKey: string): Observable<number[]> {
    return this.apiService.get(`visors/${nodeKey}/skynet-ports`);
  }

  /**
   * Registers a local port for skynet forwarding.
   */
  registerSkynetPort(nodeKey: string, port: number): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/skynet-ports/register`, { port });
  }

  /**
   * Deregisters a local port from skynet forwarding.
   */
  deregisterSkynetPort(nodeKey: string, port: number): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/skynet-ports/deregister`, { port });
  }

  /**
   * Gets all forwarded ports with rich metadata.
   */
  getForwardedPorts(nodeKey: string): Observable<any[]> {
    return this.apiService.get(`visors/${nodeKey}/forwarded-ports`);
  }

  /**
   * Registers a forwarded port with metadata (label, description, skynet/dmsg, whitelist).
   */
  registerForwardedPort(nodeKey: string, port: any): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/forwarded-ports/register`, port);
  }

  /**
   * Updates a forwarded port's metadata.
   */
  updateForwardedPort(nodeKey: string, port: any): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/forwarded-ports/update`, port);
  }

  /**
   * Gets active skynet forward connections (reverse proxies).
   */
  getSkynetForwards(nodeKey: string): Observable<any> {
    return this.apiService.get(`visors/${nodeKey}/skynet-forwards`);
  }

  /**
   * Creates a skynet/dmsg forward: maps remote_pk:remote_port (over the
   * named network) to localhost:local_port. Network is "skynet" (default;
   * empty string treated as skynet) or "dmsg".
   */
  skynetConnect(nodeKey: string, network: string, remotePK: string, remotePort: number, localPort: number): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/skynet-forwards/connect`, {
      network, remote_pk: remotePK, remote_port: remotePort, local_port: localPort
    });
  }

  /**
   * Disconnects a skynet forward by UUID.
   */
  skynetDisconnect(nodeKey: string, id: string): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/skynet-forwards/disconnect`, { id });
  }

  /**
   * Turns off a node.
   */
  shutdown(nodeKey: string): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/shutdown`);
  }

  /**
   * Checks if a node is currently being updated.
   */
  checkIfUpdating(nodeKey: string): Observable<any> {
    return this.apiService.get(`visors/${nodeKey}/update/ws/running`);
  }

  /**
   * Checks if there are updates available for a node.
   */
  checkUpdate(nodeKey: string): Observable<any> {
    let channel = 'stable';

    // Use the custom channel saved by the user, if any.
    const savedChannel = localStorage.getItem(UpdaterStorageKeys.Channel);
    channel = savedChannel ? savedChannel : channel;

    return this.apiService.get(`visors/${nodeKey}/update/available/${channel}`);
  }

  /**
   * Updates a node.
   */
  update(nodeKey: string): Observable<any> {
    const body = {
      channel: 'stable'
      // channel: 'testing' // for debugging updater
    };

    // Use any custom settings saved by the user.
    const useCustomSettings = localStorage.getItem(UpdaterStorageKeys.UseCustomSettings);
    if (useCustomSettings) {
      const channel = localStorage.getItem(UpdaterStorageKeys.Channel);
      if (channel) {
        body['channel'] = channel;
      }
      const version = localStorage.getItem(UpdaterStorageKeys.Version);
      if (version) {
        body['version'] = version;
      }
      const archiveURL = localStorage.getItem(UpdaterStorageKeys.ArchiveURL);
      if (archiveURL) {
        body['archive_url'] = archiveURL;
      }
      const checksumsURL = localStorage.getItem(UpdaterStorageKeys.ChecksumsURL);
      if (checksumsURL) {
        body['checksums_url'] = checksumsURL;
      }
    }

    return this.apiService.ws(`visors/${nodeKey}/update/ws`, body);
  }
}
