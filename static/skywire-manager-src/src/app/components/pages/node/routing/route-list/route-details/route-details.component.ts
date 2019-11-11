import { Component, OnInit, Inject, OnDestroy } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { Subscription, of } from 'rxjs';
import { RouteService } from '../../../../../../services/route.service';
import { NodeComponent } from '../../../node.component';
import { delay, flatMap } from 'rxjs/operators';
import { SnackbarService } from '../../../../../../services/snackbar.service';

class RouteRule {
  key:  string;
  rule: string;
  rule_summary?: RuleSumary;
}
class RuleSumary {
  keep_alive: number;
  rule_type: number;
  request_route_id: number;
  app_fields?: AppRuleSumary;
  forward_fields?: ForwardRuleSumary;
}

class AppRuleSumary {
  resp_rid: number;
  remote_pk: string;
  remote_port: number;
  local_port: number;
}

class ForwardRuleSumary {
  next_rid: number;
  next_tid: string;
}

@Component({
  selector: 'app-route-details',
  templateUrl: './route-details.component.html',
  styleUrls: ['./route-details.component.scss']
})
export class RouteDetailsComponent implements OnInit, OnDestroy {
  routeRule: RouteRule;

  private shouldShowError = true;
  private dataSubscription: Subscription;

  private ruleTypes = new Map<number, string>([
    [0, 'App'],
    [1, 'Forward']
  ]);

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
    this.snackbarService.closeCurrentIfTemporalError();
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
      delay(delayMilliseconds),
      flatMap(() => this.routeService.get(NodeComponent.getCurrentNodeKey(), this.data))
    ).subscribe(
      (rule: RouteRule) => {
        this.snackbarService.closeCurrentIfTemporalError();
        this.routeRule = rule;
      },
      () => {
        if (this.shouldShowError) {
          this.snackbarService.showError('common.loading-error', null, true);
          this.shouldShowError = false;
        }

        this.loadData(3000);
      },
    );
  }
}
