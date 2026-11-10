/* ═══════════════════════════════════════════════════════════════════
   Admin-Aktionen (v6.0.0)
   ───────────────────────────────────────────────────────────────────
   Alles, was man bisher aus dem Terminal kopieren musste, läuft hier
   direkt auf der Server-Seite: prüfen, sichern, zurückspielen,
   neu bauen, per Git aktualisieren, neu starten.
   Jede Aktion liefert { ok, message, lines[] } zurück – die Oberfläche
   zeigt das Protokoll an. Nur Aktionen aus der festen Liste sind möglich,
   es wird nie ein frei übergebener Befehl ausgeführt.
═══════════════════════════════════════════════════════════════════ */
const fs = require("fs");
const path = require("path");
const os = require("os");
const { execFile } = require("child_process");
const { DatabaseSync } = require("node:sqlite");

module.exports = function registerAdmin(app, ctx) {
  const { one, all, run, getSettings, writeLog, DB_PATH, BAK_DIR, INST_DIR, PORT, VERSION, createBackup, closeDb, sshWatch, audit, secure } = ctx;
  const LOG = "admin";
  let busyAction = null;

  /* ── Hilfen ─────────────────────────────────────────────────────── */
  /* launchd startet den Server mit minimalem PATH → Ordner von node, Homebrew und /usr/local ergänzen */
  const toolPath = () => [path.dirname(process.execPath), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", process.env.PATH || ""].filter(Boolean).join(":");
  const underLaunchd = () => !!(process.env.XPC_SERVICE_NAME && process.env.XPC_SERVICE_NAME !== "0") || !!process.env.LAUNCH_JOB_LABEL;
  const sh = (cmd, args, opts = {}) => new Promise(resolve => {
    execFile(cmd, args, { cwd: opts.cwd || INST_DIR, timeout: opts.timeout || 180000, maxBuffer: 16 * 1024 * 1024,
      env: { ...process.env, PATH: toolPath(), GIT_TERMINAL_PROMPT: "0", GIT_SSH_COMMAND: "ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10", ...(opts.env || {}) } },
      (err, stdout, stderr) => resolve({ ok: !err, code: err?.code ?? 0, out: String(stdout || "").trim(), err: String(stderr || "").trim(), message: err ? (err.killed ? "Zeitüberschreitung" : err.message) : "" }));
  });
  const tail = (t, n = 12) => String(t || "").split("\n").filter(Boolean).slice(-n);
  const dt = ms => new Date(ms).toLocaleString("de-DE", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });
  const loop = async (p, timeout = 15000) => {                      // eigene API über localhost fragen
    try { const r = await fetch(`http://127.0.0.1:${PORT}${p}`, { signal: AbortSignal.timeout(timeout) }); return await r.json(); }
    catch (e) { return { error: e.message }; }
  };
  const scheduleExit = (ms = 700) => setTimeout(() => { writeLog("info", LOG, "Server beendet sich für den Neustart"); process.exit(0); }, ms);

  /* ── Aktionen ───────────────────────────────────────────────────── */
  const ACTIONS = [
    { id: "health", group: "Prüfen", title: "Selbstprüfung", help: "Prüft Datenbank, Datenplausibilität, Änderungsprotokoll, Speicherplatz, Sicherungen, Oberfläche, Plane, Tempo und SSH-Erfassung. Läuft auch automatisch beim Serverstart und alle 6 Stunden." },
    { id: "logs-server", group: "Prüfen", title: "Server-Fehlerlog anzeigen", help: "Die letzten Zeilen aus dem Fehlerprotokoll des Servers – ersetzt „tail -30 /tmp/zeitprotokoll-err.log“." },
    { id: "ssh-check", group: "Prüfen", title: "SSH-Sitzungen jetzt prüfen", help: "Misst sofort die Aktivität aller offenen SSH-Sitzungen und beendet verschwundene." },

    { id: "backup", group: "Daten", title: "Jetzt sichern", help: "Legt sofort eine Sicherung der Datenbank an (die letzten 30 bleiben erhalten)." },
    { id: "restore", group: "Daten", title: "Sicherung zurückspielen", help: "Ersetzt die Datenbank durch eine Sicherung. Vorher wird der aktuelle Stand automatisch gesichert, danach startet der Server neu.",
      needs: "backup", confirm: "Die aktuelle Datenbank wird durch die gewählte Sicherung ersetzt (der aktuelle Stand wird vorher gesichert). Fortfahren?", danger: true },
    { id: "vacuum", group: "Daten", title: "Datenbank verkleinern", help: "Räumt die Datenbankdatei auf (VACUUM). Kann bei großen Datenbanken einige Sekunden dauern." },
    { id: "clean-logs", group: "Daten", title: "Alte Systemprotokolle löschen", help: "Entfernt Einträge im System-Log, die älter als 30 Tage sind." },

    { id: "git-check", group: "Update", title: "Auf Updates prüfen (Git)", help: "Holt den Stand aus dem Forgejo-Repository und zeigt, was neu ist. Ändert nichts." },
    { id: "git-update", group: "Update", title: "Aus Git aktualisieren", help: "git pull, Abhängigkeiten installieren, Oberfläche bauen, Server neu starten. Lokale Änderungen werden nie überschrieben.",
      confirm: "Neuesten Stand aus Git holen, bauen und den Server neu starten?" },
    { id: "rebuild", group: "Update", title: "Oberfläche neu bauen", help: "Baut das Frontend neu (npm run build). Danach im Browser Cmd + R." },
    { id: "deps", group: "Update", title: "Abhängigkeiten installieren", help: "npm install für Server und Oberfläche – nach einem Update mit neuen Paketen." },

    { id: "restart", group: "Server", title: "Server neu starten", help: "Beendet den Server; der Startdienst (launchd) startet ihn sofort wieder. Dauert etwa 5 Sekunden.",
      confirm: "Server jetzt neu starten? Ein laufender Timer bleibt erhalten." },
  ];

  const RUN = {
    async health() {
      const checks = [], add = (name, ok, detail, warn) => checks.push({ name, ok: !!ok, warn: !!warn && ok, detail });
      add("Server", true, `Version ${VERSION} · läuft seit ${dt(Date.now() - process.uptime() * 1000)} · ${underLaunchd() ? "unter launchd (startet bei Absturz neu)" : "nicht unter launchd – ein Neustart per Knopf ist nicht möglich"}`, !underLaunchd());
      try { const r = one("PRAGMA quick_check"); const v = r && Object.values(r)[0]; add("Datenbank", v === "ok", v === "ok" ? "Integritätsprüfung bestanden" : String(v)); }
      catch (e) { add("Datenbank", false, e.message); }
      try { const fk = all("PRAGMA foreign_key_check"); add("Verknüpfungen", fk.length === 0, fk.length ? `${fk.length} Verweise auf gelöschte Datensätze (${[...new Set(fk.map(r => r.table))].join(", ")})` : "alle Verweise gültig", fk.length > 0 && fk.length < 20); }
      catch (e) { add("Verknüpfungen", true, "nicht prüfbar: " + e.message, true); }
      try {
        const neg = one("SELECT COUNT(*) AS n FROM time_entries WHERE end_time IS NOT NULL AND (end_time < start_time OR COALESCE(duration_seconds,0) < 0)")?.n || 0;
        const long = one("SELECT COUNT(*) AS n FROM time_entries WHERE COALESCE(duration_seconds,0) > 14*3600")?.n || 0;
        const ovl = one(`SELECT COUNT(*) AS n FROM time_entries a JOIN time_entries b ON a.id<b.id AND a.date=b.date AND a.start_time < b.end_time AND b.start_time < a.end_time
                         WHERE a.date >= date('now','-30 day') AND COALESCE(a.source,'') NOT IN ('tempo','tempo_csv') AND COALESCE(b.source,'') NOT IN ('tempo','tempo_csv')`)?.n || 0;
        const t = one("SELECT start_time FROM timer_state WHERE id=1 AND start_time IS NOT NULL");
        const runH = t ? (Date.now() - Date.parse(t.start_time)) / 3600e3 : 0;
        const parts = [neg ? `${neg} mit Ende vor Beginn` : "", long ? `${long} länger als 14 h` : "", ovl ? `${ovl} Überschneidungen (30 Tage)` : "", runH > 12 ? `Timer läuft seit ${Math.round(runH)} h` : ""].filter(Boolean);
        add("Buchungen plausibel", neg === 0, parts.join(" · ") || "keine Auffälligkeiten", !!parts.length);
      } catch (e) { add("Buchungen plausibel", false, e.message); }
      try { if (audit) { const v = audit.verify(); add("Änderungsprotokoll", v.ok && v.file_ok !== false, v.ok ? `${v.rows} Einträge, Prüfsummen-Kette intakt${v.file_ok === false ? " – audit.log weicht ab" : ""}` : `Kette unterbrochen bei #${v.broken_at} – wurde außerhalb verändert`); } }
      catch (e) { add("Änderungsprotokoll", false, e.message); }
      try { fs.accessSync(path.dirname(DB_PATH), fs.constants.W_OK); } catch (e) { add("Schreibrechte", false, `Datenverzeichnis nicht beschreibbar: ${path.dirname(DB_PATH)}`); }
      if (secure) try {
        const st = secure.status();
        add("Dateirechte", st.perms_ok, st.perms_ok ? "Datenbank und Sicherungen nur für dich lesbar" : `zu offen: ${st.perms_open.join(", ")} – wird stündlich korrigiert`, !st.perms_ok);
        add("Verschlüsselung (Datenträger)", st.disk.ok !== false, st.disk.detail, st.disk.ok !== true);
        const t = st.tls;
        add("Verbindungen (TLS)", !t.insecure && !t.file_error, t.insecure ? "Zertifikatsprüfung AUSGESCHALTET (tls_insecure) – unsicher" : t.file_error ? `CA-Datei nicht lesbar: ${t.file_error}` : `Zertifikate werden geprüft · ${t.system_cas} System-Zertifikate (${t.system_source || "Node"})${t.file ? ` + ${t.file_certs} aus ${t.file}` : ""} · Server nur lokal erreichbar (127.0.0.1)`);
      } catch (e) { add("Sicherheit", false, e.message); }
      try { const st = fs.statfsSync(path.dirname(DB_PATH)); const freeMb = Math.round(st.bavail * st.bsize / 1048576); add("Speicherplatz", freeMb > 500, `${freeMb} MB frei`, freeMb < 2000); } catch (e) { add("Speicherplatz", true, "nicht ermittelbar", true); }
      try {
        const files = fs.existsSync(BAK_DIR) ? fs.readdirSync(BAK_DIR).filter(f => f.endsWith(".db")).sort() : [];
        const last = files.length ? fs.statSync(path.join(BAK_DIR, files[files.length - 1])).mtimeMs : 0;
        const ageH = last ? (Date.now() - last) / 3600e3 : null;
        add("Sicherungen", ageH != null && ageH < 48, ageH == null ? "noch keine Sicherung" : `${files.length} Stück, letzte vor ${ageH < 1 ? "wenigen Minuten" : Math.round(ageH) + " h"}`, ageH != null && ageH > 30);
      } catch (e) { add("Sicherungen", false, e.message); }
      try { const st = fs.statSync(path.join(INST_DIR, "frontend/dist/index.html")); add("Oberfläche", true, `gebaut ${dt(st.mtimeMs)}`); } catch (_) { add("Oberfläche", false, "frontend/dist fehlt – „Oberfläche neu bauen“"); }
      const s = getSettings();
      if (s.plane_base_url && s.plane_api_key) { const r = await loop("/api/integrations/plane/status"); add("Plane", !r.error && r.configured !== false, r.error || r.message || "erreichbar"); }
      else add("Plane", true, "nicht eingerichtet", true);
      if (s.tempo_push_enabled === "1") { const r = await loop("/api/integrations/tempo-push/status", 25000); add("Tempo", r.ok, r.error || r.message); }
      else add("Tempo", true, "Übertragung ausgeschaltet", true);
      if (process.platform === "darwin") try {
        const w = await loop("/api/ssh-watch/status");
        const zshrc = path.join(os.homedir(), ".zshrc");
        const hooked = fs.existsSync(zshrc) && /ssh-log\.zsh/.test(fs.readFileSync(zshrc, "utf8"));
        const logOk = fs.existsSync(path.join(os.homedir(), ".zeitprotokoll/ssh_sessions.log"));
        add("SSH-Erfassung", hooked && w.enabled, `${hooked ? "zsh-Hook aktiv" : "zsh-Hook fehlt in ~/.zshrc (install.sh ausführen)"} · Aktivitätsmessung ${w.enabled ? "an" : "aus"} (Leerlauf ab ${w.idleMin} min)${logOk ? "" : " · noch kein Protokoll"}`, !logOk);
      } catch (e) { add("SSH-Erfassung", false, e.message); }
      const bad = checks.filter(c => !c.ok).length, warn = checks.filter(c => c.warn).length;
      return { ok: bad === 0, message: bad ? `${bad} Problem${bad === 1 ? "" : "e"} gefunden` : warn ? `Alles in Ordnung, ${warn} Hinweis${warn === 1 ? "" : "e"}` : "Alles in Ordnung", checks };
    },
    async "logs-server"() {
      const f = "/tmp/zeitprotokoll-err.log";
      if (!fs.existsSync(f)) return { ok: true, message: "Keine Fehlerdatei vorhanden – gut so", lines: [] };
      const txt = fs.readFileSync(f, "utf8"); return { ok: true, message: `Letzte Zeilen aus ${f}`, lines: tail(txt, 30) };
    },
    async "ssh-check"() {
      const r = await sshWatch.tick();
      if (r.enabled === false) return { ok: false, message: "Aktivitätsmessung ist ausgeschaltet" };
      const lines = (r.open || []).map(o => `${o.target}: ${o.process ? (o.active === false ? `inaktiv seit ${Math.round((o.idle_s || 0) / 60)} min` : "aktiv") : "Prozess nicht gefunden"}${o.closed ? " → beendet (" + o.closed + ")" : ""}`);
      return { ok: true, message: `${(r.open || []).length} offene Sitzung(en) geprüft`, lines };
    },
    async backup() { const d = createBackup("manual"); if (!d) return { ok: false, message: "Keine Datenbank gefunden" }; writeLog("success", "system", `Backup erstellt: ${path.basename(d)}`); return { ok: true, message: `Gesichert: ${path.basename(d)}` }; },
    async restore({ file }) {
      if (!/^zeitprotokoll-[\w.\-]+\.db$/.test(file || "")) return { ok: false, message: "Ungültige Sicherung" };
      const src = path.join(BAK_DIR, file);
      if (!fs.existsSync(src)) return { ok: false, message: "Sicherung nicht gefunden" };
      const tmp = DB_PATH + ".restore";
      fs.copyFileSync(src, tmp);
      try { const t = new DatabaseSync(tmp); const v = Object.values(t.prepare("PRAGMA integrity_check").get())[0]; t.close(); if (v !== "ok") throw new Error(v); }
      catch (e) { fs.rmSync(tmp, { force: true }); return { ok: false, message: "Sicherung ist beschädigt: " + e.message }; }
      const safety = createBackup("vor-wiederherstellung");
      try { db_checkpoint(); closeDb(); } catch (_) {}
      fs.renameSync(tmp, DB_PATH);
      for (const x of ["-wal", "-shm"]) fs.rmSync(DB_PATH + x, { force: true });
      writeLog("warn", LOG, `Datenbank aus ${file} wiederhergestellt (vorher gesichert: ${path.basename(safety || "")})`);
      scheduleExit();
      return { ok: true, restart: true, message: `Wiederhergestellt aus ${file}. Der Server startet neu – gleich Cmd+R drücken.`, lines: [`Vorheriger Stand gesichert als ${path.basename(safety || "")}`] };
    },
    async vacuum() {
      db_checkpoint(); const b = fs.statSync(DB_PATH).size;
      ctx.execSql("VACUUM"); db_checkpoint(); const a = fs.statSync(DB_PATH).size;
      return { ok: true, message: `Datenbank: ${(b / 1048576).toFixed(1)} MB → ${(a / 1048576).toFixed(1)} MB` };
    },
    async "clean-logs"() { const r = run("DELETE FROM system_logs WHERE timestamp < ?", new Date(Date.now() - 30 * 864e5).toISOString()); return { ok: true, message: `${r.changes} alte Einträge gelöscht` }; },

    async "git-check"() {
      if (!fs.existsSync(path.join(INST_DIR, ".git"))) return { ok: false, message: "Dieser Ordner ist kein Git-Repository (Installation per Archiv)." };
      const lines = [];
      const f = await sh("git", ["fetch", "--quiet"], { timeout: 60000 });
      if (!f.ok) return { ok: false, message: "git fetch fehlgeschlagen – SSH-Schlüssel oder Netzwerk?", lines: tail(f.err || f.message) };
      const cur = (await sh("git", ["log", "-1", "--format=%h %s (%cr)"])).out;
      const inc = await sh("git", ["log", "--format=%h %s (%an, %cr)", "HEAD..@{u}"]);
      const dirty = (await sh("git", ["status", "--porcelain", "--untracked-files=no"])).out;
      lines.push(`Aktueller Stand: ${cur}`);
      if (dirty) lines.push(`Lokale Änderungen vorhanden (${dirty.split("\n").length} Datei(en)) – „Aus Git aktualisieren“ würde abbrechen.`);
      if (!inc.ok) return { ok: false, message: "Kein Upstream-Branch gesetzt", lines };
      const list = inc.out.split("\n").filter(Boolean);
      return { ok: true, message: list.length ? `${list.length} neue Änderung${list.length === 1 ? "" : "en"} verfügbar` : "Aktuell – nichts Neues", lines: [...lines, ...list] };
    },
    async "git-update"() {
      const lines = [];
      if (!fs.existsSync(path.join(INST_DIR, ".git"))) return { ok: false, message: "Dieser Ordner ist kein Git-Repository." };
      const dirty = (await sh("git", ["status", "--porcelain", "--untracked-files=no"])).out;
      if (dirty) return { ok: false, message: "Lokale Änderungen vorhanden – nichts überschrieben. Bitte erst committen oder zurücklegen.", lines: dirty.split("\n").slice(0, 10) };
      const before = (await sh("git", ["rev-parse", "HEAD"])).out;
      const pull = await sh("git", ["pull", "--ff-only"], { timeout: 90000 });
      if (!pull.ok) return { ok: false, message: "git pull fehlgeschlagen", lines: tail(pull.err || pull.out || pull.message) };
      const after = (await sh("git", ["rev-parse", "HEAD"])).out;
      if (before === after) return { ok: true, message: "Schon aktuell – nichts zu tun" };
      lines.push(...tail((await sh("git", ["log", "--format=%h %s", `${before}..${after}`])).out, 15));
      for (const [d, label] of [["backend", "Server-Pakete"], ["frontend", "Oberflächen-Pakete"]]) {
        const r = await sh("npm", ["install", "--silent"], { cwd: path.join(INST_DIR, d) });
        if (!r.ok) return { ok: false, message: `npm install (${label}) fehlgeschlagen`, lines: [...lines, ...tail(r.err || r.out || r.message)] };
      }
      const b = await sh("npm", ["run", "build"], { cwd: path.join(INST_DIR, "frontend") });
      if (!b.ok) return { ok: false, message: "Oberfläche konnte nicht gebaut werden", lines: [...lines, ...tail(b.err || b.out || b.message)] };
      writeLog("success", LOG, `Aus Git aktualisiert (${before.slice(0, 7)} → ${after.slice(0, 7)})`);
      if (underLaunchd()) { scheduleExit(1500); return { ok: true, restart: true, message: "Aktualisiert – der Server startet neu. Gleich Cmd+R drücken.", lines }; }
      return { ok: true, message: "Aktualisiert. Server bitte von Hand neu starten (läuft nicht unter launchd).", lines };
    },
    async rebuild() {
      const b = await sh("npm", ["run", "build"], { cwd: path.join(INST_DIR, "frontend") });
      return b.ok ? { ok: true, message: "Oberfläche gebaut – im Browser Cmd + R", lines: tail(b.out, 4) } : { ok: false, message: "Build fehlgeschlagen", lines: tail(b.err || b.out || b.message) };
    },
    async deps() {
      const lines = [];
      for (const d of ["backend", "frontend"]) {
        const r = await sh("npm", ["install", "--silent"], { cwd: path.join(INST_DIR, d) });
        lines.push(`${d}: ${r.ok ? "ok" : "Fehler – " + tail(r.err || r.message, 2).join(" ")}`);
        if (!r.ok) return { ok: false, message: "npm install fehlgeschlagen", lines };
      }
      return { ok: true, message: "Pakete installiert", lines };
    },
    async restart() {
      if (!underLaunchd()) return { ok: false, message: "Der Server wurde nicht über den Startdienst gestartet – er würde nach dem Beenden nicht wieder hochkommen. Neustart bitte von Hand." };
      scheduleExit();
      return { ok: true, restart: true, message: "Server startet neu – in etwa 5 Sekunden wieder da (dann Cmd+R)." };
    },
  };
  const db_checkpoint = () => { try { run("PRAGMA wal_checkpoint(TRUNCATE)"); } catch (_) {} };

  /* ── Routen ─────────────────────────────────────────────────────── */
  app.get("/api/admin/actions", (_, res) => {
    const backups = fs.existsSync(BAK_DIR) ? fs.readdirSync(BAK_DIR).filter(f => f.endsWith(".db")).sort().reverse().slice(0, 30) : [];
    res.json({ actions: ACTIONS, backups, launchd: underLaunchd(), git: fs.existsSync(path.join(INST_DIR, ".git")), busy: busyAction });
  });
  app.post("/api/admin/actions/:id", async (req, res) => {
    const id = req.params.id;
    if (!ACTIONS.some(a => a.id === id) || !RUN[id]) return res.status(404).json({ error: "Unbekannte Aktion" });
    if (busyAction) return res.status(409).json({ error: `Es läuft gerade „${busyAction}“ – bitte kurz warten.` });
    busyAction = id;
    const t0 = Date.now();
    try {
      const out = await RUN[id](req.body || {});
      writeLog(out.ok ? "info" : "warn", LOG, `${id}: ${out.message || (out.ok ? "ok" : "Fehler")}`);
      res.json({ ...out, took_ms: Date.now() - t0 });
    } catch (e) {
      writeLog("error", LOG, `${id}: ${e.message}`);
      res.status(500).json({ ok: false, error: e.message, message: e.message });
    } finally { busyAction = null; }
  });
  /* ── Automatische Selbstprüfung: beim Start, alle 6 h, per Knopf; Ergebnis für Statusanzeige und Container-Proben ── */
  let lastCheck = null;
  async function selfCheck(trigger) {
    const t0 = Date.now();
    try { const r = await RUN.health(); lastCheck = { ...r, at: new Date().toISOString(), ms: Date.now() - t0, trigger }; }
    catch (e) { lastCheck = { ok: false, message: e.message, checks: [], at: new Date().toISOString(), trigger }; }
    try { run("INSERT OR REPLACE INTO settings (key,value) VALUES ('selfcheck_last',?)", JSON.stringify({ ok: lastCheck.ok, message: lastCheck.message, at: lastCheck.at })); } catch (_) {}
    const bad = (lastCheck.checks || []).filter(c => !c.ok).map(c => `${c.name}: ${c.detail}`);
    writeLog(lastCheck.ok ? "info" : "warn", LOG, `Selbstprüfung (${trigger}): ${lastCheck.message}${bad.length ? " – " + bad.join(" | ") : ""}`);
    return lastCheck;
  }
  setTimeout(() => selfCheck("Serverstart"), 8000);
  setInterval(() => selfCheck("automatisch"), 6 * 3600e3);
  app.get("/api/selfcheck", (_, res) => res.json(lastCheck || { pending: true }));
  app.post("/api/selfcheck/run", async (_, res) => res.json(await selfCheck("von Hand")));
  /* Für OpenShift/Kubernetes/systemd: Lebt der Prozess? Ist er bereit (Datenbank antwortet)? */
  app.get("/healthz", (_, res) => res.json({ ok: true, version: VERSION, uptime_s: Math.round(process.uptime()) }));
  app.get("/readyz", (_, res) => {
    try { one("SELECT 1 AS x"); } catch (e) { return res.status(503).json({ ok: false, error: "Datenbank: " + e.message }); }
    const dbBad = lastCheck && (lastCheck.checks || []).some(c => !c.ok && ["Datenbank", "Schreibrechte"].includes(c.name));
    res.status(dbBad ? 503 : 200).json({ ok: !dbBad, selfcheck: lastCheck ? { ok: lastCheck.ok, at: lastCheck.at, message: lastCheck.message } : null });
  });
  return { selfCheck };
};
