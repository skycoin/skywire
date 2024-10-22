#!/bin/bash
########## Skywire reward processing and calculation script reward.sh ##########
# Author: Moses Narrow
################################################################################
# Prevent running this script when rewards have already been distributed
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

####################################################
[[ ! -f "ut.txt" ]] && curl -s https://ut.skywire.skycoin.com/uptimes?v=v2 | tee 'ut.json' && go run ut.go ut.json | tee 'ut.txt'
[[ -f "ut.txt" ]] && [[ $(( $(date +%s) - $(date -r "ut.txt" +%s) )) -gt 3600 ]] && curl -s https://ut.skywire.skycoin.com/uptimes?v=v2 | tee 'ut.json' && go run ut.go ut.json | tee 'ut.txt' || echo "uptimes cache file recently updated, skipped fetching uptimes"
[[ ! -f "hist/${_wdate}_ut.json" ]] && cp  ut.json hist/${_wdate}_ut.json
[[ ! -f "hist/${_wdate}_ut.txt" ]] && cp  ut.txt hist/${_wdate}_ut.txt


# New reward pool starts November 2nd, 2024
if [[ $(date +%s) -lt $(date -d "2024-11-02" +%s) ]]; then
  #echo "The date is before November 2nd, 2024."
  #v1.3.29 - two reward pools - exclude pool 2
  skywire cli rewards -x "" -ed ${_wdate} -p log_backups  |  tee hist/${_wdate}_ineligible.csv
  skywire cli rewards -x "" -20d ${_wdate} -p log_backups |  tee hist/${_wdate}_shares.csv
  skywire cli rewards -x "" -10d $(date --date="yesterday" +%Y-%m-%d) -p log_backups | tee hist/${_wdate}_rewardtxn0.csv
  skywire cli rewards -x "" -12d ${_wdate} -p log_backups |  tee hist/${_wdate}_stats.txt
else
  #echo "The date is on or after November 2nd, 2024."
  #v1.3.29 - two reward pools - include pool 2
  skywire cli rewards -ed ${_wdate} -p log_backups  |  tee hist/${_wdate}_ineligible.csv
  skywire cli rewards -20d ${_wdate} -p log_backups |  tee hist/${_wdate}_shares.csv
  skywire cli rewards -10d $(date --date="yesterday" +%Y-%m-%d) -p log_backups | tee hist/${_wdate}_rewardtxn0.csv
  skywire cli rewards -12d ${_wdate} -p log_backups |  tee hist/${_wdate}_stats.txt
fi
#return
exit 0
