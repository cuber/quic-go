package ackhandler

import (
	"testing"
	"time"

	"github.com/apernet/quic-go/internal/monotime"
	"github.com/apernet/quic-go/internal/protocol"
	"github.com/apernet/quic-go/internal/utils"
	"github.com/apernet/quic-go/internal/wire"
	"github.com/stretchr/testify/require"
)

// spec/002-ack-progress: newly acknowledged packets are counted once, and the bytes in flight follow sends,
// acknowledgments and losses.
func TestSentPacketHandlerAckProgress(t *testing.T) {
	var stats utils.ConnectionStats
	sph := newRecoveryTestHandler(utils.NewRTTStats(), &stats)
	sph.DropPackets(protocol.EncryptionInitial, monotime.Now())
	sph.DropPackets(protocol.EncryptionHandshake, monotime.Now())

	var packets packetTracker
	now := monotime.Now()
	send := func() protocol.PacketNumber {
		pn := sph.PopPacketNumber(protocol.Encryption1RTT)
		sph.SentPacket(now, pn, protocol.InvalidPacketNumber, nil, []Frame{packets.NewPingFrame(pn)}, protocol.Encryption1RTT, protocol.ECNNon, 1000, false, false)
		return pn
	}
	var pns []protocol.PacketNumber
	for range 3 {
		pns = append(pns, send())
	}
	require.Zero(t, stats.PacketsAcked.Load())
	require.Equal(t, uint64(3000), stats.BytesInFlight.Load())

	_, err := sph.ReceivedAck(&wire.AckFrame{AckRanges: ackRanges(pns[0], pns[1])}, protocol.Encryption1RTT, now.Add(10*time.Millisecond))
	require.NoError(t, err)
	require.Equal(t, uint64(2), stats.PacketsAcked.Load())
	require.Equal(t, uint64(1000), stats.BytesInFlight.Load())

	// the same ranges again acknowledge nothing new
	_, err = sph.ReceivedAck(&wire.AckFrame{AckRanges: ackRanges(pns[0], pns[1])}, protocol.Encryption1RTT, now.Add(11*time.Millisecond))
	require.NoError(t, err)
	require.Equal(t, uint64(2), stats.PacketsAcked.Load())

	// three later packets acknowledged declare pns[2] lost: it leaves the bytes in flight, uncounted as acked
	for range 3 {
		pns = append(pns, send())
	}
	_, err = sph.ReceivedAck(&wire.AckFrame{AckRanges: ackRanges(pns[0], pns[1], pns[3], pns[4], pns[5])}, protocol.Encryption1RTT, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, []protocol.PacketNumber{pns[2]}, packets.Lost)
	require.Equal(t, uint64(5), stats.PacketsAcked.Load())
	require.Zero(t, stats.BytesInFlight.Load())
}
