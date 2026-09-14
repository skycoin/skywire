// identity-vault.test.js — run by `make check-browseui`. Plain node, no framework:
// the vault is WebCrypto and localStorage, both of which node provides or can
// be stubbed in a dozen lines, and a device-theft defence is worth testing for
// the properties rather than for coverage.
// Exercises the vault against the properties that matter for device theft.
const store = {};
globalThis.localStorage = {
  getItem: k => (k in store ? store[k] : null),
  setItem: (k, v) => { store[k] = String(v); },
  removeItem: k => { delete store[k]; },
};
globalThis.btoa = s => Buffer.from(s, 'binary').toString('base64');
globalThis.atob = s => Buffer.from(s, 'base64').toString('binary');
require(process.argv[2]);
const V = globalThis.SkywireIdentityVault;
const SK = 'ef5532f6'.repeat(8); // 64 hex = 32 bytes
let failed = 0;
const ok = (name, cond) => { console.log((cond ? '  ok  ' : '  FAIL ') + name); if (!cond) failed++; };

(async () => {
  // round trip
  const vault = await V.encrypt(SK, 'correct horse battery staple');
  ok('vault record is versioned', vault.v === 1);
  ok('ciphertext is not the key', !JSON.stringify(vault).includes(SK));
  ok('iteration count recorded', vault.kdf.iterations >= 600000);
  const back = await V.decrypt(vault, 'correct horse battery staple');
  ok('round trip returns the key', back === SK);

  // wrong passphrase
  try { await V.decrypt(vault, 'wrong'); ok('wrong passphrase rejected', false); }
  catch (e) { ok('wrong passphrase rejected', /wrong passphrase/.test(e.message)); }

  // tampered ciphertext — GCM must catch it
  const t = JSON.parse(JSON.stringify(vault));
  const raw = Buffer.from(t.ct, 'base64'); raw[0] ^= 0xff; t.ct = raw.toString('base64');
  try { await V.decrypt(t, 'correct horse battery staple'); ok('tampered vault rejected', false); }
  catch (e) { ok('tampered vault rejected', true); }

  // a short/!hex key must not be accepted
  try { await V.encrypt('abcd', 'p'); ok('short key rejected', false); }
  catch (e) { ok('short key rejected', /32 bytes/.test(e.message)); }
  try { await V.encrypt('zz'.repeat(32), 'p'); ok('non-hex key rejected', false); }
  catch (e) { ok('non-hex key rejected', /not hex/.test(e.message)); }

  // empty passphrase
  try { await V.encrypt(SK, ''); ok('empty passphrase rejected', false); }
  catch (e) { ok('empty passphrase rejected', /passphrase is required/.test(e.message)); }

  // storage round trip
  V.store(vault); ok('isEnabled true once stored', V.isEnabled() === true);
  V.clear(); ok('isEnabled false once cleared', V.isEnabled() === false);

  // legacy key removal
  store['skywire-visor-sk'] = SK;
  ok('legacy key found and dropped', V.dropLegacyKey() === true);
  ok('legacy key is gone', store['skywire-visor-sk'] === undefined);
  ok('dropping again reports nothing', V.dropLegacyKey() === false);

  console.log(failed ? `\n${failed} FAILED` : '\nall vault properties hold');
  process.exit(failed ? 1 : 0);
})();
