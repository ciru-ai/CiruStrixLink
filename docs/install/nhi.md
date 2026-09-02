# Optional NHI acceleration

NHI is an optional acceleration mode over Linux USB4STREAM. It is not required
for the portable USB4NET socket/RCCL baseline.

Inspect capability and current lifecycle state without changing the host:

```bash
ciru-strixlink transport status --peer PEER_USB4_ADDRESS
```

The validated production profile currently uses:

- Linux 7.2.2 with the `thunderbolt_stream` driver;
- ring size 4095;
- interrupt throttling 8192 ns;
- HopID 9 in each direction after the network tunnel has retained HopID 8;
- a dynamically discovered service whose key is `stream`;
- `/dev/tbstreamN` (commonly `/dev/tbstream0`);
- `CAP_SYS_RAWIO` granted only to the runtime importing the DMA-BUF.

These are detected and reported, not blindly assumed on another host.

## Required order

1. Load and configure `thunderbolt-net` first.
2. Require `thunderbolt0` carrier, the correct peer route, and peer
   reachability.
3. Only then load `thunderbolt_stream` and discover its dynamic service.
4. Arm both endpoints as one coordinated transaction.
5. Require ring/throttle agreement, HopID 9/9, and a character device on both
   peers before a model may select NHI.
6. On any partial failure, stop exact device holders, remove both endpoint
   configurations, and fall back to portable mode.

Starting a persistent stream endpoint before USB4NET has initialized can race
the network tunnel's HopID allocation and take down the control link. Do not
enable a generic boot-time endpoint service that bypasses this gate.

