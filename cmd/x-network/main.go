package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"x-network/internal/dbus"
	"x-network/internal/iwd"
	"x-network/internal/netlink"
	"x-network/internal/state"
	"x-network/internal/traffic"

	gobus "github.com/godbus/dbus/v5"
)

var (
	busType = flag.String("bus", "session", "D-Bus bus type: session or system")
	debug   = flag.Bool("debug", false, "Enable debug logging")
)

func main() {
	flag.Parse()

	if *debug {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	}

	log.Println("x-network daemon starting...")

	// Initialize state manager
	stateMgr := state.NewManager()

	// Mark as startup - will trigger weather fetch on first network connection
	stateMgr.Update(func(st *state.State) {
		st.IsStartup = true
	})

	// Initialize IWD client
	iwdClient, err := iwd.NewClient(stateMgr)
	if err != nil {
		log.Printf("Warning: IWD not available: %v", err)
		// Continue without WiFi support
	} else {
		defer iwdClient.Close()
		log.Println("IWD client connected")
	}

	// Initialize netlink watcher
	nlWatcher, err := netlink.NewWatcher(stateMgr)
	if err != nil {
		log.Printf("Warning: Netlink watcher failed: %v", err)
	} else {
		defer nlWatcher.Close()
		go nlWatcher.Run()
		log.Println("Netlink watcher started")
	}

	// Initialize traffic monitor
	trafficMon := traffic.NewMonitor(stateMgr)
	go trafficMon.Run()
	defer trafficMon.Stop()
	log.Println("Traffic monitor started")

	// Initialize D-Bus service
	dbusService, err := dbus.NewService(*busType, stateMgr, iwdClient)
	if err != nil {
		log.Fatalf("Failed to start D-Bus service: %v", err)
	}
	defer dbusService.Close()
	log.Printf("D-Bus service registered on %s bus", *busType)

	// Watch for system resume to trigger weather refresh and accelerate reconnect
	go watchSystemResume(stateMgr, iwdClient)
	log.Println("System resume watcher started")

	// Wait for signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("x-network daemon ready")
	<-sigChan
	log.Println("Shutting down...")
}

// watchSystemResume listens for PrepareForSleep D-Bus signal from logind
// Sets WasResumed flag and triggers iwd scan to accelerate reconnection
func watchSystemResume(stateMgr *state.Manager, iwdClient *iwd.Client) {
	conn, err := gobus.SystemBus()
	if err != nil {
		log.Printf("Warning: Cannot watch system resume: %v", err)
		return
	}

	// Subscribe to PrepareForSleep signal from logind
	rule := "type='signal',interface='org.freedesktop.login1.Manager',member='PrepareForSleep'"
	if err := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule).Err; err != nil {
		log.Printf("Warning: Cannot subscribe to PrepareForSleep: %v", err)
		return
	}

	ch := make(chan *gobus.Signal, 1)
	conn.Signal(ch)

	for sig := range ch {
		if sig.Name == "org.freedesktop.login1.Manager.PrepareForSleep" && len(sig.Body) > 0 {
			goingToSleep, ok := sig.Body[0].(bool)
			if !ok {
				continue
			}
			if goingToSleep {
				log.Println("System going to sleep")
			} else {
				// System resumed from sleep
				log.Println("System resumed from sleep, setting resume flag")
				stateMgr.Update(func(st *state.State) {
					st.WasResumed = true
					st.ResumeTimestamp = time.Now()
					st.WeatherTriggered = false // Reset dedup flag
				})

				// Trigger iwd scan to accelerate reconnection
				// iwd's autoconnect_full can be slow; scan forces faster reconnect
				if iwdClient != nil {
					log.Println("Triggering WiFi scan to accelerate reconnection")
					go iwdClient.Scan()
				}
			}
		}
	}
}
