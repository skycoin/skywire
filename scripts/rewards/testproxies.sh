#!/bin/bash
_testpk="$(skywire cli visor pk || exit 1)"
skywire-cli proxy stop --all > /dev/null ; skywire cli tp rm -a || exit 1
skywire cli ut -s  > /dev/null
[[ -f /tmp/proxy1234567.json ]] && rm /tmp/proxy1234567.json > /dev/null
#echo '00pk, time, time_namelookup, time_connect, time_appconnect, time_pretransfer, time_redirect, time_starttransfer, time_total, ip_address, latitude, longitude, postal_code, continent_code, country_code, country_name, region_code, region_name, province_code, province_name, city_name, timezone' | tee proxy_test/proxies-tmp.csv
echo '00pk, time_now, time_total' | tee proxy_test/proxies-tmp.csv
awk -F',' 'NF == 3 {print $0}' proxy_test/proxies.csv | grep -v "00pk" | cut -d "," -f1 | sort | uniq -c | sort -nr | sed 's/      [0-9]\+ //g' | sort | tac | while read _pk ; do grep -m1 "$_pk" proxy_test/proxies.csv | tee -a proxy_test/proxies-tmp.csv ; done ; mv proxy_test/proxies-tmp.csv proxy_test/proxies.csv
#_res="$(curl -o /tmp/proxy123456.json -w "%{time_namelookup}s, %{time_connect}s, %{time_appconnect}s, %{time_pretransfer}s, %{time_redirect}s, %{time_starttransfer}s, %{time_total}s" -sL http://ip.skycoin.com/ || exit 1)"
#[[ "$(grep -m1 "027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed" proxy_test/proxies.csv)" == "" ]] && echo "027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed,$(date "+%D_%T"),${_res//$'\n'/},$(jq -r '[.ip_address, .latitude, .longitude, .postal_code, .continent_code, .country_code, .country_name, .region_code, .region_name, .province_code, .province_name, .city_name, .timezone] | @csv' /tmp/proxy123456.json | tr -d '"')" | tee -a proxy_test/proxies.csv
_res="$(curl -o /tmp/proxy123456.json -w "%{time_total}s" -sL http://ip.skycoin.com/ || exit 1)"
[[ "$(grep -m1 "N/A" proxy_test/proxies.csv)" == "" ]] && echo "N/A,$(date "+%D_%T"),${_res//$'\n'/}" | tee -a proxy_test/proxies.csv
[[ -f /tmp/proxy1234567.json ]] && rm /tmp/proxy1234567.json > /dev/null  || true
#skywire cli tp add -t dmsg $_testpk || exit 1
#timeout 11s skywire cli proxy start -k $_testpk -t 10 || exit 1
#[[ $(skywire cli proxy status | grep -m1 "Status:" ) != "Status: running" ]] && exit 1
#  _res="$(timeout 10s curl -o /tmp/proxy123456.json -w "%{time_namelookup}s, %{time_connect}s, %{time_appconnect}s, %{time_pretransfer}s, %{time_redirect}s, %{time_starttransfer}s, %{time_total}s" -sLx socks5h://127.0.0.1:1080 http://ip.skycoin.com/)" &&
#  echo "$_pk,$(date "+%D_%T"),${_res//$'\n'/},$(jq -r '[.ip_address, .latitude, .longitude, .postal_code, .continent_code, .country_code, .country_name, .region_code, .region_name, .province_code, .province_name, .city_name, .timezone] | @csv' /tmp/proxy123456.json | tr -d '"')" | tee -a proxy_test/proxies.csv
#_res="$(timeout 10s curl -o /tmp/proxy123456.json -w "%{time_total}s" -sLx socks5h://127.0.0.1:1080 http://haltingstate.net/204)" &&
#echo "self_transport,$(date "+%D_%T"),${_res//$'\n'/}" | tee -a proxy_test/proxies.csv
#skywire-cli proxy stop --all > /dev/null ; skywire cli tp rm -a || exit 1
printf "%s\n%s\n%s\n" $(jq '.[] | select(.version=="v1.3.21" and .on==true ) | .pk' /tmp/ut.json | tr -d '"') $(skywire cli proxy list -v v1.3.21) $(find log_backups/ -type f -name "health.json" | cut -d '/' -f2) | sort | uniq -c | sort -nr | grep "3 " | sed 's/      3 //g' | grep -v "${_testpk/"\n"/}" | tac > proxy_test/list.txt
cat proxy_test/list.txt | grep -vFf <(tail -n+2 proxy_test/proxies.csv | awk -F',' 'NF == 22 {print $1}') | shuf -n 25 | sort | while read _pk ; do
   timeout 30s parallel -j 4 "skywire svc tps add -z http://127.0.0.1:8078 -t dmsg -1 {} -2 ${_testpk}"
done
skywire cli tp | tr -s " " | cut -d " " -f3 | sort | while read _pk ; do
(
  skywire cli proxy stop --all > /dev/null  || true
  [[ -f /tmp/proxy1234567.json ]] && rm /tmp/proxy1234567.json > /dev/null  || true
  [[ $(skywire cli tp  | grep "$_pk" ) != *"$_pk"* ]] && echo "pk not found in transports $_pk" && exit #continue
  timeout 11s skywire cli proxy start -k $_pk -t 10 || exit # continue
  [[ $(skywire cli proxy status | grep -m1 "Status:" ) != "Status: running" ]] && exit # continue
#  _res="$(timeout 10s curl -o /tmp/proxy123456.json -w "%{time_namelookup}s, %{time_connect}s, %{time_appconnect}s, %{time_pretransfer}s, %{time_redirect}s, %{time_starttransfer}s, %{time_total}s" -sLx socks5h://127.0.0.1:1080 http://ip.skycoin.com/)" &&
#  echo "$_pk,$(date "+%D_%T"),${_res//$'\n'/},$(jq -r '[.ip_address, .latitude, .longitude, .postal_code, .continent_code, .country_code, .country_name, .region_code, .region_name, .province_code, .province_name, .city_name, .timezone] | @csv' /tmp/proxy123456.json | tr -d '"')" | tee -a proxy_test/proxies.csv
  _res="$(timeout 10s curl -o /tmp/proxy123456.json -w "%{time_total}s" -sLx socks5h://127.0.0.1:1080 http://haltingstate.net/204)" &&
  echo "$_pk,$(date "+%D_%T"),${_res//$'\n'/}" | tee -a proxy_test/proxies.csv
) & _pid="$!"
( sleep 25 ; [[ $(kill -0 "${_pid}" 2>/dev/null) ]] && kill "${_pid}" || true ) &
wait "${_pid}"
done
skywire cli ut -s  > /dev/null
printf "%s\n%s\n%s\n" $(jq '.[] | select(.version=="v1.3.21" and .on==true ) | .pk' /tmp/ut.json | tr -d '"') $(skywire cli proxy list -v v1.3.21) $(find log_backups/ -type f -name "health.json" | cut -d '/' -f2) | sort | uniq -c | sort -nr | grep "3 " | sed 's/      3 //g'  | grep -v "${_testpk}" | tac > proxy_test/list.txt
[[ $(cat proxy_test/list.txt | grep -vFf <(tail -n+2 proxy_test/proxies.csv | awk -F',' 'NF == 3 {print $1}') | wc -l) -gt 0 ]] && ./testproxies.sh
