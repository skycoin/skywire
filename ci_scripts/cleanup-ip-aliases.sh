#!/usr/bin/env bash
# Remove IP aliases created by setup-sudo-requirements.sh
# Run after E2E tests: sudo ./ci_scripts/cleanup-ip-aliases.sh

set -euo pipefail

if [[ "$OSTYPE" == "linux-gnu" ]]; then
    echo "Removing IP aliases from lo..."
    for ((i=1; i<=255; i++)); do
        ip addr del 12.12.12.$i/32 dev lo 2>/dev/null || true
    done
    echo "Done."
elif [[ "$OSTYPE" == "darwin"* ]]; then
    echo "Removing IP aliases from lo0..."
    for ((i=1; i<=255; i++)); do
        ifconfig lo0 -alias 12.12.12.$i 2>/dev/null || true
    done
    echo "Done."
fi
