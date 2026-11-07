package quic

import (
	"time"

	"github.com/apernet/quic-go/internal/monotime"
	"github.com/apernet/quic-go/internal/protocol"
	"github.com/apernet/quic-go/internal/utils"
)

// maxDelayedCopyBytes bounds the copies one connection holds back for DuplicateDelay.
const maxDelayedCopyBytes protocol.ByteCount = 256 << 10

// A duplicator sends the redundant copies of spec/001-loss-recovery C2: byte-identical
// datagrams that loss recovery, congestion control and the pacer never see.
// A connection has one only when DuplicateMaxSize or DuplicateTail is set.
// It is used from the connection's run loop only.
type duplicator struct {
	maxSize protocol.ByteCount // 0: no size-based copies
	tail    bool
	delay   time.Duration
	stats   *utils.ConnectionStats

	// copies waiting for DuplicateDelay, in deadline order
	delayed      []delayedCopy
	delayedBytes protocol.ByteCount

	// the tail candidate of the current send round
	tailBuf    *packetBuffer
	tailECN    protocol.ECN
	hasTail    bool
	tailCopied bool // the candidate was already copied by size
}

type delayedCopy struct {
	buf     *packetBuffer
	gsoSize uint16
	ecn     protocol.ECN
	due     monotime.Time
}

func newDuplicator(r LossRecovery, stats *utils.ConnectionStats) *duplicator {
	if r.DuplicateMaxSize == 0 && !r.DuplicateTail {
		return nil
	}
	return &duplicator{
		maxSize: protocol.ByteCount(r.DuplicateMaxSize),
		tail:    r.DuplicateTail,
		delay:   r.DuplicateDelay,
		stats:   stats,
	}
}

func (d *duplicator) bySize(size protocol.ByteCount) bool {
	return d.maxSize != 0 && size <= d.maxSize
}

// cloneIfSmall returns a copy of a datagram the size rule selects, or nil.
// It must be called before the original is handed to the send queue, which releases it once written.
func (d *duplicator) cloneIfSmall(data []byte) *packetBuffer {
	if !d.bySize(protocol.ByteCount(len(data))) {
		return nil
	}
	return clonePacketBuffer(data)
}

func clonePacketBuffer(data []byte) *packetBuffer {
	var buf *packetBuffer
	if len(data) <= protocol.MaxPacketBufferSize {
		buf = getPacketBuffer()
	} else {
		buf = getLargePacketBuffer()
	}
	buf.Data = append(buf.Data, data...)
	return buf
}

// send queues a copy after its original was handed to the send queue: at once, or after the delay.
// It takes ownership of buf.
func (d *duplicator) send(sq sender, buf *packetBuffer, gsoSize uint16, ecn protocol.ECN, now monotime.Time) {
	if d.delay == 0 {
		d.sendNow(sq, buf, gsoSize, ecn)
		return
	}
	if d.delayedBytes+buf.Len() > maxDelayedCopyBytes {
		d.skip(buf, gsoSize)
		return
	}
	d.delayed = append(d.delayed, delayedCopy{buf: buf, gsoSize: gsoSize, ecn: ecn, due: now.Add(d.delay)})
	d.delayedBytes += buf.Len()
}

func (d *duplicator) sendNow(sq sender, buf *packetBuffer, gsoSize uint16, ecn protocol.ECN) {
	if sq.WouldBlock() {
		d.skip(buf, gsoSize)
		return
	}
	d.stats.CopiesSent.Add(numDatagrams(buf.Len(), gsoSize))
	d.stats.CopyBytesSent.Add(uint64(buf.Len()))
	sq.Send(buf, gsoSize, ecn)
}

func (d *duplicator) skip(buf *packetBuffer, gsoSize uint16) {
	d.stats.CopiesSkipped.Add(numDatagrams(buf.Len(), gsoSize))
	buf.Release()
}

// numDatagrams is the number of datagrams a buffer is sent as.
func numDatagrams(size protocol.ByteCount, gsoSize uint16) uint64 {
	if gsoSize == 0 || size == 0 {
		return 1
	}
	return uint64((size + protocol.ByteCount(gsoSize) - 1) / protocol.ByteCount(gsoSize))
}

// sendDue sends the delayed copies whose time has come.
func (d *duplicator) sendDue(sq sender, now monotime.Time) {
	var n int
	for n < len(d.delayed) && !d.delayed[n].due.After(now) {
		c := d.delayed[n]
		d.delayedBytes -= c.buf.Len()
		d.sendNow(sq, c.buf, c.gsoSize, c.ecn)
		n++
	}
	if n == 0 {
		return
	}
	m := copy(d.delayed, d.delayed[n:])
	clear(d.delayed[m:])
	d.delayed = d.delayed[:m]
}

// deadline is when the next delayed copy is due, or zero.
func (d *duplicator) deadline() monotime.Time {
	if len(d.delayed) == 0 {
		return 0
	}
	return d.delayed[0].due
}

// noteTail records a datagram of the current send round as the tail candidate when it is
// ack-eliciting. copied says the size rule already copied it.
func (d *duplicator) noteTail(data []byte, ackEliciting, copied bool, ecn protocol.ECN) {
	if !d.tail || !ackEliciting {
		return
	}
	if d.tailBuf == nil || d.tailBuf.Cap() < protocol.ByteCount(len(data)) {
		if d.tailBuf != nil {
			d.tailBuf.Release()
		}
		d.tailBuf = clonePacketBuffer(data)
	} else {
		d.tailBuf.Data = append(d.tailBuf.Data[:0], data...)
	}
	d.tailECN = ecn
	d.hasTail = true
	d.tailCopied = copied
}

// endRound ends a send round. The tail candidate is copied only when the round ended
// because nothing was left to send.
func (d *duplicator) endRound(sq sender, nothingLeft bool, now monotime.Time) {
	if d.hasTail && nothingLeft && !d.tailCopied {
		buf := d.tailBuf
		d.tailBuf = nil
		d.send(sq, buf, 0, d.tailECN, now)
	}
	d.hasTail = false
}

// close releases what is left when the connection closes.
func (d *duplicator) close() {
	for _, c := range d.delayed {
		c.buf.Release()
	}
	d.delayed = nil
	d.delayedBytes = 0
	if d.tailBuf != nil {
		d.tailBuf.Release()
		d.tailBuf = nil
	}
	d.hasTail = false
}

// gsoTail locates the last ack-eliciting segment of a GSO batch.
type gsoTail struct {
	start, end protocol.ByteCount
	ok         bool
}

// sendBatchWithCopies sends a GSO batch and its copies (spec/001-loss-recovery C2): the whole batch
// when the size rule covers full segments, otherwise its last segment when that one qualifies, and the
// tail when the round ends with this batch because nothing is left to send.
func (c *Conn) sendBatchWithCopies(buf *packetBuffer, segSize protocol.ByteCount, ecn protocol.ECN, tail gsoTail, nothingLeft bool, now monotime.Time) {
	d := c.dup
	var batchCopy, lastCopy, tailCopy *packetBuffer
	lastStart := (buf.Len() - 1) / segSize * segSize
	switch {
	case d.bySize(segSize):
		batchCopy = clonePacketBuffer(buf.Data)
	case d.bySize(buf.Len() - lastStart):
		lastCopy = clonePacketBuffer(buf.Data[lastStart:])
	}
	if d.tail && nothingLeft && tail.ok && batchCopy == nil && (lastCopy == nil || tail.start != lastStart) {
		tailCopy = clonePacketBuffer(buf.Data[tail.start:tail.end])
	}
	c.sendQueue.Send(buf, uint16(segSize), ecn)
	if batchCopy != nil {
		d.send(c.sendQueue, batchCopy, uint16(segSize), ecn, now)
	}
	if lastCopy != nil {
		d.send(c.sendQueue, lastCopy, 0, ecn, now)
	}
	if tailCopy != nil {
		d.send(c.sendQueue, tailCopy, 0, ecn, now)
	}
}
