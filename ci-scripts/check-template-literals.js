/**
 * Fails if a component template shows English text that does not go through
 * the translate pipe, so a translated UI does not fall back to English.
 *
 * It flags text between tags and the text of matTooltip, placeholder, title
 * and aria-label attributes. Material icon names, comments, interpolations and
 * control flow blocks are skipped, and only text with two or more letters in a
 * row counts.
 *
 * Literals that predate the check are listed in
 * ci-scripts/template-literals-allowlist.txt, one "file<TAB>text" per line.
 * The list only shrinks: translate a literal and remove its line, and run with
 * --write to regenerate it.
 *
 * usage: node ci-scripts/check-template-literals.js [--write] <src-dir>
 */
const fs = require('fs');
const path = require('path');

const args = process.argv.slice(2);
const write = args[0] === '--write';
const dir = write ? args[1] : args[0];
if (!dir) {
  console.error('usage: node ci-scripts/check-template-literals.js [--write] <src-dir>');
  process.exit(2);
}
const allowlistPath = path.join(__dirname, 'template-literals-allowlist.txt');

function templates(d) {
  return fs.readdirSync(d, { withFileTypes: true }).flatMap(e => {
    const p = path.join(d, e.name);
    if (e.isDirectory()) {
      // The wallet is copied in from skycoin and checked there.
      return e.name === 'skycoin-wallet' || e.name === 'node_modules' ? [] : templates(p);
    }

    return e.name.endsWith('.html') && p.includes(path.sep + 'app' + path.sep) ? [p] : [];
  });
}

const letters = /[A-Za-z]{2,}/;

// Removes the @if (...), @for (...) and similar block headers, whose
// conditions may hold parentheses and > signs, and @let declarations.
function stripControlFlow(s) {
  let out = '';
  const head = /@(else if|if|for|switch|case|defer|placeholder|loading)\s*\(/g;
  let last = 0;
  for (let m; (m = head.exec(s));) {
    out += s.slice(last, m.index);
    let i = head.lastIndex;
    for (let depth = 1; i < s.length && depth > 0; i++) {
      depth += s[i] === '(' ? 1 : s[i] === ')' ? -1 : 0;
    }
    last = i;
    head.lastIndex = i;
  }
  out += s.slice(last);

  return out.replace(/@let\b[^;]*;/g, ' ').replace(/@(else|empty|default|defer)\b/g, ' ');
}

function literals(html) {
  const found = [];
  let s = stripControlFlow(html
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\{\{[\s\S]*?\}\}/g, ' '))
    .replace(/<mat-icon\b[^>]*>[\s\S]*?<\/mat-icon>/g, '<mat-icon></mat-icon>')
    .replace(/<code\b[^>]*>[\s\S]*?<\/code>/g, '<code></code>');

  const attr = /\s(matTooltip|placeholder|title|aria-label)="([^"]*)"/g;
  for (let m; (m = attr.exec(s));) {
    if (letters.test(m[2])) {
      found.push(m[2].trim());
    }
  }
  // A quoted string in a bound attribute, unless it is a translation key.
  const bound = /\s\[(matTooltip|placeholder|title|attr\.aria-label)\]="([^"]*)"/g;
  for (let m; (m = bound.exec(s));) {
    const quoted = /'([^']*)'/g;
    for (let q; (q = quoted.exec(m[2]));) {
      const translated = /^\s*\|\s*translate/.test(m[2].slice(quoted.lastIndex));
      if (!translated && letters.test(q[1]) && !/^[a-z0-9-]+(\.[\w-]*)+$/.test(q[1])) {
        found.push(q[1].trim());
      }
    }
  }

  const text = />([^<>]+)</g;
  for (let m; (m = text.exec(s));) {
    const t = m[1]
      .replace(/[{}]/g, ' ')
      .replace(/&[a-z]+;/g, ' ')
      .replace(/\s+/g, ' ')
      .trim();
    if (letters.test(t)) {
      found.push(t);
    }
  }

  return found;
}

const root = path.resolve(dir);
const current = [];
for (const file of templates(root).sort()) {
  const rel = path.relative(root, file);
  for (const t of literals(fs.readFileSync(file, 'utf8'))) {
    current.push(rel + '\t' + t);
  }
}

if (write) {
  fs.writeFileSync(allowlistPath, current.join('\n') + (current.length ? '\n' : ''));
  console.log(`wrote ${current.length} literals to ${allowlistPath}`);
  process.exit(0);
}

const allowed = new Map();
for (const line of fs.readFileSync(allowlistPath, 'utf8').split('\n').filter(Boolean)) {
  allowed.set(line, (allowed.get(line) || 0) + 1);
}
const fresh = [];
for (const line of current) {
  const n = allowed.get(line) || 0;
  if (n > 0) {
    allowed.set(line, n - 1);
  } else {
    fresh.push(line);
  }
}
const stale = [...allowed].filter(([, n]) => n > 0).map(([line]) => line);

if (fresh.length) {
  console.error('Template text that is not translated. Add it to the i18n files and use the translate pipe:');
  fresh.forEach(l => console.error('  ' + l.replace('\t', ': ')));
}
if (stale.length) {
  console.error('These allowlisted literals are gone. Remove them from ' + path.relative(process.cwd(), allowlistPath) + ':');
  stale.forEach(l => console.error('  ' + l.replace('\t', ': ')));
}
if (fresh.length || stale.length) {
  process.exit(1);
}
console.log(`no new untranslated template text (${current.length} allowlisted).`);
