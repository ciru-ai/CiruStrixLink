# USB4 controller, cable, and interface

Connect both Linux peers with a certified data-capable USB4 cable using
USB4-capable ports. Charging-only USB-C cables are insufficient.

Inspect the Linux bus and generated interfaces:

```bash
ls /sys/bus/thunderbolt/devices
ip -brief link
```

After `thunderbolt-net` is loaded, Linux creates an interface such as
`thunderbolt0`. If the driver is available but no interface appears:

1. verify both cable ends and selected ports;
2. disconnect and reconnect the cable;
3. run `sudo modprobe thunderbolt-net` on one peer;
4. inspect `journalctl -k` for USB4 or Thunderbolt errors;
5. rerun `ciru-strixlink prerequisites` on both peers.

CiruStrixLink cannot automatically repair an unsupported cable, disabled
firmware controller, or physically defective port.

