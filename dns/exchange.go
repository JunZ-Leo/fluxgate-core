package dns

import (
	"context"
	"net"
	"time"

	N "github.com/metacubex/mihomo/common/net"
	"github.com/metacubex/tls"
	D "github.com/miekg/dns"
)

func dnsTransportConn(conn net.Conn) net.Conn {
	if tlsConn, ok := conn.(*tls.Conn); ok {
		// A canceled or discarded connection must not wait for close_notify.
		return tlsConn.NetConn()
	}
	return conn
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
