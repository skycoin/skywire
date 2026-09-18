#!/bin/sh
# dry-stub-cli.sh — a fake `skywire cli` for SWEEP_DRY=1.
#
#   CLI=bench/dry-stub-cli.sh SWEEP_DRY_STATE=<dir> bench/run-variants.sh ...
#
# It answers exactly the calls bench/run-variants.sh makes, out of three
# key=value files under $SWEEP_DRY_STATE:
#
#   local.knobs   the local visor's router catalog   (`route settings`)
#   exit.knobs    the exit visor's router catalog    (`route settings --via …`)
#   proxy.knobs   the app's catalog                  (`proxy settings --app …`)
#
# so a dry run exercises the REAL apply/read-back/restore paths of the runner —
# a knob written here is read back from here, and a knob the runner forgets to
# restore is still sitting in the file when the self-test looks. Nothing here
# touches a visor, a transport or the network; the transfer half of a dry run is
# bench/dry-stub-bench.sh, which prices a row from these same files.
#
# The catalog it exposes is a SUBSET of the real one — enough knobs of each kind
# (bytes, ratio, count, bool, duration) to exercise membership resolution, plus
# one knob whose doc says "NEW route groups" so the dial-time note is exercised.
set -u
state=${SWEEP_DRY_STATE:-${TMPDIR:-/tmp}/sweep-dry}
mkdir -p "$state"

# --- the stub catalogs ---------------------------------------------------------
# name kind default doc
router_catalog() {
	cat <<'EOF'
leg.starve_ratio ratio 6 how many times the best active leg's delay basis a leg's own may exceed before it is cut to a probe per window
ecf.max_window_bytes bytes 8388608 per-leg send window ceiling
ecf.window_margin ratio 2 multiplier on proven delivery-per-RTT that sets a leg's send window
rack.reorder_factor ratio 1.25 slowest active leg's RTT times this is the reordering tolerance
reorder.window count 32768 how many out-of-order packets the receiver holds
sbd.enabled bool true run shared-bottleneck detection at all
dial.candidates count 3 floor on how many routes a mux dial asks the route finder for; read while a dial is CHOOSING a route
mux.sack bool true advertise CapSACK on NEW mux route groups
EOF
}
proxy_catalog() {
	cat <<'EOF'
tunnel.probe_interval duration 5s how often each tunnel's RTT is re-measured (also the settings-pull tick)
chunk.tunnel_concurrency count 4 chunks one tunnel may carry at once on a split download
chunk.per_tunnel count 2 chunks the planner aims to give each active tunnel
upload.concurrency count 4 chunks one tunnel may carry at once on a striped upload
spread.max_share ratio 0.4 largest fraction of one object's bytes any single route may carry
pool.size count 32 CEILING on tunnels held to the exit including the active ones
EOF
}

# seed <file> <catalog fn>: write the compiled defaults once.
seed() {
	[ -s "$1" ] && return 0
	$2 | while read -r n _k d _doc; do echo "$n=$d"; done > "$1"
}
seed "$state/local.knobs" router_catalog
seed "$state/exit.knobs" router_catalog
seed "$state/proxy.knobs" proxy_catalog

get() { awk -F= -v n="$2" '$1 == n {sub(/^[^=]*=/, ""); v = $0} END {print v}' "$1"; }
put() {
	_f=$1; _n=$2; _v=$3
	grep -v "^$_n=" "$_f" > "$_f.new" 2>/dev/null || : > "$_f.new"
	echo "$_n=$_v" >> "$_f.new"
	mv "$_f.new" "$_f"
}

# --- argument scan -------------------------------------------------------------
via=""; json=0; app=""; cmd=""; sub=""; sel=""; kvs=""
while [ $# -gt 0 ]; do
	case $1 in
	--via) via=$2; shift 2 ;;
	--json) json=1; shift ;;
	--app) app=$2; shift 2 ;;
	--select) sel=$2; shift 2 ;;
	-n | -k | -a | --range-port | --range-chunk-kib | --range-concurrency | --tunnels | --route) shift 2 ;;
	--direct | --prune) shift ;;
	*=*) kvs="$kvs $1"; shift ;;
	cli) shift ;;
	*)
		if [ -z "$cmd" ]; then cmd=$1
		elif [ -z "$sub" ]; then sub=$1
		fi
		shift
		;;
	esac
done

rfile=$state/local.knobs
[ -n "$via" ] && rfile=$state/exit.knobs

case "$cmd $sub" in
"route settings")
	for kv in $kvs; do
		k=${kv%%=*}; v=${kv#*=}
		router_catalog | awk -v n="$k" '$1 == n {f = 1} END {exit !f}' || { echo "stub: unknown router knob $k" >&2; exit 1; }
		put "$rfile" "$k" "$v"
	done
	[ "$json" = 1 ] || exit 0
	printf '{"knobs":{'
	first=1
	while IFS='=' read -r n v; do
		[ -n "$n" ] || continue
		[ "$first" = 1 ] || printf ','
		printf '"%s":"%s"' "$n" "$v"
		first=0
	done < "$rfile"
	printf '},"knob_detail":{'
	first=1
	router_catalog | while read -r n k d doc; do
		[ "$first" = 1 ] || printf ','
		printf '"%s":{"kind":"%s","value":"%s","default":"%s","doc":"%s"}' "$n" "$k" "$(get "$rfile" "$n")" "$d" "$doc"
		first=0
	done
	printf '}}\n'
	;;
"proxy settings")
	for kv in $kvs; do
		k=${kv%%=*}; v=${kv#*=}
		proxy_catalog | awk -v n="$k" '$1 == n {f = 1} END {exit !f}' || { echo "stub: unknown proxy knob $k" >&2; exit 1; }
		put "$state/proxy.knobs" "$k" "$v"
	done
	[ "$json" = 1 ] || exit 0
	printf '{"app":"%s","applied":1,"knobs":[' "${app:-skysocks-client}"
	first=1
	proxy_catalog | while read -r n k d doc; do
		[ "$first" = 1 ] || printf ','
		printf '{"name":"%s","value":"%s","default":"%s","kind":"%s","state":"applied","doc":"%s"}' \
			"$n" "$(get "$state/proxy.knobs" "$n")" "$d" "$k" "$doc"
		first=0
	done
	printf ']}\n'
	;;
"proxy start" | "proxy stop") exit 0 ;;
"proxy mux")
	# `proxy mux info -n <app> --json`: two groups, one leg each.
	echo '[{"desc":{"dst_port":49170},"tunnel_role":"active","legs":[{"transport_id":"aaaaaaaa-0000-0000-0000-000000000001","tp_type":"stcpr","remote_pk":"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"}],"recovery":{}},{"desc":{"dst_port":49171},"tunnel_role":"active","legs":[{"transport_id":"bbbbbbbb-0000-0000-0000-000000000002","tp_type":"stcpr","remote_pk":"0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13"}],"recovery":{}}]'
	;;
"visor state")
	case $sel in
	transports)
		# the byte counters climb with the transfer counter the stub bench keeps
		n=$(cat "$state/bytes" 2>/dev/null || echo 0)
		printf '{"transports":[{"id":"aaaaaaaa-0000-0000-0000-000000000001","log":{"sent":%s,"recv":%s}},{"id":"bbbbbbbb-0000-0000-0000-000000000002","log":{"sent":%s,"recv":%s}}]}\n' \
			"$n" "$n" "$n" "$n"
		;;
	summary) echo '{"summary":{"overview":{"build_info":{"commit":"stubcommit"}}}}' ;;
	*) echo '{}' ;;
	esac
	;;
"tp ls") echo '[{"id":"aaaaaaaa-0000-0000-0000-000000000001","remote_ip":"198.51.100.1"},{"id":"bbbbbbbb-0000-0000-0000-000000000002","remote_ip":"198.51.100.2"}]' ;;
*) echo "stub: unhandled command '$cmd $sub'" >&2; exit 1 ;;
esac
