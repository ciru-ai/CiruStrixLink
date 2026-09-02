# ethtool

`ethtool` is optional. CiruStrixLink uses it for driver and nominal link-speed
inventory, but readiness and throughput decisions come from end-to-end tests.

Common package names:

```text
Ubuntu/Debian/Fedora/Arch: ethtool
NixOS:                     pkgs.ethtool
```

An unavailable or misleading nominal speed does not block the built-in
transport qualification.

