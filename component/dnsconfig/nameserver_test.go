package dnsconfig

import (
	"testing"

	"github.com/metacubex/mihomo/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseNameServersCompatibility(t *testing.T) {
	servers, err := ParseNameServers([]string{
		"1.1.1.1",
		"2606:4700:4700::1111",
		"tcp://8.8.8.8",
		"tls://dns.google",
		"https://dns.google/dns-query#Proxy&ecs=1.2.3.0/24",
		"system",
	}, false, true)
	require.NoError(t, err)
	require.Len(t, servers, 6)

	assert.Equal(t, dns.NameServer{Addr: "1.1.1.1:53", Params: map[string]string{}, PreferH3: true}, servers[0])
	assert.Equal(t, "[2606:4700:4700::1111]:53", servers[1].Addr)
	assert.Equal(t, "tcp", servers[2].Net)
	assert.Equal(t, "8.8.8.8:53", servers[2].Addr)
	assert.Equal(t, "tls", servers[3].Net)
	assert.Equal(t, "dns.google:853", servers[3].Addr)
	assert.Equal(t, "https", servers[4].Net)
	assert.Equal(t, "https://dns.google:443/dns-query", servers[4].Addr)
	assert.Equal(t, "Proxy", servers[4].ProxyName)
	assert.Equal(t, map[string]string{"ecs": "1.2.3.0/24"}, servers[4].Params)
	assert.Equal(t, "system", servers[5].Net)
}

func TestParseNameServersRespectRulesAndDeduplicate(t *testing.T) {
	servers, err := ParseNameServers([]string{"1.1.1.1", "1.1.1.1"}, true, false)
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Equal(t, dns.RespectRules, servers[0].ProxyName)
}

func TestParseNameServersCompatibilityErrors(t *testing.T) {
	_, err := ParseNameServers([]string{"unknown://server"}, false, false)
	require.EqualError(t, err, "DNS NameServer[0] unsupport scheme: unknown")

	_, err = ParseNameServers([]string{"tailscale://"}, false, false)
	require.EqualError(t, err, "DNS NameServer[0] format error: missing Tailscale proxy name")

	_, err = ParseNameServers([]string{"rcode://unknown"}, false, false)
	require.EqualError(t, err, "DNS NameServer[0] format error: unsupported RCode type: unknown")
}
