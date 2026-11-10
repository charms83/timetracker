/* ═══════════════════════════════════════════════════════════════════
   Plausibilitätsprüfung (v6.13.0) – Schutz vor Fehl- und Doppelbuchungen
   ───────────────────────────────────────────────────────────────────
   Drei Stufen, damit nichts „kaputt“ geht:
     block   – wird nicht gespeichert (eindeutig falsch: Zukunft, > 16 h)
     confirm – speichern nur mit Bestätigung + Begründung (landet im Protokoll):
               Einzelbuchung > 10 h, Tag > 10 h (ArbZG §3), Überschneidung,
               Buchung an Urlaubs-/Krank-/Feiertag, gesperrter Tempo-Monat
     hint    – nur Hinweis (Ruhezeit < 11 h, Pause nach 6/9 h fehlt)
   Der Timer wird nie blockiert: läuft er zu lang, wird die Buchung markiert
   („Bitte prüfen“) und geht erst nach Bestätigung nach Tempo.
═══════════════════════════════════════════════════════════════════ */
module.exports = function registerChecks(app, ctx) {
  const { one, all, run, writeLog, localDateOf } = ctx;
  let ready = false;
  const ensure = () => { if (ready) return; for (const c of ["check_flags", "check_note"]) { try { run(`ALTER TABLE time_entries ADD COLUMN ${c} TEXT`); } catch (_) {} } ready = true; };
  const H = 3600;
  const hm = iso => { const d = new Date(iso); return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`; };
  const fmtH = s => (Math.round(s / 360) / 10).toLocaleString("de-DE") + " h";

  /* e = {id?, date, start_time, end_time} */
  function check(e) {
    ensure();
    const out = [];
    if (!e.start_time) return out;
    const s = Date.parse(e.start_time), en = e.end_time ? Date.parse(e.end_time) : null;
    const date = e.date || localDateOf(s);
    if (s > Date.now() + 5 * 60e3) out.push({ level: "block", code: "future", text: "Beginn liegt in der Zukunft" });
    if (en != null) {
      const dur = (en - s) / 1000;
      if (dur <= 0) out.push({ level: "block", code: "order", text: "Ende liegt vor dem Beginn" });
      if (dur > 16 * H) out.push({ level: "block", code: "long16", text: `Einzelbuchung über 16 h (${fmtH(dur)})` });
      else if (dur > 10 * H) out.push({ level: "confirm", code: "long10", text: `Einzelbuchung über 10 h (${fmtH(dur)}) – Timer vergessen?` });
      const others = all("SELECT id, start_time, end_time, duration_seconds, comment, source FROM time_entries WHERE date=? AND end_time IS NOT NULL AND id<>?", date, e.id || 0);
      const day = others.reduce((a, o) => a + (o.duration_seconds || 0), 0) + Math.max(0, dur);
      if (day > 16 * H) out.push({ level: "block", code: "day16", text: `Tag hätte ${fmtH(day)} – mehr als 16 h ist nicht möglich` });
      else if (day > 10 * H) out.push({ level: "confirm", code: "day10", text: `Tag hätte ${fmtH(day)} – über 10 h (Arbeitszeitgesetz)` });
      let ign = []; try { ign = JSON.parse(one("SELECT value FROM settings WHERE key='overlap_ignore'")?.value || "[]"); } catch (_) {}
      for (const o of others) {
        if (o.source === "tempo" || o.source === "tempo_csv") continue;   // Tempo-Spiegel: Uhrzeiten oft nur geschätzt
        if (e.id && ign.includes(`${Math.min(e.id, o.id)}-${Math.max(e.id, o.id)}`)) continue;   // „Ist richtig so“
        const ov = (Math.min(en, Date.parse(o.end_time)) - Math.max(s, Date.parse(o.start_time))) / 60e3;
        if (ov >= 3) { out.push({ level: "confirm", code: "overlap", ref: o.id, other: { id: o.id, start_time: o.start_time, end_time: o.end_time, comment: o.comment || "" }, text: `Überschneidet sich ${Math.round(ov)} min mit ${hm(o.start_time)}–${hm(o.end_time)}${o.comment ? " „" + String(o.comment).slice(0, 40) + "“" : ""}` }); break; }
      }
      // Pause (ArbZG §4): > 6 h → 30 min, > 9 h → 45 min
      if (day > 6 * H) {
        const pauses = all("SELECT start_time, end_time FROM pauses WHERE date=? AND end_time IS NOT NULL", date).reduce((a, p) => a + (Date.parse(p.end_time) - Date.parse(p.start_time)) / 1000, 0);
        const need = day > 9 * H ? 45 * 60 : 30 * 60;
        if (pauses < need) out.push({ level: "hint", code: "pause", text: `Bei ${fmtH(day)} Arbeit sind ${need / 60} min Pause vorgeschrieben – erfasst: ${Math.round(pauses / 60)} min` });
      }
    }
    const abs = one("SELECT type FROM absences WHERE date_from<=? AND date_to>=? AND type IN ('urlaub','krankheit','feiertag') LIMIT 1", date, date);
    if (abs) out.push({ level: "confirm", code: "absence", text: `An diesem Tag ist ${abs.type === "urlaub" ? "Urlaub" : abs.type === "krankheit" ? "Krankheit" : "ein Feiertag"} eingetragen` });
    try {
      if (one("SELECT 1 FROM tempo_months WHERE month=? AND locked=1", date.slice(0, 7))) out.push({ level: "confirm", code: "locked", text: `${date.slice(5, 7)}/${date.slice(0, 4)} ist in Tempo gesperrt – die Änderung kommt dort nicht mehr an` });
    } catch (_) {}
    // Ruhezeit (ArbZG §5): 11 h zwischen Arbeitsende und -beginn
    const prev = one("SELECT MAX(end_time) AS e FROM time_entries WHERE date<? AND end_time IS NOT NULL AND date>=?", date, localDateOf(s - 2 * 864e5));
    if (prev?.e && s - Date.parse(prev.e) < 11 * H * 1000 && s - Date.parse(prev.e) > 0) out.push({ level: "hint", code: "rest", text: `Nur ${fmtH((s - Date.parse(prev.e)) / 1000)} Ruhezeit seit gestern ${hm(prev.e)} (11 h vorgeschrieben)` });
    return out;
  }

  /* Wächter für Speichern: true = Antwort schon gesendet (blockiert oder Bestätigung nötig) */
  function guard(req, res, e) {
    const issues = check(e);
    const blocks = issues.filter(i => i.level === "block");
    if (blocks.length) { res.status(400).json({ error: blocks.map(b => b.text).join(" · "), issues }); return true; }
    const conf = issues.filter(i => i.level === "confirm");
    const ok = req.body?.confirm_checks === true && String(req.body?.confirm_reason || "").trim().length >= 3;
    if (conf.length && !ok) { res.status(409).json({ needs_confirm: true, error: conf.map(c => c.text).join(" · "), issues }); return true; }
    req._checks = { issues, note: conf.length ? String(req.body.confirm_reason).trim().slice(0, 300) : null };
    return false;
  }
  /* nach dem Speichern: Markierung + Protokoll */
  function stamp(id, req) {
    ensure();
    const c = req?._checks; if (!c) return;
    const flags = c.issues.filter(i => i.level !== "hint").map(i => i.code);
    run("UPDATE time_entries SET check_flags=?, check_note=? WHERE id=?", flags.length ? flags.join(",") : null, c.note, id);
    if (c.note) writeLog("warn", "pruefung", `Buchung #${id} trotz Hinweis gespeichert (${flags.join(", ")}): ${c.note}`);
  }
  /* Timer/automatisch: nie blockieren, nur markieren (geht dann nicht ungeprüft nach Tempo) */
  function flagAfter(id) {
    ensure();
    const e = one("SELECT id, date, start_time, end_time FROM time_entries WHERE id=?", id); if (!e) return;
    const flags = check(e).filter(i => i.level !== "hint").map(i => i.code);
    run("UPDATE time_entries SET check_flags=? WHERE id=? AND (check_note IS NULL OR ? IS NULL)", flags.length ? flags.join(",") : null, id, flags.length ? 1 : null);
  }
  const needsReview = e => !!(e.check_flags && !e.check_note);

  app.post("/api/checks/entry", (req, res) => res.json(check(req.body || {})));
  /* Offene Prüfungen (für Heute-Ansicht und Übertragen) */
  app.get("/api/checks/open", (req, res) => {
    ensure();
    const since = localDateOf(Date.now() - 31 * 864e5);
    res.json(all(`SELECT e.id, e.date, e.start_time, e.end_time, e.duration_seconds, e.comment, e.check_flags, t.jira_ticket, p.name AS project_name
      FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id LEFT JOIN projects p ON p.id=e.project_id
      WHERE e.check_flags IS NOT NULL AND e.check_note IS NULL AND e.date>=? ORDER BY e.date DESC, e.start_time`, since)
      .map(e => ({ ...e, issues: check(e).filter(i => i.level !== "hint") })));
  });
  app.post("/api/checks/ack", (req, res) => {
    ensure();
    const id = Number(req.body?.id), note = String(req.body?.reason || "").trim();
    if (note.length < 3) return res.status(400).json({ error: "Bitte kurz begründen" });
    run("UPDATE time_entries SET check_note=? WHERE id=?", note.slice(0, 300), id);
    writeLog("warn", "pruefung", `Buchung #${id} als geprüft bestätigt: ${note}`);
    res.json({ ok: true });
  });
  // beim Start einmal: Markierungen der letzten 31 Tage nachziehen
  setTimeout(() => { try { ensure(); for (const e of all("SELECT id FROM time_entries WHERE date>=? AND end_time IS NOT NULL AND check_note IS NULL AND COALESCE(source,'') NOT IN ('tempo','tempo_csv')", localDateOf(Date.now() - 31 * 864e5))) flagAfter(e.id); } catch (_) {} }, 20000);

  return { check, guard, stamp, flagAfter, needsReview };
};
