# DFlash 256K results

The provisional production setting is **DFlash2 k=5**, with prefix caching
disabled. All generation probes used the 262,272-token server profile, TP2,
PP1, one request, and the direct USB4 NHI path.

## Isolation correction

`flm-npu.service` remained active on Ciru throughout the original target-only,
k=3, k=5, and HumanEval measurements below. The service's lifetime memory peak
was 11.5 GiB. Those rows remain useful as a matched directional screen, but
they are mixed-workload measurements rather than exclusive-system results.

After the NPU service was stopped, the exact k=5 long-context request was
repeated twice without unloading or restarting the GLM pair:

| Repetition | TTFT | Output-only TG | Draft acceptance | Mean accepted length |
| --- | ---: | ---: | ---: | ---: |
| NPU-off 1 | 174.79 s | 12.93 tok/s | 29.14% | 2.46 |
| NPU-off 2 | 174.91 s | 14.56 tok/s | 34.26% | 2.71 |
| Pooled / mean | 174.85 s | 13.70 tok/s | — | — |

Both NPU-off repetitions clear the 10 generated tokens/s requirement. They do
not establish whether k=5 is faster than k=3 or target-only in isolation;
making those controls comparable would require unloading and reloading the
model, which was deliberately avoided while it was in use.

## Exact recovery prompt

The reconstructed Pi recovery request contained 65,680 prompt tokens. Each
original timing row generated 256 tokens from the same prompt hash.

| Mixed-workload mode | TTFT | Output-only TG | Draft acceptance | Mean accepted length |
| --- | ---: | ---: | ---: | ---: |
| Target only | 178.44 s | 9.38 tok/s | — | — |
| DFlash2 k=3 | 178.27 s | 12.16 tok/s | 39.27% | 2.18 |
| DFlash2 k=5 | 175.80 s | 15.12 tok/s | 37.53% | 2.88 |

In this initial mixed-workload screen, k=5 measured 61.3% above target-only and
24.4% above k=3. Because NPU activity was not controlled, these percentages
are directional evidence and are not claimed as isolated production gains.
The roughly three-minute TTFT was similar across modes; logs identified a
first-use Triton compile for the long-sequence
`_deepgemm_fp8_paged_mqa_logits_stage1` shape.

## Short prompt and correctness gate

On the 75-token original request, k=3 and k=5 were statistically tied at 17.84
and 17.87 output tokens/s. The broader HumanEval 0–9 gate produced the
following mixed-workload results while `flm-npu.service` remained resident:

| Mode | Pass@1 | Weighted TG | Range | Aggregate draft acceptance |
| --- | ---: | ---: | ---: | ---: |
| DFlash2 k=7 | 10/10 | 25.28 tok/s | 20.43–27.85 | 61.72% |
| DFlash2 k=5 | 10/10 | 26.10 tok/s | 23.85–27.53 | 69.37% |

The 10/10 pass@1 result remains a valid correctness gate. The speed figures
must not be treated as exclusive-system throughput.

## Trace findings

The new PyTorch profile finalized CPU events but exhausted host RAM while
serializing GPU events, so it is not used as a clean timing row. Its CPU trace
contains 5,712 all-reduce calls across the profiled long-context request and
confirms that long prefill and decode are separate phases.

Valid GPU traces from the same runtime lineage show that exposed TP2
communication consumes about 18–21% of GPU time. Target verification grows
from roughly 22% at k=3 to 33% at k=7, making k=5 the measured middle ground.
This supports a future acceptance-aware controller, but no unvalidated dynamic
threshold is enabled in production. k=5 remains loaded provisionally because
both NPU-off repetitions met the operating requirement; a clean target-only
and k=3 comparison is deferred to a maintenance window.

## Operational findings

The former external prefix-cache filesystem tier grew to 219 GiB on rank 0 and
266 GiB on rank 1 despite an 8 GiB CPU-cache setting. Those directories held
regenerable cache data and were cleared after the failed engine stopped. The
production launcher now defaults to cache-off until a disk quota and eviction
policy are implemented.
