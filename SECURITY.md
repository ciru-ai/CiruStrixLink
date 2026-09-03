# Security

`ciru-strixlink serve` and `ciru-strixlink agent` expose temporary services on
the selected USB4 address. They do not execute remote commands or access model
files, but an untrusted client can consume CPU and saturate the link.

- Both services bind to the selected USB4 address, never `0.0.0.0`.
- Keep the `/30` link private and scope any firewall allowance to the peer.
- Use `--token-file` or `CIRU_STRIXLINK_TOKEN` when the peer is not physically
  controlled.
- Stop these services after qualification. They are not intended to be
  permanent public services.

The token is a bearer secret used to reject accidental or unauthorized test
clients. It does not encrypt traffic and is not a replacement for a trusted
physical link, IPsec, or another authenticated tunnel.

The browser console is different: `ciru-strixlink ui` listens only on
`127.0.0.1:7749` by default. `--listen 0.0.0.0` is an explicit opt-in for
remote access on a trusted LAN. Its HTTP interface is not authenticated by
`--token-file`; that token authenticates the console's requests to the USB4
peer agent. The console cannot apply setup, install, rollback, or endpoint
changes, but it exposes host diagnostics and can initiate a bounded link
benchmark. On an untrusted LAN, keep the loopback default and reach it through
an authenticated tunnel. Never forward the console, peer-agent, or benchmark
ports to the public internet.

Report suspected vulnerabilities privately to the repository maintainers.
