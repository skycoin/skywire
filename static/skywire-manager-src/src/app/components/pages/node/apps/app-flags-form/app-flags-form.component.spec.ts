import { NO_ERRORS_SCHEMA } from '@angular/core';
import { ComponentFixture, TestBed, fakeAsync, flushMicrotasks } from '@angular/core/testing';
import { ReactiveFormsModule } from '@angular/forms';
import { TranslatePipe, provideTranslateService } from '@ngx-translate/core';
import { BehaviorSubject, of } from 'rxjs';

import { Node } from '../../../../../app.datatypes';
import { AppsService } from '../../../../../services/apps.service';
import { SnackbarService } from '../../../../../services/snackbar.service';
import { NodeComponent } from '../../node.component';
import { AppSettingsComponent } from '../app-settings/app-settings.component';
import { AppFlag, AppFlagsSchema, buildFlagArgs, parseFlagArgs, splitArgs } from './app-flags';
import { AppFlagsFormComponent } from './app-flags-form.component';

const PK = '0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c';

const flags = (): AppFlag[] => [
  { name: 'addr', type: 'string', default: ':1080', usage: 'listen address', check: 'hostport', set: false },
  { name: 'reconnect', type: 'bool', default: 'false', usage: 'reconnect', set: false },
  { name: 'tunnels', type: 'int', default: '1', usage: 'tunnels', set: false },
  { name: 'timeout', type: 'duration', default: '10s', usage: 'timeout', check: 'duration', set: false },
  { name: 'peer', type: 'stringList', default: '', usage: 'peers', set: false },
  { name: 'config', shorthand: 'c', type: 'string', default: '', usage: 'config path', set: false },
];

describe('app flag args', () => {
  it('splits quoted args like the visor', () => {
    expect(splitArgs(`app x --a "b c" --d='e f' ""`)).toEqual(['app', 'x', '--a', 'b c', '--d=e f', '']);
  });

  it('reads values in every flag form and keeps unknown tokens', () => {
    const p = parseFlagArgs('skycoin daemon --addr :9 --reconnect -c /etc/x --peer=a --peer b --other 1', flags());
    expect(p.positional).toEqual(['skycoin', 'daemon']);
    expect(p.other).toEqual(['--other', '1']);
    expect(p.state['addr']).toEqual({ value: ':9', set: true });
    expect(p.state['reconnect']).toEqual({ value: 'true', set: true });
    expect(p.state['config']).toEqual({ value: '/etc/x', set: true });
    expect(p.state['peer']).toEqual({ value: 'a,b', set: true });
    expect(p.state['tunnels']).toEqual({ value: '1', set: false });
  });

  it('rebuilds args with only the set flags', () => {
    const fl = flags();
    const state = parseFlagArgs('', fl).state;
    state['reconnect'] = { value: 'false', set: true };
    state['peer'] = { value: 'a, b', set: true };
    state['config'] = { value: 'my file', set: true };
    expect(buildFlagArgs('app sc --other 1 --addr :9', fl, state))
      .toBe('app sc --other 1 --reconnect=false --peer=a --peer=b "--config=my file"');
  });
});

describe('AppFlagsFormComponent', () => {
  let fixture: ComponentFixture<AppFlagsFormComponent>;
  let comp: AppFlagsFormComponent;
  let el: HTMLElement;
  let apps: jasmine.SpyObj<AppsService>;
  let emitted: string[];
  let valid: boolean[];

  const row = (name: string) => el.querySelector(`[data-flag="${name}"]`) as HTMLElement;
  const input = (name: string) => row(name).querySelector('input') as HTMLInputElement;
  const type = (name: string, value: string) => {
    const i = input(name);
    i.value = value;
    i.dispatchEvent(new Event('input'));
    fixture.detectChanges();
  };

  beforeEach(() => {
    apps = jasmine.createSpyObj('AppsService', ['getAppFlags']);
    apps.getAppFlags.and.returnValue(of({ app: 'sc', source: 'registry', flags: flags() } as AppFlagsSchema));
    spyOnProperty(NodeComponent, 'currentNode', 'get').and.returnValue(of({ localPk: PK } as Node));

    TestBed.configureTestingModule({
      declarations: [AppFlagsFormComponent],
      imports: [TranslatePipe],
      providers: [provideTranslateService(), { provide: AppsService, useValue: apps }],
      schemas: [NO_ERRORS_SCHEMA],
    });
    fixture = TestBed.createComponent(AppFlagsFormComponent);
    comp = fixture.componentInstance;
    comp.appName = 'sc';
    comp.args = 'app sc --tunnels=3';
    emitted = [];
    valid = [];
    comp.argsChange.subscribe((a: string) => emitted.push(a));
    comp.validChange.subscribe((v: boolean) => valid.push(v));
    el = fixture.nativeElement;
    fixture.detectChanges();
  });

  it('fetches the schema and renders an input per type', () => {
    expect(apps.getAppFlags).toHaveBeenCalledWith(PK, 'sc');
    expect(input('reconnect').type).toBe('checkbox');
    expect(input('tunnels').type).toBe('number');
    expect(input('timeout').type).toBe('text');
    expect(input('peer').type).toBe('text');
    expect(input('addr').type).toBe('text');
    expect(row('config').querySelector('.flag-name')!.textContent).toContain('-c,');
  });

  it('shows the configured value and the default', () => {
    expect(input('tunnels').value).toBe('3');
    expect(row('tunnels').classList).toContain('flag-set');
    expect(row('timeout').querySelector('.flag-default')!.textContent).toContain('10s');
    expect(row('timeout').querySelector('.flag-reset')).toBeNull();
  });

  it('writes an edit into the args', () => {
    const box = input('reconnect');
    box.checked = true;
    box.dispatchEvent(new Event('change'));
    expect(emitted[emitted.length - 1]).toBe('app sc --reconnect --tunnels=3');
  });

  it('resets a field to its default', () => {
    (row('tunnels').querySelector('.flag-reset') as HTMLElement).click();
    fixture.detectChanges();
    expect(emitted[emitted.length - 1]).toBe('app sc');
    expect(input('tunnels').value).toBe('1');
    expect(row('tunnels').querySelector('.flag-reset')).toBeNull();
  });

  it('marks a value the app would refuse', () => {
    type('timeout', 'soon');
    expect(row('timeout').querySelector('.flag-error')!.textContent).toContain('not a duration');
    expect(valid[valid.length - 1]).toBeFalse();
    type('timeout', '5m');
    expect(row('timeout').querySelector('.flag-error')).toBeNull();
    expect(valid[valid.length - 1]).toBeTrue();
  });

  it('follows edits made in the raw args field', fakeAsync(() => {
    fixture.componentRef.setInput('args', 'app sc --addr=:2000');
    fixture.detectChanges();
    flushMicrotasks();
    fixture.detectChanges();
    expect(input('addr').value).toBe(':2000');
    expect(input('tunnels').value).toBe('1');
  }));
});

describe('AppSettingsComponent with the flags form', () => {
  it('saves the args the flags form wrote through setAppFullConfig', () => {
    const apps = jasmine.createSpyObj('AppsService', ['setAppFullConfig']);
    apps.setAppFullConfig.and.returnValue(of({}));
    spyOnProperty(NodeComponent, 'currentNode', 'get').and.returnValue(new BehaviorSubject({ localPk: PK } as Node).asObservable());
    TestBed.configureTestingModule({
      declarations: [AppSettingsComponent],
      imports: [TranslatePipe, ReactiveFormsModule],
      providers: [
        provideTranslateService(),
        { provide: AppsService, useValue: apps },
        { provide: SnackbarService, useValue: jasmine.createSpyObj('SnackbarService', ['showDone']) },
      ],
      schemas: [NO_ERRORS_SCHEMA],
    });
    const fixture = TestBed.createComponent(AppSettingsComponent);
    const comp = fixture.componentInstance;
    comp.app = { name: 'sc', args: ['app', 'sc'], status: 0 } as any;
    fixture.detectChanges();

    comp.toggleFlagsForm();
    comp.onFlagArgs('app sc --tunnels=4');
    comp.save();
    expect(apps.setAppFullConfig).toHaveBeenCalledWith(PK, 'sc', jasmine.objectContaining({ args: 'app sc --tunnels=4' }));
  });
});
