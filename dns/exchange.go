package dns

import (
	"context"
	"net"
	"time"

	N "github.com/metacubex/mihomo/common/net"
	D "github.com/miekg/dns"
)

func dnsTransportConn(conn net.Conn) net.Conn {
	return &dnsAbortConn{Conn: conn}
}

type dnsAbortConn struct {
	net.Conn
}

func (c *dnsAbortConn) Close() error {
	transport := c.Conn
	unwrapped := false
	for {
		var next net.Conn
		switch wrapper := transport.(type) {
		case interface{ NetConn() net.Conn }:
			next = wrapper.NetConn()
		case N.WithUpstream:
			// Stop at logical stream endpoints; do not follow a shared
			// session or transport that is not itself a net.Conn wrapper.
			next, _ = wrapper.Upstream().(net.Conn)
		}
		if next == nil {
			break
		}
		transport = next
		unwrapped = true
	}
	var err error
	if unwrapped {
		// Interrupt the underlying connection before a TLS/WebSocket wrapper
		// can attempt a blocking graceful shutdown.
		err = transport.Close()
	}
	closeErr := c.Conn.Close() // Preserve tracker and per-wrapper cleanup.
	if err != nil {
		return err
	}
	return closeErr
}

func exchangeWithConn(ctx context.Context, conn net.Conn, query *D.Msg) (msg *D.Msg, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if ctx.Done() != nil {
		done := N.SetupContextForConn(ctx, dnsTransportConn(conn))
		defer func() {
			// Stop and join the cancellation callback before a DoT connection
			// can be returned to its reuse pool.
			done(&err)
			if ctxErr := ctx.Err(); ctxErr != nil {
				msg, err = nil, ctxErr
			}
		}()
	}
	client := &D.Client{UDPSize: 4096, Timeout: 5 * time.Second}
	msg, _, err = client.ExchangeWithConn(query, &D.Conn{Conn: conn, UDPSize: client.UDPSize})
	return
}
