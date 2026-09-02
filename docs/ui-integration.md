# UI integration

The versioned JSON emitted by CiruStrixLink is the supported interface for a
future desktop or web UI. Human console text is not an API.

The top-level `schema_version` changes only when consumers must update. New
optional fields or component IDs may be added without changing it.

## Component fields

| Field | Meaning |
|---|---|
| `id` | Stable machine identifier for the component |
| `label` | User-facing name |
| `required` | Whether this component blocks readiness on this host |
| `status` | `available`, `missing`, `inactive`, `not_detected`, `unsupported`, or `unknown` |
| `summary` | Short user-facing explanation |
| `detected` | Version, path, device, or other detected value |
| `can_auto_fix` | Whether a future UI may safely offer an Apply action |
| `suggested_command` | Command to display; never execute without user authorization |
| `help_url` | Component-specific instructions in the GitHub repository |

## Overall status and exit codes

| Overall status | Exit code | Meaning |
|---|---:|---|
| `ready` | 0 | All currently required capabilities are available |
| `needs_action` | 2 | A required component is missing, inactive, or not detected |
| `unsupported` | 3 | The detected OS or hardware is outside the supported target |

Optional missing tools do not make the host unready. For example,
NetworkManager may be absent while the temporary iproute2 backend remains
usable.

## UI behavior

- Render the status and summary supplied by the binary.
- Open `help_url` for component-specific guidance.
- Show `suggested_command` as copyable text.
- Build an installation preview with `install --json`. Offer an Install button
  only for actions whose `can_apply` value is true, show the entire plan, and
  invoke `install --apply` only after a deliberate user click.
- Rerun prerequisites after every applied action, cable change, reboot, or
  kernel update.
- Do not infer package commands from the OS name in the UI; the repository
  instructions own that policy.

## Transport screens

Use `transport status --peer ... --json` for each host and
`transport reconcile --a ... --b ... --json` for the pair. The pair report is
the authority for transport selection:

- `pair_identity_valid` proves the reports name distinct, reciprocal peer
  addresses; false means the UI selected the wrong reports;
- `portable_ready` enables the normal socket/RCCL link;
- `arm_allowed` enables an explicit **Prepare accelerator** action that the UI
  launches on both peers concurrently;
- `nhi_ready` means both endpoints are qualified, while `lease_available` must
  also be true before NHI runtime environment generation;
- `nhi_in_use` identifies a qualified pair already held by another workload;
- `cleanup_required` disables launch and offers coordinated cleanup;
- holder records identify the exact process blocking cleanup.

After any prepare or cleanup action, fetch both status reports and reconcile
again. Never infer pair success from one successful command. On a failed
two-sided prepare, clean both peers before selecting the portable fallback.
