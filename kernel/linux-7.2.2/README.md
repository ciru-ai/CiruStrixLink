# Qualified Linux 7.2.2 USB4STREAM patch series

The direct GPU/NHI path requires the zero-copy and DMA-BUF import ABI in
these **16 patches**. A stock USB4 network driver, or the presence of
`/dev/tbstreamN` alone, is not enough. The portable USB4NET mode needs none
of these patches.

## Origin and scope

Patches 1–15 preserve the zero-copy work from
[jyatesdotdev/strix-rdma commit 06c1836](https://github.com/jyatesdotdev/strix-rdma/tree/06c1836e220dfc21becfc350e62d173861a8ac56),
rebased for the upstream stable **Linux v7.2.2** tree. The older USB4STREAM
backport is intentionally excluded: this kernel already contains the stream
driver. Original patch authorship is preserved.

Ciru's patch 16 clears and flushes stale non-auto-clear MSI-X completion
status before rearming a reused ring, then reads back the enable register.
It fixes the reproduced missing-first-completion failure on the tested pair.

The series adds fixed TX/RX pools, message-boundary completions, explicit RX
reposting, exclusive ownership, DMA synchronization, HopID ownership and
rollback, progress diagnostics, and import of dedicated GPU DMA-BUF pools.
It modifies the kernel data path; the StrixLink binary itself does not install
or load these patches automatically.

All patches are Linux-kernel derivative work under **GPL-2.0**, separate from
the Go utility's Apache-2.0 license. See [COPYING](COPYING) and its referenced
[license](LICENSES/preferred/GPL-2.0) and
[syscall exception](LICENSES/exceptions/Linux-syscall-note).

## Apply to the exact kernel source

From your CiruStrixLink checkout, record the absolute patch path. Use a fresh
Linux source checkout, not a source tree with unrelated local changes:

```bash
PATCH_DIR="$PWD/kernel/linux-7.2.2"
(cd "$PATCH_DIR" && sha256sum -c SHA256SUMS)
git clone --depth 1 --branch v7.2.2 \
  https://github.com/gregkh/linux.git linux-7.2.2-ciru
cd linux-7.2.2-ciru
git am "$PATCH_DIR"/*.patch
git diff --check HEAD~16 HEAD
```

Build and install that tree using your distribution's kernel packaging
workflow and a configuration suitable for your hardware. Required features
include `CONFIG_USB4`, `CONFIG_USB4_NET`, `CONFIG_CONFIGFS_FS`,
`CONFIG_USB4_CONFIGFS` and `CONFIG_USB4_STREAM`. Keep the previous boot entry
available and install the same qualified kernel on both hosts. A kernel
rebuild and reboot are separate from installing the StrixLink binary.

For an existing NixOS configuration **already pinned to Linux 7.2.2**, this
fragment adds the preserved series without choosing a different kernel:

```nix
{ config, lib, ... }:
let
  patchDir = /absolute/path/to/CiruStrixLink/kernel/linux-7.2.2;
  patchNames = builtins.filter (name: lib.hasSuffix ".patch" name)
    (builtins.attrNames (builtins.readDir patchDir));
in {
  assertions = [{
    assertion = config.boot.kernelPackages.kernel.version == "7.2.2";
    message = "This CiruStrixLink patch series targets Linux 7.2.2.";
  }];
  boot.kernelPatches = map (name: {
    inherit name;
    patch = patchDir + "/${name}";
  }) patchNames;
  boot.kernelModules = [ "thunderbolt_net" ];
}
```

The fragment assumes the pinned kernel configuration enables USB4STREAM and
configfs. It does not arm a stream endpoint at boot. Apply it through your
normal NixOS configuration workflow, reboot into the new generation, and
verify the next section on both hosts.

## After reboot

```bash
uname -r
modinfo thunderbolt_net
modinfo thunderbolt_stream
ciru-strixlink prerequisites
ciru-strixlink probe
```

Configure and verify USB4NET first. Then use the
[two-host NHI procedure](../../docs/getting-started.md#6-enable-the-direct-gpunhi-path)
to inspect both hosts, reconcile, prepare concurrently and verify the fresh
pair. The stream service must not race USB4NET for HopID 8. The validated
stream profile uses HopID 9/9, ring size 4095 and throttling 8192 ns.

DMA-BUF import requires `CAP_SYS_RAWIO` on the runtime process. The GLM
package's scoped system-service procedure supplies that boundary. The
[HIP adapter](../../docs/architecture.md) and a compatible model runtime are
also required; these patches alone do not turn stock vLLM into an NHI runtime.

## Validation

The August 30 qualification on Ciru/Sozo passed 32/32 fresh lifecycles,
1,024 verified frames per direction, and 65,536-frame / 256-MiB verified soaks
per direction with clean teardown. GPU DMA-BUF activation and M8 all-reduce
gates are documented in the [performance report](../../docs/performance.md).

For this documentation publication, all 16 files were recovered unchanged,
checksum-recorded, and applied in order to an affected-file checkout of the
official v7.2.2 source. This checks patch applicability. It is not a new full
kernel build or hardware qualification. The tested complete deployment was
NixOS on the two Strix Halo hosts; distribution-specific packaging and other
kernel versions require their own validation.
