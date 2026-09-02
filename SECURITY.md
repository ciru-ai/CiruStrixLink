# Security

`ciru-strixlink serve` is a temporary network benchmark agent. It does not execute
remote commands or access model files, but an untrusted client can consume CPU
and saturate the link.

- The agent always binds to the selected USB4 address, never `0.0.0.0`.
- Keep the `/30` link private and scope any firewall allowance to the peer.
- Use `--token-file` or `CIRU_STRIXLINK_TOKEN` when the peer is not physically
  controlled.
- Stop the agent after qualification. It is not intended to be a permanent
  public service.

The token is a bearer secret used to reject accidental or unauthorized test
clients. It does not encrypt traffic and is not a replacement for a trusted
physical link, IPsec, or another authenticated tunnel.

Report suspected vulnerabilities privately to the repository maintainers.
