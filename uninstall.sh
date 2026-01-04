#!/bin/bash
set -e

echo "╔═══════════════════════════════════════╗"
echo "║       x-network Uninstaller           ║"
echo "╚═══════════════════════════════════════╝"

echo "→ Stopping service..."
systemctl --user stop x-network.service 2>/dev/null || true
systemctl --user disable x-network.service 2>/dev/null || true

echo "→ Removing files..."
sudo rm -rf /usr/lib/x-network
sudo rm -f /etc/dbus-1/session.d/org.xshell.Network.conf
sudo rm -f /usr/share/dbus-1/services/org.xshell.Network.service
rm -f ~/.config/systemd/user/x-network.service

echo "→ Reloading..."
systemctl --user daemon-reload

echo ""
echo "✓ Uninstallation complete!"
