# Validation

## v0.2.0 release-candidate requalification

Date: 2026-09-02.

A clean tracked-source snapshot passed `go test ./...`, `go vet ./...`, and a
static Linux amd64 build. That `CiruStrixLink` 0.2.0 binary was copied to
`/tmp` and run directly on both peers without changing their persistent
network configuration.

| Check | Result |
|---|---:|
| Prerequisite inventory | ready on both peers |
| Strix Halo / USB4NET probe | pass on both peers |
| Reciprocal route and source-interface assertion | pass |
| 9000-byte don't-fragment path probe | pass on both peers |
| RTT p50 / p95 / p99 (20 samples) | 0.131 / 0.192 / 0.250 ms |
| Reconnect | 5/5 |
| Upload / download integrity | pass / pass (8 MiB each) |
| Ciru to Sozo | 9.050 Gb/s |
| Sozo to Ciru | 18.797 Gb/s |
| Directional ratio | 2.08x |
| Quality gate | good / ready |

The embedded console and USB4-bound peer agent also started from the same
binary. Console health and live peer collection both returned valid v0.2
schemas. The temporary listeners were stopped after the smoke test, their
ports closed, and the existing `ciru-sozo-usb4` NetworkManager profile and
reciprocal routes remained active.

## v0.1.0 reference baseline

Date: 2026-09-01.

`CiruStrixLink` 0.1.0 was cross-compiled with `CGO_ENABLED=0` and run directly on a
pair of AMD Ryzen AI Max+ 395 systems. Neither peer needed a Go runtime, Python,
`iperf3`, or `jq`.

## Pair

- Linux 7.2.2 on both peers;
- `thunderbolt-net` on `thunderbolt0`;
- `10.77.77.1/30` and `10.77.77.2/30`;
- MTU 9000;
- four TCP streams, three seconds per isolated direction;
- 100 application echo samples;
- five fresh reconnects;
- one 8-MiB checksummed payload in each direction.

## Result

| Check | Result |
|---|---:|
| Route and source-interface assertion | pass |
| 9000-byte don't-fragment path probe | pass |
| RTT p50 / p95 / p99 | 0.131 / 0.134 / 0.156 ms |
| Reconnect | 5/5 |
| Upload integrity | pass |
| Download integrity | pass |
| Endpoint A to endpoint B | 9.433 Gb/s |
| Endpoint B to endpoint A | 18.361 Gb/s |
| Directional ratio | 1.95x |
| Quality gate | good / ready |

The tool selected endpoint B as the bulk sender/stage 0, matching the direction
chosen independently during model transport work.

## Calibration

An earlier `iperf3` four-stream baseline on the same physical pair measured
approximately 9.1 Gb/s and 19.8 Gb/s in the two directions. The built-in Go
test reproduced the important behavior and did not hide the directional
ceiling. Reported adapter speed was only 20,000 Mb/s during this validation,
which further demonstrates why nominal interface speed is not a release gate.

This is one pair, not a compatibility claim for every cable, firmware, kernel,
or port. Public qualification remains the local `doctor` plus full `test` run.
