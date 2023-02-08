import { Injectable } from '@angular/core';
import { map } from 'rxjs/operators';
import { formatDate } from '@angular/common';

import { ApiService } from './api.service';

/**
 * Allows to work with the apps of a node.
 */
@Injectable({
  providedIn: 'root'
})
export class AppsService {
  constructor(
    private apiService: ApiService,
  ) { }

  /**
   * Starts or stops an app.
   */
  changeAppState(nodeKey: string, appName: string, startApp: boolean) {
    return this.apiService.put(`visors/${nodeKey}/apps/${encodeURIComponent(appName)}`,
      { status: startApp ? 1 : 0 }
    );
  }

  /**
   * Changes the autostart setting of an app.
   */
  changeAppAutostart(nodeKey: string, appName: string, autostart: boolean) {
    return this.changeAppSettings(nodeKey, appName, { autostart: autostart });
  }

  /**
   * Changes the autostart setting of an app.
   */
  changeAppSettings(nodeKey: string, appName: string, settings: object) {
    return this.apiService.put(`visors/${nodeKey}/apps/${encodeURIComponent(appName)}`, settings);
  }

  /**
   * Get the log messages of an app.
   * @param days Number of days to take into account for logs. The result will contain log until the current date,
   * starting from "currentDate - days". If you want to get the entire log history, use -1.
   */
  getLogMessages(nodeKey: string, appName: string, days: number) {
    const since = days !== -1 ? Date.now() - (days * 86400000) : 0;
    const sinceString = formatDate(since, 'yyyy-MM-ddTHH:mm:ssZZZZZ', 'en-US');

    return this.apiService.get(this.getLogMessagesUrl(nodeKey, appName) + `?since=${sinceString}`
    ).pipe(map(response => response.logs));
  }

  /**
   * Gets the partial URL for getting the logs of an app.
   */
  getLogMessagesUrl(nodeKey: string, appName: string) {
    return `visors/${nodeKey}/apps/${encodeURIComponent(appName)}/logs`;
  }
}
