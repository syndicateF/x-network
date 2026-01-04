#!/bin/bash
set -e

echo "╔═══════════════════════════════════════╗"
echo "║       x-network Installer             ║"
echo "╚═══════════════════════════════════════╝"

cd "$(dirname "$0")"

# Build
echo "→ Building..."
go build -ldflags="-s -w" -o x-network ./cmd/x-network

# Install binary
echo "→ Installing binary..."
sudo mkdir -p /usr/lib/x-network
sudo cp x-network /usr/lib/x-network/x-network-daemon

# Install D-Bus configs
echo "→ Installing D-Bus configs..."
sudo mkdir -p /etc/dbus-1/session.d
sudo mkdir -p /usr/share/dbus-1/services
sudo cp configs/org.xshell.Network.conf /etc/dbus-1/session.d/
sudo cp configs/org.xshell.Network.service /usr/share/dbus-1/services/

# Create user systemd service (optional)
mkdir -p ~/.config/systemd/user
cat > ~/.config/systemd/user/x-network.service << 'EOF'
[Unit]
Description=x-network daemon
After=graphical-session.target

[Service]
Type=simple
ExecStart=/usr/lib/x-network/x-network-daemon
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF

echo "→ Enabling user service..."
systemctl --user daemon-reload
systemctl --user enable x-network.service
systemctl --user start x-network.service

echo ""
echo "✓ Installation complete!"
echo ""
echo "Commands:"
echo "  systemctl --user status x-network    # Check status"
echo "  systemctl --user restart x-network   # Restart daemon"
echo "  journalctl --user -u x-network -f    # View logs"
echo ""
echo "Test D-Bus:"
echo "  busctl --user get-property org.xshell.Network /org/xshell/Network org.xshell.Network WifiEnabled"
