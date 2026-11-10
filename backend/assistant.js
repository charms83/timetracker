/* ═══════════════════════════════════════════════════════════════════
   Einrichtungs-Assistent (v6.8.0)
   ───────────────────────────────────────────────────────────────────
   Führt durch die Ersteinrichtung und bleibt danach als „Was ist offen?“
   erreichbar. Abschaltbar (assistant_enabled=0) – dann weder Menüpunkt
   noch Hinweise; Einschalten wieder unter Einstellungen › System.

   Betriebsarten (assistant_mode) – steuern die Standard-Regel, wohin Zeit geht:
     jira   – nur Jira + Tempo (Tempo-Ersatz/-Begleiter, kein Plane)
     bridge – Übergang: Projekte, die schon in Plane sind → Plane UND Tempo,
              alle anderen nur Jira/Tempo (Regel „nur Plane-Projekte nach Plane“)
     plane  – Ziel: nur noch Plane, Jira/Tempo aus
   Der Server liefert nur Fakten aus der Datenbank (schnell, ohne Netz).
   Live-Prüfungen (Verbindung, Zertifikat) macht die Oberfläche über die
   vorhandenen Status-Endpunkte.
═══════════════════════════════════════════════════════════════════ */
module.exports = function registerAssistant(app, ctx) {
  const { one, all, run, getSettings, writeLog, hooks } = ctx;
  const LOG = "assistent";
  const set = (k, v) => run("INSERT OR REPLACE INTO settings (key,value) VALUES (?,?)", k, String(v));
  const n = (sql, ...a) => one(sql, ...a)?.n || 0;
  const RULE_OF_MODE = { jira: "tempo", bridge: "bridge", plane: "plane" };

  function facts() {
    const s = getSettings();
    let done = {}; try { done = JSON.parse(s.assistant_done || "{}"); } catch (_) {}
    let pins = {}; try { pins = JSON.parse(s.tls_pins || "{}"); } catch (_) {}
    const plane = !!(s.plane_base_url && s.plane_api_key && s.plane_workspace);
    const jiraOn = s.jira_enabled !== "0", jira = !!(s.jira_base_url && s.jira_token);
    const mode = s.assistant_mode || (plane && jiraOn && jira ? "bridge" : plane && !jiraOn ? "plane" : jira ? "jira" : null);
    const active = all("SELECT id, name FROM projects WHERE COALESCE(status,'')<>'archived' AND COALESCE(muted,0)=0 AND merged_into IS NULL");
    const src = hooks.sources;
    const inPlane = active.filter(p => src?.inPlane(p.id));
    const withJira = new Set(all("SELECT project_id FROM project_sources WHERE system='jira' AND status='linked' AND project_id IS NOT NULL").map(r => r.project_id));
    const onlyJira = active.filter(p => withJira.has(p.id) && !src?.inPlane(p.id));
    const noSource = active.filter(p => !withJira.has(p.id) && !src?.inPlane(p.id));
    const goneOpen = all(`SELECT p.id, p.name FROM projects p WHERE COALESCE(p.status,'')<>'archived' AND COALESCE(p.gone_ack,0)=0
      AND EXISTS (SELECT 1 FROM project_sources s WHERE s.project_id=p.id AND s.status='linked')
      AND NOT EXISTS (SELECT 1 FROM project_sources s WHERE s.project_id=p.id AND s.status='linked' AND s.gone=0)`);
    const targets = ["mon", "tue", "wed", "thu", "fri"].some(d => parseFloat(s["target_" + d]) > 0);
    return {
      enabled: s.assistant_enabled !== "0", done, mode, mode_set: !!s.assistant_mode, started: !!s.assistant_started,
      plane: { configured: plane },
      jira: { enabled: jiraOn, configured: jira, user: s.jira_username || null, pins: Object.keys(pins).length },
      sources: { last_scan: s.sources_last_scan || null, pending: n("SELECT COUNT(*) AS n FROM project_sources WHERE status='new' AND gone=0"),
        ignored: n("SELECT COUNT(*) AS n FROM project_sources WHERE status='ignored'"),
        linked_plane: n("SELECT COUNT(*) AS n FROM project_sources WHERE system='plane' AND status='linked'"),
        linked_jira: n("SELECT COUNT(*) AS n FROM project_sources WHERE system='jira' AND status='linked'") },
      projects: { active: active.length, in_plane: inPlane.length, only_jira: onlyJira.map(p => p.name), no_source: noSource.map(p => p.name),
        muted: n("SELECT COUNT(*) AS n FROM projects WHERE COALESCE(muted,0)=1 AND merged_into IS NULL"), gone_open: goneOpen.map(p => p.name) },
      rule_default: s.output_rule_default || "bridge",
      tempo: { push: s.tempo_push_enabled === "1" || s.tempo_push_enabled === "true", mode: s.tempo_push_mode === "auto" ? "auto" : "manual",
        account: s.tempo_account || "", since: s.tempo_push_since || "", new_project: s.jira_new_project || "", issue: s.tempo_booking_issue || "",
        my_tickets: s.jira_my_tickets !== "0" },
      meetings: { detect: s.meeting_detect !== "0", calendar: s.meeting_calendar !== "0", browser: s.meeting_browser !== "0", ask: s.meeting_ask !== "0" },
      ssh: { enabled: s.ssh_watch_enabled !== "0" },
      worktime: { targets, start: s.tracking_start_date || "", vacation: s.vacation_days_per_year || "" },
      entries: n("SELECT COUNT(*) AS n FROM time_entries"),
    };
  }

  /* Offene Punkte – das, was der Assistent auch nach der Einrichtung anmahnt */
  function openPoints(f) {
    const o = [], m = f.mode;
    if (!m) o.push({ step: "mode", level: "todo", text: "Arbeitsweise wählen: nur Jira, Übergang Plane + Jira oder nur Plane" });
    if ((m === "bridge" || m === "plane") && !f.plane.configured) o.push({ step: "plane", level: "todo", text: "Plane-Zugang fehlt" });
    if ((m === "bridge" || m === "jira") && !f.jira.configured) o.push({ step: "jira", level: "todo", text: "Jira-/Tempo-Zugang fehlt" });
    if (m === "plane" && f.jira.enabled) o.push({ step: "mode", level: "info", text: "Betriebsart „nur Plane“, aber Jira ist noch eingeschaltet" });
    if (f.sources.pending) o.push({ step: "projects", level: "todo", text: f.sources.pending === 1 ? "1 neues Projekt aus Plane/Jira wartet auf Entscheidung" : `${f.sources.pending} neue Projekte aus Plane/Jira warten auf Entscheidung` });
    if (f.projects.gone_open.length) o.push({ step: "projects", level: "warn", text: `Quelle abgeschlossen: ${f.projects.gone_open.slice(0, 3).join(", ")}${f.projects.gone_open.length > 3 ? " …" : ""}` });
    if (!f.sources.last_scan && (f.plane.configured || f.jira.configured)) o.push({ step: "projects", level: "todo", text: "Projekte noch nie eingelesen" });
    if ((m === "bridge" || m === "jira") && f.jira.configured && !f.tempo.push) o.push({ step: "tempo", level: "todo", text: "Tempo-Übertragung ist aus – Zeiten kommen nicht in Tempo an" });
    if ((m === "bridge" || m === "jira") && f.tempo.push && !f.tempo.new_project) o.push({ step: "tempo", level: "todo", text: "Jira-Projekt für Übergangs- und Sammeltickets fehlt – „Automatisch einrichten“ hilft" });
    if (m === "bridge" && f.projects.only_jira.length) o.push({ step: "migration", level: "info", text: `${f.projects.only_jira.length} ${f.projects.only_jira.length === 1 ? "Projekt ist" : "Projekte sind"} noch nicht in Plane (Zeit geht nur nach Jira/Tempo)` });
    if (m === "bridge" && f.projects.active && !f.projects.only_jira.length && f.projects.in_plane === f.projects.active) o.push({ step: "migration", level: "info", text: "Alle aktiven Projekte sind in Plane – Umstieg auf „nur Plane“ möglich" });
    if (!f.worktime.targets) o.push({ step: "worktime", level: "todo", text: "Sollstunden fehlen (Saldo stimmt sonst nicht)" });
    return o;
  }

  /* ══ Pflege-Aufgaben (v6.14.0) ════════════════════════════════════
     Nur lokale Datenbank-Abfragen, kein Netz – und zwischengespeichert:
     neu gerechnet höchstens alle 2 Minuten oder wenn sich etwas geändert hat
     (Buchungen/Projekte). Jede Prüfung meldet nur, wenn es etwas zu tun gibt. */
  let cache = { at: 0, sig: "", data: null };
  const sig = () => { const a = one("SELECT COUNT(*) AS n, COALESCE(MAX(id),0) AS m FROM time_entries"); const b = one("SELECT COUNT(*) AS n FROM project_sources WHERE status='linked'"); const c = one("SELECT COUNT(*) AS n FROM projects WHERE COALESCE(status,'')<>'archived'"); return `${a.n}/${a.m}/${b.n}/${c.n}`; };
  function tasks(force) {
    const sg = sig();
    if (!force && cache.data && cache.sig === sg && Date.now() - cache.at < 120e3) return cache.data;
    const s = getSettings(), out = [];
    const since = /^\d{4}-\d{2}-\d{2}$/.test(s.tempo_push_since || "") ? s.tempo_push_since : new Date().toISOString().slice(0, 8) + "01";
    const add = (id, level, title, text, count) => out.push({ id, level, title, text, count });
    // Abwesenheiten: im Kalender erkannt / in Tempo fehlt
    try {
      const h = hooks.absences?.hints() || [];
      if (h.length) add("absence_hints", "todo", "Urlaub/Abwesenheit erkannt – übernehmen?", h.slice(0, 3).map(x => `${x.title} ${x.date_from.slice(8)}.${x.date_from.slice(5, 7)}.`).join(", "), h.length);
      const m = hooks.absences?.missingInTempo() || [];
      if (m.length) add("absence_tempo", "info", "Urlaub fehlt in Tempo", m.slice(0, 3).map(x => `${x.from.slice(8)}.${x.from.slice(5, 7)}.–${x.to.slice(8)}.${x.to.slice(5, 7)}.`).join(", ") + " – dort auf dem Abwesenheitsprojekt eintragen", m.length);
    } catch (_) {}
    // Planung: Überfälliges, morgen/Woche planen
    try { for (const h of hooks.todo?.planningHints() || []) add(h.id, h.level, h.title, h.text, h.count); } catch (_) {}
    // Tempo-Zeiten noch nie abgeglichen → zuerst (sonst stimmen Lücken und Arbeitszeitkonto nicht)
    if (s.jira_base_url && s.jira_token && !s.tempo_sync_from) add("tempo_sync", "todo", "Zeiten aus Tempo übernehmen", "einmal alle eigenen Tempo-Zeiten holen – nur lesen; danach stimmen Arbeitszeitkonto und offene Tage", 1);
    // Monatsende (5 Arbeitstage vorher, ohne Wochenende) und Nachtragen – steht immer oben
    try {
      const me = hooks.monthend?.status(); const fh = h => (Math.round(h * 4) / 4).toLocaleString("de-DE") + " h";
      const dd = s => new Date(s + "T12:00:00").toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit" });
      if (me?.prev) add("monthend", "warn", `${me.prev.month_name} noch nicht vollständig`, `an ${me.prev.days.length} Tag${me.prev.days.length === 1 ? "" : "en"} fehlen ${fh(me.prev.missing_h)} – jetzt nachtragen, dann geht der Monat nach Tempo`, me.prev.days.length);
      else if (me?.remind) add("monthend", me.open_days.length && !me.optional ? "warn" : "info", `Monatsende ${me.month_name}: noch ${me.workdays_left} Arbeitstag${me.workdays_left === 1 ? "" : "e"} (bis ${dd(me.last_workday)})`,
        me.open_days.length ? `Zeiten eintragen – an ${me.open_days.length} Tag${me.open_days.length === 1 ? "" : "en"} fehlen ${fh(me.missing_h)}` + (me.optional ? ` · Konto +${fh(me.balance_h)}, nur ein Hinweis` : "") : "bisher alles eingetragen ✓ – bis zum letzten Arbeitstag weiter buchen", me.open_days.length || 1);
      else if (me?.open_days.length) add("monthend", "info", "Zeiten nachtragen", `an ${me.open_days.length} Tag${me.open_days.length === 1 ? "" : "en"} fehlen ${fh(me.missing_h)}` + (me.optional ? ` – Konto steht bei +${fh(me.balance_h)}, nur ein Hinweis` : ""), me.open_days.length);
    } catch (_) {}
    try { const d = hooks.sources?.duplicates() || []; if (d.length) add("duplicates", "todo", "Doppelte Projekte", d.slice(0, 3).map(x => `${x.from.name} ↔ ${x.into.name}`).join(", "), d.length); } catch (_) {}
    const pend = n("SELECT COUNT(*) AS n FROM project_sources WHERE status='new' AND gone=0");
    if (pend) add("pending", "todo", "Neue Projekte aus Plane/Jira", "übernehmen oder stumm schalten", pend);
    try { const lp = (hooks.cleanup?.localProjects() || []).filter(p => !p.entries || (p.last || "") >= since); if (lp.length) add("cleanup", "todo", "Projekte nur hier (ohne Plane/Jira)", lp.slice(0, 4).map(p => p.name).join(", "), lp.length); } catch (_) {}
    // Prüfen: nur Markierungen außer „Überschneidung“ (die hat ihre eigene Aufgabe)
    try { const rv = n("SELECT COUNT(*) AS n FROM time_entries WHERE check_flags IS NOT NULL AND check_flags<>'overlap' AND check_note IS NULL AND date>=?", since); if (rv) add("review", "warn", "Buchungen bitte prüfen", "fallen aus dem Rahmen (z. B. über 10 h, Urlaubstag)", rv); } catch (_) {}
    // Überschneidungen: dieselbe Regel wie die Karte (ab 3 min, „Ist richtig so“ zählt nicht)
    try { const days = Math.max(1, Math.min(90, Math.ceil((Date.now() - Date.parse(since + "T00:00:00")) / 864e5) + 1)); const ov = (hooks.presence?.overlaps(days) || []).length;
      if (ov) add("overlaps", "warn", "Überschneidende Buchungen", "vermutlich doppelt gebucht", ov); } catch (_) {}
    try { const nt = hooks.tempoPushRef?.noTicket() || []; if (nt.length) { const h = Math.round(nt.reduce((a, e) => a + (e.duration_seconds || 0), 0) / 360) / 10;
      add("tempo_noticket", "warn", "Buchungen ohne Jira-Ticket", `${nt.length} Buchung${nt.length === 1 ? "" : "en"} (${h.toLocaleString("de-DE")} h) – in Jira gibt es keine Zeit ohne Ticket; nur Plane geht aufs Übergangs-Ticket. Bitte ein Ticket zuordnen`, nt.length); } } catch (_) {}
    try { const te = n("SELECT COUNT(*) AS n FROM time_entries WHERE tempo_push_error IS NOT NULL AND tempo_push_id IS NULL AND date>=?", since); if (te) add("tempo_errors", "warn", "Fehler bei der Übertragung nach Tempo", "unter Übertragen ansehen", te); } catch (_) {}
    try { const sel = JSON.parse(s.selfcheck_last || "null"); if (sel && sel.ok === false) add("selfcheck", "warn", "Selbstprüfung meldet Probleme", sel.message || "", 1); } catch (_) {}
    const f = facts();
    for (const o of openPoints(f)) if (!["projects"].includes(o.step)) add("setup:" + o.step, o.level === "info" ? "info" : o.level, o.text, "", 1);
    cache = { at: Date.now(), sig: sg, data: out };
    return out;
  }
  app.get("/api/assistant/tasks", (req, res) => res.json(tasks(req.query.fresh === "1")));

  app.get("/api/assistant", (_, res) => { const f = facts(); res.json({ ...f, open: openPoints(f) }); });

  /* {enabled?, done?:{step:true|false}, mode?, started?} – mode setzt die Standard-Regel gleich mit */
  app.put("/api/assistant", (req, res) => {
    const b = req.body || {};
    if (b.enabled !== undefined) { set("assistant_enabled", b.enabled ? "1" : "0"); writeLog("info", LOG, b.enabled ? "Assistent eingeschaltet" : "Assistent ausgeschaltet"); }
    if (b.started) set("assistant_started", new Date().toISOString());
    if (b.done && typeof b.done === "object") {
      let d = {}; try { d = JSON.parse(getSettings().assistant_done || "{}"); } catch (_) {}
      for (const [k, v] of Object.entries(b.done)) { if (v) d[k] = new Date().toISOString(); else delete d[k]; }
      set("assistant_done", JSON.stringify(d));
    }
    if (b.mode !== undefined) {
      if (!RULE_OF_MODE[b.mode]) return res.status(400).json({ error: "Unbekannte Betriebsart" });
      set("assistant_mode", b.mode);
      set("output_rule_default", RULE_OF_MODE[b.mode]);
      if (b.mode === "plane") { set("jira_enabled", "0"); set("tempo_push_enabled", "0"); }
      else set("jira_enabled", "1");
      writeLog("info", LOG, `Betriebsart: ${b.mode} (Standard-Regel ${RULE_OF_MODE[b.mode]})`);
    }
    const f = facts(); res.json({ ...f, open: openPoints(f) });
  });
  app.post("/api/assistant/reset", (_, res) => { set("assistant_done", "{}"); run("DELETE FROM settings WHERE key IN ('assistant_started')"); res.json({ ok: true }); });

  return { facts, openPoints };
};
