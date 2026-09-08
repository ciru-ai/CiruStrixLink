# CiruStrixLink

**Direct GPU tensor transport and a two-host operator console for AMD Strix Halo over USB4.**

CiruStrixLink prepares and manages the USB4 connection behind the
[GLM5.3 Flash CIRU STRIX IU4 deployment](https://huggingface.co/jcbtc/GLM5.3-Flash-CIRU-STRIX-IU4).
Its direct NHI path uses **GPU-owned DMA-BUF pools and USB4 hardware DMA rings**
to exchange tensor partials without the TCP/IP payload path. The accompanying
HIP adapter validates the received message and performs the reduction on the
GPU. The same connection retains USB4NET for control and other traffic.

The project brings that transport together with endpoint discovery,
correctness checks, runtime configuration and an embedded browser console.
The measured exact 64-KiB BF16 all-reduce completed in **about 110 µs**, versus
**about 342 µs** through the matched RCCL socket path. The complete setup
requires the StrixLink binary, the patched stream driver and a compatible
runtime adapter; their roles and sources are documented below.

[Get started](docs/getting-started.md) ·
[GPU transport architecture](docs/architecture.md) ·
[Measurements and methodology](docs/performance.md) ·
[Download v0.3.3](https://github.com/ciru-ai/CiruStrixLink/releases/tag/v0.3.3) ·
[GLM deployment](docs/models/GLM5.3-Flash-CIRU-STRIX-IU4.md)

## What makes the transport useful

- **GPU-owned transfer pools.** Dedicated uncached HIP allocations are exported
  as DMA-BUFs and imported by USB4STREAM. GPU kernels pack TX and consume RX;
  the CPU manages submission and completion rather than copying tensor payloads
  through socket buffers.
- **Exact two-rank reductions.** The published adapter exchanges contiguous
  BF16 `[8,4096]` partials in both directions and sums on the GPU. Unmatched
  collectives retain the runtime's existing backend.
- **Explicit message ownership.** A 32-byte footer carries rank, epoch,
  sequence and payload length, with a GPU-written commit word. Circular ring
  addressing, completion accounting and RX reposting govern safe reuse.
- **A qualified two-host lifecycle.** StrixLink checks both peers, preserves
  USB4NET's HopID allocation, discovers the stream devices, and requires
  matching endpoints and an available lease before launching NHI.
- **An operator console.** Inspect transport state, run bounded TCP link tests,
  watch model PP/TG and draft acceptance, and optionally configure/load/unload
  the packaged GLM pair with coordinated readiness and rollback.

The console is a single static Go binary. Its link tests need no Python,
`iperf3`, or Go installation. Other runtimes can use the portable IP link;
using the direct GPU path requires an adapter for their tensor operations.

## Measured GPU-operation latency

Recorded on two Ryzen AI Max+ 395 / Radeon 8060S systems with the qualified
Linux 7.2.2 kernel and ROCm 10. These are completed-operation timings in
**microseconds**, not cable ping figures.

| Operation | Control | Direct NHI result | Evidence |
|---|---:|---:|---|
| 64-KiB BF16 `[8,4096]` all-reduce, median | RCCL **340.904–342.745 µs** | **109.897–110.807 µs** | Exact sums; 67.5–67.9% less latency |
| Single-token GPU activation + consumer credit | — | **45.505 µs** | BF16 pipeline mechanism gate |
| T128 activation + credit, median | BF16 **1,642.803 µs** | I8G32 **1,043.402 µs** | 36.49% shorter cycle; experimental codec |
| 6-MiB BF16 `[768,4096]` all-reduce, median | RCCL **4,943–4,961 µs** | **4,516–4,521 µs** | Exact sums; separate PP768 experiment |

The M8 gate used 16 warmups and 64 measured calls per rank. All 80 NHI
exchanges per rank passed with zero timeouts, protocol errors, driver failures
or dropped events. [Raw records and timing boundaries](docs/performance.md#exact-bf16-tensor-parallel-all-reduce)
are published with the results.

The **~3.1× M8 operation-rate improvement is a component result**. Model
impact depends on how much communication is exposed and how completion is
scheduled. A later deferred-retirement development comparison improved
verification cadence **4.05%** under fixed synthetic acceptance, and a natural
structured request improved **21.23 → 23.09 tok/s** with the same accepted
proposal count. Those experiments are distinct from the synchronous bridge
in the published GLM source archive. The [performance report](docs/performance.md)
identifies each variant, including the compressed path's scope and rejected
M8 INT8 reduction.

## Install and connect

Run the following on **both Linux x86-64 hosts**. Download tools are `curl`,
`tar` and `sha256sum`; StrixLink reports any missing network dependencies.

```bash
VERSION=0.3.3
RELEASE_DIR=$(mktemp -d)
cd "$RELEASE_DIR"
RELEASE_URL="https://github.com/ciru-ai/CiruStrixLink/releases/download/v${VERSION}"
curl -fLO "$RELEASE_URL/ciru-strixlink-${VERSION}-linux-amd64.tar.gz"
curl -fLO "$RELEASE_URL/SHA256SUMS"
sha256sum -c SHA256SUMS
mkdir unpacked
tar -xzf "ciru-strixlink-${VERSION}-linux-amd64.tar.gz" -C unpacked
sudo install -m 0755 unpacked/ciru-strixlink /usr/local/bin/ciru-strixlink
ciru-strixlink version
ciru-strixlink prerequisites
```

Connect the USB4 cable, load `thunderbolt-net` on both hosts, and configure
complementary addresses. Setup previews its changes until `--apply` is given.

```bash
# Host A: 10.77.77.1/30
sudo modprobe thunderbolt-net
ciru-strixlink setup --role a
sudo ciru-strixlink setup --role a --apply

# Host B: 10.77.77.2/30
sudo modprobe thunderbolt-net
ciru-strixlink setup --role b
sudo ciru-strixlink setup --role b --apply
```

After both sides are configured, run `doctor` on each against the other
address. Start the temporary test server on B, then test from A:

```bash
# B: leave running until the test finishes
ciru-strixlink doctor --peer 10.77.77.1
ciru-strixlink serve

# A: in a separate host terminal
ciru-strixlink doctor --peer 10.77.77.2
ciru-strixlink test --peer 10.77.77.2 --duration 7s --streams 4 \
  --output "$HOME/ciru-strixlink-report.json"
```

Stop the test listener with Ctrl-C. The test uses TCP 55321 over the private
USB4 interface and checks IP transport, integrity and reconnects. It does not
run the GPU/NHI benchmarks above. The [complete guide](docs/getting-started.md)
covers dependencies, existing profiles, tokens, firewall ports, jumbo frames,
runtime environment files and the direct-NHI setup.

## Browser console

Create the same private token file on both hosts as described in
[step 4 of the guide](docs/getting-started.md#4-open-the-browser-console).
Then run:

```bash
# Host B
ciru-strixlink agent --token-file "$HOME/.config/ciru-strixlink/peer.token"

# Host A
ciru-strixlink ui --peer 10.77.77.2 \
  --token-file "$HOME/.config/ciru-strixlink/peer.token"
```

Open `http://127.0.0.1:7749` on A. From a separate desktop, use
`ssh -N -L 7749:127.0.0.1:7749 USER_A@HOST_A` and open the same localhost URL.
The agent binds USB4 TCP 7748; the console stays on loopback. Peer tokens
protect agent access, not the browser listener.

Add `--model-url http://127.0.0.1:8083` to show an existing model frontend's
identity and metrics. The optional Launch tab requires the installed GLM
runtime, fixed complementary ranks, shared token and scoped model helpers.
[Step 7](docs/getting-started.md#7-control-the-installed-glm-pair) gives the
commands for each host. Model control is disabled by default.

## Enable the direct GPU path

| Layer | What to install or configure |
|---|---|
| Kernel | [16-patch Linux 7.2.2 series](kernel/linux-7.2.2/README.md), including DMA-BUF import and the stale-MSI-X reliability fix |
| Link | Verified USB4NET first; then prepare both NHI endpoints and reconcile HopID 9/9, ring 4095, throttling 8192 ns and lease availability |
| Runtime | [Published GLM HIP bridge and custom vLLM](docs/models/GLM5.3-Flash-CIRU-STRIX-IU4.md), or an explicitly integrated adapter for another runtime |
| Privilege | Scoped system service granting `CAP_SYS_RAWIO` to the importing runtime with root-owned executable paths |

Use the [ordered two-host NHI instructions](docs/getting-started.md#6-enable-the-direct-gpunhi-path).
`--runtime vllm` generates configuration; it does not retrofit stock vLLM with
the adapter. [Architecture](docs/architecture.md) explains GPU allocations,
packet layout, completion scheduling and the source/release boundary.

## Documentation and source

- [Getting started](docs/getting-started.md) — install, connect, test, console, runtime and paired GLM launch.
- [Architecture](docs/architecture.md) — management plane, GPU data path, DMA-BUF, frame protocol and completion.
- [Performance](docs/performance.md) — µs latency, numerical correctness, model impact, reliability and evidence.
- [CLI and console reference](docs/cli-and-console.md) — full command examples and metric definitions.
- [Transport lifecycle](docs/transports.md) — pair state, ownership, arming and cleanup.
- [Troubleshooting](docs/troubleshooting.md) — route, MTU, dependency and connection failures.
- [Changelog](CHANGELOG.md) · [Validation](docs/validation.md) · [Security](SECURITY.md).

## Build and credits

```bash
git clone https://github.com/ciru-ai/CiruStrixLink.git
cd CiruStrixLink
make test
make vet
make linux-amd64
```

The binary is written to `dist/ciru-strixlink-linux-amd64`; building requires
Go and Make. CiruStrixLink's Go utility is [Apache-2.0](LICENSE). The bundled
Linux patches retain GPL-2.0 and their original authors; the published HIP
bridge retains MIT. The kernel foundation includes Linux USB4STREAM and
[jyatesdotdev/strix-rdma](https://github.com/jyatesdotdev/strix-rdma), with
Ciru's Linux 7.2.2 rebases, reliability fix, GPU/runtime integration and
operator tooling. [Source identities and license boundaries](docs/architecture.md#source-and-release-boundary)
are documented explicitly.
