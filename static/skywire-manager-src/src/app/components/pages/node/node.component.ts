import { Component, OnDestroy, OnInit, NgZone } from '@angular/core';
import { NodeService } from '../../../services/node.service';
import { Node } from '../../../app.datatypes';
import { ActivatedRoute, Router } from '@angular/router';
import { MatDialog } from '@angular/material';
import { Subscription } from 'rxjs/internal/Subscription';
import { TranslateService } from '@ngx-translate/core';
import { ErrorsnackbarService } from '../../../services/errorsnackbar.service';
import { of, Observable, ReplaySubject, timer } from 'rxjs';
import { delay, flatMap, tap } from 'rxjs/operators';
import { StorageService } from '../../../services/storage.service';

@Component({
  selector: 'app-node',
  templateUrl: './node.component.html',
  styleUrls: ['./node.component.scss']
})
export class NodeComponent implements OnInit, OnDestroy {
  private static currentInstanceInternal: NodeComponent;
  private static currentNodeKey: string;
  private static nodeSubject: ReplaySubject<Node>;

  showMenu = false;
  node: Node;

  private dataSubscription: Subscription;
  private updateTimeSubscription: Subscription;

  secondsSinceLastUpdate = 0;
  private lastUpdate = Date.now();
  updating = false;
  errorsUpdating = false;

  public static refreshCurrentDisplayedData() {
    if (NodeComponent.currentInstanceInternal) {
      NodeComponent.currentInstanceInternal.refresh(0);
    }
  }

  public static getCurrentNodeKey(): string {
    return NodeComponent.currentNodeKey;
  }

  public static get currentNode(): Observable<Node> {
    return NodeComponent.nodeSubject.asObservable();
  }

  constructor(
    private nodeService: NodeService,
    private route: ActivatedRoute,
    private router: Router,
    private dialog: MatDialog,
    private errorSnackBar: ErrorsnackbarService,
    private translate: TranslateService,
    public storageService: StorageService,
    private ngZone: NgZone,
  ) {
    NodeComponent.nodeSubject = new ReplaySubject<Node>(1);
    NodeComponent.currentInstanceInternal = this;
  }

  ngOnInit() {
    NodeComponent.currentNodeKey = this.route.snapshot.params['key'];
    this.refresh(0);

    this.ngZone.runOutsideAngular(() => {
      this.updateTimeSubscription =
        timer(5000, 5000).subscribe(() => this.ngZone.run(() => {
          this.secondsSinceLastUpdate = Math.floor((Date.now() - this.lastUpdate) / 1000);
        }));
    });
  }

  private refresh(delayMilliseconds: number) {
    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }

    this.ngZone.runOutsideAngular(() => {
      this.dataSubscription = of(1).pipe(
        delay(delayMilliseconds),
        tap(() => this.ngZone.run(() => this.updating = true)),
        delay(120),
        flatMap(() => this.nodeService.getNode(NodeComponent.currentNodeKey))
      ).subscribe((node: Node) => {
        this.ngZone.run(() => {
          this.node = node;
          NodeComponent.nodeSubject.next(node);

          this.lastUpdate = Date.now();
          this.secondsSinceLastUpdate = 0;
          this.updating = false;
          this.errorsUpdating = false;

          this.refresh(this.storageService.getRefreshTime() * 1000);
        });
      }, () => {
        this.ngZone.run(() => {
          if (!this.errorsUpdating) {
            this.errorSnackBar.open(this.translate.instant('node.error-load'));
          }

          this.updating = false;
          this.errorsUpdating = true;

          if (!this.node) {
            this.refresh(3000);
          } else {
            this.refresh(this.storageService.getRefreshTime() * 1000);
          }
        });
      });
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
    this.updateTimeSubscription.unsubscribe();

    NodeComponent.currentInstanceInternal = undefined;
    NodeComponent.currentNodeKey = undefined;

    NodeComponent.nodeSubject.complete();
    NodeComponent.nodeSubject = undefined;
  }

  toggleMenu() {
    this.showMenu = !this.showMenu;
  }
}
