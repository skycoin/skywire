import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';
import { flatMap, map, mergeMap } from 'rxjs/operators';
import BigNumber from 'bignumber.js';

import { StorageService } from './storage.service';
import { Node, Transport, Route, HealthInfo } from '../app.datatypes';
import { ApiService } from './api.service';
import { TransportService } from './transport.service';
import { RouteService } from './route.service';

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
    private transportService: TransportService,
    private routeService: RouteService,
  ) {}

  /**
   * Get the list of the nodes connected to the hypervisor.
   */
  getNodes(): Observable<Node[]> {
    let nodes: Node[];

    return this.apiService.get('visors').pipe(mergeMap((result: Node[]) => {
      // Save the visor list.
      nodes = result || [];

      // Get the dmsg info.
      return this.apiService.get('dmsg');
    }), map((dmsgInfo: any[]) => {
      // Create a map to associate the dmsg info with the visors.
      const dmsgInfoMap = new Map<string, any>();
      dmsgInfo.forEach(info => dmsgInfoMap.set(info.public_key, info));

      // Process the node data and create a helper map.
      const obtainedNodes = new Map<string, Node>();
      nodes.forEach(node => {
        if (dmsgInfoMap.has(node.local_pk)) {
          node.dmsgServerPk = dmsgInfoMap.get(node.local_pk).server_public_key;
          node.roundTripPing = this.nsToMs(dmsgInfoMap.get(node.local_pk).round_trip);
        } else {
          node.dmsgServerPk = '-';
          node.roundTripPing = '-1';
        }

        node.ip = this.getAddressPart(node.tcp_addr, 0);
        node.port = this.getAddressPart(node.tcp_addr, 1);
        node.label = this.storageService.getNodeLabel(node.local_pk);

        obtainedNodes.set(node.local_pk, node);
      });

      const missingSavedNodes: Node[] = [];
      this.storageService.getNodes().forEach(node => {
        // If the backend did not return a saved node, add it to the response as an offline node.
        if (!obtainedNodes.has(node.publicKey) && !node.deleted) {
          const newNode: Node = new Node();
          newNode.local_pk = node.publicKey;
          newNode.label = node.label;
          newNode.online = false;

          missingSavedNodes.push(newNode);
        }

        // If the backend returned a node, informed that it is online and the saved data indicates that
        // the user deleted it from the node list in the past, remove it from the response.
        if (obtainedNodes.has(node.publicKey) && !obtainedNodes.get(node.publicKey).online && node.deleted) {
          obtainedNodes.delete(node.publicKey);
        }

        // If the user deleted an ofline node from the node list but now the backend says that it is online,
        // it will be shown in the node list again, so the "deleted" flag is removed in this code segment.
        if (obtainedNodes.has(node.publicKey) && obtainedNodes.get(node.publicKey).online && node.deleted) {
          this.storageService.changeNodeState(node.publicKey, false);
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
  getNode(nodeKey: string): Observable<Node> {
    let currentNode: Node;

    // Get the basic node data.
    return this.apiService.get(`visors/${nodeKey}`).pipe(
      flatMap((node: Node) => {
        node.ip = this.getAddressPart(node.tcp_addr, 0);
        node.port = this.getAddressPart(node.tcp_addr, 1);
        node.label = this.storageService.getNodeLabel(node.local_pk);
        currentNode = node;

        // Needed for a change made to the names on the backend.
        if (node.apps) {
          node.apps.forEach(app => {
            app.name = (app as any).name ? (app as any).name : (app as any).app;
            app.autostart = (app as any).auto_start;
          });
        }

        // Get the dmsg info.
        return this.apiService.get('dmsg');
      }),
      flatMap((dmsgInfo: any[]) => {
        for (let i = 0; i < dmsgInfo.length; i++) {
          if (dmsgInfo[i].public_key === currentNode.local_pk) {
            currentNode.dmsgServerPk = dmsgInfo[i].server_public_key;
            currentNode.roundTripPing = this.nsToMs(dmsgInfo[i].round_trip);

            // Get the health info.
            return this.apiService.get(`visors/${nodeKey}/health`);
          }
        }

        currentNode.dmsgServerPk = '-';
        currentNode.roundTripPing = '-1';

        // Get the health info.
        return this.apiService.get(`visors/${nodeKey}/health`);
      }),
      flatMap((health: HealthInfo) => {
        currentNode.health = health;

        // Get the node uptime.
        return this.apiService.get(`visors/${nodeKey}/uptime`);
      }),
      flatMap((secondsOnline: string) => {
        currentNode.seconds_online = Math.floor(Number.parseFloat(secondsOnline));

        // Get the complete transports info.
        return this.transportService.getTransports(nodeKey);
      }),
      flatMap((transports: Transport[]) => {
        currentNode.transports = transports;

        // Get the complete routes info.
        return this.routeService.getRoutes(nodeKey);
      }),
      map((routes: Route[]) => {
        currentNode.routes = routes;

        return currentNode;
      })
    );
  }

  /**
   * Gets a part of the node address: the ip or the port.
   * @param tcpAddr Complete address.
   * @param part 0 for the ip or 1 for the port.
   */
  private getAddressPart(tcpAddr: string, part: number): string {
    const addressParts = tcpAddr.split(':');
    let port = tcpAddr;

    if (addressParts && addressParts.length === 2) {
      port = addressParts[part];
    }

    return port;
  }

  /**
   * Restarts a node.
   */
  reboot(nodeKey: string): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/restart`);
  }

  /**
   * Checks if there are updates available for a node.
   */
  checkUpdate(nodeKey: string): Observable<any> {
    return this.apiService.get(`visors/${nodeKey}/update/available`);
  }

  /**
   * Updates a node.
   */
  update(nodeKey: string): Observable<any> {
    const body = {
      channel: 'testing'
    };

    return this.apiService.ws(`visors/${nodeKey}/update/ws`, body);
  }
}
