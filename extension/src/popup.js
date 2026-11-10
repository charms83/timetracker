const api_ = globalThis.browser ?? globalThis.chrome;
const $ = (id) => document.getElementById(id);
let state = null, ticket = null, paired = false, pairing = false, busy = false;

const send = async (m) => { try { return await api_.runtime.sendMessage(m); } catch (_) { return null; } };

function dur(iso) {
  const s = Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 1000));
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), p = (n) => String(n).padStart(2, "0");
  return h ? h + ":" + p(m) + ":" + p(s % 60) : m + ":" + p(s % 60);
}
function clock(iso) {
  return new Date(iso).toLocaleTimeString("de-DE", { hour: "2-digit", minute: "2-digit" });
}
function msg(text, kind) {
  const el = $("msg");
  el.hidden = !text; el.textContent = text || ""; el.className = "msg" + (kind ? " " + kind : "");
}

function renderState() {
  const el = $("state");
  const t = state && state.timer, p = state && state.pause;
  if (t && t.active) {
    el.textContent = "Läuft: " + (t.ticket || "Timer") + (t.title ? " " + t.title : "") + " · ";
    const sp = document.createElement("span");
    sp.className = "t"; sp.textContent = dur(t.start_time);
    el.appendChild(sp);
  } else if (p && p.active) {
    el.textContent = "Pause seit " + clock(p.start_time);
  } else {
    el.textContent = "Kein Timer aktiv";
  }
  $("pause").hidden = !(t && t.active);
  $("stop").hidden = !(t && t.active);
  $("resume").hidden = !(p && p.active);
  renderTicket();
}

function renderTicket() {
  const card = $("ticket");
  $("noTicket").hidden = !!ticket;
  card.hidden = !ticket;
  if (!ticket) return;
  $("tkKey").textContent = ticket.key;
  $("tkTitle").textContent = ticket.title || "";
  $("tkSys").textContent = "(" + (ticket.system === "plane" ? "Plane" : "Jira") + ")";
  const same = state && state.timer && state.timer.active && state.timer.ticket === ticket.key;
  const tr = $("track");
  tr.disabled = same || busy; tr.textContent = same ? "läuft schon" : "▶ Tracken";
  $("todo").disabled = busy;
}

function render() {
  $("unpaired").hidden = paired;
  $("paired").hidden = !paired;
  $("pair").hidden = pairing;
  $("waiting").hidden = !pairing;
  if (paired) renderState();
}

function apply(r) {
  if (!r) { msg("Interner Fehler.", "error"); return; }
  paired = r.paired !== false;
  if (r.state) state = r.state;
  if (r.offline) { msg(r.error, "error"); }
  else if (r.ok === false) msg(r.error, "error");
  else if (r.info) msg(r.info);
  else if (r.error) msg(r.error, "error");
  else msg("");
  if (!paired) state = null;
  render();
}

async function detectTicket() {
  ticket = null;
  try {
    const [tab] = await api_.tabs.query({ active: true, currentWindow: true });
    if (!tab || !tab.url) return;
    const hosts = state && state.hosts;
    const parsed = parseTicket(tab.url, tab.title || "", hosts);
    if (!parsed) return;
    if (!parsed.key && parsed.item) {   // Plane-Adresse ohne Nummer
      const r = await send({ type: "resolve", project: parsed.project, item: parsed.item });
      if (!r || !r.key) return;
      parsed.key = r.key; parsed.title = r.title || "";
    }
    ticket = parsed;
    try { // Titel aus der Seite, falls der Tab-Titel keinen liefert
      const r = await api_.tabs.sendMessage(tab.id, { type: "getTicket" });
      if (r && r.key === parsed.key && r.title) ticket.title = r.title;
    } catch (_) {}
  } catch (_) {}
}

async function load() {
  const ps = await send({ type: "pairStatus" });
  pairing = !!(ps && ps.pending);
  const r = await send({ type: "state" });
  apply(r);
  if (r && r.paired) { pairing = false; await detectTicket(); render(); }
  else if (ps && ps.error && !pairing) msg(ps.error, "error");
}

async function run(m) {
  if (busy) return;
  busy = true; render();
  const r = await send(m);
  busy = false;
  apply(r);
}

$("pair").onclick = async () => {
  msg("");
  const r = await send({ type: "pair" });
  if (r && r.error) { msg(r.error, "error"); return; }
  pairing = true; render();
};
$("pause").onclick = () => run({ type: "pause" });
$("resume").onclick = () => run({ type: "resume" });
$("stop").onclick = () => run({ type: "stop" });
$("track").onclick = () => run({ type: "start", key: ticket.key, title: ticket.title });
$("todo").onclick = () => run({ type: "todo", key: ticket.key, title: ticket.title || ticket.key });
$("openApp").onclick = (e) => { e.preventDefault(); send({ type: "openApp" }); window.close(); };

setInterval(() => { if (paired && state) renderState(); }, 1000);
setInterval(async () => {
  if (pairing) {
    const ps = await send({ type: "pairStatus" });
    if (ps && !ps.pending) { pairing = false; await load(); }
  } else if (paired && !busy) {
    apply(await send({ type: "state" }));
  }
}, 4000);
// beim Pairing schneller prüfen
setInterval(async () => {
  if (!pairing) return;
  const ps = await send({ type: "pairStatus" });
  if (ps && !ps.pending) { pairing = false; await load(); }
}, 1000);

load();
