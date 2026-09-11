package dns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	N "github.com/metacubex/mihomo/common/net"
	MTLS "github.com/metacubex/tls"
	D "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

func dotContextCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func startDoTContextServer(t *testing.T, handle func(*D.Conn)) string {
	t.Helper()
	listener, err := tls.Listen("tcp4", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{dotContextCertificate(t)},
	})
	require.NoError(t, err)
	var mu sync.Mutex
	var conns []net.Conn
	var workers sync.WaitGroup
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				handle(&D.Conn{Conn: conn})
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-acceptDone
		mu.Lock()
		for _, conn := range conns {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	return listener.Addr().String()
}

func TestDoTCancelClosesNewTLSConnection(t *testing.T) {
	for _, disableReuse := range []string{"false", "true"} {
		t.Run("disable-reuse="+disableReuse, func(t *testing.T) {
			queryRead, peerDone := make(chan error, 1), make(chan error, 1)
			address := startDoTContextServer(t, func(wire *D.Conn) {
				_, err := wire.ReadMsg()
				queryRead <- err
				_, err = wire.ReadMsg()
				peerDone <- err
			})
			// Trust only this loopback test endpoint without altering global roots.
			client := newDoTClient(address, nil, map[string]string{
				"skip-cert-verify": "true", "disable-reuse": disableReuse,
			}, nil, "")
			t.Cleanup(func() { _ = client.Close() })
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
			peerErr := awaitContextTest(t, peerDone)
			require.True(t, errors.Is(peerErr, io.EOF) || errors.Is(peerErr, io.ErrUnexpectedEOF), "peer error: %v", peerErr)
			client.access.Lock()
			pooled := client.connections.Len()
			client.access.Unlock()
			require.Zero(t, pooled)
		})
	}
}

func TestDoTCancelWithoutDrainingTLSCloseNotify(t *testing.T) {
	local, remote := net.Pipe()
	transport := &watchedDNSConn{Conn: local, closed: make(chan struct{})}
	server := tls.Server(remote, &tls.Config{
		Certificates: []tls.Certificate{dotContextCertificate(t)},
	})
	readTracker := &readTrackingDNSConn{Conn: transport, reading: make(chan struct{})}
	tlsConn := MTLS.Client(readTracker, &MTLS.Config{InsecureSkipVerify: true})
	releasePeer := make(chan struct{})
	peerStopped := make(chan struct{})
	queryRead := make(chan error, 1)
	t.Cleanup(func() {
		_ = transport.Close()
		_ = remote.Close()
		close(releasePeer)
		<-peerStopped
	})
	go func() {
		defer close(peerStopped)
		_, err := (&D.Conn{Conn: server}).ReadMsg()
		queryRead <- err
		// Deliberately stop reading: a graceful TLS Close would block here
		// until its write deadline, even though the query was canceled.
		<-releasePeer
	}()
	handshake, stopHandshake := context.WithTimeout(context.Background(), contextTestTimeout)
	defer stopHandshake()
	require.NoError(t, tlsConn.HandshakeContext(handshake))
	readTracker.enabled.Store(true)
	client := &dnsOverTLS{}
	client.connections.PushBack(tlsConn)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := client.ExchangeContext(ctx, new(D.Msg).SetQuestion("cancel.test.", D.TypeA))
		done <- err
	}()
	require.NoError(t, awaitContextTest(t, queryRead))
	awaitContextTest(t, readTracker.reading)
	cancel()
	require.ErrorIs(t, awaitContextTest(t, done), context.Canceled)
	awaitContextTest(t, transport.closed)
}

type cleanupDNSWrapper struct {
	net.Conn
	cleaned chan struct{}
	once    sync.Once
}

func (c *cleanupDNSWrapper) Upstream() any { return c.Conn }

func (c *cleanupDNSWrapper) Close() error {
	c.once.Do(func() { close(c.cleaned) })
	return c.Conn.Close()
}

func TestDoTCancelThroughNestedTLSProxy(t *testing.T) {
	local, remote := net.Pipe()
	transport := &watchedDNSConn{Conn: local, closed: make(chan struct{})}
	readTracker := &readTrackingDNSConn{Conn: transport, reading: make(chan struct{})}
	cert := dotContextCertificate(t)
	proxyServer := tls.Server(remote, &tls.Config{Certificates: []tls.Certificate{cert}})
	dnsServer := tls.Server(proxyServer, &tls.Config{Certificates: []tls.Certificate{cert}})
	proxyClient := MTLS.Client(readTracker, &MTLS.Config{InsecureSkipVerify: true})
	wrapper := &cleanupDNSWrapper{
		Conn: N.NewRefConn(proxyClient, new(int)), cleaned: make(chan struct{}),
	}
	dnsClient := MTLS.Client(wrapper, &MTLS.Config{InsecureSkipVerify: true})
	releasePeer, peerStopped := make(chan struct{}), make(chan struct{})
	queryRead := make(chan error, 1)
	t.Cleanup(func() {
		_ = transport.Close()
		_ = remote.Close()
		close(releasePeer)
		<-peerStopped
	})
	go func() {
		defer close(peerStopped)
		_, err := (&D.Conn{Conn: dnsServer}).ReadMsg()
		queryRead <- err
		<-releasePeer
	}()
	handshake, stopHandshake := context.WithTimeout(context.Background(), contextTestTimeout)
	defer stopHandshake()
	require.NoError(t, proxyClient.HandshakeContext(handshake))
	require.NoError(t, dnsClient.HandshakeContext(handshake))
	readTracker.enabled.Store(true)
	client := &dnsOverTLS{}
	client.connections.PushBack(dnsClient)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := client.ExchangeContext(ctx, new(D.Msg).SetQuestion("nested.test.", D.TypeA))
		done <- err
	}()
	require.NoError(t, awaitContextTest(t, queryRead))
	awaitContextTest(t, readTracker.reading)
	cancel()
	require.ErrorIs(t, awaitContextTest(t, done), context.Canceled)
	awaitContextTest(t, transport.closed)
	awaitContextTest(t, wrapper.cleaned)
	client.access.Lock()
	pooled := client.connections.Len()
	client.access.Unlock()
	require.Zero(t, pooled)
}

type readTrackingDNSConn struct {
	net.Conn
	enabled atomic.Bool
	reading chan struct{}
	once    sync.Once
}

func (c *readTrackingDNSConn) Read(p []byte) (int, error) {
	if c.enabled.Load() {
		c.once.Do(func() { close(c.reading) })
	}
	return c.Conn.Read(p)
}

func TestDoTReuseAndStaleRetry(t *testing.T) {
	for _, disableReuse := range []string{"false", "true"} {
		t.Run("disable-reuse="+disableReuse, func(t *testing.T) {
			var accepted atomic.Int32
			address := startDoTContextServer(t, func(wire *D.Conn) {
				accepted.Add(1)
				for {
					query, err := wire.ReadMsg()
					if err != nil {
						return
					}
					if err := wire.WriteMsg(new(D.Msg).SetReply(query)); err != nil {
						t.Error(err)
						return
					}
				}
			})
			client := newDoTClient(address, nil, map[string]string{
				"skip-cert-verify": "true", "disable-reuse": disableReuse,
			}, nil, "")
			t.Cleanup(func() { _ = client.Close() })
			if disableReuse == "false" {
				// A stale pooled connection must still cause one fresh TLS dial.
				local, remote := net.Pipe()
				require.NoError(t, remote.Close())
				t.Cleanup(func() { _ = local.Close() })
				client.connections.PushBack(local)
			}
			for i := 0; i < 2; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), contextTestTimeout)
				query := new(D.Msg).SetQuestion("reuse.test.", D.TypeA)
				reply, err := client.ExchangeContext(ctx, query)
				cancel()
				require.NoError(t, err)
				require.Equal(t, query.Id, reply.Id)
			}
			expected := int32(1)
			if disableReuse == "true" {
				expected = 2
			}
			require.Equal(t, expected, accepted.Load())
		})
	}
}
