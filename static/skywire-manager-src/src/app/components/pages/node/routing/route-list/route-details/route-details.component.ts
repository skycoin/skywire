import { Component, OnInit, Inject, OnDestroy } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { Subscription, of } from 'rxjs';
import { delay, flatMap } from 'rxjs/operators';

import { RouteService } from '../../../../../../services/route.service';
import { NodeComponent } from '../../../node.component';
import { SnackbarService } from '../../../../../../services/snackbar.service';
import { AppConfig } from 'src/app/app.config';
import { processServiceError } from 'src/app/utils/errors';

// Objects representing the structure of the response returned by the hypervisor.

class RouteRule {
  key:  string;
  rule: string;
  rule_summary?: RuleSumary;
}

class RuleSumary {
  keep_alive: number;
  rule_type: number;
  key_route_id: number;
  app_fields?: AppRuleSumary;
  forward_fields?: ForwardRuleSumary;
}

class AppRuleSumary {
  route_descriptor: RouteDescriptor;
}

class RouteDescriptor {
  dst_pk: string;
  src_pk: string;
  dst_port: number;
  src_port: number;
}

class ForwardRuleSumary {
  next_rid: number;
  next_tid: string;
}

/**
 * Modal window for showing the details of a route.
 */
@Component({
  selector: 'app-route-details',
  templateUrl: './route-details.component.html',
  styleUrls: ['./route-details.component.scss']
})
export class RouteDetailsComponent implements OnInit, OnDestroy {
  routeRule: RouteRule;

  private shouldShowError = true;
  private dataSubscription: Subscription;

  /**
   * Map with the types of route rules that the hypervisor can return and are known by this app.
   */
  private ruleTypes = new Map<number, string>([
    [0, 'App'],
    [1, 'Forward'],
    [2, 'Intermediary forward']
  ]);

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, routeID: string): MatDialogRef<RouteDetailsComponent, any> {
    const config = new MatDialogConfig();
    config.data = routeID;
    config.autoFocus = false;
    config.width = AppConfig.largeModalWidth;

    return dialog.open(RouteDetailsComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: string,
    private routeService: RouteService,
    private dialogRef: MatDialogRef<RouteDetailsComponent>,
    private snackbarService: SnackbarService,
  ) { }

  ngOnInit() {
    this.loadData(0);
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
  }

  getRuleTypeName(type: number): string {
    if (this.ruleTypes.has(type)) {
      return this.ruleTypes.get(type);
    }

    return type.toString();
  }

  closePopup() {
    this.dialogRef.close();
  }

  private loadData(delayMilliseconds: number) {
    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }

    this.dataSubscription = of(1).pipe(
      // Wait the delay.
      delay(delayMilliseconds),
      // Load the data. The node pk is obtained from the currently openned node page.
      flatMap(() => this.routeService.get(NodeComponent.getCurrentNodeKey(), this.data))
    ).subscribe(
      (rule: RouteRule) => {
        this.snackbarService.closeCurrentIfTemporaryError();
        this.routeRule = rule;
      },
      err => {
        err = processServiceError(err);

        // Show an error msg if it has not be done before during the current attempt to obtain the data.
        if (this.shouldShowError) {
          this.snackbarService.showError('common.loading-error', null, true, err);
          this.shouldShowError = false;
        }

        // Retry after a small delay.
        this.loadData(AppConfig.connectionRetryDelay);
      },
    );
  }
}
