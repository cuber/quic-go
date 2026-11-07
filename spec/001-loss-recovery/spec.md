# Feature Specification: Loss Recovery And Redundancy

- Feature ID: 001-loss-recovery
- Created: 2026-11-07
- Status: Approved (2026-11-07; user: "补丁用 fork 的方式做；开始设计吧"; "quic-go 也要使用 spec-kit 不然后续 rebase 升级就无从下手了";
  "开始吧；这个改好需要经过全量测试"). Amended the same day: PTO-retransmitted packets count as lost (C3), found by
  Core spec021's netem test.
- Line: `fork/2026-11-07/184d081eef3e` (base `184d081eef3e`, apernet `v0.61.0-mod-rename`)
- Consumer: [Core spec021](../../../xray-core/spec/021-quic-loss-recovery/spec.md), which sets these fields from
  `finalmask.quicParams` and reports the counters through `HandlerService.GetQuicStats`. The problem, the measured
  path and the rollout are Core's; this spec is the library contract.
- Plan: [plan.md](plan.md). Tasks: [tasks.md](tasks.md).

## Problem and Goal

Upstream hard-codes the delayed-ACK timer and the `max_ack_delay` it advertises (`protocol.MaxAckDelay`), the ACK
threshold (`packetsBeforeAck`), the initial RTT (`utils.DefaultInitialRTT`), the unbounded PTO backoff
(`<< ptoCount`), and the RFC 9002 loss thresholds (`timeThreshold`, `packetThreshold`). It has no way to send a
datagram twice. It counts `PacketsLost` and `BytesLost` only inside its own Cubic sender, so with a controller set
through `SetCongestionControl` (Hysteria's BBR and Brutal) they stay 0, and it counts no PTOs, spurious losses or
dropped duplicates.

Goal: each of these becomes a `Config` field that is upstream's value or off when unset, and the loss counters stop
depending on the congestion controller.

## Contract

### C1 Config fields

| Field | Type | Unset | Valid | Meaning |
|---|---|---|---|---|
| `MaxAckDelay` | `time.Duration` | 25 ms | 0 or 1 ms – 16382 ms | The local delayed-ACK timer for 1-RTT packets. The `max_ack_delay` transport parameter advertises it plus `protocol.TimerGranularity` (1 ms), truncated to whole milliseconds, as upstream advertises 26 ms today; 16383 ms is the largest value a peer accepts. |
| `AckElicitingThreshold` | `int` | 2 | 0 – 64 | Ack-eliciting 1-RTT packets received before an ACK is queued at once instead of on the timer. The immediate ACKs for a packet reported missing, for new missing packets and for ECN-CE stay as they are. |
| `InitialRTT` | `time.Duration` | 100 ms | 0 or 1 ms – 10 s | The RTT before the first sample: the starting minimum, latest and smoothed RTT, the value after a path migration resets the estimate, and the PTO before a sample (twice this). An RTT restored from a token still applies on top, as upstream. |
| `PTOBackoffLimit` | `int` | unlimited | 0 – 16 | The PTO doubles at most this many times in a row: the shift is `min(ptoCount, limit)`. The 60 s ceiling stays. |
| `LossTimeThreshold` | `float64` | 9/8 | 0 or 1.0 – 4.0 | The time threshold of RFC 9002 loss detection, in RTTs. |
| `LossPacketThreshold` | `int` | 3 | 0 – 64 | The packet reordering threshold of RFC 9002 loss detection. |
| `DuplicateMaxSize` | `uint16` | off | 0 – 65535 bytes | A 1-RTT datagram of at most this many bytes is sent twice (C2). |
| `DuplicateTail` | `bool` | off | | The last ack-eliciting datagram of a send round is sent twice whatever its size (C2). |
| `DuplicateDelay` | `time.Duration` | 0 | 0 – 1 s | The copy leaves this long after its original. Only with `DuplicateMaxSize` or `DuplicateTail`. |

- `validateConfig`, which the `Transport`'s dial and listen paths call, rejects a value outside these ranges with an
  error, as it rejects an invalid version. Upstream does not validate a `GetConfigForClient` result, and neither
  does this spec; Core checks its values before it builds a config.
- `Config.LossRecovery()` returns the values a connection uses: the fields with the unset ones replaced by their
  defaults. Core reports it as the effective configuration.
- With `ChromeParrot`, the advertised `max_ack_delay` stays Chrome's (the default, omitted from the transport
  parameters); `MaxAckDelay` still sets the local timer. A local timer shorter than the advertised value is always
  valid.

### C2 Copies

- **Selection.** A 1-RTT datagram whose size is at most `DuplicateMaxSize` is copied: an ACK-only packet, a PTO
  probe, a small stream or DATAGRAM packet, a PING. With `DuplicateTail`, the last ack-eliciting datagram of a send
  round is copied when the round ends because nothing is left to pack. A round that stops on pacing, the congestion
  window, a full send queue or pending received packets has no tail.
- **GSO.** In a GSO batch the segments are full-size except possibly the last. A size that covers full segments
  copies the whole batch with the same segment size; otherwise the last segment is copied when it qualifies. The tail
  of a GSO round is the last ack-eliciting segment of its final batch.
- **At most once.** A datagram is copied at most once, even when it qualifies by size and as the tail.
- **Identity.** A copy carries the same bytes and the same ECN marking. The receiver drops it by packet number as it
  drops any duplicate. The peer needs no support.
- **Outside recovery.** Copies are never registered with the sent packet handler: they add nothing to bytes in
  flight, the congestion controller and the pacer never see them, and `BytesSent` and `PacketsSent` count originals
  only.
- **Never copied.** Long-header and coalesced datagrams, path probes and path MTU probes, and anything sent before
  the handshake is confirmed: copies bypass the sent packet handler, so they must not count against a server's
  amplification limit. The send loops that copy run only after confirmation; the coalesced path checks it.
- **Queue.** A copy is queued only when the send queue has room; otherwise it is skipped and counted, and the
  original is never held back for it. Delayed copies wait in a per-connection deadline queue of at most 256 KiB; a
  copy that does not fit is skipped and counted. The connection's timer covers the earliest deadline, and closing
  the connection releases what is left without sending it.

### C3 Counters

`utils.ConnectionStats` and the public `ConnectionStats` gain, all monotonic per connection:

| Field | Counts |
|---|---|
| `PacketsLost`, `BytesLost` (existing) | Ack-eliciting packets declared lost, other than path probes and path MTU probes, whatever the congestion controller: by loss detection (`detectLostPackets`, the packets Cubic counted before), and by a PTO probe that retransmits their frames (`QueueProbePacket`, which Cubic never counted). The second kind is how a lone request's lost packet ends, so without it a path that loses only such packets would read 0. It raises no congestion event, since a PTO is no congestion signal; a later acknowledgement counts it in `SpuriousLosses`. An ECN-CE congestion event no longer counts as a lost packet; it is not one. |
| `SpuriousLosses` | Packets declared lost, by either way, and acknowledged later (`detectSpuriousLosses`). |
| `PTOCount` | PTO expirations (`OnLossDetectionTimeout` in PTO mode). |
| `CopiesSent`, `CopyBytesSent` | Copies handed to the send queue, and their bytes. |
| `CopiesSkipped` | Copies not sent because the send queue or the delay queue was full. |
| `DuplicatesReceived` | Packets dropped as potentially duplicate, 1-RTT and long header. `PacketsReceived` keeps counting processed packets, so a received copy shows here and not there; `BytesReceived` keeps counting every datagram. |

## Functional Requirements

| ID | Requirement | Verified by |
|---|---|---|
| FR-001 | A config that sets none of the fields behaves as upstream: the upstream suite passes, and each new path is gated on its field | full suite; unit |
| FR-002 | `MaxAckDelay`: the ACK timer and the advertised transport parameter follow it; the peer's PTO includes the advertised value | unit; integration |
| FR-003 | `AckElicitingThreshold`, `InitialRTT`, `PTOBackoffLimit` and the two loss thresholds change exactly their timer or threshold; out-of-range values are rejected | unit |
| FR-004 | C2: selection, tail, delay, at most once, identity, outside recovery, never-copied packets, full queues skipped and counted, delayed copies released on close | unit; integration |
| FR-005 | C3: every counter, with losses and spurious losses counted under a controller set through `SetCongestionControl`, and nothing counted twice | unit; integration |

## Non-goals (2026-11-07)

- Copying long-header packets, path probes or MTU probes; any congestion control change.
- The ACK frequency extension, FEC and multipath.
- Per-stream or per-packet-type copy policies.
