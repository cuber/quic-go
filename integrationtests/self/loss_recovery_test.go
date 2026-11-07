package self_test

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/internal/protocol"
	"github.com/apernet/quic-go/internal/wire"
	"github.com/apernet/quic-go/qlog"
	"github.com/apernet/quic-go/testutils/events"
	"github.com/apernet/quic-go/testutils/simnet"

	"github.com/stretchr/testify/require"
)

// Integration tests for spec/001-loss-recovery, on a simulated link with virtual time.

const lossRecoveryRTT = 10 * time.Millisecond

// echoPair connects a client to a server that echoes every byte on the first stream back.
// drop decides, for each short-header datagram, whether the link loses it.
type echoPair struct {
	client, server *quic.Conn
	str            *quic.Stream
}

func newEchoPair(t *testing.T, clientConf, serverConf *quic.Config, drop func(toServer bool, p simnet.Packet) bool) *echoPair {
	t.Helper()
	serverAddr := &net.UDPAddr{IP: net.ParseIP("1.0.0.2"), Port: 9002}
	router := &droppingRouter{Drop: func(p simnet.Packet) bool {
		if drop == nil || wire.IsLongHeaderPacket(p.Data[0]) {
			return false
		}
		return drop(p.To.String() == serverAddr.String(), p)
	}}
	clientPacketConn, serverPacketConn, closeLink := newSimnetLinkWithRouter(t, lossRecoveryRTT, router)

	ln, err := quic.Listen(serverPacketConn, getTLSConfig(), getQuicConfig(serverConf))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := quic.Dial(ctx, clientPacketConn, ln.Addr(), getTLSClientConfig(), getQuicConfig(clientConf))
	require.NoError(t, err)
	server, err := ln.Accept(ctx)
	require.NoError(t, err)
	go func() {
		str, err := server.AcceptStream(context.Background())
		if err != nil {
			return
		}
		io.Copy(str, str)
	}()
	str, err := client.OpenStream()
	require.NoError(t, err)

	t.Cleanup(func() {
		client.CloseWithError(0, "")
		server.CloseWithError(0, "")
		ln.Close()
		closeLink(t)
	})
	return &echoPair{client: client, server: server, str: str}
}

// roundTrip sends one byte and waits for its echo, returning the time it took.
func (e *echoPair) roundTrip(t *testing.T, b byte) time.Duration {
	t.Helper()
	start := time.Now()
	_, err := e.str.Write([]byte{b})
	require.NoError(t, err)
	got := make([]byte, 1)
	_, err = io.ReadFull(e.str, got)
	require.NoError(t, err)
	require.Equal(t, b, got[0])
	return time.Since(start)
}

// warmUp settles the RTT estimate on the lossless link.
func (e *echoPair) warmUp(t *testing.T) {
	t.Helper()
	for i := range 20 {
		e.roundTrip(t, byte(i))
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLossRecoveryMaxAckDelay(t *testing.T) {
	elapsed := make(map[time.Duration]time.Duration)
	for _, tc := range []struct {
		maxAckDelay, advertised time.Duration
	}{
		{0, protocol.MaxAckDelayInclGranularity}, // quic-go's 25 ms
		{2 * time.Millisecond, 3 * time.Millisecond},
	} {
		synctest.Test(t, func(t *testing.T) {
			var dropNext atomic.Bool
			var clientEvents events.Recorder
			clientConf := &quic.Config{MaxAckDelay: tc.maxAckDelay, Tracer: newTracer(&clientEvents)}
			serverConf := &quic.Config{MaxAckDelay: tc.maxAckDelay}
			pair := newEchoPair(t, clientConf, serverConf, func(toServer bool, _ simnet.Packet) bool {
				return toServer && dropNext.CompareAndSwap(true, false)
			})

			// the client reads the server's advertised max_ack_delay
			var sawServerParams bool
			for _, ev := range clientEvents.Events(qlog.ParametersSet{}) {
				if params := ev.(qlog.ParametersSet); params.Initiator == qlog.InitiatorRemote {
					require.Equal(t, tc.advertised, params.MaxAckDelay)
					sawServerParams = true
				}
			}
			require.True(t, sawServerParams)

			pair.warmUp(t)
			require.Less(t, pair.roundTrip(t, 0xff), lossRecoveryRTT+2*time.Millisecond)

			// lose the request: only the PTO recovers it, and the PTO includes the peer's max_ack_delay
			dropNext.Store(true)
			elapsed[tc.maxAckDelay] = pair.roundTrip(t, 0xfe)
			require.False(t, dropNext.Load())
		})
	}
	fast, slow := elapsed[2*time.Millisecond], elapsed[0]
	t.Logf("a lost request took %s with a 2 ms max_ack_delay, %s by default", fast, slow)
	require.LessOrEqual(t, fast, lossRecoveryRTT+2*time.Millisecond+15*time.Millisecond)
	require.GreaterOrEqual(t, slow-fast, 20*time.Millisecond)
}
