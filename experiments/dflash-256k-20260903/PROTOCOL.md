# GLM5.3 256K DFlash investigation

Date: 2026-09-03

## Decision

Determine whether the production DFlash depth of 7 remains beneficial during
long-context generation, and identify the fixed depth that gives the highest
measured throughput on the recovered pagoda workload without breaking the
correctness gate.

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
- Primary decision: highest repeatable throughput on the exact long-context
  probe, provided guardrails pass.
- Material comparison threshold: 2% throughput.

Profiler runs are explanatory and are not used as clean performance rows.
Each load records the complete command identity and is restored to the selected
serving configuration when the campaign ends.

## Outcome

DFlash2 k=5 is the best fixed depth measured on the exact request: 15.12
generated tokens/s, versus 12.16 at k=3 and 9.38 target-only. This is a
low-acceptance prose stress case, not a general model-throughput baseline.
HumanEval 0–9 passed 10/10 at 26.10 weighted generated tokens/s. Prefix caching
remains disabled because the external filesystem tier is not byte-bounded. See
`RESULTS.md` for measurements and trace findings.
