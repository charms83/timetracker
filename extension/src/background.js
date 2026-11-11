// Zeitprotokoll – Hintergrundskript. Spricht mit lokaler App oder Team-Server.
const api_ = globalThis.browser ?? globalThis.chrome;
const DEFAULT_BASE = "http://127.0.0.1:3747";
const MSG_DOWN = "Zeitprotokoll nicht erreichbar – App oder Server-Adresse prüfen.";
const PAIR_MAX_MS = 3 * 60 * 1000;
const PAIR_EVERY_MS = 1500;

async function getStore(keys) {
  try { return await api_.storage.local.get(keys); } catch (_) { return {}; }
}
async function setStore(obj) {
  try { await api_.storage.local.set(obj); } catch (_) {}
}
async function removeStore(keys) {
  try { await api_.storage.local.remove(keys); } catch (_) {}
}

function normalizeBase(raw) {
  let s = String(raw || "").trim().replace(/\/+$/, "");
  if (!s) return DEFAULT_BASE;
  if (!/^https?:\/\//i.test(s)) s = "https://" + s;
  try {
    const u = new URL(s);
    if (u.username || u.password || u.search || u.hash) return null;
    if (u.protocol !== "http:" && u.protocol !== "https:") return null;
    return u.origin + (u.pathname === "/" ? "" : u.pathname.replace(/\/+$/, ""));
  } catch (_) { return null; }
}

async function getBase() {
  const s = await getStore(["baseUrl"]);
  return normalizeBase(s.baseUrl) || DEFAULT_BASE;
}

async function getToken() {
  const s = await getStore(["token"]);
  return s.token || null;
}
async function clearToken() { await removeStore(["token"]); }

async function ensureHost(base) {
  const origin = base.replace(/\/+$/, "") + "/*";
  try {
    if (api_.permissions && api_.permissions.contains) {
      const ok = await api_.permissions.contains({ origins: [origin] });
      if (ok) return true;
      return await api_.permissions.request({ origins: [origin] });
    }
  } catch (_) {}
  return true; // Manifest enthält 127.0.0.1; andere Origins brauchen optional_host_permissions
}

// Ergebnis: {ok, status, data} | {offline:true} | {unpaired:true}
async function api(path, { method = "GET", body, auth = true } = {}) {
  const base = await getBase();
  const headers = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (auth) {
    const token = await getToken();
    if (!token) return { unpaired: true };
    headers["X-ZP-Token"] = token;
  }
  let res;
  try {
    res = await fetch(base + "/api" + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  } catch (_) { return { offline: true }; }
  let data = null;
  try { data = await res.json(); } catch (_) {}
  if (res.status === 401 && auth && data && data.pair) { await clearToken(); return { unpaired: true }; }
  return { ok: res.ok, status: res.status, data };
}

// ---- Badge ----
async function setBadge(state) {
  try {
    const a = api_.action;
    let text = "", title = "Zeitprotokoll", color = "#0F752F";
    if (state && state.timer && state.timer.active) {
      text = "●";
      title = "Zeitprotokoll – läuft" + (state.timer.ticket ? ": " + state.timer.ticket : "");
    } else if (state && state.pause && state.pause.active) {
      text = "II"; color = "#B26A00";
      title = "Zeitprotokoll – Pause";
    }
    await a.setBadgeText({ text });
    await a.setBadgeBackgroundColor({ color });
    await a.setTitle({ title });
  } catch (_) {}
}

// Liefert {paired, offline?, error?, state?}
async function refresh() {
  const r = await api("/ext/state");
  if (r.unpaired) { await setBadge(null); return { paired: false }; }
  if (r.offline) { await setBadge(null); return { paired: true, offline: true, error: MSG_DOWN }; }
  if (!r.ok) { return { paired: true, error: (r.data && r.data.error) || "Fehler " + r.status }; }
  await setBadge(r.data);
  return { paired: true, state: r.data };
}

// Aktion ausführen und danach Zustand neu laden
async function act(path, body) {
  const r = await api(path, { method: "POST", body: body ?? {} });
  if (r.unpaired) { await setBadge(null); return { paired: false }; }
  if (r.offline) return { paired: true, offline: true, error: MSG_DOWN };
  const out = await refresh();
  if (r.ok) return { ...out, ok: true };
  const msg = (r.data && r.data.error) || "Fehler " + r.status;
  if (r.status === 409) return { ...out, ok: true, info: msg };
  return { ...out, ok: false, error: msg };
}

// ---- Pairing ----
let pairLoop = false;
function browserName() {
  const ua = navigator.userAgent;
  if (/Firefox\//.test(ua)) return "Firefox";
  if (/Edg\//.test(ua)) return "Edge";
  if (/Vivaldi\//.test(ua)) return "Vivaldi";
  if (navigator.brave) return "Brave";
  return "Chromium";
}
async function setPairing(p) {
  if (p) await setStore({ pairing: p }); else await removeStore(["pairing"]);
}
async function getPairing() {
  const s = await getStore(["pairing"]);
  return s.pairing || null;
}

async function startPairing() {
  if (pairLoop) return { pending: true };
  const base = await getBase();
  if (!(await ensureHost(base))) return { error: "Zugriff auf " + base + " nicht erlaubt – bitte in den Browser-Einstellungen freigeben." };
  const r = await api("/pair/start", {
    method: "POST", auth: false,
    body: { client: browserName(), ext_version: api_.runtime.getManifest().version },
  });
  if (r.offline) return { error: MSG_DOWN };
  if (!r.ok || !r.data || !r.data.nonce) return { error: (r.data && r.data.error) || "Verbinden fehlgeschlagen." };
  await setPairing({ nonce: r.data.nonce, started: Date.now() });
  if (r.data.approve_url) { try { await api_.tabs.create({ url: r.data.approve_url }); } catch (_) {} }
  runPairLoop();
  return { pending: true };
}

async function runPairLoop() {
  if (pairLoop) return;
  pairLoop = true;
  try {
    for (;;) {
      const p = await getPairing();
      if (!p) return;
      if (Date.now() - p.started > PAIR_MAX_MS) {
        await setPairing({ error: "Zeit abgelaufen. Bitte erneut verbinden." }); return;
      }
      const r = await api("/pair/poll?nonce=" + encodeURIComponent(p.nonce), { auth: false });
      if (r.offline) { await setPairing({ error: MSG_DOWN }); return; }
      if (r.status === 404) { await setPairing({ error: "Verbindung abgelehnt oder abgelaufen." }); return; }
      if (r.ok && r.data && r.data.token) {
        await setStore({ token: r.data.token });
        await setPairing(null);
        await refresh();
        return;
      }
      await new Promise((res) => setTimeout(res, PAIR_EVERY_MS));
    }
  } finally { pairLoop = false; }
}

async function pairStatus() {
  const p = await getPairing();
  if (!p) return { pending: false };
  if (p.error) return { pending: false, error: p.error };
  return { pending: true };
}

async function getConfig() {
  const base = await getBase();
  const token = await getToken();
  return { baseUrl: base, hasToken: !!token, defaultBase: DEFAULT_BASE };
}

async function saveConfig({ baseUrl, token }) {
  const next = normalizeBase(baseUrl);
  if (!next) return { error: "Adresse ungültig (https://… oder http://127.0.0.1:3747)." };
  if (!(await ensureHost(next))) return { error: "Browser hat den Zugriff auf diese Adresse nicht freigegeben." };
  const cur = await getBase();
  const patch = { baseUrl: next === DEFAULT_BASE ? "" : next };
  if (token !== undefined) {
    const t = String(token || "").trim();
    if (t) patch.token = t; else await clearToken();
  }
  if (next !== cur) {
    await clearToken();
    await setPairing(null);
  }
  await setStore(patch);
  if (patch.token) return { ...await getConfig(), ok: true, paired: true };
  return { ...await getConfig(), ok: true };
}

async function disconnect() {
  await clearToken();
  await setPairing(null);
  await setBadge(null);
  return { paired: false, ok: true };
}

// ---- Nachrichten (Popup / Content) ----
async function handle(m) {
  switch (m && m.type) {
    case "state": return refresh();
    case "resolve": { const r = await api(`/plane/resolve?project=${encodeURIComponent(m.project || "")}&item=${encodeURIComponent(m.item)}`); return r && r.ok ? r.data : null; }
    case "start": return act("/ticket/start", { key: m.key, title: m.title || "" });
    case "stop": return act("/timer/stop");
    case "pause": return act("/pause/start");
    case "resume": return act("/pause/stop");
    case "todo": return act("/todo", { title: m.title || m.ticket_title || m.key, jira_ticket: m.key, ticket_title: m.title || "", when: "today" });
    case "pair": await setPairing(null); return startPairing();
    case "pairStatus": return pairStatus();
    case "getConfig": return getConfig();
    case "saveConfig": return saveConfig(m);
    case "disconnect": return disconnect();
    case "openApp": try { await api_.tabs.create({ url: (await getBase()) + "/" }); } catch (_) {} return { ok: true };
  }
  return { error: "unbekannt" };
}
api_.runtime.onMessage.addListener((m, _s, send) => {
  handle(m).then(send, () => send({ error: "Interner Fehler" }));
  return true;
});

// ---- Alarm / Start ----
api_.alarms.create("zp-refresh", { periodInMinutes: 1 });
api_.alarms.onAlarm.addListener((a) => { if (a.name === "zp-refresh") refresh(); });
api_.runtime.onStartup.addListener(() => refresh());
api_.runtime.onInstalled.addListener(() => refresh());
// Laufendes Pairing nach Neustart des Service Workers fortsetzen
getPairing().then((p) => { if (p && p.nonce && !p.error) runPairLoop(); });
refresh();
