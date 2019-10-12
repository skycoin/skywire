import { Injectable } from '@angular/core';
import { NodeService } from './node.service';
import { ClientConnectionService } from './client-connection.service';
import {finalize, switchMap, map} from 'rxjs/operators';
import {Observable} from 'rxjs';
import { ApiService } from './api.service';

@Injectable({
  providedIn: 'root'
})
export class AppsService {
  constructor(
    private nodeService: NodeService,
    private clientConnection: ClientConnectionService,
    private apiService: ApiService,
  ) { }

  changeAppState(appName: string, startApp: boolean, autostart: boolean) {
    return this.apiService.put(`visors/${this.nodeService.getCurrentNodeKey()}/apps/${encodeURIComponent(appName)}`,
      { status: startApp ? 1 : 0, autostart: autostart },
      { api2: true, type: 'json' }
    );
  }

  closeApp(key: string) {
    // return this.nodeService.nodeRequestWithRefresh('run/closeApp', {key}).pipe();
  }

  getLogMessages(appName: string) {
    return this.apiService.get(`visors/${this.nodeService.getCurrentNodeKey()}/apps/${encodeURIComponent(appName)}/logs`,
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
