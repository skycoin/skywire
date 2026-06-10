#!/bin/bash
########## Skywire reward processing and calculation script reward.sh ##########
# Author: Moses Narrow
################################################################################
## Files:
#date_ineligible.csv  account of non rewarded visors
#date_rewardtxn0.csv  reward transaction CSV
#date_shares.csv      reward shares CSV
#date_stats.txt       statistical data
#date_ut.json         backup of uptime tracker data (single day)
#date_ut.txt          $ skywire cli ut tpd > date_ut.txt (long-form: pk date pct)
#date.txt             transaction ID of reward distribution transaction - indicates rewards sent if exists
################################################################################
# Prevent running this script when rewards have already been distributed
[[ ! -z ${_wdate} ]] && [[ -f hist/${_wdate}.txt ]] && echo "Transaction already broadcasted for ${_wdate}" && exit 0
if [[ -z ${_wdate} ]] ; then
[[ -f hist/$(date --date="yesterday" +%Y-%m-%d).txt ]] && echo "Transaction already broadcasted for yesterday" && exit 0
# Determine the date for which to calculate rewards
# based on the last file containing the reward transaction that exists
# (i.e. 2023-05-01.txt)
###uncomment the below line to do historic calculations
#[[ -z $_wdate ]] && _wdate="$(date -d "$(find hist/????-??-??.txt | tail -n1 | cut -d '/' -f2 | cut -d '.' -f1) +1 day" "+%Y-%m-%d")"
###comment the below line to do historic calculations
[[ ! -f hist/$(date --date="yesterday" +%Y-%m-%d).txt ]] && _wdate=$(date --date="yesterday" +%Y-%m-%d)
## OR specify a date like yesterday ##
#_wdate=$(date --date="yesterday" +%Y-%m-%d) ./reward.sh
fi
####################################################
# Source uptime data from the TPD-integrated uptime tracker instead of
# the standalone UT service. TPD records transport-discovery
# registrations — a stronger reachability signal than plain visor→UT
# heartbeats. Visors that heartbeat but never register a transport
# (so add no usable capacity) were previously credited by standalone
# UT but contribute nothing to the network; switching to TPD removes
# those phantom visors from the eligible set. Empirically the
# eligible-PK pool changes by ~10–15% per day in either direction.
#
# Pin the cache to hist/ for the same reason --cdu hist/ did
# previously: default cacheDirPath() uses os.TempDir() which is shared
# across UIDs; a root-owned visor on the same host can claim it first
# at mode 0750 root:root and the reward service silently EACCES.
#
# `ut tpd --since <d> --until <d>` emits wide-form text:
#     pk  ver  on  YYYY-MM-DD
# Reshape to long form `pk YYYY-MM-DD pct` (the format --utfile reads),
# dropping rows where the per-day column is '-' (no data for that
# date — visor offline that day or new to the network).
skywire cli ut tpd --since "${_wdate}" --until "${_wdate}" --cache-dir hist/ \
    | awk -v d="${_wdate}" 'NR > 1 && $4 != "-" { print $1, d, $4 }' \
    | tee "hist/${_wdate}_ut.txt"
# Fetch the same day as JSON for hist/uptimes.json. Same schema as the
# previous standalone-source uptimes.json — array of
# {pk, on, version, daily:{date:pct,...}} — so the jq slice below
# (and any other consumer of hist/uptimes.json such as the
# version-history chart) keeps working without modification. Atomic
# via .tmp + mv on success so a failed fetch leaves the previous
# (possibly populated) file intact instead of zeroing it.
if skywire cli ut tpd --since "${_wdate}" --until "${_wdate}" --cache-dir hist/ --json \
       > "hist/uptimes.json.tmp" 2>/dev/null; then
    mv "hist/uptimes.json.tmp" "hist/uptimes.json"
else
    echo "WARN: failed to fetch TPD UT as JSON for ${_wdate}" >&2
    rm -f "hist/uptimes.json.tmp"
fi
# Extract just the target date's data from the user-owned cache into
# a date-specific file. Write to a .tmp first and mv-on-success so a
# failed jq leaves the previous (possibly populated) file intact
# instead of zeroing it.
if jq --arg date "${_wdate}" '[.[] | {pk, on, version, daily: {($date): .daily[$date]}}]' hist/uptimes.json > "hist/${_wdate}_ut.json.tmp" 2>/dev/null; then
    mv "hist/${_wdate}_ut.json.tmp" "hist/${_wdate}_ut.json"
else
    echo "WARN: failed to build hist/${_wdate}_ut.json from hist/uptimes.json" >&2
    rm -f "hist/${_wdate}_ut.json.tmp"
fi
# Calculate rewards
skywire cli rewards -b -B 0 --utfile "hist/${_wdate}_ut.txt" -ed ${_wdate} -p log_backups  |  tee hist/${_wdate}_ineligible.csv
skywire cli rewards -b -B 0 --utfile "hist/${_wdate}_ut.txt" -20d ${_wdate} -p log_backups |  tee hist/${_wdate}_shares.csv
skywire cli rewards -b -B 0 --utfile "hist/${_wdate}_ut.txt" -10d ${_wdate} -p log_backups | tee hist/${_wdate}_rewardtxn0.csv
skywire cli rewards -b -B 0 --utfile "hist/${_wdate}_ut.txt" -12d ${_wdate} -p log_backups |  tee hist/${_wdate}_stats.txt
#return
exit 0
