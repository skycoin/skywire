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
	// /etc/skywire.conf is deliberately NOT seeded, for the same reason the
	// deb/arch packages do not ship it: `skywire autoconfig` writes it from
	// the annotated template on its first run and never overwrites it after,
	// so the tab gets the SAME file a packaged host gets — every knob present
	// and commented at its default, editable with the shell exactly as on
	// Linux. A three-line stub here used to pre-empt that, which left the tab
	// with no knobs to edit (ISHYPERVISOR among them) and made the browser
	// visor diverge from a native one for no reason.
	//
	// The directory is still ours to make: autoconfig creates it too, but a
	// shell that wants to drop a conf in before the first run should find it.
	j.writeFile('/home/user/README',
		'This is an in-memory filesystem shared by the shell and the skywire binary.\n' +
		'skywire is "installed" under /opt/skywire — try:\n' +
		'    skywire autoconfig\n' +
		'    skywire cli config gen -rp\n' +
		'    cat /opt/skywire/skywire.json | jq .pk\n');
})();
