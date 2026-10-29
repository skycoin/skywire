import { NO_ERRORS_SCHEMA } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormsModule, ReactiveFormsModule } from '@angular/forms';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef } from '@angular/material/dialog';
import { HttpClient } from '@angular/common/http';
import { TranslatePipe, provideTranslateService } from '@ngx-translate/core';
import { Subject, of } from 'rxjs';

import { Node, ProxyDiscoveryEntry, Transport } from '../../../../../app.datatypes';
import { AppsService } from '../../../../../services/apps.service';
import { ProxyDiscoveryService } from '../../../../../services/proxy-discovery.service';
import { SnackbarService } from '../../../../../services/snackbar.service';
import { NodeComponent } from '../../node.component';
import { AppFlag, serverTypeFor } from '../../apps/app-flags-form/app-flags';
import { AppFlagsFormComponent } from '../../apps/app-flags-form/app-flags-form.component';
import { AppSettingsComponent } from '../../apps/app-settings/app-settings.component';
import { SelectProxyServerComponent, latencyByPk, sortServers } from './select-proxy-server.component';

const pk = (c: string) => c.repeat(66);
const NODE = pk('0');
const A = pk('a');
const B = pk('b');
const C = pk('c');

const entry = (key: string, country: string, location: string): ProxyDiscoveryEntry => {
  return { pk: key, address: key, port: '', country: country, location: location };
};

const servers = () => [entry(A, 'DE', 'Germany'), entry(B, 'US', 'United States'), entry(C, 'DE', 'Germany')];
const transports = (): Transport[] => [
  { id: '1', localPk: NODE, remotePk: C, type: 'stcpr', recv: 0, sent: 0, latencyMs: 40 },
  { id: '2', localPk: NODE, remotePk: B, type: 'stcpr', recv: 0, sent: 0, latencyMs: 90 },
  { id: '3', localPk: NODE, remotePk: B, type: 'sudph', recv: 0, sent: 0, latencyMs: 25 },
  { id: '4', localPk: NODE, remotePk: A, type: 'dmsg', recv: 0, sent: 0, latencyMs: 0 },
];

describe('server picker helpers', () => {
  it('maps client apps to their server type', () => {
    expect(serverTypeFor('skysocks-client')).toBe('proxy');
    expect(serverTypeFor('vpn-client-2')).toBe('vpn');
    expect(serverTypeFor('my-exit', 'vpn-client')).toBe('vpn');
    expect(serverTypeFor('skynet-client')).toBe('');
    expect(serverTypeFor('skysocks')).toBe('');
  });

  it('sorts measured servers by their fastest transport, then by location', () => {
    const lat = latencyByPk(transports());
    expect(lat.get(B)).toBe(25);
    expect(lat.has(A)).toBeFalse();
    expect(sortServers(servers(), lat).map(s => s.pk)).toEqual([B, C, A]);
  });
});

describe('ProxyDiscoveryService', () => {
  it('keeps only visors offering the service, with proxies filed as skysocks', () => {
    const body = {
      [A]: { pk: A, services: ['skysocks', 'vpn'], country: 'DE' },
      [B]: { pk: B, services: ['visor'], country: 'US' },
      [C]: { pk: C, services: [], country: 'DE' },
    };
    const http = { get: () => of(body) } as unknown as HttpClient;
    const svc = new ProxyDiscoveryService(http);
    let proxies: ProxyDiscoveryEntry[] = [];
    let vpns: ProxyDiscoveryEntry[] = [];
    svc.getServices(true).subscribe(l => proxies = l);
    svc.getServices(false).subscribe(l => vpns = l);
    expect(proxies.map(s => s.pk)).toEqual([A]);
    expect(vpns.map(s => s.pk)).toEqual([A]);
  });
});

describe('SelectProxyServerComponent', () => {
  let fixture: ComponentFixture<SelectProxyServerComponent>;
  let comp: SelectProxyServerComponent;
  let ref: jasmine.SpyObj<MatDialogRef<SelectProxyServerComponent>>;
  let discovery: jasmine.SpyObj<ProxyDiscoveryService>;
  const rows = () => Array.from(fixture.nativeElement.querySelectorAll('.sps-row')) as HTMLElement[];

  beforeEach(() => {
    ref = jasmine.createSpyObj('MatDialogRef', ['close']);
    discovery = jasmine.createSpyObj('ProxyDiscoveryService', ['getServices']);
    discovery.getServices.and.returnValue(of(servers()));
    TestBed.configureTestingModule({
      declarations: [SelectProxyServerComponent],
      imports: [TranslatePipe, FormsModule],
      providers: [
        provideTranslateService(),
        { provide: MatDialogRef, useValue: ref },
        { provide: MAT_DIALOG_DATA, useValue: { clientName: 'vpn-client', serviceType: 'vpn', transports: transports() } },
        { provide: ProxyDiscoveryService, useValue: discovery },
        { provide: SnackbarService, useValue: jasmine.createSpyObj('SnackbarService', ['showError']) },
      ],
      schemas: [NO_ERRORS_SCHEMA],
    });
    fixture = TestBed.createComponent(SelectProxyServerComponent);
    comp = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('lists the requested type sorted by latency with a country filter', () => {
    expect(discovery.getServices).toHaveBeenCalledWith(false);
    expect(rows().map(r => r.getAttribute('data-pk'))).toEqual([B, C, A]);
    expect(rows()[0].querySelector('.sps-latency')!.textContent).toContain('25 ms');
    expect(rows()[2].querySelector('.sps-latency')).toBeNull();
    expect(comp.countries).toEqual([
      { code: 'DE', name: 'Germany', count: 2 },
      { code: 'US', name: 'United States', count: 1 },
    ]);
  });

  it('filters by country and by search text', () => {
    const pick = (code: string) => {
      const sel = fixture.nativeElement.querySelector('.sps-country select') as HTMLSelectElement;
      sel.value = Array.from(sel.options).find(o => o.value.endsWith(code))!.value;
      sel.dispatchEvent(new Event('change'));
      fixture.detectChanges();
    };
    const search = (text: string) => {
      const box = fixture.nativeElement.querySelector('.sps-filter input') as HTMLInputElement;
      box.value = text;
      box.dispatchEvent(new Event('input'));
      fixture.detectChanges();
    };
    fixture.detectChanges();

    pick('DE');
    expect(comp.country).toBe('DE');
    expect(rows().map(r => r.getAttribute('data-pk'))).toEqual([C, A]);

    search('aaaa');
    expect(rows().map(r => r.getAttribute('data-pk'))).toEqual([A]);

    pick('US');
    expect(rows().length).toBe(0);
  });

  it('returns the chosen key', () => {
    rows()[1].click();
    expect(ref.close).toHaveBeenCalledWith(C);
  });
});

describe('choosing a server from the flags form', () => {
  const srv: AppFlag = { name: 'srv', type: 'string', default: '', usage: 'server', check: 'pk', set: false };
  let dialog: jasmine.SpyObj<MatDialog>;
  let picked: Subject<string | undefined>;

  beforeEach(() => {
    picked = new Subject<string | undefined>();
    dialog = jasmine.createSpyObj('MatDialog', ['open']);
    dialog.open.and.returnValue({ afterClosed: () => picked.asObservable() } as any);
    const apps = jasmine.createSpyObj('AppsService', ['getAppFlags', 'setAppFullConfig']);
    apps.getAppFlags.and.returnValue(of({ app: 'vpn-client', source: 'registry', flags: [srv] }));
    apps.setAppFullConfig.and.returnValue(of({}));
    spyOnProperty(NodeComponent, 'currentNode', 'get')
      .and.returnValue(of({ localPk: NODE, transports: transports() } as Node));
    TestBed.configureTestingModule({
      declarations: [AppFlagsFormComponent, AppSettingsComponent],
      imports: [TranslatePipe, ReactiveFormsModule],
      providers: [
        provideTranslateService(),
        { provide: AppsService, useValue: apps },
        { provide: MatDialog, useValue: dialog },
        { provide: SnackbarService, useValue: jasmine.createSpyObj('SnackbarService', ['showDone']) },
      ],
      schemas: [NO_ERRORS_SCHEMA],
    });
  });

  it('writes the key into the server flag and the args', () => {
    const fixture = TestBed.createComponent(AppFlagsFormComponent);
    const comp = fixture.componentInstance;
    comp.appName = 'vpn-client';
    comp.args = '--killswitch';
    comp.serverType = 'vpn';
    const args: string[] = [];
    const chosen: string[] = [];
    comp.argsChange.subscribe((a: string) => args.push(a));
    comp.serverChosen.subscribe((p: string) => chosen.push(p));
    fixture.detectChanges();

    const button = fixture.nativeElement.querySelector('[data-flag="srv"] .flag-pick') as HTMLElement;
    button.click();
    expect(dialog.open.calls.mostRecent().args[1]!.data)
      .toEqual(jasmine.objectContaining({ clientName: 'vpn-client', serviceType: 'vpn' }));
    picked.next(B);
    fixture.detectChanges();
    expect(args[args.length - 1]).toBe(`--killswitch --srv=${B}`);
    expect(chosen).toEqual([B]);
    expect((fixture.nativeElement.querySelector('[data-flag="srv"] input') as HTMLInputElement).value).toBe(B);
  });

  it('shows no picker for an app without a server type', () => {
    const fixture = TestBed.createComponent(AppFlagsFormComponent);
    fixture.componentInstance.appName = 'skynet-client';
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('.flag-pick')).toBeNull();
  });

  const settings = (status: number) => {
    const fixture = TestBed.createComponent(AppSettingsComponent);
    const comp = fixture.componentInstance;
    comp.app = { name: 'vpn-client', args: [], status: status } as any;
    fixture.detectChanges();
    comp.onFlagArgs(`--srv=${B}`);

    return comp;
  };

  it('offers to save and restart a running client', () => {
    const accepted = new Subject<void>();
    const confirm = { componentInstance: { operationAccepted: accepted }, close: jasmine.createSpy('close') };
    dialog.open.and.returnValue(confirm as any);
    const apps = TestBed.inject(AppsService) as jasmine.SpyObj<AppsService>;
    const comp = settings(1);
    comp.onServerChosen();
    expect(dialog.open).toHaveBeenCalledTimes(1);
    expect(apps.setAppFullConfig).not.toHaveBeenCalled();
    accepted.next();
    expect(confirm.close).toHaveBeenCalled();
    expect(apps.setAppFullConfig).toHaveBeenCalledWith(NODE, 'vpn-client', jasmine.objectContaining({ args: `--srv=${B}` }));
  });

  it('leaves a stopped client for the operator to save', () => {
    const apps = TestBed.inject(AppsService) as jasmine.SpyObj<AppsService>;
    settings(0).onServerChosen();
    expect(dialog.open).not.toHaveBeenCalled();
    expect(apps.setAppFullConfig).not.toHaveBeenCalled();
  });

  it('opens the flags form when the catalog asks for the picker', () => {
    const fixture = TestBed.createComponent(AppSettingsComponent);
    const comp = fixture.componentInstance;
    comp.app = { name: 'skysocks-client', args: [], status: 0 } as any;
    comp.pickServer = 1;
    fixture.detectChanges();
    expect(comp.showFlagsForm).toBeTrue();
    expect(comp.serverType).toBe('proxy');
    expect(dialog.open).toHaveBeenCalledTimes(1);
  });
});
