package redirect

import (
	"net"
	"net/http"
	"strings"
)

// clientIP returns the address of the client. Forwarding headers are only
// honoured when the request comes from a trusted proxy; otherwise any client
// could spoof its IP (and thus IP-based redirect rules and visit geolocation).
func (s *Service) clientIP(request *http.Request) string {
	remote := remoteIP(request)
	if !s.trustedProxy(remote) {
		return remote
	}
	for _, header := range []string{
		"CF-Connecting-IP",
		"X-Forwarded-For",
		"X-Forwarded",
		"Forwarded",
		"True-Client-IP",
		"X-Real-IP",
		"X-Cluster-Client-Ip",
		"Client-Ip",
	} {
		if value := strings.TrimSpace(request.Header.Get(header)); value != "" {
			if header == "Forwarded" {
				if forwarded := parseForwardedFor(value); forwarded != "" {
					return forwarded
				}
				continue
			}
			return strings.TrimSpace(strings.Split(value, ",")[0])
		}
	}
	return remote
}

func remoteIP(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil {
		return host
	}
	return strings.Trim(request.RemoteAddr, "[]")
}

func (s *Service) trustedProxy(raw string) bool {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return false
	}
	for _, entry := range s.cfg.TrustedProxies {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			if network.Contains(ip) {
				return true
			}
			continue
		}
		if candidate := net.ParseIP(entry); candidate != nil && candidate.Equal(ip) {
			return true
		}
	}
	return false
}

func parseForwardedFor(value string) string {
	for _, part := range strings.Split(value, ";") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(strings.ToLower(part), "for=") {
			continue
		}
		forwarded := strings.Trim(strings.TrimSpace(part[4:]), `"`)
		if host, _, err := net.SplitHostPort(forwarded); err == nil {
			return strings.Trim(host, "[]")
		}
		return strings.Trim(forwarded, "[]")
	}
	return ""
}
