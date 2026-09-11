package dns

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	D "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

const contextTestTimeout = 2 * time.Second

func awaitContextTest[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(contextTestTimeout):
		t.Fatal("DNS connection operation did not finish promptly")
		var zero T
		return zero
	}
}

type watchedDNSConn struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

type writeNotifyingDNSConn struct {
	net.Conn
	writing chan struct{}
	once    sync.Once
}

func (c *writeNotifyingDNSConn) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.writing) })
	return c.Conn.Write(p)
}

func (c *watchedDNSConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.closed) })
	return err
}

func TestDoTCancelClosesBorrowedConnection(t *testing.T) {
	local, remote := net.Pipe()
	conn := &watchedDNSConn{Conn: local, closed: make(chan struct{})}
	t.Cleanup(func() { _ = conn.Close(); _ = remote.Close() })
	require.NoError(t, remote.SetDeadline(time.Now().Add(10*time.Second)))
	client := &dnsOverTLS{}
	client.connections.PushBack(conn)
	queryRead := make(chan error, 1)
	peerDone := make(chan error, 1)
	go func() {
		_, err := (&D.Conn{Conn: remote}).ReadMsg()
		queryRead <- err
		_, err = remote.Read(make([]byte, 1))
		peerDone <- err
	}()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := client.ExchangeContext(ctx, new(D.Msg).SetQuestion("cancel.test.", D.TypeA))
		done <- err
	}()
	require.NoError(t, awaitContextTest(t, queryRead))
	cancel()
	require.ErrorIs(t, awaitContextTest(t, done), context.Canceled)
	awaitContextTest(t, conn.closed)
	require.Error(t, awaitContextTest(t, peerDone))
	client.access.Lock()
	pooled := client.connections.Len()
	client.access.Unlock()
	require.Zero(t, pooled, "a canceled connection must not return to the reuse pool")
}

func TestClientCancelClosesTCPConnection(t *testing.T) {
	for _, network := range []string{"tcp", "udp-to-tcp"} {
		t.Run(network, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			require.NoError(t, err)
			t.Cleanup(func() { _ = listener.Close() })
			udp, err := net.ListenPacket("udp4", listener.Addr().String())
			require.NoError(t, err)
			t.Cleanup(func() { _ = udp.Close() })
			udpDone := make(chan error, 1)
			if network == "udp-to-tcp" {
				go func() {
					buf := make([]byte, 4096)
					n, peer, err := udp.ReadFrom(buf)
					if err == nil {
						query := new(D.Msg)
						err = query.Unpack(buf[:n])
						if err == nil {
							reply := new(D.Msg).SetReply(query)
							reply.Truncated = true
							var data []byte
							data, err = reply.Pack()
							if err == nil {
								_, err = udp.WriteTo(data, peer)
							}
						}
					}
					udpDone <- err
				}()
			}
			queryRead := make(chan error, 1)
			peerDone := make(chan error, 1)
			accepted := make(chan net.Conn, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					queryRead <- err
					return
				}
				defer conn.Close()
				accepted <- conn
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				_, err = (&D.Conn{Conn: conn}).ReadMsg()
				queryRead <- err
				_, err = conn.Read(make([]byte, 1))
				peerDone <- err
			}()
			scheme := "tcp"
			if network == "udp-to-tcp" {
				scheme = "udp"
			}
			client := newClient(listener.Addr().String(), nil, scheme, nil, nil, "")
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			done := make(chan error, 1)
			go func() {
				_, err := client.ExchangeContext(ctx, new(D.Msg).SetQuestion("cancel.test.", D.TypeA))
				done <- err
			}()
			tcpConn := awaitContextTest(t, accepted)
			t.Cleanup(func() { _ = tcpConn.Close() })
			require.NoError(t, awaitContextTest(t, queryRead))
			cancel()
			require.ErrorIs(t, awaitContextTest(t, done), context.Canceled)
			peerErr := awaitContextTest(t, peerDone)
			require.Error(t, peerErr)
			var netErr net.Error
			require.False(t, errors.As(peerErr, &netErr) && netErr.Timeout(), "peer must see closure, not its own deadline")
			if network == "udp-to-tcp" {
				require.NoError(t, awaitContextTest(t, udpDone))
			}
		})
	}
}

func TestExchangeWithConnDeadline(t *testing.T) {
	local, remote := net.Pipe()
	conn := &watchedDNSConn{Conn: local, closed: make(chan struct{})}
	t.Cleanup(func() { _ = conn.Close(); _ = remote.Close() })
	queryRead := make(chan error, 1)
	go func() {
		_, err := (&D.Conn{Conn: remote}).ReadMsg()
		queryRead <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := exchangeWithConn(ctx, conn, new(D.Msg).SetQuestion("deadline.test.", D.TypeA))
		done <- err
	}()
	require.NoError(t, awaitContextTest(t, queryRead))
	require.ErrorIs(t, awaitContextTest(t, done), context.DeadlineExceeded)
	awaitContextTest(t, conn.closed)
}

func TestExchangeWithConnCancelDuringWrite(t *testing.T) {
	local, remote := net.Pipe()
	conn := &watchedDNSConn{Conn: local, closed: make(chan struct{})}
	writer := &writeNotifyingDNSConn{Conn: conn, writing: make(chan struct{})}
	t.Cleanup(func() { _ = conn.Close(); _ = remote.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := exchangeWithConn(ctx, writer, new(D.Msg).SetQuestion("cancel.test.", D.TypeA))
		done <- err
	}()
	// The peer never reads, so this cancellation must interrupt a pending write.
	awaitContextTest(t, writer.writing)
	cancel()
	require.ErrorIs(t, awaitContextTest(t, done), context.Canceled)
	awaitContextTest(t, conn.closed)
}

func TestDoTReuseSurvivesCompletedContext(t *testing.T) {
	local, remote := net.Pipe()
	conn := &watchedDNSConn{Conn: local, closed: make(chan struct{})}
	t.Cleanup(func() { _ = conn.Close(); _ = remote.Close() })
	client := &dnsOverTLS{}
	client.connections.PushBack(conn)
	served := make(chan error, 1)
	go func() {
		wire := &D.Conn{Conn: remote}
		for i := 0; i < 2; i++ {
			query, err := wire.ReadMsg()
			if err != nil {
				served <- err
				return
			}
			if err := wire.WriteMsg(new(D.Msg).SetReply(query)); err != nil {
				served <- err
				return
			}
		}
		served <- nil
	}()
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), contextTestTimeout)
		query := new(D.Msg).SetQuestion("reuse.test.", D.TypeA)
		reply, err := client.ExchangeContext(ctx, query)
		cancel()
		require.NoError(t, err)
		require.Equal(t, query.Id, reply.Id)
		client.access.Lock()
		pooled := client.connections.Len()
		client.access.Unlock()
		require.Equal(t, 1, pooled)
		select {
		case <-conn.closed:
			t.Fatal("completed request's context closed a pooled connection")
		default:
		}
	}
	require.NoError(t, awaitContextTest(t, served))
}

func TestDoTAlreadyCanceledDoesNotBorrow(t *testing.T) {
	local, remote := net.Pipe()
	conn := &watchedDNSConn{Conn: local, closed: make(chan struct{})}
	t.Cleanup(func() { _ = conn.Close(); _ = remote.Close() })
	client := &dnsOverTLS{}
	client.connections.PushBack(conn)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reply, err := client.ExchangeContext(ctx, new(D.Msg).SetQuestion("cancel.test.", D.TypeA))
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, reply)
	require.Equal(t, 1, client.connections.Len())
	select {
	case <-conn.closed:
		t.Fatal("an already canceled request must not touch an idle connection")
	default:
	}
}

type dnsTestSession struct {
	closed bool
}

func (s *dnsTestSession) Close() error {
	s.closed = true
	return nil
}

type dnsTestStream struct {
	net.Conn
	session *dnsTestSession
}

func (s *dnsTestStream) Upstream() any { return s.session }

func TestDNSAbortStopsAtStreamEndpoint(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	session := &dnsTestSession{}
	stream := &dnsTestStream{Conn: local, session: session}
	wrapper := &cleanupDNSWrapper{Conn: stream, cleaned: make(chan struct{})}
	require.NoError(t, remote.SetReadDeadline(time.Now().Add(contextTestTimeout)))
	// The stream owns a connection, but its parent session is shared.
	_ = dnsTransportConn(wrapper).Close()
	awaitContextTest(t, wrapper.cleaned)
	require.False(t, session.closed)
	_, err := remote.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF)
}
