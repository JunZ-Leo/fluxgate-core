package dnsconfig_test

import (
	"fmt"
	"testing"

	"github.com/metacubex/mihomo/component/dnsconfig"
	"github.com/metacubex/mihomo/dns"
	"github.com/stretchr/testify/require"
)

func TestParseNameServersTransports(t *testing.T) {
	tests := []struct {
		input, network, address string
	}{
		{"192.0.2.1", "", "192.0.2.1:53"},
		{"udp://192.0.2.1:5353", "", "192.0.2.1:5353"},
		{"2001:db8::1", "", "[2001:db8::1]:53"},
		{"[2001:db8::1]:5353", "", "[2001:db8::1]:5353"},
		{"udp://[fe80::1%25eth0]", "", "[fe80::1%eth0]:53"},
		{"tcp://dns.example", "tcp", "dns.example:53"},
		{"tls://dns.example", "tls", "dns.example:853"},
		{"tls://[2001:db8::1]:8853", "tls", "[2001:db8::1]:8853"},
		{"http://dns.example/dns-query", "https", "http://dns.example:80/dns-query"},
		{"https://dns.example:8443/dns-query", "https", "https://dns.example:8443/dns-query"},
		{"https://user:pass@dns.example/dns-query?ignored=1", "https", "https://user:pass@dns.example:443/dns-query"},
		{"quic://dns.example", "quic", "dns.example:853"},
		{"system", "system", ""},
		{"system://", "system", ""},
		{"dhcp://system", "system", ""},
		{"dhcp://eth0", "dhcp", "eth0"},
		{"dhcp://en*", "dhcp", "en*"},
		{"ts://node", "tailscale", "node"},
		{"tailscale://node", "tailscale", "node"},
	}
	for _, code := range []string{"success", "format_error", "server_failure", "name_error", "not_implemented", "refused"} {
		tests = append(tests, struct{ input, network, address string }{"rcode://" + code, "rcode", code})
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			servers, err := dnsconfig.ParseNameServers([]string{tt.input}, false, false)
			require.NoError(t, err)
			require.Equal(t, []dns.NameServer{{
				Net: tt.network, Addr: tt.address, Params: map[string]string{},
			}}, servers)
		})
	}
}

func TestParseNameServersFragments(t *testing.T) {
	tests := []struct {
		fragment string
		proxy    string
		params   map[string]string
	}{
		{"", "", map[string]string{}},
		{"#Proxy", "Proxy", map[string]string{}},
		{"#Proxy&ecs=192.0.2.0/24&ecs-override=true", "Proxy", map[string]string{"ecs": "192.0.2.0/24", "ecs-override": "true"}},
		{"#Proxy%20Group&value=a=b", "Proxy Group", map[string]string{"value": "a=b"}},
		{"#First&Last&key=first&key=last", "Last", map[string]string{"key": "last"}},
		{"#Proxy&", "", map[string]string{}},
		{"#key=&=value", "", map[string]string{"key": "", "": "value"}},
	}
	for _, tt := range tests {
		for _, respect := range []bool{false, true} {
			for _, preferH3 := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/rules=%t/h3=%t", tt.fragment, respect, preferH3), func(t *testing.T) {
					servers, err := dnsconfig.ParseNameServers([]string{"https://dns.example/dns-query" + tt.fragment}, respect, preferH3)
					require.NoError(t, err)
					proxy := tt.proxy
					if respect && proxy == "" {
						proxy = dns.RespectRules
					}
					require.Equal(t, []dns.NameServer{{
						Net: "https", Addr: "https://dns.example:443/dns-query",
						ProxyName: proxy, Params: tt.params, PreferH3: preferH3,
					}}, servers)
				})
			}
		}
	}
}

func TestParseNameServersOrderAndDeduplication(t *testing.T) {
	servers, err := dnsconfig.ParseNameServers([]string{
		"192.0.2.2", "192.0.2.1", "udp://192.0.2.2:53",
		"system", "dhcp://system", "system://",
		"192.0.2.2#Proxy", "192.0.2.2#Proxy&ecs=192.0.2.0/24",
		"192.0.2.2#ecs=192.0.2.0/24&Proxy",
		"192.0.2.2#Proxy&ecs=198.51.100.0/24",
	}, false, false)
	require.NoError(t, err)
	require.Equal(t, []dns.NameServer{
		{Addr: "192.0.2.2:53", Params: map[string]string{}},
		{Addr: "192.0.2.1:53", Params: map[string]string{}},
		{Net: "system", Params: map[string]string{}},
		{Addr: "192.0.2.2:53", ProxyName: "Proxy", Params: map[string]string{}},
		{Addr: "192.0.2.2:53", ProxyName: "Proxy", Params: map[string]string{"ecs": "192.0.2.0/24"}},
		{Addr: "192.0.2.2:53", ProxyName: "Proxy", Params: map[string]string{"ecs": "198.51.100.0/24"}},
	}, servers)
	servers[4].Params["ecs"] = "changed"
	require.Equal(t, "198.51.100.0/24", servers[5].Params["ecs"])
}

func TestParseNameServersErrorAndEmptyResults(t *testing.T) {
	for _, input := range [][]string{nil, {}} {
		servers, err := dnsconfig.ParseNameServers(input, false, false)
		require.NoError(t, err)
		require.Nil(t, servers)
	}
	for _, tt := range []struct{ input, message string }{
		{"unknown://server", "DNS NameServer[1] unsupport scheme: unknown"},
		{"tailscale://", "DNS NameServer[1] format error: missing Tailscale proxy name"},
		{"rcode://unknown", "DNS NameServer[1] format error: unsupported RCode type: unknown"},
		{"tls://2001:db8::1", "DNS NameServer[1] format error: address 2001:db8::1: too many colons in address"},
		{"fe80::1%eth0", `DNS NameServer[1] format error: parse "udp://[fe80::1%eth0]": invalid URL escape "%et"`},
	} {
		t.Run(tt.input, func(t *testing.T) {
			servers, err := dnsconfig.ParseNameServers([]string{"192.0.2.1", tt.input}, false, false)
			require.EqualError(t, err, tt.message)
			require.Nil(t, servers, "do not return partially parsed nameservers on error")
		})
	}
}
