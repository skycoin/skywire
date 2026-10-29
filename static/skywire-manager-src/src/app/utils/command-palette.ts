import { TabButtonData } from '../components/layout/top-bar/top-bar.component';
import { ThemeChoice, themeChoices } from '../services/theme.service';

/** A visor as the command palette sees it. */
export interface PaletteVisor {
  pk: string;
  label: string;
  ip?: string;
  place?: string;
  hostname?: string;
  online?: boolean;
  wasm?: boolean;
  version?: string;
  buildTag?: string;
}

export type PaletteGroup = 'visors' | 'pages' | 'actions';

export type PaletteAction = 'copy-key' | 'terminal' | 'logs' | 'restart' | 'update' | 'reward';

export interface PaletteItem {
  group: PaletteGroup;
  icon: string;
  title: string;
  // Second line, such as the visor's full public key.
  detail?: string;
  // Third line, such as the visor's IP and place.
  meta?: string;
  link?: any[];
  externalUrl?: string;
  action?: PaletteAction;
  visor?: PaletteVisor;
  theme?: ThemeChoice;
  score: number;
}

/**
 * An item and the texts it is found by. Each query word must match one of the
 * texts, and every group in `required` must be matched by at least one word.
 */
export interface PaletteEntry {
  item: Omit<PaletteItem, 'score'>;
  required: string[][];
  optional?: string[];
  // Shown with an empty query.
  onEmpty?: boolean;
}

export interface PaletteResultGroup {
  group: PaletteGroup;
  items: PaletteItem[];
}

export interface PaletteContext {
  visors: PaletteVisor[];
  // The visor whose page is open, if any.
  current?: PaletteVisor;
  homeTabs: TabButtonData[];
  tabsFor: (v: PaletteVisor) => TabButtonData[];
  translate: (key: string) => string;
  // The theme choice in effect, marked in its entry.
  themeChoice?: ThemeChoice;
}

const actionIcons: { [a in PaletteAction]: string } = {
  'copy-key': 'filter_none',
  'terminal': 'code',
  'logs': 'description',
  'restart': 'refresh',
  'update': 'get_app',
  'reward': 'monetization_on',
};

const onlineOnly = new Set<PaletteAction>(['restart', 'update', 'reward']);

/** Splits a query into lowercase words. */
export function paletteTokens(query: string): string[] {
  return (query || '').toLowerCase().split(/\s+/).filter(t => t.length > 0);
}

/**
 * Scores one query word against one text, 0 when it does not match. Substring
 * matches beat in-order letter matches, which are skipped for public keys.
 */
export function matchScore(token: string, text: string | undefined): number {
  if (!token || !text) {
    return 0;
  }
  const t = text.toLowerCase();
  if (t === token) {
    return 100;
  }
  const idx = t.indexOf(token);
  if (idx === 0) {
    return 80 + 10 * token.length / t.length;
  }
  if (idx > 0) {
    const wordStart = !/[a-z0-9]/.test(t[idx - 1]);

    return (wordStart ? 60 : 40) + 10 * token.length / t.length;
  }
  if (token.length < 2 || /^[0-9a-f]{20,}$/.test(t)) {
    return 0;
  }
  let pos = -1;
  let first = -1;
  for (const ch of token) {
    pos = t.indexOf(ch, pos + 1);
    if (pos < 0) {
      return 0;
    }
    if (first < 0) {
      first = pos;
    }
  }

  return 10 + 10 * token.length / (pos - first + 1);
}

/** Scores an entry against the query words, 0 when it does not match. */
export function scoreEntry(tokens: string[], entry: PaletteEntry): number {
  const groups = entry.required.concat(entry.optional ? [entry.optional] : []);
  const hit = groups.map(() => false);
  let total = 0;
  for (const token of tokens) {
    let best = 0;
    groups.forEach((texts, gi) => {
      texts.forEach(text => {
        const s = matchScore(token, text);
        if (s > 0) {
          hit[gi] = true;
        }
        best = Math.max(best, s);
      });
    });
    if (best === 0) {
      return 0;
    }
    total += best;
  }
  for (let i = 0; i < entry.required.length; i++) {
    if (!hit[i]) {
      return 0;
    }
  }

  return total;
}

function visorTexts(v: PaletteVisor): string[] {
  return [v.label, v.pk, v.ip, v.place, v.hostname].filter((s): s is string => !!s);
}

function visorName(v: PaletteVisor): string {
  return v.label || v.pk;
}

function tabEntries(v: PaletteVisor, ctx: PaletteContext, combined: boolean): PaletteEntry[] {
  return ctx.tabsFor(v).filter(t => !t.hidden && (t.linkParts || t.externalUrl)).map(t => {
    const label = ctx.translate(t.label);

    return {
      item: {
        group: 'pages',
        icon: t.icon,
        title: combined ? visorName(v) + ' › ' + label : label,
        detail: combined ? v.pk : visorName(v),
        link: t.linkParts || undefined,
        externalUrl: t.externalUrl,
      },
      required: combined ? [visorTexts(v), [label]] : [[label]],
      optional: combined ? undefined : visorTexts(v),
      onEmpty: !combined,
    } as PaletteEntry;
  });
}

function actionEntries(v: PaletteVisor, ctx: PaletteContext, combined: boolean): PaletteEntry[] {
  return (Object.keys(actionIcons) as PaletteAction[])
    .filter(a => !onlineOnly.has(a) || v.online !== false)
    .map(a => {
      const label = ctx.translate('palette.actions.' + a);

      return {
        item: {
          group: 'actions',
          icon: actionIcons[a],
          title: label + ': ' + visorName(v),
          detail: v.pk,
          action: a,
          visor: v,
        },
        required: combined ? [visorTexts(v), [label]] : [[label]],
        optional: combined ? undefined : visorTexts(v),
        onEmpty: !combined,
      } as PaletteEntry;
    });
}

function themeEntries(ctx: PaletteContext): PaletteEntry[] {
  return themeChoices.map(c => ({
    item: {
      group: 'actions',
      icon: c === 'light' ? 'wb_sunny' : c === 'dark' ? 'brightness_2' : 'brightness_auto',
      title: ctx.translate('palette.theme.' + c),
      detail: c === ctx.themeChoice ? ctx.translate('palette.theme.current') : undefined,
      theme: c,
    },
    required: [[ctx.translate('palette.theme.' + c), 'appearance', 'color', 'colour']],
  } as PaletteEntry));
}

/**
 * Builds every entry for a query. Entries pairing another visor with one of its
 * tabs or actions are only built for queries of two words or more.
 */
export function paletteEntries(ctx: PaletteContext, tokens: string[]): PaletteEntry[] {
  const entries: PaletteEntry[] = [];
  // With an empty query the open visor's tabs and actions come first.
  if (ctx.current) {
    entries.push(...tabEntries(ctx.current, ctx, false));
    entries.push(...actionEntries(ctx.current, ctx, false));
  }
  ctx.visors.forEach(v => entries.push({
    item: {
      group: 'visors',
      icon: v.wasm ? 'public' : 'devices',
      title: visorName(v),
      detail: v.pk,
      meta: [v.ip, v.place].filter(s => !!s).join(' · ') || undefined,
      link: ['/nodes', v.pk, 'info'],
    },
    required: [visorTexts(v)],
    onEmpty: true,
  }));
  ctx.homeTabs.filter(t => !t.hidden).forEach(t => {
    const label = ctx.translate(t.label);
    entries.push({
      item: { group: 'pages', icon: t.icon, title: label, link: t.linkParts || undefined, externalUrl: t.externalUrl },
      required: [[label]],
      onEmpty: true,
    });
  });
  if (ctx.themeChoice) {
    entries.push(...themeEntries(ctx));
  }
  if (tokens.length >= 2) {
    ctx.visors.filter(v => !ctx.current || v.pk !== ctx.current.pk).forEach(v => {
      entries.push(...tabEntries(v, ctx, true));
      entries.push(...actionEntries(v, ctx, true));
    });
  }

  return entries;
}

/**
 * Ranks the entries for a query: groups ordered by their best match, items by
 * score, at most `perGroup` items in each group.
 */
export function rankPalette(entries: PaletteEntry[], query: string, perGroup = 10): PaletteResultGroup[] {
  const tokens = paletteTokens(query);
  const byGroup = new Map<PaletteGroup, PaletteItem[]>();
  entries.forEach((e, i) => {
    let score: number;
    if (tokens.length === 0) {
      // An empty query keeps the order the entries were built in.
      score = e.onEmpty ? entries.length - i : 0;
    } else {
      score = scoreEntry(tokens, e);
    }
    if (score > 0) {
      const list = byGroup.get(e.item.group) || [];
      list.push({ ...e.item, score: score });
      byGroup.set(e.item.group, list);
    }
  });

  const order: PaletteGroup[] = ['visors', 'pages', 'actions'];
  const groups: PaletteResultGroup[] = [];
  byGroup.forEach((items, group) => {
    items.sort((a, b) => b.score - a.score);
    groups.push({ group: group, items: items.slice(0, perGroup) });
  });
  groups.sort((a, b) => b.items[0].score - a.items[0].score || order.indexOf(a.group) - order.indexOf(b.group));

  return groups;
}
