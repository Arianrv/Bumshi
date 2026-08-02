package proxy

// Telemetry sinks.
//
// A large share of a modern page's requests carry nothing to the user. Loading
// one Google search result page fires roughly eighty requests at google.com, of
// which about thirty are /gen_204, /client_204, jserror and play.google.com/log
// — beacons whose entire purpose is to report what the user just did. The page
// does not read their responses; /gen_204 is named for the empty status it is
// specified to return.
//
// Answering them here rather than upstream buys four things at once, and the
// first is the one that prompted this:
//
//   - Request budget. Every user shares one exit IP, and anti-abuse systems
//     count requests per address. A few minutes of ordinary browsing puts
//     thousands of requests on that address, which is what earns Google's
//     "unusual traffic" interstitial — a challenge that cannot then be solved,
//     because reCAPTCHA validates the embedding page's origin and ours is the
//     proxy. Dropping the beacons removes about two fifths of the traffic
//     without removing anything a user would notice.
//   - Bandwidth. Every byte crosses an international link twice.
//   - Latency. Beacons compete with content for connections.
//   - Privacy. This is a censorship-circumvention tool; forwarding a detailed
//     record of each user's behaviour to an advertising network is at odds with
//     the point of it.
//
// The rule that keeps this safe is: block SINKS, never SCRIPTS. An endpoint
// that exists to receive data can always be answered with its own no-op status,
// because the page is written to ignore it. A script is different — a page that
// fails to load its analytics library may never run the callback that draws the
// rest of the page, and blocking those is how ad blockers break sites. Nothing
// in this file matches a .js URL.

import (
	"net/http"
	"net/url"
	"strings"
)

// telemetryRule matches a beacon endpoint. An empty host matches any host, so a
// path that is a sink wherever it appears can be named once.
type telemetryRule struct {
	hostSuffix string // "" matches any host
	pathPrefix string
	exactPath  bool
}

// telemetrySinks are endpoints answered locally with 204.
//
// Each one is a beacon: it accepts a report and returns an empty response. Add
// to this list only after checking that the endpoint returns no body the page
// consumes — if in doubt, leave it out. A missing beacon is invisible; a
// missing response the page waits on is a blank screen.
var telemetrySinks = []telemetryRule{
	// Google's logging endpoints. gen_204 is the canonical one — the name is
	// the status code it returns.
	{hostSuffix: "google.com", pathPrefix: "/gen_204", exactPath: true},
	{hostSuffix: "google.com", pathPrefix: "/client_204", exactPath: true},
	{hostSuffix: "google.com", pathPrefix: "/httpservice/retry/jserror"},
	{hostSuffix: "play.google.com", pathPrefix: "/log"},
	{hostSuffix: "google.com", pathPrefix: "/log", exactPath: true},
	{hostSuffix: "youtube.com", pathPrefix: "/api/stats/"},
	{hostSuffix: "youtube.com", pathPrefix: "/ptracking"},

	// Google Analytics and Tag Manager collection endpoints. The libraries
	// themselves are NOT blocked — only where they post to.
	{hostSuffix: "google-analytics.com", pathPrefix: "/collect"},
	{hostSuffix: "google-analytics.com", pathPrefix: "/g/collect"},
	{hostSuffix: "google-analytics.com", pathPrefix: "/j/collect"},
	{hostSuffix: "google-analytics.com", pathPrefix: "/batch"},
	{hostSuffix: "analytics.google.com", pathPrefix: "/g/collect"},

	// Facebook's tracking pixel.
	{hostSuffix: "facebook.com", pathPrefix: "/tr", exactPath: true},

	// Error and product-analytics ingestion.
	{hostSuffix: "ingest.sentry.io", pathPrefix: "/api/"},
	{hostSuffix: "api.mixpanel.com", pathPrefix: "/track"},
	{hostSuffix: "api.amplitude.com", pathPrefix: "/"},
	{hostSuffix: "api.segment.io", pathPrefix: "/v1/"},
	{hostSuffix: "scorecardresearch.com", pathPrefix: "/"},
}

// isTelemetrySink reports whether a target is a beacon we answer ourselves.
func isTelemetrySink(target *url.URL) bool {
	host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
	path := target.EscapedPath()
	for _, r := range telemetrySinks {
		if r.hostSuffix != "" && !hostMatches(host, r.hostSuffix) {
			continue
		}
		if r.exactPath {
			if path == r.pathPrefix {
				return true
			}
			continue
		}
		if strings.HasPrefix(path, r.pathPrefix) {
			return true
		}
	}
	return false
}

// hostMatches reports whether host is suffix itself or a subdomain of it. The
// leading dot matters: without it "notgoogle.com" would match "google.com".
func hostMatches(host, suffix string) bool {
	return host == suffix || strings.HasSuffix(host, "."+suffix)
}

// serveTelemetry answers a beacon with the empty response it expects.
//
// 204 rather than an error: an error status is something a page can observe and
// retry, and a retry loop would cost more requests than forwarding the beacon
// once. 204 is what these endpoints return when they succeed, so from the
// page's point of view the report was delivered.
func (h *Handler) serveTelemetry(w http.ResponseWriter, r *http.Request) {
	// Same-origin XHR rules still apply on the proxy origin, and a beacon sent
	// with credentials expects the CORS headers its real endpoint would send.
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
	h.count("telemetry_blocked")
}
