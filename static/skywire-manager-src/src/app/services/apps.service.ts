import { Injectable } from '@angular/core';
import { ClientConnectionService } from './client-connection.service';
import { switchMap, map } from 'rxjs/operators';
import { ApiService } from './api.service';
import { formatDate } from '@angular/common';

@Injectable({
  providedIn: 'root'
})
export class AppsService {
  constructor(
    private clientConnection: ClientConnectionService,
    private apiService: ApiService,
  ) { }

  changeAppState(nodeKey: string, appName: string, startApp: boolean) {
    return this.apiService.put(`nodes/${nodeKey}/apps/${encodeURIComponent(appName)}`,
      { status: startApp ? 1 : 0 },
      { api2: true, type: 'json' }
    );
  }

  changeAppAutostart(nodeKey: string, appName: string, autostart: boolean) {
    return this.apiService.put(`nodes/${nodeKey}/apps/${encodeURIComponent(appName)}`,
      { autostart: autostart },
      { api2: true, type: 'json' }
    );
  }

  closeApp(key: string) {
    // return this.nodeService.nodeRequestWithRefresh('run/closeApp', {key}).pipe();
  }

  getLogMessages(nodeKey: string, appName: string, days: number) {
    const since = days !== -1 ? Date.now() - (days * 86400000) : 0;
    const sinceString = formatDate(since, 'yyyy-MM-ddTHH:mm:ssZZZZZ', 'en-US');

    return this.apiService.get(`nodes/${nodeKey}/apps/${encodeURIComponent(appName)}/logs?since=${sinceString}`,
      { api2: true }
    ).pipe(map(response => response.logs));
  }

  startSshServer(whitelistedKeys?: string[]) {
    // return this.nodeService.nodeRequestWithRefresh('run/sshs', {
    //   data: whitelistedKeys ? whitelistedKeys.join(',') : null,
    // });
  }

  startSshServerWithoutWhitelist() {
    // return this.nodeService.nodeRequestWithRefresh('run/sshs');
  }

  startSshClient(nodeKey: string, appKey: string) {
    // return this.clientConnection.save('sshc', <ClientConnection>{
    //   label: '',
    //   nodeKey,
    //   appKey,
    //   count: 1,
    // })
    //   .pipe(switchMap(() => this.nodeService.nodeRequestWithRefresh('run/sshc', {
    //     toNode: nodeKey,
    //     toApp: appKey,
    //   })));
  }

  startSocksc(nodeKey: string, appKey: string) {
    // return this.clientConnection.save('socksc', <ClientConnection>{
    //   label: '',
    //   nodeKey,
    //   appKey,
    //   count: 1,
    // })
    //   .pipe(switchMap(() => this.nodeService.nodeRequestWithRefresh('run/socksc', {
    //     toNode: nodeKey,
    //     toApp: appKey,
    //   })));
  }
}
