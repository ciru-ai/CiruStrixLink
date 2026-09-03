# CiruStrixLink UI/UX agent brief

- Status: current v0.2 product requirements
- Audience: UI/UX designer and frontend implementation agent
- Source of truth: CiruStrixLink JSON and command results, not duplicated UI logic

## Product objective

Design an operator-facing Linux utility that helps someone connect, qualify,
accelerate, use, and recover a pair of AMD Strix Halo systems over USB4.

The UI must answer four questions immediately:

1. Are both hosts connected and is the portable USB4 network safe to use?
2. Is anything missing, and can CiruStrixLink install it safely?
3. Is NHI acceleration unavailable, available, ready, in use, or partially
   configured?
4. What is the single safe next action?

The link is model-neutral. Do not present this as a GLM-only or vLLM-only tool.
Portable mode works for models and runtimes using ordinary IP networking. NHI
is an optional accelerated link layer with runtime-specific adapters.

## Operator and experience direction

The primary operator is technically capable but should not need to understand
Linux configfs, service IDs, HopID allocation, capabilities, or device holders
to make a safe decision. They may be setting up a new pair, recovering after a
reboot, or preparing a model launch.

The experience should feel like a calm hardware control surface: precise,
compact, stateful, and conservative. It should not feel like a generic SaaS
dashboard or a terminal pasted into a window.

### Domain vocabulary

- two physical hosts;
- USB4 cable and ports;
- portable control link;
- transport path and carrier;
- HopID allocation;
- paired accelerator endpoints;
- exclusive NHI lease;
- device holder;
- qualification and fallback;
- explicit operator authorization.

### Color world

- graphite chassis;
- black USB4 cable;
- cool aluminum;
- dim diagnostic white;
- link-active cyan;
- qualified green;
- amber intervention warning;
- restrained red for destructive or unsafe states.

Color must communicate status or action. It is not decoration, and status must
never depend on color alone.

### Signature interaction

Use a persistent **dual-host link rail** as the product's signature. Host A and
Host B sit at opposite ends. The center shows the physical USB4 connection and
two ordered lanes:

- Portable / USB4NET — HopID 8, always established first.
- NHI / USB4STREAM — HopID 9/9 only when both endpoints qualify.

The rail should make one-sided or mismatched state obvious. For example, 10/10
on one side and 9/9 on the other must visually break the NHI lane and lead to
the cleanup action. Do not reduce the pair to two unrelated host cards.

### Defaults to reject

- Generic grid of equal metric cards → use the link rail as the focal element.
- Traffic-light dots without explanation → pair every status with a plain
  label and one-line consequence.
- Permanent left navigation with many empty sections → use a shallow product
  shell and progressive disclosure around the current lifecycle.

## Recommended information architecture

Use four primary destinations:

1. **Pair** — current two-host state and the safe next action.
2. **Setup** — prerequisites, installation, and portable network configuration.
3. **Test** — diagnostics, qualification, and benchmark results.
4. **Runtime** — transport selection and environment export.

Place version, documentation, refresh, raw report download, and advanced
details in a utility area rather than primary navigation.

The Pair view is the default and must contain the product's main focal point:
the dual-host link rail plus one primary action.

## Global shell requirements

Expose the following globally:

- CiruStrixLink name and tool version;
- selected host pair;
- last refresh time for each host;
- whether each report was read with enough privilege to qualify NHI;
- global **Refresh both hosts** action;
- documentation/help entry;
- expandable activity log for commands initiated by the UI;
- raw JSON report download/copy for support;
- clear disconnected, loading, stale, unsupported, and incompatible-schema
  states.

Never silently reuse an old report after a cable change, reboot, kernel update,
setup action, endpoint action, or benchmark. Refresh both hosts and reconcile.

## 1. Pair view

### Host endpoints

Show both hosts with:

- hostname;
- operating system and version;
- kernel version;
- architecture;
- detected Strix Halo status;
- USB4 interface name;
- local USB4 address and reciprocal peer address;
- carrier state;
- MTU;
- detected USB4 link speed/lanes when available;
- privilege/inspection completeness;
- last report timestamp.

### Pair identity

Expose `pair_identity_valid` prominently. If false:

- label the state **Reports are not a reciprocal pair**;
- disable setup apply, NHI prepare, cleanup, fallback, and runtime export;
- offer **Select the correct hosts** and **Refresh reports**;
- do not suggest cleaning either machine because the reports may be unrelated.

### Portable lane

Expose:

- `portable.status`, `portable.ready`, and `portable.summary` per host;
- carrier check;
- exactly one dynamically detected `network` service;
- route to the peer and interface match;
- peer reachability;
- expected network allocation: HopID 8;
- pair-level `portable_ready`.

Translate the state into one of:

- **Not configured**;
- **Checking**;
- **Needs attention**;
- **Ready**;
- **Disconnected**.

Portable readiness is the gate for every NHI action.

### NHI lane

Expose per host:

- `thunderbolt_stream` unavailable, available, loaded, or unknown;
- whether root access is needed to complete inspection;
- dynamic stream service ID;
- ring size;
- throttling value;
- input and output HopIDs;
- detected `/dev/tbstreamN` path;
- whether the production profile matches;
- holder scan completeness;
- exact holders: PID, command, file descriptor, and `CAP_SYS_RAWIO` state.

Expose at pair level:

- `nhi_status`;
- `nhi_ready` as **Qualified**;
- `nhi_in_use` as **In use**;
- `lease_available` as **Lease available**;
- `arm_allowed` as eligibility for **Prepare accelerator**;
- `cleanup_required`;
- portable fallback availability and reason.

Do not label NHI merely “connected.” Use these distinct concepts:

- **Unavailable** — kernel/driver capability is not present;
- **Needs privileged inspection** — state exists but cannot be qualified;
- **Available** — capability exists and both endpoints are unarmed;
- **Preparing** — concurrent two-host action is running;
- **Qualified** — exact 9/9 endpoint profile is present on both hosts;
- **Ready for launch** — qualified and lease available;
- **In use** — qualified but held by a workload;
- **Partial / unsafe** — one-sided or mismatched state requiring cleanup.

### Safe next action

The Pair view must display exactly one primary recommendation derived from the
pair report, such as:

- Check requirements;
- Configure portable link;
- Test portable link;
- Use portable mode;
- Prepare accelerator;
- Generate runtime environment;
- Accelerator already in use;
- Stop the listed holder outside CiruStrixLink;
- Clean both accelerator endpoints;
- Select the correct host pair.

Secondary and advanced actions must not compete visually with this action.

## 2. Setup view

### Prerequisite inventory

For each host, list every component with:

- label and stable ID;
- required or optional;
- status: `available`, `missing`, `inactive`, `not_detected`, `unsupported`, or
  `unknown`;
- human summary;
- detected version/path/device;
- whether it is safely auto-fixable;
- copyable suggested command;
- **Open instructions** using the supplied `help_url`.

Group components as:

- Platform and hardware;
- Kernel and USB4 drivers;
- Networking tools;
- Persistent configuration;
- Optional diagnostics;
- Optional NHI acceleration.

Expose overall state as **Ready**, **Needs action**, or **Unsupported**.

### Installation preview and apply

The UI must always request an installation plan before offering Apply. Display:

- package manager;
- components already ready;
- each proposed action;
- exact packages or self-install target;
- exact command in an expandable technical detail;
- warnings;
- per-action `can_apply`;
- links for manual-only actions.

Controls:

- Include optional user-space tools;
- Install CiruStrixLink itself;
- installation prefix, hidden under Advanced and defaulted to `/usr/local`;
- **Review installation plan**;
- **Install selected items**, enabled only after an explicit review and user
  confirmation.

Requirements:

- never execute installation automatically on page load or after a scan;
- never imply kernel, firmware, reboot, Thunderbolt authorization, or NixOS
  changes can be performed when the plan marks them manual;
- use an OS-native privilege prompt or provide a copyable `sudo` command;
- do not store a root password in the UI;
- rerun prerequisites after apply.

### Portable network setup

Expose a paired setup form with:

- Host A / Host B role assignment;
- interface, default `auto`;
- private `/30` subnet, default `10.77.77.0/30`;
- calculated host addresses and peer addresses;
- MTU 1500 as the safe default;
- MTU 9000 only after both sides pass at 1500;
- backend: Automatic, NetworkManager, or temporary iproute2;
- NetworkManager profile name;
- **Take over existing profile** as an advanced, explicit choice;
- dry-run plan for both hosts;
- **Apply to both hosts** with separate progress and results.

Make persistence clear:

- NetworkManager: persists across reboot;
- iproute2: temporary until reboot.

Do not let the user accidentally assign the same endpoint role/address to both
hosts.

### Rollback

Expose rollback only for the exact CiruStrixLink-created profile. Show:

- profile to remove;
- optional preserved profile to restore;
- exact preview;
- explicit confirmation;
- separate result per host.

Do not offer broad network reset or deletion of unrelated profiles.

## 3. Test view

### Quick diagnostics

Expose a **Run quick check** action on both hosts. Show:

- chosen USB4 interface and local address;
- reciprocal route and source address;
- route-interface match;
- MTU probe result and detail;
- peer reachability;
- pass/fail summary.

### Link benchmark

The UI coordinates the temporary test agent on one host and the test on the
other. Expose:

- server host selection;
- port, default 55321;
- duration, default 5 seconds;
- parallel streams, default 4;
- RTT samples, default 100;
- optional shared token/authentication;
- **Start test** and **Stop test agent**;
- progress by phase rather than a fake percentage.

Results must show:

- RTT p50, p95, and p99;
- throughput in both directions;
- weaker direction emphasized as the quality gate;
- directional asymmetry ratio;
- faster/bulk-sender recommendation;
- reconnect passes;
- integrity in each direction;
- path MTU result;
- quality class: excellent, good, constrained, or degraded;
- generated heartbeat, timeout, retries, in-flight, and chunk-size policy;
- report export.

Avoid a decorative speedometer. Directional bars, latency distribution, and a
clear pass/fail gate communicate the actual link better.

## 4. Runtime view

Expose:

- runtime: Generic, PyTorch, or vLLM;
- requested mode: Automatic, Portable, or NHI;
- selected mode after pair reconciliation;
- why that mode was selected;
- exact USB4 interface;
- local and peer addresses;
- NHI device and parameters when selected;
- `CAP_SYS_RAWIO` requirement when NHI is selected;
- generated environment variables;
- **Copy environment** and **Save `.env` file**;
- optional handoff to a model launcher later.

Rules:

- Automatic selects NHI only when the pair is qualified and the exclusive
  lease is available;
- explicit NHI is disabled without `nhi_ready=true` and
  `lease_available=true`;
- no environment—portable or NHI—may be generated while
  `cleanup_required=true`;
- Generic is the default and must not expose vLLM-specific variables;
- make it clear that vLLM and PyTorch are runtime overlays, not separate links.

## NHI prepare flow

This is a coordinated two-host transaction, not two independent buttons.

1. Refresh and reconcile both hosts.
2. Require `pair_identity_valid`, `portable_ready`, and `arm_allowed`.
3. Show the complete prepare plan for both hosts.
4. Require explicit authorization because root changes are involved.
5. Start local prepare on both hosts concurrently.
6. Show separate host progress through:
   - network gate recheck;
   - stream module load;
   - portable gate recheck;
   - dynamic service discovery;
   - endpoint negotiation;
   - 9/9 and device verification.
7. Always collect fresh reports from both hosts and reconcile again.
8. Declare success only when `nhi_ready=true` and
   `lease_available=true`.
9. If either side fails, transition to coordinated cleanup. Do not offer
   fallback until both sides are clean.

Never start a persistent stream endpoint at boot or before USB4NET has carrier
and peer reachability.

## NHI cleanup and recovery flow

The cleanup screen or dialog must show:

- why cleanup is required;
- state on both hosts;
- exact config paths and device paths;
- discovered holders;
- whether holder scanning was complete;
- the ordered cleanup plan;
- which endpoints are CiruStrixLink-owned;
- whether legacy endpoint adoption is required.

Rules:

- if a holder exists, disable Apply and tell the user which exact workload must
  be stopped;
- CiruStrixLink must not expose a **Kill process** action;
- if the holder scan is incomplete, require a privileged rescan;
- cleanup runs on both hosts and reports separate results;
- refresh and reconcile after cleanup;
- portable fallback becomes selectable only when no partial endpoint remains;
- expose legacy `--adopt` only under Advanced with an explicit warning that the
  endpoint was created outside CiruStrixLink.

## Action and permission matrix

| UI action | Read-only | Explicit confirmation | Privilege | Pair coordination |
|---|---:|---:|---:|---:|
| Refresh prerequisites | yes | no | no | recommended |
| Preview install | yes | no | no | no |
| Apply install | no | yes | root | per host |
| Probe hardware | yes | no | no | no |
| Preview network setup | yes | no | no | both plans |
| Apply network setup | no | yes | root | both hosts |
| Quick diagnostics | yes | no | no | both hosts |
| Benchmark | temporary listener | yes | no | yes |
| Inspect transport | yes | no | root for complete NHI details | yes |
| Preview NHI prepare | yes | no | root preferred | both plans |
| Apply NHI prepare | no | yes | root | concurrent |
| Preview cleanup | yes | no | root for complete holder scan | both plans |
| Apply cleanup | no | yes | root | both hosts |
| Generate runtime environment | yes | no | no | reconciled report |
| Roll back network profile | no | yes | root | selected hosts |

## Critical state matrix

| State | Primary message | Primary action | Disabled actions |
|---|---|---|---|
| No pair selected | Select two Strix Halo hosts | Select hosts | setup, test, runtime |
| Report loading | Checking both hosts | none | all mutations |
| Wrong/nonreciprocal pair | These reports are not the same link | Select hosts | cleanup and all apply actions |
| Missing requirements | One or both hosts need setup | Review requirements | test, NHI, runtime |
| Portable disconnected | USB4 control link is down | Reconnect and refresh | NHI and runtime |
| Portable ready, NHI unavailable | Portable link is ready | Use portable mode | NHI prepare |
| Portable ready, NHI available | Accelerator can be prepared | Prepare accelerator | NHI runtime export |
| NHI qualified and free | Accelerator is ready | Generate runtime environment | prepare |
| NHI in use | Accelerator is held by a workload | View holder | new NHI launch, cleanup |
| Partial NHI pair | Accelerator endpoints do not match | Clean both endpoints | every runtime export |
| Holder blocks cleanup | Stop the listed workload first | Refresh after stopping it | cleanup apply |
| Unsupported host | This platform is not supported | Open instructions | all apply actions |
| Schema mismatch | UI and CiruStrixLink versions differ | Update component | apply actions |

## Command/API mapping

The UI backend must call CiruStrixLink; it must not recreate kernel or network
state machines.

| Product capability | Command/API |
|---|---|
| Requirements | `ciru-strixlink prerequisites --json` |
| Install preview | `ciru-strixlink install --json ...` |
| Install apply | reviewed install command plus `--apply` |
| Hardware inventory | `ciru-strixlink probe --json` |
| Network setup preview/apply | `ciru-strixlink setup ... --json [--apply]` |
| Network rollback | `ciru-strixlink rollback ... --json [--apply]` |
| Quick diagnostics | `ciru-strixlink doctor --peer ... --json` |
| Test listener | `ciru-strixlink serve ...` |
| Benchmark | `ciru-strixlink test ... --json` |
| Local transport report | `ciru-strixlink transport status --peer ... --json` |
| Pair decision | `ciru-strixlink transport reconcile --a ... --b ... --json` |
| NHI prepare/cleanup plan | `ciru-strixlink transport endpoint prepare\|cleanup ... --json` |
| NHI prepare/cleanup apply | reviewed endpoint command plus `--apply` |
| Runtime environment | `ciru-strixlink transport env ... --json` |

Versioned JSON is the API. Human console output is only for display in an
advanced activity log.

### Current backend dependency

The CLI owns local host mutations and pair decisions, but it does not provide a
remote management daemon. The UI/backend must arrange authenticated execution
on each selected host, launch paired actions concurrently, move the two JSON
reports to the reconciler, and retain command results for display.

The transport coordinator may use SSH, a local agent, system services, or a
future RPC layer. That engineering choice must not change the UI's lifecycle or
duplicate sysfs/configfs operations outside CiruStrixLink.

## Status presentation rules

- Pair every icon/color with a text label.
- Show the supplied summary before raw technical values.
- Keep expected and detected values together for failed checks.
- Use **Unknown—privileged inspection required**, never red **Failed**, when a
  root-readable value could not be inspected.
- Treat timestamps as operational data and flag stale reports.
- Keep host results side by side during paired actions.
- Never show pair success because only one host command returned successfully.
- Preserve command errors verbatim in expandable technical details, but lead
  with a concise user-facing explanation.

## Confirmation requirements

Every mutation follows preview → review → explicit apply.

Confirmation dialogs must name:

- both affected hosts;
- exact action;
- whether root privileges are required;
- whether the portable link remains available;
- what will be rolled back automatically;
- what the user may need to do manually.

Require typed confirmation only for:

- adopting a legacy endpoint;
- taking over an existing network profile;
- replacing an existing CiruStrixLink binary with `--force`.

Normal allowlisted package installation and owned-endpoint cleanup need a clear
confirm button, not punitive text entry.

## Accessibility and interaction requirements

- Full keyboard navigation and visible focus states.
- Minimum 44×44 px hit areas.
- No color-only status communication.
- Screen-reader announcements for action start, host-by-host completion,
  failure, and final pair reconciliation.
- Tabular numerals for IP addresses, HopIDs, speeds, latency, and process IDs.
- Reduced-motion support.
- Do not use a continuously animated cable or pulsing status indicator.
- Loading states must identify which host is still responding.
- Destructive and blocking warnings must remain understandable at 200% zoom.

## Visual system recommendation

- Density: compact operator workbench, using a 4 px spacing base.
- Depth: restrained borders and subtle surface shifts; avoid large shadows.
- Canvas: near-graphite neutral.
- Primary text: cool diagnostic white.
- Accent: link-active cyan, reserved for selected controls and active path.
- Success: desaturated green for qualified pair state.
- Warning: amber for manual action, unknown privilege, or constrained state.
- Danger: red only for unsafe partial state and destructive confirmation.
- Typography: a readable technical grotesk for interface copy and a distinct
  monospaced face for addresses, paths, commands, IDs, and environment output.
- Hierarchy: the link rail is largest; safe next action is second; raw checks
  and commands are progressively disclosed.

Do not style every status as a bordered card. Use spatial grouping and the
central rail to communicate system structure.

## Out of scope for the current UI

- Installing or replacing a Linux kernel;
- changing firmware or BIOS settings;
- rebooting a host automatically;
- authorizing unknown Thunderbolt devices automatically;
- imperatively editing NixOS configuration;
- killing model or EngineCore processes;
- automatically adopting endpoints created by another tool;
- starting NHI endpoints at boot;
- hiding a half-pair by immediately selecting portable mode;
- model download, model configuration, or model-serving controls;
- multi-pair fleet management beyond selecting one active pair.

## MVP acceptance checklist

- [ ] Both hosts are always visible during link decisions and paired actions.
- [ ] The dual-host link rail shows portable HopID 8 before NHI HopID 9/9.
- [ ] The UI shows one safe primary action for every critical state.
- [ ] Prerequisites include exact instructions and safe-install eligibility.
- [ ] Installation never runs without preview and confirmation.
- [ ] Portable setup cannot assign duplicate host roles or addresses.
- [ ] NHI prepare begins only after a fresh reciprocal portable-ready pair.
- [ ] NHI success requires fresh post-action reports from both hosts.
- [ ] Qualified, lease available, and in use are distinct UI states.
- [ ] Partial NHI state blocks every runtime export until both sides are clean.
- [ ] Holder detection names the exact process and never offers to kill it.
- [ ] Unknown privileged data is not rendered as passed or failed.
- [ ] Runtime export supports Generic, PyTorch, and vLLM.
- [ ] All advanced values and command output remain available for support.
- [ ] Keyboard, screen-reader, zoom, and reduced-motion behavior are complete.

## Reference documents

- [UI integration contract](ui-integration.md)
- [Transport lifecycle](transports.md)
- [Production integration](production-integration.md)
- [Installation guides](install/index.md)
- [Troubleshooting](troubleshooting.md)
