// scripts/wasm-headless/harness.mjs — Tier B headless smoke of the REAL
// compiled wasm-visor blob, no browser: Node (≥22, for the global WebSocket)
// runs Go's wasm_exec.js, instantiates build/wasm-visor-go/wasm-visor.wasm,
// boots the visor against a local dmsg server's ws:// seed, and asserts the
// critical path the browser depends on: boot → wss/ws dmsg session →
// dmsg_connected → the in-wasm hypervisor /api core answering.
//
// The ONLY browser global the critical path needs is WebSocket (native in
// Node ≥22). Optional surfaces (WebTransport, WebRTC, localStorage) degrade
// gracefully and are exercised by the in-browser CDP tests instead.
//
// Usage: node harness.mjs <wasm> <wasm_exec.js> <seedPkHex> <seedWsURL>
// Exit 0 on success; 1 with diagnostics on failure/timeout.

import fs from 'node:fs';
import vm from 'node:vm';

const [wasmPath, execPath, seedPk, seedWs] = process.argv.slice(2);
if (!wasmPath || !execPath || !seedPk || !seedWs) {
  console.error('usage: node harness.mjs <wasm> <wasm_exec.js> <seedPkHex> <seedWsURL>');
  process.exit(2);
}

const BOOT_TIMEOUT_MS = 90_000;
const DMSG_TIMEOUT_MS = 60_000;
const die = (msg) => { console.error('FAIL:', msg); process.exit(1); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// Race a promise against a timeout so a never-settling call fails loudly here
// instead of leaving Node with an unsettled top-level await (which exits with an
// opaque "Detected unsettled top-level await" / code 13 and no diagnostic).
const withTimeout = (p, ms, what) => Promise.race([
  Promise.resolve(p),
  sleep(ms).then(() => { throw new Error(`${what} did not settle within ${ms}ms`); }),
]);
const safeStatus = () => {
  try {
    const st = globalThis.skywireVisor.status();
    return typeof st === 'string' ? st : JSON.stringify(st);
  } catch (e) { return `<status() threw: ${e}>`; }
};

// wasm_exec.js is a classic script that attaches `Go` to globalThis.
vm.runInThisContext(fs.readFileSync(execPath, 'utf8'), { filename: 'wasm_exec.js' });

const go = new globalThis.Go();
const { instance } = await WebAssembly.instantiate(fs.readFileSync(wasmPath), go.importObject);
go.run(instance).then(
  () => console.error('note: wasm program exited'),
  (e) => die(`wasm program crashed: ${e}`),
);

// The program registers globalThis.skywireVisor once its main() sets up.
const deadline = Date.now() + BOOT_TIMEOUT_MS;
while (typeof globalThis.skywireVisor?.boot !== 'function') {
  if (Date.now() > deadline) die('skywireVisor.boot never appeared');
  await sleep(100);
}
console.log('wasm loaded; skywireVisor API present');

// Boot with a fresh key against the local seed; empty disc ⇒ the deployment
// default is tried and degrades to seed-only (non-fatal) in this offline run.
// boot() must be raced against a timeout: if a discovered deployment dmsg server
// is dialed and hangs (unreachable from CI), the promise can stay pending
// forever and Node would exit with an opaque unsettled-top-level-await (code 13)
// instead of a diagnosable failure.
let pk;
try {
  pk = await withTimeout(globalThis.skywireVisor.boot('', seedPk, seedWs, ''), BOOT_TIMEOUT_MS, 'skywireVisor.boot()');
} catch (e) {
  die(`${e.message} — likely a dmsg session dial hang (a discovered deployment ` +
      `server unreachable from this environment); the local ws seed alone should ` +
      `let boot() settle. last status=${safeStatus()}`);
}
console.log('booted as', pk);
if (!/^[0-9a-f]{66}$/.test(String(pk))) die(`boot returned a non-PK: ${pk}`);

// The critical-path assertion: a live dmsg session over the ws seed. Give this
// its own budget rather than sharing the boot deadline (which boot() may have
// mostly consumed).
const dmsgDeadline = Date.now() + DMSG_TIMEOUT_MS;
for (;;) {
  const st = globalThis.skywireVisor.status();
  const o = typeof st === 'string' ? JSON.parse(st) : st;
  if (o.dmsg_connected) {
    console.log(`dmsg connected (${o.dmsg_sessions} session[s])`);
    break;
  }
  if (Date.now() > dmsgDeadline) die(`dmsg never connected within ${DMSG_TIMEOUT_MS}ms: ${JSON.stringify(o)}`);
  await sleep(250);
}

// And the in-wasm hypervisor core must answer /api.
const dec = (b) => {
  if (b == null) return '';
  if (typeof b === 'string') return b;
  try { return new TextDecoder().decode(b); } catch { return String(b); }
};
let about;
try {
  about = await withTimeout(globalThis.skywireVisor.hvApi('GET', '/api/about', null), DMSG_TIMEOUT_MS, 'hvApi(/api/about)');
} catch (e) {
  die(`${e.message} — the in-wasm hypervisor core did not answer. last status=${safeStatus()}`);
}
const aboutBody = dec(about?.body ?? about);
let aboutObj;
try { aboutObj = JSON.parse(aboutBody); } catch { die(`/api/about not JSON: ${aboutBody.slice(0, 120)}`); }
if (!aboutObj.dmsg_connected) die(`/api/about reports dmsg_connected=false: ${aboutBody.slice(0, 200)}`);
console.log('hvApi /api/about OK:', aboutBody.slice(0, 120));

console.log('PASS: headless wasm-visor boot → dmsg(ws) → /api core');
process.exit(0);
