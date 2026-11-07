package self_test

import (
	"context"
	"io"
	"net"
	"sync"
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

// wireLog counts the short-header datagrams the client sends, by content.
type wireLog struct {
	mu   sync.Mutex
	seen map[string]int
	// sizes of the datagrams seen once and twice
	once, twice []int
}

func (w *wireLog) record(p simnet.Packet) (seenBefore int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen == nil {
		w.seen = make(map[string]int)
	}
	k := string(p.Data)
	seenBefore = w.seen[k]
	w.seen[k]++
	return seenBefore
}

func (w *wireLog) counts() (single, double, more int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, n := range w.seen {
		switch {
		case n == 1:
			single++
		case n == 2:
			double++
		default:
			more++
		}
	}
	return
}

func (w *wireLog) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seen = nil
}

func TestLossRecoveryNoCopiesByDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log wireLog
		pair := newEchoPair(t, nil, nil, func(toServer bool, p simnet.Packet) bool {
			if toServer {
				log.record(p)
			}
			return false
		})
		pair.warmUp(t)
		_, double, more := log.counts()
		require.Zero(t, double+more)
		require.Zero(t, pair.client.ConnectionStats().CopiesSent)
	})
}

func TestLossRecoveryCopiesBySize(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log wireLog
		var dropOriginals atomic.Bool
		// MTU probes are never copied; without them every packet the client sends has a copy
		pair := newEchoPair(t, &quic.Config{DuplicateMaxSize: 1500, DisablePathMTUDiscovery: true}, nil, func(toServer bool, p simnet.Packet) bool {
			if !toServer {
				return false
			}
			return log.record(p) == 0 && dropOriginals.Load()
		})
		pair.warmUp(t) // the handshake is confirmed by now
		log.reset()
		before := pair.client.ConnectionStats()
		for i := range 5 {
			pair.roundTrip(t, byte(i))
		}
		synctest.Wait() // let the last ACKs go out
		// every datagram the client sends goes out twice, byte for byte
		single, double, more := log.counts()
		require.Zero(t, single)
		require.NotZero(t, double)
		require.Zero(t, more)
		after := pair.client.ConnectionStats()
		require.Equal(t, uint64(double), after.CopiesSent-before.CopiesSent)
		require.Equal(t, after.PacketsSent-before.PacketsSent, after.CopiesSent-before.CopiesSent, "copies are not counted as sent packets")
		require.NotZero(t, after.CopyBytesSent)
		require.Zero(t, pair.server.ConnectionStats().CopiesSent)

		// losing every original costs nothing: the copies carry the requests
		dropOriginals.Store(true)
		for i := range 5 {
			require.Less(t, pair.roundTrip(t, byte(i)), lossRecoveryRTT+2*time.Millisecond)
		}
	})
}

func TestLossRecoveryCopyDelay(t *testing.T) {
	elapsed := make(map[time.Duration]time.Duration)
	for _, delay := range []time.Duration{0, 4 * time.Millisecond} {
		synctest.Test(t, func(t *testing.T) {
			var dropUntil atomic.Int64
			pair := newEchoPair(t, &quic.Config{DuplicateMaxSize: 1500, DuplicateDelay: delay}, nil, func(toServer bool, _ simnet.Packet) bool {
				return toServer && time.Now().UnixNano() < dropUntil.Load()
			})
			pair.warmUp(t)
			// a 2 ms burst of loss takes an original and a copy right behind it, not a copy 4 ms later
			dropUntil.Store(time.Now().Add(2 * time.Millisecond).UnixNano())
			elapsed[delay] = pair.roundTrip(t, 0xfe)
		})
	}
	t.Logf("a request in a 2 ms loss burst took %s with copies right behind, %s with copies 4 ms later", elapsed[0], elapsed[4*time.Millisecond])
	require.LessOrEqual(t, elapsed[4*time.Millisecond], lossRecoveryRTT+4*time.Millisecond+time.Millisecond)
	require.Greater(t, elapsed[0], lossRecoveryRTT+10*time.Millisecond)
}

func TestLossRecoveryTailCopies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log wireLog
		var dropOriginals atomic.Bool
		pair := newEchoPair(t, &quic.Config{DuplicateTail: true}, nil, func(toServer bool, p simnet.Packet) bool {
			if !toServer {
				return false
			}
			return log.record(p) == 0 && dropOriginals.Load()
		})
		pair.warmUp(t)

		// a bulk write sends many datagrams, but only the last of each round is copied
		log.reset()
		before := pair.client.ConnectionStats()
		data := make([]byte, 200<<10)
		_, err := pair.str.Write(data)
		require.NoError(t, err)
		_, err = io.ReadFull(pair.str, data)
		require.NoError(t, err)
		synctest.Wait()
		after := pair.client.ConnectionStats()
		packets := after.PacketsSent - before.PacketsSent
		copies := after.CopiesSent - before.CopiesSent
		t.Logf("%d copies for %d packets", copies, packets)
		require.NotZero(t, copies)
		require.Less(t, copies*5, packets)
		_, _, more := log.counts()
		require.Zero(t, more)

		// a lone request is its round's tail
		dropOriginals.Store(true)
		require.Less(t, pair.roundTrip(t, 1), lossRecoveryRTT+2*time.Millisecond)
	})
}
