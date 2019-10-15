import { Injectable } from '@angular/core';
import { ClientConnectionService } from './client-connection.service';
import { switchMap, map } from 'rxjs/operators';
import { ApiService } from './api.service';

@Injectable({
  providedIn: 'root'
})
export class AppsService {
  constructor(
    private clientConnection: ClientConnectionService,
    private apiService: ApiService,
  ) { }

  changeAppState(nodeKey: string, appName: string, startApp: boolean, autostart: boolean) {
    return this.apiService.put(`visors/${nodeKey}/apps/${encodeURIComponent(appName)}`,
      { status: startApp ? 1 : 0, autostart: autostart },
      { api2: true, type: 'json' }
    );
  }

  closeApp(key: string) {
    // return this.nodeService.nodeRequestWithRefresh('run/closeApp', {key}).pipe();
  }

  getLogMessages(nodeKey: string, appName: string) {
    return this.apiService.get(`visors/${nodeKey}/apps/${encodeURIComponent(appName)}/logs`,
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
