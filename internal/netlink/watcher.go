package netlink

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"x-network/internal/state"

	"github.com/jsimonetti/rtnetlink"
	"github.com/mdlayher/netlink"
)

// Netlink message types (from syscall)
const (
	RTM_NEWLINK = syscall.RTM_NEWLINK // 16
	RTM_DELLINK = syscall.RTM_DELLINK // 17
	RTM_NEWADDR = syscall.RTM_NEWADDR // 20
	RTM_DELADDR = syscall.RTM_DELADDR // 21
)

// Watcher watches netlink events
type Watcher struct {
	conn          *netlink.Conn   // Raw netlink connection for message type access (events)
	rtConn        *rtnetlink.Conn // rtnetlink connection for List operations (fetching)
	stateMgr      *state.Manager
	stopCh        chan struct{}
	lastLinkState map[uint32]string // Track last state per interface to avoid log spam
}

// NewWatcher creates a new netlink watcher
func NewWatcher(stateMgr *state.Manager) (*Watcher, error) {
	// Raw netlink.Conn for event watching (to access Header.Type for RTM_DELLINK)
	conn, err := netlink.Dial(syscall.NETLINK_ROUTE, &netlink.Config{
		Groups: 0x1 | 0x10, // RTMGRP_LINK | RTMGRP_IPV4_IFADDR
	})
	if err != nil {
		return nil, fmt.Errorf("failed to dial netlink: %w", err)
	}

	// rtnetlink.Conn for List operations (fetching interfaces, routes, addresses)
	rtConn, err := rtnetlink.Dial(nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to dial rtnetlink: %w", err)
	}

	return &Watcher{
		conn:          conn,
		rtConn:        rtConn,
		stateMgr:      stateMgr,
		stopCh:        make(chan struct{}),
		lastLinkState: make(map[uint32]string),
	}, nil
}

// Close closes the netlink connections
func (w *Watcher) Close() {
	close(w.stopCh)
	w.conn.Close()
	w.rtConn.Close()
}

// Run starts watching netlink events
func (w *Watcher) Run() {
	// Initial fetch
	w.fetchInterfaces()
	w.fetchAddresses()

	// Watch for events
	for {
		select {
		case <-w.stopCh:
			return
		default:
			msgs, err := w.conn.Receive()
			if err != nil {
				log.Printf("Netlink receive error: %v", err)
				continue
			}

			for _, msg := range msgs {
				w.handleRawMessage(msg)
			}
		}
	}
}

// handleRawMessage handles a raw netlink message with type detection
func (w *Watcher) handleRawMessage(msg netlink.Message) {
	switch msg.Header.Type {
	case RTM_NEWLINK:
		// Interface added or state changed
		w.handleLinkMessage(msg.Data, false)
	case RTM_DELLINK:
		// Interface removed - CRITICAL for proper cleanup
		w.handleLinkMessage(msg.Data, true)
	case RTM_NEWADDR:
		// Address added
		w.handleAddressMessage(msg.Data, false)
	case RTM_DELADDR:
		// Address removed
		w.handleAddressMessage(msg.Data, true)
	}
}

// handleLinkMessage handles link up/down events and interface removal
func (w *Watcher) handleLinkMessage(data []byte, isRemoved bool) {
	// Parse raw data into LinkMessage
	var msg rtnetlink.LinkMessage
	if err := msg.UnmarshalBinary(data); err != nil {
		log.Printf("Failed to parse link message: %v", err)
		return
	}

	// Get interface name from attributes
	ifaceName := msg.Attributes.Name
	ifaceIndex := msg.Index

	if ifaceName == "" || ifaceName == "lo" {
		return
	}

	// Handle RTM_DELLINK - interface removed from system
	if isRemoved {
		log.Printf("RTM_DELLINK: Interface %s (idx=%d) removed", ifaceName, ifaceIndex)
		w.stateMgr.Update(func(st *state.State) {
			// Clear USB state if this was our tracked USB interface (match by ifindex!)
			if st.UsbInterfaceIndex == ifaceIndex {
				log.Printf("USB interface removed (ifindex=%d matched)", ifaceIndex)
				st.UsbInterfaceDetected = false
				st.UsbTetheringAvailable = false
				st.UsbTetheringConnected = false
				st.UsbInterfaceName = ""
				st.UsbInterfaceIndex = 0
			}
		})
		return
	}

	// RTM_NEWLINK - interface exists or state changed
	isUp := (msg.Attributes.OperationalState == rtnetlink.OperStateUp)
	hasCarrier := (msg.Attributes.Carrier != nil && *msg.Attributes.Carrier == 1)

	// Log deduplication: only log when state actually changes
	stateKey := fmt.Sprintf("%v:%v", isUp, hasCarrier)
	if w.lastLinkState[ifaceIndex] != stateKey {
		log.Printf("RTM_NEWLINK: Interface %s (idx=%d): up=%v, carrier=%v", ifaceName, ifaceIndex, isUp, hasCarrier)
		w.lastLinkState[ifaceIndex] = stateKey
	}

	// Check if this is a USB interface (via sysfs - kernel source of truth)
	isUsb := isUsbInterface(ifaceName)

	w.stateMgr.Update(func(st *state.State) {
		// Handle USB interface
		if isUsb {
			// USB interface detected
			st.UsbInterfaceDetected = true
			st.UsbInterfaceName = ifaceName
			st.UsbInterfaceIndex = ifaceIndex

			if hasCarrier {
				// Carrier up = phone tethering is ready
				if !st.UsbTetheringAvailable {
					st.UsbTetheringAvailable = true
					log.Printf("USB tethering available on %s (carrier up)", ifaceName)

					// If interface is down but has carrier, bring it up
					if !isUp {
						log.Printf("Bringing up USB interface %s", ifaceName)
						go bringUpInterface(ifaceName)
					}

					// Auto-start DHCP when carrier comes up
					go w.runDHCPOnInterface(ifaceName)
				}
			} else {
				// No carrier = phone tethering not active (but interface still exists)
				st.UsbTetheringAvailable = false
				st.UsbTetheringConnected = false
				// NOTE: Don't clear UsbInterfaceDetected here - RTM_DELLINK handles that
			}
		}

		// Update general interface info (non-USB)
		// Do NOT touch WiFi ConnectionState here - IWD D-Bus is the source of truth
		if !isUsb && isUp && (st.InterfaceName == ifaceName || st.InterfaceName == "") {
			st.InterfaceName = ifaceName
			st.ConnectionType = getConnectionType(ifaceName)
		}
	})
}

// bringUpInterface brings up a network interface (requires sudo)
func bringUpInterface(iface string) {
	cmd := exec.Command("sudo", "ip", "link", "set", iface, "up")
	if err := cmd.Run(); err != nil {
		log.Printf("Failed to bring up %s: %v", iface, err)
	}
}

// handleAddressMessage handles IP address changes
func (w *Watcher) handleAddressMessage(data []byte, isRemoved bool) {
	// Parse raw data into AddressMessage
	var msg rtnetlink.AddressMessage
	if err := msg.UnmarshalBinary(data); err != nil {
		log.Printf("Failed to parse address message: %v", err)
		return
	}

	// Ignore address removal events for now (we care about address adds)
	if isRemoved {
		return
	}

	// Get interface name via rtConn (List operation)
	links, err := w.rtConn.Link.List()
	if err != nil {
		return
	}

	var ifaceName string
	for _, link := range links {
		if link.Index == msg.Index {
			ifaceName = link.Attributes.Name
			break
		}
	}

	if ifaceName == "" || ifaceName == "lo" {
		return
	}

	// Get IP address
	ip := msg.Attributes.Address
	ifaceIndex := msg.Index

	log.Printf("Address change on %s: %s", ifaceName, ip)

	// Check if this is a USB interface
	isUsb := isUsbInterface(ifaceName)

	w.stateMgr.Update(func(st *state.State) {
		// Handle USB interface address (IP + route = connected)
		if isUsb && st.UsbInterfaceName == ifaceName {
			st.IpAddress = ip.String()
			// Check for default route via this interface (Connected = IP + route)
			if w.checkDefaultRouteViaInterface(ifaceIndex) {
				st.UsbTetheringConnected = true
				st.ConnectionType = "usb"
				log.Printf("USB tethering connected on %s: %s", ifaceName, ip)
			}
		}

		// Handle WiFi/Ethernet
		if !isUsb && st.InterfaceName == ifaceName {
			st.IpAddress = ip.String()
			// Mark as connected when IP is assigned
			if st.ConnectionState == state.StateConnecting || st.ConnectionState == state.StateObtaining {
				st.ConnectionState = state.StateConnected
			}
		}
	})

	// Trigger weather refresh after resume when IPv4 is assigned
	// NOTE: Only weather is triggered here - it's time-sensitive and network-dependent
	// Holidays are NOT triggered on resume - they use month-based refresh via timer
	currentState := w.stateMgr.Get()
	if currentState.WasResumed &&
		!currentState.WeatherTriggered &&
		time.Since(currentState.ResumeTimestamp) < 60*time.Second &&
		ip != nil && ip.To4() != nil {

		log.Printf("Resume + IPv4 assigned: triggering x-fetch weather")
		go exec.Command(
			os.ExpandEnv("$HOME/.local/bin/x-fetch"),
			"weather", "--reason=resume",
		).Run()

		// Clear flags
		w.stateMgr.Update(func(st *state.State) {
			st.WasResumed = false
			st.WeatherTriggered = true
		})
	}

	// Trigger weather refresh on startup when first IPv4 is assigned
	// NOTE: Only weather is triggered here - holidays use month-based refresh
	if currentState.IsStartup &&
		!currentState.WeatherTriggered &&
		ip != nil && ip.To4() != nil {

		log.Printf("Startup + IPv4 assigned: triggering x-fetch weather")
		go exec.Command(
			os.ExpandEnv("$HOME/.local/bin/x-fetch"),
			"weather", "--reason=startup",
		).Run()

		// Clear startup flag
		w.stateMgr.Update(func(st *state.State) {
			st.IsStartup = false
			st.WeatherTriggered = true
		})
	}

	// Try to get gateway
	w.fetchGateway()
}

// fetchInterfaces fetches current interface states
func (w *Watcher) fetchInterfaces() {
	links, err := w.rtConn.Link.List()
	if err != nil {
		return
	}

	for _, link := range links {
		if link.Attributes.Name == "lo" {
			continue
		}

		ifaceName := link.Attributes.Name
		isUp := link.Attributes.OperationalState == rtnetlink.OperStateUp
		hasCarrier := link.Attributes.Carrier != nil && *link.Attributes.Carrier == 1

		// Check for USB interfaces on startup
		if isUsbInterface(ifaceName) {
			w.stateMgr.Update(func(st *state.State) {
				st.UsbInterfaceDetected = true
				st.UsbInterfaceName = ifaceName
				st.UsbInterfaceIndex = link.Index

				if hasCarrier {
					st.UsbTetheringAvailable = true
					log.Printf("USB tethering available on %s at startup (carrier up)", ifaceName)

					// If interface is down but has carrier, bring it up
					if !isUp {
						log.Printf("Bringing up USB interface %s at startup", ifaceName)
						go bringUpInterface(ifaceName)
					}

					// Auto-start DHCP
					go w.runDHCPOnInterface(ifaceName)
				}
			})
		}

		// Handle WiFi/Ethernet
		if isUp && !isUsbInterface(ifaceName) {
			w.stateMgr.Update(func(st *state.State) {
				st.InterfaceName = link.Attributes.Name
				st.MacAddress = net.HardwareAddr(link.Attributes.Address).String()
				st.ConnectionType = getConnectionType(link.Attributes.Name)
			})
		}
	}
}

// fetchAddresses fetches current IP addresses
func (w *Watcher) fetchAddresses() {
	addrs, err := w.rtConn.Address.List()
	if err != nil {
		return
	}

	st := w.stateMgr.Get()
	links, _ := w.rtConn.Link.List()

	for _, addr := range addrs {
		// Find matching link
		for _, link := range links {
			if link.Index == addr.Index && link.Attributes.Name == st.InterfaceName {
				w.stateMgr.Update(func(s *state.State) {
					if addr.Attributes.Address != nil {
						s.IpAddress = addr.Attributes.Address.String()
					}
				})
				break
			}
		}
	}
}

// fetchGateway fetches default gateway
func (w *Watcher) fetchGateway() {
	routes, err := w.rtConn.Route.List()
	if err != nil {
		return
	}

	for _, route := range routes {
		// Default route (0.0.0.0/0)
		if route.Attributes.Dst == nil && route.Attributes.Gateway != nil {
			w.stateMgr.Update(func(st *state.State) {
				st.Gateway = route.Attributes.Gateway.String()
			})
			break
		}
	}
}

// getConnectionType determines type from interface using sysfs (fully dynamic)
func getConnectionType(iface string) string {
	// Check sysfs for USB first (most reliable)
	if isUsbInterface(iface) {
		return "usb"
	}
	// Check sysfs for WiFi (kernel-standard: /sys/class/net/<iface>/wireless exists)
	if isWifiInterface(iface) {
		return "wifi"
	}
	// Default to ethernet for other physical interfaces
	if isPhysicalInterface(iface) {
		return "ethernet"
	}
	return "unknown"
}

// isUsbInterface checks if interface is USB via sysfs (kernel source of truth)
// Checks /sys/class/net/<iface>/device/subsystem -> usb
func isUsbInterface(name string) bool {
	subsystemPath := "/sys/class/net/" + name + "/device/subsystem"
	target, err := os.Readlink(subsystemPath)
	if err != nil {
		return false
	}
	return strings.HasSuffix(target, "/usb")
}

// isWifiInterface checks if interface is WiFi via sysfs
// Kernel creates /sys/class/net/<iface>/wireless for WiFi interfaces
func isWifiInterface(name string) bool {
	wirelessPath := "/sys/class/net/" + name + "/wireless"
	_, err := os.Stat(wirelessPath)
	return err == nil
}

// isPhysicalInterface checks if interface has a device in sysfs (not virtual)
func isPhysicalInterface(name string) bool {
	devicePath := "/sys/class/net/" + name + "/device"
	_, err := os.Stat(devicePath)
	return err == nil
}

// checkDefaultRouteViaInterface checks if there's a default route through the given interface
func (w *Watcher) checkDefaultRouteViaInterface(ifaceIndex uint32) bool {
	routes, err := w.rtConn.Route.List()
	if err != nil {
		return false
	}

	for _, route := range routes {
		// Default route (0.0.0.0/0) via our interface
		if route.Attributes.Dst == nil &&
			route.Attributes.Gateway != nil &&
			route.Attributes.OutIface == ifaceIndex {
			return true
		}
	}
	return false
}

// runDHCPOnInterface runs dhcpcd on the given interface asynchronously (requires sudo)
func (w *Watcher) runDHCPOnInterface(iface string) {
	go func() {
		log.Printf("Starting DHCP on USB interface %s", iface)
		cmd := exec.Command("sudo", "dhcpcd", "-4", "-q", iface)
		if err := cmd.Run(); err != nil {
			log.Printf("DHCP failed on %s: %v", iface, err)
			// Don't spam - DHCP failure handled by netlink (no IP = not connected)
		}
	}()
}
