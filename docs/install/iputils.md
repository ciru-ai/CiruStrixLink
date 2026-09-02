# MTU probe utility

CiruStrixLink uses Linux `ping` with don't-fragment probes to verify the full
configured path MTU.

Common package names:

```text
Ubuntu/Debian: iputils-ping
Fedora/RHEL:   iputils
Arch:          iputils
NixOS:         pkgs.iputils
```

Verify with:

```bash
ping -V
ciru-strixlink prerequisites
```

