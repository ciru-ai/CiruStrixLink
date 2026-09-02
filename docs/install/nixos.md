# NixOS

NixOS configuration should remain declarative. A typical host configuration is:

```nix
{ pkgs, ... }:
{
  boot.kernelModules = [ "thunderbolt_net" ];
  networking.networkmanager.enable = true;
  environment.systemPackages = with pkgs; [
    ethtool
    iproute2
    iputils
  ];
}
```

Apply through the host's normal reviewed NixOS workflow, then reboot only if a
kernel change requires it. CiruStrixLink will not edit `configuration.nix` or
run `nixos-rebuild` automatically.

After activation:

```bash
ciru-strixlink prerequisites
```

