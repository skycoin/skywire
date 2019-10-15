import { Component, HostBinding, Inject, OnInit, OnDestroy } from '@angular/core';
import { AppsService } from '../../../../../../services/apps.service';
import { LogMessage, Application } from '../../../../../../app.datatypes';
import { MAT_DIALOG_DATA } from '@angular/material';
import { Subscription } from 'rxjs';
import { NodeComponent } from '../../../node.component';

@Component({
  selector: 'app-log',
  templateUrl: './log.component.html',
  styleUrls: ['./log.component.scss'],
})
export class LogComponent implements OnInit, OnDestroy {

  @HostBinding('attr.class') hostClass = 'app-log-container';
  app: Application;
  logMessages: LogMessage[] = [];
  loading = false;

  subscription: Subscription;

  constructor(
    @Inject(MAT_DIALOG_DATA) data: Application,
    private appsService: AppsService,
  ) {
    this.app = data;
  }

  ngOnInit() {
    this.loading = true;
    this.subscription = this.appsService.getLogMessages(NodeComponent.getCurrentNodeKey(), this.app.name).subscribe(
      (log) => this.onLogsReceived(log),
      this.onLogsError.bind(this)
    );
  }

  ngOnDestroy(): void {
    this.subscription.unsubscribe();
  }

  private onLogsReceived(logs: string[] = []) {
    this.loading = false;
    logs.forEach(log => {
      const dateStart = log.startsWith('[') ? 0 : -1;
      const dateEnd = dateStart !== -1 ? log.indexOf(']') : -1;

      if (dateStart !== -1 && dateEnd !== -1) {
        this.logMessages.push({
          time: log.substr(dateStart, dateEnd + 1),
          msg: log.substr(dateEnd + 1),
        });
      } else {
        this.logMessages.push({
          time: '',
          msg: log,
        });
      }
    });
  }

  private onLogsError() {
    this.loading = false;
  }
}
