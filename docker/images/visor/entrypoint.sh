#!/bin/sh

cmd="$(echo "$1" | tr -d '[:space:]')"

case "$cmd" in
skywire-visor)
  ./"$cmd" -- "$@"
  ;;
skywire-cli)
  /bin/skywire-cli -- "$@"
  ;;
*)
  /bin/apps/"$cmd" -- "$@"
  ;;
esac
