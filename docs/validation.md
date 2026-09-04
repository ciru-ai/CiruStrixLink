# Validation

## v0.3.2 NPU isolation correction

Date: 2026-09-03.

Review of the Ciru host found that `flm-npu.service` had remained active during
the v0.3.1 DFlash and HumanEval campaign. Its lifetime memory peak was 11.5 GiB,
so the original throughput rows are now labeled mixed workload. The HumanEval
10/10 pass@1 result remains a valid correctness check.

After stopping the NPU, the exact 65,680-token k=5 request was repeated twice
without unloading or restarting the GLM pair. The repetitions measured 12.93
and 14.56 output tokens/s, pooling to 13.70; both exceed the 10 tokens/s
operating requirement. Mean TTFT was 174.85 seconds. A clean target-only and
k=3 comparison was not attempted because it would require disrupting the
active model.

CiruStrixLink 0.3.2 adds explicit `flm-npu.service` detection alongside
`qwen-main.service`. Automated tests cover reporting the NPU workload and
including its exact host and service name in the paired-load blocker.

## v0.3.1 runtime-state and DFlash validation

Date: 2026-09-03.

The complete Go test suite, `go vet`, embedded JavaScript syntax validation,
whitespace checks, and a static Linux amd64 build passed. Live validation found
both ranks loaded with the 262,272-token profile, DFlash2 k=5, prefix caching
disabled, and the qualified NHI pair `in_use`. HumanEval 0–9 passed 10/10. Its
26.10 weighted output tokens/s and the exact recovery probe's 15.12 output
tokens/s were later found to overlap the active Ciru NPU service and are
retained only as mixed-workload measurements. The loaded model was not
restarted or unloaded.

## v0.3.0 paired-launch release validation

Date: 2026-09-03.

The complete Go test suite, `go vet`, JavaScript syntax validation, whitespace
checks, and a static Linux amd64 build passed for the paired-launch release
candidate. The release adds tests for fixed-unit status parsing, host-wide
memory reporting, NixOS and generic helper command mapping, complementary-rank
requirements, competing-model refusal, authorization and confirmation gates,
and rank-1-before-rank-0 unload order.

The already-running production pair was checked read-only during validation:

| Check | Result |
|---|---:|
| Rank 0 / rank 1 system units | active / active |
| Configured context | 262,272 tokens on both ranks |
| KV allocation | 8 GiB per rank |
| Model frontend | connected; exact 262,272-token context reported |
| Privileged transport reconciliation | `in_use` |
| NHI endpoint profile | qualified HopID 9/9 on both hosts |
| Model lifecycle changes during validation | none |

This release validation deliberately did not unload, restart, or reconfigure
the active model. Paired lifecycle ordering and rollback are covered by the
automated HTTP/controller tests; live model serving and status collection were
confirmed without disturbing the user's workload.

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
