# quic-go Fork Specifications

This directory holds the specifications of `github.com/cuber/quic-go`, our fork of `apernet/quic-go` (GitHub now
lists it as `HyNetworks/quic-go`; the old name redirects). Core uses it through a `replace` of
`github.com/apernet/quic-go` ([Core spec022](../../xray-core/spec/022-quic-go-fork/spec.md)). The module path stays
`github.com/apernet/quic-go`. Every patch on a fork line belongs to one specification here.

## Lines

- Lines, generations, remotes and pins follow [Core spec023](../../xray-core/spec/023-fork-lines/spec.md) and its
  [SOP](../../xray-core/spec/023-fork-lines/sop.md). The register of lines is Core's
  [lines.md](../../xray-core/spec/023-fork-lines/lines.md); this repository keeps no copy.
- The current line is `fork/2026-11-07/184d081eef3e`, on apernet's `v0.61.0-mod-rename` (quic-go `v0.61.0` plus
  apernet's 7 commits).
- `origin` is upstream and only read; `cuber` is ours. Upstream branches copied into the fork carry none of our
  commits.

## Rules

1. **One spec per concern.** Its patches are linear commits on the line, named `feat(specNNN): …`,
   `fix(specNNN): …` or `test(specNNN): …`, each with its tests.
2. **Unset is upstream.** A new `Config` field is off, or at upstream's value, when unset. A connection built from a
   config that sets none of them behaves as upstream's, and upstream's suite passes unchanged, except where a spec
   names the difference.
3. **Small diffs.** Prefer new files, setters and optional parameters over changing upstream signatures and tests,
   so that carrying a series to a new upstream base meets few conflicts.
4. **Toolchain.** Tests run with Core's Go toolchain, the one Core's `go.mod` names: today
   `GOTOOLCHAIN=go1.27.0 go test ./...`.
5. **Carrying.** A new line takes each spec's commits in order (SOP section 3), runs that spec's tests and the full
   suite, and records the result in the spec's `tasks.md`. A spec whose behavior upstream takes over is retired with a
   dated note, and its commits are dropped from the next line.
6. **Public.** This repository is public: no credentials, addresses or private configuration in commits, specs or
   messages.

## Index

| ID | Spec | Status |
|---|---|---|
| [001](001-loss-recovery/spec.md) | Loss recovery and redundancy: `Config` fields for the delayed-ACK timer, ACK threshold, initial RTT, PTO backoff limit and loss thresholds; byte-identical copies of small and tail datagrams outside loss recovery; loss counted whatever the congestion controller, and new `ConnectionStats` counters. For [Core spec021](../../xray-core/spec/021-quic-loss-recovery/spec.md) | Approved 2026-11-07 |
| [002](002-ack-progress/spec.md) | Acknowledgment progress: `ConnectionStats.PacketsAcked` (packets acknowledged, once each) and `BytesInFlight` (the sender's bytes in flight, read without the connection's lock). For [Core spec024](../../xray-core/spec/024-hy2-relay-session/spec.md) | Approved 2026-11-08 |

Next spec: 003.
