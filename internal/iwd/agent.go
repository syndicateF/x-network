package iwd

import (
	"log"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	AgentPath     = "/org/xshell/network/agent"
	AgentIface    = "net.connman.iwd.Agent"
	AgentMgrIface = "net.connman.iwd.AgentManager"
	CredentialTTL = 30 * time.Second
)

// PendingCredential holds credentials waiting for IWD callback
type PendingCredential struct {
	Password string
	Created  time.Time
}

// Agent implements net.connman.iwd.Agent D-Bus interface
// IWD calls RequestPassphrase when it needs a password for PSK/SAE networks
type Agent struct {
	conn    *dbus.Conn
	client  *Client
	mu      sync.RWMutex
	pending map[dbus.ObjectPath]PendingCredential
}

// NewAgent creates a new IWD Agent
func NewAgent(conn *dbus.Conn, client *Client) *Agent {
	return &Agent{
		conn:    conn,
		client:  client,
		pending: make(map[dbus.ObjectPath]PendingCredential),
	}
}

// SetPending stores a password for the given network path
// Called by Connect() before triggering Network.Connect
func (a *Agent) SetPending(network dbus.ObjectPath, password string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	log.Printf("Agent: Setting pending credential for %s (%d chars)", network, len(password))
	a.pending[network] = PendingCredential{
		Password: password,
		Created:  time.Now(),
	}
}

// ClearPending removes a pending credential (on failure or timeout)
func (a *Agent) ClearPending(network dbus.ObjectPath) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pending, network)
}

// RequestPassphrase is called by IWD when it needs a password
// This is the core Agent callback for PSK/SAE networks
func (a *Agent) RequestPassphrase(network dbus.ObjectPath) (string, *dbus.Error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	log.Printf("Agent: RequestPassphrase called for %s", network)

	cred, ok := a.pending[network]
	if !ok {
		log.Printf("Agent: No pending credential for %s", network)
		return "", dbus.NewError(AgentIface+".Error.Canceled",
			[]interface{}{"No credential available"})
	}

	// Check TTL - expire stale credentials
	if time.Since(cred.Created) > CredentialTTL {
		log.Printf("Agent: Credential for %s expired (age: %v)", network, time.Since(cred.Created))
		delete(a.pending, network)
		return "", dbus.NewError(AgentIface+".Error.Canceled",
			[]interface{}{"Credential expired"})
	}

	// Clean up after use
	delete(a.pending, network)
	log.Printf("Agent: Returning password for %s (%d chars)", network, len(cred.Password))
	return cred.Password, nil
}

// RequestPrivateKeyPassphrase is called for 802.1x networks
// Not supported - return error
func (a *Agent) RequestPrivateKeyPassphrase(network dbus.ObjectPath) (string, *dbus.Error) {
	log.Printf("Agent: RequestPrivateKeyPassphrase not supported for %s", network)
	return "", dbus.NewError(AgentIface+".Error.Canceled",
		[]interface{}{"Private key passphrase not supported"})
}

// RequestUserNameAndPassword is called for 802.1x EAP networks
// Not supported - return error
func (a *Agent) RequestUserNameAndPassword(network dbus.ObjectPath) (string, string, *dbus.Error) {
	log.Printf("Agent: RequestUserNameAndPassword not supported for %s", network)
	return "", "", dbus.NewError(AgentIface+".Error.Canceled",
		[]interface{}{"User/password authentication not supported"})
}

// RequestUserPassword is called for some EAP networks
// Not supported - return error
func (a *Agent) RequestUserPassword(network dbus.ObjectPath, user string) (string, *dbus.Error) {
	log.Printf("Agent: RequestUserPassword not supported for %s", network)
	return "", dbus.NewError(AgentIface+".Error.Canceled",
		[]interface{}{"User password authentication not supported"})
}

// Cancel is called by IWD when a request is cancelled
// Reasons: "out-of-range", "user-canceled", "timed-out", "shutdown"
func (a *Agent) Cancel(reason string) *dbus.Error {
	log.Printf("Agent: Request cancelled: %s", reason)

	// Clear all pending to prevent stale state
	a.mu.Lock()
	a.pending = make(map[dbus.ObjectPath]PendingCredential)
	a.mu.Unlock()

	return nil
}

// Release is called by IWD when the agent is unregistered
func (a *Agent) Release() *dbus.Error {
	log.Printf("Agent: Released by IWD")

	// Clear all pending
	a.mu.Lock()
	a.pending = make(map[dbus.ObjectPath]PendingCredential)
	a.mu.Unlock()

	return nil
}

// RegisterWithIWD exports the agent on system bus and registers with AgentManager
func (a *Agent) RegisterWithIWD() error {
	// Export agent object on system D-Bus
	// Note: This uses the same connection as the IWD client (system bus)
	err := a.conn.Export(a, dbus.ObjectPath(AgentPath), AgentIface)
	if err != nil {
		return err
	}

	log.Printf("Agent: Exported at %s", AgentPath)

	// Register with IWD AgentManager
	obj := a.conn.Object(IWDService, "/net/connman/iwd")
	call := obj.Call(AgentMgrIface+".RegisterAgent", 0, dbus.ObjectPath(AgentPath))
	if call.Err != nil {
		return call.Err
	}

	log.Printf("Agent: Registered with IWD AgentManager")
	return nil
}

// UnregisterFromIWD unregisters the agent from IWD
func (a *Agent) UnregisterFromIWD() error {
	obj := a.conn.Object(IWDService, "/net/connman/iwd")
	return obj.Call(AgentMgrIface+".UnregisterAgent", 0, dbus.ObjectPath(AgentPath)).Err
}
