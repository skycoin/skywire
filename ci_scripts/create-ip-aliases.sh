#!/usr/bin/env bash
# Legacy script for CI compatibility. Delegates to setup-sudo-requirements.sh
# This script should be run with sudo in CI environments.

# Check if running as root (in CI)
if [ "$EUID" -eq 0 ] || [ "$(id -u)" -eq 0 ]; then
    # Running as root, execute the setup script directly
    exec "$(dirname "$0")/setup-sudo-requirements.sh"
else
    echo "⚠ This script requires root privileges."
    echo "Please run: sudo $0"
    exit 1
fi