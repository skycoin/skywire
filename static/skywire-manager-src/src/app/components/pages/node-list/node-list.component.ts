import { Component, OnDestroy, OnInit, ViewChild } from '@angular/core';
import { NodeService } from '../../../services/node.service';
import { Node } from '../../../app.datatypes';
import { Subscription } from 'rxjs';
import { MatDialog, MatTableDataSource } from '@angular/material';
import { Router } from '@angular/router';
import { ButtonComponent } from '../../layout/button/button.component';
import { TranslateService } from '@ngx-translate/core';
import { ErrorsnackbarService } from '../../../services/errorsnackbar.service';
import { AuthService } from '../../../services/auth.service';

@Component({
  selector: 'app-node-list',
  templateUrl: './node-list.component.html',
  styleUrls: ['./node-list.component.scss'],
})
export class NodeListComponent implements OnInit, OnDestroy {
  @ViewChild('refreshButton') refreshButton: ButtonComponent;
  dataSource = new MatTableDataSource<Node>();
  displayedColumns: string[] = ['enabled', 'index', 'label', 'key', 'actions'];

  private subscriptions: Subscription;

  constructor(
    private nodeService: NodeService,
    private router: Router,
    private errorSnackBar: ErrorsnackbarService,
    private dialog: MatDialog,
    private translate: TranslateService,
    private authService: AuthService,
  ) { }

  ngOnInit() {
    this.subscriptions = this.nodeService.nodes().subscribe(allNodes => {
      this.dataSource.data = allNodes.sort((a, b) => a.local_pk.localeCompare(b.local_pk));
    });

    this.refresh();
  }

  ngOnDestroy() {
    this.subscriptions.unsubscribe();
  }

  refresh() {
    // this.refreshButton.loading();
    this.subscriptions.add(
      this.nodeService.refreshNodes(
        this.onSuccess.bind(this),
        this.onError.bind(this),
      )
    );
  }

  settings() {
    this.router.navigate(['settings']);
  }

  logout() {
    this.authService.logout().subscribe(
      () => this.router.navigate(['login']),
      () => this.errorSnackBar.open(this.translate.instant('nodes.logout-error'))
    );
  }

  open(node: Node) {
    this.router.navigate(['nodes', node.local_pk]);
  }

  private onSuccess() {
    this.refreshButton.reset();
  }

  private onError(error: string) {
    console.log(error);
    this.translate.get('nodes.error-load', { error }).subscribe(str => {
      this.errorSnackBar.open(str);
    });

    this.refreshButton.error(error);
  }
}
