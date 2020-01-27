# This script needs to be `source`d from bash-compatible shell
# E.g. `source ./integration/generic/env-vars.sh` or `. ./integration/generic/env-vars.sh`
export PK_A=$(jq -r ".node.static_public_key" ./integration/generic/visorA.json)
export RPC_A=$(jq -r ".interfaces.rpc" ./integration/generic/visorA.json)
export PK_B=$(jq -r ".node.static_public_key" ./integration/intermediary-nodeB.json)
export RPC_B=$(jq -r ".interfaces.rpc" ./integration/intermediary-nodeB.json)
export PK_C=$(jq -r ".node.static_public_key" ./integration/generic/visorC.json)
export RPC_C=$(jq -r ".interfaces.rpc" ./integration/generic/visorC.json)

export CHAT_A=http://localhost:8000/message
export CHAT_C=http://localhost$(jq -r '.apps [] |select(.app=="skychat")| .args[1] ' ./integration/generic/visorC.json)/message

alias CLI_A='./skywire-cli --rpc $RPC_A'
alias CLI_B='./skywire-cli --rpc $RPC_B'
alias CLI_C='./skywire-cli --rpc $RPC_C'

export MSGD=https://dmsg.discovery.skywire.skycoin.com
export TRD=https://transport.discovery.skywire.skycoin.com
export RF=https://routefinder.skywire.skycoin.com

echo PK_A: $PK_A
echo PK_B: $PK_B
echo PK_C: $PK_C

echo CHAT_A: $CHAT_A
echo CHAT_C: $CHAT_C
