package state

import (
	"sync"
	"time"
)

// ConnectionState represents WiFi connection state
type ConnectionState string

const (
	StateDisconnected ConnectionState = "disconnected"
	StateConnecting   ConnectionState = "connecting"
	StateObtaining    ConnectionState = "obtaining" // DHCP in progress
	StateConnected    ConnectionState = "connected"
	StateFailed       ConnectionState = "failed"
)

// Network represents a WiFi network
type Network struct {
	SSID       string
	Security   string // "open", "psk", "sae", "8021x"
	SignalDBm  int16  // Raw RSSI in dBm
	Signal     uint8  // Derived percentage 0-100
	Connected  bool
	Saved      bool
	Frequency  uint32 // MHz
	ObjectPath string // IWD D-Bus path
}

// State holds all network state
type State struct {
	// WiFi state
	WifiEnabled     bool
	WifiScanning    bool
	ConnectionState ConnectionState

	// Active connection
	ActiveSSID     string
	ConnectingSSID string // Set during connection attempt, cleared on success/failure
	ActiveSecurity string
	SignalRSSI     int16
	SignalStrength uint8
	Frequency      uint32

	// Network info
	InterfaceName string
	MacAddress    string
	IpAddress     string
	Gateway       string

	// Traffic (bytes/sec)
	TrafficIn  uint64
	TrafficOut uint64

	// Network lists
	Networks      []Network
	SavedNetworks []string

	// Features
	AirplaneMode          bool
	CaptivePortalDetected  bool
	CaptivePortalURL       string
	LastCaptiveCheckSSID   string // Guard: last SSID checked for captive portal (reset on disconnect)
	HotspotActive          bool
	HotspotSSID           string

	// Connection type
	ConnectionType string // "wifi", "ethernet", "usb"

	// USB Tethering state
	UsbInterfaceDetected  bool   // USB interface exists
	UsbTetheringAvailable bool   // Phone ready (carrier up)
	UsbTetheringConnected bool   // IP + route (actually usable)
	UsbInterfaceName      string // e.g., "enp0s26u1u2"
	UsbInterfaceIndex     uint32 // ifindex - stable identifier

	// Error reporting
	LastError string // Last error message for UI feedback

	// Resume tracking for weather refresh (internal, not exposed via D-Bus)
	WasResumed       bool      // Set by PrepareForSleep(false)
	ResumeTimestamp  time.Time // When resume happened
	WeatherTriggered bool      // Dedup: prevent double trigger

	// Startup tracking - trigger weather on first network connection at boot
	IsStartup bool // Set true at daemon start, cleared after first weather trigger
}

// Manager manages state with thread-safe access
type Manager struct {
	mu       sync.RWMutex
	state    State
	onChange func(*State) // Callback when state changes
}

// NewManager creates a new state manager
func NewManager() *Manager {
	return &Manager{
		state: State{
			ConnectionState: StateDisconnected,
		},
	}
}

// SetOnChange sets the callback for state changes
func (m *Manager) SetOnChange(fn func(*State)) {
	m.mu.Lock()
	m.onChange = fn
	m.mu.Unlock()
}

// Get returns a copy of current state
func (m *Manager) Get() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// Update atomically updates state and triggers callback
func (m *Manager) Update(fn func(*State)) {
	m.mu.Lock()
	fn(&m.state)
	stateCopy := m.state
	onChange := m.onChange
	m.mu.Unlock()

	if onChange != nil {
		onChange(&stateCopy)
	}
}

// Helper: Convert dBm to percentage
func DBmToPercent(dBm int16) uint8 {
	// Linear scale: -100 dBm = 0%, -50 dBm = 100%
	if dBm <= -100 {
		return 0
	}
	if dBm >= -50 {
		return 100
	}
	return uint8(2 * (int(dBm) + 100))
}

// Helper: Get band from frequency
func FrequencyToBand(freq uint32) string {
	if freq >= 2400 && freq < 2500 {
		return "2.4GHz"
	}
	if freq >= 5000 && freq < 6000 {
		return "5GHz"
	}
	if freq >= 6000 {
		return "6GHz"
	}
	return "unknown"
}
