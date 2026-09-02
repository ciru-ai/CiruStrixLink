# Fedora and RHEL-family systems

Install the ordinary user-space prerequisites:

```bash
sudo dnf install iproute iputils ethtool NetworkManager kmod
```

Enable NetworkManager only if it is the intended network manager on the host:

```bash
systemctl is-active NetworkManager
```

If `thunderbolt-net` is missing, use a distribution-supported kernel and module
package matching `uname -r`. CiruStrixLink intentionally does not select or
replace kernel packages automatically.

