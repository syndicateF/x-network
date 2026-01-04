package dbus

import (
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
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

// checkCaptivePortal checks for captive portal by HTTP probe
func checkCaptivePortal() (detected bool, url string) {
	// Use common captive portal detection endpoints
	endpoints := []string{
		"http://detectportal.firefox.com/success.txt",
		"http://www.gstatic.com/generate_204",
		"http://captive.apple.com/hotspot-detect.html",
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Capture redirect URL
			url = req.URL.String()
			return http.ErrUseLastResponse
		},
	}

	for _, endpoint := range endpoints {
		resp, err := client.Get(endpoint)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		// Check for redirect (captive portal)
		if resp.StatusCode == 302 || resp.StatusCode == 301 {
			return true, url
		}

		// Check content for Firefox endpoint
		if strings.Contains(endpoint, "firefox") {
			body, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(body), "success") {
				return true, endpoint
			}
		}

		// Check for 204 (Google endpoint)
		if strings.Contains(endpoint, "generate_204") && resp.StatusCode != 204 {
			return true, endpoint
		}

		// Got expected response - no captive portal
		return false, ""
	}

	return false, ""
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
