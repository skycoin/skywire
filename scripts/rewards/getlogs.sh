#!/usr/bin/bash
timeout 30.0m unbuffer skywire-cli log --minv v1.3.19 -s $(tail -n1 survey-wl.conf) | tee skywire-cli-log.txt
#echo -e "skywire survey and transport log collection $(date)\n\n$(cat skywire-cli-log.txt)\n"
 echo -e "skywire survey and transport log collection $(date)\n\n$(cat skywire-cli-log.txt)\n" | tee skywire-cli-log0.txt >> /dev/null
echo "finished "$(date) | tee -a skywire-cli-log0.txt
mv skywire-cli-log0.txt skywire-cli-log.txt

#remove empty files and dirs
find log_collecting/*/ -empty -type f -delete && printf "removed empty files... \n"  || true
find log_collecting/*/ -type f -size 19c -delete && printf "removed files with http 404 errors... \n" || true
find log_collecting/*/ -type f -size 18c -delete && printf "removed files with http 404 errors... \n" || true
find log_collecting/* -empty -type d -delete && printf "removed empty dirs... \n" || true
find log_backups/*/ -empty -type f -delete && printf "removed empty files... \n" || true
find log_backups/*/ -type f -size 19c -delete && printf "removed files with http 404 errors... \n" || true
find log_backups/* -empty -type d -delete && printf "removed empty dirs... \n" || true
#for ((i=1; i<=($(date -d "$(date +%m)/$(date +%d)/$(date +%Y)" +%j)); i++)); do find log_collecting/*/ -type f -name $(date -d "01/01/2023 +$((i-1)) days" +'%Y-%m-%d' | awk '{print $0}').csv | while read _file ; do [[ $(head -n 1 $_file) == *"tp_id,recv,sent,time_stamp"* ]] && sed -i '1d' $_file ; done ; done
#for ((i=1; i<=($(date -d "$(date +%m)/$(date +%d)/$(date +%Y)" +%j)); i++)); do find log_backups/*/ -type f -name $(date -d "01/01/2023 +$((i-1)) days" +'%Y-%m-%d' | awk '{print $0}').csv | while read _file ; do [[ $(head -n 1 $_file) == *"tp_id,recv,sent,time_stamp"* ]] && sed -i '1d' $_file ; done ; done
#for ((i=1; i<=($(date -d "$(date +%m)/$(date +%d)/$(date +%Y)" +%j)); i++)); do find log_collecting/*/ -type f -name $(date -d "01/01/2023 +$((i-1)) days" +'%Y-%m-%d' | awk '{print $0}').csv -print | xargs grep -l "404 page not found" | parallel rm
#for ((i=1; i<=($(date -d "$(date +%m)/$(date +%d)/$(date +%Y)" +%j)); i++)); do find log_backups/*/ -type f -name $(date -d "01/01/2023 +$((i-1)) days" +'%Y-%m-%d' | awk '{print $0}').csv | xargs grep -l "404 page not found" | parallel rm

find log_collecting/*/$(date +'%Y-%m-%d').csv -type f -print | while read _file ; do [[ $(head -n 1 $_file) == *"tp_id,recv,sent,time_stamp"* ]] && sed -i '1d' $_file ; done || true
find log_collecting/*/$(date --date="yesterday" +'%Y-%m-%d').csv -type f -print | while read _file ; do [[ $(head -n 1 $_file) == *"tp_id,recv,sent,time_stamp"* ]] && sed -i '1d' $_file ; done || true
find log_backups/*/$(date +'%Y-%m-%d').csv -type f -print | while read _file ; do [[ $(head -n 1 $_file) == *"tp_id,recv,sent,time_stamp"* ]] && sed -i '1d' $_file ; done || true
find log_backups/*/$(date --date="yesterday" +'%Y-%m-%d').csv -type f -print | while read _file ; do [[ $(head -n 1 $_file) == *"tp_id,recv,sent,time_stamp"* ]] && sed -i '1d' $_file ; done || true
grep -l "404 page not found" log_collecting/*/$(date +'%Y-%m-%d').csv | xargs rm -f || true
grep -l "404 page not found" log_collecting/*/$(date --date="yesterday" +'%Y-%m-%d').csv | xargs rm -f || true
grep -l "404 page not found" log_backups/*/$(date +'%Y-%m-%d').csv | xargs rm -f || true
grep -l "404 page not found" log_backups/*/$(date --date="yesterday" +'%Y-%m-%d').csv | xargs rm -f || true


#check / decrypt surveys
printf "checking surveys... \n"
find log_collecting/*/node-info.json -type f -print | xargs grep -l "404 page not found" | parallel rm || true
find log_collecting/*/node-info.json -type f -print | xargs grep -l "Not Found" | parallel rm || true
find log_collecting/*/node-info.json -type f -print | xargs grep -l "PGP MESSAGE" | parallel rm  || true #xargs -I {} ./decrypt.sh {} #./decrypt.sh
find log_backups/*/node-info.json -type f -print | xargs grep -l "404 page not found" | parallel rm || true
find log_backups/*/node-info.json -type f -print | xargs grep -l "Not Found" | parallel rm || true
find log_backups/*/node-info.json -type f -print | xargs grep -l "PGP MESSAGE" | parallel rm || true #xargs -I {} ./decrypt.sh {} #./decrypt.sh

printf "checking tp logs... \n"
[[ -f log_collecting/*/$(date +'%Y-%m-%d').csv ]] && find log_collecting/*/$(date +'%Y-%m-%d').csv -type f -print | xargs grep -l "404 page not found" | parallel rm || true
[[ -f log_collecting/*/$(date --date="yesterday" +'%Y-%m-%d').csv ]] && find log_collecting/*/$(date --date="yesterday" +'%Y-%m-%d').csv -type f -print | xargs grep -l "404 page not found" | parallel rm || true
[[ -f log_collecting/*/$(date +'%Y-%m-%d').csv ]] && find log_collecting/*/$(date +'%Y-%m-%d').csv -type f -print | xargs grep -l "Not Found" | parallel rm || true
[[ -f log_collecting/*/log_collecting/*/$(date --date="yesterday" +'%Y-%m-%d').csv ]] && find log_collecting/*/$(date --date="yesterday" +'%Y-%m-%d').csv -type f -print | xargs grep -l "Not Found" | parallel rm || true
[[ -f log_collecting/*/$(date +'%Y-%m-%d').csv ]] && find log_backups/*/$(date +'%Y-%m-%d').csv -type f -print | xargs grep -l "404 page not found" | parallel rm || true
[[ -f log_collecting/*/log_collecting/*/$(date --date="yesterday" +'%Y-%m-%d').csv ]] && find log_backups/*/$(date --date="yesterday" +'%Y-%m-%d').csv -type f -print | xargs grep -l "404 page not found" | parallel rm  || true
[[ -f log_collecting/*/$(date +'%Y-%m-%d').csv ]] && find log_backups/*/$(date +'%Y-%m-%d').csv -type f -print | xargs grep -l "Not Found" | parallel rm || true
[[ -f log_collecting/*/log_collecting/*/$(date --date="yesterday" +'%Y-%m-%d').csv ]] && find log_backups/*/$(date --date="yesterday" +'%Y-%m-%d').csv -type f -print | xargs grep -l "Not Found" | parallel rm || true

#back up the collected files
rsync -r log_collecting/ log_backups || true
[[ -f log_backups/*/*~ ]] && rm log_backups/*/*~ || true

#update the addresses in the csv
#[[ -f ip-sky-pk-new.csv ]] && rm ip-sky-pk-new.csv
#find log_backups/*/node-info.json -type f | parallel "./newsky.sh {}"
#cat ip-sky-pk.csv | parallel "./updsky.sh {}"
#mv ip-sky-pk-new.csv ip-sky-pk.csv
#cat skywire-cli-log.txt
