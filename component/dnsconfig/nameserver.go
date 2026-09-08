// Package dnsconfig parses DNS configuration without creating network clients.
package dnsconfig

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"

	"github.com/metacubex/mihomo/dns"
	"golang.org/x/exp/slices"
)

func ParseNameServers(servers []string, respectRules bool, preferH3 bool) ([]dns.NameServer, error) {
	var nameservers []dns.NameServer

	for idx, server := range servers {
		server = normalizeServer(server)
		u, err := url.Parse(server)
		if err != nil {
			return nil, fmt.Errorf("DNS NameServer[%d] format error: %s", idx, err.Error())
		}

		proxyName, params := parseFragment(u.Fragment)
		addr, network, err := parseAddress(u, server)
		if err != nil {
			return nil, fmt.Errorf("DNS NameServer[%d] format error: %s", idx, err.Error())
		}
		if network == "" && u.Scheme != "udp" {
			return nil, fmt.Errorf("DNS NameServer[%d] unsupport scheme: %s", idx, u.Scheme)
		}
		if respectRules && proxyName == "" {
			proxyName = dns.RespectRules
		}

		nameserver := dns.NameServer{
			Net:       network,
			Addr:      addr,
			ProxyName: proxyName,
			Params:    params,
			PreferH3:  preferH3,
		}
		if !slices.ContainsFunc(nameservers, nameserver.Equal) {
			nameservers = append(nameservers, nameserver)
		}
	}

	return nameservers, nil
}

func parseFragment(fragment string) (string, map[string]string) {
	var proxyName string
	params := map[string]string{}
	for _, value := range strings.Split(fragment, "&") {
		parts := strings.SplitN(value, "=", 2)
		if len(parts) == 1 {
			proxyName = parts[0]
		} else {
			params[parts[0]] = parts[1]
		}
	}
	return proxyName, params
}

func parseAddress(u *url.URL, server string) (addr, network string, err error) {
	switch u.Scheme {
	case "udp":
		addr, err = withDefaultPort(u.Host, "53")
	case "tcp":
		addr, err = withDefaultPort(u.Host, "53")
		network = "tcp"
	case "tls":
		addr, err = withDefaultPort(u.Host, "853")
		network = "tls"
	case "http", "https":
		defaultPort := "443"
		if u.Scheme == "http" {
			defaultPort = "80"
		}
		addr, err = withDefaultPort(u.Host, defaultPort)
		network = "https"
		if err == nil {
			addr = (&url.URL{Scheme: u.Scheme, Host: addr, Path: u.Path, User: u.User}).String()
		}
	case "quic":
		addr, err = withDefaultPort(u.Host, "853")
		network = "quic"
	case "system":
		network = "system"
	case "ts", "tailscale":
		addr = u.Host
		network = "tailscale"
		if addr == "" {
			err = errors.New("missing Tailscale proxy name")
		}
	case "dhcp":
		addr = server[len("dhcp://"):]
		network = "dhcp"
		if addr == "system" {
			network = "system"
			addr = ""
		}
	case "rcode":
		addr = u.Host
		network = "rcode"
		switch addr {
		case "success", "format_error", "server_failure", "name_error", "not_implemented", "refused":
		default:
			err = fmt.Errorf("unsupported RCode type: %s", addr)
		}
	}
	return
}

func withDefaultPort(host, defaultPort string) (string, error) {
	hostname, port, err := net.SplitHostPort(host)
	if err != nil {
		if !strings.Contains(err.Error(), "missing port in address") {
			return "", err
		}
		hostname, port, err = net.SplitHostPort(host + ":" + defaultPort)
		if err != nil {
			return "", err
		}
	}
	return net.JoinHostPort(hostname, port), nil
}

func normalizeServer(server string) string {
	if server == "system" {
		return "system://"
	}
	if ip, err := netip.ParseAddr(server); err == nil {
		if ip.Is4() {
			return "udp://" + server
		}
		return "udp://[" + server + "]"
	}
	if strings.Contains(server, "://") {
		return server
	}
	return "udp://" + server
}
