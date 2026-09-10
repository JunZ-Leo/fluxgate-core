package dns

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"sync"

	"github.com/metacubex/mihomo/common/deque"
	"github.com/metacubex/mihomo/component/ca"
	"github.com/metacubex/mihomo/component/resolver"
	C "github.com/metacubex/mihomo/constant"

	"github.com/metacubex/tls"
	D "github.com/miekg/dns"
)

const maxOldDotConns = 8

type dnsOverTLS struct {
	port           string
	host           string
	dialer         *dnsDialer
	skipCertVerify bool
	nameCertVerify string
	disableReuse   bool

	access      sync.Mutex
	connections deque.Deque[net.Conn] // LIFO
}

var _ dnsClient = (*dnsOverTLS)(nil)

// Address implements dnsClient
func (t *dnsOverTLS) Address() string {
	return fmt.Sprintf("tls://%s", net.JoinHostPort(t.host, t.port))
}

func (t *dnsOverTLS) ExchangeContext(ctx context.Context, m *D.Msg) (*D.Msg, error) {
	for { // Only retry when reusing an old connection.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var conn net.Conn
		isOldConn := true
		if !t.disableReuse {
			t.access.Lock()
			if t.connections.Len() > 0 {
				conn = t.connections.PopBack()
			}
			t.access.Unlock()
		}
		if conn == nil {
			var err error
			conn, err = t.dialContext(ctx)
			if err != nil {
				return nil, err
			}
			isOldConn = false
		}
		msg, err := exchangeWithConn(ctx, conn, m)
		if err != nil {
			_ = dnsTransportConn(conn).Close()
			if isOldConn {
				continue
			}
			return msg, err
		}
		if !t.disableReuse {
			t.access.Lock()
			if t.connections.Len() >= maxOldDotConns {
				oldConn := t.connections.PopFront()
				go oldConn.Close()
			}
			t.connections.PushBack(conn)
			t.access.Unlock()
		} else {
			_ = dnsTransportConn(conn).Close()
		}
		return msg, nil
	}
}

func (t *dnsOverTLS) dialContext(ctx context.Context) (net.Conn, error) {
	conn, err := t.dialer.DialContext(ctx, "tcp", net.JoinHostPort(t.host, t.port))
	if err != nil {
		return nil, err
	}

	tlsConfig, err := ca.GetTLSConfig(ca.Option{
		TLSConfig: &tls.Config{
			ServerName:         t.host,
			InsecureSkipVerify: t.skipCertVerify,
		},
		NameCertVerify: t.nameCertVerify,
	})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	tlsConn := tls.Client(conn, tlsConfig)
	if err = tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	conn = tlsConn

	return conn, nil
}

func (t *dnsOverTLS) ResetConnection() {
	if !t.disableReuse {
		t.access.Lock()
		for t.connections.Len() > 0 {
			oldConn := t.connections.PopFront()
			go oldConn.Close() // close in a new goroutine, not blocking the current task
		}
		t.access.Unlock()
	}
}

func (t *dnsOverTLS) Close() error {
	runtime.SetFinalizer(t, nil)
	t.ResetConnection()
	return nil
}

func newDoTClient(addr string, resolver resolver.Resolver, params map[string]string, proxyAdapter C.ProxyAdapter, proxyName string) *dnsOverTLS {
	host, port, _ := net.SplitHostPort(addr)
	c := &dnsOverTLS{
		port:   port,
		host:   host,
		dialer: newDNSDialer(resolver, proxyAdapter, proxyName),
	}
	c.connections.SetBaseCap(maxOldDotConns)
	if params["skip-cert-verify"] == "true" {
		c.skipCertVerify = true
	}
	c.nameCertVerify = params["name-cert-verify"]
	if params["disable-reuse"] == "true" {
		c.disableReuse = true
	}
	runtime.SetFinalizer(c, (*dnsOverTLS).Close)
	return c
}
