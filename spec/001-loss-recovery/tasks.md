# Tasks: Loss Recovery And Redundancy

- [x] T001 Spec-kit: `spec/README.md`, this spec, plan and tasks; Core spec021's library contract moved here.
- [x] T002 Upstream suite on the unmodified line with Core's toolchain.
- [ ] T003 Series 1: config fields, validation, `LossRecovery()`, recovery parameters (C1; FR-002, FR-003).
- [ ] T004 Series 2: copies (C2; FR-004).
- [ ] T005 Series 3: counters (C3; FR-005).
- [ ] T006 Full suite with Core's toolchain, race on the touched packages; push the line (FR-001).

## Evidence

- 2026-11-07 T002: `GOTOOLCHAIN=go1.27.0 go test ./... -count=1 -timeout 30m` at `184d081eef3e` (darwin/arm64):
  exit 0, 25 packages ok and 13 without tests, `integrationtests/self` included.
