# Feature Specification: Acknowledgment Progress

- Feature ID: 002-ack-progress
- Created: 2026-11-08
- Status: Approved (2026-11-08; user: "为啥不能改 fork，明显可以改啊"). Core needs to tell a connection that
  stopped getting acknowledgments from one that is idle; the library knows both and reports neither.
- Line: `fork/2026-11-07/184d081eef3e`, after [spec001](../001-loss-recovery/spec.md)'s series.
- Consumer: [Core spec024](../../../xray-core/spec/024-hy2-relay-session/spec.md) R5: a Hy2 session pool takes no new
  streams on a connection with bytes in flight and no acknowledgment for 1 s.
- Plan: [plan.md](plan.md). Tasks: [tasks.md](tasks.md).

## Problem and Goal

`ConnectionStats` counts packets sent, received and lost, and PTO expirations, but not acknowledgments, and the
bytes in flight stay inside the sent packet handler. Received packets are no proxy: when only the client's direction
is lost, the server keeps retransmitting and the client keeps receiving. Without the two values, Core could only guess
a stall from PTO counts and RTT samples.

Goal: two read-only values on `ConnectionStats`, with no change to sending, recovery or the wire.

## Contract

### C1 Fields

`utils.ConnectionStats` and the public `ConnectionStats` gain:

| Field | Value |
|---|---|
| `PacketsAcked` | Monotonic: packets the peer acknowledged, each counted once, when an ACK frame first covers it (`ReceivedAck`, the packets `detectAndRemoveAckedPackets` returns). A packet declared lost and acknowledged later counts here too, as it does in `SpuriousLosses`. |
| `BytesInFlight` | A gauge: the sent packet handler's `bytesInFlight`, mirrored to an atomic wherever it changes (a sent ack-eliciting packet, `removeFromBytesInFlight`, `ResetForRetry`), so that another goroutine reads it without the connection's lock. |

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | Nothing else changes: the upstream suite and spec001's tests pass | full suite |
| FR-002 | C1: three packets of 1000 bytes are 3000 in flight; acknowledging two counts 2 and leaves 1000; the same ranges again count nothing; a packet declared lost leaves the bytes in flight without counting as acknowledged | unit |

## Non-goals (2026-11-08)

- An acknowledgment timestamp, per packet-number-space values, or an event API: Core samples the counters.
