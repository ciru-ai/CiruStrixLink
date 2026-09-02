# NetworkManager

NetworkManager is the preferred persistent backend. CiruStrixLink can still
create a temporary link with iproute2 when NetworkManager is absent.

Verify the CLI and daemon:

```bash
nmcli --version
nmcli general status
```

Common package names are `network-manager` on Ubuntu/Debian and
`NetworkManager` on Fedora/Arch. On NixOS, enable it declaratively as shown in
the [NixOS guide](nixos.md).

If another service owns the interface, do not enable a second network manager
blindly. Use the temporary backend or configure the existing service according
to its own documentation.

