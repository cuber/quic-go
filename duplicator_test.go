package quic

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/apernet/quic-go/internal/monotime"
	"github.com/apernet/quic-go/internal/protocol"
	"github.com/apernet/quic-go/internal/utils"

	"github.com/stretchr/testify/require"
)

// Tests for spec/001-loss-recovery C2.

type sentDatagram struct {
	data    []byte
	gsoSize uint16
	ecn     protocol.ECN
}

// recordingSender is a send queue that records what it is given and releases the buffers.
type recordingSender struct {
	blocked bool
	sent    []sentDatagram
}

var _ sender = &recordingSender{}

func (s *recordingSender) Send(p *packetBuffer, gsoSize uint16, ecn protocol.ECN) {
	if s.blocked {
		panic("Send on a blocked queue")
	}
	s.sent = append(s.sent, sentDatagram{data: bytes.Clone(p.Data), gsoSize: gsoSize, ecn: ecn})
	p.Release()
}
func (s *recordingSender) SendProbe(*packetBuffer, net.Addr, packetInfo) {}
func (s *recordingSender) Run() error                                    { return nil }
func (s *recordingSender) WouldBlock() bool                              { return s.blocked }
func (s *recordingSender) Available() <-chan struct{}                    { return nil }
func (s *recordingSender) Close()                                        {}

func datagram(size int, b byte) []byte { return bytes.Repeat([]byte{b}, size) }

func TestDuplicatorOff(t *testing.T) {
	require.Nil(t, newDuplicator(LossRecovery{DuplicateDelay: time.Second}, &utils.ConnectionStats{}))
	require.NotNil(t, newDuplicator(LossRecovery{DuplicateMaxSize: 1}, &utils.ConnectionStats{}))
	require.NotNil(t, newDuplicator(LossRecovery{DuplicateTail: true}, &utils.ConnectionStats{}))
}

func TestDuplicatorBySize(t *testing.T) {
	var stats utils.ConnectionStats
	d := newDuplicator(LossRecovery{DuplicateMaxSize: 100}, &stats)
	require.Nil(t, d.cloneIfSmall(datagram(101, 1)))

	var sq recordingSender
	now := monotime.Now()
	original := datagram(100, 2)
	cp := d.cloneIfSmall(original)
	require.NotNil(t, cp)
	original[0] = 9 // the queue may reuse the original's buffer once written
	d.send(&sq, cp, 0, protocol.ECT0, now)
	require.Equal(t, []sentDatagram{{data: datagram(100, 2), ecn: protocol.ECT0}}, sq.sent)
	require.Equal(t, uint64(1), stats.CopiesSent.Load())
	require.Equal(t, uint64(100), stats.CopyBytesSent.Load())

	// a full queue skips the copy
	sq.blocked = true
	d.send(&sq, d.cloneIfSmall(datagram(10, 3)), 0, protocol.ECNNon, now)
	require.Len(t, sq.sent, 1)
	require.Equal(t, uint64(1), stats.CopiesSkipped.Load())

	tailOnly := newDuplicator(LossRecovery{DuplicateTail: true}, &stats)
	require.Nil(t, tailOnly.cloneIfSmall(datagram(1, 1)))
}

func TestDuplicatorDelay(t *testing.T) {
	var stats utils.ConnectionStats
	d := newDuplicator(LossRecovery{DuplicateMaxSize: 100, DuplicateDelay: 5 * time.Millisecond}, &stats)
	var sq recordingSender
	now := monotime.Now()

	require.Zero(t, d.deadline())
	d.send(&sq, d.cloneIfSmall(datagram(10, 1)), 0, protocol.ECNNon, now)
	d.send(&sq, d.cloneIfSmall(datagram(20, 2)), 0, protocol.ECNNon, now.Add(time.Millisecond))
	require.Empty(t, sq.sent)
	require.Equal(t, now.Add(5*time.Millisecond), d.deadline())

	d.sendDue(&sq, now.Add(5*time.Millisecond-1))
	require.Empty(t, sq.sent)
	d.sendDue(&sq, now.Add(5*time.Millisecond))
	require.Equal(t, [][]byte{datagram(10, 1)}, sentData(sq.sent))
	require.Equal(t, now.Add(6*time.Millisecond), d.deadline())

	// a due copy that meets a full queue is skipped
	sq.blocked = true
	d.sendDue(&sq, now.Add(time.Second))
	require.Zero(t, d.deadline())
	require.Equal(t, uint64(1), stats.CopiesSent.Load())
	require.Equal(t, uint64(1), stats.CopiesSkipped.Load())
}

func TestDuplicatorDelayBudget(t *testing.T) {
	var stats utils.ConnectionStats
	d := newDuplicator(LossRecovery{DuplicateMaxSize: 2000, DuplicateDelay: time.Millisecond}, &stats)
	var sq recordingSender
	now := monotime.Now()
	n := int(maxDelayedCopyBytes / 1000)
	for range n + 3 {
		d.send(&sq, d.cloneIfSmall(datagram(1000, 1)), 0, protocol.ECNNon, now)
	}
	require.Equal(t, uint64(3), stats.CopiesSkipped.Load())
	d.sendDue(&sq, now.Add(time.Millisecond))
	require.Len(t, sq.sent, n)
	require.Zero(t, d.delayedBytes)

	// close releases what is still waiting
	d.send(&sq, d.cloneIfSmall(datagram(1000, 1)), 0, protocol.ECNNon, now)
	d.close()
	require.Zero(t, d.deadline())
	d.sendDue(&sq, now.Add(time.Hour))
	require.Len(t, sq.sent, n)
}

func TestDuplicatorTail(t *testing.T) {
	var stats utils.ConnectionStats
	d := newDuplicator(LossRecovery{DuplicateTail: true}, &stats)
	var sq recordingSender
	now := monotime.Now()

	// the last ack-eliciting datagram of a round that ran out of data
	d.noteTail(datagram(1200, 1), true, false, protocol.ECNNon)
	d.noteTail(datagram(300, 2), true, false, protocol.ECT1)
	d.noteTail(datagram(40, 3), false, false, protocol.ECNNon) // ACK-only
	d.endRound(&sq, true, now)
	require.Equal(t, []sentDatagram{{data: datagram(300, 2), ecn: protocol.ECT1}}, sq.sent)

	// a round that stopped for another reason has no tail
	d.noteTail(datagram(300, 4), true, false, protocol.ECNNon)
	d.endRound(&sq, false, now)
	d.endRound(&sq, true, now)
	require.Len(t, sq.sent, 1)

	// a candidate the size rule copied is not copied again
	d.noteTail(datagram(300, 5), true, true, protocol.ECNNon)
	d.endRound(&sq, true, now)
	require.Len(t, sq.sent, 1)
	require.Equal(t, uint64(1), stats.CopiesSent.Load())

	d.noteTail(datagram(300, 6), true, false, protocol.ECNNon)
	d.close()
	d.endRound(&sq, true, now)
	require.Len(t, sq.sent, 1)
}

func TestDuplicatorNumDatagrams(t *testing.T) {
	require.Equal(t, uint64(1), numDatagrams(1000, 0))
	require.Equal(t, uint64(1), numDatagrams(1000, 1200))
	require.Equal(t, uint64(3), numDatagrams(2500, 1200))
	require.Equal(t, uint64(2), numDatagrams(2400, 1200))
}

func TestDuplicatorGSOBatch(t *testing.T) {
	const seg = 100
	batch := func() *packetBuffer {
		buf := getLargePacketBuffer()
		for i := range 3 {
			buf.Data = append(buf.Data, datagram(seg, byte(i+1))...)
		}
		buf.Data = append(buf.Data, datagram(40, 4)...)
		return buf
	}
	run := func(r LossRecovery, tail gsoTail, nothingLeft bool) [][]byte {
		var sq recordingSender
		c := &Conn{sendQueue: &sq, dup: newDuplicator(r, &utils.ConnectionStats{})}
		c.sendBatchWithCopies(batch(), seg, protocol.ECNNon, tail, nothingLeft, monotime.Now())
		require.Equal(t, uint16(seg), sq.sent[0].gsoSize)
		return sentData(sq.sent[1:])
	}
	whole := batch().Data
	lastTail := gsoTail{start: 3 * seg, end: 3*seg + 40, ok: true}
	secondTail := gsoTail{start: seg, end: 2 * seg, ok: true}

	// a size covering full segments copies the whole batch
	require.Equal(t, [][]byte{whole}, run(LossRecovery{DuplicateMaxSize: seg, DuplicateTail: true}, lastTail, true))
	// otherwise the short last segment, once even when it is also the tail
	require.Equal(t, [][]byte{datagram(40, 4)}, run(LossRecovery{DuplicateMaxSize: 50, DuplicateTail: true}, lastTail, true))
	// the tail when the last ack-eliciting segment is another one
	require.Equal(t, [][]byte{datagram(40, 4), datagram(seg, 2)}, run(LossRecovery{DuplicateMaxSize: 50, DuplicateTail: true}, secondTail, true))
	// no tail unless the round ran out of data
	require.Empty(t, run(LossRecovery{DuplicateTail: true}, lastTail, false))
	require.Equal(t, [][]byte{datagram(40, 4)}, run(LossRecovery{DuplicateTail: true}, lastTail, true))
	require.Empty(t, run(LossRecovery{DuplicateTail: true}, gsoTail{}, true))
}

func sentData(sent []sentDatagram) [][]byte {
	var data [][]byte
	for _, s := range sent {
		data = append(data, s.data)
	}
	return data
}
