// Dev preview for the CiruStrixLink operator console.
// Starts `ciru-strixlink ui` on the requested host/port. When the live pair
// reports exist under dist/, they are loaded so the rail renders the real
// ciru/sozo link state; otherwise the console runs in plain live mode.
const { spawn } = require("child_process");
const fs = require("fs");
const path = require("path");

const root = path.join(__dirname, "..");
const argv = process.argv.slice(2);
function arg(name, dflt) {
  const i = argv.findIndex((a) => a === "--" + name || a.startsWith("--" + name + "="));
  if (i === -1) return dflt;
  const a = argv[i];
  return a.includes("=") ? a.split("=")[1] : argv[i + 1] || dflt;
}
const port = arg("port", process.env.PORT || "7100");
const host = arg("host", process.env.HOST || "127.0.0.1");

const repA = path.join(root, "dist", "live-ciru.transport.json");
const repB = path.join(root, "dist", "live-sozo.transport.json");
const repArgs = fs.existsSync(repA) && fs.existsSync(repB) ? ["--report-a", repA, "--report-b", repB] : [];

const exe = path.join(root, "dist", process.platform === "win32" ? "ciru-strixlink-preview.exe" : "ciru-strixlink-preview");
let cmd, args;
if (fs.existsSync(exe)) {
  cmd = exe;
  args = ["ui", "--addr", host, "--port", String(port), ...repArgs];
} else {
  cmd = process.platform === "win32" ? "go.exe" : "go";
  args = ["run", "./cmd/ciru-strixlink", "ui", "--addr", host, "--port", String(port), ...repArgs];
}
console.log(`[dev-preview] ${cmd} ${args.join(" ")}`);
const child = spawn(cmd, args, { cwd: root, stdio: "inherit" });
child.on("exit", (code) => process.exit(code ?? 0));
process.on("SIGINT", () => child.kill("SIGINT"));
process.on("SIGTERM", () => child.kill("SIGTERM"));
