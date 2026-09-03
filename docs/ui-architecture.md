# CiruStrixLink UI architecture (v0.2)

The UI is part of the single binary. `ciru-strixlink ui` starts a local console
server and prints the addresses to open in a browser. `ciru-strixlink agent`
starts a small read-only peer agent bound to the USB4 address so the console on
the other host can collect its reports without SSH.

Source of truth remains the CLI's versioned JSON. The UI backend calls the same
internal packages the commands use; it never re-implements kernel, network, or
lifecycle logic.

## Modes

```text
ciru-strixlink ui [--listen 127.0.0.1] [--port 7749] [--peer ADDRESS]
                  [--agent-port 7748] [--token-file PATH]
                  [--report-a REPORT.json --report-b REPORT.json]
ciru-strixlink agent [--interface auto] [--port 7748] [--token-file PATH]
```

- **ui** (console): binds `--listen:--port` (default `127.0.0.1:7749`). On
  startup it prints the loopback URL. An operator may explicitly pass
  `--listen 0.0.0.0` on a trusted LAN; in that mode the console also prints
  each LAN IPv4 and the USB4 link address when the portable link is configured.
- **agent**: Linux only, binds exclusively to the local USB4 address (never
  LAN/Wi-Fi). Serves read-only host reports and time-boxed benchmark listener
  coordination. Token auth via `--token-file` or `CIRU_STRIXLINK_TOKEN`
  (reuses the serve/test token mechanism). If no token is set, print the same
  style of warning `serve` prints.
- **report-file pair**: with `--report-a`/`--report-b` the console reconciles
  two transport reports from disk instead of live collection (support review of
  reports sent by a peer, and UI preview on unsupported platforms). Live local
  collection still runs so the local host panel stays honest.

## HTTP API

All responses are JSON. Errors are
`{"error": "concise message", "detail": "verbatim command/tool output"}` with a
suitable 4xx/5xx status. Collection endpoints never fail wholesale: a missing
capability lands in `errors` and the rest of the payload is still served.

### Console endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/health` | `version`, `schema_version`, `mode`, `hostname`, `os`, `supported`, `started_at`, `peer` config, `pair_source` (`live` or `files`) |
| GET | `/api/host/local` | Composite host payload (below), freshly collected |
| GET | `/api/host/peer` | Composite host payload proxied from the peer agent, or `{"state":"unreachable"|"no_peer"|"no_agent","detail":...}` |
| POST | `/api/refresh` | Re-collect local (+peer when configured), re-reconcile, return `{local, peer, pair}` |
| GET | `/api/pair` | Reconciled pair report (transport.Reconcile) or `{"state":"unavailable","reason":...}` |
| GET | `/api/install/plan?include_optional=&self=` | install.Plan preview |
| POST | `/api/setup/plan` | Body `{role,subnet,mtu,backend,profile,take_over}` -> link.SetupPlan dry run |
| POST | `/api/rollback/plan` | Body `{profile,restore}` -> rollback plan dry run |
| POST | `/api/doctor` | Body `{peer}` -> `{interface,local_ip,route,path_mtu_passed,path_mtu_detail}` |
| POST | `/api/endpoint/plan` | Body `{action:"prepare"|"cleanup",peer,name,ring,throttling_ns,adopt}` -> `{local: EndpointPlan, peer: EndpointPlan|null}` (peer plan via agent when reachable) |
| POST | `/api/bench` | Body `{duration_s,streams,rtt_samples,port}` -> full test report (orchestrates peer agent listener, then local bench.Run) |
| POST | `/api/env` | Body `{mode:"auto"|"portable"|"nhi",runtime:"generic"|"pytorch"|"vllm"}` -> `{environment: {...}, dotenv: "..."}` from the current reconciled pair |
| GET | `/api/activity` | In-memory list of UI-initiated actions `{time,action,host,status,summary,detail}` |
| GET | `/` | Embedded SPA (static/) |

### Agent endpoints (USB4-bound, token required when set)

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/agent/host` | Same composite host payload as console `/api/host/local` |
| POST | `/api/agent/endpoint/plan` | EndpointPlan dry run (read-only) |
| POST | `/api/agent/serve` | Body `{port,seconds}` -> starts the bench listener bound to the USB4 address for at most `seconds` (hard cap 120), returns when listening; the listener exits afterwards |

The agent never applies anything. Mutations stay CLI-only (`sudo ... --apply`)
in v0.2; the UI renders exact reviewed commands for the operator to run.

### Composite host payload

```jsonc
{
  "collected_at": "RFC3339",
  "collection_ms": 42,
  "host": { "hostname": "", "os_name": "", "os_version": "", "kernel": "", "architecture": "", "strix_halo_likely": false, "supported": true },
  "privileged": false,          // false when NHI state could not be fully inspected (needs_privilege)
  "prerequisites": { /* prereq.Report */ },
  "probe":         { /* link.Probe */ },
  "transport":     { /* transport.Report */ },
  "errors":        { "prerequisites": "", "probe": "", "transport": "" }
}
```

`supported` is false on non-Linux or non-Strix platforms; every view must keep
rendering with explicit unsupported/unknown states.

## Pair decision authority

The console reconciles exactly like the CLI: `transport.Reconcile(a, b)` over
the two freshest transport reports. `pair_identity_valid=false` disables every
apply-type preview and labels the pair as non-reciprocal. After any
UI-initiated action the console re-collects both hosts before rendering
success.

## Frontend information architecture

Four destinations per `docs/ui-ux-agent-brief.md`: Pair (default, dual-host
link rail + one safe next action), Setup (prerequisites, install preview,
portable setup preview, rollback preview), Test (quick diagnostics + benchmark
with directional results and quality class), Runtime (transport selection +
generated environment with copy/save). Global shell: brand + tool version,
pair identity, per-host refresh timestamps, privilege badge, Refresh both
hosts, activity log drawer, raw JSON download.
