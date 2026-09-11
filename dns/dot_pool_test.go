package dns

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	D "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

func TestDoTResetDiscardsBorrowedConnection(t *testing.T) {
	for _, closeClient := range []bool{false, true} {
		name := "ResetConnection"
		if closeClient {
			name = "Close"
		}
		t.Run(name, func(t *testing.T) {
			local, remote := net.Pipe()
			conn := &watchedDNSConn{Conn: local, closed: make(chan struct{})}
			client := &dnsOverTLS{}
			client.connections.PushBack(conn)
			queryRead := make(chan error, 1)
			replyAllowed := make(chan struct{})
			peerDone := make(chan error, 1)
			ctx, cancel := context.WithTimeout(context.Background(), contextTestTimeout)
			t.Cleanup(func() { cancel(); _ = conn.Close(); _ = remote.Close() })
			go func() {
				wire := &D.Conn{Conn: remote}
				query, err := wire.ReadMsg()
				queryRead <- err
				if err != nil {
					peerDone <- err
					return
				}
				select {
				case <-replyAllowed:
				case <-ctx.Done():
					peerDone <- ctx.Err()
					return
				}
				peerDone <- wire.WriteMsg(new(D.Msg).SetReply(query))
			}()
			query := new(D.Msg).SetQuestion("before-reset.test.", D.TypeA)
			type result struct {
				reply *D.Msg
				err   error
			}
			done := make(chan result, 1)
			go func() {
				reply, err := client.ExchangeContext(ctx, query)
				done <- result{reply, err}
			}()
			require.NoError(t, awaitContextTest(t, queryRead))
			if closeClient {
				require.NoError(t, client.Close())
			} else {
				client.ResetConnection()
				client.ResetConnection()
			}
			select {
			case <-conn.closed:
				t.Fatal("reset must allow the in-flight query to finish")
			default:
			}
			close(replyAllowed)
			ret := awaitContextTest(t, done)
			require.NoError(t, ret.err)
			require.NotNil(t, ret.reply)
			require.Equal(t, query.Id, ret.reply.Id)
			require.NoError(t, awaitContextTest(t, peerDone))
			client.access.Lock()
			pooled := client.connections.Len()
			client.access.Unlock()
			require.Zero(t, pooled, "an exchange from before reset must not repopulate the pool")
			awaitContextTest(t, conn.closed)
		})
	}
}

func TestDoTResetDuringHandshakeAndReuseAfterward(t *testing.T) {
	var connections atomic.Int32
	handshakeStarted := make(chan struct{})
	allowHandshake := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	address := startDoTContextServer(t, func(wire *D.Conn) {
		if connections.Add(1) == 1 {
			close(handshakeStarted)
			select {
			case <-allowHandshake:
			case <-ctx.Done():
				return
			}
		}
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
	client := newDoTClient(address, nil, map[string]string{"skip-cert-verify": "true"}, nil, "")
	t.Cleanup(func() { _ = client.Close() })
	firstDone := make(chan error, 1)
	go func() {
		_, err := client.ExchangeContext(ctx, new(D.Msg).SetQuestion("during-handshake.test.", D.TypeA))
		firstDone <- err
	}()
	awaitContextTest(t, handshakeStarted)
	client.ResetConnection()
	close(allowHandshake)
	require.NoError(t, awaitContextTest(t, firstDone))
	client.access.Lock()
	pooled := client.connections.Len()
	client.access.Unlock()
	require.Zero(t, pooled, "a dial started before reset must not enter the pool")

	for i := 0; i < 2; i++ {
		query := new(D.Msg).SetQuestion("after-reset.test.", D.TypeA)
		reply, err := client.ExchangeContext(ctx, query)
		require.NoError(t, err)
		require.Equal(t, query.Id, reply.Id)
	}
	require.Equal(t, int32(2), connections.Load(), "post-reset exchanges should reuse a fresh connection")
}

func TestDoTStaleReturnKeepsFreshPool(t *testing.T) {
	var connections atomic.Int32
	address := startDoTContextServer(t, func(wire *D.Conn) {
		connections.Add(1)
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
	client := newDoTClient(address, nil, map[string]string{"skip-cert-verify": "true"}, nil, "")
	t.Cleanup(func() { _ = client.Close() })
	local, remote := net.Pipe()
	conn := &watchedDNSConn{Conn: local, closed: make(chan struct{})}
	client.connections.PushBack(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() { cancel(); _ = conn.Close(); _ = remote.Close() })
	queryRead, oldDone, peerDone := make(chan error, 1), make(chan error, 1), make(chan error, 1)
	replyAllowed := make(chan struct{})
	go func() {
		wire := &D.Conn{Conn: remote}
		query, err := wire.ReadMsg()
		queryRead <- err
		if err != nil {
			peerDone <- err
			return
		}
		select {
		case <-replyAllowed:
		case <-ctx.Done():
			peerDone <- ctx.Err()
			return
		}
		peerDone <- wire.WriteMsg(new(D.Msg).SetReply(query))
	}()
	go func() {
		_, err := client.ExchangeContext(ctx, new(D.Msg).SetQuestion("old.test.", D.TypeA))
		oldDone <- err
	}()
	require.NoError(t, awaitContextTest(t, queryRead))
	client.ResetConnection()
	_, err := client.ExchangeContext(ctx, new(D.Msg).SetQuestion("fresh.test.", D.TypeA))
	require.NoError(t, err)
	close(replyAllowed)
	require.NoError(t, awaitContextTest(t, oldDone))
	require.NoError(t, awaitContextTest(t, peerDone))
	awaitContextTest(t, conn.closed)
	client.access.Lock()
	pooled := client.connections.Len()
	client.access.Unlock()
	require.Equal(t, 1, pooled)
	_, err = client.ExchangeContext(ctx, new(D.Msg).SetQuestion("reuse-fresh.test.", D.TypeA))
	require.NoError(t, err)
	require.Equal(t, int32(1), connections.Load(), "a late old exchange must not evict the fresh connection")
}
