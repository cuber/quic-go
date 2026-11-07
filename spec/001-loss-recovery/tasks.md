# Tasks: Loss Recovery And Redundancy

- [x] T001 Spec-kit: `spec/README.md`, this spec, plan and tasks; Core spec021's library contract moved here.
- [x] T002 Upstream suite on the unmodified line with Core's toolchain.
- [x] T003 Series 1: config fields, validation, `LossRecovery()`, recovery parameters (C1; FR-002, FR-003).
- [ ] T004 Series 2: copies (C2; FR-004).
- [ ] T005 Series 3: counters (C3; FR-005).
- [ ] T006 Full suite with Core's toolchain, race on the touched packages; push the line (FR-001).

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
