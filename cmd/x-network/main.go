package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"x-network/internal/dbus"
	"x-network/internal/iwd"
	"x-network/internal/netlink"
	"x-network/internal/state"
	"x-network/internal/traffic"
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

	// Wait for signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("x-network daemon ready")
	<-sigChan
	log.Println("Shutting down...")
}
