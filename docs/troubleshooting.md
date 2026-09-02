# Troubleshooting

## `probe` finds no USB4NET interface

1. Confirm both ends use USB4-capable ports and a data-capable USB4 cable.
2. On either Linux peer, run `sudo modprobe thunderbolt-net`.
3. Reconnect the cable and rerun `ciru-strixlink prerequisites`.
4. Inspect `dmesg --level=err,warn` and `journalctl -k` for Thunderbolt or USB4
   authorization, cable, or tunnel errors.

Linux creates one virtual Ethernet interface per connected Thunderbolt/USB4
port. With multiple interfaces, pass the intended one explicitly with
`--interface`.

## Setup refuses an active NetworkManager profile

This is intentional. Inspect it first:

```bash
nmcli connection show ACTIVE_PROFILE_NAME
```

If switching is safe, repeat the dry run with `--take-over`. The old profile is
not deleted. To keep the existing profile, configure it manually to match the
peer and use `doctor` and `test` without running `setup`.

## Route verification fails

Inspect the exact kernel decision:

```bash
ip route get PEER_USB4_ADDRESS
```

The result must name the selected USB4NET interface and its dedicated source
address. Do not benchmark a LAN or VPN result and label it USB4.

## Path MTU fails

Set both peers back to MTU 1500, reactivate their profiles, and repeat
`doctor`. Never change only one end.

```bash
sudo ciru-strixlink setup --role a --mtu 1500 --take-over --apply
sudo ciru-strixlink setup --role b --mtu 1500 --take-over --apply
```

Use the appropriate role on each peer. Raise both to 9000 only after the safe
configuration passes.

## `test` cannot reach the agent

Confirm `serve` is listening on the peer's dedicated USB4 address:

```bash
ss -ltn 'sport = :55321'
```

Then permit TCP 55321 only from the other `/30` address and only on the USB4
interface. Firewall tooling differs by distribution; avoid a wildcard public
allow rule. Stop the agent and remove temporary firewall access after the test.

## Token or protocol mismatch

Use the same token file on both peers or unset `CIRU_STRIXLINK_TOKEN` on both. A
binary from a future incompatible release may intentionally reject the older
wire protocol; use matching versions from the same release archive.

## Throughput is asymmetric

Asymmetry is common enough that `CiruStrixLink` treats each direction separately.
Use the reported faster sender as pipeline stage 0 when stage balance permits.
Do not average the directions for a tensor-parallel readiness decision.

## A previously good link becomes constrained

Rerun `doctor`, then the full `test`. Check, in order:

1. path MTU on both ends;
2. cable and selected ports;
3. kernel route and source address;
4. negotiated adapter speed only as supporting evidence;
5. integrity and reconnect gates;
6. the weaker directional throughput.

Regenerate the launcher environment after any material change. If readiness is
false, fail the model preflight instead of increasing timeouts indefinitely.
