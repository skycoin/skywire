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
  showMenu = false;
  node: Node;

  private subscription: Subscription;

  constructor(
    private nodeService: NodeService,
    private route: ActivatedRoute,
    private router: Router,
    private dialog: MatDialog,
    private errorSnackBar: ErrorsnackbarService,
    private translate: TranslateService,
  ) { }

  ngOnInit() {
    const key: string = this.route.snapshot.params['key'];

    this.subscription = this.nodeService.node().subscribe(
      (node: Node) => this.node = node,
      this.onError.bind(this),
    );

    this.subscription.add(
      this.nodeService.refreshNode(key, this.onError.bind(this))
    );
  }

  ngOnDestroy() {
    this.subscription.unsubscribe();
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
