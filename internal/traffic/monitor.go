package traffic

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"x-network/internal/state"
)

const (
	sysClassNet    = "/sys/class/net"
	updateInterval = 1 * time.Second
	minDeltaBytes  = 100 // Only emit if delta > 100 bytes
)

// Monitor monitors network traffic
type Monitor struct {
	stateMgr *state.Manager
	stopCh   chan struct{}
	running  atomic.Bool

	lastRx      uint64
	lastTx      uint64
	idleEmitted bool // Track if we've emitted 0,0 to avoid repeated emissions
}

// NewMonitor creates a new traffic monitor
func NewMonitor(stateMgr *state.Manager) *Monitor {
	return &Monitor{
		stateMgr: stateMgr,
		stopCh:   make(chan struct{}),
	}
}

// Run starts the traffic monitoring loop
func (m *Monitor) Run() {
	if !m.running.CompareAndSwap(false, true) {
		return
	}

	ticker := time.NewTicker(updateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.sample()
		}
	}
}

// Stop stops the traffic monitor
func (m *Monitor) Stop() {
	if m.running.CompareAndSwap(true, false) {
		close(m.stopCh)
	}
}

// sample samples current traffic and calculates delta
func (m *Monitor) sample() {
	st := m.stateMgr.Get()

	// Get active interface - prefer WiFi, fallback to USB tethering
	iface := st.InterfaceName

	// If WiFi not connected and USB tethering is active, use USB interface
	if (iface == "" || st.ConnectionState != state.StateConnected) && st.UsbTetheringConnected && st.UsbInterfaceName != "" {
		iface = st.UsbInterfaceName
	}

	if iface == "" {
		iface = m.findActiveInterface()
		if iface == "" {
			return
		}
	}

	rx, tx := m.readStats(iface)
	if rx == 0 && tx == 0 {
		return
	}

	// Calculate delta
	var deltaRx, deltaTx uint64
	if m.lastRx > 0 {
		deltaRx = rx - m.lastRx
		deltaTx = tx - m.lastTx
	}
	m.lastRx = rx
	m.lastTx = tx

	// Only update if significant traffic (delta > threshold)
	if deltaRx > minDeltaBytes || deltaTx > minDeltaBytes {
		m.stateMgr.Update(func(s *state.State) {
			s.TrafficIn = deltaRx
			s.TrafficOut = deltaTx
			s.InterfaceName = iface
		})
		m.idleEmitted = false // Reset so we can emit zero once when idle
	} else if (deltaRx == 0 && deltaTx == 0) && !m.idleEmitted {
		// Reset to 0 ONCE when truly idle, not every second
		m.stateMgr.Update(func(s *state.State) {
			s.TrafficIn = 0
			s.TrafficOut = 0
		})
		m.idleEmitted = true // Don't emit again until traffic resumes
	}
}

// readStats reads RX/TX bytes from sysfs
func (m *Monitor) readStats(iface string) (rx, tx uint64) {
	rxPath := filepath.Join(sysClassNet, iface, "statistics/rx_bytes")
	txPath := filepath.Join(sysClassNet, iface, "statistics/tx_bytes")

	rx = readUint64File(rxPath)
	tx = readUint64File(txPath)
	return
}

// findActiveInterface finds an active network interface
func (m *Monitor) findActiveInterface() string {
	entries, err := os.ReadDir(sysClassNet)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "lo" {
			continue
		}

		// Check if interface is up
		operstate := filepath.Join(sysClassNet, name, "operstate")
		data, err := os.ReadFile(operstate)
		if err != nil {
			continue
		}

		if strings.TrimSpace(string(data)) == "up" {
			// Prioritize wireless interfaces
			if strings.HasPrefix(name, "wl") {
				return name
			}
			// Or return first up interface
			return name
		}
	}

	return ""
}

// readUint64File reads a uint64 from a file
func readUint64File(path string) uint64 {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		val, err := strconv.ParseUint(strings.TrimSpace(scanner.Text()), 10, 64)
		if err != nil {
			return 0
		}
		return val
	}
	return 0
}
