#!/usr/bin/env bash
# This script sets up system requirements that need root/sudo access for e2e tests.
# Run this ONCE before running e2e tests:
#   sudo ./ci_scripts/setup-sudo-requirements.sh
#
# These settings persist across reboots on most systems, so you typically only
# need to run this once per development machine.

set -euo pipefail

echo "Setting up system requirements for e2e tests..."

# Create IP aliases for service discovery tests
if [[ "$OSTYPE" == "linux-gnu" ]]; then
    echo "Creating IP aliases on Linux (12.12.12.1-255 on lo)..."
    for ((i=1; i<=255; i++))
    do
        # Skip if alias already exists
        if ! ip addr show lo | grep -q "12.12.12.$i/32"; then
            ip addr add 12.12.12.$i/32 dev lo 2>/dev/null || true
        fi
    done
    
    # Enable IP forwarding for service discovery
    echo "Enabling IP forwarding..."
    echo 1 > /proc/sys/net/ipv4/ip_forward
    echo 1 > /proc/sys/net/ipv6/conf/all/forwarding
    
    echo "✓ Linux setup complete"
    
elif [[ "$OSTYPE" == "darwin"* ]]; then
    echo "Creating IP aliases on macOS (12.12.12.1-255 on lo0)..."
    for ((i=1; i<=255; i++))
    do
        # Skip if alias already exists
        if ! ifconfig lo0 | grep -q "12.12.12.$i"; then
            ifconfig lo0 alias 12.12.12.$i/32 2>/dev/null || true
        fi
    done
    
    # Note: macOS doesn't require the forwarding settings
    echo "✓ macOS setup complete"
    
else
    echo "⚠ Unknown OS type: $OSTYPE"
    echo "  You may need to manually configure:"
    echo "  - IP aliases 12.12.12.1-255 on loopback interface"
    echo "  - IP forwarding enabled"
    exit 1
fi

echo ""
echo "System requirements configured successfully!"
echo "You can now run: make e2e-build && make e2e-run && make e2e-test"
