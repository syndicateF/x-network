package dbus

import (
	"os/exec"
)

// setRfkill sets airplane mode via rfkill
func setRfkill(block bool) error {
	action := "unblock"
	if block {
		action = "block"
	}
	cmd := exec.Command("rfkill", action, "all")
	return cmd.Run()
}

// openURL opens a URL in the default browser
func openURL(url string) error {
	// Try common Linux browser openers
	openers := []string{"xdg-open", "gio", "gnome-open", "kde-open"}
	for _, opener := range openers {
		if path, err := exec.LookPath(opener); err == nil {
			return exec.Command(path, url).Start()
		}
	}
	return nil
}
