# Other Linux distributions

CiruStrixLink needs these capabilities, regardless of package names:

- the running kernel's in-tree `thunderbolt-net` driver;
- `ip` from iproute2;
- Linux `ping` with don't-fragment support;
- root or an equivalent reviewed privilege mechanism;
- optionally NetworkManager and ethtool.

Use the distribution's official package search and kernel documentation. Do
not substitute similarly named third-party modules or networking scripts.
After installing each capability, rerun `ciru-strixlink prerequisites`.

