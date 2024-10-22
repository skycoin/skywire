#!/bin/bash
########## Skywire reward processing and calculation script reward.sh ##########
# Author: Moses Narrow
### Helpful snippets ###
# Cache the uptime tracker data for every day but today
# skywire-cli ut | tee -a uptimes.txt ; cat uptimes.txt | sort | uniq -c | sort -nr | awk -F " " '{print $2" "$3" "$4}' | grep -v $(date '+%Y-%m-%d') | tee uptimes.txt
# Get public keys meeting minimum 75% uptime for yesterday from the uptime tracker:
# skywire-cli ut | grep $(date --date="yesterday" +%Y-%m-%d) | cut -d " " -f1
# Get public keys meeting minimum 75% uptime for yesterday from cached uptime tracker data:
# grep $(date --date="yesterday" +%Y-%m-%d) uptimes.txt | cut -d " " -f1
# Check the collected surveys (processed survey backups) for the survey ; get the ip, skycoin address, public key, and architecture from the survey
# skywire-cli ut | grep $(date --date="yesterday" +%Y-%m-%d) | cut -d " " -f1 | while read _pk ; do printf "%s,%s,%s,%s\n" $(grep -s 'ip_address' log_backups/$_pk/node-info.json | cut -d '"' -f4) $(grep -s 'skycoin_address' log_backups/$_pk/node-info.json | cut -d '"' -f 4) ${_pk} $(grep -s 'go_arch' log_backups/$_pk/node-info.json | cut -d '"' -f 4)
# If there is no ip address, we can make a sudph transport to that public key in question and then check the visor's logging for the ip address
# Finally, the filter is applied for any entries which do not have all fields or are of ineligible architecture, and the results are written to a csv for further calculations
# | grep -v ",," | grep "\." | grep -v "amd64"  ; done | tee hist/${_wdate}_ip-sky-pk-arch.csv
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
### Alternative method when skywire-cli ut fails ###
#[[ "$_wdate" != "$(date --date="yesterday" +%Y-%m-%d)" ]] && (echo "$_wdate is not yesterday - hardcoded constraints exist in the next lines" ; exit 1)
#curl -s http://ut.skywire.skycoin.com/uptimes?v=v2 | jq -r | grep -Pv "up|down|version|on|daily|pct" | grep -v "$(for i in {2..40}; do date --date="$i day ago" +%Y-%m-%d; done)" | grep -v "$(date --date="tomorrow" +%Y-%m-%d)" | grep -v "$(date +%Y-%m-%d)" | awk '{printf "%s%s",sep,$0; sep= (/,$/ ? "" : "\n")} END{print ""}' | grep $_wdate | tr -d '}' | tr -d ',' | tr -d '"' | tr -d "p" | tr -d "k" | tr -d ':' | tr -s ' ' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' | awk -F ' ' '$NF >= 75 {print $0}' | cut -d " " -f1 | while read _i ; do [[ -f log_backups/$_i/node-info.json ]] &&  printf "%s,%s,%s,%s\n" $( [[ "$(grep -s 'ip_address' log_backups/$_i/node-info.json | cut -d '"' -f4)" != "" ]] && grep -s 'ip_address' log_backups/$_i/node-info.json | cut -d '"' -f4 || tail -n 500 /opt/skywire/local/custom/skywire.log  | grep $_i |  grep Resolved | cut -d '{' -f2 | cut -d ' ' -f1 | cut -d ':' -f1 | tail -n1) $(grep -s 'skycoin_address' log_backups/$_i/node-info.json | cut -d '"' -f 4) ${_i} $(grep -s 'go_arch' log_backups/$_i/node-info.json | cut -d '"' -f 4) | grep -v ",," | grep "\." | grep -v "amd64"  ; done | tee hist/${_wdate}_ip-sky-pk-arch.csv
# Related Manual method
#curl -s http://ut.skywire.skycoin.com/uptimes?v=v2 | jq -r | grep -Pv "up|down|version|on|daily|pct" | grep -v "$(for i in {2..40}; do date --date="$i day ago" +%Y-%m-%d; done)" | grep -v "$(date --date="tomorrow" +%Y-%m-%d)" | grep -v "$(date +%Y-%m-%d)" | awk '{printf "%s%s",sep,$0; sep= (/,$/ ? "" : "\n")} END{print ""}' | grep "$(date --date="yesterday" +%Y-%m-%d)" | tr -d '}' | tr -d ',' | tr -d '"' | tr -d "p" | tr -d "k" | tr -d ':' | tr -s ' ' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' | awk -F ' ' '$NF >= 75 {print $0}'
####################################################

####################################################
####### Cache method - not working robustly ########
#Append data to file, then sort & eliminate any duplicates ; caching the uptime data because `skywire-cli ut` sometimes returns nothing which messes up this script
#skywire-cli ut | tee -a uptimes.txt  >> /dev/null ; cat uptimes.txt | sort | uniq -c | sort -nr | awk -F " " '{print $2" "$3" "$4}' | grep -v $(date '+%Y-%m-%d') | tee uptimes1.txt ; mv uptimes1.txt uptimes.txt
#extract ip address and skycoin address from the backups / cache of the surveys collected by `skywire-cli log`
#grep ${_wdate} uptimes.txt | cut -d " " -f1 | while read _i ; do [[ -f log_backups/$_i/node-info.json ]] &&  printf "%s,%s,%s,%s\n" $( [[ "$(grep -s 'ip_address' log_backups/$_i/node-info.json | cut -d '"' -f4)" != "" ]] && grep -s 'ip_address' log_backups/$_i/node-info.json | cut -d '"' -f4 || tail -n 500 /opt/skywire/local/custom/skywire.log  | grep $_i |  grep Resolved | cut -d '{' -f2 | cut -d ' ' -f1 | cut -d ':' -f1 | tail -n1) $(grep -s 'skycoin_address' log_backups/$_i/node-info.json | cut -d '"' -f 4) ${_i} $(grep -s 'go_arch' log_backups/$_i/node-info.json | cut -d '"' -f 4) | grep -v ",," | grep "\." | grep -v "amd64"  ; done | tee hist/${_wdate}_ip-sky-pk-arch.csv
#skywire-cli ut | sort | uniq -c | sort -nr | grep 2023-04-24 | sed 's/^[ \t]*//' | cut -d " " -f2 | while read _i ; do [[ -f log_backups/$_i/node-info.json ]] &&  printf "%s,%s,%s,%s\n" $( [[ "$(grep -s 'ip_address' log_backups/$_i/node-info.json | cut -d '"' -f4)" != "" ]] && grep -s 'ip_address' log_backups/$_i/node-info.json | cut -d '"' -f4 || tail -n 500 /opt/skywire/local/custom/skywire.log  | grep $_i |  grep Resolved | cut -d '{' -f2 | cut -d ' ' -f1 | cut -d ':' -f1 | tail -n1) $(grep -s 'skycoin_address' log_backups/$_i/node-info.json | cut -d '"' -f 4) ${_i} $(grep -s 'go_arch' log_backups/$_i/node-info.json | cut -d '"' -f 4) | grep -v ",," | grep "\." | grep -v "amd64"  ; done | tee hist/${_wdate}_ip-sky-pk-arch.csv
####################################################
[[ ! -f "ut.txt" ]] && curl -s https://ut.skywire.skycoin.com/uptimes?v=v2 | tee 'ut.json' && go run ut.go ut.json | tee 'ut.txt'
[[ -f "ut.txt" ]] && [[ $(( $(date +%s) - $(date -r "ut.txt" +%s) )) -gt 3600 ]] && curl -s https://ut.skywire.skycoin.com/uptimes?v=v2 | tee 'ut.json' && go run ut.go ut.json | tee 'ut.txt' || echo "uptimes cache file recently updated, skipped fetching uptimes"
[[ ! -f "hist/${_wdate}_ut.json" ]] && cp  ut.json hist/${_wdate}_ut.json
[[ ! -f "hist/${_wdate}_ut.txt" ]] && cp  ut.txt hist/${_wdate}_ut.txt
##skywire cli rewards -ed ${_wdate} -u hist/${_wdate}_ut.txt -p log_backups  |  tee hist/${_wdate}_ineligible.csv
##skywire cli rewards -20d ${_wdate} -u hist/${_wdate}_ut.txt -p log_backups |  tee hist/${_wdate}_shares.csv
##skywire cli rewards -10d ${_wdate} -u hist/${_wdate}_ut.txt -p log_backups | grep -v "Skycoin Address, Reward Amount" | tee hist/${_wdate}_rewardtxn0.csv
##skywire cli rewards -12d ${_wdate} -u hist/${_wdate}_ut.txt -p log_backups |  tee hist/${_wdate}_stats.txt

# v1.3.28
#skywire cli rewards -ed ${_wdate} -p log_backups  |  tee hist/${_wdate}_ineligible.csv
#skywire cli rewards -20d ${_wdate} -p log_backups |  tee hist/${_wdate}_shares.csv

#v1.3.29 - two reward pools
skywire cli rewards -x "" -ed ${_wdate} -p log_backups  |  tee hist/${_wdate}_ineligible.csv
skywire cli rewards -x "" -20d ${_wdate} -p log_backups |  tee hist/${_wdate}_shares.csv

##skywire cli rewards -10d ${_wdate} -p log_backups | grep -v "Skycoin Address, Reward Amount" | tee hist/${_wdate}_rewardtxn0.csv
##skywire cli rewards  -n 386,amd64 -10d ${_wdate} -p log_backups | grep -v "Skycoin Address, Reward Amount" | tee hist/${_wdate}_rewardtxn0.csv
##skywire cli rewards -o 386,amd64 -10d ${_wdate} -p log_backups | grep -v "Skycoin Address, Reward Amount" | tee hist/${_wdate}_rewardtxn0.csv

# v1.3.28
#skywire cli rewards -10d $(date --date="yesterday" +%Y-%m-%d) -p log_backups | tee hist/${_wdate}_rewardtxn0.csv
#skywire cli rewards -12d ${_wdate} -p log_backups |  tee hist/${_wdate}_stats.txt

#v1.3.29 - two reward pools
skywire cli rewards -x "" -10d $(date --date="yesterday" +%Y-%m-%d) -p log_backups | tee hist/${_wdate}_rewardtxn0.csv
skywire cli rewards -x "" -12d ${_wdate} -p log_backups |  tee hist/${_wdate}_stats.txt
echo "cat hist/${_wdate}_rewardtxn0.csv"
echo "cat hist/${_wdate}_shares.csv"
#return
exit 0

################################### PREVIOUS REWARD CALCULATION SCRIPT BELOW ################################################
#get ip, skycoin address, & architexture of public keys which met minimum 75% uptime yesterday ; create csv with ip, skycoin address, public key, arch
skywire-cli ut | sort | uniq -c | sort -nr | grep $_wdate | sed 's/^[ \t]*//' | cut -d " " -f2 | while read _i ; do [[ -f log_backups/$_i/node-info.json ]] &&  printf "%s,%s,%s,%s\n" $(grep -s 'ip_address' log_backups/$_i/node-info.json | cut -d '"' -f4) $(grep -s 'skycoin_address' log_backups/$_i/node-info.json | cut -d '"' -f 4) ${_i} $(grep -s 'go_arch' log_backups/$_i/node-info.json | cut -d '"' -f 4) | grep -v ",," | grep "\." | grep -v "amd64"  ; done | tee hist/${_wdate}_ip-sky-pk-arch.csv

[[ ! -s "hist/${_wdate}_ip-sky-pk-arch.csv" ]] && echo "hist/${_wdate}_ip-sky-pk-arch.csv is empty, exiting" && exit 1
[[ ! -f "hist/${_wdate}_ip-sky-pk-arch.csv" ]] && echo "hist/${_wdate}_ip-sky-pk-arch.csv missing, exiting" && exit 1

#Calculate the daily rewards from the yearly total budget.
_yearlytotalrewards=408000
echo "annual rewards: 40800"
_thismonth="$(date +%B)"
_daysthismonth="$(cal $(date +%m) $(date +%Y) | awk 'NF {DAYS = $NF}; END {print DAYS}')"
printf ${_daysthismonth} ; printf ' days in the month of ' ; printf $_thismonth  ; echo
_monthreward="$(echo "(${_yearlytotalrewards} / $(seq  01 12 | while read _month ; do cal $_month $(date +%Y) | awk 'NF {DAYS = $NF}; END {print DAYS}' ; done | paste -sd+ | bc -l)) * "$(cal $(date +"%m %Y") | awk 'NF {DAYS = $NF}; END {print DAYS}') | bc -l)"
echo "this month's rewards $_monthreward"
_dayreward="$(echo "$_monthreward / $_daysthismonth" | bc -l )"
echo "daily reward pool $_dayreward"
_wdate="${_wdate/ /0}"

# Calculate valid shares
printf "limiting per-ip reward shares to 8  $_wdate...\n"
[[ -f hist/${_wdate}_overlimit.txt ]] && rm hist/${_wdate}_overlimit.txt #remove any existing file
[[ ! -f hist/${_wdate}_ip-sky-pk-arch.csv ]] && exit #file must exist


# List valid shares by ip address. Total valid shares is the sum of the counts .
[[ -f hist/${_wdate}_ip-addresses.txt ]] && rm hist/${_wdate}_ip-addresses.txt #remove old file
cat hist/${_wdate}_ip-sky-pk-arch.csv | cut -d "," -f1 | sort | uniq -c | sort -nr | cut -c 5-7 | while read _i ; do
[[ "${_i}" -gt "8" ]] && _i=8
echo "$_i" | tee -a hist/${_wdate}_ip-addresses.txt >> /dev/null
done

# sort unique ip addresses into a file with count of their occurence
cat hist/${_wdate}_ip-sky-pk-arch.csv | cut -d "," -f1 | sort | uniq -c | sort -nr | sed -e 's/^[[:space:]]*//' | tee hist/${_wdate}_overlimit.txt
cat hist/${_wdate}_overlimit.txt

# total valid shares is the sum of the counts in that file
_validshares="$(cat hist/${_wdate}_ip-addresses.txt | paste -sd+ | bc)"

#the following file is created to display the data onthe reward metrics interface
echo ${_validshares} | tee hist/${_wdate}_ip-count.txt

#the number of lines in the csv is the number of eligible visors
_rewardeligible="$(cat hist/${_wdate}_ip-sky-pk-arch.csv | wc -l)"

#count lines of the sorted list of ip addresses for a count of unique ip addresses
_totalip="$(cat hist/${_wdate}_ip-sky-pk-arch.csv | cut -d "," -f1 | sort | uniq -c | sort -nr | wc -l)"

# prints to the screen visors by ip address and then a count ; debugging
cat hist/${_wdate}_overlimit.txt | while read _i ; do  grep -F  "${_i##*' '}" hist/${_wdate}_ip-sky-pk-arch.csv ; grep -F  "${_i##*' '}" hist/${_wdate}_ip-sky-pk-arch.csv | wc -l   ; done

#print the calculations
echo "Total reward-eligible visors: $_rewardeligible"
echo "Total ip addresses: $_totalip"
echo "Valid shares: $_validshares"
echo "difference:" $(echo "${_rewardeligible} - ${_validshares}" | bc)

echo "checking for visor public keys and skycoin addresses of the ip addresses which exceed the 8 visor limit... "

[[ -f hist/${_wdate}_rewardshares.csv ]] && rm hist/${_wdate}_rewardshares.csv #remove this file if it exists

#check for visor public keys and skycoin addresses of the ip addresses which exceed the 8 visor limit and generate the corrected shares per skycoin address
#prints the results to the screen with the calculation, for debugging
cat hist/${_wdate}_ip-sky-pk-arch.csv  |  cut -d "," -f1 | sort | uniq | while read _i ; do
grep $_i hist/${_wdate}_ip-sky-pk-arch.csv | sort | uniq | cut -d"," -f2 | while read _j ; do
echo $_i $_j ; done | sort | uniq -c | sort -nr | sed -e 's/^[[:space:]]*//' ; done | sort -t " "  -k2 -k3 -k1 | while read _i ; do
echo $_i
_ip=${_i}
_sky=${_i##*' '}
_ip=${_i%' '*}
_ip=${_ip#*' '}
_total="$(grep -F ${_ip} hist/${_wdate}_ip-sky-pk-arch.csv  | cut -d ',' -f2 | sort | uniq -c | sort -nr | sed -e 's/^[[:space:]]*//' | cut -d' ' -f1 | paste -sd+ | bc)"
[[ "${_total}" -gt "8" ]] && echo "(( ((8 / ${_total})) *  ${_i%%' '*} )) = "$(echo "(( ((8 / ${_total})) *  ${_i%%' '*} ))" | bc -l) || echo "${_i%%' '*}"
printf "ip: $_ip,\n"
printf "total: $_total,"
[[ "${_total}" -gt "8" ]] &&  _share=$(printf "%.3f" $(echo "(( ((8 / ${_total})) *  ${_i%%' '*} ))" | bc -l)) || _share=${_i%%' '*}
printf "share: $_share\n"
echo "$_sky,$_share" | tee -a hist/${_wdate}_rewardshares.csv   >> /dev/null
done

cat hist/${_wdate}_rewardshares.csv |  cut -d "," -f1 | sort | uniq | while read _i ; do printf "$_i,"  ; printf "%.3f\n" $(grep $_i hist/${_wdate}_rewardshares.csv | awk -F , '{print $2}' | xargs | sed -e 's/\ /+/g' | bc) ; done | sort -n -t"," -k2 | tee hist/${_wdate}_rewardtxn00.csv   >> /dev/null
echo "dayrewards / validshares = ${_dayreward} / ${_validshares}"
_multiplier=$( echo "${_dayreward} / ${_validshares}" | bc -l )
echo "_multiplier=${_multiplier}"
printf "creating reward transaction for ${_wdate}\n"

# per visor reward multiplied by reward share = reward amount for each address
[[ -f hist/${_wdate}_rewardtxn0.csv ]] && rm hist/${_wdate}_rewardtxn0.csv
cat hist/${_wdate}_rewardtxn00.csv | while IFS="," read _sky _share ; do

printf "${_sky},%.3f\n" $( echo "${_share}*${_multiplier}" | bc -l ) | tee -a hist/${_wdate}_rewardtxn0.csv  >> /dev/null
done

_addresscount="$( cat hist/${_wdate}_rewardtxn0.csv | wc -l )"
echo "addresscount $_addresscount"
_minincrement=$(printf "%.3f" $( bc -l <<< "0.001 * ${_addresscount}" ))
echo "minincrement $_minincrement"
_rtxtotal=$(awk -F ',' '{Total=Total+$2} END{print Total}' hist/${_wdate}_rewardtxn0.csv)
echo "rtxtotal $_rtxtotal"

#functions to adjust the total to account for rounding errors
_decreasetotal() {
  _diff=$(printf "%.3f" $( bc -l <<< "$_rtxtotal - $_dayreward" ))
  echo "difference $_diff"
_subtractfromeach=$(printf "%.3f" $( bc -l <<< "$_diff / $_addresscount" ))
if (( $(echo "$_diff < $_minincrement" | bc -l) )) ; then
  _subtractfromeach="0.001"
fi
echo "Subtact from each: $_subtractfromeach"
cat hist/${_wdate}_rewardtxn0.csv | while read _i ; do
  printf "${_i%%,*},%.3f\n" $( bc -l <<< "${_i##*,} - ${_subtractfromeach}" ) | tee -a hist/${_wdate}_rewardtxn01.csv  >> /dev/null
done
mv hist/${_wdate}_rewardtxn01.csv hist/${_wdate}_rewardtxn0.csv
_rtxtotal=$(awk -F ',' '{Total=Total+$2} END{print Total}' hist/${_wdate}_rewardtxn0.csv)
echo "rtxtotal $_rtxtotal"
}

_increasetotal() {
_diff=$(printf "%.3f" $( bc -l <<< "$_rtxtotal - $_dayreward" ))
  echo "difference $_diff"
_addtoeach=$(printf "%.3f" $( bc -l <<< "$_diff / $_addresscount" ))
if (( $(echo "$_diff < $_minincrement" | bc -l) )) ; then
  _addtoeach="0.001"
fi
echo "Add to each: $_addtoeach"
cat hist/${_wdate}_rewardtxn0.csv | while read _i ; do
  printf "${_i%%,*},%.3f\n" $( bc -l <<< "${_i##*,} + ${_addtoeach}" ) | tee -a hist/${_wdate}_rewardtxn01.csv  >> /dev/null
done
mv hist/${_wdate}_rewardtxn01.csv hist/${_wdate}_rewardtxn0.csv
_rtxtotal=$(awk -F ',' '{Total=Total+$2} END{print Total}' hist/${_wdate}_rewardtxn0.csv)
echo "rtxtotal $_rtxtotal"
}

#adjust the total if necessary in this sequence of checks
if (( $(echo "$_rtxtotal > $_dayreward" | bc -l) )) ; then
echo "total rewards exceeds alotted amount $_dayreward"
_decreasetotal
fi

if (( $(echo "$_rtxtotal < $_dayreward" | bc -l) )) ; then
echo "total rewards less than alotted amount $_dayreward"
_increasetotal
fi

if (( $(echo "$_rtxtotal > $_dayreward" | bc -l) )) ; then
echo "total rewards exceeds alotted amount $_dayreward"
_decreasetotal
fi
#should always end at or below the daily total maximum
if (( $(echo "$_rtxtotal > $_dayreward" | bc -l) )) ; then
echo "total rewards exceeds alotted amount $_dayreward"
_decreasetotal
fi

#reward.conf contains the following envs
#WALLET_FILE="/path/to/wallet_file.wlt"
#FROM_ADDRESS="<skycoin-address>"
source reward.conf
echo
echo "creating script to broadcast reward transactions..."
echo
#the script is created and printed to the screen so its contents can be verified
echo '#!/usr/bin/bash'  | tee r1.sh #>> /dev/null
echo 'if systemctl is-active --quiet skywire-reward.service > /dev/null ; then echo "skywire-reward service active, not executing" ; exit 1 ; fi'  | tee -a r1.sh #>> /dev/null
echo "[ ! -f hist/${_wdate}.txt ] && [[ ! -z \$RPC_ADDR ]] && skycoin-cli broadcastTransaction \$(skycoin-cli createRawTransaction $WALLET_FILE -a $FROM_ADDRESS --csv hist/${_wdate}_rewardtxn0.csv) | tee -a transaction_tests.txt && exit ; [[ -z \$RPC_ADDR ]] &&  skycoin-cli broadcastTransaction \$(skycoin-cli createRawTransaction $WALLET_FILE -a $FROM_ADDRESS --csv hist/${_wdate}_rewardtxn0.csv) | tee -a transactions0.txt | tee hist/${_wdate}.txt " | tee -a r1.sh #>> /dev/null
[ ! -f hist/${_wdate}.txt ] && chmod +x r1.sh
echo
#prompt to broadcast the transaction, allows the invocation of the script which creates and broadcasts the transaction to be copied and pasted
echo "broadcast with"
echo "./r1.sh"
#the test transaction is broadcast on the tesla fiberchain.
echo "broadcast test transaction with"
echo "RPC_ADDR='http://127.0.0.1:6417/' ./r1.sh"
echo "rewards total"
awk -F "," '{Total=Total+$2} END{print Total}' hist/${_wdate}_rewardtxn0.csv
