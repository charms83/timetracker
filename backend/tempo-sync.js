/* ═══════════════════════════════════════════════════════════════════
   Tempo-Abgleich (v6.17.0) – alle eigenen Tempo-Zeiten ins System holen
   ───────────────────────────────────────────────────────────────────
   NUR LESEN. Nichts wird nach Tempo oder Plane geschrieben.
   • Holt die eigenen Worklogs (worker = eigener Jira-Benutzerschlüssel),
     nie die von anderen.
   • Speichert sie als Buchungen mit source='tempo' und tempo_worklog_id
     (= stabile Kennung). Diese Buchungen werden nie übertragen und im
     Wochenraster nicht verändert – sie sind ein Spiegel von Tempo.
   • Buchungen, die das System selbst nach Tempo geschrieben hat
     (tempo_push_id), werden erkannt und NICHT doppelt angelegt.
   • Wiederholbar: geänderte Worklogs werden nachgezogen, in Tempo
     gelöschte im abgeglichenen Zeitraum hier auch entfernt.
   • Danach automatisch: alle 12 h die letzten 2 Monate.
   • Optional „Grundimport“: alte lokale Buchungen vor dem Stichtag
     (nie übertragen) werden durch die Tempo-Zeiten ersetzt – vorher
     wird eine Sicherung angelegt.
   Für die Server-Version: dieselbe Kennung (tempo_worklog_id) macht den
   Abgleich je Person idempotent; der Server übernimmt diese Aufgabe 1:1.
═══════════════════════════════════════════════════════════════════ */
module.exports = function registerTempoSync(app, ctx) {
  const { one, all, run, getSettings, writeLog, localDateOf, tempoPush, hooks, createBackup } = ctx;
  const LOG = "tempo-abgleich";
  const pad = n => String(n).padStart(2, "0");
  const set = (k, v) => run("INSERT OR REPLACE INTO settings (key,value) VALUES (?,?)", k, String(v));
  let running = null;   // { phase, done, total }

  /* ── Feiertage Niedersachsen für jedes Jahr (Ostern nach Gauß) ── */
  function niHolidays(y) {
    const a = y % 19, b = Math.floor(y / 100), c = y % 100, d = Math.floor(b / 4), e = b % 4, f = Math.floor((b + 8) / 25), g = Math.floor((b - f + 1) / 3),
      h = (19 * a + b - d - g + 15) % 30, i = Math.floor(c / 4), k = c % 4, l = (32 + 2 * e + 2 * i - h - k) % 7, m = Math.floor((a + 11 * h + 22 * l) / 451),
      mo = Math.floor((h + l - 7 * m + 114) / 31), da = ((h + l - 7 * m + 114) % 31) + 1;
    const easter = new Date(y, mo - 1, da, 12);
    const rel = n => { const x = new Date(easter); x.setDate(x.getDate() + n); return `${x.getFullYear()}-${pad(x.getMonth() + 1)}-${pad(x.getDate())}`; };
    const out = [[`${y}-01-01`, "Neujahr"], [rel(-2), "Karfreitag"], [rel(1), "Ostermontag"], [`${y}-05-01`, "Tag der Arbeit"], [rel(39), "Christi Himmelfahrt"],
      [rel(50), "Pfingstmontag"], [`${y}-10-03`, "Tag der Deutschen Einheit"], [`${y}-12-25`, "1. Weihnachtstag"], [`${y}-12-26`, "2. Weihnachtstag"]];
    if (y >= 2018) out.push([`${y}-10-31`, "Reformationstag"]);
    return out;
  }
  function ensureHolidays(from, to) {
    let n = 0;
    for (let y = +from.slice(0, 4); y <= +to.slice(0, 4); y++) for (const [date, name] of niHolidays(y)) {
      if (date < from || date > to) continue;
      if (one("SELECT id FROM absences WHERE date_from=? AND type='feiertag'", date)) continue;
      run("INSERT INTO absences (date_from,date_to,type,hours,note) VALUES (?,?,'feiertag',NULL,?)", date, date, name); n++;
    }
    return n;
  }

  /* ── Projekt zum Jira-Schlüssel: verknüpftes Projekt, sonst neu anlegen (Name wie in Jira, archiviert) ── */
  const projCache = new Map(), newProjects = new Set();
  async function projectFor(pkey) {
    if (!pkey) return null;
    if (projCache.has(pkey)) return projCache.get(pkey);
    let pid = hooks.sources?.projectByJiraKey(pkey) || null;
    if (!pid) {
      let name = pkey;
      try { const p = await tempoPush.request("GET", `/rest/api/2/project/${encodeURIComponent(pkey)}`); if (p?.name) name = p.name; } catch (_) {}
      const ex = one("SELECT id FROM projects WHERE name=?", name);
      pid = ex ? ex.id : Number(run("INSERT INTO projects (name,status) VALUES (?,'archived')", name).lastInsertRowid);
      newProjects.add(`${name} (${pkey})`);
      try { hooks.sources?.linkBy("jira", pkey, pid, pkey, name); } catch (_) {}
      writeLog("info", LOG, `Projekt „${name}“ (${pkey}) für alte Tempo-Zeiten ${ex ? "verknüpft" : "angelegt (archiviert)"}`);
    }
    projCache.set(pkey, pid);
    return pid;
  }
  function taskFor(pid, key, title) {
    key = String(key || "").toUpperCase();
    if (!pid || !/^[A-Z][A-Z0-9_]*-\d+$/.test(key)) return null;
    const t = one("SELECT id, title FROM tasks WHERE project_id=? AND UPPER(TRIM(jira_ticket))=?", pid, key)
      || one("SELECT id, title FROM tasks WHERE UPPER(TRIM(jira_ticket))=?", key);
    if (t) { if (title && (!t.title || t.title.toUpperCase() === key)) run("UPDATE tasks SET title=? WHERE id=?", String(title).slice(0, 255), t.id); return t.id; }
    return Number(run("INSERT INTO tasks (project_id,title,jira_ticket,status) VALUES (?,?,?,'done')", pid, String(title || key).slice(0, 255), key).lastInsertRowid);
  }

  /* Monate [from..to] in Stücken holen (Tempo DC mag keine Riesenabfragen) */
  function months(from, to) {
    const out = []; let d = new Date(from.slice(0, 7) + "-01T12:00:00");
    while (localDateOf(d.getTime()) <= to) {
      const s = localDateOf(d.getTime()), e = new Date(d.getFullYear(), d.getMonth() + 1, 0, 12);
      out.push([s < from ? from : s, localDateOf(e.getTime()) > to ? to : localDateOf(e.getTime())]);
      d = new Date(d.getFullYear(), d.getMonth() + 1, 1, 12);
    }
    return out;
  }
  async function me() {
    const s = getSettings();
    if (s.jira_userkey) return s.jira_userkey;
    const m = await tempoPush.request("GET", "/rest/api/2/myself");
    if (m?.key) set("jira_userkey", m.key); if (m?.name) set("jira_username", m.name);
    return m?.key || m?.name;
  }
  const norm = w => {
    const st = String(w.started || w.dateStarted || "").replace(" ", "T");
    const date = st.slice(0, 10), time = st.slice(11, 19) || "00:00:00";
    const acc = w.attributes?._Account_?.value || Object.values(w.attributes || {}).find(x => /account/i.test(x?.name || x?.key || ""))?.value || null;
    return { id: Number(w.tempoWorklogId ?? w.id), date, time, secs: w.timeSpentSeconds || 0, key: String(w.issue?.key || w.originTaskId || "").toUpperCase(),
      summary: w.issue?.summary || "", comment: w.comment || "", account: acc, worker: w.worker || w.author?.key || w.author?.name || "" };
  };

  /* Abgleich. dry=true → nur zählen. replaceOld → alte lokale Buchungen vor dem Stichtag ersetzen */
  async function sync({ from, to, dry = false, replaceOld = false, quiet = false }) {
    const c = tempoPush.cfg(); if (!c.base || !c.token) throw Object.assign(new Error("Jira/Tempo ist nicht verbunden"), { status: 400 });
    if (running) throw Object.assign(new Error("Abgleich läuft bereits"), { status: 409 });
    const worker = await me(); if (!worker) throw new Error("Eigener Jira-Benutzer nicht ermittelbar");
    const since = /^\d{4}-\d{2}-\d{2}$/.test(getSettings().tempo_push_since || "") ? getSettings().tempo_push_since : null;
    const st = { from, to, worklogs: 0, hours: 0, new: 0, changed: 0, removed: 0, own_skipped: 0, foreign_skipped: 0, projects_new: [], years: {}, old_local: 0, old_local_h: 0, holidays: 0, errors: [] };
    try { run("ALTER TABLE time_entries ADD COLUMN tempo_account TEXT"); } catch (_) {}
    projCache.clear(); newProjects.clear();
    const ms = months(from, to); running = { phase: "lesen", done: 0, total: ms.length };
    const own = new Set(all("SELECT tempo_push_id FROM time_entries WHERE tempo_push_id IS NOT NULL").map(r => String(r.tempo_push_id)));
    const seen = new Set(), complete = [];
    try {
      if (!dry) createBackup && createBackup("vor-tempo-abgleich");
      for (const [a, b] of ms) {
        let list;
        try { list = await tempoPush.request("POST", "/rest/tempo-timesheets/4/worklogs/search", { from: a, to: b, worker: [worker] }); }
        catch (e) { st.errors.push(`${a.slice(0, 7)}: ${e.message}`); running.done++; continue; }
        complete.push([a, b]);
        // Uhrzeit fehlt in Tempo oft (00:00) → für die Ansicht ab 08:00 hintereinander legen
        const cursor = {};
        for (const w of (Array.isArray(list) ? list : []).map(norm).sort((x, y) => (x.date + x.time).localeCompare(y.date + y.time))) {
          if (!w.id || !w.date || !w.secs) continue;
          if (w.worker && w.worker !== worker && w.worker !== getSettings().jira_username) { st.foreign_skipped++; continue; }   // nur eigene Zeiten
          if (seen.has(w.id)) continue;   // doppelt geliefert
          seen.add(w.id);
          if (own.has(String(w.id))) { st.own_skipped++; continue; }                                                          // vom System selbst übertragen
          st.worklogs++; st.hours += w.secs / 3600; st.years[w.date.slice(0, 4)] = (st.years[w.date.slice(0, 4)] || 0) + w.secs / 3600;
          let time = w.time;
          if (time === "00:00:00") { const m = cursor[w.date] ?? 8 * 60; time = `${pad(Math.floor(m / 60))}:${pad(m % 60)}:00`; cursor[w.date] = m + Math.round(w.secs / 60); }
          const startISO = new Date(`${w.date}T${time}`).toISOString(), endISO = new Date(Date.parse(startISO) + w.secs * 1000).toISOString();
          const ex = one("SELECT id, date, duration_seconds, comment, task_id, tempo_account FROM time_entries WHERE tempo_worklog_id=?", w.id);
          if (dry) { if (!ex) st.new++; const pk = w.key.split("-")[0]; if (pk && !hooks.sources?.projectByJiraKey(pk)) newProjects.add(pk); continue; }
          const pkey = w.key.split("-")[0];
          const pid = await projectFor(pkey);
          const tid = taskFor(pid, w.key, w.summary);
          if (ex) {
            if (ex.date !== w.date || ex.duration_seconds !== w.secs || (ex.comment || "") !== w.comment || ex.task_id !== tid || (ex.tempo_account || null) !== w.account) {
              run("UPDATE time_entries SET project_id=?, task_id=?, date=?, start_time=?, end_time=?, duration_seconds=?, comment=?, tempo_account=? WHERE id=?",
                pid, tid, w.date, startISO, endISO, w.secs, w.comment || null, w.account, ex.id);
              st.changed++;
            }
          } else {
            run(`INSERT INTO time_entries (project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,source,tempo_worklog_id,tempo_account) VALUES (?,?,?,?,?,?,1,?,'tempo',?,?)`,
              pid, tid, startISO, endISO, w.secs, w.comment || null, w.date, w.id, w.account);
            st.new++;
          }
        }
        running.done++;
      }
      // In Tempo gelöscht → hier auch weg (nur vollständig gelesene Monate, nur Tempo-Spiegel)
      for (const [a, b] of complete) for (const r of all("SELECT id, tempo_worklog_id FROM time_entries WHERE source='tempo' AND date BETWEEN ? AND ?", a, b))
        if (!seen.has(Number(r.tempo_worklog_id))) { if (!dry) run("DELETE FROM time_entries WHERE id=?", r.id); st.removed++; }
      // Alte lokale Buchungen vor dem Stichtag (nie übertragen) – im Grundimport ersetzen
      if (since) {
        const old = one(`SELECT COUNT(*) AS n, COALESCE(SUM(duration_seconds),0) AS s FROM time_entries WHERE date>=? AND date<? AND source NOT IN ('tempo') AND tempo_push_id IS NULL`, from, since);
        st.old_local = old.n; st.old_local_h = Math.round(old.s / 36) / 100;
        if (replaceOld && !dry && old.n) {
          run(`DELETE FROM time_entries WHERE date>=? AND date<? AND source NOT IN ('tempo') AND tempo_push_id IS NULL`, from, since);
          writeLog("warn", LOG, `Grundimport: ${old.n} alte lokale Buchungen vor ${since} durch Tempo-Zeiten ersetzt (Sicherung vorher angelegt)`);
        }
      }
      if (!dry) {
        st.holidays = ensureHolidays(from, to);
        const ts = getSettings().tracking_start_date;
        if (!ts || ts > from) set("tracking_start_date", from);
        set("tempo_sync_from", getSettings().tempo_sync_from && getSettings().tempo_sync_from < from ? getSettings().tempo_sync_from : from);
        set("tempo_sync_last", new Date().toISOString());
        try { hooks.absences?.fromTempo(); } catch (_) {}   // Urlaub/Krank aus dem Abwesenheitsprojekt übernehmen
        if (!quiet || st.new || st.changed || st.removed) writeLog("info", LOG, `Abgleich ${from} – ${to}: ${st.new} neu, ${st.changed} geändert, ${st.removed} entfernt, ${st.own_skipped} eigene Übertragungen übersprungen`);
      }
      st.hours = Math.round(st.hours * 100) / 100; for (const y in st.years) st.years[y] = Math.round(st.years[y] * 10) / 10;
      st.projects_new = [...newProjects];
      return st;
    } finally { running = null; }
  }

  const today = () => localDateOf(Date.now());
  app.get("/api/tempo-sync", (_, res) => {
    const s = getSettings();
    const n = one("SELECT COUNT(*) AS n, MIN(date) AS a, MAX(date) AS b, COALESCE(SUM(duration_seconds),0) AS s FROM time_entries WHERE source='tempo'");
    res.json({ from: s.tempo_sync_from || null, last: s.tempo_sync_last || null, running, entries: n.n, first: n.a, latest: n.b, hours: Math.round(n.s / 36) / 100,
      since: s.tempo_push_since || null, tracking_start: s.tracking_start_date || null, connected: !!(tempoPush.cfg().base && tempoPush.cfg().token) });
  });
  app.post("/api/tempo-sync", async (req, res) => {
    const b = req.body || {};
    const from = /^\d{4}-\d{2}-\d{2}$/.test(b.from || "") ? b.from : getSettings().tempo_sync_from;
    if (!from) return res.status(400).json({ error: "Bitte angeben, ab wann (z. B. Eintrittsdatum)" });
    try { res.json(await sync({ from, to: today(), dry: !!b.dry, replaceOld: !!b.replace_old })); }
    catch (e) { writeLog("error", LOG, e.message); res.status(e.status || 500).json({ error: e.message }); }
  });

  // Automatisch: alle 12 h die letzten 2 Monate (nur wenn einmal eingerichtet)
  setInterval(() => {
    const s = getSettings(); if (!s.tempo_sync_from || running) return;
    if (s.tempo_sync_last && Date.now() - Date.parse(s.tempo_sync_last) < 12 * 3600e3) return;
    const d = new Date(); d.setDate(1); d.setMonth(d.getMonth() - 1);
    const from = localDateOf(d.getTime()) > s.tempo_sync_from ? localDateOf(d.getTime()) : s.tempo_sync_from;
    sync({ from, to: today(), quiet: true }).catch(e => writeLog("warn", LOG, "Automatischer Abgleich: " + e.message));
  }, 15 * 60e3);

  return { sync };
};
