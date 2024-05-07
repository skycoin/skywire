#!/usr/bin/bash
[[ ! -d tp_setup ]] && mkdir tp_setup
skywire cli ut -s
#jq '.[] | select(.version=="v1.3.21" and .on==true ) | .pk' /tmp/ut.json | tr -d '"' | while read _pk ; do
printf "%s\n%s\n" $(jq '.[] | select(.version=="v1.3.21" and .on==true ) | .pk' /tmp/ut.json | tr -d '"') $(find ./log_backups/ -type d -print0 | xargs -0 -n1 basename) | sort | uniq -c | sort -nr | grep "2 " | sed 's/      2 //g' | tac | while read _pk ; do
  mkdir -p tp_setup/${_pk}
skywire svc tps list -1 ${_pk} 2>&1 | tee tp_setup/${_pk}/tp.json
done
find ./tp_setup/ -type f -mtime +2 -delete
find ./tp_setup/ -type d -empty -delete
