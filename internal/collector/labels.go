// -------------------------------------------------------------------------------
// Metric Label Helpers
//
// Author: Alex Freidah
//
// Normalization shared by every collector that turns Cloudflare dimensions into
// Prometheus label values, so the same host or status always lands on the same
// series whichever dataset it came from.
// -------------------------------------------------------------------------------

package collector

import (
	"net"
	"strconv"
	"strings"
)

// normalizeHost turns a request hostname into a host label value: lowercased,
// with any port stripped, so "G3.munchbox.cc:8080" and "g3.munchbox.cc" are
// one series. Ports carry no service meaning here - every host is served
// through the same edge - and would otherwise split a service's traffic.
func normalizeHost(host string) string {
	host = strings.ToLower(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// statusLabel turns an HTTP status code into a label value, keeping 0 (no
// status, e.g. no origin response) as "0".
func statusLabel(code int) string {
	return strconv.Itoa(code)
}
