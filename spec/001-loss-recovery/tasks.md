# Tasks: Loss Recovery And Redundancy

- [x] T001 Spec-kit: `spec/README.md`, this spec, plan and tasks; Core spec021's library contract moved here.
- [x] T002 Upstream suite on the unmodified line with Core's toolchain.
- [x] T003 Series 1: config fields, validation, `LossRecovery()`, recovery parameters (C1; FR-002, FR-003).
- [x] T004 Series 2: copies (C2; FR-004).
- [x] T005 Series 3: counters (C3; FR-005).
- [x] T006 Full suite with Core's toolchain, race on the touched packages; push the line (FR-001).
- [x] T007 Count PTO-retransmitted packets as lost (C3 amendment); full suite, race; push (FR-005).

## Evidence

- 2026-11-07 T002: `GOTOOLCHAIN=go1.27.0 go test ./... -count=1 -timeout 30m` at `184d081eef3e` (darwin/arm64):
  exit 0, 25 packages ok and 13 without tests, `integrationtests/self` included.
- 2026-11-07 T003:
  - Unit: `loss_recovery_test.go` (defaults, values, ranges, a connection's resolved values and first PTO),
    `internal/ackhandler/loss_recovery_test.go` (both thresholds, the PTO backoff limit and its 60 s ceiling, the ACK
    policy), `internal/utils/rtt_stats_default_test.go` (initial RTT, path migration, clone).
  - Integration: `TestLossRecoveryMaxAckDelay` on the simulated 10 ms link. The client reads the server's advertised
    `max_ack_delay` as 26 ms by default and 3 ms with 2 ms. A request whose datagram is lost takes 47 ms by default and
    24 ms with 2 ms on both ends.
  - Full suite with go1.27.0: exit 0, 25 packages ok; `-race` on `internal/ackhandler` and `internal/utils`.
- 2026-11-07 T004:
  - Unit (`duplicator_test.go`, a recording send queue): selection by size, a full queue skipped and counted, the
    delay and its order, the 256 KiB delay budget, the tail (last ack-eliciting, only when the round ran out of data,
    never twice), close, and GSO batches (whole batch, last segment, tail).
  - Integration on the simulated link (`integrationtests/self/loss_recovery_test.go`):
    - default: no client datagram appears twice on the wire;
    - `DuplicateMaxSize` 1500: after confirmation every client datagram appears exactly twice, copies equal sent
      packets with MTU discovery off (simnet's 1400-byte MTU drops MTU probes, which are never copied), the server
      copies nothing, and dropping every original costs no round-trip time;
    - `DuplicateDelay`: a request in a 2 ms loss burst takes 14 ms with copies 4 ms behind, 79 ms with copies right
      behind (both lost, PTO);
    - `DuplicateTail`: 1 copy for 160 packets of a 200 KiB write, and a lone request whose original is lost
      completes within one round trip.
  - Full suite with go1.27.0: exit 0, 25 packages ok; `-race` on the root package.
- 2026-11-07 T005:
  - Unit (`internal/ackhandler/loss_recovery_test.go`): with Cubic and with a controller set through
    `SetCongestionControl`, a lost packet counts once in `PacketsLost` and `BytesLost` and reaches the controller
    once, a lost MTU probe counts nothing, a packet acknowledged after it was declared lost counts in
    `SpuriousLosses`; each PTO expiration counts in `PTOCount`, and a PTO is no loss.
  - Integration: the lost request of `TestLossRecoveryMaxAckDelay` counts a PTO; with copies, the server's
    `DuplicatesReceived` equals the copies sent, and when every original is dropped it stays put and the client
    loses nothing; dropping every 10th client datagram of a 300 KiB write counts 25 losses under Cubic and 24 under
    a custom controller, where upstream counted 0.
  - Full suite with go1.27.0: exit 0, 25 packages ok; `-race` on the root package, `internal/ackhandler`,
    `internal/congestion`, `internal/utils` and `integrationtests/self`.
- 2026-11-07 T006: the line pushed to `cuber` at `afe4141e`; Core pins it (Core spec021 T006).
- 2026-11-07 T007, found by Core spec021's netem test (5% random loss on a 7.5 ms path, 300 lone requests): 28 and 30
  PTOs but 2 and 0 packets lost, because `QueueProbePacket` retires the lost packet of a lone request without
  counting it. `TestSentPacketHandlerProbeRetransmissionCounted`: with Cubic and with a custom controller, the probe's
  packet counts once in `PacketsLost` and `BytesLost` with no congestion event, its late acknowledgement counts in
  `SpuriousLosses`, and an MTU probe is never retransmitted this way. `TestLossRecoveryMaxAckDelay` now also
  requires the lost request in `PacketsLost`.  Full suite with go1.27.0: exit 0, 25 packages ok; `-race` on the root package, `internal/ackhandler`,
  `internal/congestion`, `internal/utils` and `integrationtests/self`.
