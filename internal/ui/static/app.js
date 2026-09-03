"use strict";
/* CiruStrixLink operator console */

/* ---------------- utilities ---------------- */

const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
const esc = (v) => String(v ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const orDash = (v) => (v === undefined || v === null || v === "" ? "—" : esc(v));

function timeAgo(iso) {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "—";
  const s = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (s < 5) return "now";
  if (s < 60) return s + "s ago";
  const m = Math.floor(s / 60);
  if (m < 60) return m + "m ago";
  const h = Math.floor(m / 60);
  if (h < 24) return h + "h " + (m % 60) + "m ago";
  return Math.floor(h / 24) + "d ago";
}
function fmtClock(iso) {
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return "—";
  return t.toLocaleTimeString([], { hour12: false });
}
async function copyText(t, btn) {
  try {
    await navigator.clipboard.writeText(t);
  } catch {
    const ta = document.createElement("textarea");
    ta.value = t; document.body.appendChild(ta); ta.select();
    document.execCommand("copy"); ta.remove();
  }
  if (btn) { const old = btn.textContent; btn.textContent = "copied"; setTimeout(() => (btn.textContent = old), 1200); }
}
function download(name, text, type = "application/json") {
  const b = new Blob([text], { type });
  const a = document.createElement("a");
  a.href = URL.createObjectURL(b); a.download = name; a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 4000);
}
function announce(msg) { $("#live").textContent = msg; }

async function api(path, opts = {}) {
  const r = await fetch(path, opts.body !== undefined
    ? { method: opts.method || "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(opts.body) }
    : { method: opts.method || "GET" });
  let data = null;
  try { data = await r.json(); } catch { /* empty */ }
  if (!r.ok) {
    const e = new Error((data && data.error) || ("HTTP " + r.status));
    e.detail = data && data.detail;
    e.status = r.status;
    throw e;
  }
  return data;
}

/* ---------------- state ---------------- */

const S = {
  health: null,
  local: null,          // console host composite
  peer: null,           // peer composite or {state, detail}
  pairEnv: null,        // {state, reason?, pair?, a?, b?, a_kind?, b_kind?}
  model: null,
  modelBusy: false,
  historyMetric: "tg",
  activity: [],
  view: "pair",
  busy: false,
  setup: { installPlan: null, installOpts: { optional: false, self: false }, setupPlan: null, rollbackPlan: null,
           form: { role: "a", subnet: "10.77.77.0/30", mtu: 1500, backend: "auto", profile: "ciru-strixlink-usb4", take_over: false },
           rb: { profile: "ciru-strixlink-usb4", restore: "" } },
  ep: null,             // endpoint plan response
  test: { doctor: null, doctorBusy: false, benchBusy: false, bench: null, benchErr: null, benchStarted: 0,
          form: { port: 55321, duration_s: 5, streams: 4, rtt_samples: 100 } },
  rt: { mode: "auto", runtime: "generic", env: null, err: null },
};

/* ---------------- boot ---------------- */

async function boot() {
  const hm = location.hash.match(/^#\/(\w+)(\?(.*))?$/);
  if (hm && ["pair", "setup", "test", "runtime", "diagnostics"].includes(hm[1])) S.view = hm[1];
  const hparams = new URLSearchParams(hm && hm[3] ? hm[3] : "");
  try {
    S.health = await api("/api/health");
  } catch (e) {
    $("#main").innerHTML = `<div class="empty"><b>Console API unreachable.</b><br>${esc(e.message)}</div>`;
    return;
  }
  renderShell();
  await Promise.all([refreshAll(true), refreshModel()]);
  if (hparams.get("plan")) loadEndpointPlan(hparams.get("plan"));
  if (hparams.get("env")) {
    const [m, r] = hparams.get("env").split(":");
    if (m) S.rt.mode = m;
    if (r) S.rt.runtime = r;
    S.view = "runtime";
    renderView();
    genEnv();
  }
  setInterval(tickAges, 5000);
  setInterval(() => { if (!document.hidden) refreshModel(); }, 5000);
  setInterval(() => { if (!S.busy) refreshAll(true); }, 30000);
}

async function refreshAll(quiet) {
  if (S.busy) return;
  S.busy = true;
  if (!quiet) { $("#loadbar").hidden = false; announce("Refreshing both hosts"); }
  try {
    const r = await api("/api/refresh", { method: "POST" });
    S.local = r.local; S.peer = r.peer; S.pairEnv = r.pair;
  } catch (e) {
    S.pairEnv = { state: "unavailable", reason: e.message };
  } finally {
    S.busy = false;
    $("#loadbar").hidden = true;
  }
  renderShell();
  renderView();
  if (!quiet) {
    const p = pairReport();
    announce(p ? `Pair reconciled: ${p.summary || p.nhi_status}` : "Refresh finished");
  }
}

function tickAges() { renderShellAges(); }

async function refreshModel() {
  if (S.modelBusy) return;
  S.modelBusy = true;
  try {
    S.model = await api("/api/model");
  } catch {
    S.model = { state: "unreachable", note: "Model monitoring is unavailable." };
  } finally {
    S.modelBusy = false;
  }
  renderModelPanel();
}

function renderModelPanel() {
  const panel = $("#overview-model");
  if (!panel) return;
  const focused = document.activeElement && document.activeElement.dataset.historyMetric;
  panel.innerHTML = modelOverviewHtml();
  if (focused) panel.querySelector(`[data-history-metric="${focused}"]`)?.focus({ preventScroll: true });
}

/* ---------------- derived pair state ---------------- */

function pairReport() { return S.pairEnv && S.pairEnv.state === "ok" ? S.pairEnv.pair : null; }
function reportA() { return S.pairEnv && S.pairEnv.a ? S.pairEnv.a : (S.local && S.local.transport) || null; }
function reportB() { return S.pairEnv && S.pairEnv.b ? S.pairEnv.b : (S.peer && S.peer.transport) || null; }

function deriveState() {
  const env = S.pairEnv;
  if (!env || env.state !== "ok") {
    return { key: "checking", tone: env && env.reason ? "warn" : "off",
      title: env && env.reason ? "Can't read both computers" : "Checking your connection…",
      sub: env && env.reason ? "The dashboard couldn't get a fresh status from both computers. Check that both computers and their status collectors are reachable." : "Getting live connection status from both computers.",
      primary: { label: "Check again", act: "refresh", kind: "primary" } };
  }
  const p = env.pair;
  if (!p.pair_identity_valid) {
    return { key: "identity", tone: "bad", title: "These computers don't match as a pair",
      sub: "The two status reports describe different connections. Check which computer is selected on each end before changing any settings.",
      primary: { label: "View diagnostics", act: "goto", view: "diagnostics", kind: "primary" } };
  }
  if (S.health && S.health.pair_source === "live") {
    const prA = S.local && S.local.prerequisites, prB = S.peer && S.peer.prerequisites;
    const bad = (pr) => pr && (pr.overall_status === "needs_action" || pr.overall_status === "unsupported");
    if (bad(prA) || bad(prB)) {
      const who = [bad(prA) ? (prA.system && prA.system.hostname) || "host A" : null, bad(prB) ? (prB.system && prB.system.hostname) || "host B" : null].filter(Boolean).join(" and ");
      return { key: "prereq", tone: "warn", title: "Connection setup needs attention",
        sub: `${who} ${bad(prA) && bad(prB) ? "have" : "has"} a missing or unsupported requirement. Review setup to see what needs fixing.`,
        primary: { label: "Review connection setup", act: "goto", view: "setup", kind: "primary" } };
    }
  }
  if (!p.portable_ready) {
    return { key: "portable-down", tone: "warn", title: "The USB4 connection needs attention",
      sub: "The computers cannot confirm a working connection over the cable. Check the cable and review the network setup.",
      primary: { label: "Review connection setup", act: "goto", view: "setup", kind: "primary" } };
  }
  if ([reportA(), reportB()].some((r) => r && r.nhi && r.nhi.status === "needs_privilege")) {
    return { key: "privilege", tone: "warn", title: "Connected. Fast-mode status is unknown.",
      sub: "The dashboard needs read-only administrator access to inspect fast USB4. This is a monitoring limitation, not a confirmed link failure.",
      primary: { label: "View inspection details", act: "goto", view: "diagnostics", kind: "primary" } };
  }
  if (p.cleanup_required || (p.fallback && p.fallback.cleanup_required) || p.nhi_status === "partial") {
    return { key: "partial", tone: "bad", title: "Fast USB4 needs attention",
      sub: "The fast-link settings don't match across the two computers. Review the recovery steps before launching another workload; nothing will be changed automatically.",
      primary: { label: "Review recovery steps", act: "endpoint-plan", arg: "cleanup", kind: "primary" } };
  }
  if (p.nhi_in_use) {
    return { key: "in-use", tone: "live", title: "Fast USB4 is in use",
      sub: "Both computers are connected, and a workload has the fast connection open. No connection changes are needed.",
      primary: { label: "See what's using it", act: "expand-endpoints", kind: "primary" } };
  }
  if (p.nhi_status === "ready" && p.lease_available) {
    return { key: "ready", tone: "ok", title: "Fast USB4 is ready",
      sub: "Both computers are connected. The fast connection is set up and available for your model to use.",
      primary: { label: "View launch settings", act: "goto", view: "runtime", kind: "primary" } };
  }
  if (p.arm_allowed) {
    return { key: "arm", tone: "warn", title: "Connected. Fast USB4 needs setup.",
      sub: "The cable connection works. Both computers support fast mode, but it hasn't been prepared yet.",
      primary: { label: "Review fast-mode setup", act: "endpoint-plan", arg: "prepare", kind: "primary" } };
  }
  return { key: "portable", tone: "ok", title: "Standard USB4 is ready",
    sub: "The normal cable connection works. Fast mode is not confirmed available; your model can use the standard connection instead.",
    primary: { label: "View launch settings", act: "goto", view: "runtime", kind: "primary" } };
}

/* ---------------- shell ---------------- */

function renderShell() {
  if (S.health) {
    $("#brand-ver").textContent = "v" + S.health.version;
  }
  const chips = [];
  const mk = (label, payload, kind) => {
    if (!payload) return "";
    if (payload.state) {
      return `<span class="hchip"><span class="pip" style="background:var(--ink-2)"></span>${label} · ${esc(payload.state.replace(/_/g, " "))}</span>`;
    }
    const h = payload.host || {};
    const age = payload.collected_at ? timeAgo(payload.collected_at) : "—";
    const stale = payload.collected_at && (Date.now() - new Date(payload.collected_at).getTime()) > 300000;
    const priv = payload.privileged ? `<span class="priv root">root</span>` : `<span class="priv user">user</span>`;
    const sup = h.supported === false ? `<span class="priv user">unsupported</span>` : "";
    const src = kind === "file" ? `<span class="priv">file</span>` : "";
    return `<span class="hchip" title="collected ${esc(payload.collected_at || "")}"><span class="pip" style="background:${payload.privileged ? "var(--green)" : "var(--amber)"}"></span>${label} <b class="mono">${esc(h.hostname || "?")}</b> ${priv}${sup}${src} <span class="${stale ? "priv user" : "dim"}" data-age>${age}${stale ? " · stale" : ""}</span></span>`;
  };
  const kindA = (S.pairEnv && S.pairEnv.a_kind) || "local";
  const kindB = (S.pairEnv && S.pairEnv.b_kind) || "agent";
  if (S.pairEnv && S.pairEnv.state === "ok" && S.health && S.health.pair_source === "files") {
    chips.push(mk("A", { host: { hostname: S.pairEnv.a.hostname }, collected_at: S.pairEnv.a.generated_at, privileged: S.pairEnv.a.nhi && S.pairEnv.a.nhi.status !== "needs_privilege" }, "file"));
    chips.push(mk("B", { host: { hostname: S.pairEnv.b.hostname }, collected_at: S.pairEnv.b.generated_at, privileged: S.pairEnv.b.nhi && S.pairEnv.b.nhi.status !== "needs_privilege" }, "file"));
  } else {
    chips.push(mk("A", S.local, kindA));
    chips.push(mk("B", S.peer, kindB));
  }
  $("#host-chips").innerHTML = chips.join("");
  renderShellAges();

  const ps = $("#pair-state");
  const p = pairReport();
  ps.innerHTML = p ? `<b>${esc(p.host_a)}</b><span aria-hidden="true"> ↔ </span><b>${esc(p.host_b)}</b>` : "Your two-computer link";
}

function renderShellAges() {
  $$("#host-chips [data-age]").forEach(() => {});
  // cheap re-render of ages only
  renderShellChipsAges();
}
function renderShellChipsAges() {
  const els = $$("#host-chips .hchip");
  const srcs = (S.pairEnv && S.pairEnv.state === "ok" && S.health && S.health.pair_source === "files")
    ? [S.pairEnv.a.generated_at, S.pairEnv.b.generated_at]
    : [S.local && S.local.collected_at, S.peer && S.peer.collected_at];
  els.forEach((el, i) => {
    const a = el.querySelector("[data-age]");
    if (a && srcs[i]) a.textContent = timeAgo(srcs[i]);
  });
}

/* ---------------- rail ---------------- */

const TONE_HEX = { ok: "#3fb38b", live: "#22d3ee", warn: "#f5b73f", bad: "#e5484d", off: "#39424e" };

function laneSVG(tone, broken, gid) {
  const c = TONE_HEX[tone] || TONE_HEX.off;
  let core;
  if (broken) {
    core = `
      <rect x="12" y="11.8" width="430" height="2.6" rx="1.3" fill="${c}"/>
      <rect x="558" y="11.8" width="430" height="2.6" rx="1.3" fill="${c}"/>
      <path d="M442 13 462 8.5 482 17.5 502 9 522 16.5 542 11 558 13" fill="none" stroke="${c}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>`;
  } else if (tone === "off") {
    core = `<line x1="14" y1="13" x2="986" y2="13" stroke="${c}" stroke-width="2.2" stroke-dasharray="6 7" stroke-linecap="round"/>`;
  } else if (tone === "warn") {
    core = `<line x1="14" y1="13" x2="986" y2="13" stroke="${c}" stroke-width="2.6" stroke-dasharray="14 6" stroke-linecap="round"/>`;
  } else {
    core = `<rect x="12" y="11.4" width="976" height="3.2" rx="1.6" fill="${c}"/>`;
  }
  return `<svg class="lane-track" viewBox="0 0 1000 26" preserveAspectRatio="none" aria-hidden="true">
    <defs><linearGradient id="${gid}" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#3d4653"/><stop offset=".4" stop-color="#232b35"/><stop offset="1" stop-color="#141a21"/>
    </linearGradient></defs>
    <rect x="0" y="6" width="1000" height="14" rx="7" fill="url(#${gid})"/>
    <rect x="8" y="7.6" width="984" height="1.6" rx="0.8" fill="rgba(255,255,255,0.08)"/>
    <rect x="0" y="4.5" width="10" height="17" rx="3" fill="#424c59"/>
    <rect x="990" y="4.5" width="10" height="17" rx="3" fill="#424c59"/>
    ${core}</svg>`;
}

function lane(model) {
  // model: {tone, broken, gid, chipL, chipR, chipClsL, chipClsR, name, word, note}
  return `<div class="lane">
    <div class="lane-head">
      <span class="lane-name">${esc(model.name)}</span>
      <span class="st ${model.tone}">${esc(model.word)}</span>
    </div>
    <div class="lane-mid">
      ${laneSVG(model.tone, model.broken, model.gid)}
      <span class="hop l ${model.chipClsL || ""}">${model.chipL}</span>
      <span class="hop r ${model.chipClsR || ""}">${model.chipR}</span>
    </div>
    <div class="lane-note">${model.note ? esc(model.note) : ""}</div>
  </div>`;
}

function portableLane(a, b, p) {
  const ra = a && a.portable, rb = b && b.portable;
  const ready = p && p.portable_ready;
  let tone = "off", word = "Unknown", note = "";
  if (ready) { tone = "live"; word = "Ready"; }
  else if (ra || rb) {
    const st = (ra && ra.status) || (rb && rb.status) || "";
    if (st === "disconnected") { tone = "bad"; word = "Disconnected"; }
    else if (st === "not_configured") { tone = "off"; word = "Not configured"; }
    else { tone = "warn"; word = "Needs attention"; }
    note = (ra && !ra.ready && ra.summary) || (rb && !rb.ready && rb.summary) || "";
  }
  return lane({ tone, gid: "sheath-p", chipL: "HopID <b>8</b>", chipR: "HopID <b>8</b>",
    chipClsL: ready ? "live" : tone, chipClsR: ready ? "live" : tone,
    name: "Portable · USB4NET", word, note });
}

function nhiLane(a, b, p) {
  const st = p ? p.nhi_status : "";
  const epA = a && a.endpoints && a.endpoints[0];
  const epB = b && b.endpoints && b.endpoints[0];
  const chipFor = (ep, rep) => {
    if (ep) return { t: `HopID <b>${ep.in_hopid}·${ep.out_hopid}</b>`, cls: ep.production_profile_match ? "ok" : "bad" };
    const priv = rep && rep.nhi && rep.nhi.status === "needs_privilege";
    if (priv) return { t: `HopID <b>?</b>`, cls: "" };
    if (st === "ready" || st === "in_use" || st === "available") return { t: `HopID <b>9·9</b>`, cls: st === "available" ? "off" : "ok" };
    return { t: `HopID <b>—</b>`, cls: "off" };
  };
  const cA = chipFor(epA, a), cB = chipFor(epB, b);
  if (st === "ready") return lane({ tone: "ok", gid: "sheath-n", chipL: cA.t, chipR: cB.t, chipClsL: cA.cls, chipClsR: cB.cls, name: "NHI · USB4STREAM", word: "Qualified", note: p.lease_available ? "exclusive lease available" : "qualified; checking lease" });
  if (st === "in_use") return lane({ tone: "ok", gid: "sheath-n", chipL: cA.t, chipR: cB.t, chipClsL: cA.cls, chipClsR: cB.cls, name: "NHI · USB4STREAM", word: "In use", note: "qualified; lease held by a workload" });
  if (st === "partial") {
    return lane({ tone: "bad", broken: true, gid: "sheath-n", chipL: cA.t, chipR: cB.t, chipClsL: cA.cls, chipClsR: cB.cls,
      name: "NHI · USB4STREAM", word: "Partial — mismatch", note: "expected HopID 9·9 on both endpoints; cleanup required" });
  }
  if (st === "unavailable") return lane({ tone: "off", gid: "sheath-n", chipL: "HopID <b>—</b>", chipR: "HopID <b>—</b>", chipClsL: "off", chipClsR: "off", name: "NHI · USB4STREAM", word: "Unavailable", note: "capability not present on one or both hosts" });
  if (p && p.arm_allowed) return lane({ tone: "warn", gid: "sheath-n", chipL: "HopID <b>9·9</b>", chipR: "HopID <b>9·9</b>", chipClsL: "off", chipClsR: "off", name: "NHI · USB4STREAM", word: "Available — unarmed", note: "both endpoints idle; prepare is allowed" });
  const priv = (a && a.nhi && a.nhi.status === "needs_privilege") || (b && b.nhi && b.nhi.status === "needs_privilege");
  if (priv) return lane({ tone: "warn", gid: "sheath-n", chipL: "HopID <b>?</b>", chipR: "HopID <b>?</b>", name: "NHI · USB4STREAM", word: "Privileged inspection", note: "root access required to qualify this lane" });
  return lane({ tone: "off", gid: "sheath-n", chipL: cA.t, chipR: cB.t, chipClsL: cA.cls, chipClsR: cB.cls, name: "NHI · USB4STREAM", word: st ? st : "Unknown", note: "" });
}

/* ---------------- Pair view ---------------- */

function plate(side, rep, payload, kind) {
  const host = (payload && payload.host) || {};
  const name = rep ? rep.hostname : host.hostname || (side === "a" ? "host A" : "host B");
  const kernel = rep ? rep.kernel : host.kernel;
  const osline = host.os_name ? `${host.os_name} ${host.os_version || ""}`.trim() : "";
  const iface = rep ? rep.interface : "";
  const addr = rep ? rep.local_address : "";
  const peer = rep ? rep.peer : "";
  const stale = rep && rep.generated_at ? "" : "";
  const priv = payload ? (payload.privileged ? '<span class="st ok">root inspection</span>' : '<span class="st warn">user — limited</span>') : "";
  const src = kind === "file" ? '<span class="st off">report file</span>' : "";
  const missing = !rep;
  return `<section class="plate" aria-label="Host ${side.toUpperCase()}">
    <div class="plate-top">
      <img class="plate-emblem" src="assets/emblem.png" alt="">
      <div class="plate-id">
        <div class="plate-host">${esc(name)}</div>
        <div class="plate-role">host ${side} ${kind === "file" ? "· report file" : kind === "agent" ? "· via peer agent" : "· this console"}</div>
      </div>
    </div>
    <dl class="plate-meta">
      ${osline ? `<dt>system</dt><dd>${esc(osline)}</dd>` : ""}
      <dt>kernel</dt><dd>${orDash(kernel)}</dd>
      <dt>usb4 iface</dt><dd class="bright">${orDash(iface)}</dd>
      <dt>link addr</dt><dd class="bright">${orDash(addr)}</dd>
      <dt>peer addr</dt><dd>${orDash(peer)}</dd>
      <dt>report</dt><dd>${rep ? esc(timeAgo(rep.generated_at)) + " · " + esc(fmtClock(rep.generated_at)) : "—"}</dd>
    </dl>
    <div class="plate-foot">${missing ? '<span class="st off">no report</span>' : ""}${priv}${src}</div>
  </section>`;
}

function overviewComputer(side, connected) {
  const rep = side === "a" ? reportA() : reportB();
  const pay = payloadFor(side);
  const name = (rep && rep.hostname) || (pay && pay.host && pay.host.hostname) || (side === "a" ? "This computer" : "Other computer");
  return `<section class="overview-computer" aria-label="${esc(name)}">
    <svg class="computer-icon" viewBox="0 0 72 64" fill="none" aria-hidden="true"><rect x="10" y="10" width="52" height="39" rx="7"/><path d="M18 55h36M24 18h24"/><circle cx="49" cy="38" r="2" class="computer-led ${connected ? "connected" : ""}"/></svg>
    <h2>${esc(name)}</h2>
    <p>${rep ? (pay && pay.host && pay.host.strix_halo_likely ? "Strix Halo computer" : "USB4 computer") : "Waiting for status"}</p>
  </section>`;
}

function overviewAction(action) {
  return `<button class="btn btn-${action.kind || "primary"}" data-act="${action.act}"${action.arg ? ` data-arg="${action.arg}"` : ""}${action.view ? ` data-view="${action.view}"` : ""}>${esc(action.label)}<span aria-hidden="true">→</span></button>`;
}

function modelSpeedHistoryHtml(m, monitored) {
  const metric = S.historyMetric;
  const isPP = metric === "pp";
  const series = monitored ? (m.history || []) : [];
  const points = series.filter((p) => Number.isFinite(p[metric]));
  const current = monitored ? (isPP ? m.pp : m.live_tg) : null;
  const value = current ? current.tokens_per_second.toLocaleString(undefined, { maximumFractionDigits: isPP ? 0 : 1 }) : "—";
  const currentNote = current ? isPP ? current.basis : m.metrics?.running > 0 ? current.basis : "Idle · no output in this interval" : monitored ? "Collecting speed samples" : "Model metrics unavailable";
  const end = series.length ? new Date(series[series.length - 1].at).getTime() : Date.now();
  const start = series.length > 1 ? new Date(series[0].at).getTime() : end - 5000;
  const span = Math.max(5000, end - start);
  const max = Math.max(1, ...points.map((p) => p[metric]));
  const step = 10 ** Math.floor(Math.log10(max));
  const ceiling = Math.max(10, Math.ceil(max * 1.1 / step) * step);
  const x = (p) => 4 + Math.max(0, Math.min(1, (new Date(p.at).getTime() - start) / span)) * 992;
  const y = (p) => 8 + (1 - p[metric] / ceiling) * 128;
  const segments = [];
  let segment = [];
  let lastAt = null;
  for (const p of series) {
    const at = new Date(p.at).getTime();
    // Missing output samples are gaps, not zero speed or interpolated uptime.
    // PP points are separate completed-request measurements.
    if (!Number.isFinite(p[metric])) {
      if (!isPP && segment.length) { segments.push(segment); segment = []; }
      continue;
    }
    if (!isPP && lastAt !== null && at - lastAt > 30000 && segment.length) {
      segments.push(segment); segment = [];
    }
    segment.push(p); lastAt = at;
  }
  if (segment.length) segments.push(segment);
  const traces = segments.map((group) => {
    const coords = group.map((p) => `${x(p).toFixed(2)},${y(p).toFixed(2)}`).join(" ");
    const first = group[0], last = group[group.length - 1];
    return `${!isPP && group.length > 1 ? `<polygon class="speed-history-area" points="${x(first)},136 ${coords} ${x(last)},136"/>` : ""}<polyline class="speed-history-line ${isPP ? "per-request" : ""}" points="${coords}" vector-effect="non-scaling-stroke"/>`;
  }).join("");
  const markers = isPP ? points : points.slice(-1);
  const dots = markers.map((p) => `<circle class="speed-history-point" cx="${x(p)}" cy="${y(p)}" r="3" vector-effect="non-scaling-stroke"><title>${esc(fmtClock(p.at))} · ${p[metric].toFixed(1)} tok/s</title></circle>`).join("");
  const duration = span < 60000 ? `${Math.round(span / 1000)}s` : `${Math.round(span / 60000)}m`;
  const empty = !points.length ? `<div class="speed-history-empty">${monitored ? isPP ? "Prompt-fill history appears as requests finish." : "Waiting for the next speed sample." : "Connect the model to see live speed."}</div>` : "";
  return `<section class="speed-history" aria-label="Token speed history">
    <div class="speed-history-head"><div><h2>${isPP ? "Prompt-fill speed" : "Generation speed"}</h2><p class="speed-history-value">${value}<span>tok/s</span></p><p class="model-meta">${esc(currentNote)}</p></div>
      <div class="speed-history-controls" role="group" aria-label="Speed chart metric">${[["tg", "Generation"], ["pp", "Prompt fill"]].map(([key, label]) => `<button type="button" data-act="history-metric" data-history-metric="${key}" aria-pressed="${metric === key}">${label}</button>`).join("")}</div>
    </div>
    <figure class="speed-history-figure"><div class="speed-history-scale" aria-hidden="true"><span>${ceiling.toLocaleString()}</span><span>${(ceiling / 2).toLocaleString()}</span><span>0</span></div>
      <div class="speed-history-plot"><svg viewBox="0 0 1000 144" preserveAspectRatio="none" role="img" aria-label="${isPP ? "Prompt-fill measurements at request completion" : "Output tokens per second in polling intervals"}, ${points.length} measurements over ${duration}, scale 0 to ${ceiling} tokens per second">
        <path class="speed-history-grid" d="M0 8H1000 M0 72H1000 M0 136H1000" vector-effect="non-scaling-stroke"/>${traces}${dots}
      </svg>${empty}</div>
      <figcaption><span>${series.length > 1 ? esc(fmtClock(series[0].at)) : "Collecting"}</span><span>${series.length > 1 ? `${duration} history · ` : ""}${isPP ? "per completed request" : "5s samples"}</span><span>Now</span></figcaption>
    </figure>
  </section>`;
}

function modelOverviewHtml() {
  const m = S.model;
  const p = pairReport();
  const saved = S.health && S.health.pair_source === "files";
  const monitored = !saved && m && m.state === "connected";
  const active = monitored && m.metrics && m.metrics.running;
  const waiting = monitored && m.metrics && m.metrics.waiting;
  const hasActivity = monitored && m.metrics && m.metrics.running !== undefined;
  const name = monitored ? m.name.replace(/-/g, " ") : !m ? "Checking model…" : m.state === "unconfigured" || saved ? "No model connected" : "Model API unreachable";
  const apiHost = monitored ? `API on ${esc(m.api_host)}${m.context_window ? ` · ${Math.floor(m.context_window / 1024)}K context` : ""}` : esc(m && m.note || "Connect a model frontend with --model-url.");
  const activity = hasActivity ? active > 0 ? `${active} running${waiting > 0 ? ` · ${waiting} queued` : ""}` : waiting > 0 ? `${waiting} queued` : "Idle" : monitored ? "API connected" : "";
  const nhi = p && p.nhi_ready && p.pair_identity_valid;
  const link = saved ? "Saved connection" : nhi ? "NHI · USB4STREAM" : p && p.portable_ready ? "Standard · USB4NET" : "Not confirmed";
  const linkNote = saved ? "Not live" : nhi ? p.nhi_in_use ? "Open by a workload" : "Ready · not in use" : p && p.portable_ready ? "Available · fast mode not ready" : "Check connection status";
  const acceptance = monitored ? m.acceptance : null;
  const acceptanceTitle = m && m.speculation ? `${m.speculation} acceptance` : "Draft acceptance";
  const acceptanceBasis = acceptance ? acceptance.basis === "Since engine start" ? acceptance.basis : `${timeAgo(acceptance.measured_at)} · latest drafts` : "Awaiting draft counters";
  const acceptanceHelp = acceptance ? `${acceptance.accepted.toLocaleString()} accepted / ${acceptance.proposed.toLocaleString()} proposed draft tokens. Bonus target tokens are not counted. ${acceptance.basis}.` : "Acceptance is accepted draft tokens divided by proposed draft tokens; no reports does not mean zero acceptance.";
  const pp = monitored ? m.pp : null;
  const rate = (r, digits) => r ? `${r.tokens_per_second.toLocaleString(undefined, { maximumFractionDigits: digits })}<span>tok/s</span>` : `<span class="metric-unavailable">—</span>`;
  const basis = (r) => r ? `${esc(r.basis)}${r.basis.startsWith("Last ") ? ` · ${timeAgo(r.measured_at)}` : ""}` : monitored ? "Awaiting measurement" : "Not available";
  return `<div class="model-overview-grid">
    <section class="model-identity"><h2>Serving model${activity ? `<span class="model-activity ${active > 0 ? "running" : ""}"><i aria-hidden="true"></i>${esc(activity)}</span>` : ""}</h2><p class="model-name" title="${esc(monitored ? m.name : name)}">${esc(name)}</p><p class="model-meta">${apiHost}</p></section>
    <section class="model-link"><h2>Link type</h2><p class="model-link-name">${link}</p><p class="model-meta">${linkNote}</p></section>
    <section class="model-speed" title="${esc(acceptanceHelp)}"><h2>${esc(acceptanceTitle)}</h2><p class="model-rate">${acceptance ? `${acceptance.percent.toFixed(1)}<span>%</span>` : '<span class="metric-unavailable">—</span>'}</p><p class="model-meta">${esc(acceptanceBasis)}</p></section>
    <section class="model-speed"><h2>Prompt fill · PP</h2><p class="model-rate">${rate(pp, 0)}</p><p class="model-meta">${basis(pp)}</p></section>
  </div>${modelSpeedHistoryHtml(m, monitored)}<p class="model-metrics-note">${monitored ? `Live speed uses elapsed wall time. PP excludes cached tokens.${m.tg ? ` Last completed decode: ${m.tg.tokens_per_second.toFixed(1)} tok/s (${esc(m.tg.basis.toLowerCase())}).` : ""} History retains up to 10 minutes while monitored.` : "Read-only model monitoring. Connection readiness is independent of model availability."}</p>`;
}

function vPair() {
  const st = deriveState();
  const p = pairReport();
  const saved = S.health && S.health.pair_source === "files";
  const connected = !!(p && p.pair_identity_valid && p.portable_ready);
  const inUse = !!(connected && p.nhi_in_use);
  const ready = st.key === "ready";
  const nextTitle = inUse ? "No connection changes needed" : ready ? "Ready for your model" : "What to do next";
  const nextNote = inUse ? "Keep this connection as it is. Open Diagnostics if you want to see which process is using it." : ready ? "Use fast USB4 (NHI) in your model launcher. StrixLink provides the settings; it does not start or stop the model." : st.sub;
  return `<div class="overview">
    <div class="overview-heading"><span class="overview-eyebrow">Your two-computer link</span><span class="overview-source">${saved ? "Saved report · not live" : "Live status · updates every 30 seconds"}</span></div>
    <section class="overview-hero tone-${st.tone}" aria-labelledby="connection-title">
      <div class="overview-title"><span class="overview-status-dot" aria-hidden="true"></span><h1 id="connection-title">${saved ? "Saved connection report" : esc(st.title)}</h1></div>
      <p class="overview-description">${saved ? "Open Diagnostics to inspect the saved connection. Current readiness and usage are unknown." : esc(st.sub)}</p>
      <div class="overview-pair">
        ${overviewComputer("a", connected)}
        <div class="overview-cable ${connected ? "connected" : ""}" role="img" aria-label="${connected ? "USB4 cable connection confirmed" : "USB4 connection not confirmed"}">
          <span class="overview-cable-name">USB4 cable</span><div class="overview-cable-line"><i></i><i></i></div><strong>${connected ? (saved ? "Connected when saved" : "Connected") : "Not confirmed"}</strong>
        </div>
        ${overviewComputer("b", connected)}
      </div>
      <div id="overview-model" class="overview-model">${modelOverviewHtml()}</div>
      <div class="overview-nextline">
        <div><h2>${saved ? "Current status" : esc(nextTitle)}</h2><p>${saved ? "Connect the console to both computers to get live status." : esc(nextNote)}</p></div>${overviewAction(saved ? { label: "View saved diagnostics", act: "goto", view: "diagnostics" } : st.primary)}
      </div>
    </section>
    <div class="overview-footer"><p>Read-only monitoring · model metrics refresh every 5 seconds</p><button class="btn btn-ghost" data-act="goto" data-view="diagnostics">Diagnostics <span aria-hidden="true">→</span></button></div>
  </div>`;
}

function vDiagnostics() {
  const env = S.pairEnv;
  const st = deriveState();
  if (!env || env.state !== "ok") {
    return `<div class="view-head"><h1>Diagnostics</h1><span class="sub">connection details for troubleshooting</span></div>
      ${env && env.reason ? `<div class="banner warn"><svg class="bic" viewBox="0 0 16 16"><path d="M8 2 1.8 13.5h12.4L8 2Z" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"/><path d="M8 6.5v3.2" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><circle cx="8" cy="11.6" r=".8" fill="currentColor"/></svg><div><h3>Pair state unavailable</h3><p>${esc(env.reason)}</p></div></div>` : ""}
      ${peerHelp()}
      ${railHtml(null, null, null, st)}
      ${stateBarHtml(st)}`;
  }
  const a = env.a, b = env.b, p = env.pair;
  const banners = [];
  if (!p.pair_identity_valid) {
    banners.push(`<div class="banner bad"><svg class="bic" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="1.4"/><path d="m5.5 5.5 5 5m0-5-5 5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg><div><h3>Reports are not a reciprocal pair</h3><p>${esc(p.summary)}</p></div></div>`);
  }
  if (S.health && S.health.pair_source === "files") {
    banners.push(`<div class="banner info"><svg class="bic" viewBox="0 0 16 16"><path d="M3 2.5h7l3 3V13.5H3V2.5Z" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"/><path d="M10 2.5v3h3" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"/></svg><div><h3>Reviewing report files</h3><p>The rail is rendered from two saved transport reports. Start the console with <span class="mono">--peer</span> and an agent on the other host for live state.</p></div></div>`);
  }
  return `<div class="view-head"><h1>Diagnostics</h1><span class="sub">kernel, permissions, transport, and process details</span></div>
    ${banners.join("")}
    ${railHtml(a, b, p, st)}
    ${stateBarHtml(st)}
    ${endpointPlanHtml()}
    ${pairDetailHtml(a, b, p)}`;
}

function railHtml(a, b, p, st) {
  const payA = payloadFor("a"), payB = payloadFor("b");
  const kindA = (S.pairEnv && S.pairEnv.a_kind) || "local";
  const kindB = (S.pairEnv && S.pairEnv.b_kind) || "agent";
  return `<div class="rail-wrap"><div class="rail">
    ${plate("a", a, payA, kindA)}
    <div class="link-core" role="img" aria-label="USB4 link lanes between host A and host B">
      ${portableLane(a, b, p)}
      ${nhiLane(a, b, p)}
    </div>
    ${plate("b", b, payB, kindB)}
  </div></div>`;
}

function payloadFor(side) {
  if (S.health && S.health.pair_source === "files") {
    const rep = side === "a" ? (S.pairEnv && S.pairEnv.a) : (S.pairEnv && S.pairEnv.b);
    if (!rep) return null;
    return { host: { hostname: rep.hostname, kernel: rep.kernel, supported: true }, privileged: !(rep.nhi && rep.nhi.status === "needs_privilege"), collected_at: rep.generated_at };
  }
  return side === "a" ? S.local : (S.peer && !S.peer.state ? S.peer : null);
}

function stateBarHtml(st) {
  return `<div class="rail-state s-${st.tone}">
    <div class="rs-text">
      <h2 class="rs-title">${esc(st.title)}</h2>
      <p class="rs-sub">${esc(st.sub)}</p>
    </div>
    <div class="rs-actions">
      <button class="btn btn-${st.primary.kind || "primary"}" data-act="${st.primary.act}" ${st.primary.arg ? `data-arg="${st.primary.arg}"` : ""} ${st.primary.view ? `data-view="${st.primary.view}"` : ""}>${esc(st.primary.label)}</button>
      <button class="btn btn-ghost" data-act="download">Raw report</button>
    </div>
  </div>`;
}

function peerHelp() {
  const h = S.health;
  if (!h || h.pair_source === "files") return "";
  if (!h.peer || !h.peer.configured) {
    return `<div class="banner info"><svg class="bic" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="1.4"/><path d="M8 7.2v3.4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><circle cx="8" cy="5.2" r=".9" fill="currentColor"/></svg><div><h3>No peer configured</h3><p>Run the agent on the other Strix Halo host, then restart this console with its USB4 address.</p>
      <div class="cmdline mt8">on host B: ciru-strixlink agent<button class="copy" data-copy="ciru-strixlink agent">copy</button></div>
      <div class="cmdline mt8">here: ciru-strixlink ui --peer PEER_USB4_ADDRESS<button class="copy" data-copy="ciru-strixlink ui --peer PEER_USB4_ADDRESS">copy</button></div>
    </div></div>`;
  }
  return "";
}

/* ---- pair detail: per-host checks, endpoints, lifecycle, cleanup ---- */

const CHECK_LABEL = (s) => ({ passed: "pass", failed: "fail", unknown: "unknown", not_armed: "not armed" }[s] || s);
const CHECK_TONE = (s) => ({ passed: "ok", failed: "bad", unknown: "warn", not_armed: "off" }[s] || "off");

function checkRows(checks) {
  return (checks || []).map((c) => `<div class="check">
    <span class="cid">${esc(c.id)}</span>
    <span class="csum">${esc(c.summary)}</span>
    <span class="cvals">
      <span class="st ${CHECK_TONE(c.status)}" style="grid-column:auto">${CHECK_LABEL(c.status)}</span>
      ${c.detected ? `<span class="det-v ${c.status === "failed" ? "bad" : c.status === "unknown" ? "unk" : ""}">detected&nbsp;${esc(c.detected)}</span>` : ""}
      ${c.expected ? `<span class="exp-v">expected&nbsp;${esc(c.expected)}</span>` : ""}
      ${c.help_url && c.status !== "passed" ? `<a href="${esc(c.help_url)}" target="_blank" rel="noreferrer">instructions ↗</a>` : ""}
    </span>
  </div>`).join("");
}

function endpointRows(eps) {
  if (!eps || !eps.length) return `<div class="empty" style="padding:14px">No armed NHI endpoints.</div>`;
  return eps.map((e) => `<div class="check">
    <span class="cid">${esc(e.name)}</span>
    <span class="csum mono" style="font-size:11.5px">${esc(e.config_path)}</span>
    <span class="cvals">
      <span class="st ${e.production_profile_match ? "ok" : "bad"}">${e.production_profile_match ? "profile 9·9" : "off-profile"}</span>
      <span class="det-v ${e.production_profile_match ? "" : "bad"}">hop ${e.in_hopid}·${e.out_hopid}</span>
      <span class="exp-v">ring ${e.ring_size} · throttle ${e.throttling_ns}ns</span>
      <span class="exp-v">${esc(e.device)}${e.device_present ? "" : " (absent)"}</span>
      ${e.holder_scan_complete ? "" : `<span class="det-v unk">holder scan incomplete</span>`}
    </span>
    ${(e.holders || []).map((h) => `<span class="cvals" style="grid-column:2"><span class="st warn">holder</span><span class="det-v unk">pid ${h.pid}</span><span class="exp-v">${esc(h.command)}</span><span class="exp-v">fd ${esc(h.fd)}</span><span class="exp-v">CAP_SYS_RAWIO ${h.has_cap_sys_rawio ? "yes" : "no"}</span></span>`).join("")}
  </div>`).join("");
}

function lifecycleRows(ls) {
  return `<ol class="steps">${(ls || []).map((s) => `<li><span class="st ${CHECK_TONE(s.status === "passed" ? "passed" : s.status === "blocked" ? "failed" : s.status === "available" || s.status === "eligible" ? "unknown" : "not_armed")}" style="margin-right:8px">${esc(s.status.replace(/_/g, " "))}</span>${esc(s.summary)}</li>`).join("")}</ol>`;
}

function cleanupHtml(cp) {
  if (!cp || !cp.required) return `<div class="empty" style="padding:14px">No cleanup required on this host.</div>`;
  return `<div class="kv"><dt>config paths</dt><dd>${(cp.config_paths || []).map((p) => `<div>${esc(p)}</div>`).join("")}</dd>
    <dt>devices</dt><dd>${(cp.device_paths || []).map((p) => `<div>${esc(p)}</div>`).join("")}</dd>
    <dt>blocked</dt><dd>${cp.blocked_by_holders ? '<span style="color:var(--amber)">yes — holders must be stopped first</span>' : "no"}</dd></div>
    <ol class="steps">${(cp.steps || []).map((s) => `<li>${esc(s)}</li>`).join("")}</ol>`;
}

function hostDetailCol(rep, side) {
  const name = rep ? rep.hostname : side === "a" ? "host A" : "host B";
  if (!rep) return `<div><div class="col-head"><span class="hn">${esc(name)}</span><span class="st off">no report</span></div></div>`;
  const nhiTone = { ready: "ok", available: "live", needs_privilege: "warn", partial: "bad", unavailable: "off" }[rep.nhi && rep.nhi.status] || "off";
  return `<div>
    <div class="col-head"><span class="hn">${esc(name)}</span>
      <span class="st ${rep.portable && rep.portable.ready ? "live" : "warn"}">portable ${rep.portable ? rep.portable.status.replace(/_/g, " ") : "?"}</span>
      <span class="st ${nhiTone}">nhi ${rep.nhi ? rep.nhi.status.replace(/_/g, " ") : "?"}</span>
    </div>
    <details class="det" ${rep.portable && !rep.portable.ready ? "open" : ""}>
      <summary><svg class="caret" viewBox="0 0 16 16"><path d="M6 4l4 4-4 4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg><span class="t">Portable lane checks</span><span class="n">USB4NET gate</span><span class="st ${rep.portable && rep.portable.ready ? "ok" : "warn"}">${rep.portable ? (rep.portable.ready ? "ready" : rep.portable.status.replace(/_/g, " ")) : "unknown"}</span></summary>
      <div class="det-body">${rep.portable ? `<p class="dim" style="margin:0 0 6px">${esc(rep.portable.summary)}</p>${checkRows(rep.portable.checks)}` : "—"}</div>
    </details>
    <details class="det" ${rep.nhi && (rep.nhi.status === "partial" || rep.nhi.status === "needs_privilege") ? "open" : ""}>
      <summary><svg class="caret" viewBox="0 0 16 16"><path d="M6 4l4 4-4 4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg><span class="t">NHI lane checks</span><span class="n">USB4STREAM</span><span class="st ${nhiTone}">${rep.nhi ? rep.nhi.status.replace(/_/g, " ") : "unknown"}</span></summary>
      <div class="det-body">${rep.nhi ? `<p class="dim" style="margin:0 0 6px">${esc(rep.nhi.summary)}</p>${checkRows(rep.nhi.checks)}` : "—"}</div>
    </details>
    <details class="det" ${(rep.endpoints || []).some((e) => !e.production_profile_match) ? "open" : ""} id="endpoints-${side}">
      <summary><svg class="caret" viewBox="0 0 16 16"><path d="M6 4l4 4-4 4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg><span class="t">Endpoints</span><span class="n">${(rep.endpoints || []).length} armed</span></summary>
      <div class="det-body">${endpointRows(rep.endpoints)}</div>
    </details>
    <details class="det">
      <summary><svg class="caret" viewBox="0 0 16 16"><path d="M6 4l4 4-4 4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg><span class="t">Lifecycle ordering</span><span class="n">non-negotiable sequence</span></summary>
      <div class="det-body">${lifecycleRows(rep.lifecycle)}</div>
    </details>
    <details class="det" ${rep.cleanup && rep.cleanup.required ? "open" : ""}>
      <summary><svg class="caret" viewBox="0 0 16 16"><path d="M6 4l4 4-4 4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg><span class="t">Cleanup plan</span><span class="n">${rep.cleanup && rep.cleanup.required ? "required" : "not required"}</span><span class="st ${rep.cleanup && rep.cleanup.required ? "warn" : "off"}">${rep.cleanup && rep.cleanup.required ? "required" : "clean"}</span></summary>
      <div class="det-body">${cleanupHtml(rep.cleanup)}</div>
    </details>
  </div>`;
}

function pairDetailHtml(a, b, p) {
  return `<div class="sec">
    <div class="sec-h"><h2>Per-host detail</h2></div>
    <div class="cols">
      ${hostDetailCol(a, "a")}
      ${hostDetailCol(b, "b")}
    </div>
  </div>`;
}

/* ---------------- endpoint prepare/cleanup plans ---------------- */

function endpointPlanHtml() {
  const ep = S.ep;
  if (!ep) return "";
  if (!ep.res && !ep.error) {
    return `<div class="sec"><div class="sec-h"><h2>NHI ${esc(ep.action)} — coordinated two-host review</h2></div>
      <div class="empty"><b>Building ${esc(ep.action)} plan on both hosts…</b></div></div>`;
  }
  if (ep.error) {
    return `<div class="sec"><div class="sec-h"><h2>${esc(ep.action)} plan</h2></div>
      <div class="banner bad"><div><h3>Plan unavailable</h3><p>${esc(ep.error)}${ep.detail ? `<br><span class="mono" style="font-size:11px">${esc(ep.detail)}</span>` : ""}</p></div></div></div>`;
  }
  const r = ep.res;
  const one = (p, title) => {
    if (!p) return `<div class="plan"><div class="plan-h"><span class="t">${esc(title)}</span><span class="st off">unavailable</span></div>
      <div class="plan-body"><p class="dim" style="margin:6px 0">${esc(r.peer_error || "Peer plan not collected.")}</p></div></div>`;
    const applyCmd = `sudo ciru-strixlink transport endpoint ${p.action} --peer ${p.options.peer || "PEER_USB4_ADDRESS"}${p.options.adopt ? " --adopt" : ""} --apply`;
    return `<div class="plan">
      <div class="plan-h"><span class="t">${esc(title)}</span>
        <span class="st ${p.can_apply ? "ok" : "bad"}">${p.can_apply ? "can apply" : "blocked"}</span>
        <span class="micro dim">${esc(p.action)} · ring ${p.options.ring} · ${p.options.throttling_ns}ns</span>
      </div>
      <div class="plan-body">
        <p style="margin:6px 0 4px">${esc(p.summary)}</p>
        <ol class="steps">${(p.steps || []).map((s) => `<li>${esc(s)}</li>`).join("")}</ol>
        ${(p.blockers || []).map((bl) => `<div class="warnline">${esc(bl)}</div>`).join("")}
        ${p.can_apply ? `<div class="cmdline mt8">${esc(applyCmd)}<button class="copy" data-copy="${esc(applyCmd)}">copy</button></div>
          <p class="hint dim" style="font-size:11px;margin:6px 0 0">Root changes are never executed from the browser. Run the reviewed command on this host; the same transaction must run on both hosts concurrently.</p>` : ""}
      </div>
    </div>`;
  };
  return `<div class="sec" id="endpoint-plan">
    <div class="sec-h"><h2>NHI ${esc(r.local.action)} — coordinated two-host review</h2></div>
    <div class="cols">
      ${one(r.local, (hostName("a")) + " (local)")}
      ${one(r.peer, (hostName("b")) + " (peer)")}
    </div>
    ${r.local.action === "cleanup" ? `<p class="dim mt8" style="font-size:12px">Cleanup never kills a process and refuses a device with a holder. Stop the listed workload outside CiruStrixLink, then refresh and re-review.</p>` : ""}
  </div>`;
}

function hostName(side) {
  const rep = side === "a" ? reportA() : reportB();
  return rep && rep.hostname ? rep.hostname : side === "a" ? "host A" : "host B";
}

async function loadEndpointPlan(action) {
  S.view = "diagnostics";
  const peer = (reportA() && reportA().peer) || (S.local && S.local.transport && S.local.transport.peer) || "";
  S.ep = { action, res: null, error: null };
  renderView();
  try {
    const r = await api("/api/endpoint/plan", { body: { action, peer, name: "ciru-nhi", ring: 4095, throttling_ns: 8192, adopt: false } });
    S.ep = { action, res: r };
    announce(`${action} plan ready for both hosts`);
  } catch (e) {
    S.ep = { action, error: e.message, detail: e.detail };
  }
  renderView();
  setTimeout(() => { const el = $("#endpoint-plan"); if (el) el.scrollIntoView({ behavior: "smooth", block: "start" }); }, 30);
}

/* ---------------- Setup view ---------------- */

const PREREQ_GROUPS = [
  { id: "platform", label: "Platform and hardware", ids: ["linux", "strix_halo"] },
  { id: "kernel", label: "Kernel and USB4 drivers", ids: ["usb4_controller", "thunderbolt_net", "modprobe"] },
  { id: "network", label: "Networking tools", ids: ["usb4net_interface", "iproute2", "ping"] },
  { id: "persist", label: "Persistent configuration", ids: ["networkmanager", "privilege_escalation"] },
  { id: "diag", label: "Optional diagnostics", ids: ["ethtool", "package_manager"] },
  { id: "nhi", label: "Optional NHI acceleration", ids: ["thunderbolt_stream"] },
];
const groupOf = (id) => { const g = PREREQ_GROUPS.find((g) => g.ids.includes(id)); return g ? g.id : "diag"; };

const PR_TONE = (s) => ({ available: "ok", missing: "warn", inactive: "warn", not_detected: "warn", unsupported: "bad", unknown: "off" }[s] || "off");

function prereqCell(rep, cid) {
  if (!rep) return `<td><span class="st off">not collected</span></td>`;
  const c = (rep.components || []).find((x) => x.id === cid);
  if (!c) return `<td><span class="st off">n/a</span></td>`;
  return `<td>
    <span class="st ${PR_TONE(c.status)}">${esc(c.status.replace(/_/g, " "))}</span>
    ${c.detected ? `<div class="mono dim" style="font-size:10.5px;margin-top:4px">${esc(c.detected)}</div>` : ""}
    <div style="font-size:11px;color:var(--ink-1);margin-top:4px;max-width:340px">${esc(c.summary)}</div>
    ${c.suggested_command ? `<div class="cmdline mt8" style="font-size:10.5px">${esc(c.suggested_command)}<button class="copy" data-copy="${esc(c.suggested_command)}">copy</button></div>` : ""}
    ${c.help_url && c.status !== "available" ? `<a href="${esc(c.help_url)}" target="_blank" rel="noreferrer" style="font-size:11px;color:var(--ink-1)">open instructions ↗</a>` : ""}
  </td>`;
}

function prereqCompareHtml() {
  const la = S.local && S.local.prerequisites;
  const lb = S.peer && !S.peer.state && S.peer.prerequisites;
  const ids = [...new Set([...(la ? la.components : []), ...(lb ? lb.components : [])].map((c) => c.id))];
  const nameA = (la && la.system && la.system.hostname) || "host A (console)";
  const nameB = (lb && lb.system && lb.system.hostname) || "host B (agent)";
  const overall = (rep) => rep ? `<span class="st ${rep.overall_status === "ready" ? "ok" : rep.overall_status === "unsupported" ? "bad" : "warn"}">${esc((rep.overall_status || "").replace(/_/g, " "))}</span>` : `<span class="st off">no agent</span>`;
  const groups = PREREQ_GROUPS.map((g) => {
    const rows = g.ids.filter((id) => ids.includes(id));
    if (!rows.length) return "";
    return `<tr class="grp"><td colspan="3">${esc(g.label)}</td></tr>` + rows.map((id) => {
      const c0 = (la && la.components.find((x) => x.id === id)) || (lb && lb.components.find((x) => x.id === id));
      return `<tr><td class="cmp"><b>${esc(c0 ? c0.label : id)}</b><span class="mono dim" style="display:block;font-size:10px">${esc(id)}${c0 && c0.required ? " · required" : " · optional"}</span></td>
        ${prereqCell(la, id)}${prereqCell(lb, id)}</tr>`;
    }).join("");
  }).join("");
  return `<table class="env-table prereq-table" style="width:100%">
    <thead><tr>
      <td class="micro">component</td>
      <td class="micro">${esc(nameA)} ${overall(la)}</td>
      <td class="micro">${esc(nameB)} ${overall(lb)}</td>
    </tr></thead><tbody>${groups}</tbody></table>
    ${!lb ? `<div class="banner info mt16"><div><h3>Peer prerequisites not collected</h3><p>Start the agent on the other host and refresh to compare both sides.</p>
      <div class="cmdline mt8">on host B: ciru-strixlink agent<button class="copy" data-copy="ciru-strixlink agent">copy</button></div></div></div>` : ""}`;
}

function installPlanHtml() {
  const o = S.setup.installOpts;
  const p = S.setup.installPlan;
  const flags = `${o.optional ? " --include-optional" : ""}${o.self ? " --self" : ""}`;
  return `<div class="sec"><div class="sec-h"><h2>Installation</h2></div>
    <div class="row">
      <label class="row" style="gap:6px;font-size:12px;color:var(--ink-1)"><input type="checkbox" data-set="installOpts.optional" ${o.optional ? "checked" : ""}> include optional user-space tools</label>
      <label class="row" style="gap:6px;font-size:12px;color:var(--ink-1)"><input type="checkbox" data-set="installOpts.self" ${o.self ? "checked" : ""}> install CiruStrixLink itself</label>
      <button class="btn" data-act="install-plan">Review installation plan</button>
    </div>
    ${p ? `<div class="plan">
      <div class="plan-h"><span class="t">Installation plan</span>
        <span class="st ${p.can_apply ? "ok" : "warn"}">${p.can_apply ? "safe to apply" : "manual steps included"}</span>
        <span class="micro dim">package manager: ${esc(p.package_manager || "unrecognized")}</span></div>
      <div class="plan-body">
        ${(p.already_ready || []).length ? `<p class="dim" style="margin:6px 0">Already ready: <span class="mono" style="font-size:11px">${p.already_ready.map(esc).join(", ")}</span></p>` : ""}
        ${(p.actions || []).map((a) => `<div class="plan-act">
          <span class="ty">${esc(a.type)}</span>
          <span class="su">${esc(a.summary)} <span class="dim mono" style="font-size:10.5px">${(a.components || []).map(esc).join(", ")}</span></span>
          <span class="st ${a.can_apply ? "ok" : "warn"}">${a.can_apply ? "auto" : "manual"}</span>
          ${a.command ? `<span class="cm"><span class="cmdline">${esc(a.command + " " + (a.args || []).join(" "))}<button class="copy" data-copy="${esc(a.command + " " + (a.args || []).join(" "))}">copy</button></span></span>` : ""}
          ${a.target ? `<span class="cm"><span class="cmdline">${esc(a.source)} → ${esc(a.target)}</span></span>` : ""}
          ${a.help_url ? `<span class="cm"><a href="${esc(a.help_url)}" target="_blank" rel="noreferrer" style="color:var(--ink-1);font-size:11px">manual instructions ↗</a></span>` : ""}
        </div>`).join("")}
        ${(p.warnings || []).map((w) => `<div class="warnline">${esc(w)}</div>`).join("")}
        ${p.can_apply ? `<div class="cmdline mt8">sudo ciru-strixlink install${flags} --apply<button class="copy" data-copy="sudo ciru-strixlink install${flags} --apply">copy</button></div>
        <p class="hint dim" style="font-size:11px;margin:6px 0 0">Nothing is installed from the browser. Review the plan, then run the command on the host itself.</p>` : `<p class="dim" style="font-size:12px;margin:8px 0 0">No allowlisted automatic actions for this selection — follow the linked manual instructions.</p>`}
      </div></div>` : ""}
  </div>`;
}

function setupPlanHtml() {
  const f = S.setup.form;
  const p = S.setup.setupPlan;
  return `<div class="sec"><div class="sec-h"><h2>Portable network setup</h2></div>
    <p class="dim" style="margin:0 0 12px;font-size:12px">Point-to-point /30 on the USB4 interface. MTU 1500 is the safe default; 9000 only after both sides pass at 1500. NetworkManager persists across reboot; iproute2 ends at reboot.</p>
    <div class="form">
      <div class="fld"><label>This host is</label>
        <div class="seg" role="group" aria-label="Host role">
          <button data-set="form.role" data-val="a" aria-pressed="${f.role === "a"}">A · .1</button>
          <button data-set="form.role" data-val="b" aria-pressed="${f.role === "b"}">B · .2</button>
        </div><span class="hint">the peer takes the opposite role</span></div>
      <div class="fld"><label>interface</label><input class="inp" value="auto" disabled><span class="hint">auto-detected thunderbolt interface</span></div>
      <div class="fld"><label>subnet</label><input class="inp" data-inp="form.subnet" value="${esc(f.subnet)}"></div>
      <div class="fld"><label>MTU</label>
        <div class="seg" role="group" aria-label="MTU">
          <button data-set="form.mtu" data-val="1500" aria-pressed="${f.mtu === 1500}">1500</button>
          <button data-set="form.mtu" data-val="9000" aria-pressed="${f.mtu === 9000}">9000</button>
        </div></div>
      <div class="fld"><label>backend</label>
        <select class="sel" data-inp="form.backend">
          <option value="auto" ${f.backend === "auto" ? "selected" : ""}>Automatic</option>
          <option value="networkmanager" ${f.backend === "networkmanager" ? "selected" : ""}>NetworkManager (persistent)</option>
          <option value="iproute2" ${f.backend === "iproute2" ? "selected" : ""}>iproute2 (temporary)</option>
        </select></div>
      <div class="fld"><label>profile name</label><input class="inp" data-inp="form.profile" value="${esc(f.profile)}"></div>
      <div class="fld"><label>&nbsp;</label>
        <label class="row" style="gap:6px;font-size:11.5px;color:var(--ink-1);min-height:36px"><input type="checkbox" data-set="form.take_over" ${f.take_over ? "checked" : ""}> take over existing profile</label></div>
    </div>
    <div class="row mt16">
      <button class="btn btn-primary" data-act="setup-plan">Preview plan</button>
    </div>
    ${p ? `<div class="plan"><div class="plan-h"><span class="t">Setup plan — this host</span>
        <span class="st live">${esc(p.backend)}</span>
        <span class="micro dim">${esc(p.interface)} · ${esc(p.address)} ↔ peer ${esc(p.peer)} · mtu ${p.mtu}</span></div>
      <div class="plan-body">
        ${(p.commands || []).map((c) => `<div class="cmdline" style="margin-top:6px">${esc(c.name + " " + (c.args || []).join(" "))}<button class="copy" data-copy="${esc(c.name + " " + (c.args || []).join(" "))}">copy</button></div>`).join("")}
        ${(p.warnings || []).map((w) => `<div class="warnline">${esc(w)}</div>`).join("")}
        <div class="cmdline mt8">sudo ciru-strixlink setup --role ${f.role} --subnet ${esc(f.subnet)} --mtu ${f.mtu} --backend ${esc(f.backend)}${f.take_over ? " --take-over" : ""} --apply<button class="copy" data-copy="sudo ciru-strixlink setup --role ${f.role} --subnet ${esc(f.subnet)} --mtu ${f.mtu} --backend ${esc(f.backend)}${f.take_over ? " --take-over" : ""} --apply">copy</button></div>
        <div class="cmdline mt8">on the peer: sudo ciru-strixlink setup --role ${f.role === "a" ? "b" : "a"} --subnet ${esc(f.subnet)} --mtu ${f.mtu} --backend ${esc(f.backend)} --apply<button class="copy" data-copy="sudo ciru-strixlink setup --role ${f.role === "a" ? "b" : "a"} --subnet ${esc(f.subnet)} --mtu ${f.mtu} --backend ${esc(f.backend)} --apply">copy</button></div>
      </div></div>` : ""}
  </div>`;
}

function rollbackHtml() {
  const r = S.setup.rb;
  const p = S.setup.rollbackPlan;
  return `<div class="sec"><div class="sec-h"><h2>Rollback</h2></div>
    <p class="dim" style="margin:0 0 12px;font-size:12px">Removes only the exact CiruStrixLink-created NetworkManager profile. Unrelated profiles are never touched.</p>
    <div class="form">
      <div class="fld"><label>profile to remove</label><input class="inp" data-inp="rb.profile" value="${esc(r.profile)}"></div>
      <div class="fld"><label>restore preserved profile (optional)</label><input class="inp" data-inp="rb.restore" value="${esc(r.restore)}" placeholder="none"></div>
    </div>
    <div class="row mt16"><button class="btn" data-act="rollback-plan">Preview rollback</button></div>
    ${p ? `<div class="plan"><div class="plan-h"><span class="t">Rollback plan</span><span class="micro dim">${esc(p.interface || "")}</span></div>
      <div class="plan-body">
        ${(p.commands || []).map((c) => `<div class="cmdline" style="margin-top:6px">${esc(c.name + " " + (c.args || []).join(" "))}<button class="copy" data-copy="${esc(c.name + " " + (c.args || []).join(" "))}">copy</button></div>`).join("")}
        ${(p.warnings || []).map((w) => `<div class="warnline">${esc(w)}</div>`).join("")}
        <div class="cmdline mt8">sudo ciru-strixlink rollback --profile ${esc(r.profile)}${r.restore ? ` --restore ${esc(r.restore)}` : ""} --apply<button class="copy" data-copy="sudo ciru-strixlink rollback --profile ${esc(r.profile)}${r.restore ? ` --restore ${esc(r.restore)}` : ""} --apply">copy</button></div>
      </div></div>` : ""}
  </div>`;
}

function vSetup() {
  const sup = S.local && S.local.host && S.local.host.supported === false;
  return `<div class="view-head"><h1>Connection setup</h1><span class="sub">requirements and network settings · changes are previewed, not applied</span></div>
    ${sup ? `<div class="banner warn"><svg class="bic" viewBox="0 0 16 16"><path d="M8 2 1.8 13.5h12.4L8 2Z" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"/><path d="M8 6.5v3.2" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><circle cx="8" cy="11.6" r=".8" fill="currentColor"/></svg><div><h3>This console host is not a supported Strix Halo Linux system</h3><p>Prerequisite and plan previews still render. Apply the reviewed commands on the target hosts.</p></div></div>` : ""}
    <div class="sec"><div class="sec-h"><h2>Prerequisite inventory</h2></div>
      ${prereqCompareHtml()}
    </div>
    ${installPlanHtml()}
    ${setupPlanHtml()}
    ${rollbackHtml()}`;
}

/* ---------------- Test view ---------------- */

function doctorHtml() {
  const t = S.test;
  const peer = defaultPeer();
  const d = t.doctor;
  return `<div class="sec"><div class="sec-h"><h2>Quick diagnostics</h2></div>
    <div class="row">
      <span class="micro">peer <b class="mono" style="color:var(--ink-1)">${esc(peer || "—")}</b></span>
      <button class="btn" data-act="doctor" ${!peer || t.doctorBusy ? "disabled" : ""}>${t.doctorBusy ? "Checking…" : "Run quick check"}</button>
      <span class="dim" style="font-size:11.5px">runs on this console host; open the console on the peer to check the reverse direction</span>
    </div>
    ${d ? (d.error ? `<div class="banner bad mt16"><div><h3>Quick check failed</h3><p>${esc(d.error)}${d.detail ? `<br><span class="mono" style="font-size:11px">${esc(d.detail)}</span>` : ""}</p></div></div>`
      : `<div class="plan mt16"><div class="plan-h"><span class="t">Route and MTU</span>
          <span class="st ${d.route && d.route.interface_match && d.path_mtu_passed ? "ok" : "bad"}">${d.route && d.route.interface_match && d.path_mtu_passed ? "pass" : "attention"}</span></div>
        <div class="plan-body"><dl class="kv">
          <dt>interface</dt><dd>${orDash(d.interface)}</dd>
          <dt>local address</dt><dd>${orDash(d.local_ip)}</dd>
          <dt>route to peer</dt><dd>${d.route ? esc(d.route.device) + " · src " + esc(d.route.source || "—") : "—"}</dd>
          <dt>route match</dt><dd>${d.route ? (d.route.interface_match ? '<span style="color:var(--green)">yes — traffic stays on the USB4 interface</span>' : '<span style="color:var(--red)">no — route leaves the USB4 interface</span>') : "—"}</dd>
          <dt>path MTU</dt><dd>${d.path_mtu_passed ? '<span style="color:var(--green)">passed</span>' : '<span style="color:var(--red)">failed</span>'} <span class="dim">${esc(d.path_mtu_detail || "")}</span></dd>
        </dl></div></div>`) : ""}
  </div>`;
}

function benchHtml() {
  const t = S.test, f = t.form;
  const b = t.bench;
  return `<div class="sec"><div class="sec-h"><h2>Link benchmark</h2></div>
    <p class="dim" style="margin:0 0 12px;font-size:12px">The console asks the peer agent to open a time-boxed listener on the USB4 address, then measures RTT, both throughput directions, reconnect behavior, and payload integrity. No root required.</p>
    <div class="form">
      <div class="fld"><label>port</label><input class="inp" data-inp="form.port" value="${f.port}"></div>
      <div class="fld"><label>duration / direction (s)</label><input class="inp" data-inp="form.duration_s" value="${f.duration_s}"></div>
      <div class="fld"><label>parallel streams</label><input class="inp" data-inp="form.streams" value="${f.streams}"></div>
      <div class="fld"><label>RTT samples</label><input class="inp" data-inp="form.rtt_samples" value="${f.rtt_samples}"></div>
    </div>
    <div class="row mt16">
      <button class="btn btn-primary" data-act="bench" ${t.benchBusy ? "disabled" : ""}>${t.benchBusy ? "Benchmark running…" : "Start test"}</button>
      ${t.benchBusy ? `<span class="mono dim" style="font-size:11px" data-bench-elapsed></span>` : ""}
    </div>
    ${t.benchBusy ? benchPhasesHtml() : ""}
    ${t.benchErr ? `<div class="banner bad mt16"><div><h3>Benchmark failed</h3><p>${esc(t.benchErr)}</p></div></div>` : ""}
    ${b ? benchResultHtml(b) : ""}
  </div>`;
}

function benchPhasesHtml() {
  const phases = ["contact peer agent", "time-boxed listener on USB4 address", "route + path MTU gate", "RTT samples", "throughput local → peer", "throughput peer → local", "integrity + reconnect", "quality policy"];
  return `<div class="plan mt16"><div class="plan-h"><span class="t">Running</span><span class="st live">in progress</span></div>
    <div class="plan-body">${phases.map((p, i) => `<div class="phase ${i === 0 ? "run" : ""}"><span class="pdot"></span><span class="pt">${esc(p)}</span>${i === 0 ? `<span class="pe">agent + measurement stream in flight</span>` : ""}</div>`).join("")}</div></div>`;
}

function benchResultHtml(r) {
  const bm = r.benchmark || {};
  const pol = bm.policy || {};
  const up = bm.local_to_peer || {}, dn = bm.peer_to_local || {};
  const max = Math.max(up.gbps || 0, dn.gbps || 0, 0.001);
  const weakIsUp = (up.gbps || 0) <= (dn.gbps || 0);
  const hostL = r.hostname || "local";
  const peer = r.peer_ip || "peer";
  const bar = (label, d, weak) => `<div class="bar-row">
    <span class="bl">${esc(label)}${weak ? ' <span style="color:var(--amber)">· gate</span>' : ""}</span>
    <span class="bar-track"><span class="bar-fill ${weak ? "weak" : ""}" style="width:${Math.max(1.5, ((d.gbps || 0) / max) * 100)}%"></span></span>
    <span class="bar-val">${(d.gbps || 0).toFixed(2)}<small>Gb/s</small></span>
  </div>`;
  const rtt = bm.rtt || {};
  const cls = pol.class || "unknown";
  return `<div class="mt24">
    <div class="qgate ${esc(cls)}">
      <span class="cls">${esc(cls)}</span>
      <div class="grow">
        <div style="font-size:12.5px">${esc(pol.reason || "")}</div>
        <div class="dim" style="font-size:11.5px;margin-top:2px">weaker direction gates the class · ${esc(fmtClock(r.timestamp_utc))} · ${esc(r.interface || "")}</div>
      </div>
      <button class="btn btn-ghost" data-act="bench-download">Export report</button>
    </div>
    <div class="sec"><div class="sec-h"><h2>Throughput — both directions</h2></div>
      <div class="bars">
        ${bar(`${hostL} → ${peer}`, up, weakIsUp)}
        ${bar(`${peer} → ${hostL}`, dn, !weakIsUp)}
      </div>
      <p class="dim" style="font-size:11.5px;margin:8px 0 0">asymmetry ${(bm.asymmetry_ratio || 0).toFixed(2)}× · recommended bulk sender / stage 0: <b class="mono" style="color:var(--ink-0)">${esc(bm.faster_sender === "local" ? hostL : peer)}</b></p>
    </div>
    <div class="sec"><div class="sec-h"><h2>Latency and integrity</h2></div>
      <div class="statgrid">
        <div class="stat"><div class="k">RTT p50</div><div class="v">${(rtt.p50_ms || 0).toFixed(3)} <small>ms</small></div></div>
        <div class="stat"><div class="k">RTT p95</div><div class="v">${(rtt.p95_ms || 0).toFixed(3)} <small>ms</small></div></div>
        <div class="stat"><div class="k">RTT p99</div><div class="v ${(rtt.p99_ms || 0) > 5 ? "warn" : ""}">${(rtt.p99_ms || 0).toFixed(3)} <small>ms</small></div></div>
        <div class="stat"><div class="k">samples</div><div class="v">${rtt.samples || 0}</div></div>
        <div class="stat"><div class="k">reconnect</div><div class="v ${bm.reconnect_passed === bm.reconnect_total ? "good" : "bad"}">${bm.reconnect_passed}/${bm.reconnect_total}</div></div>
        <div class="stat"><div class="k">integrity ↑</div><div class="v ${bm.integrity && bm.integrity.upload_ok ? "good" : "bad"}">${bm.integrity && bm.integrity.upload_ok ? "pass" : "fail"}</div></div>
        <div class="stat"><div class="k">integrity ↓</div><div class="v ${bm.integrity && bm.integrity.download_ok ? "good" : "bad"}">${bm.integrity && bm.integrity.download_ok ? "pass" : "fail"}</div></div>
        <div class="stat"><div class="k">path MTU</div><div class="v ${r.path_mtu_passed ? "good" : "bad"}">${r.path_mtu_passed ? "pass" : "fail"}</div></div>
      </div>
    </div>
    <div class="sec"><div class="sec-h"><h2>Generated runtime policy</h2></div>
      <div class="statgrid">
        <div class="stat"><div class="k">heartbeat</div><div class="v">${pol.heartbeat_interval_ms} <small>ms</small></div></div>
        <div class="stat"><div class="k">peer timeout</div><div class="v">${pol.peer_timeout_ms} <small>ms</small></div></div>
        <div class="stat"><div class="k">retries</div><div class="v">${pol.reconnect_attempts}</div></div>
        <div class="stat"><div class="k">max in-flight</div><div class="v">${pol.max_in_flight}</div></div>
        <div class="stat"><div class="k">chunk</div><div class="v">${((pol.suggested_chunk_bytes || 0) / 1024).toFixed(0)} <small>KiB</small></div></div>
      </div>
      <p class="dim" style="font-size:11.5px;margin-top:8px">Conservative starting points for the model launcher — not permission to replay an individual tensor after failure.</p>
    </div>
  </div>`;
}

function vTest() {
  return `<div class="view-head"><h1>Speed test</h1><span class="sub">tests use the cable and can affect a running model · run when idle</span></div>
    ${doctorHtml()}
    ${benchHtml()}`;
}

/* ---------------- Runtime view ---------------- */

function vRuntime() {
  const p = pairReport();
  const blocked = p && (p.cleanup_required || p.nhi_status === "partial");
  const nhiOk = p && p.nhi_status === "ready" && p.lease_available && !p.cleanup_required;
  const e = S.rt.env;
  return `<div class="view-head"><h1>Launch settings</h1><span class="sub">connection settings for your model launcher · this does not start a model</span></div>
    ${!p ? `<div class="banner warn"><div><h3>No reconciled pair</h3><p>Environment generation needs a fresh reconciled pair report.</p></div></div>` : ""}
    ${blocked ? `<div class="banner bad"><div><h3>Runtime export blocked</h3><p>Partial or mismatched NHI endpoints must be cleaned on both hosts before any environment — portable or NHI — is generated.</p></div></div>` : ""}
    <div class="sec"><div class="sec-h"><h2>Transport selection</h2></div>
      <div class="form">
        <div class="fld"><label>runtime overlay</label>
          <div class="seg" role="group" aria-label="Runtime">
            ${["generic", "pytorch", "vllm"].map((r) => `<button data-set="rt.runtime" data-val="${r}" aria-pressed="${S.rt.runtime === r}">${r}</button>`).join("")}
          </div>
          <span class="hint">overlays add variables; they do not own the link</span></div>
        <div class="fld"><label>requested mode</label>
          <div class="seg" role="group" aria-label="Mode">
            <button data-set="rt.mode" data-val="auto" aria-pressed="${S.rt.mode === "auto"}">automatic</button>
            <button data-set="rt.mode" data-val="portable" aria-pressed="${S.rt.mode === "portable"}">Standard USB4</button>
            <button data-set="rt.mode" data-val="nhi" aria-pressed="${S.rt.mode === "nhi"}" ${nhiOk ? "" : "disabled"} title="${nhiOk ? "" : "requires a verified fast connection that is not already in use"}">Fast USB4 (NHI)</button>
          </div>
          <span class="hint">automatic selects NHI only when qualified with a free lease</span></div>
      </div>
      <div class="row mt16">
        <button class="btn btn-primary" data-act="env" ${!p || blocked ? "disabled" : ""}>Generate environment</button>
        ${S.rt.err ? `<span style="color:var(--red);font-size:12px">${esc(S.rt.err)}</span>` : ""}
      </div>
    </div>
    ${e ? `<div class="sec"><div class="sec-h"><h2>Generated environment</h2></div>
      <div class="plan"><div class="plan-h"><span class="t">mode <b class="mono">${esc(e.environment.mode)}</b> · runtime <b class="mono">${esc(e.environment.runtime)}</b></span>
        <span class="st ${e.environment.mode === "nhi" ? "ok" : "live"}">${e.environment.mode === "nhi" ? "accelerated" : "portable"}</span></div>
        <div class="plan-body">
          <table class="env-table">${Object.entries(e.environment.variables || {}).map(([k, v]) => `<tr><td class="ek">${esc(k)}</td><td class="ev">${esc(v)}</td></tr>`).join("")}</table>
          <pre class="dotenv">${esc(e.dotenv)}</pre>
          <div class="row mt8">
            <button class="btn" data-act="env-copy">Copy environment</button>
            <button class="btn btn-ghost" data-act="env-save">Save .env file</button>
          </div>
          ${e.environment.mode === "nhi" ? `<p class="dim" style="font-size:11.5px;margin:10px 0 0">Grant <span class="mono">CAP_SYS_RAWIO</span> only to the runtime process that imports the NHI device.</p>` : ""}
        </div></div>
    </div>` : ""}`;
}

/* ---------------- activity drawer ---------------- */

async function openDrawer() {
  $("#drawer").hidden = false;
  try { S.activity = await api("/api/activity"); } catch { /* keep old */ }
  renderDrawer();
}
function renderDrawer() {
  const items = (S.activity || []).map((a) => `<div class="act">
    <div class="aa"><span class="at">${esc(a.action)}</span><span class="st ${a.status === "ok" ? "ok" : a.status === "error" ? "bad" : "off"}" style="font-size:9.5px">${esc(a.status)}</span><span class="am">${esc(fmtClock(a.time))}</span></div>
    ${a.summary ? `<div class="as">${esc(a.summary)}</div>` : ""}
    ${a.detail ? `<div class="ad">${esc(a.detail)}</div>` : ""}
  </div>`).join("");
  $("#drawer-body").innerHTML = items || `<div class="empty">No console-initiated commands yet.</div>`;
}

/* ---------------- dispatch + events ---------------- */

function renderView() {
  const v = S.view;
  document.body.dataset.view = v;
  $$(".tab").forEach((t) => t.setAttribute("aria-selected", String(t.dataset.view === v)));
  const p = pairReport();
  const ctx = {
    pair: "",
    setup: "No automatic changes",
    test: "Runs only when you start a test",
    runtime: "Settings export only",
    diagnostics: p ? `pair identity ${p.pair_identity_valid ? "verified" : "not verified"}` : "Status unavailable",
  };
  $("#tabs-ctx").innerHTML = ctx[v] || "";
  $("#main").innerHTML = { pair: vPair, setup: vSetup, test: vTest, runtime: vRuntime, diagnostics: vDiagnostics }[v]();
}

function setPath(obj, path, val) {
  const ks = path.split(".");
  let o = obj;
  for (let i = 0; i < ks.length - 1; i++) o = o[ks[i]];
  const last = ks[ks.length - 1];
  o[last] = typeof o[last] === "number" ? Number(val) : val;
}

async function doBench() {
  const t = S.test;
  t.benchBusy = true; t.benchErr = null; t.benchStarted = Date.now();
  renderView();
  const iv = setInterval(() => {
    const el = $("[data-bench-elapsed]");
    if (el) el.textContent = "elapsed " + Math.round((Date.now() - t.benchStarted) / 1000) + "s";
  }, 500);
  try {
    t.bench = await api("/api/bench", { body: { duration_s: Number(t.form.duration_s), streams: Number(t.form.streams), rtt_samples: Number(t.form.rtt_samples), port: Number(t.form.port) } });
    announce("Benchmark complete: " + (t.bench.benchmark && t.bench.benchmark.policy ? t.bench.benchmark.policy.class : ""));
  } catch (e) {
    t.benchErr = e.message + (e.detail ? " — " + e.detail : "");
    t.bench = null;
  } finally {
    clearInterval(iv);
    t.benchBusy = false;
  }
  renderView();
}

document.addEventListener("click", async (ev) => {
  const cp = ev.target.closest("[data-copy]");
  if (cp) { copyText(cp.dataset.copy, cp); return; }
  const tab = ev.target.closest(".tab");
  if (tab) { S.view = tab.dataset.view; history.replaceState(null, "", "#/" + S.view); renderView(); return; }
  const seg = ev.target.closest("[data-set]");
  if (seg && seg.tagName === "BUTTON") {
    setPath(S, seg.dataset.set, seg.dataset.val);
    renderView();
    return;
  }
  const el = ev.target.closest("[data-act]");
  if (!el || el.disabled) return;
  const act = el.dataset.act;
  try {
    if (act === "refresh") { await refreshAll(false); }
    else if (act === "history-metric") { S.historyMetric = el.dataset.historyMetric === "pp" ? "pp" : "tg"; renderModelPanel(); }
    else if (act === "goto") { S.view = el.dataset.view; history.replaceState(null, "", "#/" + S.view); renderView(); }
    else if (act === "activity") { await openDrawer(); }
    else if (act === "activity-close") { $("#drawer").hidden = true; }
    else if (act === "download") {
      download("ciru-strixlink-reports.json", JSON.stringify({ downloaded_at: new Date().toISOString(), console_host: S.local, peer_host: S.peer, pair: S.pairEnv }, null, 2) + "\n");
    }
    else if (act === "endpoint-plan") { await loadEndpointPlan(el.dataset.arg); }
    else if (act === "expand-endpoints") {
      S.view = "diagnostics";
      history.replaceState(null, "", "#/diagnostics");
      renderView();
      ["endpoints-a", "endpoints-b"].forEach((id) => { const d = document.getElementById(id); if (d) d.open = true; });
      const d = document.getElementById("endpoints-b") || document.getElementById("endpoints-a");
      if (d) d.scrollIntoView({ behavior: "smooth", block: "center" });
    }
    else if (act === "install-plan") {
      el.disabled = true;
      try { S.setup.installPlan = await api(`/api/install/plan?include_optional=${S.setup.installOpts.optional}&self=${S.setup.installOpts.self}`); }
      catch (e) { S.setup.installPlan = { error: e.message }; announce("Install plan failed: " + e.message); }
      finally { el.disabled = false; }
      renderView();
    }
    else if (act === "setup-plan") {
      el.disabled = true;
      try { S.setup.setupPlan = await api("/api/setup/plan", { body: { ...S.setup.form } }); }
      catch (e) { S.setup.setupPlan = { error: e.message }; announce("Setup plan failed: " + e.message); }
      finally { el.disabled = false; }
      renderView();
    }
    else if (act === "rollback-plan") {
      el.disabled = true;
      try { S.setup.rollbackPlan = await api("/api/rollback/plan", { body: { ...S.setup.rb } }); }
      catch (e) { S.setup.rollbackPlan = { error: e.message }; announce("Rollback plan failed: " + e.message); }
      finally { el.disabled = false; }
      renderView();
    }
    else if (act === "doctor") {
      S.test.doctorBusy = true; renderView();
      try { S.test.doctor = await api("/api/doctor", { body: { peer: defaultPeer() } }); }
      catch (e) { S.test.doctor = { error: e.message, detail: e.detail }; }
      finally { S.test.doctorBusy = false; }
      renderView();
    }
    else if (act === "bench") { await doBench(); }
    else if (act === "bench-download") { download("ciru-strixlink-benchmark.json", JSON.stringify(S.test.bench, null, 2) + "\n"); }
    else if (act === "env") { await genEnv(); }
    else if (act === "env-copy" && S.rt.env) { copyText(S.rt.env.dotenv, el); }
    else if (act === "env-save" && S.rt.env) { download("ciru-strixlink.env", S.rt.env.dotenv, "text/plain"); }
  } catch (e) {
    announce("Action failed: " + e.message);
  }
});

document.addEventListener("change", (ev) => {
  const cb = ev.target.closest("[data-set]");
  if (cb && cb.type === "checkbox") { setPath(S, cb.dataset.set, cb.checked); return; }
  const inp = ev.target.closest("[data-inp]");
  if (inp) setPath(S, inp.dataset.inp, inp.value);
});

document.addEventListener("keydown", (ev) => {
  if (ev.key === "Escape" && !$("#drawer").hidden) $("#drawer").hidden = true;
  if (ev.altKey && ["1", "2", "3", "4", "5"].includes(ev.key)) {
    S.view = ["pair", "setup", "test", "runtime", "diagnostics"][Number(ev.key) - 1];
    renderView();
  }
});

async function genEnv() {
  S.rt.err = null;
  try {
    S.rt.env = await api("/api/env", { body: { mode: S.rt.mode, runtime: S.rt.runtime } });
    announce("Environment generated: " + S.rt.env.environment.mode);
  } catch (e) {
    S.rt.env = null;
    S.rt.err = e.message + (e.detail ? " — " + e.detail : "");
  }
  renderView();
}

function defaultPeer() {
  const a = reportA();
  if (a && a.peer) return a.peer;
  if (S.local && S.local.transport && S.local.transport.peer) return S.local.transport.peer;
  if (S.health && S.health.peer && S.health.peer.configured) return S.health.peer.address;
  return "";
}

boot();
