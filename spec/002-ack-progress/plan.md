# Plan: Acknowledgment Progress

- Spec: [spec.md](spec.md)

## Code

- `internal/utils/connstats.go`: `PacketsAcked` and `BytesInFlight` (`atomic.Uint64`).
- `internal/ackhandler/sent_packet_handler.go`: `ReceivedAck` adds the newly acknowledged packets; `SentPacket`,
  `removeFromBytesInFlight` and `ResetForRetry` store `bytesInFlight` after changing it.
- `connection.go`: the public fields and `ConnectionStats()`.

## Tests

- `internal/ackhandler/ack_progress_test.go`: FR-002, with spec001's recovery test handler.
- Full suite with Core's toolchain; `-race` on `internal/ackhandler` and `internal/utils`.

## Carrying

One commit, `feat(spec002): …`, after spec001's series on each new line.
