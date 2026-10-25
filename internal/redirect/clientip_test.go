package redirect

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NCUnix/shlink-go/internal/config"
)

func clientIPFor(trustedProxies []string, remoteAddr string, headers map[string]string) string {
	request := httptest.NewRequest("GET", "/abc", nil)
	request.RemoteAddr = remoteAddr
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	service := &Service{cfg: config.Config{TrustedProxies: trustedProxies}}
	return service.clientIP(request)
}

func TestClientIPIgnoresForwardingHeadersFromUntrustedPeer(t *testing.T) {
	got := clientIPFor(
		[]string{"127.0.0.1", "::1"},
		"203.0.113.7:5555",
		map[string]string{"X-Forwarded-For": "1.2.3.4"},
	)
	require.Equal(t, "203.0.113.7", got)
}

func TestClientIPHonoursForwardingHeadersFromTrustedProxy(t *testing.T) {
	got := clientIPFor(
		[]string{"10.0.0.0/8"},
		"10.1.2.3:5555",
		map[string]string{"X-Forwarded-For": "1.2.3.4, 10.1.2.3"},
	)
	require.Equal(t, "1.2.3.4", got)
}

func TestClientIPPrefersCloudflareHeader(t *testing.T) {
	got := clientIPFor(
		[]string{"127.0.0.1"},
		"127.0.0.1:5555",
		map[string]string{"CF-Connecting-IP": "1.2.3.4", "X-Forwarded-For": "5.6.7.8"},
	)
	require.Equal(t, "1.2.3.4", got)
}

func TestClientIPParsesForwardedHeader(t *testing.T) {
	got := clientIPFor(
		[]string{"127.0.0.1"},
		"127.0.0.1:5555",
		map[string]string{"Forwarded": `for=1.2.3.4;proto=https`},
	)
	require.Equal(t, "1.2.3.4", got)
}

func TestParseForwardedFor(t *testing.T) {
	require.Equal(t, "1.2.3.4", parseForwardedFor(`for=1.2.3.4;proto=https`))
	require.Equal(t, "1.2.3.4", parseForwardedFor(`for="1.2.3.4:4711"`))
	require.Equal(t, "2001:db8::1", parseForwardedFor(`for="[2001:db8::1]:4711"`))
	require.Equal(t, "", parseForwardedFor(`proto=https`))
}

func TestTrustedProxy(t *testing.T) {
	service := &Service{cfg: config.Config{TrustedProxies: []string{"10.0.0.0/8", "127.0.0.1", ""}}}
	require.True(t, service.trustedProxy("10.1.2.3"))
	require.True(t, service.trustedProxy("127.0.0.1"))
	require.False(t, service.trustedProxy("203.0.113.7"))
	require.False(t, service.trustedProxy("not-an-ip"))
}
