import { Injectable } from '@angular/core';
import {
  Observable,
  ReplaySubject,
  Subject,
  timer,
  Subscription
} from 'rxjs';
import { Node, Transport, Route, Application } from '../app.datatypes';
import { ApiService } from './api.service';
import { flatMap } from 'rxjs/operators';
import { StorageService, NodeInfo } from './storage.service';
import { TransportService } from './transport.service';
import { RouteService } from './route.service';

@Injectable({
  providedIn: 'root'
})
export class NodeService {
  private allNodes = new Subject<Node[]>();
  private allNodesSubscription: Subscription;

  private currentNodeKey: string;
  private currentNode = new ReplaySubject<Node>(1);

  constructor(
    private apiService: ApiService,
    private storageService: StorageService,
    private transportService: TransportService,
    private routeService: RouteService,
  ) {}

  nodes(): Observable<Node[]> {
    return this.allNodes.asObservable();
  }

  refreshNodes(successCallback: any = null, errorCallback: any = null): Subscription {
    if (this.allNodesSubscription) {
      this.allNodesSubscription.unsubscribe();
    }

    const savedNodes = new Map<string, NodeInfo>();
    this.storageService.getNodes().forEach(node => savedNodes.set(node.publicKey, node));

    this.allNodesSubscription = timer(0, 10000)
      .pipe(flatMap(() => this.getNodes()))
      .subscribe((nodes: Node[]|null) => {
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

        this.allNodes.next(nodes);

        if (successCallback) {
          successCallback();
        }
      }, errorCallback);

    return this.allNodesSubscription;
  }

  node(): Observable<Node> {
    return this.currentNode.asObservable();
  }

  refreshNode(nodeKey: string, errorCallback: any = null) {
    this.currentNodeKey = nodeKey;

    let currentNode: Node;

    return timer(0, 10000)
      .pipe(
        flatMap(() => this.getNode()),
        flatMap((node: Node) => {
          currentNode = node;
          return this.transportService.getTransports(this.currentNodeKey);
        }),
        flatMap((transports: Transport[]) => {
          currentNode.transports = transports;
          return this.routeService.getRoutes(this.currentNodeKey);
        })
      ).subscribe(
        (routes: Route[]) => {
          currentNode.routes = routes;

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

          this.currentNode.next(currentNode);
        },
        errorCallback,
      );
  }

  getCurrentNodeKey(): string {
    return this.currentNodeKey;
  }

  private getNodes(): Observable<Node[]|null> {
    return this.apiService.get('visors', { api2: true });
  }

  private getNode(): Observable<Node> {
    return this.apiService.get(`visors/${this.currentNodeKey}`, { api2: true });
  }
}
