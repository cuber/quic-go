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

// Tests for spec/001-loss-recovery C1: the parameters of the sent and received packet handlers.

func newRecoveryTestHandler(rttStats *utils.RTTStats, stats *utils.ConnectionStats, r ...Recovery) *sentPacketHandler {
	return NewSentPacketHandler(
		0,
		1200,
		rttStats,
		stats,
		true,
		false,
		nil,
		protocol.PerspectiveServer,
		false,
		nil,
		utils.DefaultLogger,
		r...,
	).(*sentPacketHandler)
}

func TestSentPacketHandlerRecoveryDefaults(t *testing.T) {
	for _, r := range [][]Recovery{nil, {{}}} {
		sph := newRecoveryTestHandler(utils.NewRTTStats(), &utils.ConnectionStats{}, r...)
		require.Equal(t, float64(timeThreshold), sph.timeThreshold)
		require.Equal(t, protocol.PacketNumber(packetThreshold), sph.packetThreshold)
		require.Zero(t, sph.ptoBackoffLimit)
	}
}

func TestSentPacketHandlerLossTimeThreshold(t *testing.T) {
	rttStats := utils.NewRTTStats()
	sph := newRecoveryTestHandler(rttStats, &utils.ConnectionStats{}, Recovery{LossTimeThreshold: 1.5})

	var packets packetTracker
	sendPacket := func(ti monotime.Time) protocol.PacketNumber {
		pn := sph.PopPacketNumber(protocol.EncryptionInitial)
		sph.SentPacket(ti, pn, protocol.InvalidPacketNumber, nil, []Frame{packets.NewPingFrame(pn)}, protocol.EncryptionInitial, protocol.ECNNon, 1000, false, false)
		return pn
	}

	const rtt = time.Second
	now := monotime.Now()
	pn1 := sendPacket(now.Add(-2 * rtt))
	t2 := now.Add(-10 * time.Millisecond)
	pn2 := sendPacket(t2)
	pn3 := sendPacket(now)

	_, err := sph.ReceivedAck(&wire.AckFrame{AckRanges: ackRanges(pn3)}, protocol.EncryptionInitial, now.Add(rtt))
	require.NoError(t, err)
	require.Equal(t, rtt, rttStats.SmoothedRTT())
	require.Equal(t, []protocol.PacketNumber{pn1}, packets.Lost)
	// the timer for packet 2 uses 1.5 RTTs instead of 9/8
	require.Equal(t, t2.Add(rtt*3/2), sph.GetLossDetectionTimeout())
	require.NoError(t, sph.OnLossDetectionTimeout(sph.GetLossDetectionTimeout()))
	require.Equal(t, []protocol.PacketNumber{pn1, pn2}, packets.Lost)
}

func TestSentPacketHandlerLossPacketThreshold(t *testing.T) {
	for _, tc := range []struct {
		threshold int
		lost      []int
	}{
		{threshold: 0, lost: []int{0}}, // quic-go's 3
		{threshold: 2, lost: []int{0, 1}},
		{threshold: 5, lost: nil},
	} {
		sph := newRecoveryTestHandler(utils.NewRTTStats(), &utils.ConnectionStats{}, Recovery{LossPacketThreshold: tc.threshold})
		var packets packetTracker
		now := monotime.Now()
		var pns []protocol.PacketNumber
		for range 5 {
			pn := sph.PopPacketNumber(protocol.EncryptionInitial)
			sph.SentPacket(now, pn, protocol.InvalidPacketNumber, nil, []Frame{packets.NewPingFrame(pn)}, protocol.EncryptionInitial, protocol.ECNNon, 1000, false, false)
			pns = append(pns, pn)
		}
		_, err := sph.ReceivedAck(&wire.AckFrame{AckRanges: ackRanges(pns[3])}, protocol.EncryptionInitial, now.Add(time.Second))
		require.NoError(t, err)
		var lost []protocol.PacketNumber
		for _, i := range tc.lost {
			lost = append(lost, pns[i])
		}
		require.Equal(t, lost, packets.Lost, "threshold %d", tc.threshold)
	}
}

func TestSentPacketHandlerPTOBackoffLimit(t *testing.T) {
	for _, limit := range []int{0, 1, 3} {
		rttStats := utils.NewRTTStats()
		rttStats.SetMaxAckDelay(25 * time.Millisecond)
		rttStats.UpdateRTT(100*time.Millisecond, 0)
		sph := newRecoveryTestHandler(rttStats, &utils.ConnectionStats{}, Recovery{PTOBackoffLimit: limit})
		sph.DropPackets(protocol.EncryptionInitial, monotime.Now())
		sph.DropPackets(protocol.EncryptionHandshake, monotime.Now())

		var packets packetTracker
		sent := monotime.Now()
		pn := sph.PopPacketNumber(protocol.Encryption1RTT)
		sph.SentPacket(sent, pn, protocol.InvalidPacketNumber, nil, []Frame{packets.NewPingFrame(pn)}, protocol.Encryption1RTT, protocol.ECNNon, 1000, false, false)

		pto := rttStats.PTO(true)
		for count := range 6 {
			shift := count
			if limit != 0 {
				shift = min(count, limit)
			}
			require.Equal(t, sent.Add(pto<<shift), sph.GetLossDetectionTimeout(), "limit %d, PTO count %d", limit, count)
			require.NoError(t, sph.OnLossDetectionTimeout(sph.GetLossDetectionTimeout()))
		}
	}
}

func TestSentPacketHandlerPTOBackoffLimitKeepsCeiling(t *testing.T) {
	rttStats := utils.NewRTTStats()
	rttStats.UpdateRTT(20*time.Second, 0)
	sph := newRecoveryTestHandler(rttStats, &utils.ConnectionStats{}, Recovery{PTOBackoffLimit: 16})
	sph.ptoCount = 10
	require.Equal(t, maxPTODuration, sph.getScaledPTO(true))
}

func TestReceivedPacketHandlerAckPolicy(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		h := NewReceivedPacketHandler(utils.DefaultLogger)
		require.Equal(t, protocol.MaxAckDelay, h.appDataPackets.maxAckDelay)
		require.Equal(t, packetsBeforeAck, h.appDataPackets.ackElicitingThreshold)
	})

	t.Run("immediate ACK on the first packet", func(t *testing.T) {
		h := NewReceivedPacketHandler(utils.DefaultLogger)
		h.SetAckPolicy(2*time.Millisecond, 1)
		now := monotime.Now()
		require.NoError(t, h.ReceivedPacket(0, protocol.ECNNon, protocol.Encryption1RTT, now, true))
		require.NotNil(t, h.GetAckFrame(protocol.Encryption1RTT, now, true))
	})

	t.Run("timer and threshold", func(t *testing.T) {
		h := NewReceivedPacketHandler(utils.DefaultLogger)
		h.SetAckPolicy(2*time.Millisecond, 3)
		now := monotime.Now()
		require.NoError(t, h.ReceivedPacket(0, protocol.ECNNon, protocol.Encryption1RTT, now, true))
		require.NoError(t, h.ReceivedPacket(1, protocol.ECNNon, protocol.Encryption1RTT, now, true))
		require.Nil(t, h.GetAckFrame(protocol.Encryption1RTT, now, true))
		require.Equal(t, now.Add(2*time.Millisecond), h.GetAlarmTimeout())
		require.NotNil(t, h.GetAckFrame(protocol.Encryption1RTT, now.Add(2*time.Millisecond), true))

		require.NoError(t, h.ReceivedPacket(2, protocol.ECNNon, protocol.Encryption1RTT, now, true))
		require.NoError(t, h.ReceivedPacket(3, protocol.ECNNon, protocol.Encryption1RTT, now, true))
		require.Nil(t, h.GetAckFrame(protocol.Encryption1RTT, now, true))
		require.NoError(t, h.ReceivedPacket(4, protocol.ECNNon, protocol.Encryption1RTT, now, true))
		require.NotNil(t, h.GetAckFrame(protocol.Encryption1RTT, now, true))
	})
}
