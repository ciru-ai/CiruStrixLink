# Installation and prerequisite help

Run the read-only capability check first:

```bash
ciru-strixlink prerequisites
```

For machine-readable output:

```bash
ciru-strixlink prerequisites --json
```

The command reports every required and optional component, what was detected,
whether CiruStrixLink can safely fix it, and a link to the relevant document in
this directory. CiruStrixLink does not automatically replace kernels, reboot,
or change distribution configuration.

Distribution guides:

- [Ubuntu and Debian](ubuntu-debian.md)
- [Fedora and RHEL-family systems](fedora-rhel.md)
- [Arch-family systems](arch.md)
- [NixOS](nixos.md)
- [Other package managers](package-managers.md)

