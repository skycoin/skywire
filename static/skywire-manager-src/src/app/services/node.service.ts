import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';
import { map } from 'rxjs/operators';
import BigNumber from 'bignumber.js';

import { StorageService } from './storage.service';
import { Node } from '../app.datatypes';
import { ApiService } from './api.service';

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
   * Gets the list of the nodes connected to the hypervisor.
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
          node.localPk = response.overview.local_pk;
          node.version = response.overview.build_info.version;
          node.autoconnectTransports = response.public_autoconnect;
          node.buildTag = response.build_tag ? response.build_tag : '';
          node.rewardsAddress = response.reward_address;

          // Ip.
          if (response.overview && response.overview.local_ip && (response.overview.local_ip as string).trim()) {
            node.ip = response.overview.local_ip;
          } else {
            node.ip = null;
          }

          // Label.
          const labelInfo = this.storageService.getLabelInfo(node.localPk);
          node.label = labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(node);

          // If the node is offline, there if no need for getting the rest of the data.
          if (!node.online) {
            node.dmsgServerPk = '';
            node.roundTripPing = '';
            nodes.push(node);

            return;
          }

          // Health data.
          node.health = {
            servicesHealth: response.health.services_health,
          };

          // DMSG info.
          node.dmsgServerPk = response.dmsg_stats.server_public_key;
          node.roundTripPing = this.nsToMs(response.dmsg_stats.round_trip);

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
        node.secondsOnline = Math.floor(Number.parseFloat(response.uptime));
        node.minHops = response.min_hops;
        node.buildTag = response.build_tag;
        node.skybianBuildVersion = response.skybian_build_version;
        node.isSymmeticNat = response.overview.is_symmetic_nat;
        node.publicIp = response.overview.public_ip;
        node.autoconnectTransports = response.public_autoconnect;
        node.rewardsAddress = response.reward_address;

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
   * Removes the rewards address of the node.
   */
  deleteRewardsAddress(nodeKey: string) {
    return this.apiService.delete(`visors/${nodeKey}/reward`);
  }

  /**
   * Restarts a node.
   */
  reboot(nodeKey: string): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/restart`);
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
