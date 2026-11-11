import { matchScore, paletteEntries, paletteTokens, PaletteContext, PaletteVisor, rankPalette } from './command-palette';
import { nodePaletteTabs, nodeTabsData } from './node-tabs';
import { homeTabsData } from './home-tabs';
import { isPaletteShortcut, visorPkFromUrl } from '../components/layout/command-palette/command-palette.component';

const pkA = '02' + 'a'.repeat(64);
const pkB = '03' + 'b'.repeat(64);
const pkC = '03' + 'c'.repeat(64);

const labels: { [k: string]: string } = {
  'node.tabs.routing': 'Routing', 'node.tabs.info': 'Info', 'node.tabs.logs': 'Logs',
  'node.tabs.transports': 'Transports', 'nodes.transports-title': 'Transports',
  'palette.actions.restart': 'Restart', 'palette.actions.copy-key': 'Copy public key',
};

function ctx(current?: PaletteVisor): PaletteContext {
  return {
    visors: [
      { pk: pkA, label: 'magnetosphere', ip: '203.0.113.7', place: 'Dallas, TX, US', online: true },
      { pk: pkB, label: 'mainframe', online: true },
      { pk: pkC, label: 'pi-relay', online: false },
    ],
    current: current,
    homeTabs: homeTabsData(),
    tabsFor: v => nodeTabsData(v.pk, !!v.wasm),
    translate: k => labels[k] || k,
  };
}

function rank(query: string, current?: PaletteVisor) {
  return rankPalette(paletteEntries(ctx(current), paletteTokens(query)), query);
}

describe('command palette matcher', () => {
  it('matches case-insensitively and ranks exact, prefix, word, inner, then in-order letters', () => {
    const exact = matchScore('routing', 'Routing');
    const prefix = matchScore('rout', 'Routing');
    const word = matchScore('rout', 'Mesh Routing');
    const inner = matchScore('out', 'Routing');
    const letters = matchScore('rtng', 'Routing');
    expect(exact).toBeGreaterThan(prefix);
    expect(prefix).toBeGreaterThan(word);
    expect(word).toBeGreaterThan(inner);
    expect(inner).toBeGreaterThan(letters);
    expect(letters).toBeGreaterThan(0);
    expect(matchScore('xyz', 'Routing')).toBe(0);
  });

  it('matches a public key only by substring', () => {
    expect(matchScore('aaaa', pkA)).toBeGreaterThan(0);
    expect(matchScore('a2a', pkA)).toBe(0);
  });
});

describe('command palette ranking', () => {
  it('finds a visor by label, full key, ip and place, and shows the full key', () => {
    for (const q of ['magneto', pkA, '203.0.113', 'dallas']) {
      const top = rank(q)[0];
      expect(top.group).toBe('visors');
      expect(top.items[0].title).toBe('magnetosphere');
      expect(top.items[0].detail).toContain(pkA);
      expect(top.items[0].link).toEqual(['/nodes', pkA, 'info']);
    }
  });

  it('puts the closest match first', () => {
    expect(rank('ma')[0].items.map(i => i.title)).toEqual(['mainframe', 'magnetosphere']);
    expect(rank('frame')[0].items.map(i => i.title)).toEqual(['mainframe']);
  });

  it('offers a visor tab for a visor name plus a tab', () => {
    const top = rank('magnetosphere routing')[0];
    expect(top.group).toBe('pages');
    expect(top.items[0].title).toBe('magnetosphere › Routing');
    expect(top.items[0].link).toEqual(['/nodes', pkA, 'routing']);
    expect(top.items[0].detail).toBe(pkA);
  });

  it('offers the open visor tabs and actions without naming the visor', () => {
    const current = ctx().visors[1];
    const tab = rank('routing', current)[0];
    expect(tab.items[0].link).toEqual(['/nodes', pkB, 'routing']);
    const action = rank('restart', current)[0];
    expect(action.group).toBe('actions');
    expect(action.items[0].action).toBe('restart');
    expect(action.items[0].visor!.pk).toBe(pkB);
  });

  it('offers actions on a named visor but not restart on an offline one', () => {
    const top = rank('copy magnetosphere')[0];
    expect(top.items[0].action).toBe('copy-key');
    expect(top.items[0].visor!.pk).toBe(pkA);
    const offline = rank('restart pi-relay');
    expect(offline.some(g => g.items.some(i => i.action === 'restart'))).toBeFalse();
  });

  it('leads with the open visor tabs and actions for an empty query', () => {
    expect(rank('').map(g => g.group)).toEqual(['visors', 'pages']);
    const groups = rank('', ctx().visors[0]);
    expect(groups.map(g => g.group)).toEqual(['pages', 'actions', 'visors']);
    expect(groups[0].items[0].link).toEqual(['/nodes', pkA, 'info']);
  });
});

describe('command palette shortcut', () => {
  const key = (init: KeyboardEventInit) => new KeyboardEvent('keydown', init);

  it('opens on Ctrl-K, and on Cmd-K on a Mac', () => {
    expect(isPaletteShortcut(key({ key: 'k', ctrlKey: true }), false, false)).toBeTrue();
    expect(isPaletteShortcut(key({ key: 'k', ctrlKey: true }), false, true)).toBeTrue();
    expect(isPaletteShortcut(key({ key: 'K', metaKey: true }), true, true)).toBeTrue();
  });

  it('leaves ordinary typing and the Mac text field Ctrl-K alone', () => {
    expect(isPaletteShortcut(key({ key: 'k' }), false, true)).toBeFalse();
    expect(isPaletteShortcut(key({ key: 'k', ctrlKey: true, shiftKey: true }), false, false)).toBeFalse();
    expect(isPaletteShortcut(key({ key: 'k', ctrlKey: true }), true, true)).toBeFalse();
    expect(isPaletteShortcut(key({ key: 'j', ctrlKey: true }), false, false)).toBeFalse();
  });

  it('reads the open visor from the url', () => {
    expect(visorPkFromUrl('/nodes/' + pkA + '/routing')).toBe(pkA);
    expect(visorPkFromUrl('/nodes/' + pkA)).toBe(pkA);
    expect(visorPkFromUrl('/nodes/list/1')).toBeUndefined();
  });
});

describe('command palette node pages', () => {
  it('reaches the app catalog without adding it to the tab row', () => {
    expect(nodeTabsData(pkA, false).some(t => t.label === 'node.tabs.app-catalog')).toBeFalse();
    const c = ctx({ pk: pkA, label: 'magnetosphere' });
    c.tabsFor = v => nodePaletteTabs(v.pk, !!v.wasm);
    c.translate = k => k === 'node.tabs.app-catalog' ? 'App catalog' : labels[k] || k;
    const top = rankPalette(paletteEntries(c, paletteTokens('catalog')), 'catalog')[0];
    expect(top.items[0].title).toBe('App catalog');
    expect(top.items[0].link).toEqual(['/nodes', pkA, 'apps', 'catalog']);
  });
});

describe('command palette theme', () => {
  const themed = (query: string) => rankPalette(paletteEntries({ ...ctx(), themeChoice: 'system' }, paletteTokens(query)), query);

  it('offers the three theme choices and marks the current one', () => {
    const actions = themed('theme').find(g => g.group === 'actions')!;
    expect(actions.items.map(i => i.theme).sort()).toEqual(['dark', 'light', 'system']);
    expect(actions.items.find(i => i.theme === 'system')!.detail).toBe('palette.theme.current');
    expect(themed('dark')[0].items[0].theme).toBe('dark');
  });

  it('keeps the theme out of the empty query', () => {
    expect(themed('').some(g => g.items.some(i => !!i.theme))).toBeFalse();
  });
});
