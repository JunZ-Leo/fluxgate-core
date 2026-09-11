package dns

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/metacubex/mihomo/component/resolver"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"

	D "github.com/miekg/dns"
)

type client struct {
	port   string
	host   string
	dialer *dnsDialer
	schema string
}

var _ dnsClient = (*client)(nil)

// Address implements dnsClient
func (c *client) Address() string {
	return fmt.Sprintf("%s://%s", c.schema, net.JoinHostPort(c.host, c.port))
}

func (c *client) ExchangeContext(ctx context.Context, m *D.Msg) (*D.Msg, error) {
	network := "udp"
	if c.schema != "udp" {
		network = "tcp"
	}

	addr := net.JoinHostPort(c.host, c.port)
	conn, err := c.dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	msg, err := exchangeWithConn(ctx, conn, m)
	// Retry a truncated UDP response over TCP, with cancellation attached to
	// the retry's connection rather than just the original UDP socket.
	if msg != nil && msg.Truncated && network == "udp" {
		log.Debugln("[DNS] Truncated reply from %s:%s for %s over UDP, retrying over TCP", c.host, c.port, m.Question[0].String())
		tcpConn, err := c.dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return msg, err
		}
		defer tcpConn.Close()
		return exchangeWithConn(ctx, tcpConn, m)
	}
	return msg, err
}

func (c *client) ResetConnection() {}

func newClient(addr string, resolver resolver.Resolver, netType string, params map[string]string, proxyAdapter C.ProxyAdapter, proxyName string) *client {
	host, port, _ := net.SplitHostPort(addr)
	c := &client{
		port:   port,
		host:   host,
		dialer: newDNSDialer(resolver, proxyAdapter, proxyName),
		schema: "udp",
	}
	if strings.HasPrefix(netType, "tcp") {
		c.schema = "tcp"
	}
	return c
}
