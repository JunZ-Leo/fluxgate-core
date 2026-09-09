package config

import (
	"testing"

	"github.com/metacubex/mihomo/common/orderedmap"
	"github.com/metacubex/mihomo/dns"
	"github.com/stretchr/testify/require"
)

func TestDNSNameserverConfigCompatibility(t *testing.T) {
	raw, err := UnmarshalRawConfig([]byte(`
dns:
  enable: true
  respect-rules: true
  prefer-h3: true
  nameserver: [192.0.2.1, "udp://192.0.2.1:53"]
  fallback: ["tls://dns.example"]
  fallback-filter:
    geoip: false
  proxy-server-nameserver: [192.0.2.2]
  direct-nameserver: [192.0.2.3]
  direct-nameserver-follow-policy: true
  default-nameserver: ["https://192.0.2.4/dns-query", system]
  nameserver-policy:
    "first.example,second.example": "https://dns.example/dns-query#Proxy"
    "third.example": [192.0.2.5]
  proxy-server-nameserver-policy:
    "proxy.example": [192.0.2.6]
`))
	require.NoError(t, err)
	cfg, err := parseDNS(raw, nil)
	require.NoError(t, err)
	ns := func(network, address, proxy string) dns.NameServer {
		return dns.NameServer{
			Net: network, Addr: address, ProxyName: proxy,
			Params: map[string]string{}, PreferH3: true,
		}
	}
	require.Equal(t, []dns.NameServer{ns("", "192.0.2.1:53", dns.RespectRules)}, cfg.NameServer)
	require.Equal(t, []dns.NameServer{ns("tls", "dns.example:853", dns.RespectRules)}, cfg.Fallback)
	require.Equal(t, []dns.NameServer{ns("", "192.0.2.2:53", "")}, cfg.ProxyServerNameserver)
	require.Equal(t, []dns.NameServer{ns("", "192.0.2.3:53", "")}, cfg.DirectNameServer)
	require.True(t, cfg.DirectFollowPolicy)
	require.Equal(t, []dns.NameServer{
		ns("https", "https://192.0.2.4:443/dns-query", ""), ns("system", "", ""),
	}, cfg.DefaultNameserver)
	require.Equal(t, []dns.Policy{
		{Domain: "first.example", NameServers: []dns.NameServer{ns("https", "https://dns.example:443/dns-query", "Proxy")}},
		{Domain: "second.example", NameServers: []dns.NameServer{ns("https", "https://dns.example:443/dns-query", "Proxy")}},
		{Domain: "third.example", NameServers: []dns.NameServer{ns("", "192.0.2.5:53", dns.RespectRules)}},
	}, cfg.NameServerPolicy)
	require.Equal(t, []dns.Policy{
		{Domain: "proxy.example", NameServers: []dns.NameServer{ns("", "192.0.2.6:53", "")}},
	}, cfg.ProxyServerPolicy)
}

func TestDNSNameserverConfigErrors(t *testing.T) {
	for _, field := range []string{
		"nameserver", "fallback", "proxy-server-nameserver", "direct-nameserver",
		"default-nameserver", "nameserver-policy", "proxy-server-nameserver-policy",
	} {
		t.Run(field, func(t *testing.T) {
			raw := DefaultRawConfig()
			invalid := []string{"192.0.2.1", "unknown://server"}
			policy := orderedmap.New[string, any]()
			policy.Store("example.com", invalid)
			switch field {
			case "nameserver":
				raw.DNS.NameServer = invalid
			case "fallback":
				raw.DNS.Fallback = invalid
			case "proxy-server-nameserver":
				raw.DNS.ProxyServerNameserver = invalid
			case "direct-nameserver":
				raw.DNS.DirectNameServer = invalid
			case "default-nameserver":
				raw.DNS.DefaultNameserver = invalid
			case "nameserver-policy":
				raw.DNS.NameServerPolicy = policy
			case "proxy-server-nameserver-policy":
				raw.DNS.ProxyServerNameserverPolicy = policy
			}
			cfg, err := parseDNS(raw, nil)
			require.EqualError(t, err, "DNS NameServer[1] unsupport scheme: unknown")
			require.Nil(t, cfg)
		})
	}
}
