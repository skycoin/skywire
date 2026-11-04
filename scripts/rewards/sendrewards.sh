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
## get the payout csv: the oldest undistributed day, without payouts to single
## reward addresses that have sent coins. Stop if it is not a clean csv.
payout="$(skywire dmsg curl $REWARD_SYS_URL/skycoin-rewards/payout -s $REWARD_WL_SK)"
if [[ -z "$payout" ]] || echo "$payout" | grep -vx 'Skycoin Address, Reward Amount' | grep -qvE '^[1-9A-HJ-NP-Za-km-z]+, *[0-9.]+ *$'; then
  echo "could not get a clean payout csv ; exiting"
  echo "$payout"
  exit 1
fi
skywire dmsg curl $REWARD_SYS_URL/reward -d "$(skycoin-cli createRawTransaction $WALLET_FILE -a $FROM_ADDRESS --csv <(echo "$payout" | tr -d ' ' | awk -F, '{printf "%s,%.3f\n", $1, int($2*1000)/1000}' |  grep -v '^,0.000$'))" -s $REWARD_WL_SK
