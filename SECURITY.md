# Security

`ciru-strixlink serve` and `ciru-strixlink agent` expose services on the
selected USB4 address. The benchmark server is temporary. The peer agent may
remain active for the browser console, but model control is disabled unless it
is explicitly started with `--model-control`.

- Both services bind to the selected USB4 address, never `0.0.0.0`.
- Keep the `/30` link private and scope any firewall allowance to the peer.
- Use `--token-file` or `CIRU_STRIXLINK_TOKEN` when the peer is not physically
  controlled.
- Stop the benchmark service after qualification. A persistent peer agent must
  remain restricted to the dedicated point-to-point USB4 interface.

The token is a bearer secret used to reject accidental or unauthorized test
clients. It does not encrypt traffic and is not a replacement for a trusted
physical link, IPsec, or another authenticated tunnel.

The browser console is different: `ciru-strixlink ui` listens only on
`127.0.0.1:7749` by default. `--listen 0.0.0.0` is an explicit opt-in for a
monitoring-only console on a trusted LAN. Its HTTP interface is not
authenticated by `--token-file`; that token authenticates the console's
requests to the USB4 peer agent. On an untrusted LAN, keep the loopback default
and reach it through an authenticated tunnel. Never forward the console,
peer-agent, or benchmark ports to the public internet.

Setup, install, rollback, and ordinary endpoint changes remain preview-only in
the browser. The optional GLM model-control channel is narrower and has
additional boundaries:

- The CLI refuses `--model-control` unless the console is loopback-only, a
  shared peer token is present, both ranks have fixed roles and IPv4 peers, and
  the console has a model frontend URL.
- Every peer-agent request is protected by the shared bearer token, and the
  agent remains bound only to the selected USB4 address.
- The unprivileged console and agent can invoke only a root-owned helper that
  is a regular file and is not group- or world-writable.
- The packaged NixOS helper allowlists fixed probe, transport-status,
  context-selection, start, and stop actions. Generic Linux deployments use
  exact sudoers entries for the built-in `model-node` helper.
- The built-in helper accepts only the fixed GLM service name, the requesting
  service user, three compiled-in context profiles, and a fixed IPv4 peer for
  read-only transport inspection. It has no arbitrary shell, path, unit, or
  argument passthrough.
- Profile changes are rejected while the GLM unit is active. Loading is
  rejected while the selector-owned main model or the portable GLM service is
  active. The helper never enables, disables, masks, or replaces a service.

The browser should still be treated as an operator surface. Keep the console
loopback-only and use an authenticated SSH tunnel from another computer.

Report suspected vulnerabilities privately to the repository maintainers.
