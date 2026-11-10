/* Meeting- und Kalender-Erkennung (Mac, läuft im Hauptprozess der App).
   Datenschutz: Es wird NICHTS mitgehört und kein Mikrofon/keine Kamera abgefragt.
   Erkannt wird nur,
     • welche Meeting-Programme laufen bzw. gerade gestartet wurden (Prozessliste)
     • ob Zoom in einer Besprechung ist (Zoom startet dafür einen eigenen Prozess)
     • optional: ob ein Browser-Tab eine Meeting-Adresse hat (nur die Adresse wird lokal
       geprüft, gemeldet wird nur „Google Meet“, „Jitsi“ …)
   Daraus fragt die App: „Meeting erkannt – Timer starten?“ */
const { execFile } = require("child_process");
const fs = require("fs"), path = require("path"), os = require("os");

const MEETING_URL = /(\/\/meet\.example\.net\/[^?#\s]+|\/\/meet\.(?!google)[a-z0-9.-]+\/[A-Za-z0-9][^?#\s]*|meet\.google\.com\/[a-z]{3}-|zoom\.us\/(j|wc)\/|teams\.microsoft\.com\/(l\/meetup|v2|_#\/pre-join)|teams\.live\.com\/meet|meet\.jit\.si\/|jitsi|bigbluebutton|\/bbb\/|webex\.com\/(meet|wbxmjs)|whereby\.com\/)/i;
const APPS = [                                   // Programm → Muster in der Prozessliste
  ["Teams", /\/(Microsoft Teams[^/]*|MSTeams)\.app\/Contents\/MacOS\//],
  ["Zoom", /\/zoom\.us\.app\/Contents\/MacOS\/zoom\.us/],
  ["Webex", /\/Webex\.app\/Contents\/MacOS\/|Cisco Webex Meetings/],
  ["Slack", /\/Slack\.app\/Contents\/MacOS\/Slack(\s|$)/],
  ["Jitsi", /\/Jitsi Meet\.app\/Contents\/MacOS\//],
];

/* ps-Ausgabe → laufende Meeting-Programme + echte Besprechungen */
function scanPs(text) {
  const t = String(text || "");
  const running = APPS.filter(([, re]) => re.test(t)).map(([n]) => n);
  const inMeeting = [];
  if (/CptHost/.test(t)) inMeeting.push("Zoom");                     // nur während einer Zoom-Besprechung
  if (/\/Jitsi Meet\.app\/Contents\/MacOS\//.test(t)) inMeeting.push("Jitsi");   // die Jitsi-App öffnet man nur für ein Meeting
  return { running, inMeeting };
}
function browserApp(url) {
  const u = String(url || "");
  if (!MEETING_URL.test(u)) return null;
  if (/meet\.google/.test(u)) return "Google Meet"; if (/zoom\.us/.test(u)) return "Zoom"; if (/teams\./.test(u)) return "Teams";
  if (/jit/i.test(u) || /\/\/meet\.(?!google)/.test(u)) return "Jitsi"; if (/bigbluebutton|\/bbb\//i.test(u)) return "BigBlueButton"; if (/webex/.test(u)) return "Webex";
  return "Meeting im Browser";
}
/* icalBuddy-Ausgabe: "• Titel (Kalender)" / "    2026-10-27 at 09:00 - 10:00" / "    location: Ort" */
function parseIcalBuddy(text) {
  const out = []; let cur = null;
  const flush = () => { if (cur && cur.start && cur.end) out.push(cur); cur = null; };
  const iso = (d, t) => new Date(`${d}T${t.length === 5 ? t + ":00" : t}`).toISOString();
  for (const raw of String(text || "").split("\n")) {
    if (/^•\s/.test(raw)) {
      flush();
      const m = /^•\s+(.*?)(?:\s+\(([^()]*)\))?\s*$/.exec(raw);
      cur = { title: (m ? m[1] : raw.slice(2)).trim(), calendar: m && m[2] || null, location: null, allDay: false };
      continue;
    }
    if (!cur) continue;
    const l = raw.trim();
    let m = /^(\d{4}-\d{2}-\d{2}) at (\d{2}:\d{2}(?::\d{2})?) - (?:(\d{4}-\d{2}-\d{2}) at )?(\d{2}:\d{2}(?::\d{2})?)$/.exec(l);
    if (m) { cur.start = iso(m[1], m[2]); cur.end = iso(m[3] || m[1], m[4]); continue; }
    m = /^location:\s*(.*)$/.exec(l); if (m) cur.location = m[1];
  }
  flush(); return out;
}
/* Zustandswechsel → Ereignisse für die Nachfrage (rein, testbar) */
function diffState(prev, cur, first) {
  const ev = [];
  if (!first) for (const a of cur.running) if (!prev.running.includes(a)) ev.push({ type: "app-start", app: a });
  if (cur.meeting && !prev.meeting) ev.push({ type: "meeting-start", app: cur.meeting });
  if (!cur.meeting && prev.meeting) ev.push({ type: "meeting-end", app: prev.meeting });
  return ev;
}

function create({ api, log, binDir, onEvent }) {
  const diag = { cal: "unbekannt", browser: "aus" };
  let icalBin = null, lastCal = 0, cfg = { detect: true, calendar: true, browser: true }, busy = false;
  let state = { running: [], meeting: null }, first = true, goneTicks = 0;
  const sh = (cmd, args, timeout = 6000) => new Promise(res => execFile(cmd, args, { timeout, maxBuffer: 4 << 20 }, (e, so, se) => res({ e, out: so || "", err: se || "" })));

  // Aufräumen: frühere Version (6.0.0) hatte einen Mikrofon-Prüfer – entfernen
  try { fs.unlinkSync(path.join(binDir, "mic-check")); log("mic-check entfernt"); } catch (_) {}

  async function browserUrls(psText) {
    const urls = []; const jobs = [];
    // Nur Browser, die wirklich installiert sind UND laufen (Hauptprogramm, nicht irgendein Pfad mit dem Namen):
    // „tell application "X"“ für ein fehlendes Programm öffnet sonst das macOS-Fenster „Wo befindet sich X?“ (v6.20.3)
    const has = n => ["/Applications", path.join(os.homedir(), "Applications")].some(d => fs.existsSync(path.join(d, n + ".app")))
      && psText.includes(`/${n}.app/Contents/MacOS/${n}`);
    if (has("Google Chrome")) jobs.push('tell application "Google Chrome" to get URL of active tab of every window');
    if (has("Safari")) jobs.push('tell application "Safari" to get URL of current tab of every window');
    if (has("Microsoft Edge")) jobs.push('tell application "Microsoft Edge" to get URL of active tab of every window');
    if (has("Brave Browser")) jobs.push('tell application "Brave Browser" to get URL of active tab of every window');
    for (const j of jobs) {
      const r = await sh("/usr/bin/osascript", ["-e", j], 4000);
      if (r.e) { diag.browser = "kein Zugriff (Systemeinstellungen › Datenschutz › Automation)"; continue; }
      diag.browser = "ok"; urls.push(...r.out.split(/,\s*/));
    }
    if (!jobs.length) diag.browser = "kein unterstützter Browser offen (Chrome, Safari, Edge, Brave)";
    return urls;
  }

  async function detectTick() {
    const ps = await sh("/bin/ps", ["-axo", "command="], 8000);
    const sc = scanPs(ps.out);
    const signals = sc.inMeeting.map(a => ({ app: a }));
    if (cfg.browser) for (const u of await browserUrls(ps.out)) { const a = browserApp(u); if (a && !signals.some(s => s.app === a)) signals.push({ app: a }); }
    else diag.browser = "aus";
    // kurzes Verschwinden (Tab gewechselt) nicht sofort als Ende werten: 3 Takte Puffer
    let meeting = signals[0]?.app || null;
    if (!meeting && state.meeting && ++goneTicks < 3) meeting = state.meeting; else if (signals.length) goneTicks = 0;
    const cur = { running: sc.running, meeting };
    for (const ev of diffState(state, cur, first)) { try { onEvent && onEvent(ev); } catch (e) { log("meeting onEvent", e.message); } }
    state = cur; first = false;
    await api("POST", "/meetings/signal", { signals: meeting ? [{ app: meeting }] : [], diag: { ...diag, running: sc.running } });
  }

  async function ensureIcal() {
    if (icalBin) return icalBin;
    for (const p of ["/opt/homebrew/bin/icalBuddy", "/usr/local/bin/icalBuddy"]) if (fs.existsSync(p)) return (icalBin = p);
    return null;
  }
  const ymd = d => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  const NOACCESS = { 0: "noch nicht erteilt (macOS hat nicht gefragt)", 1: "durch Profil/Admin gesperrt", 2: "verweigert", 4: "nur Schreibzugriff" };
  async function calendarSync() {
    const from = new Date(Date.now() - 14 * 864e5), to = new Date(Date.now() + 14 * 864e5);   // 2 Wochen zurück (Buchung) und voraus (Planung)
    let events = null;
    // 1) direkt über EventKit (keine Zusatzsoftware; macOS fragt beim ersten Mal nach)
    const r = await sh("/usr/bin/osascript", ["-l", "JavaScript", path.join(__dirname, "calendar-read.js"), "14", "15"], 100000);
    let j = null; try { j = JSON.parse(r.out.trim()); } catch (_) {}
    if (j && j.events) { events = j.events; diag.cal = `ok (${events.length} Termine aus ${j.calendars} Kalendern)`; }
    else if (j && j.error === "noaccess") {
      diag.cal = `Kalenderzugriff ${NOACCESS[j.status] || "fehlt"} [Code ${j.status}] – Systemeinstellungen › Datenschutz & Sicherheit › Kalender › „Zeitprotokoll“ auf „Vollzugriff“. Fehlt der Eintrag: Terminal › bash ~/zeitprotokoll/desktop/kalender-freigeben.sh`;
    } else {
      diag.cal = "Kalender lesen fehlgeschlagen: " + (r.err || r.e?.message || r.out).slice(0, 160);
      // 2) Ausweg: icalBuddy, falls installiert
      const bin = await ensureIcal();
      if (bin) {
        const q = await sh(bin, ["-ea", "-n", "-iep", "title,datetime,location", "-po", "title,datetime,location", "-df", "%Y-%m-%d", "-tf", "%H:%M:%S", "-b", "•", "eventsFrom:" + ymd(from), "to:" + ymd(to)], 25000);
        if (!q.e) { events = parseIcalBuddy(q.out); diag.cal = "ok über icalBuddy (" + events.length + " Termine)"; }
      }
    }
    if (events) await api("POST", "/meetings/calendar", { from: ymd(from), to: ymd(to), events, diag: { ...diag } });
    else await api("POST", "/meetings/signal", { signals: state.meeting ? [{ app: state.meeting }] : [], diag: { ...diag, running: state.running } });
  }
  async function loop() {
    if (busy) return; busy = true;
    try {
      const c = await api("GET", "/meetings/config"); if (c) cfg = c;
      if (cfg.detect) await detectTick(); else { state = { running: [], meeting: null }; first = true; await api("POST", "/meetings/signal", { signals: [], diag: { ...diag, disabled: true } }); }
      if (cfg.calendar && (cfg.resync || Date.now() - lastCal > 10 * 60e3)) { lastCal = Date.now(); await calendarSync(); }
    } catch (e) { log("meeting-detect", e.message); } finally { busy = false; }
  }
  function start() { setTimeout(loop, 15000); setInterval(loop, 20000); }
  return { start, loop, state: () => state };
}
module.exports = { create, scanPs, browserApp, parseIcalBuddy, diffState, MEETING_URL };
