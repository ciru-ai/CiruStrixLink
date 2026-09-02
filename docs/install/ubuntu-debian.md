# Ubuntu and Debian

Install the ordinary user-space prerequisites:

```bash
sudo apt update
sudo apt install iproute2 iputils-ping ethtool network-manager kmod
```

Then rerun:

```bash
ciru-strixlink prerequisites
```

On Ubuntu, a driver absent from the running generic kernel may be supplied by
the matching `linux-modules-extra-$(uname -r)` package. Confirm that the exact
package exists before installing it:

```bash
apt-cache policy "linux-modules-extra-$(uname -r)"
```

Debian and non-generic Ubuntu kernels package modules differently. Do not
assume the Ubuntu package name applies. Prefer the distribution-supported
kernel package and reboot only when its package manager says a new kernel must
be activated.

