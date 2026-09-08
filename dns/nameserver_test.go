package dns_test

import (
	"testing"

	"github.com/metacubex/mihomo/dns"
	"github.com/stretchr/testify/require"
)

func TestParseNameServerWithoutConfig(t *testing.T) {
	// This package deliberately does not import config: standalone users such as
	// WireGuard must be able to parse nameservers without its init side effects.
	require.NotNil(t, dns.ParseNameServer)
	servers, err := dns.ParseNameServer([]string{"192.0.2.1", "tls://dns.example"})
	require.NoError(t, err)
	require.Equal(t, []dns.NameServer{
		{Addr: "192.0.2.1:53", Params: map[string]string{}},
		{Net: "tls", Addr: "dns.example:853", Params: map[string]string{}},
	}, servers)

	servers, err = dns.ParseNameServer([]string{"192.0.2.1", "unknown://server"})
	require.EqualError(t, err, "DNS NameServer[1] unsupport scheme: unknown")
	require.Nil(t, servers)
}
