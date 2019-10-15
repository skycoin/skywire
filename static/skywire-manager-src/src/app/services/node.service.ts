import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';
import { Node, Transport, Route, Application } from '../app.datatypes';
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
    const savedNodes = new Map<string, NodeInfo>();
    this.storageService.getNodes().forEach(node => savedNodes.set(node.publicKey, node));

    return this.apiService.get('visors', { api2: true }).pipe(map((nodes: Node[]) => {
      nodes = nodes || [];

      const processedNodes = new Map<string, boolean>();
      let nodesAdded = false;
      nodes.forEach(node => {
        processedNodes.set(node.local_pk, true);
        if (savedNodes.has(node.local_pk)) {
          node.label = savedNodes.get(node.local_pk).label;
          node.online = true;
        } else {
          nodesAdded = true;

          const addressParts = node.tcp_addr.split(':');
          let defaultLabel = node.tcp_addr;
          if (addressParts && addressParts.length === 2) {
            defaultLabel = ':' + addressParts[1];
          }

          this.storageService.addNode({
            publicKey: node.local_pk,
            label: defaultLabel,
          });

          node.label = defaultLabel;
          node.online = true;
        }
      });

      if (nodesAdded) {
        this.storageService.getNodes().forEach(node => savedNodes.set(node.publicKey, node));
      }

      const offlineNodes: Node[] = [];
      savedNodes.forEach(node => {
        if (!processedNodes.has(node.publicKey)) {
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

    return this.apiService.get(`visors/${nodeKey}`, { api2: true }).pipe(
      flatMap((node: Node) => {
        currentNode = node;
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
}
