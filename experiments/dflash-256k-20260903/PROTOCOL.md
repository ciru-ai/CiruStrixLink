# GLM5.3 256K DFlash investigation

Date: 2026-09-03

## Decision

Determine whether the production DFlash depth of 7 remains beneficial during
long-context generation, and select the smallest safe configuration that keeps
the recovered pagoda workload at or above 10 generated tokens/second.

## Locked baseline

- Model: `GLM5.3-Flash-CIRU-STRIX-IU4`
- Runtime: `0.1.0rc2.dev9+g9255fd9fb9.rocm100`
- Topology: TP2, PP1, one rank per Strix Halo host
- Transport: NHI over the direct USB4 link
- Context profile: 262,272 tokens with an 8 GiB KV cache per host
- Concurrency: one request
- Production speculation: DFlash2, 7 draft tokens, draft TP1

## Gates and probes

1. Correctness gate: HumanEval 0-9 through the mirrored production frontend.
   Require both ranks to remain matched, all requests to complete, and the
   canonical tests to pass before changing speculation.
2. Reproduction baseline: one bounded continuation from the exact saved pagoda
   checkpoint, compared against the prior DFlash depth-7 production gate.
3. Target-only control: repeat the identical continuation with DFlash disabled.
4. Smaller speculation: test depth 3. Test depth 5 only if it can change the
   decision between target-only, depth 3, and production depth 7.
5. Trace the representative speculative candidate separately from clean timing.

## Metrics

- Primary: server-reported generated tokens/second after first token.
- Guardrails: request success, rank agreement, TTFT, output-token count,
  DFlash accepted/drafted tokens, mean accepted length, and acceptance by
  position.
- Practical threshold: at least 10 tokens/second for the long-context probe.
- Material comparison threshold: 2% throughput, provided guardrails pass.

Profiler runs are explanatory and are not used as clean performance rows.
Each load records the complete command identity and is restored to the selected
serving configuration when the campaign ends.

## Outcome

DFlash2 k=5 remains the provisional serving configuration. The original
target-only, k=3, k=5, and HumanEval rows were later found to overlap an active
`flm-npu.service` workload on Ciru and are therefore mixed-workload evidence,
not exclusive-system measurements.

With the NPU stopped, two repetitions of the exact 65,680-token k=5 request
measured 12.93 and 14.56 generated tokens/s, pooling to 13.70 tokens/s. Both
clear the 10 tokens/s operating requirement. A clean target-only and k=3
comparison would require unloading and reloading the model and was deferred to
preserve the active user workload. The HumanEval 10/10 pass@1 result remains a
valid correctness gate, while its throughput figure is labeled mixed workload.

Prefix caching remains disabled because the external filesystem tier is not
byte-bounded. See `RESULTS.md` for measurements, the isolation correction, and
trace findings.
