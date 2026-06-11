package captive

import (
	"io"
	"net/http"
	"strings"
	"time"
)

// FallbackURL is always used when captive portal is detected but no redirect URL is found.
// neverssl.com is guaranteed to be intercepted by captive portals (no HSTS, no HTTPS).
const FallbackURL = "http://neverssl.com"

// Check probes multiple well-known captive portal detection endpoints.
// Returns (detected, portalURL). portalURL is never empty when detected=true.
//
// URL resolution priority:
//  1. HTTP redirect URL (301/302 Location header)
//  2. HTML <meta http-equiv="refresh"> URL from response body
//  3. Fallback: http://neverssl.com (always intercepted by portals)
func Check() (detected bool, portalURL string) {
	type endpoint struct {
		url      string
		validate func(resp *http.Response, body []byte) bool
	}

	endpoints := []endpoint{
		{"http://detectportal.firefox.com/success.txt", validateFirefox},
		{"http://www.gstatic.com/generate_204", validateGoogle204},
		{"http://captive.apple.com/hotspot-detect.html", validateApple},
	}

	var redirectURL string

	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Capture the redirect target — this IS the portal URL
			redirectURL = req.URL.String()
			return http.ErrUseLastResponse
		},
	}

	for _, ep := range endpoints {
		redirectURL = "" // Reset per endpoint

		resp, err := client.Get(ep.url)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Priority 1: HTTP redirect — portal is redirecting us
		if resp.StatusCode == 301 || resp.StatusCode == 302 {
			if redirectURL == "" {
				redirectURL = FallbackURL
			}
			return true, redirectURL
		}

		// Content validation — does the response match what we expect?
		if !ep.validate(resp, body) {
			// Captive detected — content doesn't match expected

			// Priority 2: HTML meta refresh URL
			if extracted := extractMetaRefreshURL(body); extracted != "" {
				return true, extracted
			}

			// Priority 3: Fallback — always have a URL to open
			return true, FallbackURL
		}

		// Content matches expected — no captive portal on this endpoint
		return false, ""
	}

	// All endpoints failed (network not ready?) — not captive
	return false, ""
}

// validateFirefox checks Firefox's success.txt endpoint.
// Expected: body contains "success" with status 200.
func validateFirefox(resp *http.Response, body []byte) bool {
	return resp.StatusCode == 200 && strings.Contains(string(body), "success")
}

// validateGoogle204 checks Google's generate_204 endpoint.
// Expected: HTTP 204 No Content.
func validateGoogle204(resp *http.Response, body []byte) bool {
	return resp.StatusCode == 204
}

// validateApple checks Apple's hotspot-detect endpoint.
// Expected: body contains "<HTML><HEAD><TITLE>Success</TITLE></HEAD><BODY>Success</BODY></HTML>".
func validateApple(resp *http.Response, body []byte) bool {
	return resp.StatusCode == 200 && strings.Contains(string(body), "Success")
}

// extractMetaRefreshURL extracts the URL from an HTML <meta http-equiv="refresh"> tag.
// This is a lightweight string scan — no HTML parser dependency.
// Matches: <meta http-equiv="refresh" content="0;url=http://...">
func extractMetaRefreshURL(body []byte) string {
	s := strings.ToLower(string(body))
	idx := strings.Index(s, "http-equiv")
	if idx == -1 {
		return ""
	}
	// Find url= after refresh
	refreshIdx := strings.Index(s[idx:], "url=")
	if refreshIdx == -1 {
		return ""
	}
	start := idx + refreshIdx + 4
	if start >= len(s) {
		return ""
	}
	// Extract URL until quote or >
	end := strings.IndexAny(s[start:], "\"'>")
	if end == -1 {
		return ""
	}
	url := strings.TrimSpace(string(body[start : start+end]))
	if strings.HasPrefix(url, "http") {
		return url
	}
	return ""
}
