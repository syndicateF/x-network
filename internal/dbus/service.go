package dbus

import (
	"fmt"
	"log"

	"x-network/internal/iwd"
	"x-network/internal/state"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

const (
	ServiceName = "org.xshell.Network"
	ObjectPath  = "/org/xshell/Network"
	Interface   = "org.xshell.Network"
)

// Service represents the D-Bus service
type Service struct {
	conn     *dbus.Conn
	stateMgr *state.Manager
	iwd      *iwd.Client
}

// NewService creates and registers the D-Bus service
func NewService(busType string, stateMgr *state.Manager, iwdClient *iwd.Client) (*Service, error) {
	var conn *dbus.Conn
	var err error

	if busType == "system" {
		conn, err = dbus.SystemBus()
	} else {
		conn, err = dbus.SessionBus()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect to D-Bus: %w", err)
	}

	s := &Service{
		conn:     conn,
		stateMgr: stateMgr,
		iwd:      iwdClient,
	}

	// Request service name
	reply, err := conn.RequestName(ServiceName, dbus.NameFlagDoNotQueue)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to request name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return nil, fmt.Errorf("name already taken")
	}

	// Export the service object
	if err := conn.Export(s, ObjectPath, Interface); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to export: %w", err)
	}

	// Export the Properties interface
	if err := conn.Export(s, ObjectPath, "org.freedesktop.DBus.Properties"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to export properties: %w", err)
	}

	// Export introspection
	node := &introspect.Node{
		Name: ObjectPath,
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			{
				Name:       Interface,
				Methods:    s.methods(),
				Properties: s.properties(),
				Signals:    s.signals(),
			},
		},
	}
	conn.Export(introspect.NewIntrospectable(node), ObjectPath, "org.freedesktop.DBus.Introspectable")

	// Subscribe to state changes
	stateMgr.SetOnChange(s.onStateChange)

	return s, nil
}

// Close closes the D-Bus connection
func (s *Service) Close() {
	s.conn.Close()
}

// onStateChange handles state updates and emits signals
func (s *Service) onStateChange(st *state.State) {
	// Emit property changed signals
	s.emitPropertiesChanged(st)
}

// emitPropertiesChanged emits PropertyChanged for modified properties
func (s *Service) emitPropertiesChanged(st *state.State) {
	changed := map[string]dbus.Variant{
		"WifiEnabled":           dbus.MakeVariant(st.WifiEnabled),
		"WifiScanning":          dbus.MakeVariant(st.WifiScanning),
		"ConnectionState":       dbus.MakeVariant(string(st.ConnectionState)),
		"ActiveSSID":            dbus.MakeVariant(st.ActiveSSID),
		"SignalRSSI":            dbus.MakeVariant(st.SignalRSSI),
		"SignalStrength":        dbus.MakeVariant(st.SignalStrength),
		"IpAddress":             dbus.MakeVariant(st.IpAddress),
		"Gateway":               dbus.MakeVariant(st.Gateway),
		"TrafficIn":             dbus.MakeVariant(st.TrafficIn),
		"TrafficOut":            dbus.MakeVariant(st.TrafficOut),
		"AirplaneMode":          dbus.MakeVariant(st.AirplaneMode),
		"CaptivePortalDetected": dbus.MakeVariant(st.CaptivePortalDetected),
		"HotspotActive":         dbus.MakeVariant(st.HotspotActive),
	}

	err := s.conn.Emit(ObjectPath, "org.freedesktop.DBus.Properties.PropertiesChanged",
		Interface, changed, []string{})
	if err != nil {
		log.Printf("Failed to emit PropertiesChanged: %v", err)
	}
}

// EmitSignal emits a custom signal
func (s *Service) EmitSignal(name string, values ...interface{}) {
	err := s.conn.Emit(ObjectPath, Interface+"."+name, values...)
	if err != nil {
		log.Printf("Failed to emit %s: %v", name, err)
	}
}

// methods returns introspection method definitions
func (s *Service) methods() []introspect.Method {
	return []introspect.Method{
		{Name: "EnableWifi", Args: []introspect.Arg{
			{Name: "enabled", Type: "b", Direction: "in"},
			{Name: "success", Type: "b", Direction: "out"},
		}},
		{Name: "Scan"},
		{Name: "Connect", Args: []introspect.Arg{
			{Name: "params", Type: "a{sv}", Direction: "in"},
			{Name: "success", Type: "b", Direction: "out"},
		}},
		{Name: "ConnectSaved", Args: []introspect.Arg{
			{Name: "ssid", Type: "s", Direction: "in"},
			{Name: "success", Type: "b", Direction: "out"},
		}},
		{Name: "Disconnect"},
		{Name: "Forget", Args: []introspect.Arg{
			{Name: "ssid", Type: "s", Direction: "in"},
			{Name: "success", Type: "b", Direction: "out"},
		}},
		{Name: "SetAutoConnect", Args: []introspect.Arg{
			{Name: "ssid", Type: "s", Direction: "in"},
			{Name: "enabled", Type: "b", Direction: "in"},
			{Name: "success", Type: "b", Direction: "out"},
		}},
		{Name: "StartHotspot", Args: []introspect.Arg{
			{Name: "ssid", Type: "s", Direction: "in"},
			{Name: "password", Type: "s", Direction: "in"},
			{Name: "success", Type: "b", Direction: "out"},
		}},
		{Name: "StopHotspot"},
		{Name: "SetAirplaneMode", Args: []introspect.Arg{
			{Name: "enabled", Type: "b", Direction: "in"},
			{Name: "success", Type: "b", Direction: "out"},
		}},
		{Name: "CheckCaptivePortal", Args: []introspect.Arg{
			{Name: "detected", Type: "b", Direction: "out"},
		}},
		{Name: "OpenCaptivePortal"},
		// USB Tethering methods
		{Name: "RequestUsbNetwork", Args: []introspect.Arg{
			{Name: "success", Type: "b", Direction: "out"},
		}},
		{Name: "ReleaseUsbNetwork"},
	}
}

// properties returns introspection property definitions
func (s *Service) properties() []introspect.Property {
	return []introspect.Property{
		{Name: "WifiEnabled", Type: "b", Access: "read"},
		{Name: "WifiScanning", Type: "b", Access: "read"},
		{Name: "ConnectionState", Type: "s", Access: "read"},
		{Name: "ActiveSSID", Type: "s", Access: "read"},
		{Name: "ActiveSecurity", Type: "s", Access: "read"},
		{Name: "SignalRSSI", Type: "n", Access: "read"},
		{Name: "SignalStrength", Type: "y", Access: "read"},
		{Name: "Frequency", Type: "u", Access: "read"},
		{Name: "IpAddress", Type: "s", Access: "read"},
		{Name: "Gateway", Type: "s", Access: "read"},
		{Name: "MacAddress", Type: "s", Access: "read"},
		{Name: "InterfaceName", Type: "s", Access: "read"},
		{Name: "TrafficIn", Type: "t", Access: "read"},
		{Name: "TrafficOut", Type: "t", Access: "read"},
		{Name: "Networks", Type: "a(ssybu)", Access: "read"},
		{Name: "SavedNetworks", Type: "as", Access: "read"},
		{Name: "AirplaneMode", Type: "b", Access: "read"},
		{Name: "CaptivePortalDetected", Type: "b", Access: "read"},
		{Name: "HotspotActive", Type: "b", Access: "read"},
		{Name: "ConnectionType", Type: "s", Access: "read"},
		{Name: "Band", Type: "s", Access: "read"},
		// USB Tethering properties
		{Name: "UsbInterfaceDetected", Type: "b", Access: "read"},
		{Name: "UsbTetheringAvailable", Type: "b", Access: "read"},
		{Name: "UsbTetheringConnected", Type: "b", Access: "read"},
		{Name: "UsbInterfaceName", Type: "s", Access: "read"},
	}
}

// signals returns introspection signal definitions
func (s *Service) signals() []introspect.Signal {
	return []introspect.Signal{
		{Name: "WifiStateChanged", Args: []introspect.Arg{{Name: "enabled", Type: "b"}}},
		{Name: "ScanStarted"},
		{Name: "ScanCompleted"},
		{Name: "NetworksChanged", Args: []introspect.Arg{{Name: "networks", Type: "a(ssybu)"}}},
		{Name: "ConnectionChanged", Args: []introspect.Arg{
			{Name: "state", Type: "s"},
			{Name: "ssid", Type: "s"},
			{Name: "signal", Type: "y"},
		}},
		{Name: "TrafficUpdated", Args: []introspect.Arg{
			{Name: "inBytes", Type: "t"},
			{Name: "outBytes", Type: "t"},
		}},
		{Name: "AddressChanged", Args: []introspect.Arg{
			{Name: "ip", Type: "s"},
			{Name: "gateway", Type: "s"},
		}},
		{Name: "InterfaceChanged", Args: []introspect.Arg{
			{Name: "iface", Type: "s"},
			{Name: "isUp", Type: "b"},
		}},
		{Name: "CaptivePortalStatus", Args: []introspect.Arg{
			{Name: "detected", Type: "b"},
			{Name: "url", Type: "s"},
		}},
		{Name: "Error", Args: []introspect.Arg{
			{Name: "operation", Type: "s"},
			{Name: "message", Type: "s"},
		}},
	}
}
