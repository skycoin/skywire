import { Injectable } from '@angular/core';
import {
  Observable,
  ReplaySubject,
  Subject,
  timer,
  Subscription
} from 'rxjs';
import { Node, Transport, Route } from '../app.datatypes';
import { ApiService } from './api.service';
import { flatMap } from 'rxjs/operators';
import { StorageService, NodeInfo } from './storage.service';

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
      .pipe(
        flatMap(() => this.getNodes()))
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
            this.storageService.addNode({
              publicKey: node.local_pk,
              label: node.tcp_addr,
            });

            node.label = node.tcp_addr;
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

    return timer(0, 10000000)
      .pipe(
        flatMap(() => this.getNode()),
        flatMap((node: Node) => {
          currentNode = node;
          return this.getTransports();
        }),
        flatMap((transports: Transport[]) => {
          currentNode.transports = transports;
          return this.getRoutes();
        })
      ).subscribe(
        (routes: Route[]) => {
          currentNode.routes = routes;
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

  private getTransports() {
    return this.apiService.get(`visors/${this.currentNodeKey}/transports`, { api2: true });
  }

  private getRoutes() {
    return this.apiService.get(`visors/${this.currentNodeKey}/routes`, { api2: true });
  }
}
