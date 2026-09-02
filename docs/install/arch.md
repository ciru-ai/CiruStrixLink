# Arch-family systems

Install the ordinary user-space prerequisites:

```bash
sudo pacman -S --needed iproute2 iputils ethtool networkmanager kmod
```

Enable NetworkManager only when it is the selected network manager for the
host. Arch kernels normally ship modules with the matching kernel package; if
`modinfo thunderbolt_net` fails, verify that the running kernel and installed
module tree are the same version before changing packages.

