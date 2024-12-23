#!/usr/bin/env bash

set -x

## PID of skywire-visor
pid=0

default_config_path=/opt/skywire/config.json

gen_default_config() {
  echo "no config found, generating one...."
  /release/skywire cli config gen -o "$default_config_path" -ri
  sed -i 's/localhost//g' "$default_config_path"
  echo "config generated"
}

sigint_handler() {
  if [ $pid -ne 0 ]; then
    kill -INT "$pid"
    wait "$pid"
  fi
	exit 130;
}

trap 'kill ${!}; sigint_handler' INT

cmd="$(echo "$2" | tr -d '[:space:]')"
shift 1

case "$cmd" in
visor)
  case "$1" in
  -c)
    /release/skywire "$cmd" "$@" &
    ;;
  *)
    gen_default_config
    /release/skywire "$cmd" -c "$default_config_path" "$@" &
    ;;
  esac
  ;;
skywire-cli)
  /release/skywire "$cmd" "$@" &
  ;;
esac

pid="$!"

while true
do
	wait ${!}
done
