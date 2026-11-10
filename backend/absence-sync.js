/* ═══════════════════════════════════════════════════════════════════
   Abwesenheiten aus Tempo und Kalender erkennen (v6.22.0)
   ───────────────────────────────────────────────────────────────────
   • Tempo (v6.25): Zeit auf dem Abwesenheitsprojekt (Standard „HAP“) zählt NICHT
     als Arbeitszeit; erkannter Urlaub wird nur vorgeschlagen. Fürs Konto zählt nur,
     was im Programm steht (von Hand oder aus Kalender/Tempo übernommen) – ganze
     Tage mit dem eigenen Tages-Soll.
   • Kalender: ganztägige Termine wie „Urlaub“, „Krank“, „Abwesend“,
     „OOO“ werden nur VORGESCHLAGEN (Assistent) – es kann ja auch der
     Urlaub von Kollegen in einem geteilten Kalender sein.
   • Umgekehrt: Urlaub, der hier eingetragen ist, aber in Tempo fehlt,
     meldet der Assistent („in Tempo eintragen“).
═══════════════════════════════════════════════════════════════════ */
const crypto = require("crypto");
module.exports = function registerAbsenceSync(app, ctx) {
  const { one, all, run, getSettings, writeLog, localDateOf } = ctx;
  const LOG = "abwesenheit";
  const pad = n => String(n).padStart(2, "0");
  const iso = d => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  let ready = false;
  function ensure() {
    if (ready) return;
    for (const c of ["source TEXT", "ext_key TEXT"]) { try { run(`ALTER TABLE absences ADD COLUMN ${c}`); } catch (_) {} }
    run(`CREATE TABLE IF NOT EXISTS absence_hints (key TEXT PRIMARY KEY, title TEXT, date_from TEXT, date_to TEXT, type TEXT, calendar TEXT, status TEXT DEFAULT 'new', created_at TEXT DEFAULT (datetime('now')))`);
    ready = true;
  }
  const typeOf = t => /vacation|urlaub|holiday|ferien/i.test(t) ? "urlaub" : /sick|krank|illness/i.test(t) ? "krankheit" : /training|fortbildung|schulung|seminar/i.test(t) ? "fortbildung" : "sonstiges";
  const covered = (from, to) => one("SELECT id FROM absences WHERE type<>'feiertag' AND date_from<=? AND date_to>=?", from, to);
  /* „20.07.-07.08.26“, „30.03.-02.04.26“, „15.05.-15.05.26“ → [von, bis] */
  function rangeFromTitle(title, fallbackYear) {
    const m = /(\d{1,2})\.(\d{1,2})\.?(\d{2,4})?\s*[-–]\s*(\d{1,2})\.(\d{1,2})\.(\d{2,4})?/.exec(String(title || ""));
    if (!m) return null;
    const y2 = m[6] ? (m[6].length === 2 ? 2000 + +m[6] : +m[6]) : fallbackYear;
    const y1 = m[3] ? (m[3].length === 2 ? 2000 + +m[3] : +m[3]) : (+m[2] > +m[5] ? y2 - 1 : y2);
    const a = `${y1}-${pad(+m[2])}-${pad(+m[1])}`, b = `${y2}-${pad(+m[5])}-${pad(+m[4])}`;
    return a <= b ? [a, b] : null;
  }

  /* ── Tempo-Abwesenheitsprojekt (Entscheidung 29.10.2026) ─────────
     Urlaub in Tempo ist ein Ticket – fürs Arbeitszeitkonto zählt NUR, was im Programm
     eingetragen ist (von Hand oder aus dem Kalender übernommen). Buchungen auf dem
     Abwesenheitsprojekt werden markiert (is_absence) und zählen nicht als Arbeitszeit.
     Erkannter Tempo-Urlaub wird nur VORGESCHLAGEN („übernehmen?“). */
  function fromTempo() {
    ensure();
    const key = (getSettings().tempo_absence_project || "HAP").toUpperCase();
    const marked = run(`UPDATE time_entries SET is_absence=1 WHERE COALESCE(is_absence,0)=0 AND task_id IN (SELECT id FROM tasks WHERE UPPER(jira_ticket) LIKE ?)`, key + "-%").changes || 0;
    // früher (v6.22) automatisch angelegte Tempo-Abwesenheiten wieder entfernen → werden zu Vorschlägen
    const removed = run("DELETE FROM absences WHERE source='tempo' AND note LIKE '%(aus Tempo)'").changes || 0;
    let hints = 0;
    for (const t of all(`SELECT t.jira_ticket AS k, t.title, MIN(e.date) AS a, MAX(e.date) AS b FROM time_entries e JOIN tasks t ON t.id=e.task_id
      WHERE UPPER(t.jira_ticket) LIKE ? AND e.date>=? GROUP BY UPPER(t.jira_ticket)`, key + "-%", getSettings().tempo_sync_from || "2000-01-01")) {
      const k = String(t.k).toUpperCase(), r = rangeFromTitle(t.title, +String(t.b).slice(0, 4)) || [t.a, t.b];
      if (covered(r[0], r[1]) || one("SELECT key FROM absence_hints WHERE key=?", "tempo:" + k)) continue;
      run("INSERT INTO absence_hints (key,title,date_from,date_to,type,calendar) VALUES (?,?,?,?,?,'Tempo')", "tempo:" + k, `${t.title || k}`.slice(0, 200), r[0], r[1], typeOf(t.title));
      hints++;
    }
    if (marked || removed || hints) writeLog("info", LOG, `Tempo-Abwesenheiten: ${marked} Buchungen zählen nicht als Arbeitszeit, ${removed} automatische Einträge entfernt, ${hints} als Vorschlag`);
    return { marked, removed, hints };
  }

  /* ── Kalender: ganztägige Abwesenheits-Termine → Vorschlag ───── */
  const WORDS = /\b(urlaub|vacation|holiday|ferien|abwesend|abwesenheit|ooo|out of office|krank|sick|elternzeit|fortbildung|schulung)\b/i;
  function fromCalendar(events, exclude) {
    ensure();
    const me = String(getSettings().jira_username || "").toLowerCase();
    let n = 0;
    for (const e of events || []) {
      if (!e.allDay || !WORDS.test(String(e.title || ""))) continue;
      if (exclude.includes(String(e.calendar || "").toLowerCase()) || /^zeitprotokoll/i.test(String(e.calendar || ""))) continue;
      const s = new Date(e.start), en = new Date(Date.parse(e.end) - 1000);     // ganztägig: Ende ist exklusiv
      const from = localDateOf(s.getTime()), to = localDateOf(en.getTime());
      if (to < from || covered(from, to)) continue;
      const key = crypto.createHash("sha1").update(`${e.title}|${from}|${to}|${e.calendar || ""}`).digest("hex").slice(0, 16);
      if (one("SELECT key FROM absence_hints WHERE key=?", key)) continue;
      run("INSERT INTO absence_hints (key,title,date_from,date_to,type,calendar) VALUES (?,?,?,?,?,?)", key, String(e.title).slice(0, 200), from, to, typeOf(e.title), e.calendar || null);
      n++;
    }
    return n;
  }

  /* ── Hier eingetragen, in Tempo fehlt es ─────────────────────── */
  function missingInTempo() {
    ensure();
    const s = getSettings(); if (!s.jira_base_url || !s.tempo_sync_from) return [];
    const key = (s.tempo_absence_project || "HAP").toUpperCase();
    const out = [];
    for (const a of all("SELECT * FROM absences WHERE type IN ('urlaub','krankheit') AND COALESCE(source,'')<>'tempo' AND date_to>=? ORDER BY date_from", s.tempo_sync_from)) {
      let missing = 0, days = 0;
      for (let d = new Date(a.date_from + "T12:00"); iso(d) <= a.date_to; d.setDate(d.getDate() + 1)) {
        const w = d.getDay(); if (w === 0 || w === 6 || iso(d) > localDateOf(Date.now() + 60 * 864e5)) continue;
        days++;
        if (!one("SELECT 1 FROM time_entries e JOIN tasks t ON t.id=e.task_id WHERE e.date=? AND UPPER(t.jira_ticket) LIKE ?", iso(d), key + "-%")) missing++;
      }
      if (missing && missing === days) out.push({ id: a.id, from: a.date_from, to: a.date_to, type: a.type, days });
    }
    return out;
  }

  app.get("/api/absence-hints", (_, res) => { ensure(); res.json({ calendar: all("SELECT * FROM absence_hints WHERE status='new' ORDER BY date_from"), missing_in_tempo: missingInTempo() }); });
  app.post("/api/absence-hints/:key", (req, res) => {
    ensure(); const h = one("SELECT * FROM absence_hints WHERE key=?", req.params.key); if (!h) return res.status(404).json({ error: "nicht gefunden" });
    if (req.body?.take) {
      if (!covered(h.date_from, h.date_to)) run("INSERT INTO absences (date_from,date_to,type,hours,note,source,ext_key) VALUES (?,?,?,NULL,?,'kalender',?)", h.date_from, h.date_to, req.body.type || h.type, `${h.title} (aus Kalender)`.slice(0, 200), h.key);
      writeLog("info", LOG, `Abwesenheit aus Kalender übernommen: ${h.title} ${h.date_from}–${h.date_to}`);
    }
    run("UPDATE absence_hints SET status=? WHERE key=?", req.body?.take ? "taken" : "dismissed", h.key);
    res.json({ ok: true });
  });
  app.post("/api/absences/from-tempo", (_, res) => res.json(fromTempo()));

  setTimeout(() => { try { fromTempo(); } catch (e) { writeLog("warn", LOG, e.message); } }, 15000);
  return { fromTempo, fromCalendar, missingInTempo, hints: () => { ensure(); return all("SELECT * FROM absence_hints WHERE status='new'"); } };
};
