# Changelog

## 0.3.1 — truthful runtime state and 256K DFlash tuning

### Fixed

- Replaced hardcoded DFlash and prefix-cache labels on the Launch page with
  settings reported independently by both ranks. Missing values now remain
  explicitly unknown, and conflicting values are shown as a rank mismatch.
- The Overview model card now derives its DFlash label from the managed
  launcher setting when no manual display override is supplied.
- Distinguished TP2 from PP1 in the runtime recipe and exposed the live Fast
  USB4 state separately from speculative decoding.

### Validated

- Selected DFlash2 k=5 for the 256K single-request profile. The exact
  65,680-token recovery prompt sustained 15.12 generated tokens/s, versus 9.38
  target-only and 12.16 at k=3.
- DFlash2 k=5 passed HumanEval 0–9 at 10/10 pass@1 and 26.10 weighted generated
  tokens/s, slightly above the prior k=7 production gate.
- Disabled the unbounded external prefix-cache disk tier in the production
  recipe pending a quota and eviction implementation.

## 0.3.0 — paired launch and operator UX

### Added

- Added a production Launch page as the third console tab for the fixed
  **GLM5.3 Flash CIRU STRIX IU4** TP2 deployment.
- Added paired profile selection for 64K, 128K, and 256K contexts, with the
  same profile written to both ranks before startup.
- Added paired lifecycle orchestration: rank 0 starts before rank 1; rank 1
  stops before rank 0; failed second-rank startup and readiness checks trigger
  a best-effort two-rank rollback.
- Added model-frontend readiness verification. A load succeeds only when the
  OpenAI-compatible frontend reports the selected context window.
- Added per-host unified system RAM, available RAM, configured KV allocation,
  rank, profile, unit state, and PID telemetry.
- Added a token-protected peer-agent control channel and single-flight guards
  for local and paired actions.
- Added the root-only `model-node` helper for generic Linux systems. It accepts
  only the fixed GLM unit, three packaged profiles, load/unload, and read-only
  Fast Link inspection with a fixed IPv4 peer.
- Added inline, copyable permission instructions for packaged NixOS and
  generic Linux installations.

### Changed

- Replaced the old Launch Settings/export tab with an actual paired Launch
  workflow and moved it from fourth to third position.
- Redesigned Connection Setup around an ordered two-machine workflow with
  clearer ownership, readiness, review, and recovery states.
- Overview, Diagnostics, and Launch now prefer the same scoped privileged
  transport reports when the explicitly enabled model-control channel is
  available.
- Clarified that the memory meters show host-wide unified system RAM and that
  the KV figure is only the configured cache allocation.
- Updated the 256K profile to the validated 262,272-token, 8 GiB KV allocation
  per rank. It remains a single-request experimental profile.
- Model-control startup now requires complementary fixed ranks, fixed USB4
  IPv4 peers, a shared token, a model frontend URL, and a loopback console.
- Paired operations use a bounded background context so closing or refreshing
  the browser cannot strand a half-completed action.

### Safety

- Loading is refused while `qwen-main.service` or the portable GLM user unit
  is active. CiruStrixLink never masks, disables, replaces, or enables those
  services.
- Context changes remain blocked while the fixed NHI model unit is running.
- Browser control remains loopback-only; the peer agent remains bound to its
  dedicated USB4 address; all peer requests require the shared token.
- Privileged helpers must be root-owned, regular files, and not writable by
  group or other users. Generic Linux deployment uses exact sudoers commands.

### Validation

- Passed the complete Go test suite, `go vet`, JavaScript syntax validation,
  whitespace checks, and a static Linux amd64 release build.
- Verified live, read-only reporting against a two-rank 256K deployment:
  both ranks loaded, model frontend connected at 262,272 tokens, and the NHI
  pair reported `in_use` with qualified HopID 9/9. No model lifecycle action
  was performed during the 0.3.0 release validation.

## 0.2.0 — release candidate

- Added the local browser console and USB4-bound peer agent.
- Added versioned prerequisite inventory and explicit allowlisted installation
  plans for supported Linux distributions.
- Added portable USB4NET setup, rollback, route/MTU/reconnect/integrity checks,
  and bidirectional link qualification.
- Added two-sided portable/NHI transport reports, reconciliation, endpoint
  lifecycle control, and launcher environment generation.
- Added the production integration guide for
  **GLM5.3 Flash CIRU STRIX IU4**.
- Added public Linux release installation instructions.

## 0.1.0

- Initial command-line USB4NET setup, diagnosis, and benchmark workflow.
