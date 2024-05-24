#!/usr/bin/bash
## source configuration file
source sendrewards.conf
## check that skycoin wallet is running
skycoin-cli status ; [[ $? -ne 0 ]] && (echo 'skycoin wallet not running ; exiting' && exit 1)
## check that skywire-reward.service isn't running to ensure rewards calculation not ongoing
skywire dmsg curl $REWARD_SYS_URL/skycoin-rewards/s | jq -r '.'
[[ "$(skywire dmsg curl $REWARD_SYS_URL/skycoin-rewards/s | jq -r '.active')" == "active"* ]] && echo "skywire-rewards are calculating - reward service active ; not executing distribution to avoid partial file download" && exit 0
## preview current reward statistics
source sendrewards.conf ; skywire dmsg curl  $REWARD_SYS_URL/$(skywire dmsg curl $REWARD_SYS_URL/skycoin-rewards/csv | sed 's/_rewardtxn0.csv/_stats.txt/g')
## allow to accept or decline
read -n 1 -p "Send rewards? [Y/n]: " user_input
echo
if [[ "$user_input" != "Y" && "$user_input" != "y" ]]; then
  echo "exiting"
  exit 0
fi
## get link to the latest CSV
#skywire dmsg curl $REWARD_SYS_URL/skycoin-rewards/csv -s $REWARD_WL_SK
## get reward csv data
# skywire dmsg curl  $REWARD_SYS_URL/$(skywire dmsg curl $REWARD_SYS_URL/skycoin-rewards/csv -s $REWARD_WL_SK) -s $REWARD_WL_SK | tr -d ' ' | awk -F, '{printf "%s,%.3f\n", $1, int($2*1000)/1000}' |  grep -v '^,0.000$'
skywire dmsg curl $REWARD_SYS_URL/reward -d "$(skycoin-cli createRawTransaction $WALLET_FILE -a $FROM_ADDRESS --csv <(skywire dmsg curl  $REWARD_SYS_URL/$(skywire dmsg curl $REWARD_SYS_URL/skycoin-rewards/csv -s $REWARD_WL_SK) -s $REWARD_WL_SK | tr -d ' ' | awk -F, '{printf "%s,%.3f\n", $1, int($2*1000)/1000}' |  grep -v '^,0.000$'))" -s $REWARD_WL_SK
