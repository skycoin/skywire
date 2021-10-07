#!/usr/bin/env bash

source "${0%/*}"/env.sh

file_name="$HOME/.ssh/authorized_keys"

ssh_keys=(
    # Evan
    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJsFNFJS97naMNkpsRRg6dPN3IYR3IYiwyWqXXk+y5Ow evanlinjin@Evans-MBP.local" # MBP 15
    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIK6Zeidb6bydieD3VdR5w9E/fuNfhIrFDf6xVZmh0qrM mail@evanlinjin.me" # Dan A4

    # Rudi
    "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCticlmijxrbtnVq2+gXqEGMiK/w0RCRMdMnWdTWHFnCBFj/S4lWHeGaQ+cSPq7eqzZgUAuZ2bmGdg2kBxN6Otecx45AFL+CrmydKDog+hWTMWGZsLc/MKe8+/g9nZ5fukkbTjqeaKWrxZElbVlSrqPl8Apbn9pzL0Z3gii2f9I3oDQWHRKBoYyy7ve6IFaf1G/HdIhDuZZGuJX1JAL9Gpo3DdHmG6KkkcXwuhm0VJm3ye/GfORUkyAKPgr6V4/K8W5zaFr+OOn2EmUXSGyDRG44mm3/OdmQNhxU42zUtD2RFPTCMj7iAucbAb74JXLebRM7//bi4F/XsGnU8WT4Hn7 sigmundus@sigmundus-ThinkPad-E460"
    "ecdsa-sha2-nistp521 AAAAE2VjZHNhLXNoYTItbmlzdHA1MjEAAAAIbmlzdHA1MjEAAACFBAH6Zp+rHR1a5S5VSngn96UI+kjt7eanjqQW/LXQ8s71e/my6N5p3sdNiKweKF5IqMM/4defcpiIuMUMdeSrf6ee2gB/G363ICsEFMJurM4qkT7ZTN2rZBFHWd1UEnA6w15V51SR47IB4xrsVU8Ndcdg3ev4R4KxDZ39ZRv1CKCPvXBrMw== rudikleine@Rudis-MBP"

    # Sergey
    "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAFgfR2UuCfW+wC+x6N/ppo3HmUaVn3skw/wRFwlVnh8XUnVNJEa/00UuCTdzDntG+3ENl0AtSll++C1xzdjgpmd3Pc7ZUiqD0ucXFB5CE1XmNVGrjcFLoJ7XosB/YZc9+nUgs3EnRcZWlpDLPRUpvBR0opfaomtuz3qBCLYo1tPt1UCybRJ9X2mob0viOsT2csH6ysS7v01K3ze1KMIVqEf2MIl2wTxPlGE1Q3V4S6/toUmktI6m8jTWFqpLIXFzGqVjA0f3BnTeQpr3q4empXxUsOcPStlmvclpisZjaoYMh9mCSqofyTFKAYvERdb44MyVCkTfoVJrhVIyItGiNtTJgCi6VXzIiwiLh4alTnPxtpPF4XunwZ1kEJi/pn0tBgAxwRRTTpwiyHl2duat8FFv+0uk3Bj1N0Z34IxvPuz0yn57UYQSJLcpgb1alEQEjnw17Q5YtjGhRXI8GEP1z8oMyL989YNUvIz2LjAACDDOxstTP5TDAtGGfTe7zjE/rqgpCs4EVdXEBy1XLEdk1+ldyRX4c4cqPN6UdQRukfRodG+F2zzNdSaXzpv7KdItusH00R/aHbFw29rSOVNOMKU21gk82qEVue84btLxi4+FkqFYNZNqFcbrOzRqPEoddArOCZISHSR1ONeBr2z5jQHraXXsbRj0DQExVK1xH/+t darkrengarius@darkrengarius"
)

printf -v ssh_keys_file "%s\n" "${ssh_keys[@]}"
echo "Replacing file $file_name on all servers with:"
echo "$ssh_keys_file"

# shellcheck disable=SC2154
for addr in "${addrs[@]}"; do
    echo "Server $addr ..."
    cmd="echo '$ssh_keys_file' > $file_name"
    ssh -o "StrictHostKeyChecking=no" "root@$addr" "$cmd"
    checkExitCode $?
    echo "DONE."
done
