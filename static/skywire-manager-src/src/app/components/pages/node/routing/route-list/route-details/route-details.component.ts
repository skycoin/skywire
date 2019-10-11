import { Component, OnInit, Inject, OnDestroy } from '@angular/core';
import { NodeService } from '../../../../../../services/node.service';
import { FormBuilder } from '@angular/forms';
import { MatDialogRef, MatSnackBar, MAT_DIALOG_DATA } from '@angular/material';
import { TranslateService } from '@ngx-translate/core';
import { Subscription } from 'rxjs';
import { RouteService } from '../../../../../../services/route.service';

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

  private dataSubscription: Subscription;

  private ruleTypes = new Map<number, string>([
    [0, 'App'],
    [1, 'Forward']
  ]);

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: string,
    private nodeService: NodeService,
    private routeService: RouteService,
    private dialogRef: MatDialogRef<RouteDetailsComponent>,
  ) { }

  ngOnInit() {
    this.dataSubscription = this.routeService.get(this.nodeService.getCurrentNodeKey(), this.data).subscribe(
      (rule: RouteRule) => this.routeRule = rule,
      () => this.closePopup(),
    );
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
}
