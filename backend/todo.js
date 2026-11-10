/* ═══════════════════════════════════════════════════════════════════
   To-dos & Planung (v6.18.0)
   ───────────────────────────────────────────────────────────────────
   Baut auf plan_tasks auf (alte „Planung“ wird mitgenommen).
   • Spalten: Überfällig · Heute · Morgen (nächster Arbeitstag) · Diese Woche · Später · Eingang
   • To-do = optional ein Ticket (Plane oder Jira, je nach Projekt) + geplanter Tag
     + Fälligkeit + Schätzung + optionaler Zeitblock (Uhrzeit) + Priorität.
   • Abgleich (einzeln abschaltbar, Einstellungen im To-do-Bereich):
       todo_sync_due        Fälligkeit → Plane target_date / Jira duedate
       todo_sync_tempo_plan geplante Zeit → Tempo-Plan (Jira-Tickets)
       todo_sync_plane_start geplanter Tag → Plane start_date
     Fehler blockieren nie das Speichern – sie stehen am To-do.
   • Vorschläge: mir zugewiesene Tickets, Termine ohne Vorbereitung,
     Liegengebliebenes, wiederkehrende Aufgaben (Vorlagen).
═══════════════════════════════════════════════════════════════════ */
module.exports = function registerTodo(app, ctx) {
  const { one, all, run, getSettings, writeLog, localDateOf, tempoPush, hooks, PORT } = ctx;
  const LOG = "todo";
  const pad = n => String(n).padStart(2, "0");
  const iso = d => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  const day = s => new Date(s + "T12:00:00");
  const today = () => localDateOf(Date.now());
  const on = k => getSettings()[k] !== "0";
  const self = async (method, path, body) => {
    const r = await fetch(`http://127.0.0.1:${PORT}/api${path}`, { method, headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
    const j = await r.json().catch(() => ({}));
    if (!r.ok) throw Object.assign(new Error(j.error || `HTTP ${r.status}`), { status: r.status });
    return j;
  };

  let ready = false;
  function ensure() {
    if (ready) return;
    for (const c of ["due_date TEXT", "task_id INTEGER", "source TEXT", "moved_count INTEGER DEFAULT 0", "block_start TEXT", "tempo_plan_id TEXT",
      "sync_hash TEXT", "sync_error TEXT", "template_id INTEGER", "event_uid TEXT", "ticket_title TEXT"]) { try { run(`ALTER TABLE plan_tasks ADD COLUMN ${c}`); } catch (_) {} }
    run(`CREATE TABLE IF NOT EXISTS todo_templates (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL, rule TEXT NOT NULL, project_id INTEGER, jira_ticket TEXT,
      estimated_minutes INTEGER DEFAULT 30, priority INTEGER DEFAULT 2, active INTEGER DEFAULT 1, last_for TEXT, created_at TEXT DEFAULT (datetime('now')))`);
    run(`CREATE TABLE IF NOT EXISTS todo_dismissed (key TEXT PRIMARY KEY, until TEXT)`);
    ready = true;
  }

  /* ── Arbeitstage ─────────────────────────────────────────────── */
  const holiday = d => !!one("SELECT 1 FROM absences WHERE date_from<=? AND date_to>=? AND type='feiertag'", d, d);
  const workday = d => { const w = day(d).getDay(); return w >= 1 && w <= 5 && !holiday(d); };
  const nextWorkday = d => { const x = day(d); do x.setDate(x.getDate() + 1); while (!workday(iso(x))); return iso(x); };
  const weekEnd = d => { const x = day(d); x.setDate(x.getDate() + (7 - (x.getDay() || 7))); return iso(x); };   // Sonntag
  const isoWeek = s => { const x = day(s); x.setDate(x.getDate() + 3 - ((x.getDay() + 6) % 7)); const f = new Date(x.getFullYear(), 0, 4, 12); return `${x.getFullYear()}-W${pad(1 + Math.round(((x - f) / 864e5 - 3 + ((f.getDay() + 6) % 7)) / 7))}`; };
  /* „heute“ / „morgen“ / „woche“ / „später“ / „eingang“ / Datum → {scheduled_date, scheduled_week} */
  function when(w) {
    const t = today();
    if (/^\d{4}-\d{2}-\d{2}$/.test(w || "")) return { scheduled_date: w, scheduled_week: isoWeek(w) };
    if (w === "today") return { scheduled_date: t, scheduled_week: isoWeek(t) };
    if (w === "tomorrow") { const n = nextWorkday(t); return { scheduled_date: n, scheduled_week: isoWeek(n) }; }
    if (w === "week") return { scheduled_date: null, scheduled_week: isoWeek(t) };
    if (w === "nextweek") { const x = day(weekEnd(t)); x.setDate(x.getDate() + 1); return { scheduled_date: null, scheduled_week: isoWeek(iso(x)) }; }
    return { scheduled_date: null, scheduled_week: null };
  }

  /* ── System des Tickets: Plane, wenn das Präfix ein Plane-Projekt ist, sonst Jira ── */
  function systemOf(key) {
    const p = String(key || "").toUpperCase().split("-")[0]; if (!p || !/-\d+$/.test(key || "")) return null;
    if (one("SELECT 1 FROM projects WHERE UPPER(plane_identifier)=? AND plane_project_id IS NOT NULL", p)) return "plane";
    if (one("SELECT 1 FROM tasks WHERE UPPER(jira_ticket)=? AND plane_work_item_id IS NOT NULL", String(key).toUpperCase())) return "plane";
    return "jira";
  }

  const SELECT = `SELECT p.*, pr.name AS project_name, t.jira_ticket AS task_ticket, t.title AS task_title,
      (SELECT COALESCE(SUM(e.duration_seconds),0) FROM time_entries e WHERE e.plan_task_id=p.id) AS tracked_seconds
    FROM plan_tasks p LEFT JOIN projects pr ON pr.id=p.project_id LEFT JOIN tasks t ON t.id=p.task_id`;
  const shape = r => { const key = (r.jira_ticket || r.task_ticket || "").toUpperCase() || null; return { ...r, ticket: key, system: key ? systemOf(key) : null, ticket_title: r.ticket_title || r.task_title || null }; };

  /* ── Wiederkehrendes: Vorlagen erzeugen ihr To-do, wenn es dran ist ──
     Regeln: month-last-workday · month-first-workday · month-<1..28> · week-<1..5> (Mo..Fr) · day */
  function dueFor(rule, ref) {
    const d = day(ref), y = d.getFullYear(), m = d.getMonth();
    const wdIn = (a, step) => { const x = new Date(a); while (!workday(iso(x))) x.setDate(x.getDate() + step); return iso(x); };
    if (rule === "month-last-workday") return wdIn(new Date(y, m + 1, 0, 12), -1);
    if (rule === "month-first-workday") return wdIn(new Date(y, m, 1, 12), 1);
    let mm;
    if ((mm = /^month-(\d+)$/.exec(rule))) return wdIn(new Date(y, m, Math.min(28, +mm[1]), 12), 1);
    if ((mm = /^week-(\d)$/.exec(rule))) { const x = new Date(d); x.setDate(x.getDate() - ((x.getDay() + 6) % 7) + (+mm[1] - 1)); return iso(x); }
    if (rule === "day") return workday(ref) ? ref : null;
    return null;
  }
  function runTemplates() {
    ensure();
    const t = today(), horizon = nextWorkday(nextWorkday(t));    // bis übermorgen anlegen
    for (const tp of all("SELECT * FROM todo_templates WHERE active=1")) {
      for (const ref of [t, horizon]) {
        const due = dueFor(tp.rule, ref);
        if (!due || due < t || due > weekEnd(horizon) || (tp.last_for && tp.last_for >= due)) continue;
        run(`INSERT INTO plan_tasks (title,project_id,jira_ticket,estimated_minutes,priority,status,scheduled_date,scheduled_week,due_date,source,template_id) VALUES (?,?,?,?,?,'open',?,?,?,'recurring',?)`,
          tp.title, tp.project_id, tp.jira_ticket, tp.estimated_minutes, tp.priority, due, isoWeek(due), due, tp.id);
        run("UPDATE todo_templates SET last_for=? WHERE id=?", due, tp.id);
      }
    }
  }

  /* ── Vergessene Tage als ein To-do (v6.23) ─────────────────────
     Ein einziges To-do „Zeiten nachtragen“ für alle Tage, an denen weniger als das Soll
     gebucht ist (wie „Nachtragen“ im Assistenten, ohne „Passt so“-Tage). Es aktualisiert sich selbst
     und hakt sich ab, sobald alles nachgetragen ist. Von Hand abgehakt → erst bei neuen Tagen wieder. */
  function syncGapTodo() {
    let st; try { st = hooks.monthend?.status(); } catch (_) { return; } if (!st) return;
    const days = [...(st.prev?.days || []), ...(st.open_days || [])].filter((d, i, a) => a.findIndex(x => x.date === d.date) === i).sort((a, b) => a.date.localeCompare(b.date));
    const sig = days.map(d => d.date).join(",");
    const cur = one("SELECT * FROM plan_tasks WHERE source='gaps' AND COALESCE(status,'open')='open' ORDER BY id DESC LIMIT 1");
    if (!days.length) { if (cur) run("UPDATE plan_tasks SET status='done', completed_at=? WHERE id=?", new Date().toISOString(), cur.id); return; }
    const dismissed = getSettings().gaps_todo_dismissed || "";
    if (!cur && dismissed && days.every(d => dismissed.split(",").includes(d.date))) return;   // bewusst abgehakt, nichts Neues
    const fmt = s => `${s.slice(8, 10)}.${s.slice(5, 7)}.`;
    const title = `Zeiten nachtragen (${days.length} Tag${days.length === 1 ? "" : "e"})`;
    const rec = days.slice(-3).reverse(), more = days.length - rec.length;
    const notes = rec.map(d => `${fmt(d.date)} fehlen ${d.missing_h.toLocaleString("de-DE")} h`).join(" · ") + (more > 0 ? ` + ${more} weitere` : "");
    const t = today();
    if (cur) run("UPDATE plan_tasks SET title=?, notes=?, ticket_title=?, scheduled_date=CASE WHEN scheduled_date<? THEN ? ELSE scheduled_date END, scheduled_week=?, estimated_minutes=? WHERE id=?",
      title, notes, sig, t, t, isoWeek(t), Math.min(120, 10 * days.length), cur.id);
    else run("INSERT INTO plan_tasks (title,notes,ticket_title,priority,status,scheduled_date,scheduled_week,estimated_minutes,source) VALUES (?,?,?,1,'open',?,?,?,'gaps')",
      title, notes, sig, t, isoWeek(t), Math.min(120, 10 * days.length));
  }

  /* ── Liste in Spalten ─────────────────────────────────────────── */
  function board() {
    ensure(); runTemplates(); try { syncGapTodo(); } catch (_) {}
    const t = today(), tm = nextWorkday(t), we = weekEnd(t), wk = isoWeek(t);
    const rows = all(`${SELECT} WHERE COALESCE(p.status,'open') NOT IN ('done','template') ORDER BY p.priority ASC, p.sort_order ASC, COALESCE(p.due_date,'9999') ASC, p.created_at ASC`).map(shape);
    const col = { overdue: [], today: [], tomorrow: [], week: [], later: [], inbox: [] };
    for (const r of rows) {
      const d = r.scheduled_date;
      if ((d && d < t) || (r.due_date && r.due_date < t)) col.overdue.push(r);
      else if (d === t) col.today.push(r);
      else if (d === tm) col.tomorrow.push(r);
      else if ((d && d <= we) || (!d && r.scheduled_week === wk)) col.week.push(r);
      else if (d || r.scheduled_week) col.later.push(r);
      else col.inbox.push(r);
    }
    const done = all(`${SELECT} WHERE p.status='done' AND substr(p.completed_at,1,10)>=? ORDER BY p.completed_at DESC LIMIT 30`, iso(new Date(Date.now() - 7 * 864e5))).map(shape);
    // Kapazität: Soll − Abwesenheit − Termine  vs. geplante Schätzungen
    const s = getSettings(), keys = ["target_sun", "target_mon", "target_tue", "target_wed", "target_thu", "target_fri", "target_sat"];
    const cap = d => {
      const target = (parseFloat(s[keys[day(d).getDay()]]) || 0) * 60;
      const abs = all("SELECT type, hours FROM absences WHERE date_from<=? AND date_to>=?", d, d);
      const free = abs.some(a => a.type === "feiertag" || a.hours == null) ? 0 : Math.max(0, target - abs.reduce((a, x) => a + (x.hours || 0) * 60, 0));
      let meet = 0; try { meet = one("SELECT COALESCE(SUM((julianday(end_time)-julianday(start_time))*1440),0) AS m FROM calendar_events WHERE date=?", d).m || 0; } catch (_) {}
      const booked = d === t ? (one("SELECT COALESCE(SUM(duration_seconds),0) AS s FROM time_entries WHERE date=? AND COALESCE(is_absence,0)=0", d).s || 0) / 60 : 0;
      return { date: d, available: Math.round(free), meetings: Math.round(meet), booked: Math.round(booked) };
    };
    const plannedOn = list => list.reduce((a, r) => a + Math.max(0, (r.estimated_minutes || 0) - Math.round((r.tracked_seconds || 0) / 60)), 0);
    return { today: t, tomorrow: tm, week: wk, columns: col, done,
      capacity: { today: { ...cap(t), planned: plannedOn(col.today) }, tomorrow: { ...cap(tm), planned: plannedOn(col.tomorrow) } },
      settings: { sync_due: on("todo_sync_due"), sync_tempo_plan: on("todo_sync_tempo_plan"), sync_plane_start: on("todo_sync_plane_start") } };
  }

  /* ── Abgleich nach außen (nie blockierend) ────────────────────── */
  const issueIds = {};
  async function jiraIssueId(key) { if (issueIds[key]) return issueIds[key]; const i = await tempoPush.request("GET", `/rest/api/2/issue/${encodeURIComponent(key)}?fields=summary`); return (issueIds[key] = i?.id); }
  async function syncOut(id) {
    const r = one("SELECT * FROM plan_tasks WHERE id=?", id); if (!r) return;
    const key = (r.jira_ticket || one("SELECT jira_ticket FROM tasks WHERE id=?", r.task_id)?.jira_ticket || "").toUpperCase();
    const sys = key ? systemOf(key) : null; if (!sys) return;
    const done = r.status === "done";
    const want = { due: r.due_date || null, day: done ? null : r.scheduled_date || null, secs: done ? 0 : (r.estimated_minutes || 0) * 60, start: r.block_start || "09:00" };
    const prev = (() => { try { return JSON.parse(r.sync_hash || "{}"); } catch (_) { return {}; } })();
    const errs = [], s = getSettings();
    try {
      if (on("todo_sync_due") && want.due !== (prev.due ?? null) && r.source !== "ticket-due") {
        if (sys === "plane") await hooks.plane.api.setFields(key, { target_date: want.due });
        else await tempoPush.request("PUT", `/rest/api/2/issue/${encodeURIComponent(key)}`, { fields: { duedate: want.due } });
        writeLog("info", LOG, `Fälligkeit ${key} → ${want.due || "keine"} (${sys})`);
      }
    } catch (e) { errs.push(`Fälligkeit: ${e.message}`); }
    try {
      if (sys === "plane" && on("todo_sync_plane_start") && want.day !== (prev.day ?? null)) {
        await hooks.plane.api.setFields(key, { start_date: want.day });
      }
      if (sys === "jira" && on("todo_sync_tempo_plan") && (want.day !== (prev.day ?? null) || want.secs !== (prev.secs ?? 0) || want.start !== (prev.start ?? "09:00"))) {
        if (r.tempo_plan_id) { try { await tempoPush.request("DELETE", `/rest/tempo-planning/1/plan/${encodeURIComponent(r.tempo_plan_id)}`); } catch (e) { if (e.status !== 404) throw e; } run("UPDATE plan_tasks SET tempo_plan_id=NULL WHERE id=?", id); }
        if (want.day && want.secs > 0) {
          const body = { planItemType: "ISSUE", planItemId: String(await jiraIssueId(key)), assigneeKey: s.jira_userkey || s.jira_username, assigneeType: "USER",
            start: want.day, end: want.day, secondsPerDay: want.secs, startTime: want.start, includeNonWorkingDays: false, description: String(r.title || "").slice(0, 250) };
          let p;
          try { p = await tempoPush.request("POST", "/rest/tempo-planning/1/plan", body); }
          catch (e) { if (e.status === 400 && s.jira_username && body.assigneeKey !== s.jira_username) p = await tempoPush.request("POST", "/rest/tempo-planning/1/plan", { ...body, assigneeKey: s.jira_username }); else throw e; }
          const pid = Array.isArray(p) ? (p[0]?.id ?? p[0]?.planId) : (p?.id ?? p?.planId);
          if (pid != null) run("UPDATE plan_tasks SET tempo_plan_id=? WHERE id=?", String(pid), id);
          writeLog("info", LOG, `Tempo-Plan ${key} am ${want.day}: ${Math.round(want.secs / 60)} min`);
        }
      }
    } catch (e) { errs.push(`Planung: ${e.message}`); }
    run("UPDATE plan_tasks SET sync_hash=?, sync_error=? WHERE id=?", JSON.stringify(errs.length ? prev : want), errs.length ? errs.join(" · ").slice(0, 300) : null, id);
    if (errs.length) writeLog("warn", LOG, `${key}: ${errs.join(" · ")}`);
  }
  const later = id => setTimeout(() => syncOut(id).catch(e => writeLog("warn", LOG, e.message)), 50);

  /* ── API ──────────────────────────────────────────────────────── */
  app.get("/api/todo", (_, res) => res.json(board()));
  /* Für den Kalender: geplante (mit/ohne Uhrzeit) und fällige To-dos im Zeitraum */
  app.get("/api/todo/range", (req, res) => {
    ensure(); const from = String(req.query.from || ""), to = String(req.query.to || "");
    if (!/^\d{4}-\d{2}-\d{2}$/.test(from) || !/^\d{4}-\d{2}-\d{2}$/.test(to)) return res.status(400).json({ error: "from/to" });
    res.json(all(`${SELECT} WHERE COALESCE(p.status,'open') NOT IN ('template') AND ((p.scheduled_date BETWEEN ? AND ?) OR (p.due_date BETWEEN ? AND ? AND COALESCE(p.status,'open')='open'))`, from, to, from, to).map(shape));
  });
  const FIELDS = ["title", "project_id", "task_id", "jira_ticket", "ticket_title", "estimated_minutes", "priority", "due_date", "block_start", "notes", "sort_order"];
  app.post("/api/todo", (req, res) => {
    ensure(); const b = req.body || {};
    const title = String(b.title || b.ticket_title || b.jira_ticket || "").trim(); if (!title) return res.status(400).json({ error: "Was ist zu tun?" });
    const w = when(b.when || null);
    const key = b.jira_ticket ? String(b.jira_ticket).trim().toUpperCase() : null;
    if (key && !b.force && one("SELECT id FROM plan_tasks WHERE UPPER(jira_ticket)=? AND COALESCE(status,'open') NOT IN ('done','template')", key)) return res.status(409).json({ error: `${key} steht schon auf der Liste` });
    let pid = b.project_id || null; if (!pid && key) pid = hooks.sources?.projectByJiraKey(key.split("-")[0]) || one("SELECT project_id FROM tasks WHERE UPPER(jira_ticket)=?", key)?.project_id || null;
    const r = run(`INSERT INTO plan_tasks (title,project_id,task_id,jira_ticket,ticket_title,estimated_minutes,priority,status,scheduled_date,scheduled_week,due_date,block_start,notes,source,event_uid,sync_hash)
      VALUES (?,?,?,?,?,?,?,'open',?,?,?,?,?,?,?,?)`, title, pid, b.task_id || null, key, b.ticket_title || null, b.estimated_minutes ?? 60, b.priority ?? 2,
      w.scheduled_date, w.scheduled_week, b.due_date || null, b.block_start || null, b.notes || null, b.source || "manual", b.event_uid || null,
      // Fälligkeit kam aus dem Ticket → nicht zurückschreiben
      b.due_from_ticket ? JSON.stringify({ due: b.due_date || null }) : null);
    const id = Number(r.lastInsertRowid); later(id);
    res.json(shape(one(`${SELECT} WHERE p.id=?`, id)));
  });
  app.put("/api/todo/:id", (req, res) => {
    ensure(); const id = Number(req.params.id), b = req.body || {}; const cur = one("SELECT * FROM plan_tasks WHERE id=?", id); if (!cur) return res.status(404).json({ error: "nicht gefunden" });
    const sets = [], vals = [];
    for (const f of FIELDS) if (b[f] !== undefined) { sets.push(`${f}=?`); vals.push(f === "jira_ticket" && b[f] ? String(b[f]).toUpperCase() : b[f] === "" ? null : b[f]); }
    if (b.when !== undefined) { const w = when(b.when); sets.push("scheduled_date=?", "scheduled_week=?"); vals.push(w.scheduled_date, w.scheduled_week); if (cur.scheduled_date && cur.scheduled_date !== w.scheduled_date) sets.push("moved_count=COALESCE(moved_count,0)+1"); }
    if (b.done !== undefined) { sets.push("status=?", "completed_at=?"); vals.push(b.done ? "done" : "open", b.done ? new Date().toISOString() : null); }
    if (b.done && cur.source === "gaps") run("INSERT OR REPLACE INTO settings (key,value) VALUES ('gaps_todo_dismissed',?)", cur.ticket_title || "");
    if (sets.length) run(`UPDATE plan_tasks SET ${sets.join(",")} WHERE id=?`, ...vals, id);
    later(id); res.json(shape(one(`${SELECT} WHERE p.id=?`, id)));
  });
  app.delete("/api/todo/:id", async (req, res) => {
    ensure(); const id = Number(req.params.id); const r = one("SELECT * FROM plan_tasks WHERE id=?", id);
    if (r?.tempo_plan_id && on("todo_sync_tempo_plan")) { try { await tempoPush.request("DELETE", `/rest/tempo-planning/1/plan/${encodeURIComponent(r.tempo_plan_id)}`); } catch (_) {} }
    run("UPDATE time_entries SET plan_task_id=NULL WHERE plan_task_id=?", id); run("DELETE FROM plan_tasks WHERE id=?", id); res.json({ ok: true });
  });
  /* Timer auf dem To-do starten: lokale Aufgabe zum Ticket sicherstellen (Plane oder Jira) */
  app.post("/api/todo/:id/start", async (req, res) => {
    try {
      ensure(); const id = Number(req.params.id); const r = one("SELECT * FROM plan_tasks WHERE id=?", id); if (!r) return res.status(404).json({ error: "nicht gefunden" });
      let { project_id: pid, task_id: tid } = r; const key = (r.jira_ticket || "").toUpperCase();
      if (!tid && key) {
        if (systemOf(key) === "plane") { const x = await self("POST", `/plane/ticket/${encodeURIComponent(key)}/task`); tid = x.task_id; pid = x.project_id; }
        else {
          pid = pid || hooks.sources?.projectByJiraKey(key.split("-")[0]);
          if (!pid) return res.status(400).json({ error: `Kein Projekt für ${key} – bitte am To-do ein Projekt wählen` });
          tid = one("SELECT id FROM tasks WHERE project_id=? AND UPPER(jira_ticket)=?", pid, key)?.id || Number(run("INSERT INTO tasks (project_id,title,jira_ticket,status) VALUES (?,?,?,'open')", pid, r.ticket_title || r.title, key).lastInsertRowid);
        }
        run("UPDATE plan_tasks SET task_id=?, project_id=? WHERE id=?", tid, pid, id);
      }
      if (!pid) return res.status(400).json({ error: "Bitte am To-do ein Projekt oder Ticket wählen" });
      await self("POST", "/timer/start", { project_id: pid, task_id: tid || null, plan_task_id: id, comment: r.title !== (r.ticket_title || key) ? r.title : null });
      if (!r.scheduled_date || r.scheduled_date !== today()) { const w = when("today"); run("UPDATE plan_tasks SET scheduled_date=?, scheduled_week=? WHERE id=?", w.scheduled_date, w.scheduled_week, id); }
      res.json({ ok: true });
    } catch (e) { res.status(e.status || 500).json({ error: e.message }); }
  });
  /* Ticket direkt tracken (z. B. aus dem Browser übernommen): passendes To-do nutzen, sonst Aufgabe sicherstellen */
  /* Ticketnummer → Aufgabe im richtigen Projekt (Plane oder Jira), legt sie bei Bedarf an */
  async function ensureTicketTask(key, title) {
    let pid = null, tid = null;
    // schon bekannt → direkt nehmen (kein Netz nötig)
    const known = one("SELECT t.id, t.project_id FROM tasks t JOIN projects p ON p.id=t.project_id WHERE UPPER(t.jira_ticket)=? AND COALESCE(p.status,'active')<>'archived' ORDER BY t.id DESC LIMIT 1", key);
    if (known) { pid = known.project_id; tid = known.id; }
    else if (systemOf(key) === "plane") { const x = await self("POST", `/plane/ticket/${encodeURIComponent(key)}/task`); tid = x.task_id; pid = x.project_id; }
    else {
      pid = hooks.sources?.projectByJiraKey(key.split("-")[0]) || one("SELECT project_id FROM tasks WHERE UPPER(jira_ticket)=?", key)?.project_id || null;
      if (!title) try { title = String((await tempoPush.request("GET", `/rest/api/2/issue/${encodeURIComponent(key)}?fields=summary`))?.fields?.summary || "").slice(0, 255); } catch (_) {}
      if (!pid) { const e = new Error(`Das Jira-Projekt ${key.split("-")[0]} ist noch mit keinem Projekt verknüpft (Projekte › Neue Projekte)`); e.status = 400; throw e; }
      const t = one("SELECT id, title FROM tasks WHERE project_id=? AND UPPER(jira_ticket)=?", pid, key);
      tid = t?.id || Number(run("INSERT INTO tasks (project_id,title,jira_ticket,status) VALUES (?,?,?,'open')", pid, title || key, key).lastInsertRowid);
      if (t && title && (!t.title || t.title.toUpperCase() === key)) run("UPDATE tasks SET title=? WHERE id=?", title, t.id);
    }
    const row = one("SELECT t.id, t.title, t.jira_ticket, p.name AS project_name FROM tasks t LEFT JOIN projects p ON p.id=t.project_id WHERE t.id=?", tid);
    return { project_id: pid, task_id: tid, title: row?.title || title || key, project_name: row?.project_name || null };
  }
  const keyOk = k => /^[A-Z][A-Z0-9_]*-\d+$/.test(k);
  /* Nur zuordnen (z. B. beim Nachtragen) – startet nichts */
  app.post("/api/ticket/ensure", async (req, res) => {
    try {
      ensure();
      const key = String(req.body?.key || "").trim().toUpperCase(), title = String(req.body?.title || "").trim().slice(0, 255);
      if (!keyOk(key)) return res.status(400).json({ error: "Keine Ticketnummer" });
      res.json({ ok: true, key, ...(await ensureTicketTask(key, title)) });
    } catch (e) { res.status(e.status || 500).json({ error: e.message }); }
  });
  app.post("/api/ticket/start", async (req, res) => {
    try {
      ensure();
      const key = String(req.body?.key || "").trim().toUpperCase(); const title = String(req.body?.title || "").trim().slice(0, 255);
      if (!keyOk(key)) return res.status(400).json({ error: "Keine Ticketnummer" });
      const todo = one("SELECT id FROM plan_tasks WHERE UPPER(jira_ticket)=? AND COALESCE(status,'open')='open' ORDER BY id DESC LIMIT 1", key);
      if (todo) return res.json(await self("POST", `/todo/${todo.id}/start`));
      const { project_id: pid, task_id: tid } = await ensureTicketTask(key, title);
      await self("POST", "/timer/start", { project_id: pid, task_id: tid });
      writeLog("info", LOG, `Timer auf ${key} gestartet (aus dem Browser übernommen)`);
      res.json({ ok: true, project_id: pid, task_id: tid });
    } catch (e) { res.status(e.status || 500).json({ error: e.message }); }
  });
  app.put("/api/todo-settings", (req, res) => {
    for (const k of ["todo_sync_due", "todo_sync_tempo_plan", "todo_sync_plane_start"]) if (req.body?.[k] !== undefined) run("INSERT OR REPLACE INTO settings (key,value) VALUES (?,?)", k, req.body[k] ? "1" : "0");
    res.json({ ok: true });
  });

  /* ── Vorschläge ───────────────────────────────────────────────── */
  const PRIO = { Dringend: 1, Highest: 1, Hoch: 1, High: 1, Mittel: 2, Medium: 2, Niedrig: 3, Low: 3, Lowest: 3 };
  let sugCache = { at: 0, data: null };
  app.get("/api/todo/suggest", async (req, res) => {
    ensure();
    if (req.query.fresh !== "1" && sugCache.data && Date.now() - sugCache.at < 5 * 60e3) return res.json(filterDismissed(sugCache.data));
    const t = today(), tm = nextWorkday(t), we = weekEnd(t), soon = iso(new Date(day(t).getTime() + 7 * 864e5));
    const onList = new Set(all("SELECT UPPER(jira_ticket) AS k FROM plan_tasks WHERE jira_ticket IS NOT NULL AND COALESCE(status,'open')<>'template' AND (status<>'done' OR substr(completed_at,1,10)>=?)", iso(new Date(Date.now() - 2 * 864e5))).map(r => r.k));
    const out = [];
    // 1. Mir zugewiesene Tickets (Plane + Jira) – fällig/dringend zuerst
    const tickets = [];
    try { const p = await self("GET", "/plane/tickets?mine=1"); for (const i of p.issues || []) tickets.push({ ...i, system: "plane", project_id: i.local_project_id }); } catch (_) {}
    if (getSettings().jira_enabled !== "0") try { const j = await self("GET", "/jira/search?mine=1"); for (const i of j.issues || []) tickets.push({ ...i, system: "jira", project_id: hooks.sources?.projectByJiraKey(i.project) || null }); } catch (_) {}
    for (const i of tickets) {
      if (onList.has(i.key) || /done|closed|erledigt|cancel/i.test(`${i.status_cat || ""} ${i.state_group || ""}`)) continue;
      const pr = PRIO[i.priority] || 2, due = i.due || null;
      let score = 0, why = [];
      if (due && due < t) { score += 100; why.push(`überfällig seit ${due}`); } else if (due && due <= tm) { score += 80; why.push(due === t ? "heute fällig" : "morgen fällig"); }
      else if (due && due <= soon) { score += 50; why.push(`fällig ${due}`); } else if (due) { score += 10; why.push(`fällig ${due}`); }
      if (pr === 1) { score += 40; why.push(i.priority); }
      if (!why.length) why.push("dir zugewiesen");
      out.push({ kind: "ticket", key: `t:${i.key}`, title: i.summary || i.key, ticket: i.key, ticket_title: i.summary, system: i.system, project_id: i.project_id, project_name: i.project_name,
        due, priority: pr, score, why: why.join(" · "), when: due && due <= t ? "today" : due && due <= tm ? "tomorrow" : due && due <= we ? "week" : pr === 1 ? "week" : "later" });
    }
    // 2. Termine morgen / diese Woche ohne To-do → Vorbereitung
    try {
      for (const ev of all("SELECT uid, title, date, start_time FROM calendar_events WHERE date>? AND date<=? ORDER BY date, start_time LIMIT 40", t, we > tm ? we : tm)) {
        if (/^(mittag|pause|lunch|urlaub|frei|blocker|fokus)/i.test(ev.title || "")) continue;
        if (one("SELECT 1 FROM plan_tasks WHERE event_uid=?", ev.uid)) continue;
        out.push({ kind: "event", key: `e:${ev.uid}`, title: `Vorbereiten: ${ev.title}`, event_uid: ev.uid, due: ev.date, priority: 2, estimated_minutes: 15,
          score: ev.date === tm ? 45 : 25, why: `Termin ${ev.date === tm ? "morgen" : ev.date} ${String(ev.start_time).slice(11, 16)}`, when: ev.date === tm ? "today" : "tomorrow" });
      }
    } catch (_) {}
    // 3. Liegengebliebenes: oft verschoben oder lange im Eingang
    for (const r of all("SELECT id, title, jira_ticket, moved_count, created_at, scheduled_date FROM plan_tasks WHERE COALESCE(status,'open')='open' AND (COALESCE(moved_count,0)>=3 OR (scheduled_date IS NULL AND scheduled_week IS NULL AND created_at < ?))", iso(new Date(Date.now() - 14 * 864e5)) + " 00:00:00")) {
      out.push({ kind: "stale", key: `s:${r.id}`, todo_id: r.id, title: r.title, ticket: r.jira_ticket, score: 30, why: r.moved_count >= 3 ? `${r.moved_count}× verschoben – heute machen, abgeben oder streichen?` : "seit über 2 Wochen im Eingang", when: "today" });
    }
    // 4. Wiederkehrendes: Vorschläge für Vorlagen, die es noch nicht gibt
    const have = new Set(all("SELECT title FROM todo_templates").map(r => r.title));
    for (const [title, rule, why] of [["Stundenzettel prüfen und einreichen", "month-last-workday", "jeden letzten Arbeitstag im Monat"], ["Woche planen", "week-1", "jeden Montag"], ["Monatsabschluss: Zeiten nachtragen", "month-25", "vor dem Monatsende"]])
      if (!have.has(title)) out.push({ kind: "recurring", key: `r:${rule}`, title, rule, why, score: 5, when: null });
    out.sort((a, b) => b.score - a.score);
    { const seen = new Set(), dedup = []; for (const x of out) { const k = x.ticket ? "t:" + String(x.ticket).toUpperCase() : x.key; if (seen.has(k)) continue; seen.add(k); if (x.ticket) x.system = systemOf(x.ticket) || x.system; dedup.push(x); } out.length = 0; out.push(...dedup); }
    sugCache = { at: Date.now(), data: out.slice(0, 40) };
    res.json(filterDismissed(sugCache.data));
  });
  function filterDismissed(list) { const t = today(); const d = new Set(all("SELECT key FROM todo_dismissed WHERE until IS NULL OR until>=?", t).map(r => r.key)); return list.filter(x => !d.has(x.key) && !(x.todo_id && !one("SELECT 1 FROM plan_tasks WHERE id=? AND status='open'", x.todo_id))
    && !(x.kind === "ticket" && x.ticket && one("SELECT 1 FROM plan_tasks WHERE UPPER(jira_ticket)=? AND COALESCE(status,'open')='open'", String(x.ticket).toUpperCase()))
    && !(x.kind === "event" && x.event_uid && one("SELECT 1 FROM plan_tasks WHERE event_uid=?", x.event_uid))); }
  app.post("/api/todo/suggest/dismiss", (req, res) => { ensure(); const k = String(req.body?.key || ""); if (k) run("INSERT OR REPLACE INTO todo_dismissed (key,until) VALUES (?,?)", k, req.body?.days ? iso(new Date(Date.now() + req.body.days * 864e5)) : null); res.json({ ok: true }); });

  /* Vorlagen (wiederkehrend) */
  app.get("/api/todo/templates", (_, res) => { ensure(); res.json(all("SELECT * FROM todo_templates ORDER BY id")); });
  app.post("/api/todo/templates", (req, res) => {
    ensure(); const b = req.body || {}; if (!b.title || !b.rule) return res.status(400).json({ error: "Titel und Rhythmus fehlen" });
    const r = run("INSERT INTO todo_templates (title,rule,project_id,jira_ticket,estimated_minutes,priority) VALUES (?,?,?,?,?,?)", b.title, b.rule, b.project_id || null, b.jira_ticket || null, b.estimated_minutes ?? 30, b.priority ?? 2);
    sugCache.at = 0; runTemplates(); res.json({ id: Number(r.lastInsertRowid) });
  });
  app.delete("/api/todo/templates/:id", (req, res) => { ensure(); run("UPDATE todo_templates SET active=0 WHERE id=?", Number(req.params.id)); res.json({ ok: true }); });

  /* Für den Assistenten: was ist bei der Planung zu tun? */
  function planningHints() {
    ensure();
    const t = today(), tm = nextWorkday(t), h = new Date().getHours(), wdN = new Date().getDay(), out = [];
    const n = (sql, ...a) => one(sql, ...a)?.n || 0;
    const open = "COALESCE(status,'open')='open'";
    const overdue = n(`SELECT COUNT(*) AS n FROM plan_tasks WHERE ${open} AND ((scheduled_date IS NOT NULL AND scheduled_date<?) OR (due_date IS NOT NULL AND due_date<?))`, t, t);
    if (overdue) out.push({ id: "todo_overdue", level: "warn", title: "Überfällige To-dos", text: "neu planen, erledigen oder streichen", count: overdue });
    if (h >= 14 && workday(t) && !n(`SELECT COUNT(*) AS n FROM plan_tasks WHERE ${open} AND scheduled_date=?`, tm)) out.push({ id: "todo_plan_tomorrow", level: "info", title: `${day(tm).toLocaleDateString("de-DE", { weekday: "long" })} planen`, text: "noch nichts geplant – Vorschläge liegen bereit", count: 1 });
    if (((wdN === 1 && h < 12) || (wdN === 5 && h >= 12)) && n(`SELECT COUNT(*) AS n FROM plan_tasks WHERE ${open} AND scheduled_week=?`, isoWeek(wdN === 5 ? nextWorkday(t) : t)) < 3) out.push({ id: "todo_plan_week", level: "info", title: wdN === 5 ? "Nächste Woche planen" : "Woche planen", text: "weniger als 3 To-dos für die Woche", count: 1 });
    return out;
  }

  setInterval(() => { try { runTemplates(); } catch (_) {} }, 60 * 60e3);   // ensure() läuft lazy (DB ist bei der Registrierung noch nicht offen)
  return { board, planningHints, syncOut };
};
