package domains

import (
	"net"
	"regexp"
	"strconv"
	"strings"
)

var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidHostname informa se s é um hostname aceito: minúsculo, total ≤ 253,
// ao menos dois rótulos e cada rótulo em ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$.
// Endereços IP não são hostnames.
func ValidHostname(s string) bool {
	if s == "" || len(s) > 253 || net.ParseIP(s) != nil {
		return false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if !labelRe.MatchString(l) {
			return false
		}
	}
	return true
}

// ValidAddress informa se s é um endereço manual host:porta, com host IP,
// localhost ou hostname válido e porta 1–65535.
func ValidAddress(s string) bool {
	host, port, err := net.SplitHostPort(s)
	if err != nil || host == "" || port == "" {
		return false
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return false
		}
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return false
	}
	return host == "localhost" || net.ParseIP(host) != nil || ValidHostname(host)
}
