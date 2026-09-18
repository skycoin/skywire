#!/bin/sh
# shellcheck disable=SC2154,SC2034 # a library: $out, $CLI and the settings_* results are the
# CALLER's variables, set and read across the source boundary.
# lib-settings.sh — apply live tuning knobs to the APP UNDER TEST, once per set.
#
#   . "$here/lib-settings.sh"
#
# A LIBRARY: functions only, no top-level work beyond defaulting its own
# variables, so sourcing it can never disturb a measurement.
#
# WHY IT HAS TO BE HERE AND NOWHERE ELSE. `skywire cli proxy settings` (#5002)
# writes a knob into the visor's PER-APP store and the app pulls it on the
# keepalive tick it already runs (tunnel.probe_interval, 5 s). The store is
# cleared when the app stops — and every runner in this directory STARTS the app
# under test itself, per set. So a knob set before the script is gone by the
# first row. The hook is placed after the app is up and warm (and, where a pool
# is awaited, after the pool has settled) and BEFORE the first row, which is the
# only window where a knob is both installable and covers the whole set.
#
#   SETTINGS="upload.chunk_bytes=2MiB pool.fill_interval=500ms"
#
# is applied with one `proxy settings --app <the app under test>` call, then
# WAITED FOR: the CLI reports each knob as pending until the app has reported a
# version that carries it, so settings_apply polls `--json` until nothing is
# pending. The cap is 3 x the app's own tunnel.probe_interval or SETTINGS_WAIT
# (20 s), whichever is shorter — a knob that has not landed in three pulls is
# not going to, and the set is recorded saying so rather than silently measuring
# the compiled default.
#
# The PAIRED REFERENCE INSTANCES (bench/lib-paired.sh) never receive any of
# this. They are the control: the whole point of the paired ratio is that the
# reference is the same on every run of a sweep, so only the subject moves.
#
# Artefacts, per set:
#   <set>.settings.json    the `proxy settings --app <app> --json` dump taken
#                          after the wait — every knob, its value, its compiled
#                          default and its state.
#   <set>.route-settings.json   the `route settings --json` BEFORE state, when
#                          ROUTE_SETTINGS moved a router knob.
# and $settings_note, which the runner appends to the set's `# ...` header line.
#
# ROUTE_SETTINGS is the visor-wide half: whole flags, not key=value, because
# `route settings` takes flags.
#
#   ROUTE_SETTINGS="--ecf-max-window 16MiB --send-window-wait-max 40ms"
#
# It is applied once at set start and RESTORED at set end (settings_restore),
# because unlike an app knob a router knob outlives the app and would silently
# carry into the next set — and into the paired reference, which shares the
# visor. `route settings --json` is a real getter, so the previous values are
# read back from it and re-applied by flag; ROUTE_SETTINGS_RESTORE overrides
# that with an explicit flag list when a knob ever grows a spelling the getter
# does not round-trip.
SETTINGS=${SETTINGS:-}
ROUTE_SETTINGS=${ROUTE_SETTINGS:-}
ROUTE_SETTINGS_RESTORE=${ROUTE_SETTINGS_RESTORE:-}
SETTINGS_WAIT=${SETTINGS_WAIT:-20}      # ceiling on the wait for a knob to land, seconds
SETTINGS_POLL=${SETTINGS_POLL:-2}
settings_note=""                        # appended to the set header by the caller
settings_route_applied=0

# _settings_secs <go duration> -> whole seconds (rounded up, floor 1).
# The knob table prints durations the way Go does: 5s, 250ms, 4m.
_settings_secs() {
	echo "$1" | awk '{
		v = $0
		if (match(v, /ms$/)) { n = substr(v, 1, length(v) - 2) + 0; s = n / 1000 }
		else if (match(v, /m$/)) { n = substr(v, 1, length(v) - 1) + 0; s = n * 60 }
		else if (match(v, /s$/)) { n = substr(v, 1, length(v) - 1) + 0; s = n }
		else { s = v + 0 }
		if (s <= 0) s = 5
		printf "%d", (s == int(s)) ? s : int(s) + 1
	}'
}

# _settings_pending <json file> -> the names of the knobs the app has not
# installed yet, space separated. Empty when everything has landed.
_settings_pending() {
	jq -r '[.knobs[]? | select(.state == "pending") | .name] | join(" ")' "$1" 2>/dev/null
}

# settings_apply <set> <app>: the whole hook. Returns 0 always — a knob that
# will not land is a fact about the run, recorded in the header and the dump,
# not a reason to throw away a set.
settings_apply() {
	_s_set=$1; _s_app=$2
	settings_note=""
	_settings_route_apply "$_s_set"
	[ -n "$SETTINGS" ] || return 0
	echo "$_s_app: applying live knobs: $SETTINGS"
	# shellcheck disable=SC2086 # SETTINGS is a deliberate word list of key=value
	$CLI cli proxy settings --app "$_s_app" $SETTINGS >/dev/null 2>&1 || {
		echo "$_s_app: 'proxy settings' REFUSED '$SETTINGS' — the set runs on the compiled defaults"
		settings_note="settings=REFUSED:$(echo "$SETTINGS" | tr ' ' ',')"
		return 0
	}
	$CLI cli proxy settings --app "$_s_app" --json > "$out/$_s_set.settings.json" 2>/dev/null
	# the cap is three of the app's OWN pull ticks, never more than SETTINGS_WAIT
	_s_probe=$(jq -r '[.knobs[]? | select(.name == "tunnel.probe_interval") | .value][0] // "5s"' "$out/$_s_set.settings.json" 2>/dev/null)
	_s_cap=$((3 * $(_settings_secs "$_s_probe")))
	[ "$_s_cap" -gt "$SETTINGS_WAIT" ] && _s_cap=$SETTINGS_WAIT
	_s_w=0
	while :; do
		_s_pend=$(_settings_pending "$out/$_s_set.settings.json")
		[ -z "$_s_pend" ] && break
		[ "$_s_w" -ge "$_s_cap" ] && break
		sleep "$SETTINGS_POLL"; _s_w=$((_s_w + SETTINGS_POLL))
		$CLI cli proxy settings --app "$_s_app" --json > "$out/$_s_set.settings.json" 2>/dev/null
	done
	_s_applied=$(jq -r '[.knobs[]? | select(.state == "applied") | .name + "=" + .value] | join(",")' "$out/$_s_set.settings.json" 2>/dev/null)
	if [ -z "$_s_pend" ]; then
		echo "$_s_app: knobs landed after ${_s_w}s (probe interval $_s_probe): $_s_applied"
		settings_note="settings=$_s_applied settings_landed=${_s_w}s"
	else
		echo "$_s_app: knob(s) STILL PENDING after ${_s_w}s (cap ${_s_cap}s, probe interval $_s_probe): $_s_pend — the set is recorded as PENDING"
		settings_note="settings=$_s_applied settings_pending=$(echo "$_s_pend" | tr ' ' ',')"
	fi
	[ -n "$ROUTE_SETTINGS" ] && settings_note="$settings_note route_settings=$(echo "$ROUTE_SETTINGS" | tr ' ' ',')"
	return 0
}

# _settings_route_apply <set>: snapshot the router knobs, then move them.
_settings_route_apply() {
	settings_route_applied=0
	[ -n "$ROUTE_SETTINGS" ] || return 0
	$CLI cli route settings --json > "$out/$1.route-settings.json" 2>/dev/null
	if [ ! -s "$out/$1.route-settings.json" ]; then
		echo "route settings: no --json state could be read — ROUTE_SETTINGS is applied but can only be restored from ROUTE_SETTINGS_RESTORE"
	fi
	echo "route settings: $ROUTE_SETTINGS"
	# shellcheck disable=SC2086 # ROUTE_SETTINGS is a deliberate flag list
	$CLI cli route settings $ROUTE_SETTINGS >/dev/null 2>&1 || {
		echo "route settings: REFUSED '$ROUTE_SETTINGS' — the router knobs are untouched"
		return 0
	}
	settings_route_applied=1
	[ -n "$SETTINGS" ] || settings_note="route_settings=$(echo "$ROUTE_SETTINGS" | tr ' ' ',')"
	return 0
}

# _settings_route_prev <set>: the restore flag list, built from the BEFORE dump
# for exactly the flags ROUTE_SETTINGS named — restoring a flag the run never
# touched would write a value the visor may not have been holding.
_settings_route_prev() {
	_r_j="$out/$1.route-settings.json"
	[ -s "$_r_j" ] || return 1
	_r_args=""
	for _r_f in $(echo "$ROUTE_SETTINGS" | tr ' ' '\n' | grep '^--'); do
		case $_r_f in
		--prefer) _r_v=$(jq -r '.transport_preference | join(",")' "$_r_j" 2>/dev/null); [ -n "$_r_v" ] || _r_v=default ;;
		--min-hops) _r_v=$(jq -r '.min_hops' "$_r_j" 2>/dev/null) ;;
		--existing-tp-only) _r_v=$(jq -r '.existing_tp_only' "$_r_j" 2>/dev/null) ;;
		--force-local) _r_v=$(jq -r '.force_local_routes' "$_r_j" 2>/dev/null) ;;
		--ecf-max-window) _r_v=$(jq -r '.ecf_max_window_bytes' "$_r_j" 2>/dev/null) ;;
		--ecf-min-window) _r_v=$(jq -r '.ecf_min_window_bytes' "$_r_j" 2>/dev/null) ;;
		--ecf-window-margin) _r_v=$(jq -r '.ecf_window_margin' "$_r_j" 2>/dev/null) ;;
		--send-window-wait-max) _r_v=$(jq -r '.send_window_wait_max' "$_r_j" 2>/dev/null) ;;
		--leg-park-min-hold) _r_v=$(jq -r '.leg_park_min_hold' "$_r_j" 2>/dev/null) ;;
		--dead-route-hold) _r_v=$(jq -r '.dead_route_hold' "$_r_j" 2>/dev/null) ;;
		--dead-route-hold-max) _r_v=$(jq -r '.dead_route_hold_max' "$_r_j" 2>/dev/null) ;;
		--mux-fec) _r_v=$(jq -r '.mux_fec' "$_r_j" 2>/dev/null) ;;
		*) echo "route settings: $_r_f has no known --json field — set ROUTE_SETTINGS_RESTORE to restore it"; return 1 ;;
		esac
		[ -n "$_r_v" ] && [ "$_r_v" != null ] || { echo "route settings: $_r_f could not be read back — set ROUTE_SETTINGS_RESTORE"; return 1; }
		_r_args="$_r_args $_r_f $_r_v"
	done
	echo "$_r_args"
}

# settings_restore <set>: put the ROUTER knobs back. The APP knobs need no
# restore — the store dies with the app, which every runner stops at set end.
settings_restore() {
	[ "$settings_route_applied" = 1 ] || return 0
	if [ -n "$ROUTE_SETTINGS_RESTORE" ]; then
		_r_back=$ROUTE_SETTINGS_RESTORE
	else
		_r_back=$(_settings_route_prev "$1") || {
			echo "route settings: NOT restored — the visor keeps '$ROUTE_SETTINGS' into the next set"
			return 0
		}
	fi
	# shellcheck disable=SC2086 # a deliberate flag list
	$CLI cli route settings $_r_back >/dev/null 2>&1 &&
		echo "route settings: restored$_r_back" ||
		echo "route settings: restore '$_r_back' REFUSED — the visor keeps '$ROUTE_SETTINGS'"
	settings_route_applied=0
	return 0
}
