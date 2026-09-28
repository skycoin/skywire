# G0 evidence — fatal / exit audit (playbook item 0.6a)

In-process on iOS, an `os.Exit` or a `Fatal` kills the app (or, on a device,
the packet-tunnel extension). The audit covers the §1 list and, beyond it,
every first-party `os.Exit` / `Fatal` in the iOS import graph of
`./pkg/mobilecore` (all 685 non-stdlib packages; the list was built with
`go list -deps -f '{{.Dir}} {{.GoFiles}}'` under GOOS=ios and the mobile
tags, then grepped file by file). `pkg/mobilecore` itself has none in its
non-test code.

## The §1 list

| Site | Verdict |
|---|---|
| `cmd/skywire-visor/commands/root.go` (14 sites) | Unreachable: cobra PreRun/Run of the visor command. `mobilecore` starts the visor through `visor.StartInProcess`, never the command. |
| `cmd/skywire-visor/commands/etc.go:48` | Unreachable: `--completion` in the visor command. |
| `pkg/visor/withoutsystray.go:24` | Unreachable: `RunTrayOnly`, only from the command's `--systray-only`. |
| `pkg/visor/visorconfig/hypervisorconfig.go:217` (`GenerateWorkDirConfig`) | **Reachable** from `GenConfig` (`config gen -i`). Converted: stdlib `log.Fatalf` on a failed `os.Getwd` → a logged fallback to the cwd-relative `users.db` (the same file). |
| `pkg/visor/visorconfig/hypervisorconfig_native.go:51` (`HypervisorConfig.Parse`) | Converted: the deferred `log.Fatalf` on `Close` → a returned error (named result). |
| `cmd/apps/vpn-client/commands/vpn-client.go:90`, `skychat.go:747`, `skysocks-client.go:227,742`, `skydex-client.go:94` | Unreachable: the cobra `Run`/`Execute` wrappers. The launcher runs the apps through the `RunFunc`s they register (`launcher.RegisterApp`), which return errors. |

## Found beyond the list

| Site | Verdict |
|---|---|
| `pkg/visor/api_visor.go` `Shutdown()` `defer os.Exit(0)` (`POST …/shutdown`, `cli visor halt`) | **Reachable** from the API. A visor started with `StartInProcess` carries an `InProcessHost`; `Shutdown` calls its `Stop` (mobilecore: `Stop`), `Reload` (`POST …/restart`) calls its `Restart` (mobilecore: stop + start in place, or the host's `RestartHook`). The `os.Exit` stays for desktop only. Covered by `TestRestartRoute`. |
| `cmd/skywire-cli/commands/config/gen.go` (48 logrus `Fatal`s), `update.go`, `parse.go`, `services.go` | **Reachable** through `GenConfig`. `cliconfig.RunGen` swaps the global logger's exit function for a panic it recovers, so a `Fatal` in `config gen` comes back as an error (`TestGenConfigErrors`). The three `os.Exit`s in gen's PreRun are for `-q`, `--all` and `--envfile`, which `GenOptions` cannot express. |
| `pkg/transport/manager.go` `runClient` `Fatalf("failed to listen on network …")` | **Reachable** at runtime (a network that cannot listen). Converted to `Errorf` + return, as `client.Start()` failures right above it already are: the other networks keep serving. Desktop behaviour change: that visor now degrades instead of exiting. |
| `pkg/transport/network/addrresolver/client.go:279` `Fatal("Permanently failed to connect to address resolver.")` | Unreachable in practice: the retrier before it retries forever (`tries=0`). Follow-up, not fixed here: that retry runs on `context.Background()`, so a core stopped while the AR is unreachable leaves the retry goroutine behind. |
| `pkg/cxo/skyobject/fill.go:67` `fatal("DB failure:", err)` (stdlib `log.Fatalln`) | **Reachable** on a CXO database error (a corrupt or failing bbolt file). Converted: `Filler.get` returns the error (`DB failure: %w`); every `Splitter` caller already hands a `Get`/`Pre` error to `Fail`, so that one filling ends with it and `Run` rejects its refcount increments. The node reports it through `OnFillingBreaks`, closes that filler without moving its seq forward, and fills the next Root it gets. Covered by `TestFiller_DBFailure_FailsFillingNotProcess` (a CXDS that fails `Get` for one key, both on `Run`'s own registry read and in a Split goroutine). Desktop behaviour change: that visor no longer exits on the error. |
| `pkg/app/client.go:50` `Fatal("Failed to obtain proc config.")` | Unreachable in practice: the launcher always sets the proc config for the app it starts (this is the path Android's in-process apps already take). |
| `pkg/skysocks/server.go:251` `os.Exit(1)` | Unreachable: SOCKS *server*; the phone runs the client and the profile disables the server app. |
| `pkg/cmdutil/catch.go`, `pkg/cmdutil/climisc.go`, `pkg/dmsg/dmsgclient/cli.go`, `cmd/skywire-cli/cliutil/cliutil.go`, `pkg/cliout/cliout.go` | Unreachable: command-line helpers (cobra `Execute` wrappers, `Catch` for flag errors). |
| `pkg/cxo/skyobject/container.go` `fatal`/`fatalf` | The helpers behind `fill.go:67` above; no other caller. Deleted with that fix. |
