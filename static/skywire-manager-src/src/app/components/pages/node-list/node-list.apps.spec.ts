import { Node } from '../../../app.datatypes';
import { NodeListComponent, appChips, fleetAppNames, runsApp } from './node-list.component';

function visor(pk: string, apps: [string, number][]): Node {
  return { localPk: pk, label: pk, apps: apps.map(([name, status]) => {
    return { name: name, status: status };
  }) } as unknown as Node;
}

// The "running app" filter and the apps column read the apps the visor summaries carry.
describe('node list apps', () => {
  const a = visor('a', [['skysocks', 1], ['vpn-client', 0]]);
  const b = visor('b', [['vpn-server', 3], ['skysocks', 2]]);
  const c = visor('c', []);

  it('counts running and starting apps as running', () => {
    expect(runsApp(a, 'skysocks')).toBeTrue();
    expect(runsApp(a, 'vpn-client')).toBeFalse();
    expect(runsApp(b, 'vpn-server')).toBeTrue();
    expect(runsApp(b, 'skysocks')).toBeFalse();
    expect(runsApp(c, 'skysocks')).toBeFalse();
  });

  it('offers every configured app name once, sorted', () => {
    expect(fleetAppNames([a, b, c])).toEqual(['skysocks', 'vpn-client', 'vpn-server']);
  });

  it('lists running chips before stopped ones', () => {
    expect(appChips(b)).toEqual([{ name: 'vpn-server', running: true }, { name: 'skysocks', running: false }]);
    expect(appChips(c)).toEqual([]);
  });

  it('filters the list to visors running the chosen app', () => {
    const matches = (NodeListComponent.prototype as any).matchesQuickFilter as (n: Node) => boolean;
    const list = { quickChip: 'all', quickSearch: '', appFilter: 'skysocks' };
    expect([a, b, c].filter(n => matches.call(list, n)).map(n => n.localPk)).toEqual(['a']);
    list.appFilter = '';
    expect([a, b, c].filter(n => matches.call(list, n)).length).toBe(3);
  });
});
