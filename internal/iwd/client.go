package iwd

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"x-network/internal/state"

	"github.com/godbus/dbus/v5"
)

const (
	IWDService        = "net.connman.iwd"
	StationIface      = "net.connman.iwd.Station"
	DeviceIface       = "net.connman.iwd.Device"
	NetworkIface      = "net.connman.iwd.Network"
	KnownNetworkIface = "net.connman.iwd.KnownNetwork"
	AccessPointIface  = "net.connman.iwd.AccessPoint"
)

// Client is the IWD D-Bus client
type Client struct {
	conn        *dbus.Conn
	stateMgr    *state.Manager
	devicePath  dbus.ObjectPath
	stationPath dbus.ObjectPath
	initialized bool   // Idempotency flag for maybeInitIWD
	agent       *Agent // IWD D-Bus Agent for credential handling

	// Connection state management
	connectMu sync.Mutex // Prevents concurrent connection attempts
	connectID uint64     // Increments on each new connection attempt
}

// NewClient creates a new IWD client with event-driven service detection
func NewClient(stateMgr *state.Manager) (*Client, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to system bus: %w", err)
	}

	c := &Client{
		conn:        conn,
		stateMgr:    stateMgr,
		initialized: false,
	}

	// Subscribe to NameOwnerChanged for IWD service lifecycle
	if err := c.subscribeToIWDLifecycle(); err != nil {
		log.Printf("Warning: Failed to subscribe to IWD lifecycle: %v", err)
	}

	// Try to initialize immediately (IWD may already be running)
	if err := c.maybeInitIWD(); err != nil {
		log.Printf("IWD not available yet, waiting for NameOwnerChanged...")
		// Not a fatal error - we'll init when IWD appears
	}

	return c, nil
}

// subscribeToIWDLifecycle subscribes to NameOwnerChanged for IWD service
// and InterfacesAdded for detecting when Station appears at boot
func (c *Client) subscribeToIWDLifecycle() error {
	// Match NameOwnerChanged for net.connman.iwd
	rule := "type='signal',sender='org.freedesktop.DBus',interface='org.freedesktop.DBus',member='NameOwnerChanged',arg0='net.connman.iwd'"
	if err := c.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule).Err; err != nil {
		return err
	}

	// Match InterfacesAdded from IWD ObjectManager (for Station appearing at boot)
	ifaceRule := "type='signal',sender='net.connman.iwd',interface='org.freedesktop.DBus.ObjectManager',member='InterfacesAdded'"
	if err := c.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, ifaceRule).Err; err != nil {
		log.Printf("Warning: Failed to subscribe to InterfacesAdded: %v", err)
	}

	// Handle signals in goroutine
	ch := make(chan *dbus.Signal, 10)
	c.conn.Signal(ch)

	go func() {
		for signal := range ch {
			switch signal.Name {
			case "org.freedesktop.DBus.NameOwnerChanged":
				if len(signal.Body) == 3 {
					name := signal.Body[0].(string)
					oldOwner := signal.Body[1].(string)
					newOwner := signal.Body[2].(string)

					if name == "net.connman.iwd" {
						if oldOwner == "" && newOwner != "" {
							// IWD appeared
							log.Printf("IWD service appeared, initializing...")
							if err := c.maybeInitIWD(); err != nil {
								log.Printf("Failed to initialize IWD: %v", err)
							}
						} else if oldOwner != "" && newOwner == "" {
							// IWD disappeared
							log.Printf("IWD service disappeared, marking WiFi unavailable")
							c.handleIWDDisappear()
						}
					}
				}

			case "org.freedesktop.DBus.ObjectManager.InterfacesAdded":
				// Station interface appeared - this handles boot race condition
				if len(signal.Body) >= 2 {
					ifaces, ok := signal.Body[1].(map[string]map[string]dbus.Variant)
					if ok {
						if _, hasStation := ifaces[StationIface]; hasStation {
							log.Printf("Station interface appeared, initializing...")
							if err := c.maybeInitIWD(); err != nil {
								log.Printf("Failed to initialize IWD after Station appeared: %v", err)
							}
						}
					}
				}
			}
		}
	}()

	return nil
}

// maybeInitIWD initializes IWD connection with idempotency
func (c *Client) maybeInitIWD() error {
	if c.initialized {
		return nil // Already initialized
	}

	// Find the WiFi device
	if err := c.findDevice(); err != nil {
		return err
	}

	// Subscribe to IWD property signals
	if err := c.subscribeSignals(); err != nil {
		log.Printf("Warning: Failed to subscribe to IWD signals: %v", err)
	}

	// Create and register Agent with IWD
	c.agent = NewAgent(c.conn, c)
	if err := c.agent.RegisterWithIWD(); err != nil {
		log.Printf("Warning: Failed to register Agent with IWD: %v", err)
		// Non-fatal - saved networks can still connect without agent
	}

	c.initialized = true
	log.Printf("IWD client connected")

	// Fetch initial Networks list (important when daemon starts with active connection)
	// Small delay ensures ActiveSSID is already set in state
	go func() {
		time.Sleep(100 * time.Millisecond)
		networks := c.fetchNetworksFromIWD()
		if networks != nil {
			c.stateMgr.Update(func(st *state.State) {
				st.Networks = networks
			})
		}
	}()

	return nil
}

// handleIWDDisappear handles IWD service disappearing
func (c *Client) handleIWDDisappear() {
	c.initialized = false
	c.devicePath = ""
	c.stationPath = ""

	c.stateMgr.Update(func(st *state.State) {
		st.WifiEnabled = false
		st.WifiScanning = false
		st.ConnectionState = state.StateDisconnected
		st.ActiveSSID = ""
		st.SignalStrength = 0
	})
}

// Close closes the D-Bus connection
func (c *Client) Close() {
	c.conn.Close()
}

// findDevice finds the WiFi device object path (single attempt, no polling)
// If Station not found at startup, InterfacesAdded signal will trigger init when it appears
func (c *Client) findDevice() error {
	obj := c.conn.Object(IWDService, "/")

	var result map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := obj.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&result)
	if err != nil {
		return fmt.Errorf("failed to get managed objects: %w", err)
	}

	// Find device and station paths, and known networks
	savedNetworks := []string{}
	for path, ifaces := range result {
		// Look for Station interface (not just Device)
		if stationProps, ok := ifaces[StationIface]; ok {
			c.stationPath = path
			log.Printf("Found Station at: %s", path)

			// Also set device path (parent or same)
			if devProps, ok := ifaces[DeviceIface]; ok {
				c.devicePath = path
				// IMPORTANT: Read device props (including Powered) from the same path!
				c.updateDeviceProps(devProps)
			}

			// Read initial station state
			c.updateStationState(stationProps)
		}

		// Find device if we haven't yet (fallback for separate device path)
		if c.devicePath == "" {
			if devProps, ok := ifaces[DeviceIface]; ok {
				c.devicePath = path
				c.updateDeviceProps(devProps)
			}
		}

		// Collect known networks (saved)
		if knProps, ok := ifaces[KnownNetworkIface]; ok {
			if nameV, ok := knProps["Name"]; ok {
				ssid := nameV.Value().(string)
				savedNetworks = append(savedNetworks, ssid)
				log.Printf("Found known network: %s", ssid)
			}
		}
	}

	if c.stationPath == "" {
		return fmt.Errorf("no WiFi station found")
	}

	// Update saved networks in state AFTER successful Station check
	// This prevents partial updates when findDevice fails at boot
	if len(savedNetworks) > 0 {
		c.stateMgr.Update(func(st *state.State) {
			st.SavedNetworks = savedNetworks
		})
	}

	return nil
}

// updateDeviceProps updates device properties
func (c *Client) updateDeviceProps(props map[string]dbus.Variant) {
	c.stateMgr.Update(func(st *state.State) {
		if v, ok := props["Name"]; ok {
			st.InterfaceName = v.Value().(string)
		}
		if v, ok := props["Address"]; ok {
			st.MacAddress = v.Value().(string)
		}
		if v, ok := props["Powered"]; ok {
			st.WifiEnabled = v.Value().(bool)
		}
	})
}

// updateStationState updates state from station properties
func (c *Client) updateStationState(props map[string]dbus.Variant) {
	c.stateMgr.Update(func(st *state.State) {
		if v, ok := props["State"]; ok {
			stateStr := v.Value().(string)
			log.Printf("Station state: %s", stateStr)
			switch stateStr {
			case "disconnected":
				st.ConnectionState = state.StateDisconnected
			case "connecting":
				st.ConnectionState = state.StateConnecting
			case "connected":
				st.ConnectionState = state.StateConnected
			case "roaming":
				st.ConnectionState = state.StateConnected
			}
		}
		if v, ok := props["Scanning"]; ok {
			st.WifiScanning = v.Value().(bool)
		}
		// Read connected network on startup!
		if v, ok := props["ConnectedNetwork"]; ok {
			networkPath := v.Value().(dbus.ObjectPath)
			log.Printf("Connected network path: %s", networkPath)
			if networkPath != "" {
				c.fetchNetworkDetails(networkPath, st)
			}
		}
	})
}

// updateDeviceState updates state from device properties (legacy, kept for signal handler)
func (c *Client) updateDeviceState(ifaces map[string]map[string]dbus.Variant) {
	if devProps, ok := ifaces[DeviceIface]; ok {
		c.updateDeviceProps(devProps)
	}
	if stationProps, ok := ifaces[StationIface]; ok {
		c.updateStationState(stationProps)
	}
}

// subscribeSignals subscribes to IWD property change signals
func (c *Client) subscribeSignals() error {
	// Match IWD property changes
	rule := fmt.Sprintf("type='signal',sender='%s',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged'", IWDService)

	call := c.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule)
	if call.Err != nil {
		return call.Err
	}

	// Handle signals in goroutine
	ch := make(chan *dbus.Signal, 10)
	c.conn.Signal(ch)

	go func() {
		for sig := range ch {
			if sig.Name == "org.freedesktop.DBus.Properties.PropertiesChanged" {
				c.handlePropertyChange(sig)
			}
		}
	}()

	return nil
}

// handlePropertyChange handles IWD property change signals
func (c *Client) handlePropertyChange(sig *dbus.Signal) {
	if len(sig.Body) < 2 {
		return
	}

	iface, ok := sig.Body[0].(string)
	if !ok {
		return
	}

	props, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return
	}

	switch iface {
	case StationIface:
		c.handleStationChange(props)
	case DeviceIface:
		c.handleDeviceChange(props)
	}
}

// handleStationChange handles Station property changes
func (c *Client) handleStationChange(props map[string]dbus.Variant) {
	// Check if scan just completed (Scanning went from true to false)
	scanCompleted := false
	if v, ok := props["Scanning"]; ok {
		scanning := v.Value().(bool)
		if !scanning {
			// Scan completed - fetch fresh networks
			scanCompleted = true
		}
	}

	c.stateMgr.Update(func(st *state.State) {
		if v, ok := props["State"]; ok {
			stateStr := v.Value().(string)
			prevState := st.ConnectionState
			switch stateStr {
			case "disconnected":
				st.ConnectionState = state.StateDisconnected
				st.ActiveSSID = ""
				st.ConnectingSSID = "" // Always clear on disconnected
				// Detect authentication failure: connecting -> disconnected
				if prevState == state.StateConnecting {
					st.LastError = "Authentication failed"
					st.ConnectionState = state.StateFailed
					log.Printf("Authentication failure detected (connecting -> disconnected)")
				}
				// Trigger USB fallback if available
				if prevState == state.StateConnected && st.UsbTetheringAvailable && st.UsbInterfaceName != "" {
					log.Printf("WiFi disconnected, attempting USB tethering fallback on %s", st.UsbInterfaceName)
					go c.tryUsbFallback(st.UsbInterfaceName)
				}
			case "connecting":
				st.ConnectionState = state.StateConnecting
				st.LastError = "" // Clear any previous error on new attempt
			case "connected":
				st.ConnectionState = state.StateConnected
				st.ConnectingSSID = "" // Clear on connected - connection complete
				st.LastError = ""      // Clear any error on successful connection
			case "roaming":
				st.ConnectionState = state.StateConnected
			}
		}
		if v, ok := props["Scanning"]; ok {
			st.WifiScanning = v.Value().(bool)
		}
		if v, ok := props["ConnectedNetwork"]; ok {
			networkPath := v.Value().(dbus.ObjectPath)
			c.fetchNetworkDetails(networkPath, st)
		}
	})

	// Fetch networks AFTER state update (outside the Update lock)
	if scanCompleted {
		networks := c.fetchNetworksFromIWD()
		if networks != nil {
			c.stateMgr.Update(func(st *state.State) {
				st.Networks = networks
			})
		}
	}

	// Refresh known networks AND available networks when connected
	// This ensures active flag and saved flag are up-to-date after connection
	if v, ok := props["State"]; ok {
		if stateStr := v.Value().(string); stateStr == "connected" {
			go func() {
				c.refreshKnownNetworks()
				// Also refresh Networks array so active flag is updated
				networks := c.fetchNetworksFromIWD()
				if networks != nil {
					c.stateMgr.Update(func(st *state.State) {
						st.Networks = networks
					})
				}
			}()
		}
	}
}

// handleDeviceChange handles Device property changes
func (c *Client) handleDeviceChange(props map[string]dbus.Variant) {
	c.stateMgr.Update(func(st *state.State) {
		if v, ok := props["Powered"]; ok {
			st.WifiEnabled = v.Value().(bool)
		}
	})
}

// fetchNetworkDetails fetches details of connected network including signal
func (c *Client) fetchNetworkDetails(path dbus.ObjectPath, st *state.State) {
	if path == "" {
		return
	}

	obj := c.conn.Object(IWDService, path)

	var props map[string]dbus.Variant
	err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, NetworkIface).Store(&props)
	if err != nil {
		return
	}

	if v, ok := props["Name"]; ok {
		st.ActiveSSID = v.Value().(string)
	}
	if v, ok := props["Type"]; ok {
		st.ActiveSecurity = v.Value().(string)
	}

	// Fetch signal strength from GetOrderedNetworks
	c.fetchActiveSignal(st, path)
}

// fetchActiveSignal gets signal strength for the active network from GetOrderedNetworks
func (c *Client) fetchActiveSignal(st *state.State, activePath dbus.ObjectPath) {
	stationObj := c.conn.Object(IWDService, c.stationPath)

	type orderedNetwork struct {
		Path dbus.ObjectPath
		RSSI int16
	}

	var result []orderedNetwork
	err := stationObj.Call(StationIface+".GetOrderedNetworks", 0).Store(&result)
	if err != nil {
		log.Printf("GetOrderedNetworks error: %v", err)
		return
	}

	// Find signal for active network
	for _, net := range result {
		if net.Path == activePath {
			// RSSI is in 1/100 dBm units, convert to dBm
			rssiDBm := int16(net.RSSI / 100)
			st.SignalRSSI = rssiDBm
			st.SignalStrength = state.DBmToPercent(rssiDBm)
			log.Printf("Active network signal: %d dBm = %d%%", rssiDBm, st.SignalStrength)
			return
		}
	}
}

// refreshState refreshes all state from IWD
func (c *Client) refreshState() {
	// Refresh networks
	c.Scan()
}

// refreshKnownNetworks fetches known networks from IWD and updates SavedNetworks
// Called when connection state changes to "connected" to sync after forget+reconnect
func (c *Client) refreshKnownNetworks() {
	obj := c.conn.Object(IWDService, "/")

	var result map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := obj.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&result)
	if err != nil {
		log.Printf("refreshKnownNetworks: failed to get managed objects: %v", err)
		return
	}

	savedNetworks := []string{}
	for _, ifaces := range result {
		if knProps, ok := ifaces[KnownNetworkIface]; ok {
			if nameV, ok := knProps["Name"]; ok {
				ssid := nameV.Value().(string)
				savedNetworks = append(savedNetworks, ssid)
			}
		}
	}

	if len(savedNetworks) > 0 {
		c.stateMgr.Update(func(st *state.State) {
			st.SavedNetworks = savedNetworks
		})
		log.Printf("Refreshed SavedNetworks: %v", savedNetworks)
	}
}

// RefreshKnownNetworks refreshes the saved networks list from IWD
func (c *Client) RefreshKnownNetworks() {
	obj := c.conn.Object(IWDService, "/")
	var result map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := obj.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&result)
	if err != nil {
		log.Printf("Failed to refresh known networks: %v", err)
		return
	}

	var savedNetworks []string
	for _, ifaces := range result {
		if knProps, ok := ifaces[KnownNetworkIface]; ok {
			if nameV, ok := knProps["Name"]; ok {
				ssid := nameV.Value().(string)
				savedNetworks = append(savedNetworks, ssid)
			}
		}
	}

	c.stateMgr.Update(func(st *state.State) {
		st.SavedNetworks = savedNetworks
	})
	log.Printf("Refreshed known networks: %v", savedNetworks)
}

// SetWifiEnabled enables/disables WiFi
func (c *Client) SetWifiEnabled(enabled bool) error {
	obj := c.conn.Object(IWDService, c.devicePath)
	return obj.Call("org.freedesktop.DBus.Properties.Set", 0, DeviceIface, "Powered", dbus.MakeVariant(enabled)).Err
}

// Scan scans for WiFi networks
// Scan triggers a WiFi network scan (ASYNC)
// Uses IWD PropertiesChanged signal to detect scan completion (no polling)
func (c *Client) Scan() ([]state.Network, error) {
	obj := c.conn.Object(IWDService, c.stationPath)

	// Trigger scan - this returns immediately
	err := obj.Call(StationIface+".Scan", 0).Err
	if err != nil && !strings.Contains(err.Error(), "Busy") {
		log.Printf("Scan call failed: %v", err)
		return nil, err
	}

	// Wait for IWD scan to complete using PropertiesChanged signal (event-driven)
	scanDone := make(chan bool, 1)

	// Subscribe to PropertiesChanged signal on Station (with arg0 filter for Station interface)
	matchRule := fmt.Sprintf("type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',path='%s',arg0='%s'", c.stationPath, StationIface)
	c.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, matchRule)

	// Channel for receiving signals
	sigChan := make(chan *dbus.Signal, 10)
	c.conn.Signal(sigChan)

	// Goroutine to listen for Scanning property change
	go func() {
		defer func() {
			c.conn.RemoveSignal(sigChan)
			c.conn.BusObject().Call("org.freedesktop.DBus.RemoveMatch", 0, matchRule)
		}()

		for sig := range sigChan {
			if sig.Name != "org.freedesktop.DBus.Properties.PropertiesChanged" {
				continue
			}
			if sig.Path != c.stationPath {
				continue
			}
			if len(sig.Body) < 2 {
				continue
			}

			// Parse changed properties
			iface, ok := sig.Body[0].(string)
			if !ok || iface != StationIface {
				continue
			}

			changed, ok := sig.Body[1].(map[string]dbus.Variant)
			if !ok {
				continue
			}

			if scanningVar, ok := changed["Scanning"]; ok {
				if scanning, ok := scanningVar.Value().(bool); ok && !scanning {
					log.Printf("Scan completed (signal received)")
					scanDone <- true
					return
				}
			}
		}
	}()

	// Wait for scan completion with 15s timeout fallback
	select {
	case <-scanDone:
		// Signal received - scan completed
	case <-time.After(15 * time.Second):
		log.Printf("Scan timeout after 15s, proceeding anyway")
	}

	// Fetch fresh network list
	networks := c.fetchNetworksFromIWD()

	// If no networks found, retry GetOrderedNetworks after a short delay
	// (IWD sometimes needs time to populate results)
	if len(networks) == 0 {
		log.Printf("First fetch returned 0 networks, retrying after 1s...")
		time.Sleep(1 * time.Second)
		networks = c.fetchNetworksFromIWD()
	}

	// Update state so UI receives fresh network list via PropertyChanged signal
	if networks != nil {
		c.stateMgr.Update(func(st *state.State) {
			st.Networks = networks
		})
	}

	return networks, nil
}

// fetchNetworksFromIWD fetches the current network list from IWD
// Called from signal handler when scan completes
func (c *Client) fetchNetworksFromIWD() []state.Network {
	obj := c.conn.Object(IWDService, c.stationPath)

	var result []struct {
		Path dbus.ObjectPath
		RSSI int16
	}
	call := obj.Call(StationIface+".GetOrderedNetworks", 0)
	if call.Err != nil {
		log.Printf("GetOrderedNetworks call failed: %v", call.Err)
		return nil
	}

	if err := call.Store(&result); err != nil {
		log.Printf("GetOrderedNetworks Store failed: %v", err)
		return nil
	}

	log.Printf("GetOrderedNetworks returned %d entries", len(result))

	// Get current ActiveSSID to properly set Connected flag
	currentState := c.stateMgr.Get()
	activeSSID := currentState.ActiveSSID

	networks := make([]state.Network, 0, len(result))
	for _, r := range result {
		log.Printf("Processing network path=%s rssi=%d", r.Path, r.RSSI)
		net := c.getNetworkInfo(r.Path, r.RSSI)
		if net != nil {
			// Override Connected based on ActiveSSID (more reliable than IWD Network.Connected)
			if net.SSID == activeSSID && activeSSID != "" {
				net.Connected = true
			}
			networks = append(networks, *net)
		}
	}

	return networks
}

// getNetworkInfo gets info for a network
func (c *Client) getNetworkInfo(path dbus.ObjectPath, rssi int16) *state.Network {
	obj := c.conn.Object(IWDService, path)

	var props map[string]dbus.Variant
	err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, NetworkIface).Store(&props)
	if err != nil {
		return nil
	}

	net := &state.Network{
		ObjectPath: string(path),
		SignalDBm:  rssi / 100, // IWD returns 1/100 dBm units, convert to dBm
		Signal:     state.DBmToPercent(rssi / 100),
	}

	if v, ok := props["Name"]; ok {
		net.SSID = v.Value().(string)
	}
	if v, ok := props["Type"]; ok {
		net.Security = v.Value().(string)
	}
	if v, ok := props["Connected"]; ok {
		net.Connected = v.Value().(bool)
	}
	if v, ok := props["KnownNetwork"]; ok {
		net.Saved = v.Value().(dbus.ObjectPath) != ""
	}

	return net
}

// Connect connects to a network
func (c *Client) Connect(ssid, password, security string, hidden bool) error {
	// Lock to prevent concurrent connection attempts
	c.connectMu.Lock()

	// Increment connection ID for this attempt
	c.connectID++
	myConnectID := c.connectID
	log.Printf("IWD Connect called: ssid=%s, password=%d chars, security=%s, hidden=%v (connectID=%d)",
		ssid, len(password), security, hidden, myConnectID)

	// Unlock after setting up state - actual IWD call will be made without lock
	// but we hold lock during state setup to ensure atomicity
	c.connectMu.Unlock()

	// Find network by SSID
	log.Printf("Starting scan for network %s", ssid)
	networks, err := c.Scan()
	if err != nil {
		log.Printf("Scan failed: %v", err)
		return err
	}
	log.Printf("Scan returned %d networks", len(networks))

	var networkPath string
	var networkSecurity string
	for _, net := range networks {
		if net.SSID == ssid {
			networkPath = net.ObjectPath
			networkSecurity = net.Security
			log.Printf("Found network: path=%s, security=%s", networkPath, networkSecurity)
			break
		}
	}

	if networkPath == "" && !hidden {
		log.Printf("Network not found: %s", ssid)
		return fmt.Errorf("network not found: %s", ssid)
	}

	// For PSK/SAE networks with password, set pending credential for agent
	// IWD will call Agent.RequestPassphrase to get the password
	netPath := dbus.ObjectPath(networkPath)
	if password != "" && (networkSecurity == "psk" || security == "psk" || networkSecurity == "wpa2" || networkSecurity == "wpa3") {
		if c.agent != nil {
			c.agent.SetPending(netPath, password)
		} else {
			log.Printf("Warning: Agent not available, connection may require saved credentials")
		}
	}

	// Set ConnectingSSID so UI knows which network is being connected
	c.stateMgr.Update(func(st *state.State) {
		st.ConnectingSSID = ssid
	})

	if hidden {
		// Connect to hidden network
		log.Printf("Connecting to hidden network %s", ssid)
		obj := c.conn.Object(IWDService, c.stationPath)
		err := obj.Call(StationIface+".ConnectHiddenNetwork", 0, ssid).Err

		// Clear ConnectingSSID only if this is still the current connection attempt
		c.connectMu.Lock()
		if c.connectID == myConnectID {
			c.stateMgr.Update(func(st *state.State) {
				st.ConnectingSSID = ""
			})
		} else {
			log.Printf("Skipping state clear - stale callback (myID=%d, currentID=%d)", myConnectID, c.connectID)
		}
		c.connectMu.Unlock()

		if err != nil && c.agent != nil {
			c.agent.ClearPending(netPath)
		}
		return err
	}

	// Connect to visible network
	log.Printf("Calling IWD Network.Connect on %s", networkPath)
	obj := c.conn.Object(IWDService, netPath)
	err = obj.Call(NetworkIface+".Connect", 0).Err

	// Clear ConnectingSSID only if this is still the current connection attempt
	c.connectMu.Lock()
	if c.connectID == myConnectID {
		c.stateMgr.Update(func(st *state.State) {
			st.ConnectingSSID = ""
		})
	} else {
		log.Printf("Skipping state clear - stale callback (myID=%d, currentID=%d)", myConnectID, c.connectID)
	}
	c.connectMu.Unlock()

	if err != nil {
		log.Printf("IWD Network.Connect failed: %v", err)
		// Clear pending credential on failure
		if c.agent != nil {
			c.agent.ClearPending(netPath)
		}
	} else {
		log.Printf("IWD Network.Connect succeeded")
	}
	return err
}

// writeIWDConfig writes the password to IWD config file using sudo
func (c *Client) writeIWDConfig(ssid, password, security string) error {
	// IWD stores configs in /var/lib/iwd/SSID.psk (or .open, .8021x)
	configPath := fmt.Sprintf("/var/lib/iwd/%s.%s", ssid, security)

	// Use printf for proper newline handling, pipe to tee for sudo write
	// Format: [Security]\nPassphrase=xxx\n
	cmd := exec.Command("sudo", "tee", configPath)
	cmd.Stdin = strings.NewReader(fmt.Sprintf("[Security]\nPassphrase=%s\n", password))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to write IWD config: %w", err)
	}

	// Set permissions (IWD requires 600)
	chmodCmd := exec.Command("sudo", "chmod", "600", configPath)
	if err := chmodCmd.Run(); err != nil {
		log.Printf("Warning: failed to chmod IWD config: %v", err)
	}

	log.Printf("Wrote IWD config for %s", ssid)
	return nil
}

// ConnectSaved connects to a saved network
func (c *Client) ConnectSaved(ssid string) error {
	// For saved networks, we need to find the KnownNetwork and trigger connect
	return c.Connect(ssid, "", "", false)
}

// Disconnect disconnects from current network
func (c *Client) Disconnect() error {
	obj := c.conn.Object(IWDService, c.stationPath)
	return obj.Call(StationIface+".Disconnect", 0).Err
}

// Forget forgets a saved network
func (c *Client) Forget(ssid string) error {
	// Find known network by SSID
	obj := c.conn.Object(IWDService, "/")

	var result map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := obj.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&result)
	if err != nil {
		return err
	}

	for path, ifaces := range result {
		if knProps, ok := ifaces[KnownNetworkIface]; ok {
			if v, ok := knProps["Name"]; ok && v.Value().(string) == ssid {
				knObj := c.conn.Object(IWDService, path)
				return knObj.Call(KnownNetworkIface+".Forget", 0).Err
			}
		}
	}

	return fmt.Errorf("known network not found: %s", ssid)
}

// SetAutoConnect sets auto-connect for a network
func (c *Client) SetAutoConnect(ssid string, enabled bool) error {
	// Find known network
	obj := c.conn.Object(IWDService, "/")

	var result map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := obj.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&result)
	if err != nil {
		return err
	}

	for path, ifaces := range result {
		if knProps, ok := ifaces[KnownNetworkIface]; ok {
			if v, ok := knProps["Name"]; ok && v.Value().(string) == ssid {
				knObj := c.conn.Object(IWDService, path)
				return knObj.Call("org.freedesktop.DBus.Properties.Set", 0,
					KnownNetworkIface, "AutoConnect", dbus.MakeVariant(enabled)).Err
			}
		}
	}

	return fmt.Errorf("known network not found: %s", ssid)
}

// StartHotspot starts WiFi hotspot
func (c *Client) StartHotspot(ssid, password string) error {
	// Switch to AP mode
	obj := c.conn.Object(IWDService, c.devicePath)
	err := obj.Call("org.freedesktop.DBus.Properties.Set", 0, DeviceIface, "Mode", dbus.MakeVariant("ap")).Err
	if err != nil {
		return err
	}

	// Start AP with profile
	apObj := c.conn.Object(IWDService, c.devicePath)
	return apObj.Call(AccessPointIface+".Start", 0, ssid, password).Err
}

// StopHotspot stops WiFi hotspot
func (c *Client) StopHotspot() error {
	apObj := c.conn.Object(IWDService, c.devicePath)
	err := apObj.Call(AccessPointIface+".Stop", 0).Err
	if err != nil {
		return err
	}

	// Switch back to station mode
	obj := c.conn.Object(IWDService, c.devicePath)
	return obj.Call("org.freedesktop.DBus.Properties.Set", 0, DeviceIface, "Mode", dbus.MakeVariant("station")).Err
}

// tryUsbFallback attempts to establish USB tethering connection as fallback
func (c *Client) tryUsbFallback(ifaceName string) {
	log.Printf("Attempting USB tethering fallback on %s", ifaceName)

	// Bring up the interface (requires sudo)
	if err := exec.Command("sudo", "ip", "link", "set", ifaceName, "up").Run(); err != nil {
		log.Printf("Failed to bring up USB interface %s: %v", ifaceName, err)
		return
	}

	// Run dhcpcd to get IP address (requires sudo)
	log.Printf("Running DHCP on USB interface %s", ifaceName)
	cmd := exec.Command("sudo", "dhcpcd", "-4", "-w", ifaceName)
	if err := cmd.Run(); err != nil {
		log.Printf("DHCP failed on USB interface %s: %v", ifaceName, err)
		return
	}

	log.Printf("USB tethering fallback established on %s", ifaceName)

	// Update state
	c.stateMgr.Update(func(st *state.State) {
		st.UsbTetheringConnected = true
		st.ConnectionType = "usb"
	})
}
