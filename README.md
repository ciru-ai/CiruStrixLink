# CiruStrixLink

`CiruStrixLink` configures and qualifies a direct Linux USB4 network between two
AMD Strix Halo systems. It is a single binary: the link tests do not require
Python, `iperf3`, or `jq`.

The tool treats USB4's reported link rate as inventory, not proof. It verifies
the selected route, source address, path MTU, reconnect behavior, application
RTT, end-to-end payload integrity, and throughput in each direction. Its JSON
report and environment file are suitable inputs to a model launcher.

The link is model-neutral. Any model server or distributed runtime that can use
an IP socket can use the portable transport. The optional NHI transport is also
model-neutral at the link layer; a runtime needs a small adapter capable of
importing its DMA-BUF. vLLM is one supported overlay, not the owner of the link.

> **CiruStrixLink 0.3.0** adds the redesigned setup workflow and an opt-in
> paired Launch page for GLM5.3 Flash CIRU STRIX IU4. See
> [what changed](#whats-new-in-030) and the [changelog](CHANGELOG.md).

## What's new in 0.3.0

- A guided Connection Setup page replaces the dense configuration form with
  an ordered two-machine workflow, clear readiness states, and reviewable
  commands.
- Launch is now the third tab. It shows both TP ranks, the active context
  profile, process IDs, host-wide unified-memory use, available memory, and
  the configured KV allocation on each machine.
- The console can optionally configure, load, and unload the fixed GLM 5.3
  deployment as one paired operation. It starts rank 0 before rank 1, stops
  rank 1 before rank 0, and does not report a successful load until the model
  frontend serves the exact selected context.
- Fast-mode status now comes from the same scoped privileged inspection on
  both hosts when model control is enabled. Overview, Diagnostics, and Launch
  therefore agree when an NHI pair is ready or already in use.
- The built-in `model-node` helper gives non-NixOS deployments the same narrow
  status/configure/load/unload boundary as the packaged NixOS wrapper.
- Missing permissions now produce copyable, host-specific NixOS and generic
  Linux instructions inside the Launch page.
- A browser disconnect no longer abandons an in-progress paired action. The
  bounded coordinator finishes or runs its rollback path.

Model control remains off by default and does not broaden the ordinary link
setup, reporting, or benchmark permissions.

## What it protects against

- accidental RCCL/Gloo traffic over Wi-Fi, LAN, or Tailscale;
- mismatched jumbo-frame settings;
- the strong directional asymmetry seen on some Strix Halo USB4 links;
- a link that is fast but corrupt, unable to reconnect, or too unstable to use;
- optimistic runtime timeouts copied from a better link.

It cannot turn one cable into physical redundancy. A deployment that must
survive a cable, port, or host failure still needs a second physical path and a
request-level restart/checkpoint policy. `CiruStrixLink` deliberately fails closed
instead of silently moving a latency-sensitive model job to a slower route.

## Requirements

- Linux with the `thunderbolt-net` driver (USB4NET/ThunderboltIP);
- `ip` from iproute2 and `ping` from iputils;
- NetworkManager for persistent cross-distro setup, or iproute2 for a temporary
  setup;
- a USB4 cable and two USB4-capable ports.

Linux documents that loading `thunderbolt-net` on one Linux peer announces it
to the other and creates a virtual Ethernet interface such as `thunderbolt0`:
[Linux USB4 and Thunderbolt documentation](https://docs.kernel.org/admin-guide/thunderbolt.html#networking-over-thunderbolt-cable).

## Prerequisite check

Before setup, run the read-only capability inventory:

```bash
ciru-strixlink prerequisites
```

It lists every required and optional component as available, missing, inactive,
not detected, unsupported, or unknown. Any component needing attention includes
a GitHub link for that exact requirement and, when safe, a suggested command.

A future UI can consume the same versioned data without parsing console text:

```bash
ciru-strixlink prerequisites --json
```

See the [UI integration contract](docs/ui-integration.md), [UI/UX agent
brief](docs/ui-ux-agent-brief.md), and [installation guides](docs/install/index.md).
Exit code 0 means ready, 2 means action is required, and 3 means the detected
platform is unsupported.

## Explicit installation

The installer is intentionally conservative. With no `--apply`, it only shows
the allowlisted actions it would take:

```bash
ciru-strixlink install
ciru-strixlink install --include-optional --self
```

After reviewing the plan, a user can explicitly authorize it:

```bash
sudo ciru-strixlink install --include-optional --self --apply
```

On Ubuntu/Debian, Fedora/RHEL, and Arch, CiruStrixLink can install known
user-space packages such as iproute2, iputils, ethtool, NetworkManager, and
kmod. It can atomically install its own binary. It does not replace a kernel,
reboot, alter firmware, authorize Thunderbolt devices, or imperatively edit
NixOS. Those cases remain linked, distribution-specific instructions. The JSON
plan exposes `can_apply` per action so a later UI can render an explicit Install
button only where the same allowlist says it is safe.

### Install a published Linux release

Run this on both Strix Halo hosts. Set `VERSION` to the release you want to
install:

```bash
VERSION=0.3.0
curl -fL \
  -o /tmp/ciru-strixlink.tar.gz \
  "https://github.com/ciru-ai/CiruStrixLink/releases/download/v${VERSION}/ciru-strixlink-${VERSION}-linux-amd64.tar.gz"
mkdir -p /tmp/ciru-strixlink-release
tar -xzf /tmp/ciru-strixlink.tar.gz -C /tmp/ciru-strixlink-release
sudo install -m 0755 \
  /tmp/ciru-strixlink-release/ciru-strixlink \
  /usr/local/bin/ciru-strixlink
ciru-strixlink version
ciru-strixlink prerequisites
```

If `prerequisites` reports missing user-space packages, preview the allowlisted
installation plan before applying it:

```bash
ciru-strixlink install --include-optional
sudo ciru-strixlink install --include-optional --apply
```

## Build

```bash
make test
make build
sudo install -m 0755 dist/ciru-strixlink /usr/local/bin/ciru-strixlink
```

For a portable Linux binary from another host:

```bash
make linux-amd64
```

## Configure a pair

Connect the cable, then inspect each host:

```bash
ciru-strixlink prerequisites
ciru-strixlink probe
```

Start with MTU 1500. `setup` only prints its plan unless `--apply` is present.
The NetworkManager profile has no gateway or DNS and can never become a default
route.

Host A:

```bash
ciru-strixlink setup --role a
sudo ciru-strixlink setup --role a --apply
```

Host B:

```bash
ciru-strixlink setup --role b
sudo ciru-strixlink setup --role b --apply
```

The defaults are `10.77.77.1/30` and `10.77.77.2/30`. Use a different private
`--subnet` for additional pairs. The aliases `stage1` and `stage0` map to A and
B, but the benchmark may recommend reversing model stage order if the measured
fast direction is different.

If NetworkManager already has an active profile on the USB4 interface,
`CiruStrixLink` refuses to displace it. Inspect the dry run, then add `--take-over`
to switch profiles without deleting the existing one.

The persistent setup uses NetworkManager's documented manual addressing,
`never-default`, and Ethernet MTU properties: [NetworkManager settings
reference](https://networkmanager.dev/docs/api/latest/nm-settings-nmcli.html).
Without NetworkManager, use `--backend iproute2`; that setup ends at reboot.

To remove a profile created by the tool, preview the exact destructive action
first. If setup displaced an older profile, name it explicitly to restore it:

```bash
ciru-strixlink rollback --restore OLD_PROFILE
sudo ciru-strixlink rollback --restore OLD_PROFILE --apply
```

Rollback deletes only the named `ciru-strixlink-usb4` profile by default. The
preserved older profile is never deleted by takeover.

## Verify and benchmark

First verify from each end:

```bash
ciru-strixlink doctor --peer 10.77.77.2   # on A
ciru-strixlink doctor --peer 10.77.77.1   # on B
```

Start the temporary test agent on B:

```bash
ciru-strixlink serve
```

Then run the full test on A:

```bash
ciru-strixlink test \
  --peer 10.77.77.2 \
  --duration 7s \
  --streams 4 \
  --output ciru-strixlink-report.json \
  --env-file ciru-strixlink.env
```

The server binds only to B's USB4 address. If the host firewall blocks TCP
55321, allow that port only on the USB4 interface and only from the peer, then
remove the rule after testing.

To authenticate the temporary test agent, put the same random value in a
mode-0600 file on both peers and pass `--token-file`. The
`CIRU_STRIXLINK_TOKEN` environment variable is also supported.

## Browser console

The same binary serves a browser console. On the peer, start the read-only
agent (Linux only; it binds exclusively to that host's USB4 address):

```bash
ciru-strixlink agent --token-file TOKEN_FILE
```

Then start the console here:

```bash
ciru-strixlink ui --peer 10.77.77.2 --token-file TOKEN_FILE
```

The console listens only on `127.0.0.1:7749` by default. Open the printed
loopback address in a browser on that host. To reach it remotely over a trusted
LAN, opt in explicitly:

```bash
ciru-strixlink ui --listen 0.0.0.0 --peer 10.77.77.2 --token-file TOKEN_FILE
```

With the wildcard listener, `ui` prints loopback, LAN IPv4, and configured
USB4 addresses. Setup, rollback, install, and endpoint actions are preview-only;
applying them still requires the reviewed `sudo ... --apply` command. The
console can also start a bounded link benchmark through the peer agent.

The peer token does not authenticate the browser console. On an untrusted LAN,
keep the default loopback listener and use an authenticated tunnel. Do not
expose ports 7748, 7749, or 55321 to the public internet. See [Security](SECURITY.md).

If NHI inspection needs root, the console and peer agent can remain ordinary
user processes. Set `CIRU_STRIXLINK_STATUS_HELPER` on each to an existing scoped
helper's absolute path. Collection invokes only
`sudo -n HELPER transport-status` and uses its JSON when it identifies the same
host, interface, and peer. The existing sudo policy must already allow that
read-only action; the UI neither grants privileges nor exposes helper commands
through HTTP. Failed inspection stays visibly unverified.

To show the served model and PP/TG in the Overview, point the console at the
existing model frontend (not an individual rank in a mirrored TP deployment):

```bash
ciru-strixlink ui --peer PEER_USB4_ADDRESS --model-url http://127.0.0.1:8083
```

`CIRU_STRIXLINK_MODEL_URL` is the equivalent environment setting. For an
authenticated frontend, set `CIRU_STRIXLINK_MODEL_TOKEN` in the console's
environment. The console only reads `/v1/models` and `/metrics`; it never sends
prompts or changes the model. The API location is shown using the console host's
name when the upstream is loopback. Model metrics refresh every five seconds,
independently of the thirty-second link inspection.

### Paired GLM launch control

The third tab is a Launch page for the fixed two-rank GLM 5.3 deployment.
Control is disabled by default. The normal console and agent remain read-only
until both processes are started with `--model-control`.

The deployment has three parts:

| Location | Process | Network exposure |
|---|---|---|
| Model host A | `ciru-strixlink ui` and its local scoped helper | Loopback only |
| Model host B | `ciru-strixlink agent` and its local scoped helper | Fixed USB4 address only |
| Desktop | Ordinary browser reached through an SSH tunnel | No StrixLink service required |

Create one mode-0600 token file and place the same value on both model hosts
through a trusted administrative channel. Give each StrixLink process its
host's fixed TP rank and the other rank's fixed USB4 IPv4 address.

On the model host that will run the peer agent:

```bash
ciru-strixlink agent \
  --token-file TOKEN_FILE \
  --model-control \
  --model-rank 1 \
  --model-peer RANK_0_USB4_ADDRESS
```

On the model host that will run the console:

```bash
ciru-strixlink ui \
  --peer RANK_1_USB4_ADDRESS \
  --token-file TOKEN_FILE \
  --model-url http://127.0.0.1:8083 \
  --model-control \
  --model-rank 0
```

The rank numbers may be reversed, but they must be complementary and must
match the model's node configuration. Model control refuses a non-loopback
console, a missing model frontend, a missing shared token, or a non-IPv4 peer.

From a desktop, forward the console's loopback port and open the Launch tab:

```bash
ssh -N -L 7749:127.0.0.1:7749 USER@CONSOLE_MODEL_HOST
```

Then open `http://127.0.0.1:7749/#/launch`. The desktop does not need the
StrixLink binary and does not receive model-host privileges.

#### Scoped permissions

On the packaged NixOS deployment, the root-owned
`/run/current-system/sw/bin/glm53-nhi-service-control` wrapper must allowlist
`probe`, `transport-status`, `start`, `stop`, and the three
`context-64k|128k|256k` actions. Grant only that wrapper passwordless access:

```nix
security.sudo.extraRules = [{
  users = [ "MODEL_SERVICE_USER" ];
  commands = [{
    command = "/run/current-system/sw/bin/glm53-nhi-service-control";
    options = [ "NOPASSWD" ];
  }];
}];
```

On other Linux distributions, install the current root-owned StrixLink binary
at `/usr/local/bin/ciru-strixlink` and allow only the exact `model-node`
commands for that host. The Launch page renders the complete host-specific
sudoers fragment—including its service user, all three profiles, and fixed
peer IP—when permission is missing. Validate the fragment with `visudo` and
install it as a root-owned mode-0440 file.

The helper cannot execute arbitrary commands, choose another unit, change a
running context, or silently stop a competing model. A paired load:

1. confirms both ranks are installed, stopped, complementary, and authorized;
2. refuses to proceed while `qwen-main.service` or the portable GLM unit is
   active on either host;
3. verifies the NHI pair is qualified and its exclusive lease is available;
4. writes the selected profile to both stopped ranks;
5. starts rank 0 and then rank 1;
6. waits for the model frontend to serve the exact selected context; and
7. stops both ranks again if startup or readiness fails.

Unload stops rank 1 before rank 0. A browser disconnect does not interrupt the
bounded paired operation. See the
[GLM 5.3 deployment guide](docs/models/GLM5.3-Flash-CIRU-STRIX-IU4.md) for the
complete helper allowlist and model-specific installation procedure.

The vLLM display uses one model's engine-0 counters from one frontend, never a
sum across mirrored ranks. Live output is tokens per polling interval (wall
time). Completed-request TG excludes prefill and the first generated token;
PP counts newly computed KV tokens, excluding cached tokens. Before a new request
finishes, completed-request rates are explicitly labeled "Since engine start";
afterward they show the last request(s) observed. Unsupported or unavailable
metrics show a dash, not an invented rate. Link type describes the inspected
connection; it is not proof that an arbitrary configured model uses that link.

The speed chart retains up to ten minutes of samples in console memory; a page
refresh keeps that history, while a console restart or model/engine change
clears it. Generation uses measured output tokens per polling interval, including
zero output while idle. The prompt-fill view plots completed-request PP samples.
No model requests are generated to populate either chart.

Draft acceptance is accepted draft tokens divided by proposed draft tokens,
excluding bonus target tokens. It uses the latest reported counter increment,
or a clearly labeled since-start total before the first increment. No new drafts
retain the previous sample and its timestamp, rather than showing zero. Set
`CIRU_STRIXLINK_MODEL_SPECULATION=DFlash2` to label the existing speculative
runtime; this is a display label and does not enable or alter speculation.

To review reports a peer sent over, reconcile files instead of a live agent:

```bash
ciru-strixlink ui --report-a host-a.transport.json --report-b host-b.transport.json
```

## Jumbo frames

Only enable MTU 9000 after the 1500-byte setup passes on both ends:

```bash
sudo ciru-strixlink setup --role a --mtu 9000 --apply
sudo ciru-strixlink setup --role b --mtu 9000 --apply
ciru-strixlink doctor --peer PEER_USB4_ADDRESS
```

`doctor` sends a don't-fragment probe at the configured interface MTU. A
failure is a release blocker; do not assume the kernel will repair an MTU
mismatch for a point-to-point link.

## Runtime contract

### Portable and accelerated transports

The portable baseline must be established first and remains the fallback:

```bash
ciru-strixlink transport status --peer 10.77.77.2 \
  --output host-a.transport.json
ciru-strixlink transport status --peer 10.77.77.1 \
  --output host-b.transport.json
ciru-strixlink transport reconcile \
  --a host-a.transport.json --b host-b.transport.json \
  --output pair.transport.json
```

`thunderbolt-net` owns network HopID 8. Optional `thunderbolt_stream` endpoints
must never be started until both reports prove carrier, correct routing, and
peer reachability. A coordinator may then start the reviewed local transaction
on both hosts concurrently:

```bash
sudo ciru-strixlink transport endpoint prepare \
  --peer PEER_USB4_ADDRESS --apply
```

After both commands return, collect fresh status reports and reconcile again.
NHI is selectable only when each peer has exactly one matching endpoint at
HopID 9/9. Any one-sided or partial state requires exact cleanup on both hosts
before portable fallback:

```bash
ciru-strixlink transport endpoint cleanup       # review holders and exact path
sudo ciru-strixlink transport endpoint cleanup --apply
```

Cleanup never kills a process and refuses to remove a device that has a holder.
Legacy endpoints require an explicit `--adopt` after their names, holders, and
parameters have been reviewed. See [transport lifecycle](docs/transports.md).

Once the pair is reconciled, generate an environment for any launcher:

```bash
ciru-strixlink transport env --peer PEER_USB4_ADDRESS \
  --mode auto --runtime generic --pair-report pair.transport.json \
  --output ciru-strixlink.env
```

`auto` selects NHI only when the two-sided report says it is ready; otherwise it
selects the portable socket transport. `--runtime vllm` adds only the vLLM
adapter variable. `generic` and `pytorch` keep the link independent of model
architecture and serving framework.

The generated environment pins RCCL-compatible launchers and Gloo to the
verified interface:

```text
NCCL_SOCKET_IFNAME==thunderbolt0
NCCL_SOCKET_FAMILY=AF_INET
GLOO_SOCKET_IFNAME=thunderbolt0
```

The leading `=` in the RCCL value requests an exact interface match instead of
a prefix match. RCCL documents both `NCCL_SOCKET_IFNAME` and
`NCCL_SOCKET_FAMILY` in its [network environment variable
reference](https://rocm.docs.amd.com/projects/rccl/en/latest/api-reference/env-variables.html#network-and-topology).

It also records the recommended bulk-sender/stage-0 host, quality class,
heartbeat, peer deadline, reconnect budget, maximum in-flight slots, and chunk
size. These values are conservative starting points, not permission to replay
an individual tensor after a failure.

The model transport should use:

- a bounded two-slot credit protocol (one slot on degraded links);
- sequence and generation numbers so a reconnected peer cannot consume stale
  state;
- checksums and explicit payload lengths;
- heartbeats and finite deadlines;
- whole-job or idempotent-request restart after a broken pipeline epoch.

Mid-microbatch transparent replay is unsafe unless the model layer can also
reconstruct both peers' cache and scheduler state.

## GLM5.3 Flash CIRU STRIX IU4

The public model recipe for **GLM5.3 Flash CIRU STRIX IU4** uses
CiruStrixLink to qualify the two-host USB4 path, reconcile the portable and NHI
states, and generate the launcher environment before starting the two TP ranks.
Follow the [model integration guide](docs/models/GLM5.3-Flash-CIRU-STRIX-IU4.md).

The public display name is `GLM5.3 Flash CIRU STRIX IU4`; scripts and API calls
use the filesystem-safe ID `GLM5.3-Flash-CIRU-STRIX-IU4`.

## Quality gates

Classification always uses the weaker isolated direction:

| Class | Minimum weaker direction | RTT p99 | Readiness |
|---|---:|---:|---|
| excellent | 12 Gb/s | 2 ms | ready |
| good | 5 Gb/s | 5 ms | ready |
| constrained | 1 Gb/s | 15 ms | ready with conservative policy |
| degraded | below a gate | any | not ready |

All ready classes additionally require every reconnect and both 8-MiB
integrity tests to pass. Exit status 2 means a readiness or path-MTU gate
failed, making the command suitable for an installer or CI preflight.

## Development

```bash
go test ./...
go vet ./...
```

The wire protocol is versioned and intentionally small. A future incompatible
test agent must increment its protocol version rather than guessing.
