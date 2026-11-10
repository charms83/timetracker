/* ═══════════════════════════════════════════════════════════════════
   SSH-Aktivität (v6.0.0)
   ───────────────────────────────────────────────────────────────────
   Das zsh-Protokoll (scripts/ssh-log.zsh) kennt nur Anfang und Ende einer
   Sitzung. Dieser Wächter ergänzt das, alle 30 Sekunden:
     • findet den laufenden ssh-Prozess jeder offenen Sitzung
       (Eltern-Prozess = die zsh, deren PID in der Sitzungs-ID steckt)
     • misst wie bei `w` die Leerlaufzeit am Terminal (letzte Eingabe)
     • zählt nur aktive Zeit → ein vergessenes Fenster verfälscht nichts
     • beendet Sitzungen sauber, deren Prozess ohne Ende-Zeile verschwunden
       ist („abgebrochen“) oder die lange ohne Eingabe offen blieben („vergessen“)
═══════════════════════════════════════════════════════════════════ */
const { execFile } = require("child_process");
const fs = require("fs");

const TICK_MS = 30 * 1000;

/* Ausgabe von `ps -axo pid=,ppid=,tty=,command=` zerlegen */
function parsePs(text) {
  const out = [];
  for (const line of String(text || "").split("\n")) {
    const m = /^\s*(\d+)\s+(\d+)\s+(\S+)\s+(.*)$/.exec(line);
    if (m) out.push({ pid: +m[1], ppid: +m[2], tty: m[3], command: m[4] });
  }
  return out;
}
/* Ist das ein ssh-Client (kein sshd, ssh-agent, scp …)? */
function isSshClient(command) {
  const first = String(command || "").trim().split(/\s+/)[0] || "";
  return /(^|\/)ssh$/.test(first);
}
/* Shell-PID aus der Sitzungs-ID „HOST-<shellpid>-<epoch>-<zufall>“ */
function shellPidOf(uid) {
  const m = /-(\d+)-\d{9,11}-\d+$/.exec(String(uid || ""));
  return m ? +m[1] : null;
}
/* Wie viele Sekunden zählen für diesen Tick? (Ruhezustand/Serverpause nicht hochrechnen) */
const tickGap = (lastSeenMs, nowMs) => lastSeenMs ? Math.max(0, Math.min(45, Math.round((nowMs - lastSeenMs) / 1000))) : 30;

module.exports = function registerSshWatch(app, ctx) {
  const { one, all, run, getSettings, writeLog, importSshLog } = ctx;
  const LOG = "ssh";
  let lastTick = null, lastResult = null, timer = null;

  function init() {
    for (const m of [
      "ALTER TABLE ssh_sessions ADD COLUMN active_seconds INTEGER",
      "ALTER TABLE ssh_sessions ADD COLUMN samples INTEGER DEFAULT 0",
      "ALTER TABLE ssh_sessions ADD COLUMN last_seen TEXT",
      "ALTER TABLE ssh_sessions ADD COLUMN last_active TEXT",
      "ALTER TABLE ssh_sessions ADD COLUMN end_reason TEXT",
    ]) { try { run(m); } catch (_) {} }
    timer = setInterval(() => tick().catch(e => writeLog("warn", LOG, "SSH-Wächter: " + e.message)), TICK_MS);
  }

  function cfg() {
    const s = getSettings();
    const num = (v, d, min, max) => { const n = parseFloat(v); return Number.isFinite(n) ? Math.max(min, Math.min(max, n)) : d; };
    return {
      enabled: s.ssh_watch_enabled !== "0",
      idleMin: num(s.ssh_idle_minutes, 10, 1, 240),          // so lange ohne Eingabe = inaktiv
      forgetH: num(s.ssh_forget_hours, 4, 0.5, 48),          // so lange inaktiv = Fenster vergessen → Sitzung beenden
    };
  }

  const psList = () => new Promise(resolve => {
    execFile("ps", ["-axo", "pid=,ppid=,tty=,command="], { timeout: 8000, maxBuffer: 8 * 1024 * 1024 }, (err, stdout) => resolve(err ? [] : parsePs(stdout)));
  });
  function ttyIdle(tty, nowMs) {                                // Millisekunden seit der letzten Eingabe am Terminal (atime)
    if (!tty || tty === "??" || tty === "?") return null;
    try { return Math.max(0, nowMs - fs.statSync("/dev/" + tty.replace(/^\/dev\//, "")).atimeMs); } catch (_) { return null; }
  }

  async function tick() {
    const c = cfg();
    if (!c.enabled) return { enabled: false };
    try { importSshLog(); } catch (_) {}                        // erst die Ende-Zeilen aus dem Protokoll holen
    const now = Date.now();
    const open = all("SELECT * FROM ssh_sessions WHERE end_time IS NULL AND start_time > ?", new Date(now - 3 * 864e5).toISOString());
    const result = { at: new Date(now).toISOString(), open: [] };
    if (!open.length) { lastTick = now; lastResult = result; return result; }
    const procs = (await psList()).filter(p => isSshClient(p.command));
    const byParent = new Map();
    for (const p of procs) (byParent.get(p.ppid) || byParent.set(p.ppid, []).get(p.ppid)).push(p);

    for (const s of open) {
      const shellPid = shellPidOf(s.session_uid);
      const proc = shellPid ? (byParent.get(shellPid) || [])[0] : null;
      const lastSeenMs = s.last_seen ? Date.parse(s.last_seen) : 0;
      const info = { id: s.id, target: s.target, shell_pid: shellPid, process: !!proc, idle_s: null, active: null };

      if (proc) {
        const idleMs = ttyIdle(proc.tty, now);                  // null = kein Terminal (z. B. ssh host befehl im Skript) → gilt als aktiv
        const active = idleMs == null ? true : idleMs <= c.idleMin * 60e3;
        info.idle_s = idleMs == null ? null : Math.round(idleMs / 1000); info.active = active;
        const gap = tickGap(lastSeenMs, now);
        const lastActive = idleMs == null ? now : now - idleMs;
        run(`UPDATE ssh_sessions SET last_seen=?, samples=COALESCE(samples,0)+1, active_seconds=COALESCE(active_seconds,0)+?,
             last_active=CASE WHEN ? THEN ? ELSE COALESCE(last_active, ?) END WHERE id=?`,
          new Date(now).toISOString(), active ? gap : 0, active ? 1 : 0, new Date(lastActive).toISOString(), new Date(lastActive).toISOString(), s.id);
        // vergessenes Fenster: lange ohne Eingabe → Sitzung beim letzten Lebenszeichen enden lassen
        if (!active && idleMs > c.forgetH * 3600e3) {
          const endMs = Math.max(Date.parse(s.start_time), now - idleMs + c.idleMin * 60e3);
          const dur = Math.round((endMs - Date.parse(s.start_time)) / 1000);
          run("UPDATE ssh_sessions SET end_time=?, duration_seconds=?, end_reason='vergessen' WHERE id=? AND end_time IS NULL", new Date(endMs).toISOString(), dur, s.id);
          writeLog("info", LOG, `SSH ${s.target}: Fenster vergessen – Sitzung endet bei der letzten Eingabe (${Math.round(dur / 60)} min)`);
          info.closed = "vergessen";
        }
      } else if (lastSeenMs && now - lastSeenMs > 90e3) {
        // Prozess ist weg, aber das Protokoll hat kein Ende (Terminal beendet, Absturz, Neustart)
        const endMs = Math.max(Date.parse(s.start_time), lastSeenMs);
        const dur = Math.round((endMs - Date.parse(s.start_time)) / 1000);
        run("UPDATE ssh_sessions SET end_time=?, duration_seconds=?, end_reason='abgebrochen' WHERE id=? AND end_time IS NULL", new Date(endMs).toISOString(), dur, s.id);
        writeLog("info", LOG, `SSH ${s.target}: Prozess ohne Ende-Zeile verschwunden – Sitzung bei letztem Lebenszeichen beendet (${Math.round(dur / 60)} min)`);
        info.closed = "abgebrochen";
      }
      result.open.push(info);
    }
    lastTick = now; lastResult = result;
    return result;
  }

  /* Wirksame Sekunden: gemessene Aktivzeit, sonst (ohne Messung) die Fensterzeit */
  const effectiveSeconds = (r, nowMs = Date.now(), maxOpenS = 24 * 3600) => {
    const open = !r.end_time;
    const age = (nowMs - Date.parse(r.start_time)) / 1000;
    if (r.samples > 0) return Math.max(0, Math.min(r.active_seconds || 0, open ? age : (r.duration_seconds || Infinity)));
    if (open) return age > maxOpenS ? 0 : Math.round(age);
    return r.duration_seconds || 0;
  };

  app.get("/api/ssh-watch/status", async (_, res) => {
    const c = cfg();
    res.json({ ...c, last_tick: lastTick ? new Date(lastTick).toISOString() : null, last: lastResult });
  });
  app.post("/api/ssh-watch/tick", async (_, res) => {                  // „Jetzt prüfen“ in der Admin-Ansicht
    try { res.json(await tick()); } catch (e) { res.status(500).json({ error: e.message }); }
  });

  return { init, tick, effectiveSeconds, _test: { parsePs, isSshClient, shellPidOf, tickGap } };
};
