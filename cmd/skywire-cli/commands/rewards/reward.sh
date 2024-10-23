#!/bin/bash
########## Skywire reward processing and calculation script reward.sh ##########
# Author: Moses Narrow
################################################################################
## Files:
#date_ineligible.csv  account of non rewarded visors
#date_rewardtxn0.csv  reward transaction CSV
#date_shares.csv      reward shares CSV
#date_stats.txt       statistical data
#date_ut.json         backup of uptime tracker data (7 days of UT data)
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
skywire cli ut --cfu "hist/${_wdate}_ut.json" | tee "hist/${_wdate}_ut.txt"
# New reward pool starts November 2nd, 2024
if [[ $(date +%s) -lt $(date -d "2024-11-02" +%s) ]]; then
  #echo "The date is before November 2nd, 2024."
  #v1.3.29 - two reward pools - exclude pool 2
  skywire cli rewards --utfile "hist/${_wdate}_ut.txt" -x "" -ed ${_wdate} -p log_backups  |  tee hist/${_wdate}_ineligible.csv
  skywire cli rewards --utfile "hist/${_wdate}_ut.txt" -x "" -20d ${_wdate} -p log_backups |  tee hist/${_wdate}_shares.csv
  skywire cli rewards --utfile "hist/${_wdate}_ut.txt" -x "" -10d ${_wdate} -p log_backups | tee hist/${_wdate}_rewardtxn0.csv
  skywire cli rewards --utfile "hist/${_wdate}_ut.txt" -x "" -12d ${_wdate} -p log_backups |  tee hist/${_wdate}_stats.txt
else
  #echo "The date is on or after November 2nd, 2024."
  #v1.3.29 - two reward pools - include pool 2
  skywire cli rewards --utfile "hist/${_wdate}_ut.txt" -ed ${_wdate} -p log_backups  |  tee hist/${_wdate}_ineligible.csv
  skywire cli rewards --utfile "hist/${_wdate}_ut.txt" -20d ${_wdate} -p log_backups |  tee hist/${_wdate}_shares.csv
  skywire cli rewards --utfile "hist/${_wdate}_ut.txt" -10d ${_wdate} -p log_backups | tee hist/${_wdate}_rewardtxn0.csv
  skywire cli rewards --utfile "hist/${_wdate}_ut.txt" -12d ${_wdate} -p log_backups |  tee hist/${_wdate}_stats.txt
fi
#return
exit 0
