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
  launch: { status: null, busy: false, err: null, profile: 2, profileTouched: false, confirmed: false },
  historyMetric: "tg",
  activity: [],
  view: "pair",
  busy: false,
  setup: { installPlan: null, installOpts: { optional: false, self: false }, setupPlan: null, rollbackPlan: null,
           formTouched: false, installTouched: false, rollbackTouched: false,
           form: { role: "a", subnet: "10.77.77.0/30", mtu: 1500, backend: "auto", profile: "ciru-strixlink-usb4", take_over: false },
           rb: { profile: "ciru-strixlink-usb4", restore: "" } },
  ep: null,             // endpoint plan response
  test: { doctor: null, doctorBusy: false, benchBusy: false, bench: null, benchErr: null, benchStarted: 0,
          form: { port: 55321, duration_s: 5, streams: 4, rtt_samples: 100 } },
};

/* ---------------- boot ---------------- */

async function boot() {
  const hm = location.hash.match(/^#\/(\w+)(\?(.*))?$/);
  if (hm && hm[1] === "runtime") history.replaceState(null, "", "#/launch");
  if (hm && ["pair", "setup", "launch", "test", "diagnostics"].includes(hm[1] === "runtime" ? "launch" : hm[1])) S.view = hm[1] === "runtime" ? "launch" : hm[1];
  const hparams = new URLSearchParams(hm && hm[3] ? hm[3] : "");
  try {
    S.health = await api("/api/health");
  } catch (e) {
    $("#main").innerHTML = `<div class="empty"><b>Console API unreachable.</b><br>${esc(e.message)}</div>`;
    return;
  }
  renderShell();
  await Promise.all([refreshAll(true), refreshModel(), refreshLaunch(false)]);
  if (hparams.get("plan")) loadEndpointPlan(hparams.get("plan"));
  setInterval(tickAges, 5000);
  setInterval(() => { if (!document.hidden) refreshModel(); }, 5000);
  setInterval(() => { if (!document.hidden) refreshLaunch(S.view === "launch"); }, 10000);
  setInterval(() => { if (!S.busy) refreshAll(true); }, 30000);
}

async function refreshAll(quiet) {
  if (S.busy) return;
  S.busy = true;
  if (!quiet) { $("#loadbar").hidden = false; announce("Refreshing both hosts"); }
  try {
    const r = await api("/api/refresh", { method: "POST" });
    S.local = r.local; S.peer = r.peer; S.pairEnv = r.pair;
    hydrateSetupDefaults();
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

async function refreshLaunch(render = true) {
  if (S.launch.busy) return;
  try {
    S.launch.status = await api("/api/launch");
    if (!S.launch.profileTouched && S.launch.status.selected_profile) S.launch.profile = S.launch.status.selected_profile;
    S.launch.err = null;
  } catch (e) {
    S.launch.err = e.message + (e.detail ? " — " + e.detail : "");
  }
  if (render && S.view === "launch") renderView();
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
      primary: { label: "Open Launch", act: "goto", view: "launch", kind: "primary" } };
  }
  if (p.nhi_status === "ready" && p.lease_available) {
    return { key: "ready", tone: "ok", title: "Fast USB4 is ready",
      sub: "Both computers are connected. The fast connection is set up and available for your model to use.",
      primary: { label: "Open Launch", act: "goto", view: "launch", kind: "primary" } };
  }
  if (p.arm_allowed) {
    return { key: "arm", tone: "warn", title: "Connected. Fast USB4 needs setup.",
      sub: "The cable connection works. Both computers support fast mode, but it hasn't been prepared yet.",
      primary: { label: "Review fast-mode setup", act: "endpoint-plan", arg: "prepare", kind: "primary" } };
  }
  return { key: "portable", tone: "ok", title: "Standard USB4 is ready",
    sub: "The normal cable connection works. Fast mode is not confirmed available; your model can use the standard connection instead.",
    primary: { label: "Open Launch", act: "goto", view: "launch", kind: "primary" } };
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
  const priv = kind === "helper" ? '<span class="st ok">scoped inspection</span>' : payload ? (payload.privileged ? '<span class="st ok">root inspection</span>' : '<span class="st warn">user — limited</span>') : "";
  const src = kind === "file" ? '<span class="st off">report file</span>' : "";
  const missing = !rep;
  return `<section class="plate" aria-label="Host ${side.toUpperCase()}">
    <div class="plate-top">
      <img class="plate-emblem" src="assets/emblem.png" alt="">
      <div class="plate-id">
        <div class="plate-host">${esc(name)}</div>
        <div class="plate-role">host ${side} ${kind === "file" ? "· report file" : kind === "helper" ? side === "a" ? "· scoped local helper" : "· scoped peer helper" : kind === "agent" ? "· via peer agent" : "· this console"}</div>
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
  const nextNote = inUse ? "Keep this connection as it is, or open Launch to unload the model from both computers." : ready ? "Open Launch to choose the GLM 5.3 memory profile and load it across both computers." : st.sub;
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
const PR_TONE = (s) => ({ available: "ok", missing: "warn", inactive: "warn", not_detected: "warn", unsupported: "bad", unknown: "off" }[s] || "off");
const PR_STATUS_LABEL = {
  available: "Ready",
  missing: "Missing",
  inactive: "Inactive",
  not_detected: "Not found",
  unsupported: "Unsupported",
  unknown: "Unknown",
};

function prereqStats(rep) {
  const all = (rep && rep.components) || [];
  const required = all.filter((c) => c.required);
  const issues = all.filter((c) => c.required && c.status !== "available");
  return { all, required, issues, ready: Boolean(rep && rep.ready && !issues.length) };
}

function requirementHtml(c) {
  const issue = c.status !== "available";
  return `<div class="requirement ${issue ? "needs-attention" : "is-ready"}">
    <span class="requirement-mark" aria-hidden="true">${issue ? "!" : "✓"}</span>
    <div class="requirement-copy">
      <div class="requirement-title"><strong>${esc(c.label)}</strong>${c.required ? "" : `<span>Optional</span>`}</div>
      <p>${esc(c.summary || "No description was reported.")}</p>
      ${c.detected ? `<code>${esc(c.detected)}</code>` : ""}
      ${issue && (c.suggested_command || c.help_url) ? `<div class="requirement-actions">
        ${c.suggested_command ? `<button class="text-action" data-copy="${esc(c.suggested_command)}">Copy suggested command</button>` : ""}
        ${c.help_url ? `<a class="text-action" href="${esc(c.help_url)}" target="_blank" rel="noreferrer">Open instructions ↗</a>` : ""}
      </div>` : ""}
    </div>
    <span class="st ${PR_TONE(c.status)}">${esc(PR_STATUS_LABEL[c.status] || c.status)}</span>
  </div>`;
}

function prereqHostHtml(rep, fallbackName, peerMissing) {
  if (!rep) {
    return `<article class="readiness-card unavailable">
      <div class="readiness-head">
        <div class="computer-glyph" aria-hidden="true"><i></i></div>
        <div><span class="setup-kicker">Other computer</span><h3>${esc(fallbackName)}</h3></div>
        <span class="st off">Not reached</span>
      </div>
      <p class="readiness-summary">CiruStrixLink cannot check this computer yet. Start its read-only status agent, then refresh both computers.</p>
      ${peerMissing ? `<div class="cmdline">ciru-strixlink agent<button class="copy" data-copy="ciru-strixlink agent">copy</button></div>` : ""}
    </article>`;
  }
  const s = prereqStats(rep);
  const hostname = (rep.system && rep.system.hostname) || fallbackName;
  const optional = s.all.length - s.required.length;
  const issueList = s.issues.map(requirementHtml).join("");
  const groups = PREREQ_GROUPS.map((g) => {
    const items = g.ids.map((id) => s.all.find((c) => c.id === id)).filter(Boolean);
    if (!items.length) return "";
    return `<div class="requirement-group"><h4>${esc(g.label)}</h4>${items.map(requirementHtml).join("")}</div>`;
  }).join("");
  return `<article class="readiness-card ${s.ready ? "ready" : "attention"}">
    <div class="readiness-head">
      <div class="computer-glyph" aria-hidden="true"><i class="${s.ready ? "on" : ""}"></i></div>
      <div><span class="setup-kicker">Computer</span><h3>${esc(hostname)}</h3></div>
      <span class="st ${s.ready ? "ok" : rep.overall_status === "unsupported" ? "bad" : "warn"}">${s.ready ? "Ready" : s.issues.length + " to fix"}</span>
    </div>
    <p class="readiness-summary">${s.ready
      ? `${s.required.length} required checks passed${optional ? ` · ${optional} optional capabilities found` : ""}.`
      : `${s.issues.length} required ${s.issues.length === 1 ? "item needs" : "items need"} attention before setup can continue.`}</p>
    ${issueList ? `<div class="requirement-issues">${issueList}</div>` : ""}
    <details class="setup-disclosure check-list">
      <summary><span>View all ${s.all.length} checks</span><small>hardware, drivers, and tools</small><i aria-hidden="true"></i></summary>
      <div class="disclosure-body">${groups}</div>
    </details>
  </article>`;
}

function prereqCompareHtml() {
  const a = S.local && S.local.prerequisites;
  const b = S.peer && !S.peer.state && S.peer.prerequisites;
  const fallbackA = (S.local && S.local.host && S.local.host.hostname) || hostName("a");
  const fallbackB = (S.peer && S.peer.host && S.peer.host.hostname) || hostName("b");
  return `<div class="readiness-grid">
    ${prereqHostHtml(a, fallbackA, false)}
    ${prereqHostHtml(b, fallbackB, true)}
  </div>`;
}

function installActionHtml(a) {
  const command = a.command ? a.command + " " + (a.args || []).join(" ") : "";
  return `<div class="install-action">
    <div><span class="setup-kicker">${esc(a.type)}</span><strong>${esc(a.summary)}</strong>
      ${(a.components || []).length ? `<small>${a.components.map(esc).join(" · ")}</small>` : ""}</div>
    <span class="st ${a.can_apply ? "ok" : "warn"}">${a.can_apply ? "Can install" : "Manual"}</span>
    ${command ? `<div class="cmdline">${esc(command)}<button class="copy" data-copy="${esc(command)}">copy</button></div>` : ""}
    ${a.target ? `<div class="cmdline">${esc(a.source)} → ${esc(a.target)}</div>` : ""}
    ${a.help_url ? `<a class="text-action" href="${esc(a.help_url)}" target="_blank" rel="noreferrer">Open manual instructions ↗</a>` : ""}
  </div>`;
}

function installPlanHtml() {
  const o = S.setup.installOpts;
  const p = S.setup.installPlan;
  const flags = `${o.optional ? " --include-optional" : ""}${o.self ? " --self" : ""}`;
  return `<details class="setup-disclosure install-tools" ${p || S.setup.installTouched ? "open" : ""}>
    <summary><span>Install missing software</span><small>Review a safe plan; this page never installs anything itself</small><i aria-hidden="true"></i></summary>
    <div class="disclosure-body">
      <div class="choice-list">
        <label class="choice-row"><input type="checkbox" data-set="setup.installOpts.optional" ${o.optional ? "checked" : ""}><span><strong>Include optional diagnostic tools</strong><small>Adds utilities that improve troubleshooting but are not required for the connection.</small></span></label>
        <label class="choice-row"><input type="checkbox" data-set="setup.installOpts.self" ${o.self ? "checked" : ""}><span><strong>Install CiruStrixLink on this computer</strong><small>Includes the command-line tool itself in the reviewed plan.</small></span></label>
      </div>
      <button class="btn" data-act="install-plan">Review what can be installed</button>
      ${p && p.error ? `<div class="banner bad mt16"><div><h3>Installation plan unavailable</h3><p>${esc(p.error)}</p></div></div>` : ""}
      ${p && !p.error ? `<div class="review-result">
        <div class="review-head"><div><span class="setup-kicker">Reviewed plan</span><h4>${(p.actions || []).length ? `${p.actions.length} ${p.actions.length === 1 ? "action" : "actions"}` : "Nothing to install"}</h4></div>
          <span class="st ${p.can_apply ? "ok" : "warn"}">${p.can_apply ? "Ready to run" : "Manual steps"}</span></div>
        ${(p.actions || []).map(installActionHtml).join("")}
        ${(p.warnings || []).map((w) => `<div class="warnline">${esc(w)}</div>`).join("")}
        ${p.can_apply ? `<div class="copy-command"><div><strong>Run this on the computer</strong><small>The browser will not request or store your administrator password.</small></div>
          <div class="cmdline">sudo ciru-strixlink install${flags} --apply<button class="copy" data-copy="sudo ciru-strixlink install${flags} --apply">copy</button></div></div>`
          : `<p class="review-note">Follow the linked instructions for items that cannot be changed safely by CiruStrixLink.</p>`}
      </div>` : ""}
    </div>
  </details>`;
}

function ipv4Parts(value) {
  const parts = String(value || "").split("/");
  const octets = parts[0].split(".").map(Number);
  if (parts[1] !== "30" || octets.length !== 4 || octets.some((n) => !Number.isInteger(n) || n < 0 || n > 255)) return null;
  const base = octets.slice();
  base[3] = base[3] & 252;
  return [`${base[0]}.${base[1]}.${base[2]}.${base[3] + 1}`, `${base[0]}.${base[1]}.${base[2]}.${base[3] + 2}`];
}

function subnetForAddress(value) {
  const octets = String(value || "").split(".").map(Number);
  if (octets.length !== 4 || octets.some((n) => !Number.isInteger(n) || n < 0 || n > 255)) return "";
  return `${octets[0]}.${octets[1]}.${octets[2]}.${octets[3] & 252}/30`;
}

function hydrateSetupDefaults() {
  if (S.setup.formTouched) return;
  const a = reportA();
  if (!a) return;
  const pair = ipv4Parts(subnetForAddress(a.local_address));
  if (pair) {
    S.setup.form.subnet = subnetForAddress(a.local_address);
    S.setup.form.role = a.local_address === pair[1] ? "b" : "a";
  }
}

function setupPairMapHtml(addressA, addressB, tone, label) {
  return `<div class="setup-pair-map ${tone}">
    <div class="setup-endpoint"><div class="computer-glyph" aria-hidden="true"><i class="on"></i></div><div><strong>${esc(hostName("a"))}</strong><code>${esc(addressA || "Address not assigned")}</code></div></div>
    <div class="setup-cable"><span>${esc(label)}</span><div><i></i><b>USB4</b><i></i></div></div>
    <div class="setup-endpoint right-side"><div><strong>${esc(hostName("b"))}</strong><code>${esc(addressB || "Address not assigned")}</code></div><div class="computer-glyph" aria-hidden="true"><i class="on"></i></div></div>
  </div>`;
}

function setupPlanResultHtml(p, f) {
  if (!p) return "";
  if (p.error) return `<div class="banner bad mt16"><div><h3>Connection plan unavailable</h3><p>${esc(p.error)}</p></div></div>`;
  const localCommand = `sudo ciru-strixlink setup --role ${f.role} --subnet ${f.subnet} --mtu ${f.mtu} --backend ${f.backend}${f.take_over ? " --take-over" : ""} --apply`;
  const peerCommand = `sudo ciru-strixlink setup --role ${f.role === "a" ? "b" : "a"} --subnet ${f.subnet} --mtu ${f.mtu} --backend ${f.backend} --apply`;
  return `<div class="review-result connection-review">
    <div class="review-head"><div><span class="setup-kicker">Reviewed plan</span><h4>Configure both computers as one pair</h4></div><span class="st live">No changes made</span></div>
    <dl class="plan-facts"><div><dt>Interface</dt><dd>${esc(p.interface)}</dd></div><div><dt>Configuration</dt><dd>${esc(p.backend)}</dd></div><div><dt>Packet size</dt><dd>${p.mtu}</dd></div></dl>
    ${(p.warnings || []).map((w) => `<div class="warnline">${esc(w)}</div>`).join("")}
    <details class="technical-detail"><summary>Show exact system commands</summary><div>
      ${(p.commands || []).map((c) => `<div class="cmdline">${esc(c.name + " " + (c.args || []).join(" "))}<button class="copy" data-copy="${esc(c.name + " " + (c.args || []).join(" "))}">copy</button></div>`).join("")}
    </div></details>
    <div class="pair-commands">
      <div><span class="setup-kicker">On ${esc(hostName("a"))}</span><div class="cmdline">${esc(localCommand)}<button class="copy" data-copy="${esc(localCommand)}">copy</button></div></div>
      <div><span class="setup-kicker">On ${esc(hostName("b"))}</span><div class="cmdline">${esc(peerCommand)}<button class="copy" data-copy="${esc(peerCommand)}">copy</button></div></div>
    </div>
    <p class="review-note">Run the two reviewed commands on their named computers, then refresh. CiruStrixLink will verify the pair before reporting success.</p>
  </div>`;
}

function connectionFormHtml() {
  const f = S.setup.form;
  const p = S.setup.setupPlan;
  const addresses = ipv4Parts(f.subnet) || ["First usable address", "Second usable address"];
  const addressA = f.role === "a" ? addresses[0] : addresses[1];
  const addressB = f.role === "a" ? addresses[1] : addresses[0];
  const detectedInterface = (reportA() && reportA().interface) || "auto";
  return `<div class="connection-form">
    <div class="field-intro"><span class="setup-kicker">Address assignment</span><h4>Give each computer its own end of the cable</h4><p>The addresses are private to this USB4 cable. They are not exposed to your home network or the internet.</p></div>
    ${setupPairMapHtml(addressA, addressB, "proposed", "Proposed")}
    <div class="swap-row"><button class="btn btn-ghost" data-set="setup.form.role" data-val="${f.role === "a" ? "b" : "a"}">⇄ Swap addresses</button></div>
    <div class="friendly-form">
      <div class="fld"><label for="setup-subnet">Private address range</label><input id="setup-subnet" class="inp" data-inp="setup.form.subnet" value="${esc(f.subnet)}"><span class="hint">Two usable addresses, one for each computer</span></div>
      <div class="fld"><label for="setup-backend">Keep the connection</label>
        <select id="setup-backend" class="sel" data-inp="setup.form.backend">
          <option value="auto" ${f.backend === "auto" ? "selected" : ""}>Automatically choose (recommended)</option>
          <option value="networkmanager" ${f.backend === "networkmanager" ? "selected" : ""}>After a restart</option>
          <option value="iproute2" ${f.backend === "iproute2" ? "selected" : ""}>Until the next restart</option>
        </select><span class="hint">Automatic uses persistent settings when supported</span></div>
      <fieldset class="fld"><legend>Packet size</legend><div class="seg" role="group" aria-label="Packet size">
          <button data-set="setup.form.mtu" data-val="1500" aria-pressed="${f.mtu === 1500}">Standard · 1500</button>
          <button data-set="setup.form.mtu" data-val="9000" aria-pressed="${f.mtu === 9000}">Jumbo · 9000</button>
        </div><span class="hint">Use Jumbo only after both computers pass at Standard</span></fieldset>
    </div>
    <details class="technical-detail advanced-settings"><summary>Advanced network settings</summary><div class="friendly-form">
      <div class="fld"><label>USB4 interface</label><input class="inp" value="${esc(detectedInterface)}" disabled><span class="hint">Detected automatically</span></div>
      <div class="fld"><label for="setup-profile">Saved connection name</label><input id="setup-profile" class="inp" data-inp="setup.form.profile" value="${esc(f.profile)}"></div>
      <label class="choice-row takeover"><input type="checkbox" data-set="setup.form.take_over" ${f.take_over ? "checked" : ""}><span><strong>Replace an existing saved connection</strong><small>Use only after reviewing which exact NetworkManager profile will be replaced.</small></span></label>
    </div></details>
    <div class="review-cta"><div><strong>Nothing changes when you review</strong><p>You will see the exact plan and a separate command for each computer.</p></div><button class="btn btn-primary" data-act="setup-plan">Review connection plan</button></div>
    ${setupPlanResultHtml(p, f)}
  </div>`;
}

function setupPlanHtml() {
  const p = pairReport();
  const connected = Boolean(p && p.pair_identity_valid && p.portable_ready);
  const fast = !p ? { tone: "off", label: "Unknown", copy: "An optional accelerator layered on top after the standard connection is healthy." }
    : p.nhi_in_use ? { tone: "live", label: "In use", copy: "The optional accelerator is configured and currently held by a workload." }
    : p.nhi_ready ? { tone: "ok", label: "Ready", copy: "The optional accelerator is configured and available for a supported runtime." }
    : p.nhi_status === "blocked" || p.nhi_status === "partial" || p.cleanup_required
      ? { tone: "warn", label: "Needs attention", copy: "The optional accelerator settings do not match. Review Diagnostics before changing them." }
      : p.arm_allowed ? { tone: "warn", label: "Can be prepared", copy: "Both computers support the optional accelerator, but it has not been prepared yet." }
      : { tone: "off", label: "Optional", copy: "An optional accelerator layered on top after the standard connection is healthy." };
  const a = reportA(), b = reportB();
  const currentMap = a && b ? setupPairMapHtml(a.local_address, b.local_address, connected ? "connected" : "attention", connected ? "Connected" : "Needs attention") : "";
  const editor = connectionFormHtml();
  return `<section class="setup-section" id="setup-network">
    <div class="setup-section-head"><div><span class="step-number">2</span><span class="setup-kicker">Cable connection</span><h2>${connected ? "The USB4 network is connected" : "Configure the USB4 cable network"}</h2>
      <p>${connected ? "Both computers have reciprocal private addresses and can reach each other over the cable." : "Assign one private address to each end so the computers can communicate without using your LAN."}</p></div>
      <span class="st ${connected ? "ok" : "warn"}">${connected ? "Connected" : "Setup needed"}</span></div>
    ${currentMap}
    <div class="lane-explainer"><div><i class="portable"></i><div><strong>Standard USB4</strong><p>The required base connection. It carries control traffic and works with ordinary network-aware runtimes.</p></div><span class="st ${connected ? "ok" : "warn"}">${connected ? "Ready" : "Not ready"}</span></div>
      <div><i class="accelerated"></i><div><strong>Fast USB4 · NHI</strong><p>${esc(fast.copy)}</p></div><span class="st ${fast.tone}">${esc(fast.label)}</span></div></div>
    ${connected ? `<details class="setup-disclosure reconfigure" ${S.setup.setupPlan || S.setup.formTouched ? "open" : ""}><summary><span>Change connection settings</span><small>Only needed after changing hardware, addresses, or persistence</small><i aria-hidden="true"></i></summary><div class="disclosure-body">${editor}</div></details>` : editor}
  </section>`;
}

function rollbackHtml() {
  const r = S.setup.rb;
  const p = S.setup.rollbackPlan;
  const command = `sudo ciru-strixlink rollback --profile ${r.profile}${r.restore ? ` --restore ${r.restore}` : ""} --apply`;
  return `<section class="setup-section recovery" id="setup-recovery">
    <details class="setup-disclosure recovery-disclosure" ${p || S.setup.rollbackTouched ? "open" : ""}>
      <summary><span>Remove CiruStrixLink network settings</span><small>Recovery only · unrelated network connections are never touched</small><i aria-hidden="true"></i></summary>
      <div class="disclosure-body">
        <div class="field-intro"><span class="setup-kicker">Start over safely</span><h4>Remove only the saved connection created by CiruStrixLink</h4><p>Review the exact profile first. This does not reset networking, stop workloads, or delete unrelated profiles.</p></div>
        <div class="friendly-form two-col">
          <div class="fld"><label for="rollback-profile">Saved connection to remove</label><input id="rollback-profile" class="inp" data-inp="setup.rb.profile" value="${esc(r.profile)}"></div>
          <div class="fld"><label for="rollback-restore">Previously saved connection to restore</label><input id="rollback-restore" class="inp" data-inp="setup.rb.restore" value="${esc(r.restore)}" placeholder="Optional"></div>
        </div>
        <button class="btn btn-danger" data-act="rollback-plan">Review removal plan</button>
        ${p && p.error ? `<div class="banner bad mt16"><div><h3>Removal plan unavailable</h3><p>${esc(p.error)}</p></div></div>` : ""}
        ${p && !p.error ? `<div class="review-result"><div class="review-head"><div><span class="setup-kicker">Reviewed removal</span><h4>${esc(r.profile)}</h4></div><span class="st warn">No changes made</span></div>
          ${(p.commands || []).map((c) => `<div class="cmdline">${esc(c.name + " " + (c.args || []).join(" "))}<button class="copy" data-copy="${esc(c.name + " " + (c.args || []).join(" "))}">copy</button></div>`).join("")}
          ${(p.warnings || []).map((w) => `<div class="warnline">${esc(w)}</div>`).join("")}
          <div class="copy-command"><div><strong>Run only after reviewing the profile name</strong><small>This command changes the local computer.</small></div><div class="cmdline">${esc(command)}<button class="copy" data-copy="${esc(command)}">copy</button></div></div>
        </div>` : ""}
      </div>
    </details>
  </section>`;
}

function setupHeroHtml() {
  const a = S.local && S.local.prerequisites;
  const b = S.peer && !S.peer.state && S.peer.prerequisites;
  const aReady = prereqStats(a).ready;
  const bReady = prereqStats(b).ready;
  const requirementsReady = aReady && bReady;
  const p = pairReport();
  const connected = Boolean(p && p.pair_identity_valid && p.portable_ready);
  const ready = requirementsReady && connected;
  const state = ready ? {
    title: "This connection is already set up",
    body: "Both computers passed their required checks and can reach each other over the USB4 cable. You do not need to change anything here.",
    action: "Return to overview", view: "pair", tone: "ok",
  } : !requirementsReady ? {
    title: "Start by checking both computers",
    body: "CiruStrixLink will show only the missing requirements first. Technical details stay available when you need them.",
    action: "Review computer checks", target: "setup-requirements", tone: "warn",
  } : {
    title: "Both computers are ready for cable setup",
    body: "Assign one private address to each end of the USB4 cable, review the plan, and run the named command on each computer.",
    action: "Configure the cable", target: "setup-network", tone: "warn",
  };
  const stage = (number, label, detail, done, active) => `<div class="setup-stage ${done ? "done" : active ? "active" : ""}"><span>${done ? "✓" : number}</span><div><strong>${esc(label)}</strong><small>${esc(detail)}</small></div></div>`;
  return `<section class="setup-hero tone-${state.tone}">
    <div class="setup-hero-copy"><span class="setup-kicker">Connection guide</span><h1>${esc(state.title)}</h1><p>${esc(state.body)}</p>
      ${state.view ? `<button class="btn btn-primary" data-act="goto" data-view="${state.view}">${esc(state.action)} <span aria-hidden="true">→</span></button>`
        : `<button class="btn btn-primary" data-act="setup-jump" data-target="${state.target}">${esc(state.action)} <span aria-hidden="true">↓</span></button>`}</div>
    <div class="setup-path" aria-label="Connection setup progress">
      ${stage("1", "Computer checks", requirementsReady ? "Both computers ready" : "Review missing requirements", requirementsReady, !requirementsReady)}
      <i aria-hidden="true"></i>
      ${stage("2", "Cable network", connected ? "Private link connected" : "Assign both cable addresses", connected, requirementsReady && !connected)}
      <i aria-hidden="true"></i>
      ${stage("3", "Ready", ready ? "No changes needed" : "Verified after refresh", ready, false)}
    </div>
  </section>`;
}

function vSetup() {
  const sup = S.local && S.local.host && S.local.host.supported === false;
  const a = prereqStats(S.local && S.local.prerequisites);
  const b = prereqStats(S.peer && !S.peer.state && S.peer.prerequisites);
  const requirementsReady = a.ready && b.ready;
  return `<div class="setup-page">
    ${setupHeroHtml()}
    ${sup ? `<div class="banner warn"><svg class="bic" viewBox="0 0 16 16"><path d="M8 2 1.8 13.5h12.4L8 2Z" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"/><path d="M8 6.5v3.2" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><circle cx="8" cy="11.6" r=".8" fill="currentColor"/></svg><div><h3>This console host is not a supported Strix Halo Linux system</h3><p>Prerequisite and plan previews still render. Apply the reviewed commands on the target hosts.</p></div></div>` : ""}
    <section class="setup-section" id="setup-requirements">
      <div class="setup-section-head"><div><span class="step-number">1</span><span class="setup-kicker">Computer checks</span><h2>${requirementsReady ? "Both computers are ready" : "Make sure both computers are ready"}</h2>
        <p>${requirementsReady ? "Every required hardware, driver, and networking check passed. Open a computer only when you need the technical inventory." : "Fix the items called out below. Checks that already passed are tucked away."}</p></div>
        <span class="st ${requirementsReady ? "ok" : "warn"}">${requirementsReady ? "Complete" : "Needs attention"}</span></div>
      ${prereqCompareHtml()}
      ${installPlanHtml()}
    </section>
    ${setupPlanHtml()}
    ${rollbackHtml()}
  </div>`;
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

/* ---------------- Launch view ---------------- */

function launchNode(node, side) {
  if (!node) return `<section class="launch-node missing"><span class="launch-rank">${side}</span><h3>Second machine</h3><p>Status unavailable</p><span class="st warn">not reporting</span></section>`;
  const loaded = node.state === "loaded";
  const tone = loaded ? "ok" : node.state === "stopped" ? "off" : "warn";
  const state = loaded ? "Loaded" : node.state === "stopped" ? "Unloaded" : node.state.replace(/_/g, " ");
  const detail = node.competing_model && !loaded ? "Main model is using this host" : node.profile_name ? `${esc(node.profile_name)} profile` : "Profile unknown";
  const total = Number(node.ram_total_bytes || 0), used = Number(node.ram_used_bytes || 0), available = Number(node.ram_available_bytes || 0);
  const usedPct = total > 0 ? Math.min(100, Math.max(0, used * 100 / total)) : 0;
  const gib = (bytes) => (bytes / 1073741824).toFixed(1);
  const memory = total > 0 ? `<div class="launch-memory ${available / total < .1 ? "tight" : ""}"><div><span>Unified system RAM</span><b>${gib(used)} <small>/ ${gib(total)} GiB</small></b><em>${gib(available)} GiB available</em><small>Host-wide · KV cache ${gib(Number(node.kv_cache_bytes || 0))} GiB</small></div><i aria-hidden="true"><span style="width:${usedPct.toFixed(1)}%"></span></i></div>` : "";
  return `<section class="launch-node ${loaded ? "loaded" : ""}"><div class="launch-node-head"><div><span class="launch-rank">Rank ${node.rank >= 0 ? node.rank : "?"}</span><h3>${esc(node.hostname || side)}</h3></div><span class="st ${node.competing_model && !loaded ? "warn" : tone}">${esc(node.competing_model && !loaded ? "in use" : state)}</span></div><p>${detail}${node.pid ? ` · PID ${node.pid}` : ""}</p>${memory}</section>`;
}

function launchPermissionHelp(s) {
  const locked = (s.blockers || []).some((b) => b.includes("control is locked"));
  if (!locked) return "";
  const nodes = [s.local, s.peer].filter(Boolean);
  const needs = nodes.filter((n) => !n.control_enabled);
  const statusRows = nodes.map((n) => `<div class="launch-permission-host"><span><b>${esc(n.hostname)}</b><small>Rank ${n.rank} · ${esc(n.username)}</small></span><span class="st ${n.control_enabled ? "ok" : "warn"}">${n.control_enabled ? "Authorized" : "Needs permission"}</span></div>`).join("");
  const guides = needs.map((n) => {
    const u = n.username, rank = n.rank;
    const transportReport = [reportA(), reportB()].find((r) => r && r.hostname === n.hostname);
    const modelPeer = (transportReport && transportReport.peer) || "PEER_USB4_ADDRESS";
    const nix = `security.sudo.extraRules = [{\n  users = [ "${u}" ];\n  commands = [{\n    command = "/run/current-system/sw/bin/glm53-nhi-service-control";\n    options = [ "NOPASSWD" ];\n  }];\n}];`;
    const commands = [`status --user ${u}`, `transport-status --user ${u} --peer ${modelPeer}`, `configure --user ${u} --profile 1`, `configure --user ${u} --profile 2`, `configure --user ${u} --profile 3`, `load --user ${u}`, `unload --user ${u}`];
    const sudoers = commands.map((a) => `${u} ALL=(root) NOPASSWD: /usr/local/bin/ciru-strixlink model-node ${a}`).join("\n");
    const restart = n === s.local
      ? `# On ${n.hostname} (rank ${rank}); open this loopback console locally or through SSH\nciru-strixlink ui --peer ${modelPeer} --token-file TOKEN_FILE --model-url MODEL_FRONTEND_URL --model-control --model-rank ${rank}`
      : `# On ${n.hostname} (rank ${rank}); binds only to its USB4 interface\nciru-strixlink agent --token-file TOKEN_FILE --model-control --model-rank ${rank} --model-peer ${modelPeer}`;
    return `<details class="launch-permission-guide"><summary><span>${esc(n.hostname)} · rank ${rank}</span><span>Show setup</span></summary><div>
      <p><b>NixOS packaged deployment</b> — make sure <span class="mono">glm53-nhi-service-control</span> includes <span class="mono">probe</span>, <span class="mono">transport-status</span>, <span class="mono">start</span>, <span class="mono">stop</span>, and all three <span class="mono">context-*</span> actions. Add this rule to the host configuration, then rebuild:</p>
      <div class="launch-code"><pre>${esc(nix)}</pre><button class="copy" data-copy="${esc(nix)}">copy</button></div>
      <details class="launch-permission-alt"><summary>Other Linux distributions</summary><p>Install the current root-owned binary at <span class="mono">/usr/local/bin/ciru-strixlink</span>, validate these exact lines with <span class="mono">visudo</span>, then install them as a mode-0440 sudoers fragment:</p><div class="launch-code"><pre>${esc(sudoers)}</pre><button class="copy" data-copy="${esc(sudoers)}">copy</button></div></details>
      <p>Finally, restart this host's StrixLink process with the shared token, fixed rank, and fixed peer address:</p><div class="launch-code"><pre>${esc(restart)}</pre><button class="copy" data-copy="${esc(restart)}">copy</button></div>
    </div></details>`;
  }).join("");
  return `<section class="launch-permission"><div class="launch-permission-head"><div><span class="setup-kicker">Permission required</span><h2>Enable model control</h2><p>The page can read model state without administrator access. Loading and unloading use one fixed, allowlisted helper on each headless host—never a general-purpose shell.</p></div></div><div class="launch-permission-status">${statusRows}</div>${guides}</section>`;
}

function vLaunch() {
  const l = S.launch, s = l.status;
  if (!s) return `<div class="launch-page"><div class="view-head"><h1>Launch</h1><span class="sub">GLM 5.3 across both computers</span></div><div class="empty">${l.err ? esc(l.err) : "Reading model state…"}</div></div>`;
  const loaded = s.state === "loaded", partial = s.state === "partial";
  const tone = loaded ? "live" : s.state === "unloaded" ? "ok" : partial || s.state === "misconfigured" ? "bad" : "warn";
  const title = loaded ? "GLM 5.3 is loaded" : s.state === "unloaded" ? "Ready to load GLM 5.3" : s.summary;
  const action = loaded || partial ? "unload" : "load";
  const canAct = action === "load" ? s.can_load : s.can_unload;
  const profileLocked = loaded || partial || l.busy;
  const actionLabel = l.busy ? (action === "load" ? "Loading both ranks…" : "Unloading both ranks…") : action === "load" ? "Load on both machines" : "Unload from both machines";
  const speculation = s.model.speculation_known ? s.model.speculation : s.model.speculation || "State unknown";
  const prefixCache = !s.model.prefix_cache_known ? "State unknown" : s.model.prefix_cache ? "Enabled · memory + NVMe" : "Disabled · no disk tier";
  const fastLink = s.fast_link_ready ? `NHI · ${(s.fast_link_state || "ready").replace(/_/g, " ")}` : s.fast_link_state ? `${s.fast_link_state.replace(/_/g, " ")} · ${s.fast_link_summary || "check both machines"}` : "State unknown";
  const profiles = (s.profiles || []).map((p) => `<button class="launch-profile ${p.experimental ? "experimental" : ""}" data-set="launch.profile" data-val="${p.id}" aria-pressed="${Number(l.profile) === p.id}" ${profileLocked ? "disabled" : ""}>
    <span class="launch-profile-top"><b>${esc(p.name)}</b>${p.recommended ? `<span class="st ok">Recommended</span>` : p.experimental ? `<span class="st warn">Experimental</span>` : ""}</span>
    <span>${p.context_window.toLocaleString()} tokens</span><small>${Math.round(p.kv_cache_bytes / 1073741824)} GiB KV cache per machine · ${esc(p.note)}</small>
  </button>`).join("");
  const allBlockers = [...(s.blockers || [])];
  const blockers = allBlockers.map((b) => `<li>${esc(b)}</li>`).join("");
  return `<div class="launch-page">
    <div class="view-head"><h1>Launch</h1><span class="sub">configure, load, or unload one paired GLM deployment</span></div>
    <section class="launch-hero tone-${tone}">
      <div class="launch-hero-copy"><span class="setup-kicker">Current model</span><div class="launch-title"><i aria-hidden="true"></i><h2>${esc(title)}</h2></div><p>${esc(s.summary)}</p>
        <div class="launch-model-name">${esc(s.model.name.replace(/-/g, " "))}</div><div class="launch-model-meta"><span>${esc(s.model.topology)}</span><span>${esc(s.model.transport)}</span></div>
      </div>
      <div class="launch-pair">${launchNode(s.local, "This machine")}<div class="launch-join"><span>one model</span><i></i><b>TP2</b><i></i></div>${launchNode(s.peer, "Second machine")}</div>
    </section>
    ${l.err ? `<div class="banner bad"><div><h3>Launch action failed</h3><p>${esc(l.err)}</p></div></div>` : ""}
    ${launchPermissionHelp(s)}
    <div class="launch-grid">
      <section class="launch-panel"><div class="launch-panel-head"><div><span class="setup-kicker">1 · Memory profile</span><h2>Choose the context window</h2></div>${loaded ? `<span class="st live">Change after unload</span>` : ""}</div>
        <p class="launch-intro">The same profile is written to both ranks before loading. The validated 128K profile is the best default.</p><div class="launch-profiles" role="group" aria-label="Context profile">${profiles}</div>
      </section>
      <section class="launch-panel"><div class="launch-panel-head"><div><span class="setup-kicker">2 · Validated recipe</span><h2>Runtime parameters</h2></div><span class="st off">Fixed together</span></div>
        <p class="launch-intro">These values belong to the tested GLM build and stay locked so the two ranks cannot drift.</p>
        <dl class="launch-specs"><div><dt>Parallel layout</dt><dd>TP2 · PP1 · 1 rank per machine</dd></div><div><dt>Speculative decode</dt><dd>${esc(speculation)}</dd></div><div><dt>Concurrent requests</dt><dd>${s.model.max_sequences}</dd></div><div><dt>Batch token limit</dt><dd>${s.model.max_batched_tokens.toLocaleString()}</dd></div><div><dt>Prefix cache</dt><dd>${esc(prefixCache)}</dd></div><div><dt>Fast USB4 transport</dt><dd>${esc(fastLink)}</dd></div></dl>
      </section>
    </div>
    <section class="launch-action tone-${tone}"><div class="launch-action-copy"><span class="setup-kicker">3 · Paired action</span><h2>${action === "load" ? "Load the model" : "Unload the model"}</h2><p>${action === "load" ? `Applies the ${esc((s.profiles || []).find((p) => p.id === Number(l.profile))?.name || "selected")} profile to both machines, then starts rank 0 followed by rank 1.` : "Stops rank 1 and rank 0 as one operation. The lightweight API frontend can remain available while the weights are unloaded."}</p>
        ${blockers ? `<ul class="launch-blockers">${blockers}</ul>` : ""}
      </div><div class="launch-action-controls"><label class="launch-confirm"><input type="checkbox" data-set="launch.confirmed" ${l.confirmed ? "checked" : ""} ${!canAct || l.busy ? "disabled" : ""}><span>I understand this changes both machines together.</span></label>
        <button class="btn ${action === "load" ? "btn-primary" : "btn-danger"} launch-main-action" data-act="launch-action" data-launch-action="${action}" ${!canAct || !l.confirmed || l.busy ? "disabled" : ""}>${esc(actionLabel)}</button>
        <button class="btn btn-ghost" data-act="launch-refresh" ${l.busy ? "disabled" : ""}>Refresh model state</button></div>
    </section>
  </div>`;
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
    launch: S.launch.status ? S.launch.status.state.replace(/_/g, " ") + " · paired control" : "Reading model state",
    test: "Runs only when you start a test",
    diagnostics: p ? `pair identity ${p.pair_identity_valid ? "verified" : "not verified"}` : "Status unavailable",
  };
  $("#tabs-ctx").innerHTML = ctx[v] || "";
  $("#main").innerHTML = { pair: vPair, setup: vSetup, launch: vLaunch, test: vTest, diagnostics: vDiagnostics }[v]();
}

function setPath(obj, path, val) {
  const ks = path.split(".");
  let o = obj;
  for (let i = 0; i < ks.length - 1; i++) o = o[ks[i]];
  const last = ks[ks.length - 1];
  o[last] = typeof o[last] === "number" ? Number(val) : val;
}

function invalidateSetupPreview(path) {
  if (path.startsWith("setup.form.")) {
    S.setup.formTouched = true;
    S.setup.setupPlan = null;
  } else if (path.startsWith("setup.installOpts.")) {
    S.setup.installTouched = true;
    S.setup.installPlan = null;
  } else if (path.startsWith("setup.rb.")) {
    S.setup.rollbackTouched = true;
    S.setup.rollbackPlan = null;
  }
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
    if (seg.dataset.set.startsWith("launch.")) { S.launch.profileTouched = true; S.launch.confirmed = false; }
    invalidateSetupPreview(seg.dataset.set);
    renderView();
    return;
  }
  const el = ev.target.closest("[data-act]");
  if (!el || el.disabled) return;
  const act = el.dataset.act;
  try {
    if (act === "refresh") { await Promise.all([refreshAll(false), refreshLaunch(false)]); }
    else if (act === "setup-jump") {
      const target = document.getElementById(el.dataset.target);
      if (target) target.scrollIntoView({ behavior: "smooth", block: "start" });
    }
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
    else if (act === "launch-refresh") { await refreshLaunch(true); }
    else if (act === "launch-action") { await doLaunchAction(el.dataset.launchAction); }
  } catch (e) {
    announce("Action failed: " + e.message);
  }
});

document.addEventListener("change", (ev) => {
  const cb = ev.target.closest("[data-set]");
  if (cb && cb.type === "checkbox") {
    setPath(S, cb.dataset.set, cb.checked);
    invalidateSetupPreview(cb.dataset.set);
    renderView();
    return;
  }
  const inp = ev.target.closest("[data-inp]");
  if (inp) {
    setPath(S, inp.dataset.inp, inp.value);
    invalidateSetupPreview(inp.dataset.inp);
    renderView();
  }
});

document.addEventListener("keydown", (ev) => {
  if (ev.key === "Escape" && !$("#drawer").hidden) $("#drawer").hidden = true;
  if (ev.altKey && ["1", "2", "3", "4", "5"].includes(ev.key)) {
    S.view = ["pair", "setup", "launch", "test", "diagnostics"][Number(ev.key) - 1];
    renderView();
  }
});

async function doLaunchAction(action) {
  const l = S.launch;
  l.busy = true; l.err = null;
  renderView();
  try {
    const result = await api("/api/launch", { body: { action, profile: Number(l.profile), confirmed: true } });
    l.status = result.status;
    l.confirmed = false;
    announce(result.summary);
    await Promise.all([refreshModel(), refreshAll(true)]);
  } catch (e) {
    l.err = e.message + (e.detail ? " — " + e.detail : "");
    announce("Launch action failed: " + e.message);
  } finally {
    l.busy = false;
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
