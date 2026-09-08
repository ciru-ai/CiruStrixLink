# Transport performance on two Strix Halo systems

The useful latency metric for distributed inference is a **completed GPU
operation**, including the work needed to make the received tensor usable.
The NHI experiments measure that boundary in **microseconds (µs)**. TCP echo
latency and cable bandwidth appear later as supporting link diagnostics.

These are saved August 30–September 2, 2026 measurements on Ciru and Sozo,
two Ryzen AI Max+ 395 / Radeon 8060S (`gfx1151`) systems. They are not new
v0.3.3 benchmark runs. StrixLink 0.3.3 updates the console's model-control
policy; it does not change the transport or model runtime.

## Exact BF16 tensor-parallel all-reduce

Each rank contributes a contiguous **BF16 `[8,4096]` tensor: 65,536 payload
bytes**. Both ranks exchange their partial simultaneously and compute the
same elementwise sum. This matches the eight-position verification shape
used during the GLM DFlash2 investigation.

| Backend | Rank | Median, µs | Mean, µs | p90, µs | Maximum absolute error |
|---|---|---:|---:|---:|---:|
| RCCL over USB4NET | Sozo | 342.745 | 347.975 | 389.653 | 0 |
| Direct NHI BF16 | Sozo | **109.897** | **114.980** | **128.281** | **0** |
| RCCL over USB4NET | Ciru | 340.904 | 347.479 | 387.096 | 0 |
| Direct NHI BF16 | Ciru | **110.807** | **114.577** | **127.666** | **0** |

That is **67.5–67.9% lower median completion latency**, or about **3.1×** the
operation rate in this isolated test. It is not a claim of 3.1× model speed.

The gate used Linux 7.2.2 with the qualified USB4STREAM patches, Torch
2.13.0+rocm10.0.0, the one-channel RCCL control, 16 warmups and 64 measured
calls per rank. Timing used a host monotonic clock around each operation with
GPU synchronization before and after it. The NHI timing includes GPU pack,
exchange, validation, sum, and synchronous completion; it is not bare wire
flight time. Buffers and transport pools were allocated outside the timed loop.

All **80 exchanges per rank** completed with **zero timeouts, protocol errors,
driver failures, or dropped driver events**. Both backends matched the BF16
reference exactly. The records expose the HIP component version as
`7.15.26333`; the SDK was ROCm 10, not a separately named ROCm 7.15 release.

Raw records: [RCCL](benchmarks/m8-rccl.jsonl),
[direct NHI](benchmarks/m8-bf16_nhi.jsonl). The recorded timing boundary and
source provenance are in [the evidence manifest](benchmarks/provenance.json).

## GPU activation transfer with consumption credit

This is a separate one-way pipeline experiment. The producer sends a GPU
activation and receives a **32-byte credit only after the consumer GPU has
consumed and validated it**. Producer full-cycle time includes that return
credit; it is not interchangeable with the all-reduce table above.

| Activation shape | Encoding | Producer median, µs | Sustained period, µs |
|---|---|---:|---:|
| T=1 | BF16 | **45.505** | — |
| T=1 | I8G32 | 83.067 | — |
| T=128 | BF16 | 1,642.803 | 1,673.04 |
| T=128 | I8G32 | **1,043.402** | **1,059.07** |

For one token, compression costs more than it saves: BF16 is the faster
path. At T=128, the integrated group-32 INT8 codec reduces median cycle time
by **599.401 µs / 36.49%**, and sustained period by **36.70%**. Both T128 arms
passed 400 cycles, final-output checks, carrier guards, event accounting and
the final rendezvous.

This proves the synthetic activation/codec mechanism. It does not establish
real-model accuracy for compressed activations, intermediate token counts,
or a production I8G32 all-reduce. In fact, the separate M8 I8G32 all-reduce
implementation failed its numerical gate and was rejected. The published GLM
M8 adapter uses **exact BF16**, not that rejected compressed reduction.

## Larger all-reduce and prefill

A separate September 1 experiment used BF16 `[768,4096]`: **6,291,456 bytes
per rank**, with 160-block GPU pack/sum kernels appropriate to the larger
payload.

| Backend | Sozo median, µs | Ciru median, µs |
|---|---:|---:|
| RCCL | 4,961 | 4,943 |
| Direct NHI BF16 | **4,516** | **4,521** |

The component saved **8.54–8.98%**, remained bit-exact, and completed all 32
exchanges per rank without transport errors. A scoped live PP768 model row
improved **472.241 → 477.314 prompt tokens/s (+1.07%)** with the same first
token. Only about 42 exact-shape calls used that path; the rest remained RCCL.
This is a historical PP768 experiment, not an additional shape enabled by
installing StrixLink 0.3.3 or the published M8-only bridge.

## What translated into model speed

The completion schedule matters. The first synchronous M8 model integration
did not convert the component's 110-µs result into a serving gain. Host pack
synchronization, polling, event retirement and RX reposting prevented the
runner from enqueueing subsequent work. A later one-deep deferred-retirement
experiment became useful after the fixed-M8 expert alignment path was repaired.

| Matched development workload | RCCL | Deferred NHI | Change |
|---|---:|---:|---:|
| Synthetic all-accept verification rate | 5.521743/s | 5.745559/s | **+4.05%** |
| Synthetic all-accept output | 44.173941 tok/s | 45.964476 tok/s | **+4.05%** |
| Natural structured output | 21.2319 tok/s | 23.092215 tok/s | **+8.76%** |

The synthetic control forced acceptance of 175/175 draft proposals and
measures transaction capacity, not model quality or ordinary output speed.
The natural comparison accepted **154/329 proposals in both arms**, so its
gain did not come from accepting more drafts. These are bounded development
comparisons, not a repeated production benchmark suite.

The **published GLM kernel source archive contains the synchronous M8
bridge**. The deferred results document a separate development variant and
must not be attributed to installing the StrixLink binary. See the
[source and release boundary](architecture.md#source-and-release-boundary).

The released model's recorded HumanEval 0–9 sample passed **10/10** at
**26.10 weighted generated tokens/s**, with **69.4% draft acceptance**.
That is whole-model evidence with the custom kernels, quantization and
DFlash2 together; it is not an isolated NHI uplift. One other inference
workload was resident. See the [model card and settings](https://huggingface.co/jcbtc/GLM5.3-Flash-CIRU-STRIX-IU4#performance).

## Protocol overhead, memory and reliability

- The M8 bridge sends **65,536 bytes + a 32-byte footer = 65,568 bytes**,
  across 17 frames of up to 4 KiB. The footer adds **0.0488%** to the logical
  payload, excluding lower-level USB4 framing.
- A rank allocates separate persistent TX and RX pools, each
  `4095 × 4096 = 16,773,120 bytes`, for **31.99 MiB combined**. Mapped control
  state, HIP events and output tensors are additional allocations. This is
  pool capacity, not a process-RSS or CPU-utilization measurement.
- Patch-16 qualification passed **32/32 fresh endpoint lifecycles**, alternating
  directions; **1,024 verified frames per direction**; and **65,536 verified
  4-KiB frames / 256 MiB per direction** with clean teardown.
- The qualified page-backed soak used a **32-entry ring**, separately from the
  M8 bridge's 4,095-entry ring. Its sender throughput was 1,889.6 MB/s
  Sozo → Ciru and 1,039.8 MB/s in reverse. These are not GPU all-reduce rates.
- The local reliability patch clears stale MSI-X completion status before
  rearming a reused ring. It fixed a reproduced missing first completion;
  a cable reconnect alone did not establish that property.

CPU utilization, power, and an isolated production end-to-end NHI/RCCL sweep
were not measured in the recovered final release evidence. No values are
inferred for them.

## Supporting USB4NET qualification

The built-in `ciru-strixlink test` exercises **TCP over USB4NET**, not the
GPU/NHI adapter. Its September 2 v0.2.0 result was:

| Metric | Result |
|---|---:|
| Ciru → Sozo | 9.050 Gb/s |
| Sozo → Ciru | 18.797 Gb/s |
| Application echo p50 / p95 / p99 | **130.975 / 192.473 / 249.991 µs** |
| Echo samples | 20 |
| Reconnects | 5/5 |
| CRC32 payload integrity | Passed, 8 MiB each direction |
| Path MTU | 9000 bytes, passed |

The echo exchanges eight bytes on an established TCP connection. It is not
one-way wire latency or a GPU collective. The built-in test measures the two
throughput directions in isolation. The original iperf3 full-duplex experiment
recorded 9.705 + 19.764 = **29.469 Gb/s aggregate**, with zero retransmits;
aggregate means the sum of simultaneous opposite directions.

For your own pair, follow [the setup and test guide](getting-started.md).
Cable, port, firmware, kernel, tensor shape and runtime scheduling all affect
the result. The [validation history](validation.md) retains release checks.
