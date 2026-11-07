# Plan: Loss Recovery And Redundancy

- Spec: [spec.md](spec.md)
- Moved here from Core spec021's plan on 2026-11-07, when this spec-kit was set up. Core's plan keeps the Core side.

## Series

Three commits on the line, in this order. Each keeps upstream's signatures where it can (spec README rule 3).

### 1. Config and recovery (C1)

- **`interface.go`, `config.go`.** The nine `Config` fields; `populateConfig` copies them as they are, so zero keeps
  meaning "unset". `validateConfig` checks the ranges. `Config.LossRecovery()` (new `loss_recovery.go`) resolves the
  defaults, and every call site reads the resolved values from it.
- **`internal/utils/rtt_stats.go`.** A `defaultRTT` field, 0 meaning `DefaultInitialRTT`, set by `SetDefaultRTT`
  before any sample. It replaces `DefaultInitialRTT` in `ResetForPathMigration` and in `PTO` before a sample.
  `NewRTTStats()` keeps its signature.
- **`internal/ackhandler/received_packet_tracker.go`, `received_packet_handler.go`.** `packetsBeforeAck` becomes the
  tracker's `ackElicitingThreshold` field, initialized to the old constant. `ReceivedPacketHandler.SetAckPolicy`
  sets it and `maxAckDelay`. `NewReceivedPacketHandler(logger)` keeps its signature.
- **`internal/ackhandler/sent_packet_handler.go`.** `timeThreshold` and `packetThreshold` become fields, initialized
  to the old constants; `getScaledPTO` shifts by `min(ptoCount, ptoBackoffLimit)` when a limit is set.
  `NewSentPacketHandler` takes an optional trailing `ackhandler.Recovery`, so its 43 test call sites stay as they are.
- **`connection.go`.** Both constructors pass the `Recovery`, advertise `MaxAckDelay + TimerGranularity`, and
  `preSetup` calls `SetDefaultRTT` and `SetAckPolicy`. `chromeParrotTransportParameters` still runs after, so a parrot
  advertises Chrome's value.

### 2. Copies (C2)

- **`duplicator.go`** (new). Per connection, `nil` when both copy modes are off, so every hook is one nil check:
  - `clone` copies a datagram into a pooled buffer of the right size, before the original reaches the send queue,
    because the queue releases the original once written;
  - `send` queues a copy now (when the queue has room) or puts it on the deadline queue (when `DuplicateDelay` is
    set and the 256 KiB budget allows);
  - `sendDue` sends what is due, `deadline` gives the earliest deadline, `close` releases everything;
  - the tail candidate: with `DuplicateTail`, the non-GSO loop keeps a private copy of the last ack-eliciting datagram
    of the round, marked when the size rule already copied it.
- **`connection.go` hooks.**
  - `appendOneShortHeaderPacket` records whether the packet was ack-eliciting (stream frames or ack-eliciting
    frames) in `Conn.lastPacketAckEliciting`, only when the connection has a duplicator, so the upstream function
    keeps its signature.
  - `sendPacketsWithoutGSO`, `sendPacketsWithGSO`, `maybeSendAckOnlyPacket` and `sendPackedCoalescedPacket` hand
    qualifying datagrams over before `sendQueue.Send`. The first three run only after the handshake is confirmed;
    the coalesced path copies only then, and only a datagram that holds just a short-header packet (1-RTT PTO
    probes), because copies must not count against a server's amplification limit.
  - The GSO batch goes through `sendBatchWithCopies` (in `duplicator.go`) when the connection has a duplicator.
  - The copy counters (`CopiesSent`, `CopyBytesSent`, `CopiesSkipped`) come with this series.
  - The tail: the non-GSO loop sends its candidate when `appendOneShortHeaderPacket` returns `errNothingToPack`, and
    drops it on any other exit; the GSO loop copies the final batch's last ack-eliciting segment when the batch ends
    on `errNothingToPack`.
  - `maybeResetTimer` includes the earliest copy deadline unless the connection is hard-blocked; the run loop calls
    `sendDue` before it checks the send queue; the end of `run` calls `close`.
  - Path probes (`PackPathProbePacket`, written through the transport) and MTU probes (`PackMTUProbePacket`) are not
    handed over.

### 3. Counters (C3)

- **`internal/utils/connstats.go`, `connection.go`.** `SpuriousLosses`, `PTOCount` and `DuplicatesReceived` and
  their public copies in `ConnectionStats`.
- **Loss.** The two `Add` calls move from `cubicSender.OnCongestionEvent` to `detectLostPackets`, at its call of
  `cc.OnCongestionEvent` for a lost packet.
- **`PTOCount`** at both `ptoCount++` sites of `OnLossDetectionTimeout`; **`SpuriousLosses`** in
  `detectSpuriousLosses`; **`DuplicatesReceived`** at the two `IsPotentiallyDuplicate` drops.

## Tests

- The full suite with Core's toolchain before and after (FR-001).
- `loss_recovery_test.go`: config validation and `LossRecovery()` defaults.
- `internal/utils/rtt_stats_test.go`, `internal/ackhandler/received_packet_tracker_test.go`,
  `sent_packet_handler_test.go`: one test per field (FR-002, FR-003), counters (FR-005).
- `duplicator_test.go`: the duplicator against a fake sender (FR-004).
- `integrationtests/self/loss_recovery_test.go`: real connections through the proxy, dropping chosen packets:
  advertised `max_ack_delay`, copies received and dropped, counters under a custom congestion controller.
