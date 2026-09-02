# Administrative privileges

Reading prerequisites and running most diagnostics does not require root.
Loading a module and changing persistent network configuration does.

CiruStrixLink accepts either:

- a root shell; or
- `sudo` for explicit `--apply` commands.

The tool does not request or store passwords. Review every dry-run plan before
rerunning it with `sudo` and `--apply`.

