package ackhandler

import (
	"testing"
	"time"

	congestionExt "github.com/apernet/quic-go/congestion"
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

// countingCC is a congestion controller set through SetCongestionControl, as Hysteria sets BBR and Brutal.
type countingCC struct{ congestionEvents int }

func (c *countingCC) SetRTTStatsProvider(congestionExt.RTTStatsProvider)  {}
func (c *countingCC) TimeUntilSend(congestionExt.ByteCount) monotime.Time { return 0 }
func (c *countingCC) HasPacingBudget(monotime.Time) bool                  { return true }
func (c *countingCC) OnPacketSent(monotime.Time, congestionExt.ByteCount, congestionExt.PacketNumber, congestionExt.ByteCount, bool) {
}
func (c *countingCC) CanSend(congestionExt.ByteCount) bool { return true }
func (c *countingCC) MaybeExitSlowStart()                  {}
func (c *countingCC) OnPacketAcked(congestionExt.PacketNumber, congestionExt.ByteCount, congestionExt.ByteCount, monotime.Time) {
}
func (c *countingCC) OnCongestionEvent(congestionExt.PacketNumber, congestionExt.ByteCount, congestionExt.ByteCount) {
	c.congestionEvents++
}
func (c *countingCC) OnRetransmissionTimeout(bool)               {}
func (c *countingCC) SetMaxDatagramSize(congestionExt.ByteCount) {}
func (c *countingCC) InSlowStart() bool                          { return false }
func (c *countingCC) InRecovery() bool                           { return false }
func (c *countingCC) GetCongestionWindow() congestionExt.ByteCount {
	return 1 << 20
}

// spec/001-loss-recovery C3: losses and spurious losses counted whatever the controller, once each.
func TestSentPacketHandlerLossCounters(t *testing.T) {
	for _, custom := range []bool{false, true} {
		var stats utils.ConnectionStats
		sph := newRecoveryTestHandler(utils.NewRTTStats(), &stats)
		var cc countingCC
		if custom {
			sph.SetCongestionControl(&cc)
		}
		sph.DropPackets(protocol.EncryptionInitial, monotime.Now())
		sph.DropPackets(protocol.EncryptionHandshake, monotime.Now())

		var packets packetTracker
		now := monotime.Now()
		send := func(mtuProbe bool) protocol.PacketNumber {
			pn := sph.PopPacketNumber(protocol.Encryption1RTT)
			sph.SentPacket(now, pn, protocol.InvalidPacketNumber, nil, []Frame{packets.NewPingFrame(pn)}, protocol.Encryption1RTT, protocol.ECNNon, 1000, mtuProbe, false)
			return pn
		}
		var pns []protocol.PacketNumber
		pns = append(pns, send(true)) // a lost MTU probe is no congestion signal and not counted
		for range 5 {
			pns = append(pns, send(false))
		}
		_, err := sph.ReceivedAck(&wire.AckFrame{AckRanges: ackRanges(pns[4])}, protocol.Encryption1RTT, now.Add(time.Second))
		require.NoError(t, err)
		require.Equal(t, []protocol.PacketNumber{pns[0], pns[1]}, packets.Lost)
		require.Equal(t, uint64(1), stats.PacketsLost.Load(), "custom controller: %t", custom)
		require.Equal(t, uint64(1000), stats.BytesLost.Load())
		if custom {
			require.Equal(t, 1, cc.congestionEvents)
		}

		// pns[1] arrives after all: a spurious loss
		_, err = sph.ReceivedAck(&wire.AckFrame{AckRanges: ackRanges(pns[1], pns[2], pns[3], pns[4], pns[5])}, protocol.Encryption1RTT, now.Add(time.Second+time.Millisecond))
		require.NoError(t, err)
		require.Equal(t, uint64(1), stats.SpuriousLosses.Load())
		require.Equal(t, uint64(1), stats.PacketsLost.Load())
	}
}

func TestSentPacketHandlerPTOCounter(t *testing.T) {
	var stats utils.ConnectionStats
	rttStats := utils.NewRTTStats()
	rttStats.UpdateRTT(100*time.Millisecond, 0)
	sph := newRecoveryTestHandler(rttStats, &stats)
	sph.DropPackets(protocol.EncryptionInitial, monotime.Now())
	sph.DropPackets(protocol.EncryptionHandshake, monotime.Now())
	var packets packetTracker
	pn := sph.PopPacketNumber(protocol.Encryption1RTT)
	sph.SentPacket(monotime.Now(), pn, protocol.InvalidPacketNumber, nil, []Frame{packets.NewPingFrame(pn)}, protocol.Encryption1RTT, protocol.ECNNon, 1000, false, false)
	for i := range 3 {
		require.Equal(t, uint64(i), stats.PTOCount.Load())
		require.NoError(t, sph.OnLossDetectionTimeout(sph.GetLossDetectionTimeout()))
	}
	require.Equal(t, uint64(3), stats.PTOCount.Load())
	require.Zero(t, stats.PacketsLost.Load(), "a PTO is a probe, not a loss")
}
