# DFlash 256K results

The selected production setting is **DFlash2 k=5**, with prefix caching
disabled. All generation probes used the 262,272-token server profile, TP2,
PP1, one request, and the direct USB4 NHI path.

## Exact recovery prompt

The reconstructed Pi recovery request contained 65,680 prompt tokens. Each
clean timing row generated 256 tokens from the same prompt hash.

| Mode | TTFT | Output-only TG | Draft acceptance | Mean accepted length |
| --- | ---: | ---: | ---: | ---: |
| Target only | 178.44 s | 9.38 tok/s | — | — |
| DFlash2 k=3 | 178.27 s | 12.16 tok/s | 39.27% | 2.18 |
| DFlash2 k=5 | 175.80 s | 15.12 tok/s | 37.53% | 2.88 |

k=5 improves long-context decode throughput by 61.3% over target-only and
24.4% over k=3. The roughly three-minute TTFT is independent of DFlash depth;
logs identified a first-use Triton compile for the long-sequence
`_deepgemm_fp8_paged_mqa_logits_stage1` shape.

## Short prompt and correctness gate

On the 75-token original request, k=3 and k=5 were statistically tied at 17.84
and 17.87 output tokens/s. The broader HumanEval 0–9 gate produced:

| Mode | Pass@1 | Weighted TG | Range | Aggregate draft acceptance |
| --- | ---: | ---: | ---: | ---: |
| DFlash2 k=7 | 10/10 | 25.28 tok/s | 20.43–27.85 | 61.72% |
| DFlash2 k=5 | 10/10 | 26.10 tok/s | 23.85–27.53 | 69.37% |

## Trace findings

The new PyTorch profile finalized CPU events but exhausted host RAM while
serializing GPU events, so it is not used as a clean timing row. Its CPU trace
contains 5,712 all-reduce calls across the profiled long-context request and
confirms that long prefill and decode are separate phases.

Valid GPU traces from the same runtime lineage show that exposed TP2
communication consumes about 18–21% of GPU time. Target verification grows
from roughly 22% at k=3 to 33% at k=7, making k=5 the measured middle ground.
This supports a future acceptance-aware controller, but no unvalidated dynamic
threshold is enabled in production.

## Operational findings

The former external prefix-cache filesystem tier grew to 219 GiB on rank 0 and
266 GiB on rank 1 despite an 8 GiB CPU-cache setting. Those directories held
regenerable cache data and were cleared after the failed engine stopped. The
production launcher now defaults to cache-off until a disk quota and eviction
policy are implemented.
