# GLM5.3 Flash CIRU STRIX UI4 with CiruStrixLink

This is the public two-node transport recipe for **GLM5.3 Flash CIRU STRIX
UI4**. The tested production path uses two 128-GiB AMD Strix Halo systems,
Linux 7.2.2, ROCm 10, vLLM V2, TP2, DFlash2 k=7, and direct USB4 NHI transport
for the exposed M8 all-reduce.

The model is not a portable one-command Transformers checkpoint. Its rank-local
weights, gfx1151 kernels, vLLM integration, and transport state must match.

## 1. Install CiruStrixLink on both hosts

```bash
VERSION=0.2.0
curl -fL \
  -o /tmp/ciru-strixlink.tar.gz \
  "https://github.com/ciru-ai/CiruStrixLink/releases/download/v${VERSION}/ciru-strixlink-${VERSION}-linux-amd64.tar.gz"
mkdir -p /tmp/ciru-strixlink-release
tar -xzf /tmp/ciru-strixlink.tar.gz -C /tmp/ciru-strixlink-release
sudo install -m 0755 \
  /tmp/ciru-strixlink-release/ciru-strixlink \
  /usr/local/bin/ciru-strixlink
ciru-strixlink prerequisites
```

The browser console is optional. It previews the same plans as the CLI:

```bash
# On the peer, bound only to its USB4 address:
ciru-strixlink agent --token-file TOKEN_FILE

# On the host where you will open the browser:
ciru-strixlink ui --peer PEER_USB4_ADDRESS --token-file TOKEN_FILE
```

## 2. Configure and qualify the USB4 network

Start at MTU 1500. Use role `a` on one host and role `b` on the other:

```bash
ciru-strixlink setup --role a
sudo ciru-strixlink setup --role a --apply
```

```bash
ciru-strixlink setup --role b
sudo ciru-strixlink setup --role b --apply
```

Run `doctor` on both hosts. Then start the temporary test server on one host and
run the matched test from the other:

```bash
ciru-strixlink doctor --peer PEER_USB4_ADDRESS
ciru-strixlink serve
```

```bash
ciru-strixlink test \
  --peer PEER_USB4_ADDRESS \
  --duration 7s \
  --streams 4 \
  --output ciru-strixlink-report.json \
  --env-file ciru-strixlink.env
```

Only move both hosts to MTU 9000 after the 1500-byte path passes. The faster
measured sender should be TP rank 0.

## 3. Reconcile portable and NHI transport

Collect a fresh report on each host and reconcile the pair:

```bash
ciru-strixlink transport status \
  --peer PEER_USB4_ADDRESS \
  --output this-host.transport.json
```

Move one report to the other host, then:

```bash
ciru-strixlink transport reconcile \
  --a rank-0.transport.json \
  --b rank-1.transport.json \
  --output pair.transport.json
```

If the pair report says the accelerated endpoint can be prepared, review and
apply the endpoint transaction concurrently on both hosts:

```bash
ciru-strixlink transport endpoint prepare --peer PEER_USB4_ADDRESS
sudo ciru-strixlink transport endpoint prepare \
  --peer PEER_USB4_ADDRESS --apply
```

Collect and reconcile fresh reports after preparation. Generate the vLLM
environment from the reconciled pair on both hosts:

```bash
ciru-strixlink transport env \
  --peer PEER_USB4_ADDRESS \
  --mode auto \
  --runtime vllm \
  --pair-report pair.transport.json \
  --output ciru-strixlink.env
```

Do not manually force NHI after a failed reconciliation. `auto` keeps the
qualified portable socket path as the fallback.

## 4. Install the model and runtime

The planned Hugging Face repository is
`jcbtc/GLM5.3-Flash-CIRU-STRIX-UI4`. Each host needs the shared configuration,
runtime integration, and only its own rank-local weight set:

```bash
MODEL_REPO=jcbtc/GLM5.3-Flash-CIRU-STRIX-UI4
MODEL_ROOT=/srv/llm/models/GLM5.3-Flash-CIRU-STRIX-UI4
NODE_RANK=0  # use 1 on the peer

hf download "$MODEL_REPO" \
  --include "config/*" "runtime/*" "rank-${NODE_RANK}/*" \
            "systemd/*" "launch-node.sh" "install-user-service.sh" \
  --local-dir "$MODEL_ROOT"
```

DFlash2 remains an external dependency and is not duplicated in the target
weight repository:

```bash
hf download incoai/GLM-5.3-Flash-DFlash2 \
  --local-dir /srv/llm/models/GLM-5.3-Flash-DFlash2
```

The target repository follows the official GLM-5.3-Flash MIT license. The
external DFlash2 draft currently uses CC BY-NC-ND 4.0, so the full speculative
path has a separate non-commercial dependency.

Install the pinned Ciru vLLM/ROCm runtime named by the model release. A generic
PyPI vLLM build does not contain the GLM hybrid loader, gfx1151 UI4 kernels,
DFlash2 V2 integration, or NHI adapter required by this package.

## 5. Launch the production path

Use the packaged `launch-node.sh` on both hosts. Start rank 0 first and rank 1
immediately after:

```bash
export MODEL_ROOT=/srv/llm/models/GLM5.3-Flash-CIRU-STRIX-UI4
export DFLASH_MODEL=/srv/llm/models/GLM-5.3-Flash-DFlash2
export VLLM_SOURCE=/srv/llm/runtime/vllm-glm53-strix
export VLLM_VENV=/srv/llm/venvs/vllm-rocm10-gfx1151
export VLLM_RUNTIME_ENV=/srv/llm/runtime/vllm-glm53-strix/runtime-env.sh
export STRIXLINK_ENV="$PWD/ciru-strixlink.env"
export MASTER_ADDR=RANK_0_USB4_ADDRESS
export PREFIX_CACHE_ROOT=/path/on/local/nvme/GLM5.3-Flash-CIRU-STRIX-UI4

bash "$MODEL_ROOT/launch-node.sh" \
  NODE_RANK HOST_IP EPOCH API_PORT
```

The launch script preserves the production configuration: packaged GLM chat
template, official `temperature=1.0` and `top_p=0.95`, V2, TP2, DFlash2 k=7,
6-GiB KV cache per rank, 65,664-token model length, chunked prefill, GLM tool
and reasoning parsers, and the qualified M8 NHI transport.

The launcher also enables vLLM automatic prefix caching with an 8-GiB host
LRU tier and persistent local-NVMe filesystem tier. Set `PREFIX_CACHE_ROOT` to
a fast local path on each host. Only prompt blocks are offloaded; generated
tails are not written to disk. The filesystem cache persists across restarts
and has no built-in byte quota, so place it in a dedicated cache directory or
quota-managed filesystem.

## 6. Install the user systemd service

Run this on both hosts. It installs and reloads the unit without starting the
model:

```bash
bash "$MODEL_ROOT/install-user-service.sh"
```

Edit `~/.config/GLM5.3-Flash-CIRU-STRIX-UI4/node.env`. Use different rank,
host-IP, and local-NVMe cache values on each host, while keeping the master
address, master port, and NHI epoch identical. Then start the two units as one
paired operation:

```bash
systemctl --user enable --now GLM5.3-Flash-CIRU-STRIX-UI4.service
journalctl --user -u GLM5.3-Flash-CIRU-STRIX-UI4.service -f
```

The unit intentionally uses `Restart=no`; an isolated rank restart is not a
safe recovery policy for TP2 plus NHI. Enable user lingering separately if the
service must start before login.

## 7. Send a production chat request

Use the chat endpoint so the packaged GLM chat template and reasoning contract
are applied. Do not benchmark this build through raw `generate(prompt)` or the
legacy completions endpoint.

```bash
curl http://RANK_0_HOST:8100/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "GLM5.3-Flash-CIRU-STRIX-UI4",
    "messages": [
      {"role": "user", "content": "Write a robust Python LRU cache."}
    ],
    "temperature": 1.0,
    "top_p": 0.95,
    "reasoning_effort": "max",
    "max_tokens": 4096
  }'
```

For tool use, send OpenAI-compatible `tools` and `tool_choice` fields. The
server is launched with the production `glm47` tool-call and reasoning parsers.

## Tested versus portable

- Tested release target: two gfx1151 Strix Halo hosts, 128 GiB each, Linux
  7.2.2, ROCm 10, one request at a time.
- Qualified fallback: USB4NET sockets selected by CiruStrixLink.
- Performance path: matching `/dev/tbstream0` endpoints at HopID 9/9 and the
  packaged M8 NHI adapter.
- Not yet claimed: one-host execution, mixed GPU types, arbitrary kernels,
  Windows serving, or an unmodified upstream vLLM install.
