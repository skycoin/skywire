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
#date_ut.txt          $ skywire cli ut > date_ut.txt
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
# Pin the UT cache to hist/ instead of the default `/tmp/<UThost>/`.
# Default cacheDirPath() uses os.TempDir() which is shared across all
# UIDs; if any other user (e.g. a root-owned visor on the same host)
# touches that path first it's created mode 0750 root:root and the
# reward service (running as the rewards user) silently EACCES on the
# jq read below. Bash truncates the redirect target BEFORE jq runs,
# so the failure surfaces as an empty hist/<date>_ut.json — which
# `ParseHistoricUptimeData` then skips, freezing the version-history
# chart on the last good day.
skywire cli ut --cdu hist/ | tee "hist/${_wdate}_ut.txt"
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
skywire cli rewards -b --utfile "hist/${_wdate}_ut.txt" -ed ${_wdate} -p log_backups  |  tee hist/${_wdate}_ineligible.csv
skywire cli rewards -b --utfile "hist/${_wdate}_ut.txt" -20d ${_wdate} -p log_backups |  tee hist/${_wdate}_shares.csv
skywire cli rewards -b --utfile "hist/${_wdate}_ut.txt" -10d ${_wdate} -p log_backups | tee hist/${_wdate}_rewardtxn0.csv
skywire cli rewards -b --utfile "hist/${_wdate}_ut.txt" -12d ${_wdate} -p log_backups |  tee hist/${_wdate}_stats.txt
#return
exit 0
