import { Component, OnDestroy, OnInit } from '@angular/core';
import { NodeService } from '../../../services/node.service';
import { Node } from '../../../app.datatypes';
import { ActivatedRoute, Router } from '@angular/router';
import { MatDialog } from '@angular/material';
import { Subscription } from 'rxjs/internal/Subscription';
import { TranslateService } from '@ngx-translate/core';
import { ErrorsnackbarService } from '../../../services/errorsnackbar.service';

@Component({
  selector: 'app-node',
  templateUrl: './node.component.html',
  styleUrls: ['./node.component.scss']
})
export class NodeComponent implements OnInit, OnDestroy {
  private static currentInstanceInternal: NodeComponent;

  showMenu = false;
  node: Node;

  private dataSubscription: Subscription;
  private refreshingSubscription: Subscription;

  public static refreshDisplayedData() {
    if (NodeComponent.currentInstanceInternal) {
      NodeComponent.currentInstanceInternal.startRefreshingData();
    }
  }

  constructor(
    private nodeService: NodeService,
    private route: ActivatedRoute,
    private router: Router,
    private dialog: MatDialog,
    private errorSnackBar: ErrorsnackbarService,
    private translate: TranslateService,
  ) {
    NodeComponent.currentInstanceInternal = this;
  }

  ngOnInit() {
    this.dataSubscription = this.nodeService.node().subscribe(
      (node: Node) => this.node = node,
      this.onError.bind(this),
    );

    this.startRefreshingData();
  }

  private startRefreshingData() {
    if (this.refreshingSubscription) {
      this.refreshingSubscription.unsubscribe();
    }

    const key: string = this.route.snapshot.params['key'];
    this.refreshingSubscription = this.nodeService.refreshNode(key, this.onError.bind(this));
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
    this.refreshingSubscription.unsubscribe();

    NodeComponent.currentInstanceInternal = undefined;
  }

  toggleMenu() {
    this.showMenu = !this.showMenu;
  }

  private onError() {
    this.translate.get('node.error-load').subscribe(str => {
      this.errorSnackBar.open(str);
      this.router.navigate(['nodes']);
    });
  }
}
