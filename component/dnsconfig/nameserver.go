// Package dnsconfig provides the configuration-facing DNS parser.
package dnsconfig

import "github.com/metacubex/mihomo/dns"

// ParseNameServers preserves the configuration parser entry point.
func ParseNameServers(servers []string, respectRules bool, preferH3 bool) ([]dns.NameServer, error) {
	return dns.ParseNameServers(servers, respectRules, preferH3)
}
