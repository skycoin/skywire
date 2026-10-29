import { SettingRow, settingError } from '../../node-settings/node-settings.component';

/** One flag of an app, as GET /visors/{pk}/apps/{app}/flags reports it (visorapi.AppFlag). */
export interface AppFlag {
  name: string;
  shorthand?: string;
  type: 'bool' | 'string' | 'int' | 'duration' | 'stringList';
  default: string;
  usage: string;
  check?: string;
  value?: string;
  set: boolean;
}

export interface AppFlagsSchema {
  app: string;
  source: string;
  flags: AppFlag[];
}

/** The form's state for one flag. Unset flags stay out of the args. */
export interface FlagState {
  value: string;
  set: boolean;
}

/** Splits an args string like visorconfig.SplitArgs: whitespace, "double" and 'single' quotes. */
export function splitArgs(s: string): string[] {
  const out: string[] = [];
  let cur = '';
  let inDouble = false;
  let inSingle = false;
  let inToken = false;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (inDouble) {
      if (c === '\\' && i + 1 < s.length) {
        cur += s[++i];
      } else if (c === '"') {
        inDouble = false;
      } else {
        cur += c;
      }
    } else if (inSingle) {
      if (c === '\'') {
        inSingle = false;
      } else {
        cur += c;
      }
    } else if (c === ' ' || c === '\t' || c === '\n') {
      if (inToken) {
        out.push(cur);
        cur = '';
        inToken = false;
      }
    } else {
      if (c === '"') {
        inDouble = true;
      } else if (c === '\'') {
        inSingle = true;
      } else {
        cur += c;
      }
      inToken = true;
    }
  }
  if (inToken) {
    out.push(cur);
  }

  return out;
}

/** Joins tokens like visorconfig.joinArgs, quoting where needed. */
export function joinArgs(tokens: string[]): string {
  return tokens.map(t => {
    if (t === '') {
      return '""';
    }

    return /[\s"'\\]/.test(t) ? `"${t.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"` : t;
  }).join(' ');
}

interface ParsedArgs {
  // Leading subcommand words such as "skycoin daemon".
  positional: string[];
  // Tokens for flags the schema does not know, kept as they were.
  other: string[];
  state: { [name: string]: FlagState };
}

function lookup(flags: AppFlag[], key: string): AppFlag | undefined {
  return flags.find(f => f.name === key || (!!f.shorthand && f.shorthand === key));
}

/** Reads the value each flag gets from an args string. */
export function parseFlagArgs(args: string, flags: AppFlag[]): ParsedArgs {
  const tokens = splitArgs(args);
  const parsed: ParsedArgs = { positional: [], other: [], state: {} };
  flags.forEach(f => parsed.state[f.name] = { value: f.default, set: false });
  let i = 0;
  while (i < tokens.length && !tokens[i].startsWith('-')) {
    parsed.positional.push(tokens[i++]);
  }
  for (; i < tokens.length; i++) {
    const t = tokens[i];
    const eq = t.indexOf('=');
    const key = t.replace(/^-+/, '').split('=')[0];
    const f = t.startsWith('-') ? lookup(flags, key) : undefined;
    if (!f) {
      parsed.other.push(t);
      continue;
    }
    let value: string;
    if (eq >= 0) {
      value = t.slice(eq + 1);
    } else if (f.type === 'bool') {
      value = 'true';
    } else {
      value = i + 1 < tokens.length ? tokens[++i] : '';
    }
    const st = parsed.state[f.name];
    st.value = f.type === 'stringList' && st.set ? `${st.value},${value}` : value;
    st.set = true;
  }

  return parsed;
}

/** Rebuilds the args string with the form's flag values, keeping the rest as it was. */
export function buildFlagArgs(args: string, flags: AppFlag[], state: { [name: string]: FlagState }): string {
  const parsed = parseFlagArgs(args, flags);
  const out = [...parsed.positional, ...parsed.other];
  for (const f of flags) {
    const st = state[f.name];
    if (!st || !st.set) {
      continue;
    }
    if (f.type === 'stringList') {
      const items = st.value.split(',').map(s => s.trim()).filter(s => s);
      items.forEach(item => out.push(`--${f.name}=${item}`));
      if (items.length === 0) {
        out.push(`--${f.name}=`);
      }
    } else if (f.type === 'bool') {
      out.push(st.value === 'true' ? `--${f.name}` : `--${f.name}=false`);
    } else {
      out.push(`--${f.name}=${st.value}`);
    }
  }

  return joinArgs(out);
}

/** What is wrong with a flag value, or '' when the app would accept it. */
export function flagError(f: AppFlag, value: string): string {
  if (f.type === 'bool') {
    return '';
  }
  const check = f.type === 'duration' ? 'duration' : (f.check || '');
  const row = { format: f.type === 'int' ? 'int' : 'string', check: check, secret: false } as SettingRow;

  return settingError(row, value);
}
