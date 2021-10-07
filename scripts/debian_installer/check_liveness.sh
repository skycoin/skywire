#!/usr/bin/env sh

remote_pk=$1
tp_type="dmsg"
rpc="--rpc localhost:3435"
addr="localhost:8001"
chat="http://$addr/message"

cli_path="/release/skywire-cli"
ls_tp="$cli_path $rpc visor ls-tp"

add_tp="$cli_path $rpc visor add-tp $remote_pk --type $tp_type"
check_tp=$($ls_tp | awk '{if($3 == "'"$remote_pk"'" && $5 == "true"){print}}' | wc -l)

if [ "$check_tp" -eq 0 ]; then
  $add_tp
  # shellcheck disable=SC2181
  if [ $? -ne 0 ]; then
    exit 1
  fi
  sleep 5
fi

curl --data '{"recipient":"'"$remote_pk"'", "message":"test"}' -X POST "$chat"
