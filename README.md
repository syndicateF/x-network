# x-network

[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Linux](https://img.shields.io/badge/Platform-Linux-orange)](https://kernel.org)

A D-Bus network daemon for Linux providing WiFi management via [iwd](https://iwd.wiki.kernel.org), USB tethering support, and real-time network state via netlink.

## Features

| Feature | Description |
|---------|-------------|
| **WiFi Management** | Scan, connect, disconnect, and manage saved networks |
| **USB Tethering** | Auto-detect phone tethering with automatic DHCP |
| **Real-time Events** | Netlink-based interface and IP change detection |
| **Traffic Monitoring** | Per-interface RX/TX statistics |
| **Signal Strength** | dBm and percentage readings from iwd |
| **Captive Portal** | Detection and browser launch |
| **Hotspot** | Create WiFi access point via iwd |
| **Airplane Mode** | rfkill integration |

## Requirements

- **Go 1.23+**
- **iwd** (Intel Wireless Daemon)
- **Linux kernel** with netlink

```bash
# Arch Linux
sudo pacman -S iwd go
sudo systemctl enable --now iwd
```

## Installation

```bash
git clone https://github.com/syndicateF/x-network.git
cd x-network
./install.sh
```

The installer builds the daemon, copies it to `/usr/lib/x-network/`, configures D-Bus, and enables the systemd user service.

## D-Bus Interface

| | |
|---|---|
| **Service** | `org.xshell.Network` |
| **Path** | `/org/xshell/Network` |
| **Bus** | Session |

### Properties

<details>
<summary>WiFi State</summary>

| Property | Type | Description |
|----------|------|-------------|
| `WifiEnabled` | `b` | Radio power state |
| `WifiScanning` | `b` | Scan in progress |
| `ConnectionState` | `s` | `disconnected`, `connecting`, `connected`, `failed` |
| `ConnectingSSID` | `s` | Network currently being connected |
| `ActiveSSID` | `s` | Connected network name |
| `ActiveSecurity` | `s` | Security type (open, psk, sae) |
| `SignalRSSI` | `n` | Signal strength in dBm |
| `SignalStrength` | `y` | Signal percentage (0-100) |
| `Frequency` | `u` | Channel frequency in MHz |
| `Band` | `s` | `2.4GHz`, `5GHz`, or `6GHz` |

</details>

<details>
<summary>Network Info</summary>

| Property | Type | Description |
|----------|------|-------------|
| `IpAddress` | `s` | Current IP address |
| `Gateway` | `s` | Default gateway |
| `MacAddress` | `s` | Interface MAC address |
| `InterfaceName` | `s` | Active interface name |
| `ConnectionType` | `s` | `wifi`, `ethernet`, or `usb` |
| `TrafficIn` | `t` | Download bytes/sec |
| `TrafficOut` | `t` | Upload bytes/sec |

</details>

<details>
<summary>Network Lists</summary>

| Property | Type | Description |
|----------|------|-------------|
| `Networks` | `a(ssybu)` | Available networks (ssid, security, signal, connected, saved) |
| `SavedNetworks` | `as` | Saved network SSIDs |

</details>

<details>
<summary>USB Tethering</summary>

| Property | Type | Description |
|----------|------|-------------|
| `UsbInterfaceDetected` | `b` | USB network interface exists |
| `UsbTetheringAvailable` | `b` | Phone tethering ready (carrier up) |
| `UsbTetheringConnected` | `b` | USB connection active with IP |
| `UsbInterfaceName` | `s` | USB interface name |

</details>

<details>
<summary>Other</summary>

| Property | Type | Description |
|----------|------|-------------|
| `AirplaneMode` | `b` | rfkill state |
| `HotspotActive` | `b` | AP mode active |
| `CaptivePortalDetected` | `b` | Captive portal present |
| `LastError` | `s` | Last error message |

</details>

### Methods

| Method | Description |
|--------|-------------|
| `Connect(a{sv})` | Connect with params (ssid, password, security, hidden) |
| `ConnectSaved(s)` | Connect to saved network by SSID |
| `Disconnect()` | Disconnect current connection |
| `Scan()` | Trigger network scan |
| `Forget(s)` | Remove saved network |
| `EnableWifi(b)` | Enable/disable WiFi radio |
| `StartHotspot(ss)` | Start hotspot with SSID and password |
| `StopHotspot()` | Stop hotspot |
| `SetAirplaneMode(b)` | Toggle airplane mode |
| `RequestUsbNetwork()` | Request DHCP on USB tethering interface |
| `ReleaseUsbNetwork()` | Release USB DHCP lease |

### Signals

Property changes emit `org.freedesktop.DBus.Properties.PropertiesChanged`.

## Usage

```bash
# Service status
systemctl --user status x-network

# Get all properties
busctl --user call org.xshell.Network /org/xshell/Network \
    org.freedesktop.DBus.Properties GetAll s org.xshell.Network

# Trigger scan
busctl --user call org.xshell.Network /org/xshell/Network \
    org.xshell.Network Scan

# Connect to network
busctl --user call org.xshell.Network /org/xshell/Network \
    org.xshell.Network Connect 'a{sv}' 2 \
    ssid s "NetworkName" password s "password123"

# View logs
journalctl --user -u x-network -f
```

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      Your UI / Shell                         │
│            (QuickShell, Waybar, AGS, EWW, etc)               │
└─────────────────────────┬───────────────────────────────────┘
                          │ D-Bus (Session)
┌─────────────────────────┴───────────────────────────────────┐
│                     x-network-daemon                         │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │  IWD Client │  │   Netlink   │  │   Traffic Monitor   │  │
│  │  (D-Bus)    │  │   Watcher   │  │   (sysfs stats)     │  │
│  └──────┬──────┘  └──────┬──────┘  └──────────┬──────────┘  │
│         │                │                     │             │
│  ┌──────┴────────────────┴─────────────────────┴──────────┐ │
│  │                    State Manager                        │ │
│  │              (Thread-safe, centralized)                 │ │
│  └─────────────────────────┬───────────────────────────────┘ │
│                            │                                 │
│  ┌─────────────────────────┴───────────────────────────────┐ │
│  │                    D-Bus Service                         │ │
│  │           (PropertiesChanged on state change)            │ │
│  └──────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

## Project Structure

```
x-network/
├── cmd/x-network/       # Entry point
├── internal/
│   ├── dbus/            # D-Bus service, methods, properties
│   ├── iwd/             # IWD client and agent
│   ├── netlink/         # Interface and address watcher
│   ├── state/           # Centralized state manager
│   └── traffic/         # Traffic statistics
├── configs/             # D-Bus and systemd configs
├── install.sh
└── uninstall.sh
```

## Development

```bash
# Build
go build -o x-network-daemon ./cmd/x-network

# Rebuild and restart
go build -o x-network-daemon ./cmd/x-network && \
    sudo cp -f x-network-daemon /usr/lib/x-network/ && \
    systemctl --user restart x-network
```

## License

MIT
