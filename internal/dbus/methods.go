package dbus

import (
	"log"
	"os/exec"
	"x-network/internal/state"

	"github.com/godbus/dbus/v5"
)

// D-Bus method implementations

// EnableWifi enables or disables WiFi
func (s *Service) EnableWifi(enabled bool) (bool, *dbus.Error) {
	if s.iwd == nil {
		return false, dbus.NewError(Interface+".Error", []interface{}{"IWD not available"})
	}

	err := s.iwd.SetWifiEnabled(enabled)
	if err != nil {
		s.EmitSignal("Error", "EnableWifi", err.Error())
		return false, nil
	}

	s.stateMgr.Update(func(st *state.State) {
		st.WifiEnabled = enabled
	})
	s.EmitSignal("WifiStateChanged", enabled)
	return true, nil
}

// Scan triggers a WiFi network scan
func (s *Service) Scan() *dbus.Error {
	if s.iwd == nil {
		return dbus.NewError(Interface+".Error", []interface{}{"IWD not available"})
	}

	// Set WifiScanning=true immediately
	s.stateMgr.Update(func(st *state.State) {
		st.WifiScanning = true
	})

	go func() {
		networks, err := s.iwd.Scan()

		// Set WifiScanning=false when scan completes (regardless of success)
		s.stateMgr.Update(func(st *state.State) {
			st.WifiScanning = false
			if networks != nil {
				st.Networks = networks
			}
		})

		if err != nil {
			s.EmitSignal("Error", "Scan", err.Error())
		}
	}()

	return nil
}

// Connect connects to a network with parameters
func (s *Service) Connect(params map[string]dbus.Variant) (bool, *dbus.Error) {
	log.Printf("Connect called with %d params", len(params))

	if s.iwd == nil {
		return false, dbus.NewError(Interface+".Error", []interface{}{"IWD not available"})
	}

	// Extract parameters
	ssid := ""
	password := ""
	security := "psk"
	hidden := false

	if v, ok := params["ssid"]; ok {
		ssid = v.Value().(string)
	}
	if v, ok := params["password"]; ok {
		password = v.Value().(string)
	}
	if v, ok := params["security"]; ok {
		security = v.Value().(string)
	}
	if v, ok := params["hidden"]; ok {
		hidden = v.Value().(bool)
	}

	if ssid == "" {
		return false, dbus.NewError(Interface+".Error", []interface{}{"SSID required"})
	}

	s.stateMgr.Update(func(st *state.State) {
		st.ConnectionState = state.StateConnecting
		st.ActiveSSID = ssid
		st.LastError = "" // Clear previous error on new attempt
	})
	s.EmitSignal("ConnectionChanged", "connecting", ssid, uint8(0))

	go func() {
		err := s.iwd.Connect(ssid, password, security, hidden)
		if err != nil {
			s.stateMgr.Update(func(st *state.State) {
				st.ConnectionState = state.StateFailed
				st.LastError = err.Error() // Set error for UI to display
			})
			s.EmitSignal("Error", "Connect", err.Error())
			s.EmitSignal("ConnectionChanged", "failed", ssid, uint8(0))
		}
		// Success state will be set by IWD signal handlers
	}()

	return true, nil
}

// ConnectSaved connects to a saved network
func (s *Service) ConnectSaved(ssid string) (bool, *dbus.Error) {
	if s.iwd == nil {
		return false, dbus.NewError(Interface+".Error", []interface{}{"IWD not available"})
	}

	s.stateMgr.Update(func(st *state.State) {
		st.ConnectionState = state.StateConnecting
		st.ActiveSSID = ssid
	})
	s.EmitSignal("ConnectionChanged", "connecting", ssid, uint8(0))

	go func() {
		err := s.iwd.ConnectSaved(ssid)
		if err != nil {
			s.stateMgr.Update(func(st *state.State) {
				st.ConnectionState = state.StateFailed
			})
			s.EmitSignal("Error", "ConnectSaved", err.Error())
		}
	}()

	return true, nil
}

// Disconnect disconnects from current network
func (s *Service) Disconnect() *dbus.Error {
	if s.iwd == nil {
		return dbus.NewError(Interface+".Error", []interface{}{"IWD not available"})
	}

	st := s.stateMgr.Get()
	ssid := st.ActiveSSID

	err := s.iwd.Disconnect()
	if err != nil {
		s.EmitSignal("Error", "Disconnect", err.Error())
		return nil
	}

	s.stateMgr.Update(func(st *state.State) {
		st.ConnectionState = state.StateDisconnected
		st.ActiveSSID = ""
		st.SignalRSSI = 0
		st.SignalStrength = 0
	})
	s.EmitSignal("ConnectionChanged", "disconnected", ssid, uint8(0))

	return nil
}

// Forget forgets a saved network
func (s *Service) Forget(ssid string) (bool, *dbus.Error) {
	if s.iwd == nil {
		return false, dbus.NewError(Interface+".Error", []interface{}{"IWD not available"})
	}

	err := s.iwd.Forget(ssid)
	if err != nil {
		s.EmitSignal("Error", "Forget", err.Error())
		return false, nil
	}

	// Refresh the saved networks list after successful forget
	s.iwd.RefreshKnownNetworks()

	return true, nil
}

// SetAutoConnect enables/disables auto-connect for a network
func (s *Service) SetAutoConnect(ssid string, enabled bool) (bool, *dbus.Error) {
	if s.iwd == nil {
		return false, dbus.NewError(Interface+".Error", []interface{}{"IWD not available"})
	}

	err := s.iwd.SetAutoConnect(ssid, enabled)
	if err != nil {
		s.EmitSignal("Error", "SetAutoConnect", err.Error())
		return false, nil
	}

	return true, nil
}

// StartHotspot starts WiFi hotspot
func (s *Service) StartHotspot(ssid, password string) (bool, *dbus.Error) {
	if s.iwd == nil {
		return false, dbus.NewError(Interface+".Error", []interface{}{"IWD not available"})
	}

	err := s.iwd.StartHotspot(ssid, password)
	if err != nil {
		s.EmitSignal("Error", "StartHotspot", err.Error())
		return false, nil
	}

	s.stateMgr.Update(func(st *state.State) {
		st.HotspotActive = true
		st.HotspotSSID = ssid
	})

	return true, nil
}

// StopHotspot stops WiFi hotspot
func (s *Service) StopHotspot() *dbus.Error {
	if s.iwd == nil {
		return dbus.NewError(Interface+".Error", []interface{}{"IWD not available"})
	}

	err := s.iwd.StopHotspot()
	if err != nil {
		s.EmitSignal("Error", "StopHotspot", err.Error())
		return nil
	}

	s.stateMgr.Update(func(st *state.State) {
		st.HotspotActive = false
		st.HotspotSSID = ""
	})

	return nil
}

// SetAirplaneMode enables/disables airplane mode
func (s *Service) SetAirplaneMode(enabled bool) (bool, *dbus.Error) {
	err := setRfkill(enabled)
	if err != nil {
		s.EmitSignal("Error", "SetAirplaneMode", err.Error())
		return false, nil
	}

	s.stateMgr.Update(func(st *state.State) {
		st.AirplaneMode = enabled
	})

	return true, nil
}

// CheckCaptivePortal checks for captive portal
func (s *Service) CheckCaptivePortal() (bool, *dbus.Error) {
	detected, url := checkCaptivePortal()

	s.stateMgr.Update(func(st *state.State) {
		st.CaptivePortalDetected = detected
		st.CaptivePortalURL = url
	})
	s.EmitSignal("CaptivePortalStatus", detected, url)

	return detected, nil
}

// OpenCaptivePortal opens captive portal URL in browser
func (s *Service) OpenCaptivePortal() *dbus.Error {
	st := s.stateMgr.Get()
	if st.CaptivePortalURL != "" {
		openURL(st.CaptivePortalURL)
	}
	return nil
}

// RequestUsbNetwork requests DHCP on USB tethering interface
// This doesn't "enable" tethering (phone controls that) - just requests network
func (s *Service) RequestUsbNetwork() (bool, *dbus.Error) {
	st := s.stateMgr.Get()

	if !st.UsbInterfaceDetected {
		return false, dbus.NewError(Interface+".Error", []interface{}{"No USB network interface detected"})
	}

	if !st.UsbTetheringAvailable {
		return false, dbus.NewError(Interface+".Error", []interface{}{"USB tethering not available (no carrier)"})
	}

	if st.UsbTetheringConnected {
		return true, nil // Already connected
	}

	// Run DHCP asynchronously
	go func() {
		iface := st.UsbInterfaceName
		log.Printf("Requesting USB network on %s", iface)
		cmd := exec.Command("dhcpcd", "-4", "-q", iface)
		if err := cmd.Run(); err != nil {
			log.Printf("DHCP request failed on %s: %v", iface, err)
			s.EmitSignal("Error", "RequestUsbNetwork", err.Error())
		}
		// Success handled by netlink RTM_NEWADDR event
	}()

	return true, nil
}

// ReleaseUsbNetwork releases DHCP lease on USB tethering interface
func (s *Service) ReleaseUsbNetwork() *dbus.Error {
	st := s.stateMgr.Get()

	if st.UsbInterfaceName == "" {
		return nil // Nothing to release
	}

	// Release DHCP lease
	go func() {
		iface := st.UsbInterfaceName
		log.Printf("Releasing USB network on %s", iface)
		cmd := exec.Command("dhcpcd", "-k", iface)
		cmd.Run() // Ignore error - interface might already be gone

		s.stateMgr.Update(func(st *state.State) {
			st.UsbTetheringConnected = false
		})
	}()

	return nil
}
