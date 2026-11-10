/* ═══════════════════════════════════════════════════════════════════
   Meetings und Kalender → Buchungsvorschläge (v6.0.0)
   ───────────────────────────────────────────────────────────────────
   Die Mac-App meldet
     • erkannte Meetings (Zoom, Teams, Meet, Jitsi, BigBlueButton … bzw. Mikrofon aktiv)
     • Termine aus dem macOS-Kalender (heute und die letzten Tage)
   Der Server führt beides zusammen und schlägt Zeitbuchungen vor.
   Nichts wird ungefragt gebucht: Der Nutzer bestätigt oder ignoriert.
   Wiederkehrende Termine („StandUp“) merkt sich der Server mit Projekt,
   Aufgabe und Tätigkeit – beim nächsten Mal genügt ein Klick.
═══════════════════════════════════════════════════════════════════ */
const crypto = require("crypto");

const MEETING_CLOSE_AFTER_MS = 100 * 1000;      // so lange ohne Signal = Meeting vorbei
const GENERIC = "Meeting";                      // Label, wenn nur das Mikrofon aktiv war
const SPECIFIC_RANK = (a) => (a && a !== GENERIC && !/Mikrofon/i.test(a)) ? 2 : 1;

const normTitle = t => String(t || "").toLowerCase()
  .replace(/\b\d{1,4}[./-]\d{1,2}([./-]\d{2,4})?\b/g, " ").replace(/\b\d{1,2}:\d{2}\b/g, " ")
  .replace(/[^a-zäöüß ]+/g, " ").replace(/\s+/g, " ").trim();
const overlapMs = (aS, aE, bS, bE) => Math.max(0, Math.min(aE, bE) - Math.max(aS, bS));

module.exports = function registerMeetings(app, ctx) {
  const { one, all, run, getSettings, writeLog, localDateOf, onEntryChanged } = ctx;
  const LOG = "meetings";
  let lastSignal = null, lastDiag = {}, lastCalSync = null;
  let resyncRequested = false;

  function init() {
    run(`CREATE TABLE IF NOT EXISTS calendar_events (
      uid TEXT PRIMARY KEY, date TEXT NOT NULL, start_time TEXT NOT NULL, end_time TEXT NOT NULL,
      title TEXT, calendar TEXT, location TEXT, updated_at TEXT DEFAULT (datetime('now')))`);
    run("CREATE INDEX IF NOT EXISTS idx_cal_date ON calendar_events(date)");
    run(`CREATE TABLE IF NOT EXISTS meeting_events (
      id INTEGER PRIMARY KEY AUTOINCREMENT, app TEXT, start_time TEXT NOT NULL, end_time TEXT, last_seen TEXT NOT NULL)`);
    run(`CREATE TABLE IF NOT EXISTS meeting_state (key TEXT PRIMARY KEY, status TEXT NOT NULL, entry_id INTEGER, created_at TEXT DEFAULT (datetime('now')))`);   // booked | dismissed
    run(`CREATE TABLE IF NOT EXISTS meeting_rules (
      pattern TEXT PRIMARY KEY, project_id INTEGER, task_id INTEGER, activity TEXT, hits INTEGER DEFAULT 1, updated_at TEXT DEFAULT (datetime('now')))`);
    // alte Daten aufräumen
    const cut = new Date(Date.now() - 60 * 864e5).toISOString();
    try { run("DELETE FROM calendar_events WHERE start_time < ?", cut); run("DELETE FROM meeting_events WHERE start_time < ?", cut); run("DELETE FROM meeting_state WHERE created_at < ?", cut.slice(0, 19).replace("T", " ")); } catch (_) {}
  }

  function cfg() {
    const s = getSettings();
    return {
      detect: s.meeting_detect !== "0",
      calendar: s.meeting_calendar !== "0",
      browser: s.meeting_browser !== "0",
      ask: s.meeting_ask !== "0",
      exclude: String(s.meeting_calendar_exclude ?? "Geburtstage,Birthdays,Feiertage,Holidays,Deutsche Feiertage,Siri Suggestions").split(",").map(x => x.trim()).filter(Boolean),
      minMinutes: Math.max(1, Math.min(60, parseInt(s.meeting_min_minutes) || 3)),
    };
  }

  /* ── Meldungen der Mac-App ──────────────────────────────────────── */
  function applySignals(signals, nowMs) {
    const open = one("SELECT * FROM meeting_events WHERE end_time IS NULL ORDER BY id DESC LIMIT 1");
    const best = (signals || []).filter(s => s && s.app).sort((a, b) => SPECIFIC_RANK(b.app) - SPECIFIC_RANK(a.app))[0];
    const nowIso = new Date(nowMs).toISOString();
    if (best) {
      if (open) {
        const upgrade = SPECIFIC_RANK(best.app) > SPECIFIC_RANK(open.app);
        run("UPDATE meeting_events SET last_seen=?, app=? WHERE id=?", nowIso, upgrade ? best.app : open.app, open.id);
      } else {
        run("INSERT INTO meeting_events (app,start_time,last_seen) VALUES (?,?,?)", best.app, nowIso, nowIso);
      }
    } else if (open && nowMs - Date.parse(open.last_seen) > MEETING_CLOSE_AFTER_MS) {
      run("UPDATE meeting_events SET end_time=? WHERE id=?", open.last_seen, open.id);
    }
  }
  app.get("/api/meetings/config", (_, res) => {
    const c = cfg(); const resync = resyncRequested; resyncRequested = false;
    res.json({ detect: c.detect, calendar: c.calendar, browser: c.browser, ask: c.ask, exclude: c.exclude, resync });
  });
  app.post("/api/meetings/signal", (req, res) => {
    const now = Date.now();
    lastSignal = now; lastDiag = { ...(req.body?.diag || {}), at: new Date(now).toISOString(), signals: req.body?.signals || [] };
    try { applySignals(req.body?.signals, now); res.json({ ok: true }); }
    catch (e) { res.status(500).json({ error: e.message }); }
  });
  app.post("/api/meetings/calendar", (req, res) => {
    const { from, to, events, diag } = req.body || {};
    if (!/^\d{4}-\d{2}-\d{2}$/.test(from || "") || !/^\d{4}-\d{2}-\d{2}$/.test(to || "") || !Array.isArray(events)) return res.status(400).json({ error: "from, to, events erwartet" });
    const excl = cfg().exclude.map(x => x.toLowerCase());
    let n = 0;
    try {
      run("BEGIN");
      run("DELETE FROM calendar_events WHERE date BETWEEN ? AND ?", from, to);
      for (const e of events) {
        const s = Date.parse(e.start), en = Date.parse(e.end);
        if (!Number.isFinite(s) || !Number.isFinite(en) || en <= s || e.allDay) continue;          // ganztägige Termine sind keine Zeitbuchung
        if (en - s > 12 * 3600e3) continue;
        if (excl.includes(String(e.calendar || "").toLowerCase())) continue;
        if (/^zeitprotokoll/i.test(String(e.calendar || ""))) continue;                // eigener Abwesenheits-Kalender (abonniert) nicht zurücklesen
        const uid = crypto.createHash("sha1").update(`${e.title}|${new Date(s).toISOString()}|${e.calendar || ""}`).digest("hex").slice(0, 16);
        run("INSERT OR REPLACE INTO calendar_events (uid,date,start_time,end_time,title,calendar,location) VALUES (?,?,?,?,?,?,?)",
          uid, localDateOf(s), new Date(s).toISOString(), new Date(en).toISOString(), String(e.title || "Termin").slice(0, 200), e.calendar || null, e.location || null);
        n++;
      }
      run("COMMIT");
    } catch (e) { try { run("ROLLBACK"); } catch (_) {} return res.status(500).json({ error: e.message }); }
    try { ctx.hooks?.absences?.fromCalendar(events, excl); } catch (_) {}   // ganztägig „Urlaub“ & Co. → Vorschlag
    lastCalSync = { at: new Date().toISOString(), count: n, from, to, diag: diag || null };
    res.json({ ok: true, count: n });
  });

  /* ── Vorschläge berechnen ───────────────────────────────────────── */
  function suggestions(days = 3) {
    const c = cfg();
    const now = Date.now();
    const sinceDate = localDateOf(now - (days - 1) * 864e5);
    const state = Object.fromEntries(all("SELECT key,status FROM meeting_state").map(r => [r.key, r.status]));
    const entries = all(`SELECT start_time,end_time FROM time_entries WHERE date>=? AND end_time IS NOT NULL`, sinceDate)
      .map(e => [Date.parse(e.start_time), Date.parse(e.end_time)]);
    const timer = one("SELECT * FROM timer_state WHERE id=1");
    if (timer?.start_time && timer.project_id) entries.push([Date.parse(timer.start_time), now]);
    const covered = (s, e) => entries.reduce((sum, [a, b]) => sum + overlapMs(s, e, a, b), 0) / Math.max(1, e - s);

    const cals = c.calendar ? all("SELECT * FROM calendar_events WHERE date>=? ORDER BY start_time", sinceDate) : [];
    const dets = c.detect ? all("SELECT * FROM meeting_events WHERE start_time>=? ORDER BY start_time", new Date(Date.parse(sinceDate + "T00:00:00") - 0).toISOString()) : [];
    const detsDone = dets.map(d => ({ ...d, s: Date.parse(d.start_time), e: Date.parse(d.end_time || d.last_seen), open: !d.end_time }))
      .filter(d => d.e - d.s >= c.minMinutes * 60e3);
    const usedDet = new Set();
    const out = [];

    for (const ev of cals) {
      const s = Date.parse(ev.start_time), e = Date.parse(ev.end_time);
      if (s > now + 5 * 60e3) continue;                                                              // Zukunft: noch nicht buchbar
      const det = detsDone.find(d => !usedDet.has(d.id) && (overlapMs(s, e, d.s, d.e) / Math.min(e - s, d.e - d.s) >= 0.3 || (d.s >= s - 10 * 60e3 && d.s <= e)));
      if (det) usedDet.add(det.id);
      let bs = s, be = Math.min(e, now);
      if (det) { bs = Math.max(det.s, s - 15 * 60e3); be = Math.min(Math.max(det.e, Math.min(e, det.e)), e + 30 * 60e3); }
      const key = "cal:" + ev.uid;
      out.push({ key, title: ev.title, start_time: new Date(bs).toISOString(), end_time: new Date(be).toISOString(), minutes: Math.round((be - bs) / 60e3),
        sources: det ? ["Kalender", det.app] : ["Kalender"], cal: { start_time: ev.start_time, end_time: ev.end_time, calendar: ev.calendar, location: ev.location }, detected: det ? { app: det.app, start_time: det.start_time, end_time: det.end_time || det.last_seen } : null,
        running: false });
    }
    for (const d of detsDone) {
      if (usedDet.has(d.id)) continue;
      out.push({ key: "det:" + d.id, title: `${d.app === GENERIC ? "Meeting" : d.app + "-Meeting"} (erkannt)`, start_time: new Date(d.s).toISOString(), end_time: new Date(d.e).toISOString(),
        minutes: Math.round((d.e - d.s) / 60e3), sources: [d.app], cal: null, detected: { app: d.app, start_time: d.start_time, end_time: d.end_time || d.last_seen }, running: d.open });
    }

    const rules = Object.fromEntries(all("SELECT * FROM meeting_rules").map(r => [r.pattern, r]));
    const projs = Object.fromEntries(all("SELECT id,name FROM projects").map(p => [p.id, p.name]));
    return out.filter(x => !state[x.key])
      .filter(x => covered(Date.parse(x.start_time), Date.parse(x.end_time)) < 0.6)
      .filter(x => x.minutes >= 1)
      .map(x => { const r = rules[normTitle(x.title)]; const best = !r ? suggestTasks(x.title, 1)[0] : null; return { ...x, date: localDateOf(Date.parse(x.start_time)), best: best && best.score >= 60 ? best : null,
        rule: r ? { project_id: r.project_id, project_name: projs[r.project_id] || null, task_id: r.task_id, activity: r.activity, hits: r.hits } : null }; })
      .sort((a, b) => b.start_time.localeCompare(a.start_time));
  }

  app.get("/api/meetings/suggestions", (req, res) => {
    try { res.json(suggestions(Math.max(1, Math.min(14, parseInt(req.query.days) || 3)))); }
    catch (e) { res.status(500).json({ error: e.message }); }
  });

  app.post("/api/meetings/book", (req, res) => {
    const b = req.body || {};
    const s = Date.parse(b.start_time), e = Date.parse(b.end_time);
    if (!b.key || !Number.isFinite(s) || !Number.isFinite(e) || e <= s) return res.status(400).json({ error: "Zeit ungültig" });
    if (e - s > 12 * 3600e3) return res.status(400).json({ error: "Länger als 12 Stunden – bitte prüfen" });
    if (!b.project_id) return res.status(400).json({ error: "Bitte ein Projekt wählen" });
    if (one("SELECT 1 FROM meeting_state WHERE key=?", b.key)) return res.status(409).json({ error: "Schon erledigt" });
    // Schon gebucht (z. B. Timer lief zum Meeting)? Dann nicht doppelt anlegen, sondern den Termin dieser Buchung zuordnen
    if (!b.force) {
      const ex = all("SELECT id, start_time, end_time FROM time_entries WHERE date=? AND end_time IS NOT NULL", localDateOf(s))
        .map(x => ({ id: x.id, ov: Math.max(0, Math.min(e, Date.parse(x.end_time)) - Math.max(s, Date.parse(x.start_time))), len: Date.parse(x.end_time) - Date.parse(x.start_time) }))
        .filter(x => x.ov >= 0.5 * Math.min(e - s, x.len)).sort((x, y) => y.ov - x.ov)[0];
      if (ex) {
        run("INSERT OR REPLACE INTO meeting_state (key,status,entry_id) VALUES (?,?,?)", b.key, "booked", ex.id);
        return res.json({ ok: true, entry_id: ex.id, merged: true, message: "War schon gebucht – Termin der vorhandenen Buchung zugeordnet" });
      }
    }
    const title = String(b.comment || b.title || "Meeting").slice(0, 300);
    const r = run(`INSERT INTO time_entries (project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,source,activity)
                   VALUES (?,?,?,?,?,?,1,?,'meeting',?)`,
      Number(b.project_id), b.task_id ? Number(b.task_id) : null, new Date(s).toISOString(), new Date(e).toISOString(), Math.round((e - s) / 1000), title, localDateOf(s), b.activity || null);
    run("INSERT OR REPLACE INTO meeting_state (key,status,entry_id) VALUES (?,?,?)", b.key, "booked", Number(r.lastInsertRowid));
    try { ctx.hooks?.checks?.flagAfter(Number(r.lastInsertRowid)); } catch (_) {}
    if (b.remember !== false) {
      const pat = normTitle(b.rule_title || b.title || title);
      if (pat) run(`INSERT INTO meeting_rules (pattern,project_id,task_id,activity,hits) VALUES (?,?,?,?,1)
                    ON CONFLICT(pattern) DO UPDATE SET project_id=excluded.project_id, task_id=excluded.task_id, activity=excluded.activity, hits=hits+1, updated_at=datetime('now')`,
        pat, Number(b.project_id), b.task_id ? Number(b.task_id) : null, b.activity || null);
    }
    try { onEntryChanged && onEntryChanged(); } catch (_) {}
    res.json({ ok: true, entry_id: Number(r.lastInsertRowid) });
  });

  app.post("/api/meetings/dismiss", (req, res) => {
    const keys = Array.isArray(req.body?.keys) ? req.body.keys : [req.body?.key];
    for (const k of keys.filter(Boolean)) run("INSERT OR REPLACE INTO meeting_state (key,status) VALUES (?, 'dismissed')", String(k));
    res.json({ ok: true });
  });
  app.delete("/api/meetings/rules/:pattern", (req, res) => { run("DELETE FROM meeting_rules WHERE pattern=?", req.params.pattern); res.json({ ok: true }); });

  /* Passende Aufgabe/Ticket zu einem Termintitel vorschlagen (Titel, frühere Buchungen, gelernte Regeln) */
  const { textSim } = require("./textmatch");
  function suggestTasks(text, limit = 6) {
    const out = [];
    const rule = one("SELECT r.*, p.name AS project_name, t.title AS task_title, t.jira_ticket FROM meeting_rules r LEFT JOIN projects p ON p.id=r.project_id LEFT JOIN tasks t ON t.id=r.task_id WHERE pattern=?", normTitle(text));
    if (rule) out.push({ task_id: rule.task_id, project_id: rule.project_id, key: rule.jira_ticket || null, title: rule.task_title || null, project_name: rule.project_name, score: 100, reasons: [`so ${rule.hits}× gebucht`] });
    const since = localDateOf(Date.now() - 180 * 864e5);
    const hist = {};
    for (const r of all(`SELECT task_id, comment, date FROM time_entries WHERE task_id IS NOT NULL AND date>=? AND comment IS NOT NULL AND TRIM(comment)<>'' ORDER BY date DESC LIMIT 3000`, since)) {
      const h = hist[r.task_id] ||= { comments: [], n: 0, last: r.date }; h.n++; if (h.comments.length < 25) h.comments.push(r.comment);
    }
    for (const t of all(`SELECT t.id, t.title, t.jira_ticket, t.project_id, p.name AS project_name FROM tasks t JOIN projects p ON p.id=t.project_id
                         WHERE COALESCE(t.status,'open')<>'done' AND COALESCE(p.status,'')<>'archived'`)) {
      if (rule && rule.task_id === t.id) continue;
      const simT = textSim(text, `${t.title} ${t.jira_ticket || ""}`);
      const h = hist[t.id]; let simH = 0;
      if (h) for (const c of h.comments) { const v = textSim(text, c); if (v > simH) simH = v; }
      const sim = Math.max(simT, simH * 0.9);
      if (sim < 0.3) continue;
      const freq = h ? Math.min(1, Math.log(1 + h.n) / Math.log(25)) : 0;
      const score = Math.round(Math.min(0.99, 0.8 * sim + 0.2 * freq) * 100);
      const reasons = []; if (simT >= simH * 0.9) reasons.push(`Titel ähnlich (${Math.round(simT * 100)} %)`); else reasons.push("ähnliche frühere Buchung");
      if (h) reasons.push(`${h.n}× gebucht`);
      out.push({ task_id: t.id, project_id: t.project_id, key: t.jira_ticket || null, title: t.title, project_name: t.project_name, score, reasons });
    }
    return out.sort((a, b) => b.score - a.score).slice(0, limit);
  }
  app.get("/api/meetings/suggest-task", (req, res) => {
    const text = String(req.query.text || "").trim();
    res.json(text ? suggestTasks(text) : []);
  });

  /* Laufender/gleich beginnender Termin + gelernte Zuordnung → für „Timer starten?“ */
  app.get("/api/meetings/now", (_, res) => {
    const now = Date.now();
    const ev = all("SELECT * FROM calendar_events WHERE start_time <= ? AND end_time >= ? ORDER BY start_time DESC",
      new Date(now + 10 * 60e3).toISOString(), new Date(now).toISOString())[0] || null;
    const r = ev ? one("SELECT r.*, p.name AS project_name FROM meeting_rules r LEFT JOIN projects p ON p.id=r.project_id WHERE pattern=?", normTitle(ev.title)) : null;
    // ohne gelernte Regel: bester Ticket-Vorschlag (frühere Buchungen, Titel)
    const best = ev && !r ? (suggestTasks(ev.title, 1)[0] || null) : null;
    const state = ev ? one("SELECT status FROM meeting_state WHERE key=?", "cal:" + ev.uid)?.status || null : null;
    const next = !ev ? all("SELECT * FROM calendar_events WHERE start_time > ? AND start_time <= ? ORDER BY start_time LIMIT 1",
      new Date(now).toISOString(), new Date(now + 30 * 60e3).toISOString())[0] || null : null;
    res.json({ ask: cfg().ask, event: ev, rule: r || null, best: best && best.score >= 60 ? best : null, state, next });
  });
  /* Eingelesene Kalendertermine anzeigen (Kontrolle in Einstellungen › Erkennung) */
  app.get("/api/meetings/events", (req, res) => {
    const days = Math.max(1, Math.min(14, parseInt(req.query.days) || 2));
    const from = /^\d{4}-\d{2}-\d{2}$/.test(req.query.from || "") ? req.query.from : localDateOf(Date.now() - (days - 1) * 864e5);
    const to = /^\d{4}-\d{2}-\d{2}$/.test(req.query.to || "") ? req.query.to : "9999-12-31";
    const state = Object.fromEntries(all("SELECT key,status,entry_id FROM meeting_state").map(r => [r.key, r]));
    const rules = Object.fromEntries(all("SELECT r.*, p.name AS project_name FROM meeting_rules r LEFT JOIN projects p ON p.id=r.project_id").map(r => [r.pattern, r]));
    res.json(all("SELECT * FROM calendar_events WHERE date>=? AND date<=? ORDER BY start_time", from, to).map(e => ({ ...e,
      key: "cal:" + e.uid, state: state["cal:" + e.uid]?.status || null, entry_id: state["cal:" + e.uid]?.entry_id || null, rule: rules[normTitle(e.title)] || null })));
  });

  /* ── Status / Diagnose ──────────────────────────────────────────── */
  app.get("/api/meetings/status", (_, res) => {
    const c = cfg(), now = Date.now();
    res.json({ ...c, last_signal: lastSignal ? new Date(lastSignal).toISOString() : null, signal_age_s: lastSignal ? Math.round((now - lastSignal) / 1000) : null,
      diag: lastDiag, calendar_sync: lastCalSync,
      open_meeting: one("SELECT * FROM meeting_events WHERE end_time IS NULL ORDER BY id DESC LIMIT 1") || null,
      counts: { calendar_events: one("SELECT COUNT(*) AS n FROM calendar_events")?.n || 0, meetings: one("SELECT COUNT(*) AS n FROM meeting_events")?.n || 0, rules: one("SELECT COUNT(*) AS n FROM meeting_rules")?.n || 0 },
      rules: all("SELECT r.pattern, r.hits, r.activity, p.name AS project_name FROM meeting_rules r LEFT JOIN projects p ON p.id=r.project_id ORDER BY r.hits DESC LIMIT 50") });
  });
  app.post("/api/meetings/resync", (_, res) => { resyncRequested = true; res.json({ ok: true }); });

  return { init, suggestions, _test: { normTitle, applySignals } };
};
