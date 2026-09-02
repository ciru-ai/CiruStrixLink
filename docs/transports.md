# Transport lifecycle

CiruStrixLink owns one lifecycle with two modes:

- **portable** uses `thunderbolt-net`, ordinary IP sockets, RCCL, Gloo, or a
  runtime's own TCP protocol;
- **NHI** optionally adds `thunderbolt_stream` and a DMA-BUF endpoint for a
  runtime adapter.

The portable mode works with any model. NHI is model-neutral at the link layer,
but a runtime must know how to import the device. The endpoint is an exclusive
pair resource: one active process per device is the safe public contract.

## Non-negotiable ordering

1. Initialize `thunderbolt-net` and configure the point-to-point interface.
2. Require carrier, the exact peer route, source address, and peer
   reachability. The network service must retain HopID 8.
3. Inspect both hosts and reconcile their reports.
4. Only when the pair result is `arm_allowed`, start `endpoint prepare` on both
   hosts concurrently.
5. Discover exactly one service whose `key` is `stream`; service IDs are
   dynamic and must not be hardcoded.
6. Require ring 4095, throttling 8192 ns, HopID 9/9, and a character device on
   both hosts. `/dev/tbstream0` is common, but the detected index owns the path.
7. Reconcile fresh reports. Only `nhi_ready=true` plus
   `lease_available=true` permits a new launcher to select NHI and grant its
   process `CAP_SYS_RAWIO`.

The values above describe the currently validated Linux 7.2.2 production
profile. Public hosts are capability-detected: the utility reports an
unavailable accelerator rather than pretending an older or differently built
kernel supports it.

Starting a persistent stream endpoint before the network gate can consume the
network tunnel's allocation. The observed failure was
`failed to allocate Rx HopID 8, got 9`; the control link disappeared and an
orphaned EngineCore holding the stream device prevented recovery without a
reboot. A boot service must therefore establish USB4NET first and must not arm
NHI independently.

## Pair transaction

The two-host launcher or future UI is the coordinator. It must use the JSON API
below rather than copy configfs/sysfs logic:

```text
status(A) + status(B)
          |
          v
       reconcile ---- portable not ready ---> stop
          |
          +---- NHI unavailable ------------> generate portable env
          |
          +---- arm_allowed
                    |
                    v
             prepare(A) || prepare(B)
                    |
                    v
          status(A) + status(B) -> reconcile
                    |
          +---------+----------+
          |                    |
       NHI ready          any partial/failure
          |                    |
    generate NHI env      cleanup(A) || cleanup(B)
                               |
                         generate portable env
```

The local prepare operation is transactional: it locks lifecycle state,
rechecks the network gate before and after module load, records the exact
endpoint, and rolls back its partial local work on error. Pair coordination is
still mandatory because one process cannot make two machines atomic.

After prepare, fresh reports—not command success alone—decide the mode. If only
one endpoint exists, if either endpoint is not 9/9, or if parameters differ,
the reconciler returns a partial state. The coordinator cleans both sides. It
must not leave the successful half armed while silently falling back.

NHI uses an exclusive pair lease. A reconciled pair can be technically
qualified (`nhi_ready=true`) while `nhi_in_use=true`; a new launcher must also
require `lease_available=true`. Portable sockets can serve multiple processes
subject to the runtime's normal port and bandwidth management.

## Exact cleanup and holders

`transport endpoint cleanup` previews the exact config path, device, and
holders. Apply refuses when a process has the device open. The operator or
launcher must first stop that exact workload, then rerun cleanup on both peers.
CiruStrixLink does not guess at process names and does not kill processes.

State created by the utility lives under `/run/ciru-strixlink-nhi`. An endpoint
created by an older helper is not owned automatically. `--adopt` is the explicit
escape hatch after the operator has reviewed the detected endpoint.

## Supported API boundary

- `prerequisites --json`: host dependencies and user-facing help URLs;
- `install --json`: explicit, allowlisted install actions;
- `transport status --json`: read-only local mode and holder state;
- `transport reconcile --a ... --b ... --json`: authoritative pair decision;
- `transport endpoint prepare|cleanup`: dry-run local lifecycle transaction,
  with `--apply` as explicit authorization;
- `transport env`: launcher environment selected from the reconciled pair.

Human console output is for operators. Launchers and UIs consume the versioned
JSON fields and call these commands; they do not recreate lifecycle rules.

## Portable fallback

The portable link remains available to vLLM, PyTorch, llama.cpp integrations,
custom model servers, and future models as long as they can communicate over an
IP socket. The generated environment pins RCCL-compatible transports and Gloo
to the exact USB4 interface. A framework-specific overlay may add variables but
does not own USB4 bring-up, NHI arming, cleanup, or pair reconciliation.
