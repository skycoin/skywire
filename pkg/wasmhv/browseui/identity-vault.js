// identity-vault.js — the tab visor's secret key, encrypted at rest.
//
// Threat model: someone has the device. They open the browser, the page loads,
// and without this the visor boots as you — the key sits in storage in the
// clear and nothing asks for anything. With it, the key at rest is ciphertext
// and the visor cannot start until a passphrase decrypts it.
//
// What it does NOT defend against, stated plainly because the difference
// matters: script running on the origin while the visor is up. The decrypted
// key is necessarily in memory then, and a page that has been tampered with
// can capture the passphrase as it is typed. On a self-hosted page that needs
// local access already; on a hosted one it means trusting whoever serves it.
// This protects a stolen device, not a compromised origin.
//
// PBKDF2-SHA256 over the passphrase, AES-GCM over the key. GCM authenticates,
// so a wrong passphrase and a tampered vault both surface as a failed decrypt
// rather than as plausible-looking garbage handed to a visor.
(function () {
	'use strict';
	if (globalThis.SkywireIdentityVault) return;

	var VAULT_KEY = 'skywire-visor-identity';
	// The pre-vault key. Nothing in the tree writes it any more and it does not
	// derive the running visor's public key — it is a leftover from before the
	// identity moved into the jsfs config. A stale 32-byte secret readable by
	// anything on the origin protects nothing and is worth exactly its removal.
	var LEGACY_SK_KEY = 'skywire-visor-sk';
	var ITERATIONS = 600000; // OWASP 2023 floor for PBKDF2-SHA256
	var SALT_BYTES = 16;
	var IV_BYTES = 12;      // 96 bits, the size AES-GCM is specified for

	function subtle() {
		var c = globalThis.crypto;
		if (!c || !c.subtle) {
			throw new Error('WebCrypto unavailable: a secure context is required to encrypt the visor key');
		}
		return c.subtle;
	}

	function bytesToHex(b) {
		var out = '';
		for (var i = 0; i < b.length; i++) { out += ('0' + b[i].toString(16)).slice(-2); }
		return out;
	}

	function hexToBytes(h) {
		if (typeof h !== 'string' || h.length % 2 !== 0 || /[^0-9a-fA-F]/.test(h)) {
			throw new Error('not hex');
		}
		var out = new Uint8Array(h.length / 2);
		for (var i = 0; i < out.length; i++) { out[i] = parseInt(h.substr(i * 2, 2), 16); }
		return out;
	}

	function b64(bytes) {
		var s = '';
		for (var i = 0; i < bytes.length; i++) { s += String.fromCharCode(bytes[i]); }
		return btoa(s);
	}

	function unb64(s) {
		var bin = atob(s);
		var out = new Uint8Array(bin.length);
		for (var i = 0; i < bin.length; i++) { out[i] = bin.charCodeAt(i); }
		return out;
	}

	function deriveKey(passphrase, salt, iterations) {
		var enc = new TextEncoder();
		return subtle().importKey('raw', enc.encode(passphrase), 'PBKDF2', false, ['deriveKey'])
			.then(function (base) {
				return subtle().deriveKey(
					{ name: 'PBKDF2', salt: salt, iterations: iterations, hash: 'SHA-256' },
					base,
					{ name: 'AES-GCM', length: 256 },
					false,
					['encrypt', 'decrypt']);
			});
	}

	// encrypt takes the 64-hex secret key and returns the vault record. The key
	// is handled as BYTES, not as the hex string: a vault over the text would
	// still be a vault, but it would leak the key's length and encoding into
	// the ciphertext size for no reason.
	function encrypt(skHex, passphrase) {
		if (!passphrase) { return Promise.reject(new Error('a passphrase is required')); }
		var sk;
		try { sk = hexToBytes(skHex); } catch (e) {
			return Promise.reject(new Error('secret key is not hex'));
		}
		if (sk.length !== 32) { return Promise.reject(new Error('secret key is not 32 bytes')); }
		var salt = globalThis.crypto.getRandomValues(new Uint8Array(SALT_BYTES));
		var iv = globalThis.crypto.getRandomValues(new Uint8Array(IV_BYTES));
		return deriveKey(passphrase, salt, ITERATIONS).then(function (key) {
			return subtle().encrypt({ name: 'AES-GCM', iv: iv }, key, sk);
		}).then(function (ct) {
			return {
				v: 1,
				kdf: { name: 'PBKDF2', hash: 'SHA-256', iterations: ITERATIONS, salt: b64(salt) },
				iv: b64(iv),
				ct: b64(new Uint8Array(ct)),
			};
		});
	}

	// decrypt returns the 64-hex secret key. A wrong passphrase and a tampered
	// vault are the same failure here — GCM authenticates — and both are
	// reported as one message rather than distinguished, since telling them
	// apart helps an attacker and not the operator.
	function decrypt(vault, passphrase) {
		if (!vault || vault.v !== 1 || !vault.kdf || !vault.iv || !vault.ct) {
			return Promise.reject(new Error('not a vault record'));
		}
		var iterations = vault.kdf.iterations;
		if (!(iterations > 0)) { return Promise.reject(new Error('vault has no iteration count')); }
		return deriveKey(passphrase, unb64(vault.kdf.salt), iterations).then(function (key) {
			return subtle().decrypt({ name: 'AES-GCM', iv: unb64(vault.iv) }, key, unb64(vault.ct));
		}).then(function (pt) {
			var sk = new Uint8Array(pt);
			if (sk.length !== 32) { throw new Error('decrypted key is not 32 bytes'); }
			return bytesToHex(sk);
		}, function () {
			throw new Error('wrong passphrase, or the stored key has been altered');
		});
	}

	function load() {
		try {
			var raw = localStorage.getItem(VAULT_KEY);
			return raw ? JSON.parse(raw) : null;
		} catch (e) { return null; }
	}

	function store(vault) {
		localStorage.setItem(VAULT_KEY, JSON.stringify(vault));
	}

	function clear() {
		try { localStorage.removeItem(VAULT_KEY); } catch (e) { /* storage denied */ }
	}

	// dropLegacyKey removes the pre-vault plaintext key. Returns whether there
	// was one. Called unconditionally at boot: it is dead in every page, and
	// leaving a secret in storage that nothing reads is the kind of thing that
	// is only ever found by whoever takes the device.
	function dropLegacyKey() {
		try {
			if (localStorage.getItem(LEGACY_SK_KEY) === null) { return false; }
			localStorage.removeItem(LEGACY_SK_KEY);
			console.info('skywire: removed a stale plaintext visor key left by an older build');
			return true;
		} catch (e) { return false; }
	}

	globalThis.SkywireIdentityVault = {
		VAULT_KEY: VAULT_KEY,
		LEGACY_SK_KEY: LEGACY_SK_KEY,
		encrypt: encrypt,
		decrypt: decrypt,
		load: load,
		store: store,
		clear: clear,
		isEnabled: function () { return !!load(); },
		dropLegacyKey: dropLegacyKey,
	};
})();
