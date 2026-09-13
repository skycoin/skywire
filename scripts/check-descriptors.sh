#!/usr/bin/env bash
# check-descriptors.sh — every non-test Go file carries a well-formed
# c<layer>-<domain>-<system> descriptor, and one package agrees on one value.
#
# See docs/code-descriptors.md. The descriptor is a TAXONOMY LABEL, not a
# rename: it is read and grepped, never part of an identifier.
#
# This checks only well-formedness and per-package consistency. It deliberately
# does NOT decide which domain a package belongs to — domain and system are a
# documentation grouping with no Go analogue (an `app` package importing `net`
# is normal), and the source standard is explicit that they must not become a
# lint rule. The one part that IS enforced, the layer, is enforced by the Go
# compiler already: import cycles do not build.
#
# Existing drift is listed below so the check fails on NEW drift while the debt
# stays visible and countable. Fixing one means deleting its line here in the
# same commit.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

RE='c[0-4]-(com|net|app|vis)-[a-z0-9]+'
ROOTS=(pkg cmd internal)

for r in "${ROOTS[@]}"; do
	[ -d "$r" ] || { echo "check-descriptors: $r not found — run from the repo" >&2; exit 2; }
done

# --- known drift, 2026-09-13 -------------------------------------------------
# Non-test files with no descriptor.
read -r -d '' ALLOW_UNTAGGED <<'LIST' || true
cmd/apps/skychat/commands/forward.go
cmd/apps/skychat/commands/reply.go
cmd/apps/skydex-client/commands/auth.go
cmd/apps/skydex-client/commands/skydex-client.go
cmd/apps/skydex-client/main.go
cmd/apps/skydex-market/commands/root.go
cmd/apps/skydex-market/commands/root_js.go
cmd/apps/skydex-market/main.go
cmd/skywire-cli/cliutil/livetui/livetui_js.go
cmd/skywire-cli/cliutil/pterm/pterm_js.go
cmd/skywire-cli/cliutil/pterm/pterm_native.go
cmd/skywire-cli/cliutil/putils/putils_js.go
cmd/skywire-cli/cliutil/putils/putils_native.go
cmd/skywire-cli/commands/dmsg/chat_tui.go
cmd/skywire-cli/commands/dmsg/chat_tui_js.go
cmd/skywire-cli/commands/edit/edit_js.go
cmd/skywire-cli/commands/rewards/server/statscxo.go
cmd/skywire-cli/commands/rewards/server/stats_tui_arch.go
cmd/skywire-cli/commands/rewards/server/stats_tui_coverage.go
cmd/skywire-cli/commands/rewards/server/stats_tui_data.go
cmd/skywire-cli/commands/rewards/server/stats_tui_dmsg.go
cmd/skywire-cli/commands/rewards/server/stats_tui.go
cmd/skywire-cli/commands/rewards/server/stats_tui_pervisor.go
cmd/skywire-cli/commands/rewards/server/stats_tui_sn.go
cmd/skywire-cli/commands/rewards/server/stats_tui_uptime.go
cmd/skywire-cli/commands/skychat/chat_tui.go
cmd/skywire-cli/commands/skychat/chat_tui_js.go
cmd/skywire-cli/commands/visor/ping/mux_bandwidth_tui_impl.go
cmd/skywire-cli/commands/visor/ping/tree_tui.go
cmd/skywire-cli/commands/visor/ping/tui_js.go
cmd/skywire-cli/commands/visor/state.go
cmd/skywire/commands/doc/serve.go
cmd/skywire/tui/install.go
cmd/skywire/tui/tui.go
cmd/skywire/tui/tui_js.go
internal/skydex-client/app/app.go
internal/skydex-market/app/app.go
pkg/app/appcommon/mock_addr.go
pkg/app/appcommon/mock_conn.go
pkg/app/appcommon/mock_listener.go
pkg/app/appevent/mock_rpc_client.go
pkg/app/appnet/mock_networker.go
pkg/app/appserver/mock_proc_manager.go
pkg/app/appserver/mock_rpc_ingress_client.go
pkg/cliout/cliconfig/cliconfig.go
pkg/cliout/clihelp/clihelp.go
pkg/cliout/climdisc/climdisc.go
pkg/cliout/cliout.go
pkg/cliout/cliproxy/cliproxy.go
pkg/cliout/clirewards/clirewards.go
pkg/cliout/cliroute/cliroute.go
pkg/cliout/clisvc/clisvc.go
pkg/cliout/clitp/clitp.go
pkg/cliout/clitps/clitps.go
pkg/cliout/clivisor/clivisor.go
pkg/cliout/filter.go
pkg/deployment/sd/store/mock_store.go
pkg/dmsg/dmsgc/dmsgc_js.go
pkg/dmsg/dmsgc/dmsgc_native.go
pkg/dmsg/dmsg/client_relay.go
pkg/dmsg/dmsg/relay_inproc.go
pkg/dmsg/dmsg/relay_local.go
pkg/flags/cloudshapes.go
pkg/flags/gencloud/main.go
pkg/flags/rain.go
pkg/rfclient/mock_client.go
pkg/router/fec_flush.go
pkg/router/fec.go
pkg/router/fec_mux.go
pkg/router/mock_id_reserver.go
pkg/router/mock_route_group_dialer.go
pkg/router/mock_router.go
pkg/router/router_intake.go
pkg/router/router_route_source.go
pkg/skysocks/rangesplit.go
pkg/skysocks/rangesplit_https.go
pkg/tpviz/wasmgl/overlay.go
pkg/tpviz/wasmgl/wasmgl.go
pkg/transport/dmsg_fallback_js.go
pkg/transport/dmsg_fallback_native.go
pkg/transport/network/addrresolver/mock_api_client.go
pkg/transport/network/mock_dialer.go
pkg/utclient/mock_api_client.go
pkg/visor/api_suspend.go
pkg/visor/autoconnect_wsgate_js.go
pkg/visor/autoconnect_wsgate_native.go
pkg/visor/rpcgrpc/ping_grpc.pb.go
pkg/visor/rpcgrpc/ping.pb.go
pkg/wasmhv/deskhost/desk_install_js.go
pkg/wasmhv/deskhost/desk_js.go
pkg/wasmhv/deskhost/desk_pair_js.go
pkg/wasmhv/deskhost/shell_exec_js.go
pkg/wasmhv/execwasm/embed_js.go
pkg/wasmhv/execwasm/embed_native.go
pkg/wasmhv/execwasm/execwasm.go
pkg/wasmhv/execwasm/serve.go
LIST

# Packages carrying more than one descriptor.
read -r -d '' ALLOW_MIXED <<'LIST' || true
pkg/app/appnet
pkg/app/launcher
pkg/cxo/cxosub
pkg/httputil
pkg/logging
pkg/proxystatus
pkg/pty
pkg/router
pkg/tpviz
pkg/visor
pkg/visor/visorconfig
pkg/vpn
LIST
# -----------------------------------------------------------------------------

fail=0
missing=0
mixed=0

while IFS= read -r f; do
	[ -n "$f" ] || continue
	case "$f" in *_test.go) continue ;; esac
	if grep -qE "$RE" "$f" 2>/dev/null; then continue; fi
	if printf '%s\n' "$ALLOW_UNTAGGED" | grep -qxF "$f"; then
		missing=$((missing + 1))
		continue
	fi
	echo "NO DESCRIPTOR: $f — add a c<layer>-<domain>-<system> tag (docs/code-descriptors.md)"
	fail=1
done < <(find "${ROOTS[@]}" -name '*.go' 2>/dev/null | sort)

while IFS= read -r d; do
	[ -n "$d" ] || continue
	n=$(grep -rhoE "$RE" "$d"/*.go 2>/dev/null | sort -u | wc -l)
	[ "$n" -gt 1 ] || continue
	if printf '%s\n' "$ALLOW_MIXED" | grep -qxF "$d"; then
		mixed=$((mixed + 1))
		continue
	fi
	echo "MIXED DESCRIPTORS: $d carries $(grep -rhoE "$RE" "$d"/*.go 2>/dev/null | sort -u | tr '\n' ' ')— one package, one descriptor"
	fail=1
done < <(find "${ROOTS[@]}" -name '*.go' -not -name '*_test.go' 2>/dev/null | xargs -r -n1 dirname | sort -u)

echo "descriptors: known debt — ${missing} untagged file(s), ${mixed} mixed package(s); see docs/code-descriptors.md"
exit "$fail"
