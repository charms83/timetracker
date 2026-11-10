/* ═══════════════════════════════════════════════════════════════════
   Anwesenheit am Mac + Tagesüberblick (v6.12.0)
   ───────────────────────────────────────────────────────────────────
   Optional (Einstellung presence_track, Standard an): Die Mac-App meldet
   jede Minute, ob jemand am Rechner ist (Eingabe in den letzten 5 Minuten,
   Bildschirm nicht gesperrt). Gespeichert wird NUR die Minute – kein Inhalt,
   keine Programme, kein Bildschirm. Daraus entsteht „von wann bis wann“
   gearbeitet wurde, und wo Lücken ohne Buchung sind.
═══════════════════════════════════════════════════════════════════ */
module.exports = function registerPresence(app, ctx) {
  const { one, all, run, getSettings, localDateOf } = ctx;
  let ready = false;
  const ensure = () => { if (ready) return; run(`CREATE TABLE IF NOT EXISTS presence_min (ts TEXT PRIMARY KEY, date TEXT NOT NULL)`);
    try { run("CREATE INDEX IF NOT EXISTS idx_presence_date ON presence_min(date)"); } catch (_) {} ready = true; };
  const on = () => getSettings().presence_track !== "0";
  const pad = n => String(n).padStart(2, "0");
  const localMinute = d => `${localDateOf(d.getTime())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;

  app.post("/api/presence", (req, res) => {
    ensure();
    if (!on()) return res.json({ ok: true, off: true });
    if (req.body?.active) { const d = new Date(); run("INSERT OR IGNORE INTO presence_min (ts,date) VALUES (?,?)", localMinute(d), localDateOf(d.getTime())); }
    // älter als 120 Tage weg
    if (Math.random() < 0.01) run("DELETE FROM presence_min WHERE date < ?", localDateOf(Date.now() - 120 * 864e5));
    res.json({ ok: true });
  });

  /* Überschneidende Buchungen der letzten Tage – wahrscheinlich doppelt (Termin + Timer) oder zwei Tickets gleichzeitig */
  function overlaps(days) {
    let ign = []; try { ign = JSON.parse(getSettings().overlap_ignore || "[]"); } catch (_) {}
    const rows = all(`SELECT e.id, e.date, e.start_time, e.end_time, e.duration_seconds, e.comment, e.task_id, e.project_id, e.source, p.name AS project_name, t.jira_ticket, t.title AS task_title
      FROM time_entries e LEFT JOIN projects p ON p.id=e.project_id LEFT JOIN tasks t ON t.id=e.task_id
      WHERE e.date>=? AND e.end_time IS NOT NULL AND COALESCE(e.source,'') NOT IN ('tempo','tempo_csv') ORDER BY e.date, e.start_time`, localDateOf(Date.now() - (days - 1) * 864e5));
    const out = [];
    for (let i = 0; i < rows.length; i++) for (let j = i + 1; j < rows.length && rows[j].date === rows[i].date; j++) {
      const a = rows[i], b = rows[j];
      const ov = (Math.min(Date.parse(a.end_time), Date.parse(b.end_time)) - Math.max(Date.parse(a.start_time), Date.parse(b.start_time))) / 60e3;
      if (ov < 3 || ign.includes(`${Math.min(a.id, b.id)}-${Math.max(a.id, b.id)}`)) continue;
      const same = (a.task_id && a.task_id === b.task_id) || (a.jira_ticket && a.jira_ticket === b.jira_ticket) || (!a.task_id && !b.task_id && a.project_id === b.project_id);
      const shorter = Math.min(a.duration_seconds || 0, b.duration_seconds || 0) / 60;
      out.push({ a, b, minutes: Math.round(ov), same, suggest: same || ov >= 0.7 * shorter ? "merge" : "trim" });
    }
    return out.reverse();
  }
  app.get("/api/overlaps", (req, res) => res.json(overlaps(Math.max(1, Math.min(90, parseInt(req.query.days) || 14)))));
  app.post("/api/overlaps/ignore", (req, res) => {
    let ign = []; try { ign = JSON.parse(getSettings().overlap_ignore || "[]"); } catch (_) {}
    const a = Number(req.body?.a), b = Number(req.body?.b); ign.push(`${Math.min(a, b)}-${Math.max(a, b)}`);
    // „Ist richtig so“ → Überschneidungs-Markierung an beiden Buchungen entfernen
    for (const id of [a, b]) { const e = one("SELECT check_flags FROM time_entries WHERE id=?", id); if (e?.check_flags) { const f = e.check_flags.split(",").filter(x => x !== "overlap"); run("UPDATE time_entries SET check_flags=? WHERE id=?", f.length ? f.join(",") : null, id); } }
    run("INSERT OR REPLACE INTO settings (key,value) VALUES ('overlap_ignore',?)", JSON.stringify([...new Set(ign)].slice(-500))); res.json({ ok: true });
  });

  /* Minuten → Spannen (Lücken bis 10 min gelten als durchgehend) */
  function spans(date) {
    const mins = all("SELECT ts FROM presence_min WHERE date=? ORDER BY ts", date).map(r => { const [h, m] = r.ts.slice(11, 16).split(":").map(Number); return h * 60 + m; });
    const out = [];
    for (const m of mins) {
      const last = out[out.length - 1];
      if (last && m - last.e <= 10) last.e = m + 1; else out.push({ s: m, e: m + 1 });
    }
    return out;
  }
  const minOf = iso => { const d = new Date(iso); return d.getHours() * 60 + d.getMinutes(); };

  /* Tagesüberblick: Anwesenheit, Arbeitstag, Buchungen, offene Termine, offene SSH-Sitzungen, Lücken */
  app.get("/api/day-overview", (req, res) => res.json(dayOverview(/^\d{4}-\d{2}-\d{2}$/.test(req.query.date || "") ? req.query.date : localDateOf(Date.now()))));
  function dayOverview(date) {
    ensure();
    const isToday = date === localDateOf(Date.now());
    const nowMin = minOf(new Date().toISOString());
    const pres = on() ? spans(date) : [];
    const wd = one("SELECT start_time, end_time FROM work_days WHERE date=?", date);
    const entries = all(`SELECT e.id, e.start_time, e.end_time, e.duration_seconds, e.comment, e.project_id, e.task_id, e.source, e.tempo_push_id, (t.plane_work_item_id IS NOT NULL) AS plane, p.name AS project_name, t.jira_ticket, t.title AS task_title
      FROM time_entries e LEFT JOIN projects p ON p.id=e.project_id LEFT JOIN tasks t ON t.id=e.task_id WHERE e.date=? AND COALESCE(e.is_absence,0)=0 ORDER BY e.start_time`, date)
      .map(e => ({ ...e, s: minOf(e.start_time), e: e.end_time ? minOf(e.end_time) : (isToday ? nowMin : minOf(e.start_time) + 1) }));
    const pauses = all("SELECT start_time, end_time FROM pauses WHERE date=?", date).map(p => ({ s: minOf(p.start_time), e: p.end_time ? minOf(p.end_time) : (isToday ? nowMin : minOf(p.start_time)) }));
    let events = [];
    try {
      events = all(`SELECT c.uid, c.title, c.start_time, c.end_time FROM calendar_events c WHERE c.date=? ORDER BY c.start_time`, date)
        .map(c => ({ ...c, s: minOf(c.start_time), e: minOf(c.end_time) }))
        .filter(c => !entries.some(b => Math.max(0, Math.min(b.e, c.e) - Math.max(b.s, c.s)) >= 0.5 * (c.e - c.s)));
      const handled = new Set(all("SELECT key FROM meeting_state WHERE status IN ('booked','dismissed')").map(r => r.key));
      events = events.filter(c => !handled.has("cal:" + c.uid) && !handled.has(c.uid));
    } catch (_) {}
    let ssh = [];
    try {
      ssh = all(`SELECT id, hostname, target, start_time, end_time, duration_seconds FROM ssh_sessions WHERE date=? AND time_entry_id IS NULL ORDER BY start_time`, date)
        .map(x => ({ ...x, server: x.hostname || x.target, s: minOf(x.start_time), e: x.end_time ? minOf(x.end_time) : (isToday ? nowMin : minOf(x.start_time) + 1) }))
        .filter(x => x.e - x.s >= 1 && !entries.some(b => Math.max(0, Math.min(b.e, x.e) - Math.max(b.s, x.s)) >= 0.8 * (x.e - x.s)));
    } catch (_) {}
    // Arbeitstag: Anwesenheit, sonst Arbeitstag-Start/-Ende, sonst erste/letzte Buchung
    const first = Math.min(...[pres[0]?.s, wd ? minOf(wd.start_time) : null, entries[0]?.s].filter(v => v != null));
    const lastEnd = Math.max(...[pres[pres.length - 1]?.e, wd?.end_time ? minOf(wd.end_time) : (wd && isToday ? nowMin : null), ...entries.map(e => e.e)].filter(v => v != null));
    const work = Number.isFinite(first) && Number.isFinite(lastEnd) ? { s: first, e: lastEnd } : null;
    // Lücken: innerhalb der Anwesenheit (oder des Arbeitstags), ohne Buchung und ohne Pause, ab 10 Minuten
    const base = pres.length ? pres : work ? [work] : [];
    const busy = [...entries, ...pauses].map(b => [b.s, b.e]).sort((a, b) => a[0] - b[0]);
    const gaps = [];
    for (const sp of base) {
      let cur = sp.s;
      for (const [bs, be] of busy) {
        if (be <= cur || bs >= sp.e) continue;
        if (bs - cur >= 10) gaps.push({ s: cur, e: Math.min(bs, sp.e) });
        cur = Math.max(cur, be);
      }
      if (sp.e - cur >= 10) gaps.push({ s: cur, e: sp.e });
    }
    const presMin = pres.reduce((a, p) => a + (p.e - p.s), 0);
    const bookedMin = Math.round(entries.reduce((a, e) => a + (e.duration_seconds || (e.e - e.s) * 60), 0) / 60);
    // Überschneidungen (vermutlich doppelt gebucht), ab 5 Minuten
    const overlaps = [];
    for (let i = 0; i < entries.length; i++) for (let j = i + 1; j < entries.length; j++) {
      const a = entries[i], b = entries[j], ov = Math.min(a.e, b.e) - Math.max(a.s, b.s);
      if (ov >= 5) overlaps.push({ a, b, minutes: ov });
    }
    return { date, tracking: on(), presence: pres, overlaps, presence_minutes: presMin, work, pauses, entries, events, ssh, gaps,
      gap_minutes: gaps.reduce((a, g) => a + g.e - g.s, 0), booked_minutes: bookedMin, now: isToday ? nowMin : null };
  }
  return { overlaps, dayOverview };
};
