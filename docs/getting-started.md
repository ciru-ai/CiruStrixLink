# Connect two hosts and use CiruStrixLink

This guide takes two Linux Strix Halo machines from installation to a tested
USB4 connection and browser console, then explains how to enable the direct
GPU/NHI path. Run host commands on the two machines connected by the cable.
A separate desktop only needs a browser and SSH.

| Name in this guide | USB4 address | Example role |
|---|---|---|
| Host A | `10.77.77.1/30` | Console |
| Host B | `10.77.77.2/30` | Peer agent |

These are example endpoint roles, independent of model rank. Model rank must
match the installed weight partition; assigning address A does not make a
machine rank 0. Choose another private `/30` if these addresses conflict with
your existing network.

## 1. Install the same release on both hosts

Use Linux x86-64 on both machines, a data-capable USB4 cable and USB4-capable
ports. The release binary is static; it needs no Go runtime or Python. The
download commands use `curl`, `tar` and `sha256sum`.

On **both A and B**, run this in a Bash shell:

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

The checksum must pass and the binary must report `0.3.3`. If the capability
report identifies missing user-space packages, preview the installer and then
apply its listed actions:

```bash
ciru-strixlink install --include-optional
sudo ciru-strixlink install --include-optional --apply
```

The portable path needs `ip`, `ping`, `thunderbolt-net`, and NetworkManager
for persistent setup. [Distribution-specific help](install/index.md) covers
Ubuntu/Debian, Fedora, Arch and NixOS. NixOS changes remain declarative. The
installer does not supply a patched NHI kernel; step 6 covers that dependency.

## 2. Configure the USB4 network

Connect the cable. On both hosts:

```bash
sudo modprobe thunderbolt-net
ciru-strixlink probe
```

`probe` should identify a USB4 network interface, commonly `thunderbolt0`.
If there is more than one, add `--interface YOUR_INTERFACE` to commands that
select a link. See [troubleshooting](troubleshooting.md) if none appears.

On **A**:

```bash
ciru-strixlink setup --role a
sudo ciru-strixlink setup --role a --apply
ciru-strixlink doctor --peer 10.77.77.2
```

On **B**:

```bash
ciru-strixlink setup --role b
sudo ciru-strixlink setup --role b --apply
ciru-strixlink doctor --peer 10.77.77.1
```

Run both `doctor` checks again after both sides are configured. Require the
correct USB4 route, source address, peer reachability and path MTU. Setup
starts at MTU 1500 and creates a point-to-point connection with no default
gateway or DNS. `--backend iproute2` is available for temporary setup without
NetworkManager.

An existing active NetworkManager profile is preserved. If setup refuses
to replace it, inspect `nmcli connection show`, then either keep that profile
and use `doctor`, or preview setup again with `--take-over` before applying.

## 3. Test both directions

On **B**, keep this terminal open:

```bash
ciru-strixlink serve
```

On **A**:

```bash
ciru-strixlink test --peer 10.77.77.2 \
  --duration 7s --streams 4 --rtt-samples 100 \
  --output "$HOME/ciru-strixlink-report.json" \
  --env-file "$HOME/ciru-strixlink-test.env"
```

The test checks five reconnects, application RTT, 8-MiB integrity transfers in
both directions and isolated upload/download throughput. A healthy result
has passing integrity/reconnects, a valid MTU and a ready quality class.
Exit status 2 means a readiness/MTU gate failed. The report's latency fields
are milliseconds; multiply by 1,000 to express them in microseconds.

The listener binds only B's USB4 address. If needed, permit TCP 55321 only
from A on that interface. Stop it with Ctrl-C after testing and remove any
temporary firewall rule. This unauthenticated first test is for the private
two-host cable; `serve` and `test` both accept `--token-file` for the shared
token configured below.

This test measures **USB4NET/TCP**. It does not execute the GPU all-reduce
benchmarks in [the performance report](performance.md).

To try jumbo frames after MTU 1500 passes, run on the indicated hosts:

```bash
# A
sudo ciru-strixlink setup --role a --mtu 9000 --apply
# B
sudo ciru-strixlink setup --role b --mtu 9000 --apply
```

Repeat both `doctor` checks and the full test. Keep the same MTU on both ends.

## 4. Open the browser console

Create one shared token on **A** and copy it to B through your existing SSH
connection. The example uses OpenSSL and SSH; replace `USER_B@HOST_B` with B's
login and an address you already use for administration. Run this once for a
new pair; preserve an existing token when upgrading:

```bash
install -d -m 0700 "$HOME/.config/ciru-strixlink"
(umask 077; openssl rand -hex 32 > "$HOME/.config/ciru-strixlink/peer.token")
ssh USER_B@HOST_B 'install -d -m 0700 "$HOME/.config/ciru-strixlink"'
scp "$HOME/.config/ciru-strixlink/peer.token" \
  USER_B@HOST_B:.config/ciru-strixlink/peer.token
ssh USER_B@HOST_B 'chmod 0600 "$HOME/.config/ciru-strixlink/peer.token"'
```

On **B**, start the peer agent and leave it running:

```bash
ciru-strixlink agent --token-file "$HOME/.config/ciru-strixlink/peer.token"
```

On **A**, start the console:

```bash
ciru-strixlink ui --peer 10.77.77.2 \
  --token-file "$HOME/.config/ciru-strixlink/peer.token"
```

Open `http://127.0.0.1:7749` in A's browser. To use a separate desktop, run
this on the **desktop**, substituting A's SSH login/address, then open the same
localhost URL there:

```bash
ssh -N -L 7749:127.0.0.1:7749 USER_A@HOST_A
```

Permit peer-agent TCP 7748 only from A over B's USB4 interface if its firewall
requires it. The peer token authenticates agent access; it does not add a
browser login. Keep the console on loopback and use the SSH tunnel.

Connection Setup walks through the two-host configuration. Overview and
Diagnostics show each host's state and the reconciled connection. Launch is
optional and only becomes usable after the GLM prerequisites in step 7.

## 5. Use the portable link with a runtime

On **each host**, select the other host's address and generate a local file:

```bash
PEER=10.77.77.2  # on B use 10.77.77.1
ciru-strixlink transport env --peer "$PEER" \
  --mode portable --runtime generic \
  --output "$HOME/ciru-strixlink-portable.env"
set -a
. "$HOME/ciru-strixlink-portable.env"
set +a
```

Launch your distributed runtime from that shell using its own rank, model and
rendezvous options. The variables select USB4 for compatible RCCL/Gloo socket
traffic; a custom TCP application must bind its own socket to the USB4 address.
StrixLink does not divide a model between hosts or install an inference engine.

For read-only model metrics, restart the console command with
`--model-url http://127.0.0.1:8083` when that is your existing frontend. It reads
model identity and metrics without submitting prompts. In a mirrored TP
deployment use the frontend, not an individual rank API.

## 6. Enable the direct GPU/NHI path

This requires **the patched kernel plus a compatible GPU/runtime adapter**.
Installing the Go binary or setting an environment variable alone does not
enable it. The [Linux 7.2.2 patch guide](../kernel/linux-7.2.2/README.md)
provides the exact recovered source series and its validation scope. The
[published GLM package](models/GLM5.3-Flash-CIRU-STRIX-IU4.md) supplies the M8
HIP bridge and vLLM integration.

With the kernel installed, USB4NET verified and the model stopped, collect
reports into your home directory:

```bash
# A
ciru-strixlink transport status --peer 10.77.77.2 --output "$HOME/host-a.json"
# B
ciru-strixlink transport status --peer 10.77.77.1 --output "$HOME/host-b.json"
```

Copy B's report to A with SSH/SCP, then reconcile on A:

```bash
scp USER_B@HOST_B:host-b.json "$HOME/host-b.json"
ciru-strixlink transport reconcile \
  --a "$HOME/host-a.json" --b "$HOME/host-b.json" \
  --output "$HOME/pair.json" --json
```

Require `portable_ready` and the pair's `arm_allowed` state before preparing
unarmed endpoints. If the exact pair is already `nhi_ready`, inspect its
lease/holder state and use it instead of preparing a duplicate endpoint.

Preview on both hosts first. Then run the two apply commands concurrently in
two terminals; neither host can complete the pairing alone:

```bash
# A
ciru-strixlink transport endpoint prepare --peer 10.77.77.2
sudo ciru-strixlink transport endpoint prepare --peer 10.77.77.2 --apply
# B
ciru-strixlink transport endpoint prepare --peer 10.77.77.1
sudo ciru-strixlink transport endpoint prepare --peer 10.77.77.1 --apply
```

Repeat the report collection, transfer and reconciliation after preparation.
Require **`nhi_ready=true` and `lease_available=true`**, then copy the fresh
`pair.json` to B. Generate a separate environment on each host:

```bash
PEER=10.77.77.2  # on B use 10.77.77.1
ciru-strixlink transport env --peer "$PEER" \
  --mode nhi --runtime vllm --pair-report "$HOME/pair.json" \
  --output "$HOME/ciru-strixlink-nhi.env"
```

The runtime importing the DMA-BUF needs `CAP_SYS_RAWIO`. Use the model's
scoped system-service installation, which stages root-owned executable paths,
rather than granting capabilities to a general-purpose user Python binary.
Follow [the GLM direct-NHI service procedure](models/GLM5.3-Flash-CIRU-STRIX-IU4.md#7-privileged-direct-nhi-system-flow)
to install the model, runtime, frontend and service configuration.

For partial setup, preview `ciru-strixlink transport endpoint cleanup` on
both hosts. Stop the exact device holder if present, then apply cleanup and
recollect both reports. Do not force NHI or silently retain half an endpoint
pair. [Transport lifecycle details](transports.md) explain the states.

## 7. Control the installed GLM pair

After installing both GLM ranks and their scoped helpers, stop the ordinary
console/agent commands from step 4 and restart them with model control.
This example assumes **A is model rank 0 and B is model rank 1**; reverse the
rank arguments if your weight partitions are reversed.

On **B**:

```bash
ciru-strixlink agent --token-file "$HOME/.config/ciru-strixlink/peer.token" \
  --model-control --model-rank 1 --model-peer 10.77.77.1
```

On **A**:

```bash
ciru-strixlink ui --peer 10.77.77.2 \
  --token-file "$HOME/.config/ciru-strixlink/peer.token" \
  --model-url http://127.0.0.1:8083 --model-control --model-rank 0
```

Open `http://127.0.0.1:7749/#/launch`, using the desktop tunnel if needed.
When a scoped permission is missing, the Launch page gives the exact local
NixOS/sudoers setup. Select a context while the pair is unloaded, then Load.
Success requires both services and the frontend's exact context to agree.
Unload stops rank 1 then rank 0. Each process is a foreground command here;
Ctrl-C stops the console/agent, not an independently running model service.

## Where to go next

- [Microsecond latency, correctness and model impact](performance.md)
- [GPU DMA-BUF, packet format and completion architecture](architecture.md)
- [Complete CLI and console reference](cli-and-console.md)
- [Troubleshooting](troubleshooting.md)
- [Security and scoped privileges](../SECURITY.md)
