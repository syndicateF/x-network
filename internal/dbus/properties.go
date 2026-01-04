package dbus

import (
	"x-network/internal/state"

	"github.com/godbus/dbus/v5"
)

// Properties interface implementation for org.freedesktop.DBus.Properties

// Get implements org.freedesktop.DBus.Properties.Get
func (s *Service) Get(iface, propName string) (dbus.Variant, *dbus.Error) {
	if iface != Interface {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", []interface{}{"Unknown interface"})
	}

	st := s.stateMgr.Get()

	switch propName {
	case "WifiEnabled":
		return dbus.MakeVariant(st.WifiEnabled), nil
	case "WifiScanning":
		return dbus.MakeVariant(st.WifiScanning), nil
	case "ConnectionState":
		return dbus.MakeVariant(string(st.ConnectionState)), nil
	case "ActiveSSID":
		return dbus.MakeVariant(st.ActiveSSID), nil
	case "ConnectingSSID":
		return dbus.MakeVariant(st.ConnectingSSID), nil
	case "ActiveSecurity":
		return dbus.MakeVariant(st.ActiveSecurity), nil
	case "SignalRSSI":
		return dbus.MakeVariant(st.SignalRSSI), nil
	case "SignalStrength":
		return dbus.MakeVariant(st.SignalStrength), nil
	case "Frequency":
		return dbus.MakeVariant(st.Frequency), nil
	case "IpAddress":
		return dbus.MakeVariant(st.IpAddress), nil
	case "Gateway":
		return dbus.MakeVariant(st.Gateway), nil
	case "MacAddress":
		return dbus.MakeVariant(st.MacAddress), nil
	case "InterfaceName":
		return dbus.MakeVariant(st.InterfaceName), nil
	case "TrafficIn":
		return dbus.MakeVariant(st.TrafficIn), nil
	case "TrafficOut":
		return dbus.MakeVariant(st.TrafficOut), nil
	case "Networks":
		return dbus.MakeVariant(s.networksToDBus(st.Networks)), nil
	case "SavedNetworks":
		return dbus.MakeVariant(st.SavedNetworks), nil
	case "AirplaneMode":
		return dbus.MakeVariant(st.AirplaneMode), nil
	case "CaptivePortalDetected":
		return dbus.MakeVariant(st.CaptivePortalDetected), nil
	case "HotspotActive":
		return dbus.MakeVariant(st.HotspotActive), nil
	case "ConnectionType":
		return dbus.MakeVariant(st.ConnectionType), nil
	case "Band":
		return dbus.MakeVariant(state.FrequencyToBand(st.Frequency)), nil
	// USB Tethering properties
	case "UsbInterfaceDetected":
		return dbus.MakeVariant(st.UsbInterfaceDetected), nil
	case "UsbTetheringAvailable":
		return dbus.MakeVariant(st.UsbTetheringAvailable), nil
	case "UsbTetheringConnected":
		return dbus.MakeVariant(st.UsbTetheringConnected), nil
	case "UsbInterfaceName":
		return dbus.MakeVariant(st.UsbInterfaceName), nil
	case "LastError":
		return dbus.MakeVariant(st.LastError), nil
	default:
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", []interface{}{"Unknown property: " + propName})
	}
}

// GetAll implements org.freedesktop.DBus.Properties.GetAll
func (s *Service) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != Interface {
		return nil, dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", []interface{}{"Unknown interface"})
	}

	st := s.stateMgr.Get()

	return map[string]dbus.Variant{
		"WifiEnabled":           dbus.MakeVariant(st.WifiEnabled),
		"WifiScanning":          dbus.MakeVariant(st.WifiScanning),
		"ConnectionState":       dbus.MakeVariant(string(st.ConnectionState)),
		"ActiveSSID":            dbus.MakeVariant(st.ActiveSSID),
		"ConnectingSSID":        dbus.MakeVariant(st.ConnectingSSID), // Added - was missing!
		"ActiveSecurity":        dbus.MakeVariant(st.ActiveSecurity),
		"SignalRSSI":            dbus.MakeVariant(st.SignalRSSI),
		"SignalStrength":        dbus.MakeVariant(st.SignalStrength),
		"Frequency":             dbus.MakeVariant(st.Frequency),
		"IpAddress":             dbus.MakeVariant(st.IpAddress),
		"Gateway":               dbus.MakeVariant(st.Gateway),
		"MacAddress":            dbus.MakeVariant(st.MacAddress),
		"InterfaceName":         dbus.MakeVariant(st.InterfaceName),
		"TrafficIn":             dbus.MakeVariant(st.TrafficIn),
		"TrafficOut":            dbus.MakeVariant(st.TrafficOut),
		"Networks":              dbus.MakeVariant(s.networksToDBus(st.Networks)),
		"SavedNetworks":         dbus.MakeVariant(st.SavedNetworks),
		"AirplaneMode":          dbus.MakeVariant(st.AirplaneMode),
		"CaptivePortalDetected": dbus.MakeVariant(st.CaptivePortalDetected),
		"HotspotActive":         dbus.MakeVariant(st.HotspotActive),
		"ConnectionType":        dbus.MakeVariant(st.ConnectionType),
		"Band":                  dbus.MakeVariant(state.FrequencyToBand(st.Frequency)),
		// USB Tethering properties
		"UsbInterfaceDetected":  dbus.MakeVariant(st.UsbInterfaceDetected),
		"UsbTetheringAvailable": dbus.MakeVariant(st.UsbTetheringAvailable),
		"UsbTetheringConnected": dbus.MakeVariant(st.UsbTetheringConnected),
		"UsbInterfaceName":      dbus.MakeVariant(st.UsbInterfaceName),

		// Error reporting
		"LastError": dbus.MakeVariant(st.LastError),
	}, nil
}

// Set implements org.freedesktop.DBus.Properties.Set (read-only, returns error)
func (s *Service) Set(iface, propName string, value dbus.Variant) *dbus.Error {
	return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly", []interface{}{"Properties are read-only"})
}

// NetworkDBus represents a network for D-Bus
type NetworkDBus struct {
	SSID      string
	Security  string
	Signal    uint8
	Connected bool
	Frequency uint32
}

// networksToDBus converts networks to D-Bus format
func (s *Service) networksToDBus(networks []state.Network) []NetworkDBus {
	result := make([]NetworkDBus, len(networks))
	for i, n := range networks {
		result[i] = NetworkDBus{
			SSID:      n.SSID,
			Security:  n.Security,
			Signal:    n.Signal,
			Connected: n.Connected,
			Frequency: n.Frequency,
		}
	}
	return result
}
