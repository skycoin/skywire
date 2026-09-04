# Install Command Generator

Compose a copy-paste install command for your platform — Debian/Ubuntu,
Arch Linux, macOS, or Windows — with your choice of package mirror,
auto-update mechanism, and visor configuration flags. Keys for the
visor's identity can optionally be generated locally in your browser
(via the wasm cipher); the generator can also hand you a ready
`/etc/skywire.conf` or `skywire-config.json`.

The generator is embedded below. It is maintained in the
[skycoin/apt-repo](https://github.com/skycoin/apt-repo) repository and
also available standalone at
[deb.skywire.skycoin.com/generator](https://deb.skywire.skycoin.com/generator/).

<iframe src="https://deb.skywire.skycoin.com/generator/"
        title="Skywire install command generator"
        style="width:100%;height:2600px;border:0"
        loading="lazy"></iframe>

## See also

- [install.md](install.md) — all installation methods in detail
- [configuration.md](configuration.md) — `config gen` flags and the hypervisor UI
- [Packaging & Updates](../packaging/README.md) — how the packages and
  auto-update mechanisms behind these commands work
