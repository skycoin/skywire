#!/usr/bin/bash
## Replaced by `skywire cli rewards send`, which reads WALLET_FILE and REWARD_SYS_URL from the file SKYENV names.
SKYENV="${SKYENV:-sendrewards.conf}" exec skywire cli rewards send "$@"
