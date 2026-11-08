# Tasks: Acknowledgment Progress

- [x] T001 spec, plan and tasks; index row in `spec/README.md`.
- [x] T002 C1 fields and FR-002; full suite with Core's toolchain, race on the touched packages; commit on the line.
- [ ] T003 Push the line; Core's `replace` names the commit (Core spec024).

## Evidence
- 2026-11-08 T002: `TestSentPacketHandlerAckProgress` (FR-002) with spec001's recovery test handler; `-race` on
  `internal/ackhandler` and `internal/utils`; `GOTOOLCHAIN=go1.27.0 go test ./... -count=1 -timeout 30m`: exit 0,
  25 packages ok, `integrationtests/self` included (FR-001).
