# Production integration

CiruStrixLink is the production owner of USB4 link bring-up, qualification,
NHI arming and recovery, and launcher environment generation. The GLM build and
other model launchers should treat it as a command/API dependency.

## Launcher contract

A two-host coordinator performs this bounded sequence:

1. Run `transport status --peer ... --output ...` on both hosts.
2. Run `transport reconcile` over those two reports.
3. If `arm_allowed=true`, invoke `transport endpoint prepare --apply` on both
   hosts concurrently.
4. Always collect new reports and reconcile again.
5. If `nhi_ready=true` and `lease_available=true`, run `transport env --mode
   nhi`; otherwise invoke exact
   cleanup on both hosts and run `transport env --mode portable`.
6. Launch the model with the generated environment. Grant `CAP_SYS_RAWIO` only
   to a process that will use NHI.
7. When the model exits, check holders and clean both NHI endpoints. The
   portable control link remains up.

The coordinator owns concurrency and remote execution. CiruStrixLink owns every
host mutation and every readiness decision. This separates SSH, systemd, a
future GUI, or another fleet controller from kernel-specific sysfs details.

## Optional paired GLM control

Version 0.3.0 adds an opt-in control plane for the fixed
`GLM5.3-Flash-CIRU-STRIX-IU4` deployment. It is separate from the model-neutral
transport contract above and remains disabled unless both the console and peer
agent receive `--model-control`.

The console must run on one model host with a loopback listener, fixed rank,
fixed USB4 peer, shared token, and model frontend URL. The other model host runs
the USB4-bound agent with the same token, its complementary rank, and the
console host's fixed USB4 address. A remote desktop reaches the loopback console
through an authenticated tunnel; it does not run the privileged helper.

The coordinator uses this transaction:

1. Inspect both fixed system units and reject missing or duplicate ranks.
2. Reject load while a selector-owned main model or portable GLM unit is
   active on either host.
3. Obtain scoped privileged transport reports from both hosts and require a
   qualified, available NHI pair.
4. Configure the selected profile on both stopped ranks.
5. Start rank 0, then rank 1.
6. Poll both units and the model frontend for up to 150 seconds. Success
   requires the frontend to report the exact selected context.
7. If rank 1 or readiness fails, stop rank 1 and rank 0 and report any
   incomplete rollback.

Unload runs in the reverse rank order. The console holds a single-flight lock
for the pair, each agent holds a local single-flight lock, and the bounded
operation context is independent of the browser connection.

On generic Linux, `/usr/local/bin/ciru-strixlink model-node` is the privileged
boundary. Sudoers must enumerate the exact status, transport-status, three
configure, load, and unload commands for the local service user. On the
packaged NixOS path, use the root-owned `glm53-nhi-service-control` wrapper.
Neither path accepts an arbitrary service name or shell command.

## Migration from the GLM helper

The existing `_ops/nhi-persistence` scripts and `vllm-nhi-tp` service encode a
second lifecycle and must not remain enabled alongside CiruStrixLink. Migration
is deliberately staged so an active evaluation is not disturbed:

1. Install the same CiruStrixLink build on both hosts.
2. Disable the old boot-time endpoint service for the next maintenance reboot;
   keep `thunderbolt-net` available.
3. After reboot, verify the portable pair before attempting NHI.
4. Have the GLM launcher call the commands above and remove its direct sysfs,
   module-parameter, device, and holder logic.
5. Retain the old files only as archived evidence, not an executable fallback.

Do not perform this switchover while a benchmark owns `/dev/tbstreamN`.

## Thin adapters

An adapter may choose `--runtime vllm`, `pytorch`, or `generic`, pass paths
between the two hosts, and source the resulting dotenv file. It must not:

- load `thunderbolt_stream` itself;
- assume the service ID or `/dev/tbstream0`;
- create or remove configfs endpoints;
- infer NHI readiness from one host;
- kill a presumed EngineCore or other holder;
- fall back while leaving one endpoint armed.

This boundary lets GLM, another vLLM model, PyTorch jobs, and future runtimes use
the same physical link without inheriting a GLM-specific state machine.
