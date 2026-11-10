/* ═══════════════════════════════════════════════════════════════════
   Änderungsprotokoll (Audit-Log) für Zeitbuchungen (v6.1.0)
   ───────────────────────────────────────────────────────────────────
   Wie bei Tempo: Buchungen dürfen geändert werden – aber jede Änderung
   hinterlässt eine Spur, die man nicht still entfernen kann.
     • SQLite-Trigger erfassen JEDE Neuanlage/Änderung/Löschung einer Buchung,
       egal über welchen Weg (Oberfläche, Schnellfenster, Import, Skript).
     • Vorher/Nachher als JSON, Zeitpunkt, Benutzer, auslösende Aktion.
     • Das Protokoll ist nur anhängbar: Ändern/Löschen blockiert die Datenbank.
     • Jede Zeile wird verkettet versiegelt (SHA-256 über die Vorzeile) und
       zusätzlich in eine Textdatei (audit.log) neben der Datenbank geschrieben.
       So fällt eine nachträgliche Manipulation bei der Prüfung auf.
   Ausgenommen: reine Abgleich-Spalten (Plane/Tempo-IDs) und Kopien aus Tempo.
═══════════════════════════════════════════════════════════════════ */
const crypto = require("crypto");
const fs = require("fs");
const path = require("path");
const os = require("os");

const COLS = ["project_id", "task_id", "start_time", "end_time", "duration_seconds", "comment", "billable", "date", "activity", "source"];

module.exports = function registerAudit(app, ctx) {
  const { one, all, run, execSql, writeLog, DB_PATH } = ctx;
  const FILE = path.join(path.dirname(DB_PATH), "audit.log");
  const json = pfx => `json_object(${COLS.map(c => `'${c}', ${pfx}.${c}`).join(", ")})`;
  const changed = COLS.map(c => `OLD.${c} IS NOT NEW.${c}`).join(" OR ");
  const notCopy = pfx => `COALESCE(${pfx}.source,'') NOT IN ('tempo','tempo_csv')`;
  const ins = (action, tbl, rowExpr, before, after) =>
    `INSERT INTO audit_log (tbl,row_id,action,before,after,actor,origin) VALUES ('${tbl}', ${rowExpr}, '${action}', ${before}, ${after},
       (SELECT actor FROM audit_ctx WHERE id=1), (SELECT origin FROM audit_ctx WHERE id=1));`;

  function init() {
    execSql(`
      CREATE TABLE IF NOT EXISTS audit_log (
        id INTEGER PRIMARY KEY AUTOINCREMENT, ts TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
        tbl TEXT NOT NULL, row_id INTEGER, action TEXT NOT NULL, before TEXT, after TEXT, actor TEXT, origin TEXT,
        prev_hash TEXT, hash TEXT);
      CREATE INDEX IF NOT EXISTS idx_audit_row ON audit_log(tbl,row_id);
      CREATE TABLE IF NOT EXISTS audit_ctx (id INTEGER PRIMARY KEY CHECK(id=1), actor TEXT, origin TEXT);
      INSERT OR IGNORE INTO audit_ctx (id,actor,origin) VALUES (1, NULL, 'system');

      DROP TRIGGER IF EXISTS audit_te_ins; DROP TRIGGER IF EXISTS audit_te_upd; DROP TRIGGER IF EXISTS audit_te_del;
      CREATE TRIGGER audit_te_ins AFTER INSERT ON time_entries WHEN ${notCopy("NEW")}
        BEGIN ${ins("create", "time_entries", "NEW.id", "NULL", json("NEW"))} END;
      CREATE TRIGGER audit_te_upd AFTER UPDATE ON time_entries WHEN (${changed}) AND (${notCopy("NEW")} OR ${notCopy("OLD")})
        BEGIN ${ins("update", "time_entries", "NEW.id", json("OLD"), json("NEW"))} END;
      CREATE TRIGGER audit_te_del AFTER DELETE ON time_entries WHEN ${notCopy("OLD")}
        BEGIN ${ins("delete", "time_entries", "OLD.id", json("OLD"), "NULL")} END;

      -- nur anhängen: Löschen verboten, Ändern nur zum Versiegeln (hash von NULL auf Wert)
      DROP TRIGGER IF EXISTS audit_no_delete; DROP TRIGGER IF EXISTS audit_no_update;
      CREATE TRIGGER audit_no_delete BEFORE DELETE ON audit_log BEGIN SELECT RAISE(ABORT, 'Änderungsprotokoll: Löschen nicht erlaubt'); END;
      CREATE TRIGGER audit_no_update BEFORE UPDATE ON audit_log
        WHEN OLD.hash IS NOT NULL OR NEW.ts IS NOT OLD.ts OR NEW.tbl IS NOT OLD.tbl OR NEW.row_id IS NOT OLD.row_id OR NEW.action IS NOT OLD.action
          OR NEW.before IS NOT OLD.before OR NEW.after IS NOT OLD.after OR NEW.actor IS NOT OLD.actor OR NEW.origin IS NOT OLD.origin
        BEGIN SELECT RAISE(ABORT, 'Änderungsprotokoll ist unveränderlich'); END;
    `);
    // Erste Inbetriebnahme: vorhandene Buchungen als Ausgangsstand festhalten
    if (!one("SELECT 1 FROM audit_log LIMIT 1")) {
      const n = one("SELECT COUNT(*) AS n FROM time_entries")?.n || 0;
      setCtx("system", "Protokoll eingeschaltet");
      run("INSERT INTO audit_log (tbl,row_id,action,after,actor,origin) VALUES ('system',NULL,'start',?, (SELECT actor FROM audit_ctx WHERE id=1),'Protokoll eingeschaltet')",
        JSON.stringify({ entries: n, sum_seconds: one("SELECT COALESCE(SUM(duration_seconds),0) AS s FROM time_entries")?.s || 0 }));
    }
    seal();
    setInterval(seal, 30 * 1000);
  }

  let defaultActor = null;
  try { defaultActor = os.userInfo().username; } catch (_) { defaultActor = "lokal"; }
  function setCtx(origin, note, actor) {
    run("UPDATE audit_ctx SET origin=?, actor=? WHERE id=1", note ? `${origin} – ${note}` : origin, actor || defaultActor);
  }

  /* Versiegeln: Hash-Kette + Kopie in audit.log */
  const rowHash = (prev, r) => crypto.createHash("sha256")
    .update([prev || "", r.id, r.ts, r.tbl, r.row_id ?? "", r.action, r.before ?? "", r.after ?? "", r.actor ?? "", r.origin ?? ""].join("␟")).digest("hex");
  function seal() {
    try {
      const open = all("SELECT * FROM audit_log WHERE hash IS NULL ORDER BY id");
      if (!open.length) return 0;
      let prev = one("SELECT hash FROM audit_log WHERE hash IS NOT NULL ORDER BY id DESC LIMIT 1")?.hash || null;
      const lines = [];
      for (const r of open) {
        const h = rowHash(prev, r);
        run("UPDATE audit_log SET prev_hash=?, hash=? WHERE id=? AND hash IS NULL", prev, h, r.id);
        lines.push(JSON.stringify({ ...r, prev_hash: prev, hash: h }));
        prev = h;
      }
      try { fs.appendFileSync(FILE, lines.join("\n") + "\n", { mode: 0o600 }); } catch (e) { writeLog("warn", "audit", "audit.log nicht schreibbar: " + e.message); }
      return open.length;
    } catch (e) { writeLog("warn", "audit", "Versiegeln: " + e.message); return 0; }
  }
  function verify() {
    seal();
    let prev = null, n = 0;
    for (const r of all("SELECT * FROM audit_log ORDER BY id")) {
      if (r.prev_hash !== prev || rowHash(prev, r) !== r.hash) return { ok: false, rows: n, broken_at: r.id, ts: r.ts };
      prev = r.hash; n++;
    }
    // Abgleich mit der Datei: letzter Hash muss dort stehen
    let fileOk = null;
    try { const t = fs.readFileSync(FILE, "utf8").trim().split("\n"); const last = t.length ? JSON.parse(t[t.length - 1]) : null; fileOk = !prev || (last && last.hash === prev); } catch (_) { fileOk = null; }
    return { ok: true, rows: n, last_hash: prev, file: FILE, file_ok: fileOk };
  }

  /* Middleware: welche Aktion hat die Änderung ausgelöst? */
  function middleware(req, res, next) {
    if (req.method !== "GET" && req.path.startsWith("/api/")) {
      try { setCtx(`${req.method} ${req.path}`, null, req.get("x-zp-user") || null); } catch (_) {}
      res.on("finish", () => { try { setCtx("system"); } catch (_) {} });
    }
    next();
  }

  const parse = v => { try { return v ? JSON.parse(v) : null; } catch (_) { return null; } };
  const diff = (b, a) => !b || !a ? null : Object.fromEntries(COLS.filter(c => b[c] !== a[c]).map(c => [c, [b[c], a[c]]]));
  const shape = r => { const b = parse(r.before), a = parse(r.after); return { ...r, before: b, after: a, changes: diff(b, a) }; };

  app.get("/api/audit", (req, res) => {
    const lim = Math.max(1, Math.min(500, parseInt(req.query.limit) || 100));
    const where = [], params = [];
    if (req.query.entry) { where.push("tbl='time_entries' AND row_id=?"); params.push(Number(req.query.entry)); }
    if (req.query.action) { where.push("action=?"); params.push(String(req.query.action)); }
    if (req.query.from) { where.push("ts>=?"); params.push(String(req.query.from)); }
    if (req.query.before) { where.push("id<?"); params.push(Number(req.query.before)); }
    const rows = all(`SELECT * FROM audit_log ${where.length ? "WHERE " + where.join(" AND ") : ""} ORDER BY id DESC LIMIT ${lim}`, ...params).map(shape);
    const projects = Object.fromEntries(all("SELECT id,name FROM projects").map(p => [p.id, p.name]));
    const tasks = Object.fromEntries(all("SELECT id,title,jira_ticket FROM tasks").map(t => [t.id, (t.jira_ticket ? t.jira_ticket + " " : "") + t.title]));
    res.json({ rows, projects, tasks, total: one("SELECT COUNT(*) AS n FROM audit_log")?.n || 0 });
  });
  app.get("/api/audit/verify", (_, res) => res.json(verify()));

  return { init, seal, verify, setCtx, middleware };
};
