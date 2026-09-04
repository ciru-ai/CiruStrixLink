# GLM5.3 Flash CIRU STRIX IU4 with CiruStrixLink

This is the public two-node transport recipe for **GLM5.3 Flash CIRU STRIX
IU4**. The tested production path uses two 128-GiB AMD Strix Halo systems,
Linux 7.2.2, ROCm 10, vLLM V2, TP2, PP1, DFlash2 k=5, and direct USB4 NHI transport
for the exposed M8 all-reduce.

The model is not a portable one-command Transformers checkpoint. Its rank-local
weights, gfx1151 kernels, vLLM integration, and transport state must match.
CiruStrixLink is the recommended qualification and transport-environment tool,
not a hard model dependency. An existing USB4/RCCL setup can use the launcher's
`portable` or `external` mode instead.

This is an independent Ciru release. AMD, ROCm, vLLM, Z.ai/GLM, and DFlash2
names and marks remain the property of their respective owners and are used
only to identify compatible hardware and software.

## 1. Install the bundled CiruStrixLink release on both hosts

The model repository includes the qualified CiruStrixLink 0.3.3 archive. Use an
isolated Python environment for the Hugging Face CLI, then extract the bundled
binary:

```bash
HF_ENV="${XDG_DATA_HOME:-$HOME/.local/share}/ciru-hf"
python3 -m venv "$HF_ENV"
"$HF_ENV/bin/pip" install -U huggingface_hub
HF="$HF_ENV/bin/hf"

VERSION=0.3.3
MODEL_REPO=jcbtc/GLM5.3-Flash-CIRU-STRIX-IU4
mkdir -p /tmp/ciru-strixlink-release
"$HF" download "$MODEL_REPO" \
  --include "tools/ciru-strixlink-${VERSION}-linux-amd64.tar.gz" \
  --local-dir /tmp/ciru-strixlink-model-release
tar -xzf \
  "/tmp/ciru-strixlink-model-release/tools/ciru-strixlink-${VERSION}-linux-amd64.tar.gz" \
  -C /tmp/ciru-strixlink-release
sudo install -m 0755 \
  /tmp/ciru-strixlink-release/ciru-strixlink \
  /usr/local/bin/ciru-strixlink
ciru-strixlink prerequisites
```

The companion `ciru-ai/CiruStrixLink` GitHub release is a source and binary
mirror. The model does not depend on that repository being present at runtime.

The browser console is optional. It previews the same plans as the CLI:

```bash
# On the peer, bound only to its USB4 address:
ciru-strixlink agent --token-file TOKEN_FILE

# On the host where you will open the browser:
ciru-strixlink ui --peer PEER_USB4_ADDRESS --token-file TOKEN_FILE
```

The Launch page can also coordinate this fixed GLM deployment as one TP2
service. Model control is off by default. Enable it only with a shared token
and keep the console on loopback.

On the packaged NixOS deployment, use the root-owned
`/run/current-system/sw/bin/glm53-nhi-service-control` wrapper. It allowlists
only `probe`, `transport-status`, `start`, `stop`, and the three
`context-64k|128k|256k` actions. Grant that wrapper `NOPASSWD` access through
`security.sudo.extraRules`. The
Launch page shows a copyable host-specific example whenever this permission is
missing.

On another Linux distribution, grant the service user only the exact helper
commands below. Replace `USER` with the login name on that machine and repeat
the policy on both hosts:

```text
USER ALL=(root) NOPASSWD: /usr/local/bin/ciru-strixlink model-node status --user USER
USER ALL=(root) NOPASSWD: /usr/local/bin/ciru-strixlink model-node transport-status --user USER --peer OTHER_RANK_USB4_IP
USER ALL=(root) NOPASSWD: /usr/local/bin/ciru-strixlink model-node configure --user USER --profile 1
USER ALL=(root) NOPASSWD: /usr/local/bin/ciru-strixlink model-node configure --user USER --profile 2
USER ALL=(root) NOPASSWD: /usr/local/bin/ciru-strixlink model-node configure --user USER --profile 3
USER ALL=(root) NOPASSWD: /usr/local/bin/ciru-strixlink model-node load --user USER
USER ALL=(root) NOPASSWD: /usr/local/bin/ciru-strixlink model-node unload --user USER
```

Install that as a root-owned sudoers fragment with mode `0440`, then start the
USB4-only agent and loopback console with the opt-in flag:

```bash
# Peer machine; listener remains bound to its dedicated USB4 address.
ciru-strixlink agent --token-file TOKEN_FILE --model-control --model-rank 1 \
  --model-peer OTHER_RANK_USB4_IP

# Browser machine; reach this loopback listener locally or through SSH.
ciru-strixlink ui --peer PEER_USB4_ADDRESS \
  --token-file TOKEN_FILE --model-url MODEL_FRONTEND_URL \
  --model-control --model-rank 0
```

Use the ranks configured in the model's node files; the example assumes the
browser machine is rank 0. The explicit rank keeps cold-start orchestration
unambiguous even when no model process is running to inspect. Model control
also requires a fixed IPv4 peer on each process, a shared token, a model
frontend URL on the console, and the console's default loopback-only listener.

The helper accepts only this model, the three packaged context profiles, and
start/stop on its static NHI unit. It never enables a rank at boot and does not
inspect or manage unrelated applications or services. The Launch page
configures both ranks before starting rank 0 and then rank 1; unloading stops
rank 1 and then rank 0. If rank 1 cannot start, it attempts to stop rank 0
again and reports the incomplete recovery instead of claiming success.
Loading is refused while the portable GLM user unit is active, so the two
packaged forms of this same deployment cannot contend for its files and ports.

## 2. Configure and qualify the USB4 network

Start at MTU 1500. Use role `a` on one host and role `b` on the other. Preview
first; `--apply` is the explicit write:

```bash
ciru-strixlink setup --role a
sudo ciru-strixlink setup --role a --apply
```

```bash
ciru-strixlink setup --role b
sudo ciru-strixlink setup --role b --apply
```

Run `doctor` on both hosts. Then start the temporary test server on one host
and run the matched test from the other:

```bash
ciru-strixlink doctor --peer PEER_USB4_ADDRESS
ciru-strixlink serve
```

```bash
ciru-strixlink test \
  --peer PEER_USB4_ADDRESS \
  --duration 7s \
  --streams 4 \
  --output ciru-strixlink-report.json
```

Repeat the test in the opposite direction. Only move both hosts to MTU 9000
after the 1500-byte path passes in both directions. The faster measured sender
should be TP rank 0.

## 3. Generate the ordinary portable environment

The ordinary user-service path is explicit portable USB4 networking. It does
not need an NHI pair report and does not acquire a privileged endpoint lease.
Run this on each host, using the other host's USB4 address:

```bash
STRIX_CONFIG="${XDG_CONFIG_HOME:-$HOME/.config}/GLM5.3-Flash-CIRU-STRIX-IU4"
install -d -m 0700 "$STRIX_CONFIG"
ciru-strixlink transport env \
  --peer PEER_USB4_ADDRESS \
  --mode portable \
  --runtime vllm \
  --output "$STRIX_CONFIG/ciru-strixlink-portable.env"
```

Keep this file at the persistent path shown above. Do not point a service at a
report or environment file in a temporary checkout or current working
directory.

Direct NHI is a separate advanced flow: reconcile both hosts, prepare both
endpoints concurrently, and launch through the privileged system unit in
section 7. Never force NHI after a failed reconciliation.

## 4. Install the model, draft, and pinned runtime

Create the documented storage roots on each host before downloading. The
directories are owned by the ordinary model-service user; the capability-
bearing NHI runtime uses separate root-owned paths described later.

```bash
sudo install -d -o "$(id -un)" -g "$(id -gn)" \
  /srv/llm/models \
  /srv/llm/runtime \
  /srv/llm/venvs \
  /srv/llm/cache
```

Each host downloads the shared configuration and only its own rank-local
weight set:

```bash
HF_ENV="${XDG_DATA_HOME:-$HOME/.local/share}/ciru-hf"
if [[ ! -x "$HF_ENV/bin/hf" ]]; then
  python3 -m venv "$HF_ENV"
  "$HF_ENV/bin/pip" install -U huggingface_hub
fi
HF="$HF_ENV/bin/hf"

MODEL_REPO=jcbtc/GLM5.3-Flash-CIRU-STRIX-IU4
MODEL_ROOT=/srv/llm/models/GLM5.3-Flash-CIRU-STRIX-IU4
NODE_RANK=0  # use 1 on the peer

"$HF" download "$MODEL_REPO" \
  --include "config/*" \
  --include "runtime/**" \
  --include "rank-${NODE_RANK}/*" \
  --include "systemd/*" \
  --include "context-profiles/*" \
  --include "tools/*" \
  --include "launch-node.sh" \
  --include "install-user-service.sh" \
  --include "install-nhi-system-service.sh" \
  --include "select-context-profile.sh" \
  --local-dir "$MODEL_ROOT"
```

DFlash2 is an external dependency and is not duplicated in the target-weight
repository. Pin the validated revision rather than following a moving branch:

```bash
DFLASH_REVISION=bf582e4eacc1810f76656d1811693ff6c6737d2a
"$HF" download incoai/GLM-5.3-Flash-DFlash2 \
  --revision "$DFLASH_REVISION" \
  --local-dir /srv/llm/models/GLM-5.3-Flash-DFlash2
```

The official GLM-5.3-Flash parent is MIT licensed, but the pinned wtdcode AWQ
derivative used to construct these weights declares no model license and
includes no license file. Redistribution clearance for the AWQ-derived target
weights is pending. The external DFlash2 draft is CC BY-NC-ND 4.0, so the
packaged speculative path has a separate non-commercial dependency.

Install the self-contained runtime shipped under `runtime/packages/`. On the
tested NixOS path, import `runtime/packages/nixos-module.nix`, rebuild the host
once, and then run on both hosts:

```bash
cd "$MODEL_ROOT/runtime/packages"
nix-shell ./shell.nix --run 'bash ./INSTALL-RUNTIME.sh'
```

A generic PyPI vLLM build does not contain the GLM hybrid loader, gfx1151 IU4
kernels, DFlash2 V2 integration, or NHI adapter required by this package.

## 5. Launch over the portable path

Use the packaged `launch-node.sh` on both hosts. This example consumes the
persistent portable environment generated in section 3:

```bash
export MODEL_ROOT=/srv/llm/models/GLM5.3-Flash-CIRU-STRIX-IU4
export DFLASH_MODEL=/srv/llm/models/GLM-5.3-Flash-DFlash2
export VLLM_SOURCE=/srv/llm/runtime/vllm-glm53-strix
export VLLM_VENV=/srv/llm/venvs/vllm-rocm10-gfx1151
export VLLM_RUNTIME_ENV=/srv/llm/runtime/vllm-glm53-strix/runtime-env.sh
export AITER_SOURCE=/srv/llm/runtime/aiter-gfx1151
export TRANSPORT_MODE=strixlink
export STRIXLINK_ENV="${XDG_CONFIG_HOME:-$HOME/.config}/GLM5.3-Flash-CIRU-STRIX-IU4/ciru-strixlink-portable.env"
export MASTER_ADDR=RANK_0_USB4_ADDRESS
export PREFIX_CACHE_ROOT=/srv/llm/cache/GLM5.3-Flash-CIRU-STRIX-IU4/prefix-kv

NODE_RANK=0                 # use 1 on the peer
HOST_IP=THIS_HOST_USB4_ADDRESS
EPOCH=1                    # choose the same new value on both hosts
API_PORT=8100
bash "$MODEL_ROOT/launch-node.sh" \
  "$NODE_RANK" "$HOST_IP" "$EPOCH" "$API_PORT"
```

Start rank 0 first and rank 1 immediately after. `MASTER_ADDR`, `MASTER_PORT`,
and `EPOCH` must match on both hosts.

To launch without CiruStrixLink, omit `STRIXLINK_ENV` and choose an
unprivileged mode:

```bash
# Existing USB4 network interface; launcher supplies socket defaults.
export TRANSPORT_MODE=portable
export TRANSPORT_INTERFACE=thunderbolt0

# Or preserve a completely caller-owned RCCL/Gloo environment.
export TRANSPORT_MODE=external
```

External mode preserves explicitly supplied transport variables. Direct NHI
does not belong in this ordinary-user flow; use the capability-bearing system
unit in section 7.

The launcher preserves the release configuration: packaged GLM chat template,
official `temperature=1.0` and `top_p=0.95`, V2, TP2, PP1, DFlash2 k=5, selectable
64K/128K/256K context and KV profiles, chunked prefill, and the GLM tool and
reasoning parsers.

The current production recipe disables automatic prefix caching. The external
filesystem tier has no built-in byte quota and can fill its backing filesystem;
do not enable it until the deployment supplies a real quota and eviction policy.

The managed NHI launcher reads two small runtime settings from
`~/.config/ciru-glm53-iu4/` on each rank. Keep them identical and change them
only while the pair is stopped:

```text
dflash-tokens          # one integer from 0 through 7; production default: 5
prefix-cache-enabled   # 0 disables the cache; current production value: 0
```

Setting `dflash-tokens` to `0` is a true target-only run: the launcher omits the
speculative configuration instead of merely changing a display label. Invalid
values stop the launch with an error; missing files use the packaged k=5 and
cache-off defaults. The Launch page reports these settings only when both ranks
agree; otherwise it shows an unknown or mismatched state.

## 6. Install the ordinary user service

Run the installer on both hosts. It installs and reloads the unit without
starting the model:

```bash
bash "$MODEL_ROOT/install-user-service.sh"
```

Edit `~/.config/GLM5.3-Flash-CIRU-STRIX-IU4/node.env` on each host. Use
different `NODE_RANK`, `HOST_IP`, and local-NVMe cache values, while keeping
`MASTER_ADDR`, `MASTER_PORT`, and `EPOCH` identical. For the managed portable
path, set these two values and use the absolute path for that host:

```text
TRANSPORT_MODE=strixlink
STRIXLINK_ENV=/home/USER/.config/GLM5.3-Flash-CIRU-STRIX-IU4/ciru-strixlink-portable.env
```

Start rank 0 manually, then start rank 1 immediately after on the peer. Do not
enable either unit for independent boot-time startup:

```bash
systemctl --user start GLM5.3-Flash-CIRU-STRIX-IU4.service
journalctl --user -u GLM5.3-Flash-CIRU-STRIX-IU4.service -f
```

The unit intentionally uses `Restart=no`; an isolated rank restart is not a
safe recovery policy for TP2. Stop or restart both hosts as one paired
operation.

Select the same context number on both hosts while the pair is stopped, then
start both units manually again:

```bash
glm53-context list
glm53-context 1   # 64K / 6 GiB KV per rank
glm53-context 2   # 128K / 12 GiB KV per rank
glm53-context 3   # 256K / 8 GiB KV per rank (single-request experimental profile)
```

The selector changes only the next-start environment and never starts or
restarts the service. Profile 3 retains disk-backed prefix caching but reduces
its host staging tier to 1 GiB to preserve unified-memory headroom.

## 7. Privileged direct-NHI system flow

NHI DMA-BUF import requires `CAP_SYS_RAWIO`; an ordinary user service cannot
grant it. Use a distinct NHI environment, a root-owned capability-bearing
runtime, and the packaged static system unit.

First collect a current transport report on each host. Copy both reports to
each host and reconcile them (or reconcile once and copy the resulting pair
report to the other host):

```bash
STRIX_CONFIG="${XDG_CONFIG_HOME:-$HOME/.config}/GLM5.3-Flash-CIRU-STRIX-IU4"
install -d -m 0700 "$STRIX_CONFIG"
ciru-strixlink transport status \
  --peer PEER_USB4_ADDRESS \
  --output "$STRIX_CONFIG/rank-N.transport.json"
```

```bash
ciru-strixlink transport reconcile \
  --a "$STRIX_CONFIG/rank-0.transport.json" \
  --b "$STRIX_CONFIG/rank-1.transport.json" \
  --output "$STRIX_CONFIG/pair.transport.json"
```

Only if reconciliation returns `arm_allowed=true`, preview and apply the
transaction concurrently on both hosts:

```bash
ciru-strixlink transport endpoint prepare --peer PEER_USB4_ADDRESS
sudo ciru-strixlink transport endpoint prepare \
  --peer PEER_USB4_ADDRESS --apply
```

Collect new `transport status` reports from both hosts after preparation,
exchange them, and reconcile again. Make the resulting prepared-pair report
available at the same private path on both hosts:

```bash
ciru-strixlink transport status \
  --peer PEER_USB4_ADDRESS \
  --output "$STRIX_CONFIG/rank-N.prepared.transport.json"

ciru-strixlink transport reconcile \
  --a "$STRIX_CONFIG/rank-0.prepared.transport.json" \
  --b "$STRIX_CONFIG/rank-1.prepared.transport.json" \
  --output "$STRIX_CONFIG/pair-prepared.transport.json"
```

Only when that fresh pair reports `nhi_ready=true` and
`lease_available=true` should each host generate its own persistent NHI
environment:

```bash
ciru-strixlink transport env \
  --peer PEER_USB4_ADDRESS \
  --mode nhi \
  --runtime vllm \
  --pair-report "$STRIX_CONFIG/pair-prepared.transport.json" \
  --output "$STRIX_CONFIG/ciru-strixlink-nhi.env"
```

If either readiness value is false, do not generate or launch NHI. Run the
two-sided cleanup sequence below on both hosts, reconcile fresh reports, and
return to the portable environment only after both endpoints are clean.

Install or build the release runtime directly under the root-owned paths shown
in `systemd/nhi.env.example` (under `/opt/ciru/glm53-iu4`). Do not place
user-writable Python or shared-library code in this capability-bearing path.
Copy the dedicated NHI template itself and edit only its accepted values; do
not merge it into or derive it from the ordinary user-service `node.env`:

```bash
cp "$MODEL_ROOT/systemd/nhi.env.example" "$STRIX_CONFIG/node-nhi.env"
chmod 0600 "$STRIX_CONFIG/node-nhi.env"
```

Keep `NODE_RANK`, `HOST_IP`, and cache paths host-local; keep `MASTER_ADDR`,
`MASTER_PORT`, `EPOCH`, and the context profile paired. The service user must
already belong to the `render` and `video` groups. The installer validates the
allowlisted inputs and root-owned runtime, copies immutable service inputs
under `/etc`, and reloads the system manager without starting the model:

```bash
sudo bash "$MODEL_ROOT/install-nhi-system-service.sh" \
  "$(id -un)" \
  "$STRIX_CONFIG/node-nhi.env" \
  "$STRIX_CONFIG/ciru-strixlink-nhi.env"
```

Stop the portable user unit on both hosts. Start the NHI unit manually on rank
0, then rank 1 immediately after. Do not enable the NHI unit at boot:

```bash
systemctl --user stop GLM5.3-Flash-CIRU-STRIX-IU4.service
sudo systemctl start "GLM5.3-Flash-CIRU-STRIX-IU4-nhi@$(id -un).service"
```

The NHI installer adds a separate root-owned context selector. Run the same
selection on both hosts only while the NHI pair is stopped:

```bash
sudo glm53-nhi-context "$(id -un)" list
sudo glm53-nhi-context "$(id -un)" 1  # or 2 / 3
```

Before releasing the NHI endpoint, stop both ranks. On **each** host, preview
the exact cleanup and then apply it concurrently. Cleanup is a two-sided
operation even when preparation failed partway through:

```bash
sudo systemctl stop "GLM5.3-Flash-CIRU-STRIX-IU4-nhi@$(id -un).service"
ciru-strixlink transport endpoint cleanup
sudo ciru-strixlink transport endpoint cleanup --apply
```

Collect and reconcile fresh status reports from both hosts after cleanup. If
either endpoint still reports a holder or partial state, do not start a
portable fallback until both sides are clean. The unit deliberately does not
perform one-sided automatic cleanup or restart.

## 8. Send a production chat request

Use the chat endpoint so the packaged GLM chat template and reasoning contract
are applied. Do not benchmark this build through raw `generate(prompt)` or the
legacy completions endpoint.

```bash
curl http://RANK_0_HOST:8100/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "GLM5.3-Flash-CIRU-STRIX-IU4",
    "messages": [
      {"role": "user", "content": "Write a robust Python LRU cache."}
    ],
    "temperature": 1.0,
    "top_p": 0.95,
    "reasoning_effort": "max",
    "chat_template_kwargs": {"clear_thinking": true},
    "max_tokens": 4096
  }'
```

For tool use, send OpenAI-compatible `tools` and `tool_choice` fields. The
server is launched with the production `glm47` tool-call and reasoning parsers.
Keep the API on the private USB4 network or place authentication and access
control in front of it; the example request itself does not configure API
authentication.

## Tested versus portable

- Tested release target: two gfx1151 Strix Halo hosts, 128 GiB each, Linux
  7.2.2, ROCm 10, one request at a time.
- Qualified fallback: USB4NET sockets selected by CiruStrixLink; equivalent
  existing interfaces can use `portable` or `external` mode.
- Performance path: matching `/dev/tbstream0` endpoints at HopID 9/9 and the
  packaged M8 NHI adapter through the privileged system unit.
- Not yet claimed: one-host execution, mixed GPU types, arbitrary kernels,
  Windows serving, or an unmodified upstream vLLM install.
