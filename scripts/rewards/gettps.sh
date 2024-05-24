#!/usr/bin/bash
#refresh the cached uptime tracker data at /tmp/ut.json
skywire cli ut -s
#online status alone is too broad of a qualifier for surveying. But there are sometimes errors with the dmsghttp logserver that wouldn't otherwise cause the transport setup node to fail. Some nodes may be offline though UT shows online and vice versa.
find ./log_backups/ -type d -print0 | xargs -0 -n1 basename | while read _pk ; do
mkdir -p tp_setup/${_pk}
done
printf "%s\n%s\n%s\n" $(jq '.[] | select(.version=="v1.3.21" and .on==true ) | .pk' /tmp/ut.json | tr -d '"') $(find ./log_backups/ -type d -print0 | xargs -0 -n1 basename) $(find log_backups/ -type f -name "health.json" -mmin -60 | cut -d '/' -f2) | sort | uniq -c | sort -nr | grep -v "1 " | sed 's/      [23] //g' | tac | while read _pk ; do
timeout 3.0m parallel -j 25 'skywire svc tps list -1 {} 2>&1 | tee tp_setup/{}/tp.json'
done
skywire cli ut -s
find ./log_backups/ -type d -print0 | xargs -0 -n1 basename | while read _pk ; do
mkdir -p tp_setup/${_pk}
done
printf "%s\n%s\n%s\n" $(jq '.[] | select(.version=="v1.3.21" and .on==true ) | .pk' /tmp/ut.json | tr -d '"') $(find ./log_backups/ -type d -print0 | xargs -0 -n1 basename) $(find log_backups/ -type f -name "health.json" -mmin -60 | cut -d '/' -f2) | sort | uniq -c | sort -nr | grep -v "1 " | sed 's/      [23] //g' | while read _pk ; do
timeout 3.0m parallel -j 25 'skywire svc tps list -1 {} 2>&1 | tee tp_setup/{}/tp.json'
done
skywire cli ut -s
printf "%s\n%s\n" $(jq '.[] | select(.version=="v1.3.21" and .on==true ) | .pk' /tmp/ut.json | tr -d '"') $(find tp_setup/ -type f -name "tp.json" -mmin -60 | cut -d '/' -f2) | sort | uniq -c | sort -nr | sed 's/      [12] //g' | while read _pk ; do
timeout 3.0m parallel -j 25 'jq -e . tp_setup/{}/tp.json  > /dev/null 2>&1 || skywire svc tps list -1 {} 2>&1 | tee tp_setup/{}/tp.json'
done
echo "cleaning up"
find ./tp_setup/ -type f -mtime +2 -delete
find ./tp_setup/ -type d -empty -delete
