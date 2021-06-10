#!/bin/sh

## PID of skywire-visor
pid=0

default_config_path=/opt/skywire/config.json

gen_default_config() {
  echo "no config found, generating one...."
  /release/skywire-cli visor gen-config -o "$default_config_path" -r --is-hypervisor
  sed -i 's/localhost//g' "$default_config_path"
  echo "config generated"
}

sigint_handler() {
  if [ $pid -ne 0 ]; then
    kill -SIGINT "$pid"
    wait "$pid"
  fi
}

trap 'kill ${!}; sigint_handler' INT TERM

cmd="$(echo "$1" | tr -d '[:space:]')"
shift 1

case "$cmd" in
skywire-visor)
  case "$1" in
  -c)
    /release/"$cmd" "$@" &
    pid="$!"
    ;;
  *)
    gen_default_config
    /release/"$cmd" -c "$default_config_path" "$@"
    pid="$!"
    ;;
  esac
  ;;
skywire-cli)
  /release/"$cmd" "$@"
  pid="$!"
  ;;
esac
