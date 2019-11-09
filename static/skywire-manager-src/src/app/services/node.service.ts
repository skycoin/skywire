import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';
import { Node, Transport, Route, Application, HealthInfo } from '../app.datatypes';
import { ApiService } from './api.service';
import { flatMap, map } from 'rxjs/operators';
import { StorageService, NodeInfo } from './storage.service';
import { TransportService } from './transport.service';
import { RouteService } from './route.service';

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

  getNodes(): Observable<Node[]> {
    return this.apiService.get('nodes', { api2: true }).pipe(map((nodes: Node[]) => {
      nodes = nodes || [];
      const obtainedNodes = new Map<string, boolean>();
      nodes.forEach(node => {
        node.port = this.getPort(node.tcp_addr);
        node.label = this.storageService.getNodeLabel(node.local_pk);
        node.online = true;

        obtainedNodes.set(node.local_pk, true);
      });

      const offlineNodes: Node[] = [];
      this.storageService.getNodes().forEach(node => {
        if (!obtainedNodes.has(node.publicKey)) {
          const newNode: Node = new Node();
          newNode.local_pk = node.publicKey;
          newNode.label = node.label;
          newNode.online = false;

          offlineNodes.push(newNode);
        }
      });

      nodes = nodes.concat(offlineNodes);
      nodes = nodes.sort((a, b) => a.local_pk.localeCompare(b.local_pk));

      return nodes;
    }));
  }

  getNode(nodeKey: string): Observable<Node> {
    let currentNode: Node;

    return this.apiService.get(`nodes/${nodeKey}`, { api2: true }).pipe(
      flatMap((node: Node) => {
        node.port = this.getPort(node.tcp_addr);
        node.label = this.storageService.getNodeLabel(node.local_pk);
        currentNode = node;

        return this.apiService.get(`nodes/${nodeKey}/health`, { api2: true });
      }),
      flatMap((health: HealthInfo) => {
        currentNode.health = health;

        return this.apiService.get(`nodes/${nodeKey}/uptime`, { api2: true });
      }),
      flatMap((secondsOnline: string) => {
        currentNode.seconds_online = Math.floor(Number.parseFloat(secondsOnline));

        return this.transportService.getTransports(nodeKey);
      }),
      flatMap((transports: Transport[]) => {
        currentNode.transports = transports;

        return this.routeService.getRoutes(nodeKey);
      }),
      map((routes: Route[]) => {
        currentNode.routes = routes;

        if (currentNode.apps) {
          const startedApps: Application[] = [];
          const stoppedApps: Application[] = [];
          currentNode.apps.forEach(app => {
            if (app.status === 1) {
              startedApps.push(app);
            } else {
              stoppedApps.push(app);
            }
          });

          startedApps.sort((a, b) => a.name.localeCompare(b.name));
          stoppedApps.sort((a, b) => a.name.localeCompare(b.name));
          currentNode.apps = startedApps.concat(stoppedApps);
        }

        return currentNode;
      })
    );
  }

  private getPort(tcpAddr: string): string {
    const addressParts = tcpAddr.split(':');
    let port = tcpAddr;

    if (addressParts && addressParts.length === 2) {
      port = addressParts[1];
    }

    return port;
  }
}
