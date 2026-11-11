"use strict";
/* ════════════════════════════════════════════════════════════════
   Zeitprotokoll – Mac-Programm (Electron)
   • Hauptfenster: bestehende Oberfläche (http://localhost:3747)
   • Schnellfenster: klein, immer im Vordergrund, stündliche Erinnerung
   • Menüleiste: laufende Zeit + Schnellaktionen
   Der Server (~/zeitprotokoll/backend/server.js) läuft wie bisher per
   LaunchAgent; falls nicht erreichbar, startet das Programm ihn selbst.
═══════════════════════════════════════════════════════════════════ */
const { app, BrowserWindow, Tray, Menu, nativeImage, ipcMain, powerMonitor, shell, screen, session, Notification, safeStorage } = require("electron");
const path = require("path");
const fs   = require("fs");
const os   = require("os");
const { spawn, execSync } = require("child_process");

const HOME     = os.homedir();
const CFG_DIR  = path.join(HOME, ".zeitprotokoll");
const CFG_FILE = path.join(CFG_DIR, "desktop.json");
const DEVICE_TOKEN_FILE = path.join(CFG_DIR, "device-token.bin"); // Klartext nie in desktop.json – OS-Verschlüsselung (Keychain/DPAPI)
const LOCAL_API = "http://127.0.0.1:3747/api";
const LOCAL_WEB = "http://127.0.0.1:3747/";

// ── Einstellungen ────────────────────────────────────────────────
const DEFAULTS = {
  reminderMinutes: 60,     // spätestens alle X Minuten nach vorne holen
  idleLimitSeconds: 300,   // nicht erinnern, wenn länger als 5 Min keine Eingabe
  workFrom: 6, workTo: 20, // nur in diesem Zeitfenster erinnern
  weekdaysOnly: true,
  alwaysOnTop: false,     // true = Tracker auch über dem eigenen Hauptfenster; sonst „schlau“ (s. unten)
  showAtLogin: true,
  miniBounds: null,
  mainInBrowser: true,    // v6.30: „Zeitprotokoll öffnen“ öffnet den Standard-Browser statt eines eigenen großen Fensters
  // Team-Server (Go): mode "team" + serverUrl; Auth = Login → Server-Sitzung (Cookie)
  mode: "local",          // "local" | "team"
  serverUrl: "",          // nur https://… (http nur localhost mit allowHttpTeam)
  allowHttpTeam: false,   // Dev: http://localhost bzw. 127.0.0.1 erlauben
  serverCertPin: "",      // optional: SHA-256 des Server-Zertifikats (hex), Pinning gegen MITM
};
/** Optionaler Geräte-Token (Add-on); Normalfall Team = Session-Cookie. Persistenz safeStorage. */
let deviceTokenMem = "";
function deviceTokenOk(t) {
  return typeof t === "string" && (t === "" || /^zpdev_[A-Za-z0-9_-]{20,180}$/.test(t));
}
function setDeviceToken(raw) {
  const t = String(raw || "").trim();
  if (!deviceTokenOk(t)) return false;
  deviceTokenMem = t;
  try {
    fs.mkdirSync(CFG_DIR, { recursive: true, mode: 0o700 });
    if (!t) {
      try { fs.unlinkSync(DEVICE_TOKEN_FILE); } catch (_) {}
      return true;
    }
    let buf;
    if (safeStorage.isEncryptionAvailable()) buf = safeStorage.encryptString(t);
    else { log("safeStorage unavailable – Token-Datei nur mit 0600"); buf = Buffer.from(t, "utf8"); }
    fs.writeFileSync(DEVICE_TOKEN_FILE, buf, { mode: 0o600 });
    try { fs.chmodSync(DEVICE_TOKEN_FILE, 0o600); } catch (_) {}
  } catch (e) { log("device-token store", String(e && e.message || e)); }
  return true;
}
function loadDeviceTokenFromDisk() {
  try {
    if (!fs.existsSync(DEVICE_TOKEN_FILE)) { deviceTokenMem = ""; return; }
    const buf = fs.readFileSync(DEVICE_TOKEN_FILE);
    if (safeStorage.isEncryptionAvailable()) {
      try { deviceTokenMem = safeStorage.decryptString(buf); }
      catch (_) { // Fallback: ältere Klartext-Datei
        const s = buf.toString("utf8").trim();
        deviceTokenMem = deviceTokenOk(s) && s ? s : "";
        if (deviceTokenMem) setDeviceToken(deviceTokenMem); // neu verschlüsseln
      }
    } else {
      const s = buf.toString("utf8").trim();
      deviceTokenMem = deviceTokenOk(s) && s ? s : "";
    }
  } catch (_) { deviceTokenMem = ""; }
}
/** Einmal: Klartext aus alter desktop.json raus und in den Credential-Store. */
function migrateDeviceTokenFromCfg() {
  const legacy = typeof cfg.deviceToken === "string" ? cfg.deviceToken.trim() : "";
  if (legacy) {
    if (deviceTokenOk(legacy) && legacy) setDeviceToken(legacy);
    delete cfg.deviceToken;
    saveCfg();
    log("device-token migrated out of desktop.json");
  } else if ("deviceToken" in cfg) {
    delete cfg.deviceToken;
    saveCfg();
  }
}
function teamUrlOk(url) {
  try {
    const u = new URL(String(url || "").trim());
    if (u.protocol === "https:") return true;
    if (cfg.allowHttpTeam && u.protocol === "http:" && (u.hostname === "localhost" || u.hostname === "127.0.0.1")) return true;
  } catch (_) {}
  return false;
}
function baseUrls() {
  if (cfg.mode === "team" && cfg.serverUrl && teamUrlOk(cfg.serverUrl)) {
    const root = String(cfg.serverUrl).replace(/\/+$/, "");
    return { api: root + "/api", web: root + "/" };
  }
  return { api: LOCAL_API, web: LOCAL_WEB };
}
function isTeamMode() { return cfg.mode === "team" && teamUrlOk(cfg.serverUrl); }
function certPinNorm(s) {
  return String(s || "").toLowerCase().replace(/[^a-f0-9]/g, "");
}
function certMatchesPin(certificate) {
  const pin = certPinNorm(cfg.serverCertPin);
  if (!pin) return true;
  const fp = certPinNorm(certificate && (certificate.fingerprint256 || certificate.fingerprint));
  return fp.length >= 64 && fp.includes(pin);
}
const LOG_FILE = path.join(HOME, ".zeitprotokoll", "desktop.log");
function log(...a) {
  const line = `${new Date().toISOString()} ${a.map(x => typeof x === "string" ? x : JSON.stringify(x)).join(" ")}\n`;
  try { fs.mkdirSync(path.dirname(LOG_FILE), { recursive: true }); fs.appendFileSync(LOG_FILE, line); } catch (_) {}
}
process.on("uncaughtException", e => log("uncaughtException", e.stack || String(e)));
process.on("unhandledRejection", e => log("unhandledRejection", e?.stack || String(e)));
let cfg = { ...DEFAULTS };
try { cfg = { ...DEFAULTS, ...JSON.parse(fs.readFileSync(CFG_FILE, "utf8")) }; } catch (_) {}
const saveCfg = () => {
  try {
    fs.mkdirSync(CFG_DIR, { recursive: true, mode: 0o700 });
    const out = { ...cfg };
    delete out.deviceToken; // Klartext-Token nie in desktop.json
    fs.writeFileSync(CFG_FILE, JSON.stringify(out, null, 2), { mode: 0o600 });
    try { fs.chmodSync(CFG_FILE, 0o600); } catch (_) {}
  } catch (_) {}
};
// v6.18: „Immer im Vordergrund“ war Standard und hat genervt → einmalig aus (im Menü wieder einschaltbar)
if (!cfg.topSmart) { cfg.alwaysOnTop = false; cfg.topSmart = true; cfg.topMigrated = true; saveCfg(); }

// Nur eine Instanz
if (!app.requestSingleInstanceLock()) { app.quit(); }

let tray = null, mini = null, main = null, tab = null, lastShown = Date.now(), pendingReason = null;
let status = { timer: { active: false }, pause: { active: false } };
let quitting = false;

// ── API-Helfer ───────────────────────────────────────────────────
/** Team: Sitzungs-Cookie aus dem Login-Fenster (Server speichert die Session). Kein Geräte-Token nötig.
 *  Optionaler Geräte-Token nur für Add-on/curl ohne Browser-Login. */
async function api(method, p, body) {
  try {
    const { api: base } = baseUrls();
    const headers = { "Content-Type": "application/json", Accept: "application/json" };
    const url = base + p;
    const init = { method, headers, body: body ? JSON.stringify(body) : undefined };
    let r;
    if (isTeamMode()) {
      // Cookies der Electron-Session mitsenden; kein Origin (sonst CSRF-Schutz)
      if (deviceTokenMem) headers["X-ZP-Device-Token"] = deviceTokenMem; // optional
      const doFetch = typeof session.defaultSession.fetch === "function"
        ? session.defaultSession.fetch.bind(session.defaultSession)
        : fetch;
      r = await doFetch(url, init);
    } else {
      r = await fetch(url, init);
    }
    return await r.json();
  } catch (_) { return null; }
}
/** Ob für den Team-Server eine zp_session-Cookie existiert (nach Login im Fenster). */
async function hasTeamSession() {
  if (!isTeamMode()) return false;
  try {
    const origin = new URL(cfg.serverUrl).origin;
    const names = ["__Host-zp_session", "zp_session"];
    for (const name of names) {
      const cookies = await session.defaultSession.cookies.get({ url: origin, name });
      if (cookies.some(c => !!c.value)) return true;
    }
    return false;
  } catch (_) { return false; }
}

// ── Server sicherstellen (nur lokaler Node; Team-Modus: Go-Server muss erreichbar sein) ──
function findNode() {
  for (const p of ["/opt/homebrew/bin/node", "/usr/local/bin/node", "/usr/bin/node"]) if (fs.existsSync(p)) return p;
  try { return execSync("/bin/zsh -lc 'command -v node'", { encoding: "utf8" }).trim(); } catch (_) { return null; }
}
async function ensureServer() {
  if (isTeamMode()) {
    const h = await api("GET", "/health");
    return !!(h && (h.ok === true || h.status === "ok" || h.version || h.time));
  }
  if (await api("GET", "/health")) return true;
  const server = path.join(HOME, "zeitprotokoll", "backend", "server.js");
  const node = findNode();
  if (node && fs.existsSync(server)) {
    const child = spawn(node, [server], { cwd: path.dirname(server), detached: true, stdio: "ignore", env: { ...process.env, MODE: "standalone" } });
    child.unref();
    for (let i = 0; i < 20; i++) { await new Promise(r => setTimeout(r, 500)); if (await api("GET", "/health")) return true; }
  }
  return false;
}

// ── Fenster ──────────────────────────────────────────────────────
// Lasche: frei verschiebbar (auch auf andere Bildschirme), rastet am linken/rechten Rand ein.
// cfg.tab = { displayId, side: "left"|"right", rel: 0..1 (Höhe relativ zum Bildschirm) }
const TAB_W = 56, TAB_H = 104;
function tabDisplay() {
  const all = screen.getAllDisplays();
  return all.find(d => d.id === cfg.tab?.displayId) || screen.getPrimaryDisplay();
}
function tabSide() { return cfg.tab?.side === "left" ? "left" : "right"; }
function tabBounds() {
  const wa = tabDisplay().workArea;
  const rel = cfg.tab?.rel != null ? cfg.tab.rel : (cfg.tabY != null ? (cfg.tabY - wa.y) / Math.max(1, wa.height - TAB_H) : 0.32);
  const y = wa.y + Math.round(Math.min(1, Math.max(0, rel)) * (wa.height - TAB_H));
  return { x: tabSide() === "left" ? wa.x : wa.x + wa.width - TAB_W, y, width: TAB_W, height: TAB_H };
}
function placeTab() {
  if (!tab || tab.isDestroyed()) return;
  tab.setBounds(tabBounds());
  tab.webContents.send("tab:side", tabSide());
}
let dragStart = null;
ipcMain.on("tab:dragstart", () => { if (tab) { const b = tab.getBounds(); dragStart = { x: b.x, y: b.y }; } });
ipcMain.on("tab:drag", (_e, d) => {
  if (!tab || !dragStart || !d) return;
  tab.setBounds({ x: Math.round(dragStart.x + d.dx), y: Math.round(dragStart.y + d.dy), width: TAB_W, height: TAB_H });
});
ipcMain.on("tab:dragend", () => {
  if (!tab || !dragStart) return;
  dragStart = null;
  const b = tab.getBounds(), c = { x: b.x + TAB_W / 2, y: b.y + TAB_H / 2 };
  const disp = screen.getDisplayNearestPoint(c), wa = disp.workArea;
  cfg.tab = { displayId: disp.id, side: c.x < wa.x + wa.width / 2 ? "left" : "right",
              rel: Math.min(1, Math.max(0, (b.y - wa.y) / Math.max(1, wa.height - TAB_H))) };
  delete cfg.tabY;
  saveCfg();
  placeTab();
});

function createTab() {
  tab = new BrowserWindow({
    ...tabBounds(), show: false, frame: false, transparent: true, hasShadow: false,
    resizable: false, movable: true, fullscreenable: false, minimizable: false, maximizable: false,
    alwaysOnTop: true, skipTaskbar: true, focusable: true, acceptFirstMouse: true,
    webPreferences: { preload: path.join(__dirname, "preload-tab.js"), contextIsolation: true, nodeIntegration: false },
  });
  tab.setAlwaysOnTop(true, "floating");
  tab.setVisibleOnAllWorkspaces(true, { visibleOnFullScreen: true });
  tab.loadFile(path.join(__dirname, "ui", "tab.html"));
  tab.once("ready-to-show", () => { tab.showInactive(); tab.webContents.send("tab:side", tabSide()); });
  // Bildschirm an-/abgesteckt → Lasche wieder sichtbar platzieren
  for (const ev of ["display-removed", "display-added", "display-metrics-changed"]) screen.on(ev, () => setTimeout(placeTab, 500));
  tab.webContents.on("render-process-gone", (_e, d) => log("tab render-process-gone", d));
}
function setExpanded(v, reason) {
  if (v) {
    showMini(reason);
  } else {
    mini?.hide();
    lastShown = Date.now();
    tab?.webContents.send("tab:state", false);
  }
}
function peekTab(reason) {
  pendingReason = reason;
  lastShown = Date.now();
  tab?.webContents.send("tab:peek", reason);
}
ipcMain.on("tab:toggle", () => setExpanded(!(mini && mini.isVisible()), pendingReason));

function createMini() {
  const wa = screen.getPrimaryDisplay().workArea;
  const tb = tabBounds();
  // gespeicherte Position nur nutzen, wenn sie noch auf einem angeschlossenen Bildschirm liegt
  const onScreen = r => r && screen.getAllDisplays().some(d => { const a = d.workArea; return r.x < a.x + a.width && r.x + r.width > a.x && r.y < a.y + a.height && r.y + r.height > a.y; });
  const twa = tabDisplay().workArea;
  const b = (onScreen(cfg.miniBounds) && cfg.miniBounds) || { width: 380, height: 640,
    x: tabSide() === "left" ? tb.x + TAB_W + 8 : tb.x - 380 - 8, y: Math.max(twa.y + 10, Math.min(tb.y - 260, twa.y + twa.height - 650)) };
  mini = new BrowserWindow({
    ...b, minWidth: 320, minHeight: 420,
    show: false, title: "Zeiterfassung",
    titleBarStyle: "hiddenInset", trafficLightPosition: { x: 12, y: 15 },
    resizable: true, fullscreenable: false, minimizable: false, maximizable: false,
    backgroundColor: "#f5f5f7", acceptFirstMouse: true,
    alwaysOnTop: true,
    webPreferences: { preload: path.join(__dirname, "preload.js"), contextIsolation: true, nodeIntegration: false },
  });
  mini.setAlwaysOnTop(true, "floating");   // bleibt über Browser & Co. sichtbar (v6.20.1)
  mini.webContents.on("console-message", (_e, level, message, line, src) => { if (level >= 2) log("mini console", message, `${src}:${line}`); });
  mini.webContents.on("render-process-gone", (_e, d) => log("mini render-process-gone", d));
  mini.webContents.on("did-fail-load", (_e, code, desc, url) => log("mini did-fail-load", code, desc, url));
  mini.on("unresponsive", () => log("mini unresponsive"));
  mini.webContents.on("did-finish-load", () => log("mini loaded"));
  mini.loadFile(path.join(__dirname, "ui", "mini.html"));
  const remember = () => { if (mini && !mini.isDestroyed()) { cfg.miniBounds = mini.getBounds(); saveCfg(); } };
  mini.on("moved", remember); mini.on("resized", remember);
  mini.on("close", e => { if (!quitting) { e.preventDefault(); setExpanded(false); } });
  // Kein Umschalten beim Wegklicken mehr: das ließ den Tracker hinter den Browser fallen und schloss Auswahllisten
}

function showMini(reason) {
  if (!mini || mini.isDestroyed()) createMini();
  const doShow = () => {
    app.focus({ steal: true });
    mini.setAlwaysOnTop(!(main && !main.isDestroyed() && main.isFocused()) || cfg.alwaysOnTop, "floating");
    mini.show(); mini.focus();
    lastShown = Date.now();
    pendingReason = null;
    tab?.webContents.send("tab:state", true);
    if (reason) mini.webContents.send("reminder", reason);
  };
  if (mini.webContents.isLoading()) mini.webContents.once("did-finish-load", doShow); else doShow();
}

function createMain(view, forceWindow) {
  const { web: WEB } = baseUrls();
  const target = WEB + (typeof view === "string" && view ? "#" + view : "");
  // Team: immer im Electron-Fenster anmelden – Sitzungs-Cookie bleibt in der App (Server hält die Session).
  // Lokal: wahlweise Systembrowser.
  if (cfg.mainInBrowser && !forceWindow && !isTeamMode()) { shell.openExternal(target); return; }
  if (main && !main.isDestroyed()) {
    if (typeof view === "string" && view) main.webContents.executeJavaScript(`location.hash = ${JSON.stringify(view)}`).catch(() => {});
    main.show(); main.focus(); app.focus({ steal: true }); return;
  }
  main = new BrowserWindow({
    width: 1200, height: 820, minWidth: 900, minHeight: 600,
    titleBarStyle: "hiddenInset", trafficLightPosition: { x: 16, y: 18 },
    backgroundColor: "#ffffff", title: "Zeitprotokoll",
    webPreferences: {
      contextIsolation: true, nodeIntegration: false,
      webSecurity: true, allowRunningInsecureContent: false,
      preload: path.join(__dirname, "preload-main.js"),
    },
  });
  main.loadURL(target);
  main.webContents.on("did-finish-load", () => {
    // Mac-Anmutung: Systemschrift, Platz für Ampel-Knöpfe, Fenster ziehbar über Seitenleisten-Kopf
    main.webContents.insertCSS(`
      body, button, input, select, textarea { font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Helvetica Neue", sans-serif !important; }
      .topbar { -webkit-app-region: drag; }
      .topbar button, .topbar a, .topbar input { -webkit-app-region: no-drag; }
      main { -webkit-app-region: no-drag; }
    `);
  });
  // Team: kein HTTP-Downgrade (OIDC-IdP auf https bleibt erlaubt)
  main.webContents.on("will-navigate", (e, url) => {
    if (!isTeamMode()) return;
    try {
      const u = new URL(url);
      if (u.protocol === "http:" && !(cfg.allowHttpTeam && (u.hostname === "localhost" || u.hostname === "127.0.0.1"))) {
        e.preventDefault(); log("blocked insecure navigate", url);
      }
    } catch (_) { e.preventDefault(); }
  });
  main.webContents.setWindowOpenHandler(({ url }) => {
    shell.openExternal(url); return { action: "deny" };
  });
  main.on("closed", () => { main = null; });
  // „Schlau“: Tracker schwebt über anderen Programmen (Browser …), aber nicht über dem eigenen Hauptfenster.
  // Hauptfenster vorne → Tracker darf dahinter; anderes Programm vorne → Tracker wieder obenauf. Nichts wird geschlossen.
  main.on("focus", () => { if (!cfg.alwaysOnTop && mini && !mini.isDestroyed()) mini.setAlwaysOnTop(false); });
  main.on("blur", () => { if (mini && !mini.isDestroyed()) mini.setAlwaysOnTop(true, "floating"); });
  main.on("closed", () => { if (mini && !mini.isDestroyed()) mini.setAlwaysOnTop(true, "floating"); });
  app.focus({ steal: true });
}

// ── Menüleiste ───────────────────────────────────────────────────
const fmt = s => { s = Math.max(0, Math.floor(s)); const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60); return `${h}:${String(m).padStart(2, "0")}`; };

function trayIcon() {
  const p = path.join(__dirname, "ui", "trayTemplate.png");
  if (fs.existsSync(p)) { const i = nativeImage.createFromPath(p); i.setTemplateImage(true); return i; }
  return nativeImage.createEmpty();
}

/** Tray: Team-Server-URL setzen/ändern (kleines Eingabefenster). */
let teamUrlWin = null;
function editTeamServerUrl() {
  if (teamUrlWin && !teamUrlWin.isDestroyed()) { teamUrlWin.focus(); return; }
  const cur = String(cfg.serverUrl || "");
  teamUrlWin = new BrowserWindow({
    width: 520, height: 250, resizable: false, minimizable: false, maximizable: false,
    title: "Team-Server-Adresse", show: true,
    webPreferences: { nodeIntegration: true, contextIsolation: false },
  });
  const html = `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Team-Server</title></head>
<body style="font:13px -apple-system,system-ui,sans-serif;padding:18px;background:#f4f3ef;color:#1a1a1a;margin:0">
  <div style="font-weight:600;font-size:15px;margin-bottom:6px">Team-Server</div>
  <div style="color:#555;margin-bottom:12px">Adresse mit https://… – leer oder „Einzelplatz“ für den lokalen Tracker.</div>
  <input id="u" style="width:100%;padding:9px 10px;box-sizing:border-box;border:1px solid #ccc;border-radius:6px;font:13px ui-monospace,monospace" placeholder="https://zeit.firma.example" />
  <div style="margin-top:14px;display:flex;gap:8px;flex-wrap:wrap">
    <button id="ok" style="padding:7px 14px;border-radius:6px;border:0;background:#0F752F;color:#fff;font-weight:600;cursor:pointer">Übernehmen</button>
    <button id="local" style="padding:7px 14px;border-radius:6px;border:1px solid #ccc;background:#fff;cursor:pointer">Einzelplatz</button>
    <button id="cancel" style="padding:7px 14px;border-radius:6px;border:1px solid #ccc;background:#fff;cursor:pointer">Abbrechen</button>
  </div>
  <script>
    const { ipcRenderer } = require("electron");
    const u = document.getElementById("u");
    u.value = ${JSON.stringify(cur)};
    u.focus(); u.select();
    document.getElementById("ok").onclick = () => ipcRenderer.send("team-url-result", { mode: "team", serverUrl: u.value });
    document.getElementById("local").onclick = () => ipcRenderer.send("team-url-result", { mode: "local", serverUrl: "" });
    document.getElementById("cancel").onclick = () => ipcRenderer.send("team-url-result", null);
    u.onkeydown = (e) => { if (e.key === "Enter") document.getElementById("ok").click(); if (e.key === "Escape") document.getElementById("cancel").click(); };
  </script>
</body></html>`;
  teamUrlWin.loadURL("data:text/html;charset=utf-8," + encodeURIComponent(html));
  const onResult = (_e, data) => {
    ipcMain.removeListener("team-url-result", onResult);
    if (teamUrlWin && !teamUrlWin.isDestroyed()) teamUrlWin.close();
    teamUrlWin = null;
    if (!data) return;
    if (data.mode === "local") {
      cfg.mode = "local";
      saveCfg();
      refreshStatus();
      new Notification({ title: "Zeitprotokoll", body: "Wieder Einzelplatz (lokal)." }).show();
      return;
    }
    const u = String(data.serverUrl || "").trim().replace(/\/+$/, "");
    if (!u) {
      new Notification({ title: "Zeitprotokoll", body: "Bitte eine Adresse eingeben oder Einzelplatz wählen." }).show();
      return;
    }
    if (/^http:\/\/(localhost|127\.0\.0\.1)(:\d+)?$/i.test(u)) cfg.allowHttpTeam = true;
    cfg.mode = "team";
    if (!teamUrlOk(u)) {
      cfg.mode = isTeamMode() ? "team" : "local";
      new Notification({ title: "Zeitprotokoll", body: "Ungültige Adresse (https://… oder http://localhost)." }).show();
      return;
    }
    cfg.serverUrl = u;
    saveCfg();
    refreshStatus();
    new Notification({ title: "Zeitprotokoll", body: "Team-Server: " + u.replace(/^https?:\/\//, "") }).show();
  };
  ipcMain.on("team-url-result", onResult);
  teamUrlWin.on("closed", () => {
    ipcMain.removeListener("team-url-result", onResult);
    teamUrlWin = null;
  });
}

async function refreshStatus() {
  const [timer, pause] = await Promise.all([api("GET", "/timer/status"), api("GET", "/pause/status")]);
  status = { timer: timer || { active: false }, pause: pause || { active: false }, offline: !timer };
  if (!tray) return;
  let title = "";
  if (status.offline) title = " offline";
  else if (status.timer.active) title = " " + fmt((Date.now() - Date.parse(status.timer.start_time)) / 1000);
  else if (status.pause.active) title = " Pause " + fmt((Date.now() - Date.parse(status.pause.start_time)) / 1000);
  tray.setTitle(title, { fontType: "monospacedDigit" });
  tab?.webContents.send("tab:update", status);
  const t = status.timer;
  const label = t.active ? `${t.project?.name || "–"}${t.task ? " · " + (t.task.jira_ticket || t.task.title) : ""}` : (status.pause.active ? "Pause läuft" : "Kein Timer aktiv");
  tray.setToolTip("Zeitprotokoll – " + label);
  const teamLabel = isTeamMode()
    ? (`Team-Server · ${(cfg.serverUrl || "").replace(/^https?:\/\//, "").replace(/\/$/, "") || "–"}`)
    : "Einzelplatz (lokal)";
  tray.setContextMenu(Menu.buildFromTemplate([
    { label, enabled: false },
    { label: teamLabel, enabled: false },
    { label: "Team-Server-Adresse …", click: () => editTeamServerUrl() },
    { type: "separator" },
    { label: "Schnellfenster zeigen", accelerator: "CommandOrControl+Alt+Z", click: () => setExpanded(true, pendingReason) },
    { label: cfg.mainInBrowser ? "Zeitprotokoll im Browser öffnen" : "Hauptfenster öffnen", click: () => createMain() },
    ...(cfg.mainInBrowser ? [] : [{ label: "Im Browser öffnen", click: () => shell.openExternal(baseUrls().web) }]),
    { label: "Stundenzettel (Woche/Monat)", click: () => createMain("stundenzettel") },
    { label: "Einstellungen (Verbindungen)", click: () => createMain("einstellungen") },
    { label: "Browser-Add-ons einrichten …", click: async () => { const r = await installAllAddons(); if (!r.length) new Notification({ title: "Zeitprotokoll", body: "Alle Browser sind schon verbunden (Safari erkennt der Tracker selbst)." }).show(); } },
    { type: "separator" },
    ...(t.active ? [
      { label: "Pause", click: async () => { await api("POST", "/pause/start"); refreshStatus(); notifyMini(); } },
      { label: "Stopp", click: async () => { await api("POST", "/timer/stop"); refreshStatus(); notifyMini(); } },
    ] : status.pause.active ? [
      { label: "Pause beenden", click: async () => { await api("POST", "/pause/stop"); refreshStatus(); notifyMini(); } },
    ] : []),
    { type: "separator" },
    { label: "Großes Fenster statt Browser", type: "checkbox", checked: !cfg.mainInBrowser, click: m => { cfg.mainInBrowser = !m.checked; saveCfg(); if (cfg.mainInBrowser && main && !main.isDestroyed()) main.close(); refreshStatus(); } },
    ...(cfg.mainInBrowser ? [] : [{ label: "Schnellfenster auch über dem Hauptfenster", type: "checkbox", checked: cfg.alwaysOnTop, click: m => { cfg.alwaysOnTop = m.checked; saveCfg(); if (m.checked) mini?.setAlwaysOnTop(true, "floating"); } }]),
    { label: `Erinnerung alle ${cfg.reminderMinutes} Min`, submenu: [15, 30, 45, 60, 90, 120].map(n => ({ label: `${n} Minuten`, type: "radio", checked: cfg.reminderMinutes === n, click: () => { cfg.reminderMinutes = n; saveCfg(); refreshStatus(); } })) },
    { label: "Fehlende Stunden melden", type: "checkbox", checked: cfg.missingCheck !== false, click: m => { cfg.missingCheck = m.checked; saveCfg(); } },
    { label: "Beim Anmelden starten", type: "checkbox", checked: app.getLoginItemSettings().openAtLogin, click: m => app.setLoginItemSettings({ openAtLogin: m.checked }) },
    { type: "separator" },
    { label: "Zeitprotokoll beenden", accelerator: "CommandOrControl+Q", click: () => { quitting = true; app.quit(); } },
  ]));
}
const notifyMini = () => { if (mini && !mini.isDestroyed()) mini.webContents.send("refresh"); };

// ── Fehlende Stunden: einmal pro Arbeitstag (ab 9 Uhr) auf Lücken der letzten 7 Tage hinweisen
async function missingTick() {
  const now = new Date(), wd = now.getDay();
  if (cfg.missingCheck === false || wd === 0 || wd === 6 || now.getHours() < 9) return;
  const todayStr = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
  if (cfg.lastMissingCheck === todayStr) return;
  if (powerMonitor.getSystemIdleTime() > cfg.idleLimitSeconds) return;       // erst, wenn jemand am Rechner ist
  const r = await api("GET", "/missing-hours?days=7&min_h=0.5");
  cfg.lastMissingCheck = todayStr; saveCfg();
  if (!r || !r.days || !r.days.length || r.total_missing_h < 1) return;
  const reason = { kind: "missing", text: "Ein paar Zeiten sind noch offen – kurz nachtragen?" };
  if (mini && mini.isVisible()) mini.webContents.send("reminder", reason); else peekTab(reason);
}

// ── Stündliche Erinnerung ────────────────────────────────────────
function reminderTick() {
  const now = new Date();
  const h = now.getHours(), wd = now.getDay();
  if (cfg.weekdaysOnly && (wd === 0 || wd === 6)) return;
  if (h < cfg.workFrom || h >= cfg.workTo) return;
  if (powerMonitor.getSystemIdleTime() > cfg.idleLimitSeconds) return;          // du bist nicht am Mac
  if (mini && mini.isVisible() && mini.isFocused()) { lastShown = Date.now(); return; }
  if (Date.now() - lastShown < cfg.reminderMinutes * 60 * 1000) return;
  const t = status.timer;
  const reason = t.active
    ? { kind: "running", text: `Noch an ${t.project?.name || "–"}${t.task ? " · " + (t.task.jira_ticket || t.task.title) : ""}?` }
    : status.pause.active ? { kind: "pause", text: "Pause läuft noch – weiterarbeiten?" }
    : { kind: "idle", text: "Woran arbeitest du gerade?" };
  if (mini && mini.isVisible()) mini.webContents.send("reminder", reason);   // Panel schon offen -> direkt zeigen
  else peekTab(reason);                                                     // sonst nur kurz aufblinken, kein Fokus-Klau
}

// ── Meeting erkannt → nachfragen (nie automatisch buchen) ───────
let meetingAskedAt = 0, meetingTimerStarted = false;
function remind(reason) {
  lastShown = Date.now();
  if (mini && mini.isVisible()) mini.webContents.send("reminder", reason); else peekTab(reason);
}
async function onMeetingEvent(ev) {
  log("meeting", ev.type, ev.app);
  // Maßstab = lokales Mac-System: Mini fragt nach. Team: optional Hint + /meetings/now wie lokal.
  if (isTeamMode()) {
    if (!(await hasTeamSession()) && !deviceTokenMem) return;
    const day = new Date();
    const d = `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, "0")}-${String(day.getDate()).padStart(2, "0")}`;
    await api("POST", "/client/hints", { hints: [{ kind: "meeting", day: d, title: `${ev.app || "Meeting"} (${ev.type})`,
      dedupe_key: `meeting:${ev.type}:${ev.app || ""}:${d}:${Math.floor(Date.now() / 60000)}`,
      payload: { type: ev.type, app: ev.app } }] });
  }
  const now = await api("GET", "/meetings/now");
  if (now && now.ask === false) return;
  await refreshStatus();
  const t = status.timer;
  if (ev.type === "meeting-end") {
    if (meetingTimerStarted && t.active) remind({ kind: "meeting-end", app: ev.app, text: `${ev.app}-Meeting beendet – Timer stoppen?` });
    meetingTimerStarted = false; return;
  }
  if (Date.now() - meetingAskedAt < 5 * 60e3) return;                     // Programmstart + Meeting kurz hintereinander → nur einmal fragen
  meetingAskedAt = Date.now();
  const title = now?.event?.title || null;
  const label = ev.type === "app-start" ? `${ev.app} gestartet` : ev.type === "calendar" ? "Termin beginnt" : `${ev.app}-Meeting erkannt`;
  const pick = now?.rule ? { project_id: now.rule.project_id, task_id: now.rule.task_id, activity: now.rule.activity, project_name: now.rule.project_name }
    : now?.best ? { project_id: now.best.project_id, task_id: now.best.task_id, project_name: now.best.project_name, key: now.best.key } : null;
  remind({ kind: "meeting", app: ev.app, title, rule: pick, start_time: now?.event?.start_time || null, timerActive: !!t.active,
    text: `${label}${title ? ": „" + title + "“" : ""} – Timer starten?${pick?.project_name ? " (" + (pick.key ? pick.key + " · " : "") + pick.project_name + ")" : ""}` });
}
/* Termin beginnt (auch ohne Meeting-Programm) → einmal nachfragen, vorbelegt.
   Team wie lokal: /meetings/now am Go-Server (Kalender liefert die Mac-App). */
const calAsked = new Set();
async function calendarTick() {
  const now = await api("GET", "/meetings/now");
  if (!now || now.error) return;
  const ev = now?.event; if (!ev || now.ask === false || now.state || calAsked.has(ev.uid)) return;
  const since = Date.now() - Date.parse(ev.start_time);
  if (since < -2 * 60e3 || since > 10 * 60e3) return;                 // 2 min vorher bis 10 min nach Beginn
  calAsked.add(ev.uid);
  await refreshStatus();
  if (status.timer.active && status.timer.comment === ev.title) return;
  await onMeetingEvent({ type: "calendar", app: "Termin" });
}
setInterval(() => calendarTick().catch(() => {}), 60 * 1000);
// ── Anwesenheit → presence_min (Gaps/Heute/Tempo-Outbox). Team mit Session = wie lokal;
//    nur Geräte-Token: Hint, Server schreibt presence_min mit.
let screenLocked = false;
async function presenceTick() {
  const active = !screenLocked && powerMonitor.getSystemIdleTime() < cfg.idleLimitSeconds;
  if (!active) return;
  if (isTeamMode()) {
    if (await hasTeamSession()) {
      await api("POST", "/presence", { active: true });
      return;
    }
    if (!deviceTokenMem) return;
    const day = new Date();
    const d = `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, "0")}-${String(day.getDate()).padStart(2, "0")}`;
    const hm = `${String(day.getHours()).padStart(2, "0")}:${String(day.getMinutes()).padStart(2, "0")}`;
    await api("POST", "/client/hints", { hints: [{ kind: "presence", day: d, title: "am Mac", dedupe_key: `presence:${d}:${hm}`, payload: { active: true } }] });
    return;
  }
  await api("POST", "/presence", { active: true });
}
setInterval(() => presenceTick().catch(() => {}), 60 * 1000);
ipcMain.on("meeting:timerStarted", () => { meetingTimerStarted = true; });

// ── IPC vom Schnellfenster ───────────────────────────────────────
/* ── Ticket aus dem Browser übernehmen (v6.19) ────────────────────
   Liest NUR auf Anfrage (Tracker wird geöffnet/fokussiert) Adresse und Titel des
   aktiven Tabs der laufenden Browser. Behalten wird nur, was zur eingestellten
   Jira- oder Plane-Adresse gehört – alles andere wird sofort verworfen, nichts
   wird gespeichert. Abschaltbar: Einstellungen › Erkennung. macOS fragt einmal je
   Browser, ob Zeitprotokoll ihn „steuern“ darf (nur lesen des aktiven Tabs). */
const { execFile } = require("child_process");
const BROWSERS = [["Safari", "safari"], ["Google Chrome", "chromium"], ["Arc", "chromium"], ["Microsoft Edge", "chromium"], ["Brave Browser", "chromium"], ["Vivaldi", "chromium"], ["Chromium", "chromium"]];
// timeout: beim ersten Mal je Browser lange warten – sonst schließt macOS die Erlaubnis-Frage („… möchte Safari steuern“) nach 3 s wieder
const osa = (script, timeout = 3000) => new Promise(res => execFile("/usr/bin/osascript", ["-e", script], { timeout }, (err, out, eo) => res(err ? { err: String(eo || err.message).trim(), killed: !!err.killed } : { out: String(out).trim() })));
const ADDON_NAME = { "Firefox": "Firefox", "Microsoft Edge": "Edge", "Vivaldi": "Vivaldi", "Brave Browser": "Brave", "Google Chrome": "Chromium", "Arc": "Chromium", "Chromium": "Chromium" };
let pairedCache = { at: 0, names: [] };
async function pairedNames() {
  if (Date.now() - pairedCache.at < 60e3) return pairedCache.names;
  const l = await api("GET", "/pair/clients"); pairedCache = { at: Date.now(), names: Array.isArray(l) ? l.map(c => c.name) : [] };
  return pairedCache.names;
}
const KEY_RE = /\b([A-Z][A-Z0-9_]{1,15}-\d+)\b/;
function parseTicket(url, title, hosts, browser) {
  let u; try { u = new URL(url); } catch (_) { return null; }
  const sys = hosts.find(([, h]) => u.host === h)?.[0]; if (!sys) return null;
  let key = (u.pathname.match(/\/browse\/([A-Za-z][A-Za-z0-9_]*-\d+)/) || [])[1] || u.searchParams.get("selectedIssue")
    || (u.pathname.match(/\/(?:issues|work-items)\/([A-Za-z][A-Za-z0-9_]*-\d+)/) || [])[1] || (String(title).match(KEY_RE) || [])[1];
  if (!key && sys === "plane") {   // Plane ohne Nummer in der Adresse → über Zeitprotokoll auflösen
    const U = "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}";
    const project = u.searchParams.get("peekProjectId") || (u.pathname.match(new RegExp("/projects/(" + U + ")", "i")) || [])[1] || null;
    // Vorschau (halbe Ansicht): peekId/peekIssue – auch ohne Projekt in der Adresse (z. B. „Meine Work-Items“)
    const item = u.searchParams.get("peekId") || u.searchParams.get("peekIssue") || u.searchParams.get("peekIssueId")
      || (u.pathname.match(new RegExp("/(?:issues|work-items)/(" + U + ")", "i")) || [])[1];
    if (item && new RegExp("^" + U + "$", "i").test(item)) return { key: null, system: "plane", project, item, title: "", browser };
  }
  if (!key) return null; key = key.toUpperCase();
  const name = String(title || "").replace(/^\[[^\]]+\]\s*/, "").replace(new RegExp("^" + key + "[:\\s–-]*", "i"), "")
    .replace(/\s*[-|–]\s*(Jira|Plane|Beispiel[^-|–]*)\s*$/i, "").replace(/\s*[-|–]\s*(Jira|Plane)\s*$/i, "").trim();
  const titled = new RegExp("\\b" + key + "\\b", "i").test(String(title || "")) || /\/browse\//.test(u.pathname);   // Titel gehört sonst zur Seite (z. B. Board), nicht zum Ticket
  return { key, system: sys, title: titled && name && name.toUpperCase() !== key ? name : "", browser };
}
let btCache = { at: 0, data: [] };
let btRunning = null;
function browserTickets() { if (!btRunning) btRunning = browserTicketsRun().finally(() => { btRunning = null; }); return btRunning; }   // nie zwei Abfragen gleichzeitig (sonst doppelte macOS-Frage)
async function browserTicketsRun() {
  if (process.platform !== "darwin") return [];
  if (Date.now() - btCache.at < 2000) return btCache.data;
  const s = await api("GET", "/settings"); if (!s || s.browser_detect === "0") return [];
  const hosts = [["jira", s.jira_base_url], ["plane", s.plane_base_url]].map(([k, v]) => { try { return [k, new URL(v).host]; } catch (_) { return null; } }).filter(Boolean);
  if (!hosts.length) return [];
  // Laufende Browser über die Prozessliste bestimmen – NICHT per AppleScript: „application "X"“ für ein nicht
  // installiertes Programm öffnet sonst das macOS-Fenster „Wo befindet sich X?“ (v6.20.3)
  const procs = await new Promise(r => execFile("/bin/ps", ["-axo", "comm="], { timeout: 3000 }, (e, out) => r(e ? "" : String(out))));
  const running = BROWSERS.map(([n]) => n).filter(n => ["/Applications", path.join(HOME, "Applications")].some(d => fs.existsSync(path.join(d, n + ".app")))
    && procs.includes(`/${n}.app/Contents/MacOS/${n}`));
  const run = { out: running.join("\n") };
  const out = [], seen = new Set();
  const paired = await pairedNames();
  cfg.browserAsked = cfg.browserAsked || {};
  for (const name of String(run.out || "").split("\n").filter(Boolean)) {
    const kind = BROWSERS.find(([n]) => n === name)?.[1]; if (!kind) continue;
    if (paired.includes(ADDON_NAME[name])) continue;            // dort macht das Add-on das – keine macOS-Erlaubnis nötig
    if (cfg.browserAsked[name] === "denied") continue;           // einmal abgelehnt → nicht erneut fragen
    const q = kind === "safari"
      ? `tell application "${name}" to if (count of windows) > 0 then return (URL of current tab of front window) & linefeed & (name of current tab of front window)`
      : `tell application "${name}" to if (count of windows) > 0 then return (URL of active tab of front window) & linefeed & (title of active tab of front window)`;
    const first = !cfg.browserAsked[name];
    const r = await osa(q, first ? 120000 : 3000);
    if (first && !r.killed) { cfg.browserAsked[name] = r.err && /-1743|nicht erlaubt|not allowed/i.test(r.err) ? "denied" : "ok"; saveCfg(); }
    if (r.err) { if (/-1743|nicht erlaubt|not allowed/i.test(r.err)) log("browser-ticket: keine Erlaubnis für", name); continue; }
    const [url, ...t] = String(r.out || "").split("\n");
    const hit = parseTicket(url, t.join(" "), hosts, name);
    if (hit && !hit.key && hit.item) { const r2 = await api("GET", `/plane/resolve?project=${hit.project || ""}&item=${hit.item}`); if (r2?.key) { hit.key = r2.key; hit.title = r2.title || ""; } else continue; delete hit.project; delete hit.item; }
    if (hit && !seen.has(hit.key)) { seen.add(hit.key); out.push(hit); }
  }
  btCache = { at: Date.now(), data: out };
  return out;
}
ipcMain.handle("browser:tickets", () => browserTickets().catch(() => []));

/* ── Browser-Add-on: installierte Browser erkennen und Add-on anbieten (v6.20) ── */
const EXT_DIR = path.join(__dirname, "..", "extension", "dist");
const KNOWN_BROWSERS = [["Firefox", "firefox"], ["Google Chrome", "chromium"], ["Microsoft Edge", "chromium"], ["Brave Browser", "chromium"], ["Arc", "chromium"], ["Vivaldi", "chromium"], ["Safari", "safari"]];
function installedBrowsers() {
  return KNOWN_BROWSERS.map(([name, kind]) => ({ name, kind, installed: ["/Applications", path.join(HOME, "Applications")].some(d => fs.existsSync(path.join(d, name + ".app"))) })).filter(b => b.installed);
}
ipcMain.handle("ext:browsers", async () => { pairedCache.at = 0; const paired = await pairedNames(); return installedBrowsers().map(b => ({ ...b, connected: b.kind !== "safari" && paired.includes(ADDON_NAME[b.name]) })); });
async function installAddon(name) {
  const b = KNOWN_BROWSERS.find(([n]) => n === name); if (!b) return { error: "Unbekannter Browser" };
  if (b[1] === "firefox") {
    const signed = path.join(EXT_DIR, "zeitprotokoll-firefox-signed.xpi"), plain = path.join(EXT_DIR, "zeitprotokoll-firefox.xpi");
    if (fs.existsSync(signed)) { await new Promise(r => execFile("/usr/bin/open", ["-a", "Firefox", signed], () => r())); return { ok: true, text: "Firefox fragt jetzt „Zeitprotokoll hinzufügen?“ – bestätigen, dann im Add-on „Mit Zeitprotokoll verbinden“." }; }
    shell.showItemInFolder(plain);
    return { manual: true, text: "Noch nicht von Mozilla signiert – zum Testen: in Firefox about:debugging → „Dieser Firefox“ → „Temporäres Add-on laden …“ → die markierte Datei wählen. (Gilt bis Firefox neu startet.)" };
  }
  if (b[1] === "chromium") {
    shell.showItemInFolder(path.join(EXT_DIR, "chromium", "manifest.json"));
    return { manual: true, text: `In ${name}: Erweiterungen verwalten → „Entwicklermodus“ an → „Entpackte Erweiterung laden“ → den markierten Ordner „chromium“ wählen.` };
  }
  return { manual: true, text: "Safari braucht ein eigenes, von Apple signiertes Programm – hier bleibt das Erkennen über den Tracker (Einstellungen › Erkennung)." };
}
ipcMain.handle("ext:install", (_e, name) => installAddon(name));
// Alle gefundenen Browser ohne verbundenes Add-on auf einmal einrichten
async function installAllAddons() {
  pairedCache.at = 0; const paired = await pairedNames(), out = [];
  for (const b of installedBrowsers()) if (b.kind !== "safari" && !paired.includes(ADDON_NAME[b.name])) out.push({ name: b.name, ...(await installAddon(b.name)) });
  return out;
}
ipcMain.handle("ext:installAll", () => installAllAddons());
ipcMain.on("mini:hide", () => setExpanded(false));
ipcMain.on("mini:quit", () => { quitting = true; app.quit(); });
ipcMain.on("mini:openMain", (_e, view) => createMain(view));
ipcMain.on("mini:changed", () => refreshStatus());
// Mini/Tray: gleiche API wie Main (Team = Session-Cookie, lokal = Node :3747)
ipcMain.handle("api:call", async (_e, { method, path, body }) => {
  if (!method || typeof path !== "string" || !path.startsWith("/")) return { error: "ungültiger API-Aufruf" };
  return api(String(method).toUpperCase(), path, body);
});
ipcMain.handle("cfg:get", async () => {
  const c = { ...cfg };
  delete c.deviceToken;
  c.hasDeviceToken = !!deviceTokenMem; // optional, nicht nötig für normalen Team-Betrieb
  c.hasTeamSession = await hasTeamSession();
  if (deviceTokenMem) c.deviceTokenHint = deviceTokenMem.slice(0, 8) + "…";
  return c;
});
ipcMain.handle("cfg:setTeam", (_e, { mode, serverUrl, deviceToken, allowHttpTeam, serverCertPin }) => {
  if (mode === "local" || mode === "team") cfg.mode = mode;
  if (typeof allowHttpTeam === "boolean") cfg.allowHttpTeam = allowHttpTeam;
  if (typeof serverCertPin === "string") {
    const p = certPinNorm(serverCertPin);
    if (p && p.length !== 64) return { ok: false, error: "serverCertPin: SHA-256 als 64 Hex-Zeichen" };
    cfg.serverCertPin = p;
  }
  if (typeof serverUrl === "string") {
    const u = serverUrl.trim().replace(/\/+$/, "");
    if (u && cfg.mode === "team" && !teamUrlOk(u)) {
      return { ok: false, error: "Team-Server braucht https://… (http nur localhost mit allowHttpTeam)" };
    }
    cfg.serverUrl = u;
  }
  // deviceToken optional (Add-on/ohne Login); Normalfall = Anmelden im Fenster, Session auf dem Server
  if (typeof deviceToken === "string") {
    if (!setDeviceToken(deviceToken.trim())) return { ok: false, error: "deviceToken ungültig (erwartet zpdev_…)" };
  }
  if (cfg.mode === "team" && cfg.serverUrl && !teamUrlOk(cfg.serverUrl)) {
    cfg.mode = "local";
    return { ok: false, error: "Ungültige serverUrl – Modus zurück auf local" };
  }
  saveCfg();
  log("cfg team", cfg.mode, cfg.serverUrl ? "url=set" : "url=empty", deviceTokenMem ? "token=optional" : "auth=session", cfg.serverCertPin ? "pin=set" : "pin=off");
  return { ok: true, mode: cfg.mode, hasDeviceToken: !!deviceTokenMem };
});

/** TLS-Härtung ohne den Alltag zu blockieren (Firmen-CA/OIDC bleiben nutzbar). */
function installTeamTransportSecurity() {
  // Ungültige Zertifikate nie per Dialog durchwinken – System-/Firmen-CA bleibt über Chromium vertrauenswürdig
  app.on("certificate-error", (event, _wc, url, error, certificate, callback) => {
    event.preventDefault();
    log("tls rejected", error, url, certificate && certificate.subjectName);
    callback(false);
  });
  const ses = session.defaultSession;
  // Cert-Pin nur wenn gesetzt – sonst Standard-Prüfung (brauchbar mit Firmen-Proxy/CA)
  if (cfg.serverCertPin && typeof ses.setCertificateVerifyProc === "function") {
    ses.setCertificateVerifyProc((request, callback) => {
      if (typeof request.errorCode === "number" && request.errorCode !== 0) { callback(-2); return; }
      if (isTeamMode()) {
        try {
          if (request.hostname === new URL(cfg.serverUrl).hostname && !certMatchesPin(request.certificate)) {
            log("tls pin mismatch", request.hostname);
            callback(-2);
            return;
          }
        } catch (_) { callback(-2); return; }
      }
      callback(0);
    });
  }
  // Alltag: Benachrichtigungen ok; Kamera/Mikro/Ortung weiter aus
  const allowPerm = new Set(["notifications", "clipboard-sanitized-write"]);
  ses.setPermissionRequestHandler((_wc, permission, callback) => {
    callback(allowPerm.has(permission));
  });
  // Team: Klartext-HTTP blockieren (außer localhost-Dev); OIDC-IdP auf https bleibt erlaubt
  ses.webRequest.onBeforeRequest({ urls: ["http://*/*"] }, (details, cb) => {
    if (!isTeamMode()) { cb({}); return; }
    try {
      const parsed = new URL(details.url || "");
      const ok = cfg.allowHttpTeam && (parsed.hostname === "localhost" || parsed.hostname === "127.0.0.1");
      if (!ok) { log("blocked insecure request", details.url); cb({ cancel: true }); return; }
    } catch (_) { cb({ cancel: true }); return; }
    cb({});
  });
}

// ── Start ────────────────────────────────────────────────────────
app.on("second-instance", () => showMini());
app.on("activate", () => showMini());
app.on("before-quit", () => { quitting = true; });
app.on("window-all-closed", e => e.preventDefault());

app.whenReady().then(async () => {
  installTeamTransportSecurity();
  // Optionaler Geräte-Token (Legacy/Add-on): aus alter Config migrieren; Team-Normalfall = Login-Session
  migrateDeviceTokenFromCfg();
  loadDeviceTokenFromDisk();
  // Lokal: App-Schlüssel (X-ZP-Token). Team: Sitzungs-Cookie (Server) – kein Header nötig.
  const tokenFile = path.join(CFG_DIR, "app-token");
  let appTok = null; const readTok = () => { try { appTok = fs.readFileSync(tokenFile, "utf8").trim(); } catch (_) { appTok = null; } return appTok; };
  // Nur lokaler Node braucht X-ZP-Token; Team-Server nutzt HttpOnly-Cookie nach Login
  session.defaultSession.webRequest.onBeforeSendHeaders(
    { urls: ["http://127.0.0.1:3747/api/*", "http://localhost:3747/api/*"] },
    (d, cb) => {
      const t = appTok || readTok();
      if (t) d.requestHeaders["X-ZP-Token"] = t;
      cb({ requestHeaders: d.requestHeaders });
    });
  // Bestehende insecure Config: Team ohne https → warnen und deaktivieren
  if (cfg.mode === "team" && cfg.serverUrl && !teamUrlOk(cfg.serverUrl)) {
    log("team mode disabled: serverUrl muss https sein");
    cfg.mode = "local";
    saveCfg();
  }
  session.defaultSession.webRequest.onCompleted({ urls: ["http://127.0.0.1:3747/*"] }, d => { if (d.statusCode === 403) readTok(); });
  log("start", app.getVersion(), process.versions.electron, "mode", cfg.mode || "local");
  // App-Menü: ⌘Q beendet, Bearbeiten-Menü für Kopieren/Einfügen in Feldern
  Menu.setApplicationMenu(Menu.buildFromTemplate([
    { label: "Zeitprotokoll", submenu: [
      { label: "Schnellfenster zeigen", accelerator: "CommandOrControl+Alt+Z", click: () => setExpanded(true, pendingReason) },
      { label: "Zeitprotokoll öffnen", accelerator: "CommandOrControl+O", click: () => createMain() },
      { label: "Stundenzettel (Woche/Monat)", accelerator: "CommandOrControl+Alt+W", click: () => createMain("stundenzettel") },
      { type: "separator" },
      { label: "Fenster ausblenden", accelerator: "CommandOrControl+W", click: () => BrowserWindow.getFocusedWindow()?.close() },
      { label: "Zeitprotokoll beenden", accelerator: "CommandOrControl+Q", click: () => { quitting = true; app.quit(); } },
    ]},
    { role: "editMenu", label: "Bearbeiten" },
    { label: "Entwickler", submenu: [{ role: "reload", label: "Neu laden" }, { role: "toggleDevTools", label: "Entwicklertools" }] },
  ]));
  if (!cfg._loginSet) { app.setLoginItemSettings({ openAtLogin: true }); cfg._loginSet = true; saveCfg(); }
  const srv = await ensureServer();
  log("server erreichbar:", srv);
  tray = new Tray(trayIcon());
  tray.on("click", () => setExpanded(!(mini && mini.isVisible()), pendingReason));
  const { globalShortcut } = require("electron");
  globalShortcut.register("CommandOrControl+Alt+Z", () => showMini());
  createTab();
  await refreshStatus();
  createMini();
  if (cfg.showAtLogin) setExpanded(true);
  setInterval(refreshStatus, 15 * 1000);
  setInterval(reminderTick, 60 * 1000);
  setInterval(() => missingTick().catch(e => log("missingTick", e.message)), 5 * 60 * 1000);
  setTimeout(() => missingTick().catch(() => {}), 20 * 1000);
  try { require("./meeting-detect").create({ api, log, binDir: path.join(HOME, "Documents", "Zeitprotokoll", "bin"), onEvent: onMeetingEvent }).start(); } catch (e) { log("meeting-detect start", e.message); }
  // Team: SSH-Log → Server (lokal macht das Node). Tracking-Schalter vom Server → Flag-Datei für zsh.
  if (isTeamMode()) {
    try { require("./ssh-sync").create({ api, log }).start(); } catch (e) { log("ssh-sync start", e.message); }
  }
  powerMonitor.on("lock-screen", () => { screenLocked = true; });
  powerMonitor.on("suspend", () => { screenLocked = true; });
  powerMonitor.on("resume", () => { screenLocked = false; presenceTick().catch(() => {}); });
  powerMonitor.on("unlock-screen", () => { screenLocked = false; presenceTick().catch(() => {}); });
  powerMonitor.on("unlock-screen", () => setTimeout(() => { if (!status.timer.active) peekTab({ kind: "idle", text: "Willkommen zurück – woran arbeitest du?" }); }, 1500));
});
