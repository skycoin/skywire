// pkg/wasmhv/browseui/seed-skywire.js c3-vis-wasm
// Seeds the skywire package layout into the generic Linux root that bottle's
// jsfs.js installs — the application half of the split: bottle owns the FHS
// skeleton, this file makes the tab look like a host with the skywire
// package installed. Runs right after jsfs.js in the BrowseJS bundle;
// idempotent so a bundle loaded twice doesn't clobber operator edits.
(function () {
	'use strict';
	var j = globalThis.jsfs;
	if (!j || !j.installed || j.skywireSeeded) return;
	j.skywireSeeded = true;

	['/etc/skywire', '/opt/skywire/apps', '/opt/skywire/bin', '/opt/skywire/local',
		'/var/log/skywire',
	].forEach(function (d) { j.mkdirp(d); });

	j.writeFile('/etc/hostname', 'skywire-playground\n');
	j.writeFile('/etc/os-release', 'PRETTY_NAME="Skywire Playground (wasm)"\nID=skywire-playground\n');
	// The SKYENV file, exactly as the Linux packages ship it: PKGENV=true
	// makes `skywire autoconfig` / `skywire cli config gen` resolve the
	// package paths (/opt/skywire/skywire.json). Edit it with the shell
	// the same way you would on Linux.
	j.writeFile('/etc/skywire.conf',
		'#/etc/skywire.conf\n' +
		'#sourced by `skywire autoconfig` and `skywire cli config gen`\n' +
		'PKGENV=true\n');
	j.writeFile('/home/user/README',
		'This is an in-memory filesystem shared by the shell and the skywire binary.\n' +
		'skywire is "installed" under /opt/skywire — try:\n' +
		'    skywire autoconfig\n' +
		'    skywire cli config gen -rp\n' +
		'    cat /opt/skywire/skywire.json | jq .pk\n');
})();
