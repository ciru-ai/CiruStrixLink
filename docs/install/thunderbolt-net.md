# USB4NET kernel driver

Linux USB4 networking uses the in-tree `thunderbolt-net` driver. It may be
built into the kernel or supplied as a loadable module.

Inspect and load it with:

```bash
modinfo thunderbolt_net
sudo modprobe thunderbolt-net
```

If `modinfo` cannot find it, the running kernel does not expose the module
through the normal module tree. Follow the guide for your distribution before
changing the kernel:

- [Ubuntu and Debian](ubuntu-debian.md)
- [Fedora and RHEL](fedora-rhel.md)
- [Arch](arch.md)
- [NixOS](nixos.md)

Do not download an arbitrary out-of-tree kernel module. Kernel modules must
match the running kernel, and a package change may require a reboot.

