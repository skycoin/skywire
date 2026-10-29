import { NO_ERRORS_SCHEMA } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { TranslatePipe, provideTranslateService } from '@ngx-translate/core';
import { BehaviorSubject, of } from 'rxjs';

import { Node } from '../../../../../app.datatypes';
import { ApiService } from '../../../../../services/api.service';
import { AppsService } from '../../../../../services/apps.service';
import { LiveService } from '../../../../../services/live.service';
import { SnackbarService } from '../../../../../services/snackbar.service';
import { NodeComponent } from '../../node.component';
import { AppCatalogComponent, CatalogEntry, groupCatalog, nextInstanceName } from './app-catalog.component';

const PK = '0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c';

const catalog = (): CatalogEntry[] => [
  { name: 'custom-thing', kind: 'external', description: '', multi_instance: false,
    instances: [{ name: 'custom-thing', port: 0, auto_start: false, status: 'stopped' }] },
  { name: 'skychat', kind: 'service', description: 'Chat', multi_instance: false, instances: [] },
  { name: 'skysocks', kind: 'server', description: 'SOCKS5 server', multi_instance: false,
    instances: [{ name: 'skysocks', port: 3, auto_start: true, status: 'running' }] },
  { name: 'skysocks-client', kind: 'client', description: 'SOCKS5 client', multi_instance: true,
    instances: [{ name: 'skysocks-client', port: 13, auto_start: false, status: 'stopped' }] },
];

describe('app catalog grouping', () => {
  it('groups by kind in server, client, service, external order', () => {
    const groups = groupCatalog(catalog());
    expect(groups.map(g => g.kind)).toEqual(['server', 'client', 'service', 'external']);
    expect(groups[0].entries.map(e => e.name)).toEqual(['skysocks']);
  });

  it('drops empty groups and files unknown kinds as external', () => {
    const groups = groupCatalog([{ name: 'x', kind: 'odd', description: '', multi_instance: false, instances: [] }]);
    expect(groups.map(g => g.kind)).toEqual(['external']);
  });

  it('proposes the first free instance name', () => {
    const e = catalog()[3];
    expect(nextInstanceName(e)).toBe('skysocks-client-2');
    expect(nextInstanceName(e, ['skysocks-client-2'])).toBe('skysocks-client-3');
  });
});

describe('AppCatalogComponent', () => {
  let fixture: ComponentFixture<AppCatalogComponent>;
  let el: HTMLElement;
  let apps: jasmine.SpyObj<AppsService>;
  let api: jasmine.SpyObj<ApiService>;
  let dialog: jasmine.SpyObj<MatDialog>;
  let node$: BehaviorSubject<Node>;

  const card = (name: string) => el.querySelector(`[data-app="${name}"]`) as HTMLElement;
  const click = (root: HTMLElement, sel: string) => {
    (root.querySelector(sel) as HTMLElement).click();
    fixture.detectChanges();
  };

  beforeEach(() => {
    apps = jasmine.createSpyObj('AppsService', ['addApp', 'changeAppState', 'changeAppAutostart']);
    apps.addApp.and.returnValue(of({}));
    apps.changeAppState.and.returnValue(of({}));
    apps.changeAppAutostart.and.returnValue(of({}));
    api = jasmine.createSpyObj('ApiService', ['get']);
    api.get.and.callFake(() => of(catalog()));
    const live = jasmine.createSpyObj('LiveService', ['watch']);
    live.watch.and.callFake((_u: string, _ms: number, req: () => any) => of(req()));
    dialog = jasmine.createSpyObj('MatDialog', ['open']);
    node$ = new BehaviorSubject<Node>({ localPk: PK, apps: [
      { name: 'skysocks', status: 1, autostart: true },
      { name: 'skysocks-client', status: 0, autostart: false },
      { name: 'custom-thing', status: 0, autostart: false },
    ] } as unknown as Node);
    spyOnProperty(NodeComponent, 'currentNode', 'get').and.returnValue(node$.asObservable());
    spyOn(NodeComponent, 'refreshCurrentDisplayedData');

    TestBed.configureTestingModule({
      declarations: [AppCatalogComponent],
      imports: [TranslatePipe],
      providers: [
        provideTranslateService(),
        { provide: AppsService, useValue: apps },
        { provide: ApiService, useValue: api },
        { provide: LiveService, useValue: live },
        { provide: MatDialog, useValue: dialog },
        { provide: SnackbarService, useValue: jasmine.createSpyObj('SnackbarService', ['showDone', 'showError']) },
      ],
      schemas: [NO_ERRORS_SCHEMA],
    });
    fixture = TestBed.createComponent(AppCatalogComponent);
    el = fixture.nativeElement;
    fixture.detectChanges();
  });

  it('renders one card per entry under its kind heading', () => {
    expect(el.querySelectorAll('.cat-group').length).toBe(4);
    expect(el.querySelectorAll('.cat-card').length).toBe(4);
    expect(api.get).toHaveBeenCalledWith(`visors/${PK}/apps/catalog`);
  });

  it('shows not added with an Add button for an entry without instances', () => {
    const c = card('skychat');
    expect(c.querySelector('.cat-not-added')).toBeTruthy();
    expect(c.querySelector('.cat-instance')).toBeNull();
  });

  it('adds an app under its own name, refreshes and offers to start it', () => {
    const accepted = { subscribe: (fn: () => void) => fn() };
    dialog.open.and.returnValue({ componentInstance: { operationAccepted: accepted }, close: () => undefined } as any);
    api.get.calls.reset();
    click(card('skychat'), '.cat-add');
    expect(apps.addApp).toHaveBeenCalledWith(PK, 'skychat', 'skychat');
    expect(api.get).toHaveBeenCalled();
    expect(dialog.open).toHaveBeenCalled();
    expect(apps.changeAppState).toHaveBeenCalledWith(PK, 'skychat', true);
  });

  it('adds another instance of a multi-instance app under the proposed name', () => {
    spyOn(window, 'prompt').and.callFake((_m?: string, d?: string) => d || '');
    dialog.open.and.returnValue({ componentInstance: { operationAccepted: { subscribe: () => undefined } } } as any);
    expect(card('skysocks').querySelector('.cat-add-another')).toBeNull();
    click(card('skysocks-client'), '.cat-add-another');
    expect(apps.addApp).toHaveBeenCalledWith(PK, 'skysocks-client-2', 'skysocks-client');
  });

  it('stops a running instance and starts a stopped one', () => {
    click(card('skysocks'), '.cat-state');
    expect(apps.changeAppState).toHaveBeenCalledWith(PK, 'skysocks', false);
    click(card('skysocks-client'), '.cat-state');
    expect(apps.changeAppState).toHaveBeenCalledWith(PK, 'skysocks-client', true);
    expect(NodeComponent.refreshCurrentDisplayedData).toHaveBeenCalled();
  });

  it('flips autostart', () => {
    click(card('skysocks'), '.cat-autostart');
    expect(apps.changeAppAutostart).toHaveBeenCalledWith(PK, 'skysocks', false);
  });

  it('takes the status from the node apps and refetches when the app set changes', () => {
    const badge = () => card('skysocks-client').querySelector('.cat-status') as HTMLElement;
    expect(badge().classList).toContain('status-stopped');
    api.get.calls.reset();
    node$.next({ localPk: PK, apps: [
      { name: 'skysocks', status: 1, autostart: true },
      { name: 'skysocks-client', status: 1, autostart: false },
      { name: 'custom-thing', status: 0, autostart: false },
    ] } as unknown as Node);
    fixture.detectChanges();
    expect(badge().classList).toContain('status-running');
    expect(api.get).not.toHaveBeenCalled();

    node$.next({ localPk: PK, apps: [{ name: 'skychat', status: 0, autostart: false }] } as unknown as Node);
    expect(api.get).toHaveBeenCalled();
  });
});
