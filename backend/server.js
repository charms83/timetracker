"use strict";
try { require("dotenv").config(); } catch(_) {}  // optional – für Server-Modus
const express      = require("express");
const cors         = require("cors");
const path         = require("path");
const fs           = require("fs");
const { execSync, spawn } = require("child_process");
// node:sqlite ist in Node.js 22+ eingebaut – keine Installation nötig
const { DatabaseSync } = require("node:sqlite");

const VERSION = "6.36.0";
const MODE     = process.env.MODE || "standalone"; // standalone | server
const app      = express();
const PORT     = process.env.PORT    || 3747;
const DB_PATH  = process.env.DB_PATH || path.join(process.env.HOME||"/tmp", "Documents/Zeitprotokoll/zeitprotokoll.db");
const BAK_DIR  = path.join(path.dirname(DB_PATH), "backup");
const INST_DIR = path.join(__dirname, "..");

fs.mkdirSync(path.dirname(DB_PATH), { recursive: true });
fs.mkdirSync(BAK_DIR, { recursive: true });
const hooks = {};   // späte Verknüpfung zwischen Modulen (z. B. Projekt-Regeln)
const secure = require("./secure")({ getSettings: () => getSettings(), writeLog: (...a) => writeLog(...a), DB_PATH, BAK_DIR, saveSetting: (k, v) => run("INSERT OR REPLACE INTO settings (key,value) VALUES (?,?)", k, v) });

// Zugang zur lokalen Schnittstelle: eigene Oberfläche, Mac-App (Schlüssel), gekoppelte Add-ons – sonst niemand (pairing.js)
const pairing = require("./pairing")({ PORT, getSettings: () => getSettings(), saveSetting: (k, v) => run("INSERT OR REPLACE INTO settings (key,value) VALUES (?,?)", k, v), writeLog: (...a) => writeLog(...a) });
// Messung (v6.32.1): langsame Anfragen und blockierter Server landen im Protokoll (Kategorie „tempo-messung“ → „leistung“)
const perfSeen = new Map();
const perfLog = (key, msg) => { const t = perfSeen.get(key) || 0; if (Date.now() - t < 60e3) return; perfSeen.set(key, Date.now()); try { writeLog("warn", "leistung", msg); } catch (_) {} };
app.use((req, res, next) => {
  const t0 = process.hrtime.bigint();
  res.on("finish", () => { const ms = Number(process.hrtime.bigint() - t0) / 1e6; if (ms > 700 && req.path.startsWith("/api/")) perfLog(req.method + req.path, `Langsam: ${req.method} ${req.path} ${Math.round(ms)} ms`); });
  next();
});
{ let last = Date.now(); setInterval(() => { const lag = Date.now() - last - 1000; last = Date.now(); if (lag > 300) perfLog("lag", `Server war ${lag} ms blockiert`); }, 1000).unref?.(); }
app.use(cors(pairing.corsOptions));
app.use(pairing.guard);
app.use(express.json());
// Änderungsprotokoll: merkt sich, welche Aktion eine Buchung ändert (Modul audit.js, unten registriert)
app.use((req, res, next) => { try { return audit.middleware(req, res, next); } catch (_) { return next(); } });

/* ═══════════════════════════════════════════════
   NIEDERSACHSEN FEIERTAGE 2024–2027
═══════════════════════════════════════════════ */
const NI_HOLIDAYS = [
  {date:"2024-01-01",name:"Neujahr"},{date:"2024-03-29",name:"Karfreitag"},
  {date:"2024-04-01",name:"Ostermontag"},{date:"2024-05-01",name:"Tag der Arbeit"},
  {date:"2024-05-09",name:"Christi Himmelfahrt"},{date:"2024-05-20",name:"Pfingstmontag"},
  {date:"2024-10-03",name:"Tag der Deutschen Einheit"},{date:"2024-10-31",name:"Reformationstag"},
  {date:"2024-12-25",name:"1. Weihnachtstag"},{date:"2024-12-26",name:"2. Weihnachtstag"},
  {date:"2025-01-01",name:"Neujahr"},{date:"2025-04-18",name:"Karfreitag"},
  {date:"2025-04-21",name:"Ostermontag"},{date:"2025-05-01",name:"Tag der Arbeit"},
  {date:"2025-05-29",name:"Christi Himmelfahrt"},{date:"2025-06-09",name:"Pfingstmontag"},
  {date:"2025-10-03",name:"Tag der Deutschen Einheit"},{date:"2025-10-31",name:"Reformationstag"},
  {date:"2025-12-25",name:"1. Weihnachtstag"},{date:"2025-12-26",name:"2. Weihnachtstag"},
  {date:"2026-01-01",name:"Neujahr"},{date:"2026-04-03",name:"Karfreitag"},
  {date:"2026-04-06",name:"Ostermontag"},{date:"2026-05-01",name:"Tag der Arbeit"},
  {date:"2026-05-14",name:"Christi Himmelfahrt"},{date:"2026-05-25",name:"Pfingstmontag"},
  {date:"2026-10-03",name:"Tag der Deutschen Einheit"},{date:"2026-10-31",name:"Reformationstag"},
  {date:"2026-12-25",name:"1. Weihnachtstag"},{date:"2026-12-26",name:"2. Weihnachtstag"},
  {date:"2027-01-01",name:"Neujahr"},{date:"2027-03-26",name:"Karfreitag"},
  {date:"2027-03-29",name:"Ostermontag"},{date:"2027-05-01",name:"Tag der Arbeit"},
  {date:"2027-05-06",name:"Christi Himmelfahrt"},{date:"2027-05-17",name:"Pfingstmontag"},
  {date:"2027-10-03",name:"Tag der Deutschen Einheit"},{date:"2027-10-31",name:"Reformationstag"},
  {date:"2027-12-25",name:"1. Weihnachtstag"},{date:"2027-12-26",name:"2. Weihnachtstag"},
];

/* ═══════════════════════════════════════════════
   DATABASE – better-sqlite3 (synchron, server-ready)
   Kein saveDB() nötig – schreibt direkt auf Disk
═══════════════════════════════════════════════ */
let db;

function createDB() {
  const d = new DatabaseSync(DB_PATH);
  d.exec("PRAGMA journal_mode = WAL");
  d.exec("PRAGMA foreign_keys = ON");
  d.exec("PRAGMA busy_timeout = 5000");     // kurz warten statt „database is locked“
  d.exec("PRAGMA synchronous = NORMAL");    // mit WAL sicher und schneller
  return d;
}

// Query-Helfer – identische API wie vorher
function one(sql, ...p)  { return db.prepare(sql).get(...p);  }
function all(sql, ...p)  { return db.prepare(sql).all(...p);  }
function run(sql, ...p)  { return db.prepare(sql).run(...p);  }
function tx(fn)          {
  db.exec("BEGIN");
  try { const r = fn(); db.exec("COMMIT"); return r; }
  catch(e) { db.exec("ROLLBACK"); throw e; }
}

const dateOfIso   = iso => localDateOf(Date.parse(iso));
const today = () => localDateOf(Date.now());
const now   = () => new Date().toISOString();
const intId = id => parseInt(id);

/* ═══════════════════════════════════════════════
   BACKUP
═══════════════════════════════════════════════ */
function createBackup(label = "") {
  if (!fs.existsSync(DB_PATH)) return null;
  const ts   = new Date().toISOString().replace(/[:.]/g,"-").slice(0,19);
  const name = `zeitprotokoll-${ts}${label?"-"+label:""}.db`;
  const dest = path.join(BAK_DIR, name);
  // Datei-Kopie (WAL-Checkpoint vorher)
  try {
    // konsistente Kopie im laufenden Betrieb (statt Datei kopieren)
    try { db.exec(`VACUUM INTO '${dest.replace(/'/g, "''")}'`); }
    catch (_) { db.exec("PRAGMA wal_checkpoint(TRUNCATE)"); fs.copyFileSync(DB_PATH, dest); }
    try { fs.chmodSync(dest, 0o600); } catch (_) {}
    // Alte Backups aufräumen – maximal 30 behalten
    const files = fs.readdirSync(BAK_DIR)
      .filter(f => f.endsWith(".db"))
      .sort();
    if (files.length > 30)
      files.slice(0, files.length - 30).forEach(f =>
        fs.rmSync(path.join(BAK_DIR, f), { force: true }));
  } catch(e) { console.error("Backup failed:", e.message); }
  return dest;
}

app.post("/api/admin/backup", (_, res) => {
  const dest = createBackup("manual");
  writeLog("success","system",`Backup erstellt: ${path.basename(dest||"")}`);
  res.json({ ok: true, file: path.basename(dest||"") });
});

app.get("/api/admin/backups", (_, res) => {
  const files = fs.existsSync(BAK_DIR)
    ? fs.readdirSync(BAK_DIR).filter(f=>f.endsWith(".db")).sort().reverse()
    : [];
  res.json({ backups: files, dir: BAK_DIR });
});

/* ═══════════════════════════════════════════════
   SYSTEM LOG
═══════════════════════════════════════════════ */
function writeLog(level, category, message) {
  try {
    db.prepare("INSERT INTO system_logs (timestamp,level,category,message) VALUES (?,?,?,?)")
      .run(now(), level, category, message);
  } catch(_) {}
}

function cleanOldLogs() {
  const cutoff = new Date(Date.now() - 7*24*60*60*1000).toISOString();
  try { db.prepare("DELETE FROM system_logs WHERE timestamp<?").run(cutoff); } catch(_) {}
}


function getSettings() {
  return Object.fromEntries(all("SELECT key,value FROM settings").map(r=>[r.key,r.value]));
}

function stopTimer() {
  const s = one("SELECT * FROM timer_state WHERE id=1");
  if (!s?.start_time) return;
  const end = now();
  const dur = Math.floor((new Date(end)-new Date(s.start_time))/1000);
  if (dur >= 60) {   // unter 1 Minute (z. B. aus Versehen gestartet) wird nicht gespeichert
    const r = run(`INSERT INTO time_entries (project_id,task_id,plan_task_id,start_time,end_time,duration_seconds,comment,date,source,activity)
         VALUES (?,?,?,?,?,?,?,?,'manual',?)`,
      s.project_id||null, s.task_id||null, s.plan_task_id||null, s.start_time, end, dur,
      s.comment||null, dateOfIso(s.start_time), s.activity||null);
    try { hooks.checks && hooks.checks.flagAfter(Number(r.lastInsertRowid)); } catch (_) {}   // Timer nie blockieren, nur markieren
  }
  run("UPDATE timer_state SET project_id=NULL,task_id=NULL,plan_task_id=NULL,comment=NULL,start_time=NULL,activity=NULL WHERE id=1");
}

/* Live-Pause-Tracking: unabhängig vom (entfernten) Arbeitstag-Konzept.
   Start/Stop wie beim Projekt-Timer, landet als normale "freie" Pause
   (work_day_id=null) in der pauses-Tabelle, wird also genauso von den
   Ist-Stunden abgezogen wie eine manuell nachgetragene Pause. */
function stopPause() {
  const s = one("SELECT * FROM pause_state WHERE id=1");
  if (!s?.start_time) return;
  const end = now();
  const dur = Math.floor((new Date(end)-new Date(s.start_time))/1000);
  if (dur > 5) {
    run("INSERT INTO pauses (work_day_id,start_time,end_time,date) VALUES (NULL,?,?,?)",
      s.start_time, end, dateOfIso(s.start_time));
  }
  run("UPDATE pause_state SET start_time=NULL WHERE id=1");
}

function enrichEntry(e) {
  if (!e) return e;
  const p = e.project_id ? one("SELECT p.name AS pname,p.hourly_rate,c.name AS cname FROM projects p LEFT JOIN customers c ON c.id=p.customer_id WHERE p.id=?", e.project_id) : null;
  const t = e.task_id    ? one("SELECT title,jira_ticket FROM tasks WHERE id=?", e.task_id) : null;
  const pt = e.plan_task_id ? one("SELECT title FROM plan_tasks WHERE id=?", e.plan_task_id) : null;
  return {...e, project_name:p?.pname, hourly_rate:p?.hourly_rate, customer_name:p?.cname, task_title:t?.title, jira_ticket:t?.jira_ticket, plan_task_title:pt?.title};
}

/* Ergänzt eine plan_tasks-Zeile um bereits getrackte Sekunden (Ist gegen
   estimated_minutes), damit Fortschritt im Timer-Picker & in der Planung
   sichtbar ist. */
function withTrackedSeconds(planTask) {
  const r = one("SELECT COALESCE(SUM(duration_seconds),0) as s FROM time_entries WHERE plan_task_id=?", planTask.id);
  return {...planTask, tracked_seconds: r?.s || 0};
}

/* Summe aller "freien" Pausen (kein work_day_id) an einem Datum – Sekunden.
   Wird für Tage ohne Arbeitstag-Fenster genutzt, um manuell erfasste
   Pausen von den gebuchten Stunden abzuziehen. */
function freePauseSecondsForDate(date) {
  const r = one(
    `SELECT COALESCE(SUM(CASE WHEN end_time IS NOT NULL
       THEN (julianday(end_time)-julianday(start_time))*86400 ELSE 0 END),0) as ps
     FROM pauses WHERE date=? AND work_day_id IS NULL`, date);
  return r?.ps || 0;
}

/* ═══════════════════════════════════════════════
   PLANUNGS-MODUL
═══════════════════════════════════════════════ */
app.get("/api/plan/tasks", (req, res) => {
  const { date, week, status } = req.query;
  let sql = "SELECT p.*, pr.name AS project_name FROM plan_tasks p LEFT JOIN projects pr ON pr.id=p.project_id WHERE 1=1";
  const params = [];
  if (date)   { sql += " AND (p.scheduled_date=? OR (p.scheduled_date IS NULL AND p.scheduled_week IS NULL))"; params.push(date); }
  if (week)   { sql += " AND p.scheduled_week=?"; params.push(week); }
  if (status) { sql += " AND p.status=?"; params.push(status); }
  else        { sql += " AND p.status != 'done'"; }
  sql += " ORDER BY p.priority ASC, p.sort_order ASC, p.created_at ASC";
  res.json(all(sql, ...params).map(withTrackedSeconds));
});

app.get("/api/plan/today", (req, res) => {
  const d = req.query.date || today();
  // Heute geplant + offene von gestern (verschoben)
  const todayTasks = all(
    "SELECT p.*, pr.name AS project_name FROM plan_tasks p LEFT JOIN projects pr ON pr.id=p.project_id WHERE p.scheduled_date=? AND p.status!='done' ORDER BY p.priority, p.sort_order",
    d).map(withTrackedSeconds);
  // Offene Aufgaben ohne Datum (Inbox)
  const inbox = all(
    "SELECT p.*, pr.name AS project_name FROM plan_tasks p LEFT JOIN projects pr ON pr.id=p.project_id WHERE p.scheduled_date IS NULL AND p.scheduled_week IS NULL AND p.status='open' ORDER BY p.priority, p.sort_order LIMIT 10").map(withTrackedSeconds);
  res.json({ date: d, todayTasks, inbox });
});

app.post("/api/plan/tasks", (req, res) => {
  const { title, project_id, jira_ticket, estimated_minutes, priority, scheduled_date, scheduled_week, recurrence, notes } = req.body;
  if (!title?.trim()) return res.status(400).json({ error: "Titel fehlt" });
  const r = run(
    "INSERT INTO plan_tasks (title,project_id,jira_ticket,estimated_minutes,priority,scheduled_date,scheduled_week,recurrence,notes) VALUES (?,?,?,?,?,?,?,?,?)",
    title.trim(), project_id||null, jira_ticket||null, estimated_minutes||60,
    priority||2, scheduled_date||null, scheduled_week||null, recurrence||null, notes||null);
  res.json(withTrackedSeconds(one("SELECT p.*,pr.name AS project_name FROM plan_tasks p LEFT JOIN projects pr ON pr.id=p.project_id WHERE p.id=?", r.lastInsertRowid)));
});

app.put("/api/plan/tasks/:id", (req, res) => {
  const id = intId(req.params.id);
  const { title, project_id, jira_ticket, estimated_minutes, priority, status, scheduled_date, scheduled_week, recurrence, notes, completed_at } = req.body;
  run(`UPDATE plan_tasks SET title=?,project_id=?,jira_ticket=?,estimated_minutes=?,priority=?,
    status=?,scheduled_date=?,scheduled_week=?,recurrence=?,notes=?,completed_at=? WHERE id=?`,
    title, project_id||null, jira_ticket||null, estimated_minutes||60, priority||2,
    status||'open', scheduled_date||null, scheduled_week||null, recurrence||null, notes||null,
    completed_at||null, id);
  res.json(withTrackedSeconds(one("SELECT p.*,pr.name AS project_name FROM plan_tasks p LEFT JOIN projects pr ON pr.id=p.project_id WHERE p.id=?", id)));
});

app.delete("/api/plan/tasks/:id", (req, res) => {
  run("DELETE FROM plan_tasks WHERE id=?", intId(req.params.id));
  res.json({ ok: true });
});

// Aufgabe auf nächsten Tag/Woche verschieben
app.post("/api/plan/tasks/:id/move", (req, res) => {
  const id   = intId(req.params.id);
  const task = one("SELECT * FROM plan_tasks WHERE id=?", id);
  if (!task) return res.status(404).json({ error: "Nicht gefunden" });

  if (task.scheduled_date) {
    const next = new Date(task.scheduled_date + "T12:00:00Z");
    next.setUTCDate(next.getUTCDate() + 1);
    // Wochenende überspringen
    while ([0,6].includes(next.getUTCDay())) next.setUTCDate(next.getUTCDate()+1);
    run("UPDATE plan_tasks SET scheduled_date=?,status='open' WHERE id=?", next.toISOString().split("T")[0], id);
  } else if (task.scheduled_week) {
    // Nächste Woche
    const [y,w] = task.scheduled_week.split("-W").map(Number);
    // ISO-Jahre haben 52 oder 53 Wochen (53, wenn der 28.12. in KW 53 liegt)
    const weeksIn = yy => { const d = new Date(Date.UTC(yy,11,28)); const day=(d.getUTCDay()+6)%7; d.setUTCDate(d.getUTCDate()-day+3); const f=new Date(Date.UTC(d.getUTCFullYear(),0,4)); return 1+Math.round(((d-f)/864e5-3+((f.getUTCDay()+6)%7))/7); };
    const nextW = w >= weeksIn(y) ? `${y+1}-W01` : `${y}-W${String(w+1).padStart(2,"0")}`;
    run("UPDATE plan_tasks SET scheduled_week=?,status='open' WHERE id=?", nextW, id);
  }
  res.json(one("SELECT * FROM plan_tasks WHERE id=?", id));
});

// Tagesabschluss: offene Aufgaben auf nächsten Tag verschieben
app.post("/api/plan/rollover", (req, res) => {
  const { from_date } = req.body;
  if (!from_date) return res.status(400).json({ error: "from_date fehlt" });
  const next = new Date(from_date + "T12:00:00Z");
  next.setUTCDate(next.getUTCDate()+1);
  while ([0,6].includes(next.getUTCDay())) next.setUTCDate(next.getUTCDate()+1);
  const nextDate = next.toISOString().split("T")[0];
  const result = run(
    "UPDATE plan_tasks SET scheduled_date=?,status='open' WHERE scheduled_date=? AND status='open'",
    nextDate, from_date);
  writeLog("info","system",`Rollover: ${result.changes} offene Aufgaben von ${from_date} → ${nextDate}`);
  res.json({ moved: result.changes, to: nextDate });
});

/* ═══════════════════════════════════════════════
   CUSTOMERS
═══════════════════════════════════════════════ */
app.get("/api/customers", (_, res) => {
  res.json(all("SELECT * FROM customers ORDER BY name").map(c => ({
    ...c,
    project_count:  one("SELECT COUNT(*) as n FROM projects WHERE customer_id=?", c.id)?.n || 0,
    total_seconds:  one("SELECT COALESCE(SUM(te.duration_seconds),0) as s FROM time_entries te JOIN projects p ON p.id=te.project_id WHERE p.customer_id=?", c.id)?.s || 0,
  })));
});
app.post("/api/customers", (req, res) => {
  const {name,contact_name,email,notes} = req.body;
  if (!name?.trim()) return res.status(400).json({error:"Name fehlt"});
  const r = run("INSERT INTO customers (name,contact_name,email,notes) VALUES (?,?,?,?)", name.trim(), contact_name||null, email||null, notes||null);
  res.json(one("SELECT * FROM customers WHERE id=?", r.lastInsertRowid));
});
app.put("/api/customers/:id", (req, res) => {
  const {name,contact_name,email,notes} = req.body;
  run("UPDATE customers SET name=?,contact_name=?,email=?,notes=? WHERE id=?", name, contact_name||null, email||null, notes||null, intId(req.params.id));
  res.json(one("SELECT * FROM customers WHERE id=?", intId(req.params.id)));
});
app.delete("/api/customers/:id", (req, res) => {
  run("DELETE FROM customers WHERE id=?", intId(req.params.id));
  res.json({ok:true});
});

/* ═══════════════════════════════════════════════
   PROJECTS
═══════════════════════════════════════════════ */
app.get("/api/projects", (req, res) => {
  const {status} = req.query;
  // stumm geschaltete Projekte tauchen in Auswahllisten nicht auf (Verwaltung: ?with_muted=1)
  const conds = [], params = [];
  if (status && status!=="all") { conds.push("p.status=?"); params.push(status); }
  if (req.query.with_muted !== "1") conds.push("COALESCE(p.muted,0)=0");
  const where = conds.length ? " WHERE " + conds.join(" AND ") : "";
  const projects = all(`SELECT p.*, c.name AS customer_name FROM projects p LEFT JOIN customers c ON c.id=p.customer_id${where} ORDER BY c.name, p.name`, ...params);
  res.json(projects.map(p => ({
    ...p,
    task_count:     one("SELECT COUNT(*) as n FROM tasks WHERE project_id=?", p.id)?.n || 0,
    booked_seconds: one("SELECT COALESCE(SUM(duration_seconds),0) as s FROM time_entries WHERE project_id=?", p.id)?.s || 0,
  })));
});
app.get("/api/projects/:id", (req, res) => {
  const p = one("SELECT p.*, c.name AS customer_name FROM projects p LEFT JOIN customers c ON c.id=p.customer_id WHERE p.id=?", intId(req.params.id));
  if (!p) return res.status(404).json({error:"Nicht gefunden"});
  p.booked_seconds = one("SELECT COALESCE(SUM(duration_seconds),0) as s FROM time_entries WHERE project_id=?", p.id)?.s || 0;
  const tasks = all("SELECT * FROM tasks WHERE project_id=? ORDER BY sort_order, created_at", p.id)
    .map(t => ({...t, booked_seconds: one("SELECT COALESCE(SUM(duration_seconds),0) as s FROM time_entries WHERE task_id=?", t.id)?.s||0}));
  res.json({...p, tasks});
});
app.post("/api/projects", (req, res) => {
  const {customer_id,name,jira_key,status,budget_hours,hourly_rate,description} = req.body;
  if (!name?.trim()) return res.status(400).json({error:"Name fehlt"});
  const r = run("INSERT INTO projects (customer_id,name,jira_key,status,budget_hours,hourly_rate,description) VALUES (?,?,?,?,?,?,?)",
    customer_id||null, name.trim(), jira_key||null, status||"active", parseFloat(budget_hours)||0, parseFloat(hourly_rate)||0, description||null);
  res.json(one("SELECT * FROM projects WHERE id=?", r.lastInsertRowid));
});
app.put("/api/projects/:id", (req, res) => {
  const {customer_id,name,jira_key,status,budget_hours,hourly_rate,description} = req.body;
  run("UPDATE projects SET customer_id=?,name=?,jira_key=?,status=?,budget_hours=?,hourly_rate=?,description=? WHERE id=?",
    customer_id||null, name, jira_key||null, status, parseFloat(budget_hours)||0, parseFloat(hourly_rate)||0, description||null, intId(req.params.id));
  res.json(one("SELECT * FROM projects WHERE id=?", intId(req.params.id)));
});
app.delete("/api/projects/:id", (req, res) => {
  run("DELETE FROM projects WHERE id=?", intId(req.params.id));
  res.json({ok:true});
});

/* ═══════════════════════════════════════════════
   TASKS
═══════════════════════════════════════════════ */
app.get("/api/tasks", (req, res) => {
  const tasks = all("SELECT * FROM tasks WHERE project_id=? ORDER BY sort_order, created_at", intId(req.query.projectId));
  res.json(tasks.map(t => ({...t, booked_seconds: one("SELECT COALESCE(SUM(duration_seconds),0) as s FROM time_entries WHERE task_id=?", t.id)?.s||0})));
});
app.post("/api/tasks", async (req, res) => {
  const {project_id,title,jira_ticket,status,estimated_hours,description,local_only} = req.body;
  if (!title?.trim()||!project_id) return res.status(400).json({error:"title + project_id fehlen"});
  const maxOrder = one("SELECT MAX(sort_order) AS m FROM tasks WHERE project_id=?", intId(project_id))?.m || 0;
  const r = run("INSERT INTO tasks (project_id,title,jira_ticket,status,estimated_hours,description,sort_order) VALUES (?,?,?,?,?,?,?)",
    intId(project_id), title.trim(), jira_ticket||null, status||"open", parseFloat(estimated_hours)||0, description||null, maxOrder+1);
  let task = one("SELECT * FROM tasks WHERE id=?", r.lastInsertRowid);
  // Freier Titel ohne Ticket in einem Plane-Projekt → gleich als Work-Item in Plane anlegen
  if (!local_only && !(jira_ticket||"").trim()) {
    try { task = (await plane.linkTaskToPlane(task.id)) || task; }
    catch (e) { writeLog("warn", "plane", `Work-Item für „${task.title}“ nicht angelegt (bleibt lokal): ${e.message}`); task = {...task, plane_error: e.message}; }
  }
  res.json(task);
});
app.put("/api/tasks/:id", (req, res) => {
  const {title,jira_ticket,status,estimated_hours,description} = req.body;
  const before = one("SELECT jira_ticket FROM tasks WHERE id=?", intId(req.params.id));
  run("UPDATE tasks SET title=?,jira_ticket=?,status=?,estimated_hours=?,description=? WHERE id=?",
    title, jira_ticket||null, status, parseFloat(estimated_hours)||0, description||null, intId(req.params.id));
  // Ticketnummer geändert → alte Plane-Zuordnung lösen (sonst landet Zeit am alten Work-Item)
  const norm = v => String(v||"").trim().toUpperCase();
  if (before && norm(before.jira_ticket) !== norm(jira_ticket)) {
    run("UPDATE tasks SET plane_work_item_id=NULL, plane_project_id=NULL WHERE id=?", intId(req.params.id));
    syncAuto();
  }
  res.json(one("SELECT * FROM tasks WHERE id=?", intId(req.params.id)));
});
app.delete("/api/tasks/:id", (req, res) => {
  run("DELETE FROM tasks WHERE id=?", intId(req.params.id));
  res.json({ok:true});
});

/* ═══════════════════════════════════════════════
   TIMER
═══════════════════════════════════════════════ */
app.get("/api/timer/status", (_, res) => {
  const s = one("SELECT * FROM timer_state WHERE id=1");
  if (!s?.start_time) return res.json({active:false});
  const project  = s.project_id ? one("SELECT p.*, c.name AS customer_name FROM projects p LEFT JOIN customers c ON c.id=p.customer_id WHERE p.id=?", s.project_id) : null;
  const task     = s.task_id    ? one("SELECT * FROM tasks WHERE id=?", s.task_id) : null;
  const planTask = s.plan_task_id ? one("SELECT * FROM plan_tasks WHERE id=?", s.plan_task_id) : null;
  res.json({active:true, project, task, planTask, comment:s.comment, start_time:s.start_time, activity:s.activity||null});
});
app.post("/api/timer/start", (req, res) => {
  setTimeout(() => syncAuto(), 0);
  let {project_id,task_id,plan_task_id,comment,activity} = req.body;
  if (!project_id && !plan_task_id) return res.status(400).json({error:"project_id oder plan_task_id fehlt"});
  const rd = hooks.sources ? hooks.sources.redirect(project_id, task_id) : null;   // abgeschlossenes Projekt → Nachfolger
  if (rd) { project_id = rd.project_id; task_id = rd.task_id; }
  stopPause();
  stopTimer();
  // optional rückwirkend starten (z. B. ab Terminbeginn) – höchstens 12 h zurück, nie in der Zukunft,
  // und nie vor das Ende der letzten Buchung (keine Überschneidung)
  let startIso = now();
  const want = Date.parse(req.body.start_time || "");
  if (Number.isFinite(want) && want < Date.now() && Date.now() - want < 12 * 3600e3) {
    const lastEnd = one("SELECT MAX(end_time) AS e FROM time_entries WHERE end_time IS NOT NULL AND date=?", localDateOf(want))?.e;
    startIso = new Date(Math.max(want, lastEnd ? Date.parse(lastEnd) : 0)).toISOString();
  }
  run("UPDATE timer_state SET project_id=?,task_id=?,plan_task_id=?,comment=?,start_time=?,activity=? WHERE id=1",
    project_id?intId(project_id):null, task_id?intId(task_id):null, plan_task_id?intId(plan_task_id):null, comment||null, startIso, activity||null);
  res.json({ok:true, start_time: startIso, redirected_from: rd?.redirected_from || null});
});
app.post("/api/timer/stop", (_, res) => { stopTimer(); syncAuto(); res.json({ok:true}); });

/* ═══════════════════════════════════════════════
   LIVE PAUSE TRACKING
═══════════════════════════════════════════════ */
app.post("/api/pause/start", (_, res) => {
  stopTimer();  // laufenden Projekt-Timer beenden, Eintrag wird gespeichert
  stopPause();  // Sicherheit: falls doch schon eine Pause offen war
  run("UPDATE pause_state SET start_time=? WHERE id=1", now());
  res.json({ok:true});
});
app.post("/api/pause/stop", (_, res) => { stopPause(); res.json({ok:true}); });
app.get("/api/pause/status", (_, res) => {
  const s = one("SELECT * FROM pause_state WHERE id=1");
  if (!s?.start_time) return res.json({active:false});
  res.json({active:true, start_time:s.start_time});
});

/* ═══════════════════════════════════════════════
   WORK DAY
═══════════════════════════════════════════════ */
app.get("/api/workday", (req, res) => {
  const date    = req.query.date || today();
  const workday = one("SELECT * FROM work_days WHERE date=?", date);
  const logs    = workday ? all("SELECT * FROM daily_logs WHERE work_day_id=? ORDER BY timestamp", workday.id) : [];
  const entries = all("SELECT * FROM time_entries WHERE date=? ORDER BY start_time", date).map(enrichEntry);
  // Pausen anhand des Datums abfragen (nicht nur über work_day_id) – seit Entfernung
  // des Arbeitstag-Konzepts haben neue Pausen kein work_day_id mehr, sind aber über
  // die eigene date-Spalte trotzdem korrekt zuordenbar.
  const pauses  = all("SELECT * FROM pauses WHERE date=? ORDER BY start_time", date);
  res.json({workday:workday||null, dailyLogs:logs, timeEntries:entries, pauses});
});
app.post("/api/workday/start", (req, res) => {
  const existing = one("SELECT * FROM work_days WHERE date=?", today());
  if (existing) return res.json({workday:existing, alreadyRunning:true});
  const r = run("INSERT INTO work_days (date,start_time) VALUES (?,?)", today(), now());
  res.json({workday:one("SELECT * FROM work_days WHERE id=?", r.lastInsertRowid)});
});
app.put("/api/workday/:id", (req, res) => {
  const {start_time,end_time,notes} = req.body;
  const fields=[]; const params=[];
  if (start_time!==undefined){fields.push("start_time=?");params.push(start_time);}
  if (end_time  !==undefined){fields.push("end_time=?");  params.push(end_time);}
  if (notes     !==undefined){fields.push("notes=?");     params.push(notes);}
  if (fields.length) { params.push(intId(req.params.id)); run(`UPDATE work_days SET ${fields.join(",")} WHERE id=?`,...params); }
  res.json(one("SELECT * FROM work_days WHERE id=?", intId(req.params.id)));
});
app.post("/api/workday/end", (req, res) => {
  stopTimer();
  stopPause();
  run("UPDATE pauses SET end_time=? WHERE date=? AND end_time IS NULL", now(), today());
  run("UPDATE work_days SET end_time=?,notes=? WHERE date=? AND end_time IS NULL", now(), req.body.notes||null, today());
  res.json({ok:true});
});

app.post("/api/workday/reopen", (req, res) => {
  const { date } = req.body;
  const d = date || today();
  const wd = one("SELECT * FROM work_days WHERE date=?", d);
  if (!wd) return res.status(404).json({error:"Kein Arbeitstag gefunden"});
  if (!wd.end_time) return res.json({ok:true, message:"Bereits offen"});
  // Pause von Feierabend bis jetzt eintragen (die Unterbrechung zählt nicht als Arbeitszeit)
  run("INSERT INTO pauses (work_day_id,start_time,date) VALUES (?,?,?)", wd.id, wd.end_time, d);
  run("UPDATE work_days SET end_time=NULL WHERE id=?", wd.id);
  writeLog("info","system",`Arbeitstag ${d} wieder geöffnet (Pause seit ${wd.end_time.slice(11,16)} eingetragen)`);
  res.json({ok:true, pauseFrom: wd.end_time});
});
app.post("/api/workday/pause/start", (req, res) => {
  const wd = one("SELECT * FROM work_days WHERE date=? AND end_time IS NULL", today());
  if (!wd) return res.status(400).json({error:"Kein aktiver Arbeitstag"});
  const existing = one("SELECT * FROM pauses WHERE work_day_id=? AND end_time IS NULL", wd.id);
  if (existing) return res.json({pause:existing, alreadyPaused:true});
  const r = run("INSERT INTO pauses (work_day_id,start_time,date) VALUES (?,?,?)", wd.id, now(), today());
  res.json({pause:one("SELECT * FROM pauses WHERE id=?", r.lastInsertRowid)});
});
app.post("/api/workday/pause/end", (req, res) => {
  const wd = one("SELECT * FROM work_days WHERE date=? AND end_time IS NULL", today());
  if (!wd) return res.status(400).json({error:"Kein aktiver Arbeitstag"});
  const activePause = one("SELECT * FROM pauses WHERE work_day_id=? AND end_time IS NULL", wd.id);
  if (!activePause) return res.json({ok:true, skipped:true});
  const durSecs = Math.floor((new Date() - new Date(activePause.start_time)) / 1000);
  if (durSecs < 300) {
    // Unter 5 Minuten → Pause verwerfen, nicht speichern
    run("DELETE FROM pauses WHERE id=?", activePause.id);
    res.json({ok:true, discarded:true, reason:"unter 5 Minuten"});
  } else {
    run("UPDATE pauses SET end_time=? WHERE id=?", now(), activePause.id);
    res.json({ok:true});
  }
});


/* ═══════════════════════════════════════════════
   PAUSEN – manuell anlegen, bearbeiten & löschen
═══════════════════════════════════════════════ */
/* Prüft ob [start,end] sich mit einer bereits vorhandenen Pause am selben
   Tag überschneidet (excludeId für Bearbeiten der Pause selbst). */
function pauseOverlaps(date, start, end, excludeId = null) {
  const existing = all(
    "SELECT * FROM pauses WHERE date=? AND end_time IS NOT NULL AND id != ?",
    date, excludeId || -1);
  return existing.find(p => start < p.end_time && p.start_time < end) || null;
}

app.post("/api/pauses", (req, res) => {
  const { date, start_time, end_time } = req.body;
  if (!start_time) return res.status(400).json({ error: "start_time erforderlich" });
  if (!end_time)   return res.status(400).json({ error: "end_time erforderlich" });
  const d = date || dateOfIso(start_time);
  if (end_time <= start_time) return res.status(400).json({ error: "Ende liegt vor Start" });

  const durSecs = Math.floor((new Date(end_time) - new Date(start_time)) / 1000);
  if (durSecs < 300) {
    return res.status(400).json({ error: "Pause muss mindestens 5 Minuten lang sein" });
  }

  const overlap = pauseOverlaps(d, start_time, end_time);
  if (overlap) {
    return res.status(400).json({
      error: `Überschneidet sich mit bestehender Pause ${overlap.start_time.slice(11,16)}–${overlap.end_time.slice(11,16)} Uhr`
    });
  }

  // Falls für den Tag ein Arbeitstag existiert, Pause daran verknüpfen
  // (dann zählt sie automatisch in der Workday-Fenster-Berechnung mit).
  const wd = one("SELECT id FROM work_days WHERE date=?", d);

  const r = run("INSERT INTO pauses (work_day_id,start_time,end_time,date) VALUES (?,?,?,?)",
    wd ? wd.id : null, start_time, end_time, d);
  const pause = one("SELECT * FROM pauses WHERE id=?", r.lastInsertRowid);
  writeLog("info","system",`Pause manuell erfasst: ${d} ${start_time.slice(11,16)}–${end_time.slice(11,16)}`);
  res.json(pause);
});

app.put("/api/pauses/:id", (req, res) => {
  const { start_time, end_time } = req.body;
  const id = intId(req.params.id);
  if (!start_time) return res.status(400).json({ error: "start_time fehlt" });

  const current = one("SELECT * FROM pauses WHERE id=?", id);
  if (!current) return res.status(404).json({ error: "Pause nicht gefunden" });

  if (end_time && end_time <= start_time) return res.status(400).json({ error: "Ende liegt vor Start" });
  if (end_time) {
    const overlap = pauseOverlaps(current.date, start_time, end_time, id);
    if (overlap) {
      return res.status(400).json({
        error: `Überschneidet sich mit bestehender Pause ${overlap.start_time.slice(11,16)}–${overlap.end_time.slice(11,16)} Uhr`
      });
    }
  }

  run("UPDATE pauses SET start_time=?, end_time=? WHERE id=?",
    start_time, end_time||null, id);
  res.json(one("SELECT * FROM pauses WHERE id=?", id));
});

app.delete("/api/pauses/:id", (req, res) => {
  run("DELETE FROM pauses WHERE id=?", intId(req.params.id));
  res.json({ ok: true });
});

/* ═══════════════════════════════════════════════
   DAILY LOG
═══════════════════════════════════════════════ */
app.post("/api/daily-log", (req, res) => {
  const {text} = req.body;
  if (!text?.trim()) return res.status(400).json({error:"text fehlt"});
  const wd = one("SELECT * FROM work_days WHERE date=?", today());
  const r  = run("INSERT INTO daily_logs (work_day_id,timestamp,text,date) VALUES (?,?,?,?)", wd?.id||null, now(), text.trim(), today());
  res.json(one("SELECT * FROM daily_logs WHERE id=?", r.lastInsertRowid));
});
app.put("/api/daily-log/:id", (req, res) => {
  const { text } = req.body;
  if (!text?.trim()) return res.status(400).json({ error: "text fehlt" });
  run("UPDATE daily_logs SET text=? WHERE id=?", text.trim(), intId(req.params.id));
  res.json(one("SELECT * FROM daily_logs WHERE id=?", intId(req.params.id)));
});

app.delete("/api/daily-log/:id", (req, res) => {
  run("DELETE FROM daily_logs WHERE id=?", intId(req.params.id));
  res.json({ok:true});
});

/* ═══════════════════════════════════════════════
   TIME ENTRIES
═══════════════════════════════════════════════ */
app.get("/api/time-entries", (req, res) => {
  const {from,to,date,projectId} = req.query;
  let sql="SELECT * FROM time_entries WHERE 1=1"; const params=[];
  if(from)      {sql+=" AND date>=?";      params.push(from);}
  if(to)        {sql+=" AND date<=?";      params.push(to);}
  if(date)      {sql+=" AND date=?";       params.push(date);}
  if(projectId) {sql+=" AND project_id=?"; params.push(intId(projectId));}
  sql+=" ORDER BY start_time DESC";
  res.json(all(sql,...params).map(enrichEntry));
});
app.post("/api/time-entries", (req, res) => {
  let {project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,activity} = req.body;
  if (!start_time) return res.status(400).json({error:"start_time erforderlich"});
  const rd = hooks.sources ? hooks.sources.redirect(project_id, task_id) : null;
  if (rd) { project_id = rd.project_id; task_id = rd.task_id; }
  // Dauer immer aus Beginn/Ende berechnen, wenn nicht angegeben
  if (end_time && !duration_seconds) duration_seconds = Math.max(0, Math.round((Date.parse(end_time) - Date.parse(start_time)) / 1000));
  const _dur = (end_time && duration_seconds) ? duration_seconds : null;
  const tooShort = _dur !== null && _dur < 300; // < 5min
  if (hooks.checks && hooks.checks.guard(req, res, { date: date || dateOfIso(start_time), start_time, end_time })) return;   // Plausibilität
  const r = run("INSERT INTO time_entries (project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,activity) VALUES (?,?,?,?,?,?,?,?,?)",
    project_id ? intId(project_id) : null, task_id?intId(task_id):null, start_time, end_time||null,
    duration_seconds||null, comment||null, billable===false?0:1, date||dateOfIso(start_time), activity||null);
  if (hooks.checks) hooks.checks.stamp(Number(r.lastInsertRowid), req);
  const entry = enrichEntry(one("SELECT * FROM time_entries WHERE id=?", r.lastInsertRowid));
  syncAuto();
  res.json({...entry, warning: tooShort ? "Eintrag unter 5 Minuten – bitte prüfen" : null});
});
app.put("/api/time-entries/:id", (req, res) => {
  const {project_id,task_id,start_time,end_time,duration_seconds,comment,billable} = req.body;
  // Plausibilität nur, wenn sich die Zeit ändert (Text/Ticket ändern fragt nie nach)
  if (hooks.checks && (start_time!==undefined || end_time!==undefined || duration_seconds!==undefined)) {
    const cur = one("SELECT * FROM time_entries WHERE id=?", intId(req.params.id));
    if (cur) { const st = start_time ?? cur.start_time; const cand = { id: cur.id, start_time: st, end_time: end_time !== undefined ? end_time : cur.end_time, date: req.body.date || (start_time ? dateOfIso(start_time) : cur.date) };
      if (hooks.checks.guard(req, res, cand)) return; }
  }
  const fields=[]; const params=[];
  if(project_id!==undefined)       {fields.push("project_id=?");      params.push(project_id?intId(project_id):null);}
  if(task_id!==undefined)          {fields.push("task_id=?");          params.push(task_id?intId(task_id):null);}
  if(start_time!==undefined)       {fields.push("start_time=?","date=?"); params.push(start_time, req.body.date || dateOfIso(start_time));}
  if(end_time!==undefined)         {fields.push("end_time=?");         params.push(end_time||null);}
  if(duration_seconds!==undefined) {fields.push("duration_seconds=?"); params.push(duration_seconds||null);}
  if(comment!==undefined)          {fields.push("comment=?");          params.push(comment||null);}
  if(billable!==undefined)         {fields.push("billable=?");         params.push(billable?1:0);}
  if(req.body.activity!==undefined){fields.push("activity=?");         params.push(req.body.activity||null);}
  if(fields.length){params.push(intId(req.params.id)); run(`UPDATE time_entries SET ${fields.join(",")} WHERE id=?`,...params);
    if (duration_seconds===undefined && (start_time!==undefined || end_time!==undefined)) {      // Dauer passend zu Beginn/Ende halten
      const e = one("SELECT start_time,end_time FROM time_entries WHERE id=?", intId(req.params.id));
      if (e?.end_time) run("UPDATE time_entries SET duration_seconds=? WHERE id=?", Math.max(0, Math.round((Date.parse(e.end_time) - Date.parse(e.start_time)) / 1000)), intId(req.params.id));
    }
    if (hooks.checks && req._checks) hooks.checks.stamp(intId(req.params.id), req);
    syncAuto();}
  res.json(enrichEntry(one("SELECT * FROM time_entries WHERE id=?", intId(req.params.id))));
});
app.delete("/api/time-entries/:id", (req, res) => {
  try { plane.beforeDelete(intId(req.params.id)); } catch(e) { console.error("Plane:", e.message); }
  try { tempoPush.beforeDelete(intId(req.params.id)); } catch(e) { console.error("Tempo:", e.message); }
  run("DELETE FROM time_entries WHERE id=?", intId(req.params.id));
  syncAuto();
  res.json({ok:true});
});

/* Doppelte/überlappende Buchungen auflösen (v6.12.1)
   merge = zu EINER Buchung zusammenführen (früheste Start-, späteste Endzeit, Texte zusammen, Ticket der Buchung mit Ticket)
   trim  = verschiedene Tickets: die überlappende Zeit bei der kürzeren Buchung abschneiden */
app.post("/api/time-entries/resolve-overlap", (req, res) => {
  const A = one("SELECT e.*, t.jira_ticket FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id WHERE e.id=?", Number(req.body?.a));
  const B = one("SELECT e.*, t.jira_ticket FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id WHERE e.id=?", Number(req.body?.b));
  if (!A || !B || A.id === B.id || !A.end_time || !B.end_time) return res.status(400).json({ error: "Buchungen nicht gefunden" });
  const mode = req.body?.mode === "trim" ? "trim" : "merge";
  if (mode === "merge") {
    // behalten: die mit Ticket, sonst die längere
    const [keep, drop] = (B.task_id && !A.task_id) || (!!B.task_id === !!A.task_id && (B.duration_seconds || 0) > (A.duration_seconds || 0)) ? [B, A] : [A, B];
    const st = keep.start_time < drop.start_time ? keep.start_time : drop.start_time;
    const en = keep.end_time > drop.end_time ? keep.end_time : drop.end_time;
    const texts = [...new Set([keep.comment, drop.comment].map(t => (t || "").trim()).filter(Boolean))];
    run("UPDATE time_entries SET start_time=?, end_time=?, duration_seconds=?, comment=?, activity=COALESCE(activity,?), task_id=COALESCE(task_id,?), project_id=COALESCE(project_id,?) WHERE id=?",
      st, en, Math.round((Date.parse(en) - Date.parse(st)) / 1000), texts.join(" · ") || null, drop.activity || null, drop.task_id || null, drop.project_id || null, keep.id);
    try { run("UPDATE meeting_state SET entry_id=? WHERE entry_id=?", keep.id, drop.id); } catch (_) {}
    try { run("UPDATE ssh_sessions SET time_entry_id=? WHERE time_entry_id=?", keep.id, drop.id); } catch (_) {}
    try { plane.beforeDelete(drop.id); } catch (_) {}
    try { tempoPush.beforeDelete(drop.id); } catch (_) {}
    run("DELETE FROM time_entries WHERE id=?", drop.id);
    try { hooks.checks && hooks.checks.flagAfter(keep.id); } catch (_) {}
    writeLog("info", "buchungen", `Buchungen #${drop.id} und #${keep.id} zusammengeführt (${st.slice(11, 16)}–${en.slice(11, 16)})`);
    syncAuto();
    return res.json({ ok: true, kept: keep.id, removed: drop.id });
  }
  // trim: kürzere Buchung um den überlappenden Teil kürzen
  const [long, short] = (A.duration_seconds || 0) >= (B.duration_seconds || 0) ? [A, B] : [B, A];
  let s2 = short.start_time, e2 = short.end_time;
  if (short.start_time >= long.start_time && short.start_time < long.end_time) s2 = long.end_time;      // fängt in der langen an → später beginnen
  else if (short.end_time > long.start_time && short.end_time <= long.end_time) e2 = long.start_time;   // endet in der langen → früher enden
  else return res.status(400).json({ error: "Die kürzere Buchung liegt ganz in der längeren – bitte zusammenführen oder eine löschen" });
  const d = Math.round((Date.parse(e2) - Date.parse(s2)) / 1000);
  if (d < 60) return res.status(400).json({ error: "Danach bliebe weniger als 1 Minute – bitte zusammenführen" });
  run("UPDATE time_entries SET start_time=?, end_time=?, duration_seconds=? WHERE id=?", s2, e2, d, short.id);
  try { hooks.checks && (hooks.checks.flagAfter(short.id), hooks.checks.flagAfter(long.id)); } catch (_) {}
  syncAuto();
  res.json({ ok: true, trimmed: short.id });
});

/* ═══════════════════════════════════════════════
   SETTINGS
═══════════════════════════════════════════════ */
const SECRET_KEYS = ["jira_token", "plane_api_key"];
app.get("/api/settings", (_, res) => {
  const s = getSettings();
  // Parse numeric values
  const numeric = ["target_mon","target_tue","target_wed","target_thu","target_fri","target_sat","target_sun","opening_balance_hours","vacation_days_per_year"];
  numeric.forEach(k => { if(s[k]!==undefined) s[k]=parseFloat(s[k])||0; });
  for (const k of SECRET_KEYS) if (s[k]) s[k] = "••••" + String(s[k]).slice(-4);
  res.json(s);
});
app.put("/api/settings", (req, res) => {
  for (const [key, value] of Object.entries(req.body)) {
    if (SECRET_KEYS.includes(key) && String(value).startsWith("••••")) continue;  // maskierter Wert → unverändert lassen
    run("INSERT OR REPLACE INTO settings (key, value) VALUES (?,?)", key, String(value));
  }
  res.json({ok:true});
});

/* ═══════════════════════════════════════════════
   ABSENCES
═══════════════════════════════════════════════ */
/* Abwesenheiten als Kalender-Abo (macOS Kalender › Ablage › Neues Kalenderabonnement, oder webcal-Link).
   Bleibt automatisch aktuell – Änderungen hier erscheinen beim nächsten Abgleich im Kalender. */
app.get(["/api/calendar/abwesenheiten.ics", "/api/absences.ics"], (req, res) => {
  const LBL = { urlaub: "Urlaub", krankheit: "Krank", feiertag: "Feiertag", fortbildung: "Fortbildung", sonstiges: "Abwesend", gleitzeit: "Gleitzeit" };
  const esc = t => String(t || "").replace(/\\/g, "\\\\").replace(/[,;]/g, m => "\\" + m).replace(/\r?\n/g, "\\n");
  const d8 = d => d.replace(/-/g, "");
  const next = d => { const x = new Date(d + "T12:00:00Z"); x.setUTCDate(x.getUTCDate() + 1); return x.toISOString().slice(0, 10); };
  const stamp = new Date().toISOString().replace(/[-:]/g, "").replace(/\.\d+/, "");
  const lines = ["BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Beispiel//Zeitprotokoll//DE", "CALSCALE:GREGORIAN", "METHOD:PUBLISH",
    "X-WR-CALNAME:Zeitprotokoll Abwesenheiten", "X-WR-TIMEZONE:Europe/Berlin", "REFRESH-INTERVAL;VALUE=DURATION:PT1H", "X-PUBLISHED-TTL:PT1H"];
  for (const a of all("SELECT * FROM absences ORDER BY date_from")) {
    const t = LBL[a.type] || a.type;
    lines.push("BEGIN:VEVENT", `UID:zp-abs-${a.id}@zeitprotokoll`, `DTSTAMP:${stamp}`, `DTSTART;VALUE=DATE:${d8(a.date_from)}`, `DTEND;VALUE=DATE:${d8(next(a.date_to))}`,
      `SUMMARY:${esc(t + (a.note ? ` – ${a.note}` : ""))}`, "TRANSP:TRANSPARENT", `CATEGORIES:${esc(t)}`,
      ...(a.hours ? [`DESCRIPTION:${esc(`${a.hours} h`)}`] : []), "END:VEVENT");
  }
  lines.push("END:VCALENDAR");
  res.set("Content-Type", "text/calendar; charset=utf-8");
  if (req.query.download) res.set("Content-Disposition", 'attachment; filename="zeitprotokoll-abwesenheiten.ics"');
  res.send(lines.join("\r\n") + "\r\n");
});
app.get("/api/absences", (req, res) => {
  const {from,to} = req.query;
  let sql="SELECT * FROM absences WHERE 1=1"; const params=[];
  if(from){sql+=" AND date_to >= ?";  params.push(from);}
  if(to)  {sql+=" AND date_from <= ?";params.push(to);}
  sql+=" ORDER BY date_from";
  res.json(all(sql,...params));
});
app.post("/api/absences", (req, res) => {
  const {date_from,date_to,type,hours,note} = req.body;
  if(!date_from||!type) return res.status(400).json({error:"date_from + type erforderlich"});
  const r = run("INSERT INTO absences (date_from,date_to,type,hours,note) VALUES (?,?,?,?,?)",
    date_from, date_to||date_from, type, hours!=null&&hours!==""?parseFloat(hours):null, note||null);
  res.json(one("SELECT * FROM absences WHERE id=?", r.lastInsertRowid));
});
app.put("/api/absences/:id", (req, res) => {
  const {date_from,date_to,type,hours,note} = req.body;
  run("UPDATE absences SET date_from=?,date_to=?,type=?,hours=?,note=? WHERE id=?",
    date_from, date_to||date_from, type, hours!=null&&hours!==""?parseFloat(hours):null, note||null, intId(req.params.id));
  res.json(one("SELECT * FROM absences WHERE id=?", intId(req.params.id)));
});
app.delete("/api/absences/:id", (req, res) => {
  run("DELETE FROM absences WHERE id=?", intId(req.params.id));
  res.json({ok:true});
});

/* ── Feiertage Niedersachsen importieren ── */
app.post("/api/absences/import-holidays", (req, res) => {
  const {year} = req.body;
  const holidays = year
    ? NI_HOLIDAYS.filter(h => h.date.startsWith(String(year)))
    : NI_HOLIDAYS;
  let imported=0, skipped=0;
  for (const h of holidays) {
    const exists = one("SELECT id FROM absences WHERE date_from=? AND type='feiertag'", h.date);
    if (exists) { skipped++; continue; }
    run("INSERT INTO absences (date_from,date_to,type,hours,note) VALUES (?,?,'feiertag',NULL,?)",
      h.date, h.date, h.name);
    imported++;
  }
  res.json({imported, skipped});
});

/* ═══════════════════════════════════════════════
   MONTH CLOSINGS (Monatsabschluss)
═══════════════════════════════════════════════ */
app.get("/api/month-closings", (_, res) => {
  res.json(all("SELECT * FROM month_closings ORDER BY year DESC, month DESC"));
});

app.post("/api/month-closings", (req, res) => {
  const {year, month, notes} = req.body;
  if (!year||!month) return res.status(400).json({error:"year + month erforderlich"});

  const existing = one("SELECT * FROM month_closings WHERE year=? AND month=?", year, month);
  if (existing) return res.status(409).json({error:"Monat bereits abgeschlossen"});

  // Calculate saldo for this month
  const from = `${year}-${String(month).padStart(2,"0")}-01`;
  const lastDay = new Date(year, month, 0).getDate();
  const to   = `${year}-${String(month).padStart(2,"0")}-${lastDay}`;

  const settings  = getSettings();
  const firstEntry2 = one("SELECT MIN(date) as d FROM time_entries");
  const autoStart2  = firstEntry2?.d || from;
  const trackFrom = settings.tracking_start_date || autoStart2;
  const openBal   = parseFloat(settings.opening_balance_hours) || 0;
  const dayTargets = {
    0:parseFloat(settings.target_sun)||0, 1:parseFloat(settings.target_mon)||0,
    2:parseFloat(settings.target_tue)||0, 3:parseFloat(settings.target_wed)||0,
    4:parseFloat(settings.target_thu)||0, 5:parseFloat(settings.target_fri)||0,
    6:parseFloat(settings.target_sat)||0,
  };

  // Get previous closed months' carry-over
  const prevClose = one("SELECT carry_over_hours FROM month_closings WHERE (year*12+month) < (?*12+?) ORDER BY year DESC, month DESC LIMIT 1", year, month);
  const baseBalance = prevClose ? prevClose.carry_over_hours : openBal;

  // Calculate this month's delta
  const absences = all("SELECT * FROM absences WHERE date_to >= ? AND date_from <= ?", from, to);
  let soll=0, ist=0;
  // Timezone-sicherer Date-Loop (immer T12:00:00Z verwenden)
  let loopDate = from > trackFrom ? from : trackFrom;
  const endDate = to;
  while (loopDate <= endDate) {
    const d   = loopDate;
    const dow = new Date(d + "T12:00:00Z").getUTCDay();
    const targetH = dayTargets[dow]||0;
    const absence = absences.find(a => d >= a.date_from && d <= a.date_to);
    const absenceH = absence ? (absence.hours!=null ? absence.hours : targetH) : 0;
    const wd2 = one("SELECT id,start_time,end_time FROM work_days WHERE date=?", d);
    const bookedRow2 = one("SELECT COALESCE(SUM(duration_seconds),0) as s FROM time_entries WHERE date=? AND COALESCE(is_absence,0)=0", d);
    const bookedH2 = (bookedRow2?.s||0)/3600;
    let workH2 = 0;
    // Tempo-importierte Tage → Summe der Buchungen
    const totalE2  = one("SELECT COUNT(*) as n FROM time_entries WHERE date=? AND COALESCE(is_absence,0)=0", d)?.n || 0;
    const tempoE2  = one("SELECT COUNT(*) as n, COALESCE(SUM(duration_seconds),0) as s FROM time_entries WHERE date=? AND source IN ('tempo_csv','tempo') AND COALESCE(is_absence,0)=0", d);
    if (totalE2 > 0 && tempoE2?.n === totalE2) {
      workH2 = (tempoE2?.s || 0) / 3600;
    } else if (settings.ist_from_workday === "1" && wd2?.start_time && wd2?.end_time) {   // v6.36: Arbeitstag-Fenster nur, wenn eingeschaltet
      const rawSecs2 = Math.max(0,(new Date(wd2.end_time)-new Date(wd2.start_time))/1000);
      const pauseRow2 = one("SELECT COALESCE(SUM(CASE WHEN end_time IS NOT NULL THEN (julianday(end_time)-julianday(start_time))*86400 ELSE 0 END),0) as ps FROM pauses WHERE work_day_id=?", wd2.id);
      workH2 = Math.max(0, rawSecs2 - (pauseRow2?.ps||0)) / 3600;
    } else {
      const freePauseSecs2 = freePauseSecondsForDate(d);
      workH2 = Math.max(0, bookedH2 - freePauseSecs2 / 3600);
    }
    soll += targetH;
    ist  += absence ? absenceH : workH2;
    // Next day
    const next = new Date(loopDate + "T12:00:00Z");
    next.setUTCDate(next.getUTCDate() + 1);
    loopDate = next.toISOString().split("T")[0];
  }

  const monthDelta    = ist - soll;
  const carryOver     = baseBalance + monthDelta;

  const r = run(`INSERT INTO month_closings (year,month,soll_hours,ist_hours,delta_hours,carry_over_hours,notes,closed_at)
    VALUES (?,?,?,?,?,?,?,?)`,
    year, month, Math.round(soll*100)/100, Math.round(ist*100)/100,
    Math.round(monthDelta*100)/100, Math.round(carryOver*100)/100, notes||null, now());
  res.json(one("SELECT * FROM month_closings WHERE id=?", r.lastInsertRowid));
});

app.delete("/api/month-closings/:id", (req, res) => {
  run("DELETE FROM month_closings WHERE id=?", intId(req.params.id));
  res.json({ok:true});
});

/* ═══════════════════════════════════════════════
   BALANCE CORRECTIONS – Saldo jederzeit auf einen
   Stichtag korrigieren ("am 1.9. Saldo auf +5h setzen")
═══════════════════════════════════════════════ */
app.get("/api/balance-corrections", (_, res) => {
  res.json(all("SELECT * FROM balance_corrections ORDER BY date DESC, id DESC"));
});

app.post("/api/balance-corrections", (req, res) => {
  const { date, balance_hours, note } = req.body;
  if (!date) return res.status(400).json({error:"date erforderlich"});
  if (balance_hours === undefined || balance_hours === null || balance_hours === "")
    return res.status(400).json({error:"balance_hours erforderlich"});
  const r = run("INSERT INTO balance_corrections (date,balance_hours,note) VALUES (?,?,?)",
    date, parseFloat(balance_hours), note||null);
  writeLog("info","system",`Saldo-Korrektur: ${date} auf ${balance_hours}h gesetzt`);
  res.json(one("SELECT * FROM balance_corrections WHERE id=?", r.lastInsertRowid));
});

app.put("/api/balance-corrections/:id", (req, res) => {
  const { date, balance_hours, note } = req.body;
  const id = intId(req.params.id);
  if (date!==undefined) run("UPDATE balance_corrections SET date=? WHERE id=?", date, id);
  if (balance_hours!==undefined) run("UPDATE balance_corrections SET balance_hours=? WHERE id=?", parseFloat(balance_hours), id);
  if (note!==undefined) run("UPDATE balance_corrections SET note=? WHERE id=?", note, id);
  res.json(one("SELECT * FROM balance_corrections WHERE id=?", id));
});

app.delete("/api/balance-corrections/:id", (req, res) => {
  run("DELETE FROM balance_corrections WHERE id=?", intId(req.params.id));
  res.json({ok:true});
});

/* ═══════════════════════════════════════════════
   OVERVIEW (Balance-Berechnung)
═══════════════════════════════════════════════ */
function overviewHandler(req, res) {
  const {from, to} = req.query;
  if (!from||!to) return res.status(400).json({error:"from + to erforderlich"});

  const settings = getSettings();
  const openBal   = parseFloat(settings.opening_balance_hours) || 0;
  const dayTargets = {
    0:parseFloat(settings.target_sun)||0, 1:parseFloat(settings.target_mon)||0,
    2:parseFloat(settings.target_tue)||0, 3:parseFloat(settings.target_wed)||0,
    4:parseFloat(settings.target_thu)||0, 5:parseFloat(settings.target_fri)||0,
    6:parseFloat(settings.target_sat)||0,
  };

  // Auto-detect tracking start: use manual setting, else first time_entry, else "from"
  const firstEntry = one("SELECT MIN(date) as d FROM time_entries");
  const autoStart  = firstEntry?.d || from;
  const trackFrom  = settings.tracking_start_date || autoStart;
  // Konto zählt nur „bestätigte“ Zeit: aus Tempo importiert oder freigegeben (heute = nach Tempo übertragen, später eigene Monatsfreigabe).
  // Noch nicht Freigegebenes steht daneben (pendingH). Abschaltbar mit konto_nur_freigegeben=0 (dann zählt alles Gebuchte).
  const TE = settings.konto_nur_freigegeben === "0" ? "" : " AND (source IN ('tempo','tempo_csv') OR tempo_worklog_id IS NOT NULL OR tempo_push_id IS NOT NULL)";

  // Get last closed month's carry-over as base
  const _fy = parseInt(from.slice(0,4)), _fm = parseInt(from.slice(5,7));
  const lastClose = one("SELECT * FROM month_closings WHERE (year*12+month) < (?*12+?) ORDER BY year DESC, month DESC LIMIT 1", _fy, _fm);
  let runningBase = lastClose ? lastClose.carry_over_hours : openBal;

  // Manuelle Saldo-Korrekturen: jede Korrektur mit date<=to wird berücksichtigt.
  // Die jüngste Korrektur VOR `from` dient als neuer Fixpunkt/Startbasis (Berechnung
  // startet intern erst ab dort). Alle Korrekturen innerhalb [internalFrom, to] werden
  // zusätzlich an ihrem jeweiligen Datum als direkter Override angewendet, statt die
  // normale Tagesbilanz zu addieren – so kann man jederzeit einen neuen Stichtag setzen.
  const allCorrections   = all("SELECT * FROM balance_corrections WHERE date <= ? ORDER BY date ASC, id ASC", to);
  const priorCorrections = allCorrections.filter(c => c.date < from);
  const baseCorrection   = priorCorrections.length ? priorCorrections[priorCorrections.length-1] : null;
  if (baseCorrection) runningBase = baseCorrection.balance_hours;
  // Kein Monatsabschluss, keine Korrektur: Konto läuft ab Tracking-Start, nicht erst ab `from` (sonst steht es nach Monatswechsel auf 0)
  else if (!lastClose && trackFrom < from) {
    const prevDay = new Date(from + "T12:00:00Z"); prevDay.setUTCDate(prevDay.getUTCDate() - 1);
    let prev = null; overviewHandler({ query: { from: trackFrom, to: prevDay.toISOString().slice(0, 10) } }, { status() { return this; }, json(v) { prev = v; } });
    const pd = prev?.days?.[prev.days.length - 1]; if (pd) runningBase = pd.cumulativeBalance;
  }
  const internalFrom = baseCorrection ? baseCorrection.date : from;
  const correctionByDate = {};
  allCorrections.filter(c => c.date >= internalFrom && c.date <= to).forEach(c => { correctionByDate[c.date] = c.balance_hours; });

  // Build date range (intern ggf. früher startend, falls eine Korrektur vor `from` liegt)
  const dates=[];
  const cur = new Date(internalFrom+"T12:00:00Z");
  const end = new Date(to+"T12:00:00Z");
  while (cur<=end) {
    dates.push(cur.toISOString().split("T")[0]);
    cur.setUTCDate(cur.getUTCDate()+1);
  }

  const absences = all("SELECT * FROM absences WHERE date_to >= ? AND date_from <= ?", internalFrom, to);
  let cumBalance = runningBase;

  const allDays = dates.map(date => {
    const isBeforeTracking = date < trackFrom;
    const dow = new Date(date+"T12:00:00Z").getUTCDay();
    const targetH = isBeforeTracking ? 0 : (dayTargets[dow]||0);
    const absence = absences.find(a => date>=a.date_from && date<=a.date_to)||null;
    const absenceH = absence ? (absence.hours!=null ? absence.hours : (dayTargets[dow]||0)) : 0;
    // Gebuchte Zeit (Projektbuchungen) - nur zur Anzeige
    const bookedRow = one("SELECT COALESCE(SUM(duration_seconds),0) as s FROM time_entries WHERE date=? AND COALESCE(is_absence,0)=0"+TE, date);
    const bookedH   = (bookedRow?.s||0)/3600;
    const allRow    = TE ? one("SELECT COALESCE(SUM(duration_seconds),0) as s FROM time_entries WHERE date=? AND COALESCE(is_absence,0)=0", date) : null;
    const pendingH  = TE ? Math.max(0, ((allRow?.s||0) - (bookedRow?.s||0)) / 3600) : 0;   // gebucht, aber noch nicht freigegeben

    // Workday-Datensatz
    const wd = one("SELECT id,start_time,end_time FROM work_days WHERE date=?", date);

    // ── IST-Berechnung ────────────────────────────────────────────
    // Tempo-CSV-importierte Tage: Summe der Buchungen verwenden
    //   (Fenster wäre falsch weil Abend-Einträge das Fenster künstlich strecken)
    // App-getrackte Tage: Workday-Fenster (Start→Ende−Pausen) verwenden
    let workH = 0;
    const todayStr2 = today();

    // Prüfen ob der Tag ausschließlich Tempo-CSV-Einträge hat
    const totalEntries  = one("SELECT COUNT(*) as n FROM time_entries WHERE date=? AND COALESCE(is_absence,0)=0"+TE, date)?.n || 0;
    const tempoEntries  = one("SELECT COUNT(*) as n, COALESCE(SUM(duration_seconds),0) as s FROM time_entries WHERE date=? AND source IN ('tempo_csv','tempo') AND COALESCE(is_absence,0)=0", date);
    const isTempoDay    = totalEntries > 0 && tempoEntries?.n === totalEntries;

    if (isTempoDay && date < todayStr2) {
      // Tempo-importierte Tage → Summe der gebuchten Tempo-Zeit als Basis
      workH = (tempoEntries?.s || 0) / 3600;
      // Zusätzliche manuelle Einträge außerhalb der Tempo-Zeit addieren
      const manualExtra = one(
        "SELECT COALESCE(SUM(duration_seconds),0) as s FROM time_entries WHERE date=? AND source NOT IN ('tempo','tempo_csv')"+TE,
        date);
      workH += (manualExtra?.s || 0) / 3600;
    } else if (settings.ist_from_workday === "1" && wd?.start_time) {
      // App-getrackte Tage → Workday-Fenster (v6.36: nur noch, wenn „Arbeitstag zählt als Ist“ eingeschaltet ist; Standard: gebuchte Zeit)
      const wdStart  = new Date(wd.start_time);
      const wdEndRaw = wd.end_time ? new Date(wd.end_time) : null;

      let effectiveEnd = wdEndRaw;
      if (!effectiveEnd) effectiveEnd = null; // heute: noch offen

      if (effectiveEnd) {
        const rawSecs = Math.max(0, (effectiveEnd - wdStart) / 1000);
        const pauseRow = one(
          "SELECT COALESCE(SUM(CASE WHEN end_time IS NOT NULL THEN (julianday(end_time)-julianday(start_time))*86400 ELSE 0 END),0) as ps FROM pauses WHERE work_day_id=?", wd.id);
        const pauseSecs = pauseRow?.ps || 0;
        workH = Math.max(0, rawSecs - pauseSecs) / 3600;

        // Manuelle Einträge ab Feierabend (>=) hinzurechnen
        if (wdEndRaw) {
          const overtimeRow = one(
            "SELECT COALESCE(SUM(duration_seconds),0) as s FROM time_entries WHERE date=? AND start_time >= ? AND source NOT IN ('tempo_csv','tempo')",
            date, wd.end_time);
          workH += (overtimeRow?.s || 0) / 3600;
        }
      } else if (date < todayStr2) {
        // Vergangener Tag, Workday offen → Summe als Fallback
        workH = bookedH;
      }
      // Heute mit offenem Workday → workH bleibt 0, Frontend berechnet live
    } else {
      // Kein Workday → Buchungen als Fallback, manuelle Pausen abziehen
      const freePauseSecs = freePauseSecondsForDate(date);
      workH = Math.max(0, bookedH - freePauseSecs / 3600);
    }

    const effectiveH = absence ? absenceH : workH;
    const isFuture   = date > today();          // Zukunft zählt nicht ins Minus (Soll kommt erst, wenn der Tag da ist)
    const balanceH   = isBeforeTracking || isFuture ? 0 : (effectiveH - targetH);
    const hasCorrection = Object.prototype.hasOwnProperty.call(correctionByDate, date);
    if (hasCorrection) cumBalance = correctionByDate[date];
    else               cumBalance += balanceH;

    const isToday = date === todayStr2;
    return {date, dow, targetH, bookedH, pendingH: Math.round(pendingH*100)/100, workH: Math.round(workH*100)/100,
      absenceH, absence, balanceH: Math.round(balanceH*100)/100,
      // cumulativeBalance schließt heute NICHT ein – erst ab morgen (außer heute ist selbst ein Korrektur-Stichtag)
      cumulativeBalance: (isToday && !hasCorrection) ? Math.round((cumBalance - balanceH)*100)/100 : Math.round(cumBalance*100)/100,
      workday:wd||null, isBeforeTracking, isToday, isFuture};
  });

  const days = allDays.filter(d => d.date >= from);

  // Vacation stats
  const vacYear = new Date().getFullYear();
  const vacTotal = parseFloat(settings.vacation_days_per_year)||0;
  // Urlaubstage = Arbeitstage (Mo–Fr, kein Feiertag) in allen Urlaubs-Zeiträumen des Jahres – nicht Anzahl der Einträge (v6.22)
  const hol = new Set(all("SELECT date_from FROM absences WHERE type='feiertag' AND date_from LIKE ?", `${vacYear}-%`).map(r => r.date_from));
  const vacDays = new Set();
  for (const a of all("SELECT date_from, date_to, hours FROM absences WHERE type='urlaub' AND date_to >= ? AND date_from <= ?", `${vacYear}-01-01`, `${vacYear}-12-31`)) {
    for (let d = new Date(a.date_from + "T12:00:00Z"); d <= new Date(a.date_to + "T12:00:00Z"); d.setUTCDate(d.getUTCDate() + 1)) {
      const ds = d.toISOString().slice(0, 10), w = d.getUTCDay();
      if (ds.startsWith(String(vacYear)) && w > 0 && w < 6 && !hol.has(ds) && (dayTargets[w] || 0) > 0) vacDays.add(ds);
    }
  }
  const vacUsed = vacDays.size;

  res.json({days, settings, openBal, trackFrom, vacationTotal:vacTotal, vacationUsed:vacUsed});
}
app.get("/api/overview", overviewHandler);
/* Kontostand am Ende eines Tages (für Hinweise: genug Plus → Nachtragen nur optional) */
function balanceAt(date) {
  const st = getSettings(), from0 = st.tracking_start_date || one("SELECT MIN(date) AS d FROM time_entries")?.d || date;
  let out = null; overviewHandler({ query: { from: from0 < date ? from0 : date, to: date } }, { status() { return this; }, json(v) { out = v; } });
  const d = out?.days?.[out.days.length - 1]; return d ? d.cumulativeBalance : null;
}

/* ═══════════════════════════════════════════════
   REPORT
═══════════════════════════════════════════════ */
app.get("/api/report", (req, res) => {
  const {from,to} = req.query;
  if (!from||!to) return res.status(400).json({error:"from + to erforderlich"});
  const entries = all("SELECT * FROM time_entries WHERE date>=? AND date<=? ORDER BY date, start_time", from, to).map(enrichEntry);
  const projMap={};
  entries.forEach(e => {
    if(!e.project_id) return;
    const k=e.project_id;
    if(!projMap[k]){
      const p = one("SELECT budget_hours,jira_key FROM projects WHERE id=?", e.project_id);
      projMap[k]={project_id:k, project_name:e.project_name, customer_name:e.customer_name,
        hourly_rate:e.hourly_rate, budget_hours:p?.budget_hours||0, jira_key:p?.jira_key||null,
        total_seconds:0, billable_seconds:0, entry_count:0};
    }
    projMap[k].total_seconds    += e.duration_seconds||0;
    projMap[k].billable_seconds += e.billable?(e.duration_seconds||0):0;
    projMap[k].entry_count++;
  });
  res.json({summary:Object.values(projMap), entries, from, to});
});

/* ═══════════════════════════════════════════════
   TEMPO API INTEGRATION – Jira DATA CENTER
   ─────────────────────────────────────────
   Endpoint: {JIRA_BASE_URL}/rest/tempo-timesheets/4/worklogs/search
   Auth:     Bearer {TEMPO_TOKEN}  (Tempo-eigener Token, nicht Jira-PAT)
   Token:    Jira → Tempo → Settings → API Integration → New Token
   Docs DC:  https://apidocs.tempo.io/dc/modules/timesheets.html
═══════════════════════════════════════════════ */

/* HTTP POST helper für Tempo Data Center */
function tempoRequest(jiraBaseUrl, token, path, body) {
  return secure.withChainRepair(jiraBaseUrl, () => tempoRequestOnce(jiraBaseUrl, token, path, body));
}
function tempoRequestOnce(jiraBaseUrl, token, path, body) {
  return new Promise((resolve, reject) => {
    const urlStr   = jiraBaseUrl.replace(/\/$/, "") + path;
    const parsed   = new URL(urlStr);
    const isHttps  = parsed.protocol === "https:";
    const lib      = isHttps ? require("https") : require("http");
    const payload  = body ? JSON.stringify(body) : null;

    const options = {
      hostname: parsed.hostname,
      port:     parsed.port || (isHttps ? 443 : 80),
      path:     parsed.pathname + parsed.search,
      method:   body ? "POST" : "GET",
      headers: {
        "Authorization":  "Bearer " + token,
        "Content-Type":   "application/json",
        "Accept":         "application/json",
        ...(payload ? { "Content-Length": Buffer.byteLength(payload) } : {}),
      },
      ...(isHttps ? secure.requestOptions(urlStr) : {}),   // Zertifikat prüfen (Firmen-CA aus dem System)
    };

    const req = lib.request(options, res => {
      let data = "";
      res.on("data", c => data += c);
      res.on("end", () => {
        try   { resolve({ status: res.statusCode, body: JSON.parse(data) }); }
        catch { resolve({ status: res.statusCode, body: data }); }
      });
    });
    req.on("error", reject);
    req.setTimeout(20000, () => { req.destroy(); reject(new Error("Timeout nach 20s")); });
    if (payload) req.write(payload);
    req.end();
  });
}

/* DC Worklog-Format normalisieren → internes Format */
function normalizeDCWorklog(wl) {
  // DC: dateStarted = "2024-03-15T00:00:00.000+0000"
  const rawDate   = wl.dateStarted || wl.started || "";
  const startDate = rawDate.slice(0, 10);   // "2024-03-15"
  const startTime = rawDate.slice(11, 19) || "09:00:00";
  return {
    _id:          wl.id,                          // DC: Integer ID
    issueKey:     wl.issue?.key || "",
    summary:      wl.issue?.summary || "",
    comment:      wl.comment   || wl.description || "",
    startDate,
    startTime,
    durSecs:      wl.timeSpentSeconds || 0,
    author:       wl.author?.name || wl.author?.key || "",
  };
}

/* Aufgabe zu einem Jira-Ticket im Projekt finden oder anlegen (Tempo-Import behält so den Ticket-Key) */
function taskForJiraKey(projectId, key, title) {
  key = String(key || "").trim().toUpperCase();
  if (!projectId || !/^[A-Z][A-Z0-9_]*-\d+$/.test(key)) return null;
  const t = one("SELECT * FROM tasks WHERE project_id=? AND UPPER(TRIM(jira_ticket))=?", projectId, key);
  if (t) {
    if (title && (!t.title || t.title.toUpperCase() === key)) run("UPDATE tasks SET title=? WHERE id=?", String(title).slice(0, 255), t.id);
    return t.id;
  }
  const r = run("INSERT INTO tasks (project_id,title,jira_ticket,status) VALUES (?,?,?,'open')", projectId, String(title || key).slice(0, 255), key);
  return Number(r.lastInsertRowid);
}

/* Jira-User via /rest/api/2/myself ermitteln */
async function getJiraUser(s) {
  const r = await tempoRequest(s.jira_base_url, s.jira_token, "/rest/api/2/myself", null);
  if (r.status !== 200) throw new Error(`Kann Jira-User nicht ermitteln: HTTP ${r.status}`);
  // DC gibt name (username) und key zurück
  return { name: r.body.name, key: r.body.key, displayName: r.body.displayName };
}

/* Verbindungstest + Auto-Detect Username */
app.get("/api/integrations/tempo/status", async (_, res) => {
  const s = getSettings();
  if (!s.jira_token || !s.jira_base_url) {
    return res.json({ configured: false, connected: false,
      message: "Jira-URL und PAT fehlen – bitte in Einstellungen eintragen" });
  }
  try {
    // 1. Jira-User ermitteln (zeigt ob PAT grundsätzlich gültig ist)
    let jiraUser = null;
    try {
      jiraUser = await getJiraUser(s);
      // Username automatisch speichern
      if (jiraUser.name) {
        run("INSERT OR REPLACE INTO settings (key,value) VALUES ('jira_username',?)", jiraUser.name);
      }
    } catch(e) {
      return res.json({ configured: true, connected: false,
        message: "Jira-Authentifizierung fehlgeschlagen: " + e.message,
        hint: "PAT prüfen – muss ein gültiger Jira Personal Access Token sein" });
    }

    // 2. Tempo-Endpoint testen
    const testDate = new Date().toISOString().slice(0, 10);
    const r = await tempoRequest(s.jira_base_url, s.jira_token,
      `/rest/tempo-timesheets/4/worklogs/search?from=${testDate}&to=${testDate}&limit=1`, {});

    if (r.status === 200) {
      res.json({ configured: true, connected: true,
        message: `Verbindung OK ✓ – eingeloggt als ${jiraUser.displayName} (${jiraUser.name})`,
        jiraUser, autoFilterActive: true });
    } else if (r.status === 401) {
      res.json({ configured: true, connected: false,
        message: `Jira OK (${jiraUser.name}), aber Tempo-Zugriff verweigert (401) – Tempo-Lizenz prüfen` });
    } else {
      res.json({ configured: true, connected: false,
        message: `Jira OK (${jiraUser.name}), Tempo: HTTP ${r.status}`,
        detail: JSON.stringify(r.body).slice(0, 300) });
    }
  } catch(e) {
    res.json({ configured: true, connected: false, message: "Verbindungsfehler: " + e.message });
  }
});

let importRunning = false, importCancelled = false;
/* Abbruch-Signal */
app.post("/api/integrations/tempo/import/cancel", (_, res) => {
  if (importRunning) {
    importCancelled = true;
    writeLog("warn", "import", "Abbruch angefordert – wird nach aktuellem Eintrag gestoppt…");
    res.json({ ok: true, message: "Abbruch-Signal gesendet" });
  } else {
    res.json({ ok: false, message: "Kein Import läuft" });
  }
});

/* IMPORT: Tempo DC → Zeitprotokoll */
app.post("/api/integrations/tempo/import", async (req, res) => {
  const { from, to, dryRun } = req.body;
  if (!from || !to) return res.status(400).json({ error: "from + to erforderlich" });

  const s = getSettings();
  if (!s.jira_token)    return res.status(400).json({ error: "Kein Jira PAT – bitte in Einstellungen hinterlegen" });
  if (!s.jira_base_url) return res.status(400).json({ error: "Keine Jira-URL – bitte in Einstellungen hinterlegen" });

  if (importRunning) {
    return res.status(409).json({ error: "Ein Import läuft bereits – bitte erst abbrechen oder warten" });
  }
  importRunning   = true;
  importCancelled = false;

  const stats    = { total:0, imported:0, skipped_duplicate:0, skipped_no_project:0, skipped_other_user:0, errors:[], unmapped_issues:[], preview:[], cancelled:false };
  const logCat   = "import";

  writeLog("info", logCat, `${dryRun?"[TESTLAUF] ":""}Import gestartet: ${from} → ${to}`);

  try {
    // ── Aktuellen User ermitteln (Pflicht – nur eigene Zeiten) ────
    writeLog("info", logCat, `Verbinde mit ${s.jira_base_url} – ermittle Benutzer…`);
    let jiraUser;
    try {
      jiraUser = await getJiraUser(s);
      // Username ggf. speichern
      if (jiraUser.name) run("INSERT OR REPLACE INTO settings (key,value) VALUES ('jira_username',?)", jiraUser.name);
      writeLog("info", logCat, `Authentifiziert als: ${jiraUser.displayName} (${jiraUser.name}) – nur diese Einträge werden importiert`);
    } catch(e) {
      writeLog("error", logCat, "Kann Jira-User nicht ermitteln: " + e.message);
      throw new Error("Jira-Authentifizierung fehlgeschlagen: " + e.message);
    }

    // Gespeicherten Username als Fallback nutzen
    const myUsername = jiraUser.name || s.jira_username || "";
    const myKey      = jiraUser.key  || "";
    if (!myUsername) {
      writeLog("error", logCat, "Kein Benutzername ermittelt – Import abgebrochen");
      throw new Error("Benutzername konnte nicht ermittelt werden");
    }

    // ── Paginierter Abruf ──────────────────────
    const allWorklogs = [];
    let offset = 0;
    const limit = 500;
    while (true) {
      const apiPath = `/rest/tempo-timesheets/4/worklogs/search?from=${from}&to=${to}&limit=${limit}&offset=${offset}`;
      const r = await tempoRequest(s.jira_base_url, s.jira_token, apiPath, {});
      if (r.status !== 200) {
        const msg = `Tempo API Fehler HTTP ${r.status}: ${JSON.stringify(r.body).slice(0,300)}`;
        writeLog("error", logCat, msg);
        throw new Error(msg);
      }
      const batch = Array.isArray(r.body) ? r.body : (r.body?.results || r.body?.worklogs || []);
      allWorklogs.push(...batch);
      if (batch.length > 0)
        writeLog("progress", logCat, `${allWorklogs.length} Einträge von Tempo geladen…`);
      if (importCancelled) { writeLog("warn", logCat, "Abruf abgebrochen"); break; }
      if (batch.length < limit) break;
      offset += limit;
    }

    // ── User-Filter: nur eigene Worklogs ──────
    const myWorklogs = allWorklogs.filter(wl => {
      const author = wl.author?.name || wl.author?.key || "";
      return author === myUsername || author === myKey;
    });
    const filteredOut = allWorklogs.length - myWorklogs.length;
    stats.skipped_other_user = filteredOut;

    writeLog("info", logCat,
      `${allWorklogs.length} Worklogs gesamt → ${myWorklogs.length} gehören ${myUsername}${filteredOut > 0 ? ` (${filteredOut} von anderen Usern übersprungen)` : " (alle)"}`);

    stats.total = myWorklogs.length;
    writeLog("info", logCat, `Starte ${dryRun?"Prüfung":"Import"} von ${stats.total} eigenen Worklogs…`);

    let logStep = 0;
    for (const rawWl of myWorklogs) {
      if (importCancelled) {
        stats.cancelled = true;
        writeLog("warn", logCat, `Import abgebrochen nach ${stats.imported} Einträgen`);
        break;
      }
      try {
        const wl = normalizeDCWorklog(rawWl);

        // Duplikat
        const dupe = one("SELECT id, task_id, project_id FROM time_entries WHERE tempo_worklog_id=?", wl._id);
        if (dupe) {
          // früher importierte Einträge kannten den Ticket-Key nicht → jetzt nachtragen
          if (!dupe.task_id && wl.issueKey && !dryRun) {
            const tid = taskForJiraKey(dupe.project_id, wl.issueKey, wl.summary);
            if (tid) { run("UPDATE time_entries SET task_id=? WHERE id=?", tid, dupe.id); stats.relinked = (stats.relinked || 0) + 1; continue; }
          }
          stats.skipped_duplicate++; continue;
        }

        // Projekt-Mapping: "OPS-42" → prefix "OPS"
        const jiraPrefix = wl.issueKey.split("-")[0] || "";
        const pidByKey = jiraPrefix ? (hooks.sources ? hooks.sources.projectByJiraKey(jiraPrefix) : one("SELECT id FROM projects WHERE UPPER(jira_key)=UPPER(?)", jiraPrefix)?.id) : null;
        let project = pidByKey ? one("SELECT * FROM projects WHERE id=?", pidByKey) : null;

        let task = null;
        if (!project && wl.issueKey) {
          task = one(`SELECT t.*, p.id AS p_id FROM tasks t JOIN projects p ON p.id=t.project_id WHERE UPPER(t.jira_ticket)=UPPER(?)`, wl.issueKey);
          if (task) project = one("SELECT * FROM projects WHERE id=?", task.p_id);
        }

        if (!project) {
          stats.skipped_no_project++;
          if (wl.issueKey && !stats.unmapped_issues.includes(jiraPrefix)) {
            stats.unmapped_issues.push(jiraPrefix);
            writeLog("warn", logCat, `Kein Projekt für Ticket-Prefix "${jiraPrefix}" (${wl.issueKey}) → Jira-Kürzel im Projekt anlegen`);
          }
          continue;
        }

        if (!dryRun) {
          const startISO = new Date(`${wl.startDate}T${wl.startTime}`).toISOString();
          const endISO   = new Date(new Date(startISO).getTime() + wl.durSecs * 1000).toISOString();
          const taskId = task?.id || taskForJiraKey(project.id, wl.issueKey, wl.summary);
          run(`INSERT INTO time_entries (project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,source,tempo_worklog_id) VALUES (?,?,?,?,?,?,1,?,'tempo',?)`,
            project.id, taskId||null, startISO, endISO, wl.durSecs, wl.comment||null, wl.startDate, wl._id);
        } else {
          // Dry-run: collect preview
          stats.preview.push({
            date:        wl.startDate,
            startTime:   wl.startTime.slice(0,5),
            issueKey:    wl.issueKey,
            projectName: project.name,
            taskTitle:   task?.title || null,
            comment:     wl.comment || null,
            durSecs:     wl.durSecs,
            worklogId:   wl._id,
          });
        }
        stats.imported++;

        logStep++;
        if (logStep % 25 === 0)
          writeLog("progress", logCat, `${stats.imported} ${dryRun?"geprüft":"importiert"}, ${stats.skipped_duplicate} Duplikate, ${stats.skipped_no_project} ohne Projekt…`);
      } catch(e) {
        stats.errors.push({ worklogId: rawWl.id, issue: rawWl.issue?.key, error: e.message });
        writeLog("error", logCat, `Worklog ${rawWl.id} (${rawWl.issue?.key||"?"}): ${e.message}`);
      }
    }

    const summary = dryRun
      ? `Testlauf (${myUsername}): ${stats.total} eigene gefunden · ${stats.imported} importierbar · ${stats.skipped_no_project} ohne Projekt (${stats.unmapped_issues.join(", ")||"–"}) · ${stats.skipped_duplicate} Duplikate${stats.skipped_other_user > 0 ? ` · ${stats.skipped_other_user} fremde übersprungen` : ""}`
      : `Import (${myUsername}): ${stats.imported} importiert · ${stats.skipped_duplicate} Duplikate · ${stats.skipped_no_project} ohne Projekt${stats.skipped_other_user > 0 ? ` · ${stats.skipped_other_user} fremde übersprungen` : ""}`;
    writeLog(stats.cancelled ? "warn" : stats.errors.length > 0 ? "warn" : "success", logCat, summary);

    res.json({ ...stats, dryRun: !!dryRun, message: summary });
  } catch(e) {
    writeLog("error", logCat, "Import fehlgeschlagen: " + e.message);
    res.status(500).json({ error: e.message });
  } finally {
    importRunning   = false;
    importCancelled = false;
  }
});


/* Rollback: alle Zeiteinträge in Zeitraum löschen */
app.delete("/api/integrations/tempo/imported", (req, res) => {
  const { from, to, all: deleteAll } = req.query;
  if (!from || !to) return res.status(400).json({ error: "from + to erforderlich" });

  // Mit deleteAll=true: alle Einträge (egal welche Quelle)
  // Ohne: nur source IN (tempo, tempo_csv) - mit Fallback auf NULL (alte Importe ohne source)
  let sql, msg;
  if (deleteAll === 'true') {
    sql = "SELECT COUNT(*) as n FROM time_entries WHERE date>=? AND date<=?";
  } else {
    // Source-basiert ODER NULL (Importe vor der Migration hatten kein source)
    sql = "SELECT COUNT(*) as n FROM time_entries WHERE (source IN ('tempo','tempo_csv') OR source IS NULL) AND date>=? AND date<=?";
  }

  const countRow = one(sql, from, to);
  const count = countRow?.n || 0;

  if (deleteAll === 'true') {
    run("DELETE FROM time_entries WHERE date>=? AND date<=?", from, to);
  } else {
    run("DELETE FROM time_entries WHERE (source IN ('tempo','tempo_csv') OR source IS NULL) AND date>=? AND date<=?", from, to);
  }

  writeLog("info","import",`Rollback: ${count} Einträge gelöscht (${from}–${to}${deleteAll==='true'?' inkl. manuelle':''} )`);
  res.json({ deleted: count });
});

/* ═══════════════════════════════════════════════
   IN-APP UPDATE – kein Terminal nötig
   Extrakt + Copy + npm build im Hintergrund
   Frontend pollt /api/logs?category=update
═══════════════════════════════════════════════ */
app.post("/api/admin/update",
  express.raw({type:"application/octet-stream", limit:"200mb"}),
  (req, res) => {
    const tmpFile = "/tmp/zp-update.tar.gz";
    const tmpDir  = "/tmp/zp-update-" + Date.now();
    const logCat  = "update";

    writeLog("info", logCat, "Update-Datei empfangen – starte Installation…");

    try {
      fs.writeFileSync(tmpFile, req.body);
      fs.mkdirSync(tmpDir, {recursive:true});

      // Extract (mit oder ohne Hauptordner im Archiv)
      writeLog("info", logCat, "Entpacke Archiv…");
      execSync(`tar -xzf ${tmpFile} -C ${tmpDir}`, {stdio:"pipe"});
      const TOP = ["backend","frontend","desktop","scripts"];
      const entries = fs.readdirSync(tmpDir).filter(n => !n.startsWith("."));
      let root = tmpDir;
      if (entries.length === 1 && !TOP.includes(entries[0]) && fs.statSync(path.join(tmpDir, entries[0])).isDirectory())
        root = path.join(tmpDir, entries[0]);

      // Copy files: komplette Quell-Ordner, aber keine Laufzeit-/Build-Ordner und keine Daten
      writeLog("info", logCat, "Kopiere neue Dateien…");
      const SKIP = new Set(["node_modules","dist","release",".git","backup"]);
      let copied = 0;
      const copyTree = (src, dest, depth = 0) => {
        for (const ent of fs.readdirSync(src, {withFileTypes:true})) {
          if (SKIP.has(ent.name) || ent.name.startsWith("_backup") || /\.(db|db-wal|db-shm|log|tar\.gz|zip)$/.test(ent.name) || ent.name === ".env" || ent.name === "package-lock.json") continue;
          const s2 = path.join(src, ent.name), d2 = path.join(dest, ent.name);
          if (ent.isDirectory()) { if (depth < 6) copyTree(s2, d2, depth + 1); }
          else if (ent.isFile()) {
            fs.mkdirSync(path.dirname(d2), {recursive:true});
            fs.copyFileSync(s2, d2); copied++;
            writeLog("info", logCat, `✓ ${path.relative(root, s2)}`);
          }
        }
      };
      for (const d of TOP) if (fs.existsSync(path.join(root, d))) copyTree(path.join(root, d), path.join(INST_DIR, d));
      if (!copied) throw new Error("Im Archiv wurden keine Programmdateien gefunden (erwartet: backend/, frontend/, desktop/ oder scripts/)");
      writeLog("info", logCat, `${copied} Dateien kopiert`);

      // Antwort sofort senden – Build läuft im Hintergrund
      res.json({ ok: true, status: "building",
        message: "Dateien kopiert – Frontend wird gebaut… (Log beobachten)" });

      // Build im Hintergrund via spawn (blockiert nicht)
      writeLog("info", logCat, "npm install + vite build gestartet…");

      // launchd startet den Server mit minimalem PATH → Ordner von node, Homebrew und /usr/local ergänzen
      const buildPath = [path.dirname(process.execPath), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", process.env.PATH || ""].filter(Boolean).join(":");
      const build = spawn("sh", ["-c",
        `cd "${INST_DIR}/backend" && npm install --silent && cd "${INST_DIR}/frontend" && npm install --silent && npm run build`],
        { detached: true, stdio: "pipe", env: { ...process.env, PATH: buildPath } });

      let buildOut = "";
      build.stdout.on("data", d => { buildOut += d; });
      build.stderr.on("data", d => { buildOut += d; });

      build.on("close", code => {
        fs.rmSync(tmpDir,  {recursive:true, force:true});
        fs.rmSync(tmpFile, {force:true});

        if (code === 0) {
          writeLog("success", logCat, "✓ Build abgeschlossen – Server startet neu in 3s…");
          setTimeout(() => {
            writeLog("info", logCat, "Server-Neustart…");
            process.exit(0);
          }, 3000);
        } else {
          const errSnippet = buildOut.slice(-500);
          writeLog("error", logCat, "Build fehlgeschlagen: " + errSnippet);
        }
      });

      build.on("error", e => {
        writeLog("error", logCat, "Build-Prozess Fehler: " + e.message);
      });

    } catch(e) {
      try { fs.rmSync(tmpDir,{recursive:true,force:true}); } catch(_){}
      writeLog("error", "update", "Update fehlgeschlagen: " + e.message);
      if (!res.headersSent) res.status(500).json({ error: e.message });
    }
  }
);


/* ═══════════════════════════════════════════════
   SYSTEM LOGS
═══════════════════════════════════════════════ */
app.get("/api/logs", (req, res) => {
  const { category, limit=100, since } = req.query;
  let sql = "SELECT * FROM system_logs WHERE 1=1";
  const params = [];
  if (category) { sql += " AND category=?"; params.push(category); }
  if (since)    { sql += " AND timestamp > ?"; params.push(since); }
  sql += " ORDER BY id DESC LIMIT ?";
  params.push(parseInt(limit));
  const logs = all(sql, ...params).reverse();  // chronological order
  res.json({ logs, count: logs.length });
});

app.delete("/api/logs", (req, res) => {
  const { category } = req.query;
  if (category) run("DELETE FROM system_logs WHERE category=?", category);
  else          run("DELETE FROM system_logs");
  res.json({ ok: true });
});

app.post("/api/logs/cleanup", (req, res) => {
  cleanOldLogs();
  res.json({ ok: true });
});

app.get("/api/admin/holidays", (_, res) => {
  res.json({holidays: NI_HOLIDAYS, count: NI_HOLIDAYS.length});
});


/* ═══════════════════════════════════════════════
   CSV IMPORT
   Format (Semikolon-getrennt, erste Zeile = Header):
   datum;start;ende;kommentar;projekt;ticket
   ─────────────────────────────────────────────
   - datum:     YYYY-MM-DD
   - start:     HH:MM  (Pflicht)
   - ende:      HH:MM  (optional – ohne Ende = Stundenlog-Notiz)
   - kommentar: Text   (optional)
   - projekt:   Name des Projekts (optional, Matching über Name)
   - ticket:    Jira-Ticket z.B. OPS-42 (optional)
═══════════════════════════════════════════════ */
app.post("/api/csv-import", (req, res) => {
  const { csv, dryRun } = req.body;
  if (!csv?.trim()) return res.status(400).json({ error: "CSV fehlt" });

  const lines  = csv.split(/\r?\n/).map(l => l.trim()).filter(l => l && !l.startsWith("#"));
  const header = lines[0].split(";").map(h => h.toLowerCase().trim());
  const rows   = lines.slice(1);
  const logCat = "import";

  const col = key => header.indexOf(key);
  const get  = (row, key) => { const i = col(key); return i>=0 ? (row[i]||"").trim() : ""; };

  const stats = { total: rows.length, imported: 0, logs: 0, workdays: 0,
    skipped: 0, errors: [], preview: [], dryRun: !!dryRun };

  writeLog("info", logCat, `CSV-Import ${dryRun?"(Testlauf)":""}: ${rows.length} Zeilen`);

  // Cache projects by name for matching
  const allProjects = all("SELECT * FROM projects");
  const findProject = name => {
    if (!name) return null;
    const n = name.toLowerCase();
    return allProjects.find(p => p.name.toLowerCase() === n ||
      (p.jira_key && p.jira_key.toLowerCase() === n.split("-")[0].toLowerCase()));
  };
  const findTask = (ticket, projectId) => {
    if (!ticket || !projectId) return null;
    return one("SELECT * FROM tasks WHERE UPPER(jira_ticket)=UPPER(?) AND project_id=?", ticket, projectId);
  };

  // Group by date → determine workday range
  const byDate = {};
  for (const raw of rows) {
    const row   = raw.split(";");
    const datum = get(row, "datum");
    const start = get(row, "start");
    if (!datum || !start) { stats.skipped++; continue; }
    if (!byDate[datum]) byDate[datum] = [];
    byDate[datum].push(row);
  }

  for (const [date, dateRows] of Object.entries(byDate).sort()) {
    // Earliest start + latest end → workday
    let wdStart = null, wdEnd = null;
    const entries = [], logs = [];

    for (const row of dateRows) {
      const start    = get(row, "start");
      const end      = get(row, "ende") || get(row, "end") || get(row, "bis");
      const comment  = get(row, "kommentar") || get(row, "comment") || get(row, "beschreibung");
      const projName = get(row, "projekt") || get(row, "project");
      const ticket   = get(row, "ticket");

      if (!start) { stats.skipped++; continue; }

      const startISO = new Date(`${date}T${start}:00`);
      const endISO   = end ? new Date(`${date}T${end}:00`) : null;

      // Update workday range
      if (!wdStart || startISO < new Date(`${date}T${wdStart}:00`)) wdStart = start;
      if (endISO) {
        if (!wdEnd || endISO > new Date(`${date}T${wdEnd}:00`)) wdEnd = end;
      }

      if (end) {
        // Zeitbuchung
        const project = findProject(projName);
        const task    = project ? findTask(ticket, project.id) : null;
        const durSecs = Math.max(0, Math.round((endISO - startISO) / 1000));
        entries.push({ start, end, startISO: startISO.toISOString(), endISO: endISO.toISOString(),
          durSecs, comment, project, task, projName, ticket });
      } else if (comment) {
        // Nur Notiz (kein Ende)
        logs.push({ start, startISO: startISO.toISOString(), comment });
      } else {
        stats.skipped++;
      }
    }

    if (dryRun) {
      // Preview only
      if (wdStart) stats.preview.push({ date, wdStart, wdEnd, entries: entries.length, logs: logs.length,
        entries_detail: entries.map(e=>({start:e.start, end:e.end, comment:e.comment, project:e.project?.name||e.projName||null, ticket:e.ticket, durSecs:e.durSecs}))});
      stats.imported += entries.length;
      stats.logs     += logs.length;
      if (wdStart) stats.workdays++;
      continue;
    }

    // ── Write to DB ──────────────────────────────
    // Workday
    if (wdStart) {
      const wdStartISO = new Date(`${date}T${wdStart}:00`).toISOString();
      const wdEndISO   = wdEnd ? new Date(`${date}T${wdEnd}:00`).toISOString() : null;
      const existing   = one("SELECT * FROM work_days WHERE date=?", date);
      if (!existing) {
        run("INSERT INTO work_days (date,start_time,end_time) VALUES (?,?,?)", date, wdStartISO, wdEndISO||null);
        stats.workdays++;
        writeLog("info", logCat, `Arbeitstag ${date}: ${wdStart}–${wdEnd||"?"}`);
      } else {
        // Extend if earlier start or later end
        if (wdStartISO < existing.start_time) run("UPDATE work_days SET start_time=? WHERE id=?", wdStartISO, existing.id);
        if (wdEndISO && (!existing.end_time || wdEndISO > existing.end_time)) run("UPDATE work_days SET end_time=? WHERE id=?", wdEndISO, existing.id);
      }
    }

    // Time entries
    const wd = one("SELECT * FROM work_days WHERE date=?", date);
    for (const e of entries) {
      try {
        run(`INSERT INTO time_entries (project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,source)
          VALUES (?,?,?,?,?,?,1,?,'csv')`,
          e.project?.id||null, e.task?.id||null, e.startISO, e.endISO, e.durSecs, e.comment||null, date);
        stats.imported++;
      } catch(err) {
        stats.errors.push({ date, time: e.start, error: err.message });
        writeLog("error", logCat, `Eintrag ${date} ${e.start}: ${err.message}`);
      }
    }

    // Daily logs
    for (const l of logs) {
      try {
        run("INSERT INTO daily_logs (work_day_id,timestamp,text,date) VALUES (?,?,?,?)",
          wd?.id||null, l.startISO, l.comment, date);
        stats.logs++;
      } catch(err) {
        stats.errors.push({ date, time: l.start, error: err.message });
      }
    }
  }

  const summary = dryRun
    ? `Testlauf: ${stats.workdays} Arbeitstage, ${stats.imported} Buchungen, ${stats.logs} Notizen würden importiert`
    : `Importiert: ${stats.workdays} Arbeitstage, ${stats.imported} Buchungen, ${stats.logs} Notizen`;
  writeLog(stats.errors.length?"warn":"success", logCat, summary);
  res.json({ ...stats, message: summary });
});

/* ═══════════════════════════════════════════════
   DATA INTEGRITY CHECK
═══════════════════════════════════════════════ */

/* ═══════════════════════════════════════════════
   SEED: Beispiel Projekte aus Tempo-Daten
   Erstellt Kunden + Projekte falls nicht vorhanden
═══════════════════════════════════════════════ */
app.post("/api/admin/seed-example", (_, res) => {
  const created = { customers:[], projects:[] };

  const ensureCustomer = (name) => {
    const ex = one("SELECT * FROM customers WHERE name=?", name);
    if (ex) return ex.id;
    const r = run("INSERT INTO customers (name) VALUES (?)", name);
    created.customers.push(name);
    return r.lastInsertRowid;
  };

  const ensureProject = (jiraKey, name, customerId, status='active') => {
    const ex = one("SELECT * FROM projects WHERE jira_key=?", jiraKey);
    if (ex) return ex.id;
    const r = run("INSERT INTO projects (customer_id,name,jira_key,status) VALUES (?,?,?,?)",
      customerId||null, name, jiraKey, status);
    created.projects.push(name + " [" + jiraKey + "]");
    return r.lastInsertRowid;
  };

  // Kunden
  const cidBeispiel  = ensureCustomer("Beispiel GmbH");
  const cidRevacon  = ensureCustomer("REVACON Real Kunde B GmbH");
  const cidElka     = ensureCustomer("ELKA Kunde A GmbH");

  // Projekte – intern (Beispiel)
  ensureProject("INTERNAL",  "Beispiel Internal",                  cidBeispiel);
  ensureProject("ISMS",      "ISMS",                              cidBeispiel);
  ensureProject("PRJA",      "Beispiel Interne Prozesse Projekt",  cidBeispiel);
  ensureProject("MARKETING", "Beispiel Marketing",                 cidBeispiel);
  ensureProject("HS",        "Beispiel Sales",                     cidBeispiel);
  ensureProject("HP",        "Beispiel Purchasing",                cidBeispiel);
  // Projekte – extern
  ensureProject("REVACON",   "REVACON Real Kunde B GmbH",          cidRevacon);
  ensureProject("ELKA",      "ELKA Kunde A GmbH",             cidElka);

  const msg = `${created.customers.length} Kunden + ${created.projects.length} Projekte angelegt`;
  writeLog("success", "system", "Beispiel Seed: " + msg);
  res.json({ ok: true, message: msg, created });
});

app.get("/api/check/integrity", (req, res) => {
  const issues = [];
  const localHm = iso => { const d = new Date(iso); return `${String(d.getHours()).padStart(2,"0")}:${String(d.getMinutes()).padStart(2,"0")}`; };

  // 1. Überlappende Zeiteinträge – gleiche Regel wie Assistent/Plausibilitätsprüfung (v6.25.0):
  //    ab 3 Minuten, „Ist richtig so“ zählt nicht, Tempo-Spiegel nicht (Tempo liefert oft nur geschätzte Uhrzeiten)
  const entries = all("SELECT * FROM time_entries WHERE end_time IS NOT NULL ORDER BY date, start_time");
  const byDate  = {};
  entries.forEach(e => { (byDate[e.date]=byDate[e.date]||[]).push(e); });
  let ign = []; try { ign = JSON.parse(getSettings().overlap_ignore || "[]"); } catch (_) {}
  const mirror = e => ["tempo","tempo_csv"].includes(e.source);
  for (const [date, dayEntries] of Object.entries(byDate)) {
    const own = dayEntries.filter(e => !mirror(e));
    for (let i=0; i<own.length; i++) for (let j=i+1; j<own.length; j++) {
      const a = own[i], b = own[j];
      const ov = (Math.min(Date.parse(a.end_time), Date.parse(b.end_time)) - Math.max(Date.parse(a.start_time), Date.parse(b.start_time))) / 60e3;
      if (ov < 3 || ign.includes(`${Math.min(a.id,b.id)}-${Math.max(a.id,b.id)}`)) continue;
      issues.push({ type:"overlap", severity:"warn", date,
        message:`Überschneidung ${Math.round(ov)} min: ${localHm(a.start_time)}–${localHm(a.end_time)} und ${localHm(b.start_time)}–${localHm(b.end_time)} – im Assistenten lösen`,
        ids: [a.id, b.id] });
    }
  }

  // 2. Fehlende Endzeiten (älter als heute)
  const today_str = today();
  const openEntries = all("SELECT * FROM time_entries WHERE end_time IS NULL AND date < ?", today_str);
  openEntries.forEach(e => {
    issues.push({ type:"missing_end", severity:"error", date: e.date,
      message:`Kein Ende: ${e.start_time.slice(11,16)} – ${e.comment||"kein Kommentar"}`, id: e.id });
  });

  // 3. Negative Dauer
  const negEntries = all("SELECT * FROM time_entries WHERE duration_seconds < 0 OR (end_time IS NOT NULL AND end_time < start_time)");
  negEntries.forEach(e => {
    issues.push({ type:"negative_duration", severity:"error", date: e.date,
      message:`Negative Dauer: ${e.start_time.slice(11,16)}–${e.end_time?.slice(11,16)||"?"}`, id: e.id });
  });

  // 4. (entfernt v6.25.0) „Buchungen ohne Arbeitstag“ – seit Tempo-Abgleich und Wochenraster sind Tage
  //    ohne Arbeitstag-Eintrag normal (das Konto rechnet dann mit der Summe der Buchungen)

  // 5. Sehr lange Einträge (>12h)
  const longEntries = all("SELECT * FROM time_entries WHERE duration_seconds > 43200 AND COALESCE(source,'') NOT IN ('tempo','tempo_csv')");
  longEntries.forEach(e => {
    issues.push({ type:"too_long", severity:"warn", date: e.date,
      message:`Sehr langer Eintrag: ${Math.round((e.duration_seconds||0)/3600*10)/10}h – ${e.comment||"kein Kommentar"}`, id: e.id });
  });

  // 6. Pausen-Überschneidungen
  //    Wichtig: "freie" Pausen (kein work_day_id, also manuell auf einem
  //    reinen Buchungstag erfasst) liegen ABSICHTLICH innerhalb eines
  //    durchgehenden Zeiteintrags (z.B. 09-17 Uhr + Mittagspause 12-13 Uhr),
  //    damit sie von den gebuchten Stunden abgezogen werden. Das ist kein
  //    Fehler und wird daher nicht geprüft. Nur an einen Arbeitstag
  //    gebundene Pausen (Live-Tracking) sollten NIE einen Zeiteintrag
  //    überschneiden – das würde auf doppelt erfasste Zeit hindeuten.
  const allPauses = all("SELECT * FROM pauses WHERE end_time IS NOT NULL ORDER BY date, start_time");
  const pausesByDate = {};
  allPauses.forEach(p => { (pausesByDate[p.date]=pausesByDate[p.date]||[]).push(p); });
  for (const [date, dayPauses] of Object.entries(pausesByDate)) {
    const dayEntries2 = byDate[date] || [];
    for (const pz of dayPauses) {
      if (pz.work_day_id == null) continue; // freie Pause – Überschneidung ist gewollt
      for (const e of dayEntries2) {
        if (pz.start_time < e.end_time && e.start_time < pz.end_time) {
          issues.push({ type:"pause_overlap", severity:"warn", date,
            message:`Pause überschneidet sich mit Eintrag: Pause ${pz.start_time.slice(11,16)}–${pz.end_time.slice(11,16)}, Eintrag ${e.start_time.slice(11,16)}–${e.end_time.slice(11,16)}`,
            ids: [pz.id, e.id] });
        }
      }
    }
    // Pausen untereinander überschneidend (immer ein Fehler, unabhängig vom Typ)
    for (let i=0; i<dayPauses.length-1; i++) {
      const a = dayPauses[i], b = dayPauses[i+1];
      if (a.end_time > b.start_time) {
        issues.push({ type:"pause_overlap", severity:"warn", date,
          message:`Zwei Pausen überschneiden sich: ${a.start_time.slice(11,16)}–${a.end_time.slice(11,16)} und ${b.start_time.slice(11,16)}–${b.end_time.slice(11,16)}`,
          ids: [a.id, b.id] });
      }
    }
  }

  const summary = { total: issues.length,
    errors:   issues.filter(i=>i.severity==="error").length,
    warnings: issues.filter(i=>i.severity==="warn").length,
    infos:    issues.filter(i=>i.severity==="info").length };

  res.json({ issues, summary, checkedAt: now() });
});


/* ═══════════════════════════════════════════════
   TEMPO CSV IMPORT
   Format: offizieller Tempo-Benutzer-Export
   Felder: Issue Key, Hours, Work date, Username,
           Project Key, Project Name, Work Description,
           Issue Type (Vacation erkannt)
═══════════════════════════════════════════════ */
app.post("/api/tempo-csv-import", (req, res) => {
  const { csv, dryRun } = req.body;
  if (!csv?.trim()) return res.status(400).json({ error: "CSV fehlt" });
  const logCat = "import";

  // CSV parsen (Komma-getrennt, gequotet)
  function parseCSVLine(line) {
    const result = []; let cur = "", inQ = false;
    for (let i=0; i<line.length; i++) {
      const c = line[i];
      if (c==='"') { inQ=!inQ; }
      else if (c==="," && !inQ) { result.push(cur.trim()); cur=""; }
      else { cur+=c; }
    }
    result.push(cur.trim());
    return result;
  }

  const rawLines = csv.split(/\r?\n/).filter(l=>l.trim());
  if (!rawLines.length) return res.status(400).json({ error: "Leere CSV" });

  const header = parseCSVLine(rawLines[0]).map(h=>h.toLowerCase().replace(/"/g,""));
  const col = key => header.indexOf(key);

  const get = (row, key) => (row[col(key)]||"").replace(/^"|"$/g,"").trim();

  const stats = { total:0, imported:0, vacations:0, workdays:0,
    skipped_duplicate:0, errors:[], preview:[], dryRun:!!dryRun };

  writeLog("info", logCat, `Tempo CSV Import ${dryRun?"(Testlauf)":""}: ${rawLines.length-1} Zeilen`);

  // Alle Projekte cachen
  const allProjects = all("SELECT * FROM projects");
  const findProject = (key, name) => {
    if (!key && !name) return null;
    // 1. Exakter Key-Match
    let p = key ? allProjects.find(p => p.jira_key && p.jira_key.toUpperCase()===key.toUpperCase()) : null;
    // 2. Name-Match
    if (!p && name) p = allProjects.find(p => p.name.toLowerCase()===name.toLowerCase());
    return p;
  };

  // Zeilen parsen
  const entries = [];
  for (let i=1; i<rawLines.length; i++) {
    const line = rawLines[i].trim();
    if (!line) continue;
    const row = parseCSVLine(line);
    if (row.length < 5) continue;

    stats.total++;

    const issueKey   = get(row, "issue key");
    const issueSum   = get(row, "issue summary");
    const hoursStr   = get(row, "hours");
    const workDate   = get(row, "work date");    // "2026-05-04 07:38"
    const username   = get(row, "username");
    const projKey    = get(row, "project key");
    const projName   = get(row, "project name");
    const desc       = get(row, "work description").replace(/\n/g," ").replace(/  +/g," ");
    const issueType  = get(row, "issue type");

    if (!workDate || !hoursStr) { stats.errors.push({row:i, error:"Datum oder Stunden fehlen"}); continue; }

    // Datum + Startzeit
    const [datePart, timePart] = workDate.split(" ");
    const startH = timePart ? parseInt(timePart.split(":")[0]) : 8;
    const startM = timePart ? parseInt(timePart.split(":")[1]) : 0;

    // Stunden → Sekunden
    const hours   = parseFloat(hoursStr) || 0;
    const durSecs = Math.round(hours * 3600);

    // Start und Ende berechnen
    const startDate  = new Date(`${datePart}T${String(startH).padStart(2,"0")}:${String(startM).padStart(2,"0")}:00`);
    const endDate    = new Date(startDate.getTime() + durSecs * 1000);
    const startISO   = startDate.toISOString();
    const endISO     = endDate.toISOString();

    // Urlaub erkennen
    const isVacation = issueType.toLowerCase().includes("vacation") ||
                       issueKey.startsWith("HAP-") ||
                       projKey === "HAP";

    // Projekt suchen
    const project = isVacation ? null : findProject(projKey, projName);

    // Kommentar zusammenbauen
    const comment = [desc, issueSum && desc!==issueSum ? issueSum : ""].filter(Boolean).join(" · ").slice(0, 500);

    entries.push({
      date: datePart, startISO, endISO, durSecs, hours,
      issueKey, projKey, projName, comment, isVacation, project,
      startH, startM
    });
  }

  // Nach Datum gruppieren für Workday-Bestimmung
  const byDate = {};
  entries.forEach(e => { (byDate[e.date]=byDate[e.date]||[]).push(e); });

  for (const [date, dayEntries] of Object.entries(byDate).sort()) {
    // Urlaubstage
    const vacEntries = dayEntries.filter(e=>e.isVacation);
    const workEntries = dayEntries.filter(e=>!e.isVacation);

    // Urlaub importieren
    for (const v of vacEntries) {
      if (dryRun) {
        stats.preview.push({ date, type:"urlaub", hours:v.hours, note:"Urlaub (aus HAP)" });
        stats.vacations++;
        continue;
      }
      const existing = one("SELECT id FROM absences WHERE date_from=? AND type='urlaub'", date);
      if (!existing) {
        run("INSERT INTO absences (date_from,date_to,type,hours,note) VALUES (?,?,'urlaub',?,?)",
          date, date, v.hours, `Urlaub (${v.issueKey})`);
        stats.vacations++;
        writeLog("info", logCat, `Urlaub: ${date} ${v.hours}h`);
      }
    }

    if (!workEntries.length) continue;

    // Workday: früheste Startzeit bis letzte Endzeit
    const earliest = workEntries.reduce((a,b)=>a.startISO<b.startISO?a:b);
    const latest   = workEntries.reduce((a,b)=>a.endISO>b.endISO?a:b);

    if (dryRun) {
      stats.workdays++;
      stats.preview.push({
        date, type:"arbeitstag",
        wdStart: earliest.startISO.slice(11,16),
        wdEnd:   latest.endISO.slice(11,16),
        entries: workEntries.length,
        entries_detail: workEntries.map(e=>({
          start:  e.startISO.slice(11,16),
          end:    e.endISO.slice(11,16),
          ticket: e.issueKey,
          project:e.project?.name || e.projName,
          comment:e.comment.slice(0,60),
          durSecs:e.durSecs
        }))
      });
      stats.imported += workEntries.length;
      continue;
    }

    // Workday schreiben / erweitern
    const existingWd = one("SELECT * FROM work_days WHERE date=?", date);
    let wdId;
    if (!existingWd) {
      const r = run("INSERT INTO work_days (date,start_time,end_time) VALUES (?,?,?)",
        date, earliest.startISO, latest.endISO);
      wdId = r.lastInsertRowid;
      stats.workdays++;
      writeLog("info", logCat, `Arbeitstag ${date}: ${earliest.startISO.slice(11,16)}–${latest.endISO.slice(11,16)}`);
    } else {
      wdId = existingWd.id;
      if (earliest.startISO < existingWd.start_time)
        run("UPDATE work_days SET start_time=? WHERE id=?", earliest.startISO, wdId);
      if (!existingWd.end_time || latest.endISO > existingWd.end_time)
        run("UPDATE work_days SET end_time=? WHERE id=?", latest.endISO, wdId);
    }

    // Zeiteinträge
    for (const e of workEntries) {
      try {
        // Duplikat-Check: gleiche Startzeit + Projekt
        const dup = one(
          "SELECT id, task_id FROM time_entries WHERE start_time=? AND project_id IS ?",
          e.startISO, e.project?.id||null);
        if (dup) {
          // früher importierte Einträge kannten den Ticket-Key nicht → jetzt nachtragen
          if (!dup.task_id && e.issueKey && e.project?.id) {
            const tid = taskForJiraKey(e.project.id, e.issueKey, null);
            if (tid) { run("UPDATE time_entries SET task_id=? WHERE id=?", tid, dup.id); stats.relinked = (stats.relinked || 0) + 1; continue; }
          }
          stats.skipped_duplicate++; continue;
        }

        const csvTask = e.project?.id ? taskForJiraKey(e.project.id, e.issueKey, null) : null;
        run(`INSERT INTO time_entries
          (project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,source)
          VALUES (?,?,?,?,?,?,1,?,'tempo_csv')`,
          e.project?.id||null, csvTask, e.startISO, e.endISO, e.durSecs, e.comment||null, date);
        stats.imported++;
      } catch(err) {
        stats.errors.push({ date, time:e.startISO.slice(11,16), error:err.message });
        writeLog("error", logCat, `${date} ${e.startISO.slice(11,16)}: ${err.message}`);
      }
    }
  }

  const summary = dryRun
    ? `Testlauf: ${stats.workdays} Arbeitstage, ${stats.imported} Buchungen, ${stats.vacations} Urlaubstage`
    : `Importiert: ${stats.workdays} Arbeitstage, ${stats.imported} Buchungen, ${stats.vacations} Urlaubstage, ${stats.skipped_duplicate} Duplikate`;
  writeLog(stats.errors.length?"warn":"success", logCat, summary);
  res.json({ ...stats, message: summary });
});


/* Vollständiger Reset für einen Zeitraum
   Löscht work_days + time_entries + pauses + daily_logs
   Nutze das für Tempo-CSV Reimport */
app.delete("/api/admin/reset-range", (req, res) => {
  const { from, to } = req.query;
  if (!from || !to) return res.status(400).json({ error: "from + to erforderlich" });

  // Zählen
  const wdCount  = one("SELECT COUNT(*) as n FROM work_days WHERE date>=? AND date<=?", from, to)?.n || 0;
  const teCount  = one("SELECT COUNT(*) as n FROM time_entries WHERE date>=? AND date<=?", from, to)?.n || 0;
  const logCount = one("SELECT COUNT(*) as n FROM daily_logs WHERE date>=? AND date<=?", from, to)?.n || 0;

  // Löschen (Reihenfolge wegen Foreign Keys)
  run("DELETE FROM pauses WHERE date>=? AND date<=?", from, to);
  run("DELETE FROM daily_logs WHERE date>=? AND date<=?", from, to);
  run("DELETE FROM time_entries WHERE date>=? AND date<=?", from, to);
  run("DELETE FROM work_days WHERE date>=? AND date<=?", from, to);

  writeLog("info","import",`Reset ${from}–${to}: ${wdCount} Arbeitstage, ${teCount} Buchungen, ${logCount} Logs gelöscht`);
  res.json({ deleted: { workdays: wdCount, entries: teCount, logs: logCount } });
});

/* ═══════════════════════════════════════════════
   SSH-SITZUNGEN  (v4.8.0)
   Quelle: ~/.zeitprotokoll/ssh_sessions.log, geschrieben von
   scripts/ssh-log.zsh (zsh-Funktion um "ssh").
═══════════════════════════════════════════════ */
const SSH_LOG = process.env.SSH_LOG || path.join(process.env.HOME||"/tmp", ".zeitprotokoll/ssh_sessions.log");
const SSH_MAX_OPEN_S = 24*3600;   // offene Sitzung älter als 24h → Ende unbekannt

// Lokales Datum (Zeitzone des Macs), nicht UTC
function localDateOf(ms) {
  const d = new Date(ms);
  return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,"0")}-${String(d.getDate()).padStart(2,"0")}`;
}

function importSshLog() {
  if (!fs.existsSync(SSH_LOG)) return { ok:true, file:SSH_LOG, exists:false, started:0, ended:0 };
  // Nur neue Bytes seit dem letzten Import lesen (Offset in settings)
  const size = fs.statSync(SSH_LOG).size;
  let offset = parseInt(one("SELECT value FROM settings WHERE key='ssh_log_offset'")?.value || "0");
  if (offset > size) offset = 0;                     // Datei neu angelegt/gekürzt
  if (offset === size) { const assigned = autoAssignSsh(); return { ok:true, file:SSH_LOG, exists:true, started:0, ended:0, assigned }; }
  const fd = fs.openSync(SSH_LOG, "r");
  const buf = Buffer.alloc(size - offset);
  fs.readSync(fd, buf, 0, buf.length, offset);
  fs.closeSync(fd);
  let text = buf.toString("utf8");
  const lastNl = text.lastIndexOf("\n");
  if (lastNl < 0) return { ok:true, file:SSH_LOG, exists:true, started:0, ended:0, assigned:0 };
  text = text.slice(0, lastNl);                      // unvollständige letzte Zeile beim nächsten Mal
  const newOffset = offset + Buffer.byteLength(text, "utf8") + 1;
  const lines = text.split("\n");
  let started = 0, ended = 0;
  const insS = db.prepare(`INSERT OR IGNORE INTO ssh_sessions
      (session_uid,date,start_time,target,hostname,username,port,remote_cmd)
      VALUES (?,?,?,?,?,?,?,?)`);
  const updE = db.prepare(`UPDATE ssh_sessions SET end_time=?, duration_seconds=?, exit_code=?
      WHERE session_uid=? AND end_time IS NULL`);
  tx(() => {
    for (const l of lines) {
      if (!l) continue;
      const f = l.split("\t");
      if (f[0] === "S" && f.length >= 4) {
        const ms = parseInt(f[2]) * 1000;
        if (!ms) continue;
        const r = insS.run(f[1], localDateOf(ms), new Date(ms).toISOString(),
          f[3]||"", f[4]||"", f[5]||"", parseInt(f[6])||null, f[7]||"");
        started += r.changes;
      } else if (f[0] === "E" && f.length >= 3) {
        const endMs = parseInt(f[2]) * 1000;
        const s = one("SELECT start_time FROM ssh_sessions WHERE session_uid=?", f[1]);
        if (!s || !endMs) continue;
        const dur = Math.max(0, Math.round((endMs - Date.parse(s.start_time))/1000));
        const r = updE.run(new Date(endMs).toISOString(), dur, parseInt(f[3]), f[1]);
        ended += r.changes;
      }
    }
    run("INSERT OR REPLACE INTO settings (key,value) VALUES ('ssh_log_offset',?)", String(newOffset));
  });
  const assigned = autoAssignSsh();
  if (started || ended) writeLog("info","ssh",`SSH-Import: ${started} neu, ${ended} beendet, ${assigned} automatisch zugeordnet`);
  return { ok:true, file:SSH_LOG, exists:true, started, ended, assigned };
}

// Sitzungen ohne Projekt dem Projekt/Ticket zuordnen, dessen Timer zu Sitzungsbeginn lief
function autoAssignSsh() {
  let n = 0;
  const open = all("SELECT id,start_time FROM ssh_sessions WHERE project_id IS NULL AND assign_source IS NULL");
  if (!open.length) return 0;
  const t = one("SELECT * FROM timer_state WHERE id=1");
  for (const s of open) {
    let hit = one(`SELECT project_id,task_id FROM time_entries
                   WHERE project_id IS NOT NULL AND start_time<=? AND end_time>? ORDER BY start_time DESC LIMIT 1`,
                  s.start_time, s.start_time);
    if (!hit && t?.start_time && t.project_id && t.start_time <= s.start_time) hit = t;
    if (hit) {
      run("UPDATE ssh_sessions SET project_id=?,task_id=?,assign_source='timer' WHERE id=?",
          hit.project_id, hit.task_id||null, s.id);
      n++;
    }
  }
  return n;
}

app.post("/api/ssh-sessions/import", (_, res) => {
  try { res.json(importSshLog()); }
  catch(e) { writeLog("error","ssh",`SSH-Import fehlgeschlagen: ${e.message}`); res.status(500).json({error:e.message}); }
});

/* Ein-/Ausschalter für die SSH-Zeiterfassung: legt nur eine Flag-Datei an/weg,
   die die zsh-Funktion (scripts/ssh-log.zsh) bei jedem "ssh"-Aufruf selbst prüft
   - kein Netzwerk-Roundtrip pro SSH-Verbindung nötig. */
const SSH_TRACK_FLAG = path.join(path.dirname(SSH_LOG), "ssh_tracking_disabled");
app.get("/api/ssh-tracking", (_, res) => {
  res.json({ enabled: !fs.existsSync(SSH_TRACK_FLAG) });
});
app.post("/api/ssh-tracking", (req, res) => {
  const enabled = req.body?.enabled !== false;
  try {
    fs.mkdirSync(path.dirname(SSH_TRACK_FLAG), { recursive: true });
    if (enabled) { if (fs.existsSync(SSH_TRACK_FLAG)) fs.unlinkSync(SSH_TRACK_FLAG); }
    else fs.writeFileSync(SSH_TRACK_FLAG, `deaktiviert ${now()}\n`);
    writeLog("info","ssh",`SSH-Zeiterfassung ${enabled?"aktiviert":"deaktiviert"}`);
    res.json({ enabled });
  } catch(e) { res.status(500).json({error:e.message}); }
});

app.get("/api/ssh-sessions", (req, res) => {
  try { importSshLog(); } catch(_) {}
  const from = req.query.from || localDateOf(Date.now()).slice(0,8)+"01";
  const to   = req.query.to   || localDateOf(Date.now());
  const nowMs = Date.now();
  const rows = all(`SELECT s.*, p.name AS project_name, p.jira_key AS project_key,
                            t.title AS task_title, t.jira_ticket AS task_ticket
                     FROM ssh_sessions s
                     LEFT JOIN projects p ON p.id=s.project_id
                     LEFT JOIN tasks t    ON t.id=s.task_id
                     WHERE s.date BETWEEN ? AND ? ORDER BY s.start_time`, from, to)
    .map(r => {
      const open = !r.end_time;
      const age  = (nowMs - Date.parse(r.start_time))/1000;
      const window_s = open ? (age > SSH_MAX_OPEN_S ? 0 : Math.round(age)) : (r.duration_seconds||0);
      return { ...r,
        status: open ? (age > SSH_MAX_OPEN_S ? "unknown" : "open") : "closed",
        // gemessene Aktivzeit (Eingaben am Terminal), sonst Fensterzeit; offene Sitzungen laufen live mit, „unbekannt“ zählt 0
        effective_seconds: sshWatch.effectiveSeconds(r, nowMs, SSH_MAX_OPEN_S),
        window_seconds: window_s, measured: (r.samples||0) > 0,
        server: r.hostname || r.target };
    });
  const agg = (keyFn) => {
    const m = new Map();
    for (const r of rows) {
      const k = keyFn(r);
      const a = m.get(k) || { key:k, count:0, seconds:0, first:r.start_time, last:r.end_time||r.start_time, servers:new Set() };
      a.count++; a.seconds += r.effective_seconds;
      if (r.start_time < a.first) a.first = r.start_time;
      const e = r.end_time || r.start_time; if (e > a.last) a.last = e;
      a.servers.add(r.server);
      m.set(k, a);
    }
    return [...m.values()].map(a => ({ ...a, servers:[...a.servers] }));
  };
  res.json({
    from, to, log_file: SSH_LOG, log_exists: fs.existsSync(SSH_LOG),
    sessions: rows,
    byHost: agg(r => r.server).sort((a,b) => b.seconds - a.seconds),
    byDay:  agg(r => r.date).sort((a,b) => a.key < b.key ? -1 : 1),
    byProject: agg(r => r.project_name || "(ohne Projekt)").sort((a,b) => b.seconds - a.seconds),
    total_seconds: rows.reduce((s,r) => s + r.effective_seconds, 0),
  });
});

app.put("/api/ssh-sessions/:id", (req, res) => {
  const b = req.body || {};
  const f = [], p = [];
  if (b.note !== undefined)       { f.push("note=?");       p.push(b.note || null); }
  if (b.project_id !== undefined) { f.push("project_id=?","assign_source=?"); p.push(b.project_id ? intId(b.project_id) : null, b.project_id ? "manual" : null); }
  if (b.task_id !== undefined)    { f.push("task_id=?");    p.push(b.task_id ? intId(b.task_id) : null); }
  if (f.length) { p.push(intId(req.params.id)); run(`UPDATE ssh_sessions SET ${f.join(",")} WHERE id=?`, ...p); }
  res.json({ ok:true });
});

// Eine oder mehrere Sitzungen als Zeiteintrag(e) übernehmen
// body: { ids:[...], project_id?, task_id?, comment? }
app.post("/api/ssh-sessions/to-entries", (req, res) => {
  const { ids = [], project_id, task_id, comment, merge } = req.body || {};
  const created = [], skipped = [];
  if (merge) {
    // mehrere kurze Sitzungen (z. B. „kurz auf einem anderen Server nachgeschaut“) → EINE Buchung je Tag
    const list = ids.map(id => one("SELECT * FROM ssh_sessions WHERE id=?", intId(id))).filter(s => {
      const ok = s && s.end_time && !s.time_entry_id && s.assign_source !== "timer"; if (!ok && s) skipped.push(s.id); return ok; });
    const byDay = {}; for (const s of list) (byDay[s.date] ||= []).push(s);
    const pid0 = project_id ? intId(project_id) : list.find(s => s.project_id)?.project_id;
    const tid0 = task_id ? intId(task_id) : (list.find(s => s.task_id && s.project_id === pid0)?.task_id || null);
    if (!pid0) return res.status(400).json({ error: "Bitte erst einer der Sitzungen ein Projekt zuordnen (oder Projekt wählen)" });
    tx(() => {
      for (const [day, ss] of Object.entries(byDay)) {
        ss.sort((a, b) => a.start_time.localeCompare(b.start_time));
        const eff = ss.reduce((a, s) => a + Math.max(1, Math.round(sshWatch.effectiveSeconds(s))), 0);
        const hosts = [...new Set(ss.map(s => s.hostname || s.target))];
        const text = comment || `SSH ${hosts.slice(0, 4).join(", ")}${hosts.length > 4 ? ` +${hosts.length - 4}` : ""} (${ss.length} Sitzungen)`;
        const start = ss[0].start_time, endIso = new Date(Date.parse(start) + eff * 1000).toISOString();
        const r = run(`INSERT INTO time_entries (project_id,task_id,start_time,end_time,duration_seconds,comment,date,source) VALUES (?,?,?,?,?,?,?,'ssh')`,
          pid0, tid0, start, endIso, eff, text, day);
        for (const s of ss) run("UPDATE ssh_sessions SET time_entry_id=?, project_id=?, task_id=? WHERE id=?", r.lastInsertRowid, pid0, tid0, s.id);
        created.push(r.lastInsertRowid);
      }
    });
    return res.json({ ok: true, created, skipped, merged: true });
  }
  tx(() => {
    for (const id of ids) {
      const s = one("SELECT * FROM ssh_sessions WHERE id=?", intId(id));
      if (!s || !s.end_time || s.time_entry_id || s.assign_source === "timer") { skipped.push(id); continue; }
      const pid = project_id ? intId(project_id) : s.project_id;
      const tid = task_id    ? intId(task_id)    : s.task_id;
      const text = comment || `SSH ${s.hostname || s.target}${s.remote_cmd ? " ("+s.remote_cmd.slice(0,60)+")" : ""}${s.note ? " – "+s.note : ""}`;
      // gemessene Aktivzeit statt Fensterzeit; der Eintrag läuft dann vom Start bis Start + aktive Zeit
      const eff = Math.max(1, Math.round(sshWatch.effectiveSeconds(s)));
      const endIso = (s.samples||0) > 0 ? new Date(Date.parse(s.start_time) + eff*1000).toISOString() : s.end_time;
      const r = run(`INSERT INTO time_entries (project_id,task_id,start_time,end_time,duration_seconds,comment,date,source)
                     VALUES (?,?,?,?,?,?,?,'ssh')`,
                    pid || null, tid || null, s.start_time, endIso, eff, text, s.date);
      run("UPDATE ssh_sessions SET time_entry_id=?, project_id=?, task_id=? WHERE id=?", r.lastInsertRowid, pid||null, tid||null, s.id);
      created.push(r.lastInsertRowid);
    }
  });
  res.json({ ok:true, created, skipped });
});

app.delete("/api/ssh-sessions/:id", (req, res) => {
  run("DELETE FROM ssh_sessions WHERE id=?", intId(req.params.id));
  res.json({ ok:true });
});

/* ═══════════════════════════════════════════════
   STUNDENZETTEL (Woche/Monat)  (v4.9.0)
   Gruppiert nach lokalem Datum (Zeitzone des Macs).
═══════════════════════════════════════════════ */
app.get("/api/timesheet", (req, res) => {
  const { from, to } = req.query;
  if (!from || !to) return res.status(400).json({ error: "from + to erforderlich" });
  const settings = getSettings();
  const target = d => parseFloat(settings[["target_sun","target_mon","target_tue","target_wed","target_thu","target_fri","target_sat"][d]]) || 0;
  const shift = (s, n) => { const d = new Date(s + "T12:00:00"); d.setDate(d.getDate() + n); return localDateOf(d.getTime()); };

  // Tage
  const dates = [];
  for (let d = from; d <= to; d = shift(d, 1)) dates.push(d);
  const days = {};
  for (const date of dates) {
    const dow = new Date(date + "T12:00:00").getDay();
    days[date] = { date, dow, target_h: target(dow), first_start: null, last_end: null,
                   work_seconds: 0, pause_seconds: 0, ssh_seconds: 0, absence: null, absence_h: 0, activities: {} };
  }

  // Zeiteinträge (±1 Tag holen, dann nach lokalem Datum zuordnen)
  const entries = all(`SELECT e.*, p.name AS project_name, p.jira_key AS project_key,
                              t.title AS task_title, t.jira_ticket AS task_ticket, c.name AS customer_name
                       FROM time_entries e
                       LEFT JOIN projects p ON p.id=e.project_id
                       LEFT JOIN customers c ON c.id=p.customer_id
                       LEFT JOIN tasks t ON t.id=e.task_id
                       WHERE e.date BETWEEN ? AND ? ORDER BY e.start_time`, shift(from, -1), shift(to, 1));
  const rows = new Map();
  for (const e of entries) {
    if (e.is_absence) continue;   // Tempo-Abwesenheitsprojekt (HAP) zählt nicht als Arbeitszeit
    const date = localDateOf(Date.parse(e.start_time));
    const day = days[date]; if (!day) continue;
    const sec = e.duration_seconds || (e.end_time ? Math.round((Date.parse(e.end_time) - Date.parse(e.start_time)) / 1000) : 0);
    day.work_seconds += sec;
    if (!day.first_start || e.start_time < day.first_start) day.first_start = e.start_time;
    const end = e.end_time || e.start_time;
    if (!day.last_end || end > day.last_end) day.last_end = end;
    const key = `${e.project_id || 0}|${e.task_id || 0}`;
    const r = rows.get(key) || { key, project_id: e.project_id, task_id: e.task_id,
      project: e.project_name || "Ohne Projekt", project_key: e.project_key || "", customer: e.customer_name || "",
      ticket: e.task_ticket || e.task_title || "", days: {}, comments: {}, total_seconds: 0 };
    r.days[date] = (r.days[date] || 0) + sec;
    r.total_seconds += sec;
    if (e.comment) { const c = (r.comments[date] ||= []); if (!c.includes(e.comment)) c.push(e.comment); }
    rows.set(key, r);
    const act = day.activities[r.key] ||= { label: r.project + (r.ticket ? " · " + r.ticket : ""), seconds: 0 };
    act.seconds += sec;
  }

  // Pausen
  const pauses = all("SELECT * FROM pauses WHERE date BETWEEN ? AND ? AND end_time IS NOT NULL", shift(from, -1), shift(to, 1));
  for (const p of pauses) {
    const day = days[localDateOf(Date.parse(p.start_time))]; if (!day) continue;
    day.pause_seconds += Math.max(0, Math.round((Date.parse(p.end_time) - Date.parse(p.start_time)) / 1000));
  }
  // SSH (nur Info)
  try {
    for (const s of all("SELECT date, start_time, end_time, duration_seconds, samples, active_seconds FROM ssh_sessions WHERE date BETWEEN ? AND ?", from, to))
      if (days[s.date]) days[s.date].ssh_seconds += sshWatch.effectiveSeconds(s);
  } catch (_) {}
  // Abwesenheiten
  for (const a of all("SELECT * FROM absences WHERE date_to >= ? AND date_from <= ?", from, to)) {
    for (const date of dates) if (date >= a.date_from && date <= a.date_to) {
      const d = days[date]; d.absence = { type: a.type, note: a.note };
      d.absence_h = a.hours != null ? a.hours : d.target_h;
    }
  }
  const todayLocal = localDateOf(Date.now());
  const dayList = dates.map(date => {
    const d = days[date];
    const ist_h = d.work_seconds / 3600 + (d.absence ? d.absence_h : 0);
    const future = date > todayLocal;               // Zukunft zählt nicht ins +/−
    return { ...d, future, activities: Object.values(d.activities).sort((a, b) => b.seconds - a.seconds),
             ist_h: Math.round(ist_h * 100) / 100, balance_h: future ? 0 : Math.round((ist_h - d.target_h) * 100) / 100 };
  });
  const rowList = [...rows.values()].sort((a, b) => (a.project + a.ticket).localeCompare(b.project + b.ticket, "de"));
  res.json({ from, to, days: dayList, rows: rowList,
    totals: { work_seconds: dayList.reduce((s, d) => s + d.work_seconds, 0),
              pause_seconds: dayList.reduce((s, d) => s + d.pause_seconds, 0),
              target_h: dayList.reduce((s, d) => s + d.target_h, 0),
              target_to_date_h: dayList.filter(d => !d.future).reduce((s, d) => s + d.target_h, 0),
              ist_h: Math.round(dayList.reduce((s, d) => s + d.ist_h, 0) * 100) / 100,
              balance_h: Math.round(dayList.reduce((s, d) => s + d.balance_h, 0) * 100) / 100 } });
});

/* ═══ PLANE-ANBINDUNG (Modul backend/plane.js) ═══ */
const audit = require("./audit")(app, { one, all, run, writeLog, DB_PATH, execSql: sql => db.exec(sql) });
app.get("/api/security/tls", (_, res) => res.json({ candidate: secure.candidate(), trusted: secure.trusted(), server: secure.serverInfo() }));
app.post("/api/security/trust-server", (req, res) => { try { res.json(secure.trustServer(String(req.body?.host || ""), String(req.body?.fingerprint || ""))); } catch (e) { res.status(400).json({ error: e.message }); } });
app.post("/api/security/untrust-server", (req, res) => res.json(secure.untrustServer(String(req.body?.host || ""))));
app.post("/api/security/trust-root", (req, res) => { try { res.json(secure.trustRoot(String(req.body?.fingerprint || ""))); } catch (e) { res.status(400).json({ error: e.message }); } });
const plane = require("./plane")(app, { one, all, run, getSettings, writeLog, localDateOf, secure, hooks }); hooks.plane = plane;
/* ═══ TEMPO-AUSGANG (Modul backend/tempo-push.js) ═══ */
const tempoPush = require("./tempo-push")(app, { one, all, run, getSettings, writeLog, localDateOf, secure, hooks }); hooks.tempoPushRef = tempoPush;
const sources = require("./sources")(app, { one, all, run, getSettings, writeLog, plane, tempoPush }); hooks.sources = sources;
/* ═══ SSH-AKTIVITÄT (Modul backend/ssh-watch.js) ═══ */
require("./assistant")(app, { one, all, run, getSettings, writeLog, hooks });
hooks.checks = require("./checks")(app, { one, all, run, writeLog, localDateOf });
hooks.presence = require("./presence")(app, { one, all, run, getSettings, localDateOf });
hooks.tempoSync = require("./tempo-sync")(app, { one, all, run, getSettings, writeLog, localDateOf, tempoPush, hooks, createBackup });
pairing.routes(app, { self: async (m, p) => { const r = await fetch(`http://127.0.0.1:${PORT}/api${p}`, { method: m }); return r.json(); } });
hooks.todo = require("./todo")(app, { one, all, run, getSettings, writeLog, localDateOf, tempoPush, hooks, PORT });
hooks.absences = require("./absence-sync")(app, { one, all, run, getSettings, writeLog, localDateOf });
hooks.monthend = require("./monthend")(app, { one, all, run, getSettings, localDateOf, hooks, balanceAt });
hooks.cleanup = require("./cleanup")(app, { one, all, run, getSettings, writeLog, hooks, tempoPush, createBackup });
require("./jira")(app, { one, all, run, getSettings, writeLog, tempoPush, hooks });
const sshWatch = require("./ssh-watch")(app, { one, all, run, getSettings, writeLog, importSshLog });
/* ═══ ADMIN-AKTIONEN (Modul backend/admin.js) ═══ */
const meetings = require("./meetings")(app, { one, all, run, getSettings, writeLog, localDateOf, onEntryChanged: () => syncAuto(), hooks });
require("./admin")(app, { one, all, run, getSettings, writeLog, DB_PATH, BAK_DIR, INST_DIR, PORT, VERSION, createBackup, closeDb: () => db.close(), execSql: sql => db.exec(sql), sshWatch, audit, secure });
function syncAuto() { try { plane.scheduleAuto(); } catch (_) {} try { tempoPush.scheduleAuto(); } catch (_) {} }

/* ═══════════════════════════════════════════════
   TÄTIGKEITSARTEN (Liste in settings.activities, JSON)
═══════════════════════════════════════════════ */
const DEFAULT_ACTIVITIES = ["Entwicklung","Konzeption","Meeting","Support","Administration","Dokumentation","Fahrzeit"];
function activityList() {
  try { const a = JSON.parse(getSettings().activities || "null"); if (Array.isArray(a)) return a.filter(x => typeof x === "string" && x.trim()); } catch (_) {}
  return DEFAULT_ACTIVITIES;
}
app.get("/api/activities", (_, res) => res.json(activityList()));
app.put("/api/activities", (req, res) => {
  const list = (Array.isArray(req.body) ? req.body : []).map(x => String(x).trim()).filter(Boolean).slice(0, 40);
  run("INSERT OR REPLACE INTO settings (key,value) VALUES ('activities',?)", JSON.stringify([...new Set(list)]));
  res.json(activityList());
});

/* ═══════════════════════════════════════════════
   WOCHENRASTER (wie Tempo-Timesheet): Zeilen = Ticket/Projekt, Spalten = Tage
═══════════════════════════════════════════════ */
const GRID_LOCKED = ["tempo","tempo_csv","plane"];   // importierte Buchungen nicht über das Raster ändern
app.get("/api/grid", (req, res) => {
  const { from, to } = req.query;
  if (!from || !to) return res.status(400).json({ error: "from + to erforderlich" });
  const rows = new Map();
  // System je Zeile: Plane (Work-Item bzw. Plane-Projektkürzel), Jira (Ticketnummer), sonst ohne Ticket
  const planeIdents = new Set(all("SELECT UPPER(plane_identifier) AS i FROM projects WHERE plane_project_id IS NOT NULL AND plane_identifier IS NOT NULL").map(r => r.i));
  for (const e of all(`SELECT e.*, t.jira_ticket, t.title AS task_title, t.plane_work_item_id AS task_wi, p.name AS project_name
                       FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id LEFT JOIN projects p ON p.id=e.project_id
                       WHERE e.date>=? AND e.date<=? AND e.end_time IS NOT NULL AND e.duration_seconds > 0`, from, to)) {
    const k = `${e.project_id || 0}|${e.task_id || 0}`;
    const key = String(e.jira_ticket || "").toUpperCase();
    const system = !/^[A-Z][A-Z0-9_]*-\d+$/.test(key) ? null : (e.task_wi || planeIdents.has(key.split("-")[0])) ? "plane" : "jira";
    if (!rows.has(k)) rows.set(k, { key: k, project_id: e.project_id, task_id: e.task_id, ticket: e.jira_ticket || null, system, absence: !!e.is_absence,
      title: e.task_title || null, project_name: e.project_name || null, cells: {}, locked: {}, items: {}, total: 0 });
    const r = rows.get(k);
    r.cells[e.date] = (r.cells[e.date] || 0) + e.duration_seconds;
    (r.items[e.date] ||= []).push({ id: e.id, start_time: e.start_time, end_time: e.end_time, duration_seconds: e.duration_seconds, comment: e.comment || "", source: e.source || "", tempo: !!e.tempo_push_id, account: e.tempo_account || null });
    if (GRID_LOCKED.includes(e.source)) r.locked[e.date] = true;
    r.total += e.duration_seconds;
  }
  res.json({ from, to, rows: [...rows.values()].sort((a, b) => b.total - a.total) });
});

/* Zelle auf eine Gesamtdauer setzen: mehr → neuer Eintrag, weniger → letzte Einträge kürzen/löschen */
app.put("/api/grid/cell", (req, res) => {
  const { date, project_id, task_id, seconds, activity } = req.body || {};
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date || "")) return res.status(400).json({ error: "Datum fehlt" });
  if (!project_id) return res.status(400).json({ error: "Projekt fehlt" });
  const target = Math.max(0, Math.round(Number(seconds) || 0));
  if (target > 24 * 3600) return res.status(400).json({ error: "Mehr als 24 Stunden an einem Tag" });
  const pid = intId(project_id), tid = task_id ? intId(task_id) : null;
  const list = all(`SELECT * FROM time_entries WHERE date=? AND project_id=? AND task_id IS ? AND end_time IS NOT NULL ORDER BY start_time`, date, pid, tid);
  const fixed = list.filter(e => GRID_LOCKED.includes(e.source)).reduce((s, e) => s + (e.duration_seconds || 0), 0);
  const editable = list.filter(e => !GRID_LOCKED.includes(e.source));
  const current = fixed + editable.reduce((s, e) => s + (e.duration_seconds || 0), 0);
  if (target < fixed) return res.status(400).json({ error: `Mindestens ${Math.round(fixed/60)} min stammen aus Tempo/Plane und lassen sich hier nicht ändern` });
  tx(() => {
    if (target > current) {
      // neuen Block hinter die letzte Buchung des Tages legen (sonst 08:00 Uhr)
      const last = one("SELECT MAX(end_time) AS m FROM time_entries WHERE date=? AND end_time IS NOT NULL", date)?.m;
      let start = last ? new Date(last) : new Date(`${date}T08:00:00`);
      if (dateOfIso(start.toISOString()) !== date) start = new Date(`${date}T08:00:00`);
      const add = target - current, end = new Date(start.getTime() + add * 1000);
      run(`INSERT INTO time_entries (project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,source,activity)
           VALUES (?,?,?,?,?,NULL,1,?,'grid',?)`, pid, tid, start.toISOString(), end.toISOString(), add, date, activity || null);
    } else if (target < current) {
      let cut = current - target;
      for (const e of editable.slice().reverse()) {
        if (cut <= 0) break;
        const d = e.duration_seconds || 0;
        if (d <= cut) { try { plane.beforeDelete(e.id); } catch (_) {} try { tempoPush.beforeDelete(e.id); } catch (_) {} run("DELETE FROM time_entries WHERE id=?", e.id); cut -= d; }
        else {
          const nd = d - cut;
          run("UPDATE time_entries SET duration_seconds=?, end_time=? WHERE id=?", nd, new Date(Date.parse(e.start_time) + nd * 1000).toISOString(), e.id);
          cut = 0;
        }
      }
    }
  });
  syncAuto();
  res.json({ ok: true, seconds: target });
});

/* ═══════════════════════════════════════════════
   FEHLENDE STUNDEN (vergangene Arbeitstage unter Soll)
═══════════════════════════════════════════════ */
app.get("/api/missing-hours", (req, res) => {
  const settings = getSettings();
  const days = Math.min(62, Math.max(1, parseInt(req.query.days) || 14));
  const minGap = Math.max(0, parseFloat(req.query.min_h ?? "0.25"));
  const target = dow => parseFloat(settings[["target_sun","target_mon","target_tue","target_wed","target_thu","target_fri","target_sat"][dow]]) || 0;
  const trackFrom = settings.tracking_start_date || one("SELECT MIN(date) AS d FROM time_entries")?.d || today();
  const t0 = today(), out = [];
  for (let i = days; i >= 1; i--) {
    const d = new Date(); d.setHours(12, 0, 0, 0); d.setDate(d.getDate() - i);
    const date = localDateOf(d.getTime());
    if (date < trackFrom || date >= t0) continue;
    if (one("SELECT 1 AS x FROM month_closings WHERE year=? AND month=?", parseInt(date.slice(0, 4)), parseInt(date.slice(5, 7)))) continue;   // Monat abgeschlossen → keine Fehlzeit-Meldung
    const soll = target(d.getDay());
    if (!soll) continue;
    const abs = one("SELECT * FROM absences WHERE date_from<=? AND date_to>=? LIMIT 1", date, date);
    const absH = abs ? (abs.hours != null ? abs.hours : soll) : 0;
    const ist = (one("SELECT COALESCE(SUM(duration_seconds),0) AS s FROM time_entries WHERE date=? AND end_time IS NOT NULL AND COALESCE(is_absence,0)=0", date)?.s || 0) / 3600;
    const missing = Math.round((soll - absH - ist) * 100) / 100;
    if (missing >= minGap) out.push({ date, dow: d.getDay(), soll_h: soll, ist_h: Math.round(ist * 100) / 100, absence_h: absH, missing_h: missing, empty: ist === 0 });
  }
  const total = Math.round(out.reduce((a, b) => a + b.missing_h, 0) * 100) / 100;
  let bal = null; try { bal = balanceAt(localDateOf(Date.now() - 864e5)); } catch (_) {}
  res.json({ days: out, total_missing_h: total, balance_h: bal, optional: bal != null && total > 0 && bal >= total });
});

/* ═══════════════════════════════════════════════
   SCHÄTZUNG PRO TICKET (lokal, in tasks.estimated_hours)
═══════════════════════════════════════════════ */
app.get("/api/ticket-estimates", (_, res) => {
  const rows = all(`SELECT UPPER(TRIM(t.jira_ticket)) AS ticket, MAX(t.estimated_hours) AS est,
                           (SELECT COALESCE(SUM(e.duration_seconds),0) FROM time_entries e JOIN tasks t2 ON t2.id=e.task_id
                              WHERE UPPER(TRIM(t2.jira_ticket))=UPPER(TRIM(t.jira_ticket)) AND e.end_time IS NOT NULL) AS secs
                    FROM tasks t WHERE t.jira_ticket IS NOT NULL AND TRIM(t.jira_ticket)<>'' GROUP BY 1`);
  res.json(Object.fromEntries(rows.map(r => [r.ticket, { estimate_h: r.est || 0, total_secs: r.secs || 0 }])));
});
app.put("/api/ticket-estimates/:ticket", (req, res) => {
  const key = String(req.params.ticket || "").trim().toUpperCase();
  const h = Math.max(0, Math.min(10000, parseFloat(String(req.body?.hours ?? "0").replace(",", ".")) || 0));
  const r = run("UPDATE tasks SET estimated_hours=? WHERE UPPER(TRIM(jira_ticket))=?", h, key);
  if (!r.changes) return res.status(404).json({ error: `Ticket ${key} ist lokal noch nicht bekannt – erst einmal darauf buchen` });
  res.json({ ok: true, ticket: key, estimate_h: h });
});

/* ═══════════════════════════════════════════════
   SERVER-ANSICHT: Version, Laufzeit, Datenbank, Sicherungen, Plane
═══════════════════════════════════════════════ */
const STARTED_AT = new Date().toISOString();
app.get("/api/admin/server-info", (_, res) => {
  const stat = f => { try { const s = fs.statSync(f); return { size: s.size, mtime: s.mtime.toISOString() }; } catch (_) { return null; } };
  let backups = [];
  try { backups = fs.readdirSync(BAK_DIR).filter(f => f.endsWith(".db")).sort(); } catch (_) {}
  const lastBackup = backups.length ? backups[backups.length - 1] : null;
  const distIndex = path.join(__dirname, "../frontend/dist/index.html");
  const cnt = sql => { try { return one(sql)?.n || 0; } catch (_) { return null; } };
  const s = getSettings();
  const planeSync = (() => { try { return one("SELECT MAX(synced_at) AS t, COUNT(*) AS n, SUM(CASE WHEN error IS NOT NULL THEN 1 ELSE 0 END) AS e FROM plane_ticket_sync"); } catch (_) { return null; } })();
  let pkgFront = null; try { pkgFront = JSON.parse(fs.readFileSync(path.join(__dirname, "../frontend/package.json"), "utf8")).version; } catch (_) {}
  res.json({
    version: VERSION, frontend_version: pkgFront, mode: MODE, port: Number(PORT),
    started_at: STARTED_AT, uptime_s: Math.round(process.uptime()), pid: process.pid,
    node: process.version, platform: `${process.platform} ${process.arch}`, hostname: require("os").hostname(),
    memory_mb: Math.round(process.memoryUsage().rss / 1048576),
    install_dir: INST_DIR, frontend_built: stat(distIndex)?.mtime || null,
    db: { path: DB_PATH, ...(stat(DB_PATH) || {}) },
    backups: { dir: BAK_DIR, count: backups.length, last: lastBackup, last_time: lastBackup ? stat(path.join(BAK_DIR, lastBackup))?.mtime : null },
    counts: { entries: cnt("SELECT COUNT(*) AS n FROM time_entries"), projects: cnt("SELECT COUNT(*) AS n FROM projects"),
              tasks: cnt("SELECT COUNT(*) AS n FROM tasks"), absences: cnt("SELECT COUNT(*) AS n FROM absences"),
              ssh_sessions: cnt("SELECT COUNT(*) AS n FROM ssh_sessions") },
    first_entry: one("SELECT MIN(date) AS d FROM time_entries")?.d || null,
    plane: { configured: !!(s.plane_base_url && s.plane_api_key && s.plane_workspace), url: s.plane_base_url || null,
             workspace: s.plane_workspace || null, mode: s.plane_sync_mode || "manual",
             tickets: planeSync?.n || 0, errors: planeSync?.e || 0, last_sync: planeSync?.t || null },
    ssh_log: { path: SSH_LOG, ...(stat(SSH_LOG) || {}) },
    log_files: { stdout: "/tmp/zeitprotokoll.log", stderr: "/tmp/zeitprotokoll-err.log",
                 desktop: path.join(process.env.HOME || "", ".zeitprotokoll/desktop.log") },
  });
});

app.get("/api/health", (_, res) => res.json({ok:true, version:VERSION}));

/* ═══════════════════════════════════════════════
   STATIC + FALLBACK
═══════════════════════════════════════════════ */
const STATIC = path.join(__dirname, "../frontend/dist");
if (fs.existsSync(STATIC)) {
  app.use(express.static(STATIC));
  app.get("*", (req,res) => {
    if (req.path.startsWith("/api")) return res.status(404).json({error:"Not found"});
    res.sendFile(path.join(STATIC,"index.html"));
  });
}

/* ═══════════════════════════════════════════════
   START
═══════════════════════════════════════════════ */

/* ═══════════════════════════════════════════════
   DB STARTUP + SCHEMA
═══════════════════════════════════════════════ */
function startServer() {
  db = createDB();

  // Schema erstellen (MUSS vor den Migrationen laufen – sonst schlägt z.B.
  // bei einer frischen/leeren DB die ALTER-/UPDATE-Migration fehl, weil die
  // betroffenen Tabellen noch gar nicht existieren)
  db.exec(`
    CREATE TABLE IF NOT EXISTS customers (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, contact_name TEXT, email TEXT, notes TEXT, created_at TEXT DEFAULT (datetime('now')));
    CREATE TABLE IF NOT EXISTS projects (id INTEGER PRIMARY KEY AUTOINCREMENT, customer_id INTEGER REFERENCES customers(id) ON DELETE SET NULL, name TEXT NOT NULL, jira_key TEXT, status TEXT DEFAULT 'active', budget_hours REAL DEFAULT 0, hourly_rate REAL DEFAULT 0, description TEXT, created_at TEXT DEFAULT (datetime('now')));
    CREATE TABLE IF NOT EXISTS tasks (id INTEGER PRIMARY KEY AUTOINCREMENT, project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE, title TEXT NOT NULL, jira_ticket TEXT, status TEXT DEFAULT 'open', estimated_hours REAL DEFAULT 0, description TEXT, sort_order INTEGER DEFAULT 0, created_at TEXT DEFAULT (datetime('now')));
    CREATE TABLE IF NOT EXISTS time_entries (id INTEGER PRIMARY KEY AUTOINCREMENT, project_id INTEGER REFERENCES projects(id) ON DELETE SET NULL, task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL, plan_task_id INTEGER REFERENCES plan_tasks(id) ON DELETE SET NULL, start_time TEXT NOT NULL, end_time TEXT, duration_seconds INTEGER, comment TEXT, billable INTEGER DEFAULT 1, date TEXT NOT NULL, source TEXT DEFAULT 'manual', tempo_worklog_id INTEGER UNIQUE, created_at TEXT DEFAULT (datetime('now')));
    CREATE TABLE IF NOT EXISTS work_days (id INTEGER PRIMARY KEY AUTOINCREMENT, date TEXT NOT NULL UNIQUE, start_time TEXT NOT NULL, end_time TEXT, notes TEXT);
    CREATE TABLE IF NOT EXISTS daily_logs (id INTEGER PRIMARY KEY AUTOINCREMENT, work_day_id INTEGER REFERENCES work_days(id) ON DELETE CASCADE, timestamp TEXT NOT NULL, text TEXT NOT NULL, date TEXT NOT NULL);
    CREATE TABLE IF NOT EXISTS pauses (id INTEGER PRIMARY KEY AUTOINCREMENT, work_day_id INTEGER REFERENCES work_days(id) ON DELETE CASCADE, start_time TEXT NOT NULL, end_time TEXT, date TEXT NOT NULL);
    CREATE TABLE IF NOT EXISTS timer_state (id INTEGER PRIMARY KEY CHECK (id=1), project_id INTEGER, task_id INTEGER, plan_task_id INTEGER, comment TEXT, start_time TEXT);
    CREATE TABLE IF NOT EXISTS pause_state (id INTEGER PRIMARY KEY CHECK (id=1), start_time TEXT);
    CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT);
    CREATE TABLE IF NOT EXISTS absences (id INTEGER PRIMARY KEY AUTOINCREMENT, date_from TEXT NOT NULL, date_to TEXT NOT NULL, type TEXT NOT NULL, hours REAL, note TEXT, created_at TEXT DEFAULT (datetime('now')));
    CREATE TABLE IF NOT EXISTS system_logs (id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp TEXT NOT NULL, level TEXT NOT NULL DEFAULT 'info', category TEXT NOT NULL DEFAULT 'system', message TEXT NOT NULL);
    CREATE TABLE IF NOT EXISTS month_closings (id INTEGER PRIMARY KEY AUTOINCREMENT, year INTEGER NOT NULL, month INTEGER NOT NULL, soll_hours REAL, ist_hours REAL, delta_hours REAL, carry_over_hours REAL, notes TEXT, closed_at TEXT, UNIQUE(year,month));
    CREATE TABLE IF NOT EXISTS balance_corrections (id INTEGER PRIMARY KEY AUTOINCREMENT, date TEXT NOT NULL, balance_hours REAL NOT NULL, note TEXT, created_at TEXT DEFAULT (datetime('now')));
    CREATE TABLE IF NOT EXISTS plan_tasks (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      title TEXT NOT NULL,
      project_id INTEGER REFERENCES projects(id) ON DELETE SET NULL,
      jira_ticket TEXT,
      estimated_minutes INTEGER DEFAULT 60,
      priority INTEGER DEFAULT 2,
      status TEXT DEFAULT 'open',
      scheduled_date TEXT,
      scheduled_week TEXT,
      recurrence TEXT,
      notes TEXT,
      sort_order INTEGER DEFAULT 0,
      completed_at TEXT,
      created_at TEXT DEFAULT (datetime('now'))
    );
    CREATE TABLE IF NOT EXISTS ssh_sessions (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      session_uid TEXT NOT NULL UNIQUE,
      date TEXT NOT NULL,
      start_time TEXT NOT NULL,
      end_time TEXT,
      duration_seconds INTEGER,
      target TEXT,
      hostname TEXT,
      username TEXT,
      port INTEGER,
      remote_cmd TEXT,
      exit_code INTEGER,
      note TEXT,
      project_id INTEGER REFERENCES projects(id) ON DELETE SET NULL,
      task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
      assign_source TEXT,
      time_entry_id INTEGER REFERENCES time_entries(id) ON DELETE SET NULL,
      created_at TEXT DEFAULT (datetime('now'))
    );
    CREATE INDEX IF NOT EXISTS idx_ssh_sessions_date ON ssh_sessions(date);
    INSERT OR IGNORE INTO timer_state (id) VALUES (1);
    INSERT OR IGNORE INTO pause_state (id) VALUES (1);
    INSERT OR IGNORE INTO settings VALUES ('target_mon','6.4');
    INSERT OR IGNORE INTO settings VALUES ('target_tue','6.4');
    INSERT OR IGNORE INTO settings VALUES ('target_wed','6.4');
    INSERT OR IGNORE INTO settings VALUES ('target_thu','6.4');
    INSERT OR IGNORE INTO settings VALUES ('target_fri','6.4');
    INSERT OR IGNORE INTO settings VALUES ('target_sat','0');
    INSERT OR IGNORE INTO settings VALUES ('target_sun','0');
    INSERT OR IGNORE INTO settings VALUES ('opening_balance_hours','0');
    INSERT OR IGNORE INTO settings VALUES ('vacation_days_per_year','30');
    INSERT OR IGNORE INTO settings VALUES ('tracking_start_date','');
    INSERT OR IGNORE INTO settings VALUES ('jira_token','');
    INSERT OR IGNORE INTO settings VALUES ('jira_base_url','');
    INSERT OR IGNORE INTO settings VALUES ('jira_username','');
  `);

  // Schema-Migration: fehlende Spalten sicher hinzufügen (für bestehende DBs)
  const migrations = [
    "ALTER TABLE time_entries ADD COLUMN activity TEXT",
    "ALTER TABLE timer_state ADD COLUMN activity TEXT",
    "ALTER TABLE time_entries ADD COLUMN source TEXT DEFAULT 'manual'",
    "ALTER TABLE time_entries ADD COLUMN tempo_worklog_id INTEGER",
    "ALTER TABLE time_entries ADD COLUMN is_absence INTEGER DEFAULT 0",
    "ALTER TABLE daily_logs ADD COLUMN date TEXT",
    "ALTER TABLE pauses ADD COLUMN date TEXT",
    "ALTER TABLE time_entries ADD COLUMN plan_task_id INTEGER",
    "ALTER TABLE timer_state ADD COLUMN plan_task_id INTEGER",
    "ALTER TABLE ssh_sessions ADD COLUMN project_id INTEGER",
    "ALTER TABLE ssh_sessions ADD COLUMN task_id INTEGER",
    "ALTER TABLE ssh_sessions ADD COLUMN assign_source TEXT",
    "ALTER TABLE ssh_sessions ADD COLUMN time_entry_id INTEGER",
  ];
  for (const m of migrations) { try { db.prepare(m).run(); } catch(_) {} }
  db.prepare("UPDATE time_entries SET source='manual' WHERE source IS NULL").run();

  plane.init();
  tempoPush.init();
  sshWatch.init();
  meetings.init();
  sources.init();
  audit.init();
  try { secure.harden(); } catch (_) {}
  cleanOldLogs();
  // Indizes für häufige Abfragen (bleiben schnell, auch mit vielen Jahren Tempo-Daten)
  for (const ix of ["time_entries(date)", "time_entries(task_id)", "time_entries(project_id)", "time_entries(plan_task_id)", "time_entries(tempo_push_id)", "time_entries(source,date)",
    "tasks(jira_ticket)", "plan_tasks(status,scheduled_date)", "system_logs(timestamp)", "absences(date_from,date_to)"]) {
    try { db.exec(`CREATE INDEX IF NOT EXISTS ix_${ix.replace(/[^a-z_]+/g, "_").replace(/_+$/, "")} ON ${ix}`); } catch (_) {}
  }
  try { db.exec("PRAGMA optimize"); } catch (_) {}
  setInterval(() => { try { db.exec("PRAGMA optimize"); cleanOldLogs(); } catch (_) {} }, 6 * 3600e3);
  writeLog("success","system",`Server v${VERSION} gestartet (${MODE}-Modus)`);

  // SSH-Protokoll beim Start und danach alle 2 Minuten einlesen
  try { importSshLog(); } catch(e) { console.error("SSH-Import:", e.message); }
  setInterval(() => { try { importSshLog(); } catch(_) {} }, 2*60*1000);

  // Static files
  const STATIC = path.join(__dirname, "../frontend/dist");
  if (fs.existsSync(STATIC)) {
    app.use(express.static(STATIC));
    app.get("*", (req,res) => {
      if (req.path.startsWith("/api")) return res.status(404).json({error:"Not found"});
      res.sendFile(path.join(STATIC,"index.html"));
    });
  }

  app.listen(PORT, "127.0.0.1", () => {
    console.log(`[Zeitprotokoll v${VERSION}] http://localhost:${PORT} [${MODE}]`);
  });
}

// Stabilität: ein Fehler in einem Hintergrund-Job darf den Server nicht beenden.
process.on("unhandledRejection", e => { try { writeLog("error", "system", "Unbehandelter Fehler (Hintergrund): " + (e?.stack || e)); } catch (_) { console.error(e); } });
process.on("uncaughtException", e => {
  try { writeLog("error", "system", "Absturz: " + (e?.stack || e)); } catch (_) {}
  console.error(e);
  // Zustand ist in der Datenbank – sauber beenden, launchd startet neu (KeepAlive)
  setTimeout(() => process.exit(1), 200);
});

startServer();
