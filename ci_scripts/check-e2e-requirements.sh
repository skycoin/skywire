#!/usr/bin/env bash
# This script checks if the system requirements for e2e tests are configured.
# If not, it instructs the user to run setup-sudo-requirements.sh with sudo.

set -euo pipefail

check_failed=0

if [[ "$OSTYPE" == "linux-gnu" ]]; then
    # Check if IP forwarding is enabled
    if [ "$(cat /proc/sys/net/ipv4/ip_forward 2>/dev/null || echo 0)" != "1" ]; then
        echo "⚠ IPv4 forwarding is not enabled"
        check_failed=1
    fi
    
    if [ "$(cat /proc/sys/net/ipv6/conf/all/forwarding 2>/dev/null || echo 0)" != "1" ]; then
        echo "⚠ IPv6 forwarding is not enabled"
        check_failed=1
    fi
    
    # Check if at least some IP aliases exist
    if ! ip addr show lo | grep -q "12.12.12"; then
        echo "⚠ IP aliases (12.12.12.x) not found on loopback interface"
        check_failed=1
    fi
    
elif [[ "$OSTYPE" == "darwin"* ]]; then
    # Check if at least some IP aliases exist on macOS
    if ! ifconfig lo0 | grep -q "12.12.12"; then
        echo "⚠ IP aliases (12.12.12.x) not found on loopback interface"
        check_failed=1
    fi
fi

if [ $check_failed -eq 1 ]; then
    echo ""
    echo "❌ System requirements not configured for e2e tests."
    echo ""
    echo "Please run the following command to set up requirements:"
    echo "  sudo ./ci_scripts/setup-sudo-requirements.sh"
    echo ""
    echo "This only needs to be done once per machine."
    exit 1
fi

echo "✓ System requirements configured for e2e tests"
exit 0
