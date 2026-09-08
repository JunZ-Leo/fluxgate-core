package dns_test

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/dns"
	D "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

func serveLocalDNS(t *testing.T, server *D.Server) {
	t.Helper()
	ready := make(chan struct{})
	done := make(chan error, 1)
	server.NotifyStartedFunc = func() { close(ready) }
	go func() { done <- server.ActivateAndServe() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.ShutdownContext(ctx); err != nil {
			t.Error(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-ctx.Done():
			t.Error("local DNS server did not stop")
		}
	})
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("local DNS server did not start")
	}
}

func TestParsedNameServerResolvesLocally(t *testing.T) {
	for _, tc := range []struct {
		name, scheme string
		truncateUDP  bool
		udp, tcp     int32
	}{
		{"UDP", "udp://", false, 1, 0},
		{"TCP", "tcp://", false, 0, 1},
		{"UDP retries truncated reply over TCP", "udp://", true, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			require.NoError(t, err)
			t.Cleanup(func() { _ = listener.Close() })
			packetConn, err := net.ListenPacket("udp4", listener.Addr().String())
			require.NoError(t, err)
			t.Cleanup(func() { _ = packetConn.Close() })
			var udpQueries, tcpQueries atomic.Int32
			handler := D.HandlerFunc(func(w D.ResponseWriter, query *D.Msg) {
				isUDP := w.RemoteAddr().Network() == "udp"
				if isUDP {
					udpQueries.Add(1)
				} else {
					tcpQueries.Add(1)
				}
				reply := new(D.Msg)
				reply.SetReply(query)
				if isUDP && tc.truncateUDP {
					reply.Truncated = true
				} else {
					reply.Answer = []D.RR{&D.A{
						Hdr: D.RR_Header{Name: query.Question[0].Name, Rrtype: D.TypeA, Class: D.ClassINET, Ttl: 60},
						A:   net.IPv4(192, 0, 2, 10),
					}}
				}
				if err := w.WriteMsg(reply); err != nil {
					t.Error(err)
				}
			})
			serveLocalDNS(t, &D.Server{Listener: listener, Handler: handler})
			serveLocalDNS(t, &D.Server{PacketConn: packetConn, Handler: handler})

			require.NotNil(t, dns.ParseNameServer)
			servers, err := dns.ParseNameServer([]string{tc.scheme + listener.Addr().String()})
			require.NoError(t, err)
			resolvers := dns.NewResolver(dns.Config{Main: servers})
			t.Cleanup(resolvers.ResetConnection)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for i := 0; i < 2; i++ {
				addresses, err := resolvers.LookupIPv4(ctx, "fixture.test")
				require.NoError(t, err)
				require.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.10")}, addresses)
			}
			// The second lookup is served from cache, not an external DNS service.
			require.Equal(t, tc.udp, udpQueries.Load())
			require.Equal(t, tc.tcp, tcpQueries.Load())
		})
	}
}
