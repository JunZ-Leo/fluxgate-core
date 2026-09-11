package dns

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

const dnsPairAttempts = 16

// ListenLocalDNSPair is shared with external-package DNS integration tests.
// It is compiled only into the test binary.
func ListenLocalDNSPair() (net.Listener, net.PacketConn, error) {
	return listenLocalDNSPair(net.ListenPacket, net.Listen)
}

func listenLocalDNSPair(
	listenPacket func(string, string) (net.PacketConn, error),
	listen func(string, string) (net.Listener, error),
) (net.Listener, net.PacketConn, error) {
	var bindErr error
	for attempt := 0; attempt < dnsPairAttempts; attempt++ {
		// TCP's ephemeral allocator can choose ports reserved for UDP on
		// Windows. Let UDP select its port, then reserve the matching TCP port.
		packet, err := listenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			return nil, nil, err
		}
		listener, err := listen("tcp4", packet.LocalAddr().String())
		if err == nil {
			return listener, packet, nil
		}
		if closeErr := packet.Close(); closeErr != nil {
			return nil, nil, fmt.Errorf("close UDP after TCP bind error (%v): %w", err, closeErr)
		}
		bindErr = err
		retry := false
		for _, retryErr := range dnsPairRetryErrors {
			retry = retry || errors.Is(err, retryErr)
		}
		if !retry {
			return nil, nil, err
		}
	}
	return nil, nil, fmt.Errorf("cannot bind a DNS UDP/TCP port pair after %d attempts: %w", dnsPairAttempts, bindErr)
}

type pairTestPacket struct {
	net.PacketConn
	port   int
	closed bool
}

func (p *pairTestPacket) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p.port}
}
func (p *pairTestPacket) Close() error { p.closed = true; return nil }

type pairTestListener struct{ net.Listener }

func TestDNSPairRetriesSecondaryConflicts(t *testing.T) {
	for _, retryErr := range dnsPairRetryErrors {
		t.Run(retryErr.Error(), func(t *testing.T) {
			var packets []*pairTestPacket
			listener := &pairTestListener{}
			gotListener, gotPacket, err := listenLocalDNSPair(
				func(network, address string) (net.PacketConn, error) {
					require.Equal(t, "udp4", network)
					require.Equal(t, "127.0.0.1:0", address)
					p := &pairTestPacket{port: 30000 + len(packets)}
					packets = append(packets, p)
					return p, nil
				},
				func(network, address string) (net.Listener, error) {
					require.Equal(t, "tcp4", network)
					require.Equal(t, packets[len(packets)-1].LocalAddr().String(), address)
					if len(packets) < 3 {
						return nil, &net.OpError{Op: "listen", Net: network, Err: retryErr}
					}
					return listener, nil
				},
			)
			require.NoError(t, err)
			require.Same(t, listener, gotListener)
			require.Same(t, packets[2], gotPacket)
			require.True(t, packets[0].closed)
			require.True(t, packets[1].closed)
			require.False(t, packets[2].closed)
		})
	}
}

func TestDNSPairFailuresAreBounded(t *testing.T) {
	fatalErr := errors.New("unrecoverable listen error")
	for _, tc := range []struct {
		name     string
		udpError bool
		tcpError error
		attempts int
	}{
		{"initial UDP bind failure", true, nil, 1},
		{"unrelated TCP bind failure", false, fatalErr, 1},
		{"persistent port conflict", false, dnsPairRetryErrors[0], dnsPairAttempts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var packets []*pairTestPacket
			calls := 0
			l, p, err := listenLocalDNSPair(
				func(_, _ string) (net.PacketConn, error) {
					calls++
					if tc.udpError {
						return nil, fatalErr
					}
					p := &pairTestPacket{port: 30000 + calls}
					packets = append(packets, p)
					return p, nil
				},
				func(_, _ string) (net.Listener, error) {
					return nil, tc.tcpError
				},
			)
			require.Error(t, err)
			require.Nil(t, l)
			require.Nil(t, p)
			require.Equal(t, tc.attempts, calls)
			if tc.udpError {
				require.ErrorIs(t, err, fatalErr)
			} else {
				require.ErrorIs(t, err, tc.tcpError)
			}
			for _, packet := range packets {
				require.True(t, packet.closed)
			}
		})
	}
}

func TestDNSPairSharesPort(t *testing.T) {
	listener, packet, err := ListenLocalDNSPair()
	require.NoError(t, err)
	defer listener.Close()
	defer packet.Close()
	require.Equal(t, listener.Addr().String(), packet.LocalAddr().String())
}
