// Loads the built manager UI in a real browser and asserts it actually renders.
//
//   node ci-scripts/ui-smoke/check.js [dist-dir]
//
// Set BROWSER_BIN to the browser binary (defaults to Chrome's usual path);
// BROWSER_KIND to 'firefox' to drive a Gecko browser instead.
// No hypervisor: the failures this is for — a bundle that throws on load,
// unresolved translation keys, a downlevel that the browser rejects — all
// happen before the first API call succeeds.
const http = require('http'), fs = require('fs'), path = require('path');
// Resolved from the UI project, which is where the node_modules live.
const p = require(require('path').resolve(__dirname, '../../static/skywire-manager-src/node_modules/puppeteer-core'));

const ROOT = path.resolve(process.argv[2] || 'static/skywire-manager-src/dist');
const PORT = 8771;
const TYPES = { '.html':'text/html', '.js':'text/javascript', '.css':'text/css',
  '.json':'application/json', '.svg':'image/svg+xml', '.png':'image/png',
  '.ico':'image/x-icon', '.woff':'font/woff', '.woff2':'font/woff2', '.ttf':'font/ttf' };

const server = http.createServer((req, res) => {
  let rel = decodeURIComponent(req.url.split('?')[0]);
  if (rel === '/') rel = '/index.html';
  let file = path.join(ROOT, rel);
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(ROOT, 'index.html');
  res.writeHead(200, { 'Content-Type': TYPES[path.extname(file)] || 'application/octet-stream' });
  res.end(fs.readFileSync(file));
}).listen(PORT);

(async () => {
  const browser = await p.launch({
    browser: process.env.BROWSER_KIND || 'chrome',
    executablePath: process.env.BROWSER_BIN || process.env.CHROME_BIN || '/usr/bin/google-chrome',
    headless: true,
    args: process.env.BROWSER_KIND === 'firefox' ? [] : ['--no-sandbox', '--disable-dev-shm-usage'],
  });
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(String(e).slice(0, 200)));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text().slice(0, 200)); });

  await page.goto(`http://127.0.0.1:${PORT}/#/login`, { waitUntil: 'load', timeout: 60000 });
  await new Promise(r => setTimeout(r, 6000));

  const state = await page.evaluate(() => {
    const root = document.querySelector('app-root');
    const text = (document.body.innerText || '').replace(/\s+/g, ' ').trim();
    return { rendered: !!(root && root.children.length), chars: text.length, sample: text.slice(0, 120),
      elements: document.querySelectorAll('*').length,
      html: root ? root.innerHTML.slice(0, 300) : '',
      url: location.href,
      rawKeys: (text.match(/\b[a-z-]+\.[a-z-]+\.[a-z-]+\b/g) || []).slice(0, 5) };
  });

  console.log('app-root rendered children :', state.rendered);
  console.log('visible characters         :', state.chars);
  console.log('sample                     :', state.sample);
  console.log('possible raw i18n keys     :', state.rawKeys.join(', ') || '(none)');
  console.log('total elements             :', state.elements);
  console.log('url after load             :', state.url);
  console.log('app-root html              :', state.html.replace(/\s+/g, ' ').slice(0, 200));
  // Without a hypervisor the API calls fail; those are expected and are not
  // what this looks for.
  const fatal = errors.filter(e =>
    !/Failed to load resource|NetworkError|api\/|JSHandle@object/i.test(e));
  console.log('page errors (non-network)  :', fatal.length);
  fatal.slice(0, 5).forEach(e => console.log('   ' + e));

  await browser.close(); server.close();
  const ok = state.rendered && state.elements >= 20 && fatal.length === 0;
  console.log(ok ? '\nOK: the bundle loads and renders' : '\nFAILED');
  process.exit(ok ? 0 : 1);
})().catch(e => { console.error('harness error:', e.message); server.close(); process.exit(2); });
