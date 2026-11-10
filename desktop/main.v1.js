"use strict";
/* ════════════════════════════════════════════════════════════════
   Zeitprotokoll – Mac-Programm (Electron)
   • Hauptfenster: bestehende Oberfläche (http://localhost:3747)
   • Schnellfenster: klein, immer im Vordergrund, stündliche Erinnerung
   • Menüleiste: laufende Zeit + Schnellaktionen
   Der Server (~/zeitprotokoll/backend/server.js) läuft wie bisher per
   LaunchAgent; falls nicht erreichbar, startet das Programm ihn selbst.
═══════════════════════════════════════════════════════════════════ */
const { app, BrowserWindow, Tray, Menu, nativeImage, ipcMain, powerMonitor, shell, screen } = require("electron");
const path = require("path");
const fs   = require("fs");
const os   = require("os");
const { spawn, execSync } = require("child_process");

const API      = "http://127.0.0.1:3747/api";
const WEB      = "http://127.0.0.1:3747/";
const HOME     = os.homedir();
const CFG_FILE = path.join(HOME, ".zeitprotokoll", "desktop.json");

// ── Einstellungen ────────────────────────────────────────────────
const DEFAULTS = {
  reminderMinutes: 60,     // spätestens alle X Minuten nach vorne holen
  idleLimitSeconds: 300,   // nicht erinnern, wenn länger als 5 Min keine Eingabe
  workFrom: 6, workTo: 20, // nur in diesem Zeitfenster erinnern
  weekdaysOnly: true,
  alwaysOnTop: true,
  showAtLogin: true,
  miniBounds: null,
};
let cfg = { ...DEFAULTS };
try { cfg = { ...DEFAULTS, ...JSON.parse(fs.readFileSync(CFG_FILE, "utf8")) }; } catch (_) {}
const saveCfg = () => { try { fs.mkdirSync(path.dirname(CFG_FILE), { recursive: true }); fs.writeFileSync(CFG_FILE, JSON.stringify(cfg, null, 2)); } catch (_) {} };

// Nur eine Instanz
if (!app.requestSingleInstanceLock()) { app.quit(); }

let tray = null, mini = null, main = null, lastShown = Date.now(), status = { timer: { active: false }, pause: { active: false } };
let quitting = false;

// ── API-Helfer ───────────────────────────────────────────────────
async function api(method, p, body) {
  try {
    const r = await fetch(API + p, { method, headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
    return await r.json();
  } catch (_) { return null; }
}

// ── Server sicherstellen ─────────────────────────────────────────
function findNode() {
  for (const p of ["/opt/homebrew/bin/node", "/usr/local/bin/node", "/usr/bin/node"]) if (fs.existsSync(p)) return p;
  try { return execSync("/bin/zsh -lc 'command -v node'", { encoding: "utf8" }).trim(); } catch (_) { return null; }
}
async function ensureServer() {
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
function createMini() {
  const wa = screen.getPrimaryDisplay().workArea;
  const b = cfg.miniBounds || { width: 380, height: 640, x: wa.x + wa.width - 400, y: wa.y + 40 };
  mini = new BrowserWindow({
    ...b, minWidth: 320, minHeight: 420,
    show: false, frame: false, transparent: false, hasShadow: true, roundedCorners: true,
    resizable: true, fullscreenable: false, minimizable: false, maximizable: false,
    backgroundColor: "#f5f5f7", vibrancy: "sidebar", visualEffectState: "active",
    alwaysOnTop: cfg.alwaysOnTop, skipTaskbar: true,
    webPreferences: { preload: path.join(__dirname, "preload.js"), contextIsolation: true, nodeIntegration: false },
  });
  mini.setAlwaysOnTop(cfg.alwaysOnTop, "floating");
  mini.setVisibleOnAllWorkspaces(true, { visibleOnFullScreen: true });
  mini.loadFile(path.join(__dirname, "ui", "mini.html"));
  const remember = () => { if (mini && !mini.isDestroyed()) { cfg.miniBounds = mini.getBounds(); saveCfg(); } };
  mini.on("moved", remember); mini.on("resized", remember);
  mini.on("close", e => { if (!quitting) { e.preventDefault(); mini.hide(); } });
}

function showMini(reason) {
  if (!mini || mini.isDestroyed()) createMini();
  const doShow = () => {
    mini.showInactive(); mini.show(); mini.focus();
    lastShown = Date.now();
    if (reason) mini.webContents.send("reminder", reason);
  };
  if (mini.webContents.isLoading()) mini.webContents.once("did-finish-load", doShow); else doShow();
}

function createMain() {
  if (main && !main.isDestroyed()) { main.show(); main.focus(); return; }
  main = new BrowserWindow({
    width: 1200, height: 820, minWidth: 900, minHeight: 600,
    titleBarStyle: "hiddenInset", trafficLightPosition: { x: 16, y: 18 },
    backgroundColor: "#ffffff", title: "Zeitprotokoll",
    webPreferences: { contextIsolation: true, nodeIntegration: false },
  });
  main.loadURL(WEB);
  main.webContents.on("did-finish-load", () => {
    // Mac-Anmutung: Systemschrift, Platz für Ampel-Knöpfe, Fenster ziehbar über Seitenleisten-Kopf
    main.webContents.insertCSS(`
      body, button, input, select, textarea { font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Helvetica Neue", sans-serif !important; }
      aside { padding-top: 44px !important; -webkit-app-region: drag; }
      aside button, aside a, aside input { -webkit-app-region: no-drag; }
      main { -webkit-app-region: no-drag; }
    `);
  });
  main.webContents.setWindowOpenHandler(({ url }) => { shell.openExternal(url); return { action: "deny" }; });
  main.on("closed", () => { main = null; if (process.platform === "darwin") app.dock.hide(); });
  if (process.platform === "darwin") app.dock.show();
}

// ── Menüleiste ───────────────────────────────────────────────────
const fmt = s => { s = Math.max(0, Math.floor(s)); const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60); return `${h}:${String(m).padStart(2, "0")}`; };

function trayIcon() {
  const p = path.join(__dirname, "ui", "trayTemplate.png");
  if (fs.existsSync(p)) { const i = nativeImage.createFromPath(p); i.setTemplateImage(true); return i; }
  return nativeImage.createEmpty();
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
  const t = status.timer;
  const label = t.active ? `${t.project?.name || "–"}${t.task ? " · " + (t.task.jira_ticket || t.task.title) : ""}` : (status.pause.active ? "Pause läuft" : "Kein Timer aktiv");
  tray.setToolTip("Zeitprotokoll – " + label);
  tray.setContextMenu(Menu.buildFromTemplate([
    { label, enabled: false },
    { type: "separator" },
    { label: "Schnellfenster zeigen", accelerator: "CommandOrControl+Alt+Z", click: () => showMini() },
    { label: "Hauptfenster öffnen", click: createMain },
    { type: "separator" },
    ...(t.active ? [
      { label: "Pause", click: async () => { await api("POST", "/pause/start"); refreshStatus(); notifyMini(); } },
      { label: "Stopp", click: async () => { await api("POST", "/timer/stop"); refreshStatus(); notifyMini(); } },
    ] : status.pause.active ? [
      { label: "Pause beenden", click: async () => { await api("POST", "/pause/stop"); refreshStatus(); notifyMini(); } },
    ] : []),
    { type: "separator" },
    { label: "Immer im Vordergrund", type: "checkbox", checked: cfg.alwaysOnTop, click: m => { cfg.alwaysOnTop = m.checked; saveCfg(); mini?.setAlwaysOnTop(m.checked, "floating"); } },
    { label: `Erinnerung alle ${cfg.reminderMinutes} Min`, submenu: [15, 30, 45, 60, 90, 120].map(n => ({ label: `${n} Minuten`, type: "radio", checked: cfg.reminderMinutes === n, click: () => { cfg.reminderMinutes = n; saveCfg(); refreshStatus(); } })) },
    { label: "Beim Anmelden starten", type: "checkbox", checked: app.getLoginItemSettings().openAtLogin, click: m => app.setLoginItemSettings({ openAtLogin: m.checked }) },
    { type: "separator" },
    { label: "Zeitprotokoll beenden", role: "quit" },
  ]));
}
const notifyMini = () => { if (mini && !mini.isDestroyed()) mini.webContents.send("refresh"); };

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
  showMini(reason);
}

// ── IPC vom Schnellfenster ───────────────────────────────────────
ipcMain.on("mini:hide", () => { mini?.hide(); lastShown = Date.now(); });
ipcMain.on("mini:openMain", () => createMain());
ipcMain.on("mini:changed", () => refreshStatus());
ipcMain.handle("cfg:get", () => cfg);

// ── Start ────────────────────────────────────────────────────────
app.on("second-instance", () => showMini());
app.on("activate", () => showMini());
app.on("before-quit", () => { quitting = true; });
app.on("window-all-closed", e => e.preventDefault());

app.whenReady().then(async () => {
  if (process.platform === "darwin") app.dock.hide();   // läuft als Menüleisten-Programm
  if (!cfg._loginSet) { app.setLoginItemSettings({ openAtLogin: true }); cfg._loginSet = true; saveCfg(); }
  await ensureServer();
  tray = new Tray(trayIcon());
  tray.on("click", () => { if (mini?.isVisible()) mini.hide(); else showMini(); });
  const { globalShortcut } = require("electron");
  globalShortcut.register("CommandOrControl+Alt+Z", () => showMini());
  await refreshStatus();
  createMini();
  if (cfg.showAtLogin) showMini();
  setInterval(refreshStatus, 15 * 1000);
  setInterval(reminderTick, 60 * 1000);
  powerMonitor.on("unlock-screen", () => setTimeout(() => { if (!status.timer.active) showMini({ kind: "idle", text: "Willkommen zurück – woran arbeitest du?" }); }, 1500));
});
