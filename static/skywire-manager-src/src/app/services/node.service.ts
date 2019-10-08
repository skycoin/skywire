import { Injectable } from '@angular/core';
import {
  Observable,
  ReplaySubject,
  Subject,
  timer,
  Unsubscribable
} from 'rxjs';
import { Node, Transport } from '../app.datatypes';
import { ApiService } from './api.service';
import { flatMap } from 'rxjs/operators';

@Injectable({
  providedIn: 'root'
})
export class NodeService {
  private allNodes = new Subject<Node[]>();
  private allNodesSubscription: Unsubscribable;

  private currentNodeKey: string;
  private currentNode = new ReplaySubject<Node>(1);

  constructor(
    private apiService: ApiService,
  ) {}

  nodes(): Observable<Node[]> {
    return this.allNodes.asObservable();
  }

  refreshNodes(successCallback: any = null, errorCallback: any = null): Unsubscribable {
    if (this.allNodesSubscription) {
      this.allNodesSubscription.unsubscribe();
    }

    this.allNodesSubscription = timer(0, 10000)
      .pipe(flatMap(() => this.getNodes()))
      .subscribe(
        (nodes: Node[]|null) => {
          nodes = nodes || [];

          this.allNodes.next(nodes);

          if (successCallback) {
            successCallback();
          }
        },
        errorCallback
      );

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
          return this.getTransports();
        })
      ).subscribe(
        (transports: Transport[]) => {
          currentNode.transports = transports;
          this.currentNode.next(currentNode);
        },
        errorCallback,
      );
  }

  getCurrentNodeKey(): string {
    return this.currentNodeKey;
  }

  private getNodes(): Observable<Node[]|null> {
    return this.apiService.get('nodes', { api2: true });
  }

  private getNode(): Observable<Node> {
    return this.apiService.get(`nodes/${this.currentNodeKey}`, { api2: true });
  }

  private getTransports() {
    return this.apiService.get(`nodes/${this.currentNodeKey}/transports`, { api2: true });
  }
}
