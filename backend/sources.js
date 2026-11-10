/* ═══════════════════════════════════════════════════════════════════
   Projekte und ihre Quellen (v6.5.0)
   ───────────────────────────────────────────────────────────────────
   Ein Projekt in Zeitprotokoll ist das echte Projekt (z. B. „Beispiel Marketing“).
   Plane- und Jira-Projekte sind QUELLEN für Tickets, die genau einem Projekt
   zugeordnet sind. Zeitprotokoll legt in Plane/Jira keine Projekte an:
     • einmal im Monat (und per Knopf) werden die Projekte aus Plane und Jira gelesen
     • neue landen auf „Neue Projekte gefunden“ – Projektleitung übernimmt sie
       (Vorschlag: gleiches Kürzel in der anderen Quelle / ähnlicher Name)
     • verschwindet eine Quelle (archiviert), zeigt das System das an
   Je Projekt: Regel, wohin die Zeit geht (Plane, Tempo, beides, nur hier),
   und ein Nachfolge-Projekt für Buchungen nach dem Abschluss.
═══════════════════════════════════════════════════════════════════ */
const { textSim, toks, nameSim } = require("./textmatch");

module.exports = function registerSources(app, ctx) {
  const { one, all, run, getSettings, writeLog, plane, tempoPush } = ctx;
  const LOG = "projekte";
  let scanning = false, lastScan = null;

  function init() {
    run(`CREATE TABLE IF NOT EXISTS project_sources (
      id INTEGER PRIMARY KEY AUTOINCREMENT, system TEXT NOT NULL, external_id TEXT NOT NULL, key TEXT, name TEXT,
      project_id INTEGER REFERENCES projects(id) ON DELETE SET NULL, status TEXT NOT NULL DEFAULT 'new',
      gone INTEGER DEFAULT 0, first_seen TEXT DEFAULT (datetime('now')), last_seen TEXT DEFAULT (datetime('now')),
      UNIQUE(system, external_id))`);
    for (const m of [
      "ALTER TABLE projects ADD COLUMN successor_id INTEGER",
      "ALTER TABLE projects ADD COLUMN output_rule TEXT DEFAULT 'default'",   // default | bridge | plane | tempo | none
      "ALTER TABLE projects ADD COLUMN closed_at TEXT",
      "ALTER TABLE projects ADD COLUMN muted INTEGER DEFAULT 0",          // stumm: nicht in Auswahllisten, kein Abgleich
      "ALTER TABLE projects ADD COLUMN merged_into INTEGER",
      "ALTER TABLE projects ADD COLUMN gone_ack INTEGER DEFAULT 0",       // „Quelle abgeschlossen“ gesehen, Projekt bleibt offen
    ]) { try { run(m); } catch (_) {} }
    // Bestehende Verknüpfungen übernehmen (Plane-Projekt-ID, Jira-Projekt)
    for (const p of all("SELECT id, name, plane_project_id, plane_identifier, jira_project FROM projects")) {
      if (p.plane_project_id) run(`INSERT INTO project_sources (system,external_id,key,name,project_id,status) VALUES ('plane',?,?,?,?,'linked')
                                   ON CONFLICT(system,external_id) DO NOTHING`, p.plane_project_id, (p.plane_identifier || "").toUpperCase() || null, p.name, p.id);
      if (p.jira_project) run(`INSERT INTO project_sources (system,external_id,key,name,project_id,status) VALUES ('jira',?,?,?,?,'linked')
                               ON CONFLICT(system,external_id) DO NOTHING`, p.jira_project.toUpperCase(), p.jira_project.toUpperCase(), p.name, p.id);
    }
    // Abgleich (v6.8.0): Altfelder gesetzt, Quellzeile aber noch „neu/ignoriert“ → als verknüpft führen
    for (const p of all("SELECT id, plane_project_id, jira_project FROM projects WHERE COALESCE(status,'')<>'archived'")) {
      if (p.plane_project_id) run("UPDATE project_sources SET project_id=?, status='linked' WHERE system='plane' AND external_id=? AND status<>'linked'", p.id, p.plane_project_id);
      if (p.jira_project) run("UPDATE project_sources SET project_id=?, status='linked' WHERE system='jira' AND external_id=? AND status<>'linked'", p.id, p.jira_project.toUpperCase());
    }
    // Einmalig: jira_key, das nur der Plane-Kennung nachgeschrieben wurde, aber mit einem anderen Projekt kollidiert, das dieses Jira-Projekt wirklich hat
    if (!getSettings().migr_680_keys) {
      for (const p of all("SELECT id, jira_key FROM projects WHERE jira_project IS NULL AND jira_key IS NOT NULL AND UPPER(jira_key)=UPPER(COALESCE(plane_identifier,''))")) {
        const k = p.jira_key.toUpperCase();
        const owner = one("SELECT id FROM projects WHERE id<>? AND UPPER(jira_project)=?", p.id, k)
          || one("SELECT project_id AS id FROM project_sources WHERE system='jira' AND status='linked' AND UPPER(key)=? AND project_id<>?", k, p.id);
        if (owner) { run("UPDATE projects SET jira_key=NULL WHERE id=?", p.id); writeLog("info", LOG, `Kürzel ${k} von Projekt #${p.id} entfernt (gehört in Jira zu Projekt #${owner.id})`); }
      }
      run("INSERT OR REPLACE INTO settings (key,value) VALUES ('migr_680_keys','1')");
    }
    setTimeout(() => autoScan().catch(() => {}), 60 * 1000);
    setInterval(() => autoScan().catch(() => {}), 6 * 3600e3);
  }
  async function autoScan() {
    const last = getSettings().sources_last_scan;
    if (last && Date.now() - Date.parse(last) < 30 * 864e5) return;
    await scan("monatlich");
  }

  /* Quellen einlesen */
  async function scan(trigger = "von Hand") {
    if (scanning) return { busy: true };
    scanning = true;
    const out = { plane: null, jira: null, new: 0, gone: 0, errors: [] };
    try {
      const seen = { plane: new Set(), jira: new Set() };
      if (plane?.api?.configured()) {
        try {
          const list = await plane.api.projects();
          out.plane = list.length;
          for (const p of list) {
            seen.plane.add(p.id);
            const archived = !!p.archived_at;
            upsert("plane", p.id, (p.identifier || "").toUpperCase(), p.name, archived);
          }
        } catch (e) { out.errors.push("Plane: " + e.message); }
      }
      const s = getSettings();
      if (s.jira_enabled !== "0" && s.jira_base_url && s.jira_token) {
        try {
          const list = await tempoPush.request("GET", "/rest/api/2/project");
          out.jira = (list || []).length;
          for (const p of list || []) { seen.jira.add(String(p.key).toUpperCase()); upsert("jira", String(p.key).toUpperCase(), String(p.key).toUpperCase(), p.name, !!p.archived); }
        } catch (e) { out.errors.push("Jira: " + e.message); }
      }
      // verschwundene Quellen markieren (nur Systeme, die gerade erfolgreich gelesen wurden)
      for (const sys of ["plane", "jira"]) {
        if (out[sys] == null) continue;
        for (const r of all("SELECT id, external_id, gone FROM project_sources WHERE system=?", sys))
          if (!seen[sys].has(r.external_id) && !r.gone) { run("UPDATE project_sources SET gone=1 WHERE id=?", r.id); out.gone++; }
      }
      out.new = one("SELECT COUNT(*) AS n FROM project_sources WHERE status='new' AND gone=0")?.n || 0;
      run("INSERT OR REPLACE INTO settings (key,value) VALUES ('sources_last_scan',?)", new Date().toISOString());
      lastScan = { at: new Date().toISOString(), trigger, ...out };
      writeLog(out.errors.length ? "warn" : "info", LOG, `Projekte eingelesen (${trigger}): Plane ${out.plane ?? "–"}, Jira ${out.jira ?? "–"}, neu zu prüfen ${out.new}${out.gone ? `, verschwunden ${out.gone}` : ""}${out.errors.length ? " – " + out.errors.join("; ") : ""}`);
      return lastScan;
    } finally { scanning = false; }
  }
  function upsert(system, extId, key, name, archived) {
    const ex = one("SELECT * FROM project_sources WHERE system=? AND external_id=?", system, extId);
    if (ex) { run("UPDATE project_sources SET key=?, name=?, gone=?, last_seen=datetime('now') WHERE id=?", key, name, archived ? 1 : 0, ex.id); return; }
    // schon über die alten Felder verknüpft?
    const linked = system === "plane" ? one("SELECT id FROM projects WHERE plane_project_id=?", extId) : one("SELECT id FROM projects WHERE UPPER(jira_project)=?", extId);
    run("INSERT INTO project_sources (system,external_id,key,name,project_id,status,gone) VALUES (?,?,?,?,?,?,?)",
      system, extId, key, name, linked?.id || null, linked ? "linked" : "new", archived ? 1 : 0);
  }

  /* Vorschlag für eine neue Quelle */
  function suggestFor(src) {
    const out = [];
    // 1. gleiches Kürzel in der anderen Quelle, schon verknüpft
    const twin = one("SELECT s.project_id, p.name FROM project_sources s JOIN projects p ON p.id=s.project_id WHERE s.system<>? AND UPPER(s.key)=? AND s.status='linked' LIMIT 1", src.system, (src.key || "").toUpperCase());
    if (twin) out.push({ project_id: twin.project_id, name: twin.name, score: 95, reason: `gleiches Kürzel ${src.key} in ${src.system === "plane" ? "Jira" : "Plane"}` });
    // 2. Name/Kürzel ähnlich
    for (const p of all("SELECT id, name, jira_key, plane_identifier FROM projects WHERE COALESCE(status,'')<>'archived'")) {
      if (out.some(o => o.project_id === p.id)) continue;
      const keyHit = src.key && [p.jira_key, p.plane_identifier].some(k => (k || "").toUpperCase() === src.key.toUpperCase());
      const sim = Math.max(textSim(src.name, p.name), nameSim(src.name, p.name, commonWords()) * 0.95);
      const score = Math.round(Math.max(keyHit ? 85 : 0, sim * 90));
      if (score >= 45) out.push({ project_id: p.id, name: p.name, score, reason: keyHit ? `Kürzel ${src.key}` : `Name ähnlich (${Math.round(sim * 100)} %)` });
    }
    return out.sort((a, b) => b.score - a.score).slice(0, 3);
  }

  /* Wörter, die in vielen Projektnamen vorkommen (Firmenname) – zählen beim Vergleich nicht */
  function commonWords() {
    const cnt = {};
    for (const p of all("SELECT name FROM projects WHERE COALESCE(status,'')<>'archived'")) for (const w of new Set(toks(p.name))) cnt[w] = (cnt[w] || 0) + 1;
    return new Set(Object.entries(cnt).filter(([, n]) => n >= 3).map(([w]) => w));
  }
  /* Vermutlich doppelte Projekte (gleicher Kunde zweimal angelegt) */
  function duplicates() {
    let ign = []; try { ign = JSON.parse(getSettings().dup_ignore || "[]"); } catch (_) {}
    const common = commonWords();
    const ps = all("SELECT * FROM projects WHERE COALESCE(status,'')<>'archived' AND COALESCE(muted,0)=0 AND merged_into IS NULL").map(p => {
      const srcs = all("SELECT system, key FROM project_sources WHERE project_id=? AND status='linked'", p.id);
      const used = one("SELECT COUNT(*) AS n, COALESCE(SUM(duration_seconds),0) AS s FROM time_entries WHERE project_id=?", p.id);
      const keys = new Set([p.jira_key, p.plane_identifier, p.jira_project, ...srcs.map(x => x.key)].filter(Boolean).map(k => k.toUpperCase()));
      return { ...p, srcs, n: used.n, secs: used.s, keys };
    });
    const out = [];
    for (let i = 0; i < ps.length; i++) for (let j = i + 1; j < ps.length; j++) {
      const a = ps[i], b = ps[j];
      if (ign.includes(`${Math.min(a.id, b.id)}-${Math.max(a.id, b.id)}`)) continue;
      const sameName = a.name.trim().toLowerCase() === b.name.trim().toLowerCase();
      const conflict = a.srcs.filter(x => b.srcs.some(y => y.system === x.system)).map(x => x.system);
      if (conflict.length && !sameName) continue;          // beide haben schon eine Quelle desselben Systems und heißen verschieden → verschiedene Projekte
      const keyHit = [...a.keys].some(k => b.keys.has(k));
      const sim = nameSim(a.name, b.name, common);
      const score = sameName ? 100 : Math.round(Math.max(keyHit ? 90 : 0, sim * 85));
      if (score < 70) continue;
      const [into, from] = (b.secs > a.secs || (b.secs === a.secs && b.srcs.some(x => x.system === "plane"))) ? [b, a] : [a, b];
      const d = x => ({ id: x.id, name: x.name, entries: x.n, seconds: x.secs, sources: x.srcs });
      out.push({ score, reason: sameName ? "gleicher Name" : keyHit ? "gleiches Kürzel" : "ähnlicher Name", from: d(from), into: d(into), conflict });
    }
    return out.sort((x, y) => y.score - x.score).slice(0, 10);
  }

  /* Verknüpfung in die alten Felder spiegeln (bestehender Code nutzt sie) */
  function mirror(src, projectId) {
    if (src.system === "plane") {
      run("UPDATE projects SET plane_project_id=NULL, plane_identifier=NULL WHERE plane_project_id=?", src.external_id);
      if (projectId) run("UPDATE projects SET plane_project_id=?, plane_identifier=? WHERE id=?", src.external_id, src.key, projectId);
    } else {
      run("UPDATE projects SET jira_project=NULL WHERE UPPER(jira_project)=?", src.external_id);
      if (projectId) run("UPDATE projects SET jira_project=? WHERE id=?", src.external_id, projectId);
    }
  }

  const projectRow = p => {
    const srcs = all("SELECT id, system, key, name, gone FROM project_sources WHERE project_id=? AND status='linked' ORDER BY system", p.id);
    const used = one("SELECT COUNT(*) AS n, COALESCE(SUM(duration_seconds),0) AS s, MAX(date) AS last FROM time_entries WHERE project_id=?", p.id);
    const eff = { plane: allowsPlane(p.id), tempo: allowsTempo(p.id) };
    return { id: p.id, name: p.name, status: p.status, closed: p.status === "archived", muted: !!p.muted, merged_into: p.merged_into || null, output_rule: p.output_rule || "default", successor_id: p.successor_id || null,
      effective: eff, in_plane: inPlane(p.id),
      successor_name: p.successor_id ? one("SELECT name FROM projects WHERE id=?", p.successor_id)?.name || null : null,
      sources: srcs, entries: used?.n || 0, seconds: used?.s || 0, last: used?.last || null, all_gone: srcs.length > 0 && srcs.every(x => x.gone),
      gone_ack: !!p.gone_ack,
      // Projektname soll später genau wie in Plane heißen (sonst wie in Jira)
      name_source: (() => { const src = srcs.find(x => x.system === "plane" && !x.gone) || srcs.find(x => x.system === "jira" && !x.gone);
        return src && src.name && src.name.trim() !== p.name.trim() ? { system: src.system, name: src.name.trim() } : null; })() };
  };

  app.get("/api/sources", (_, res) => {
    const pending = all("SELECT * FROM project_sources WHERE status='new' AND gone=0 ORDER BY system, name").map(s => ({ ...s, suggestions: suggestFor(s) }));
    const projects = all("SELECT * FROM projects ORDER BY CASE WHEN status='archived' THEN 1 ELSE 0 END, name").map(projectRow);
    const ignored = all("SELECT * FROM project_sources WHERE status='ignored' ORDER BY system, name");
    const s = getSettings();
    res.json({ pending, projects, ignored, duplicates: duplicates(), last_scan: s.sources_last_scan || null, last: lastScan,
      default_rule: s.output_rule_default || "bridge" });
  });
  app.post("/api/sources/scan", async (_, res) => { try { res.json(await scan("von Hand")); } catch (e) { res.status(500).json({ error: e.message }); } });

  /* übernehmen: {project_id} = an bestehendes Projekt hängen, {create:{name}} = neues Projekt */
  app.post("/api/sources/:id/link", (req, res) => {
    const src = one("SELECT * FROM project_sources WHERE id=?", Number(req.params.id));
    if (!src) return res.status(404).json({ error: "Quelle nicht gefunden" });
    let pid = req.body?.project_id ? Number(req.body.project_id) : null;
    if (!pid && req.body?.create) pid = createFrom(src, req.body.create.name);
    if (!pid) return res.status(400).json({ error: "Projekt wählen oder neu anlegen" });
    linkSource(src, pid);
    res.json({ ok: true, project_id: pid });
  });
  function createFrom(src, name) {
    name = String(name || src.name || src.key).trim().slice(0, 120);
    const pid = Number(run("INSERT INTO projects (name,jira_key,status,output_rule) VALUES (?,?,'active','default')", name, src.system === "jira" ? src.key || null : null).lastInsertRowid);
    writeLog("info", LOG, `Projekt „${name}“ aus ${src.system === "plane" ? "Plane" : "Jira"} ${src.key || ""} übernommen`);
    return pid;
  }
  /* EINZIGER Weg, eine Quelle mit einem Projekt zu verbinden (Projekte-Seite, Assistent, alte Einstellungs-Routen, meine Tickets) */
  function linkSource(src, pid) {
    // je System höchstens eine Quelle pro Projekt (1 Kunde = 1 Plane-Projekt)
    const other = one("SELECT * FROM project_sources WHERE project_id=? AND system=? AND status='linked' AND id<>?", pid, src.system, src.id);
    if (other) { run("UPDATE project_sources SET project_id=NULL, status='new' WHERE id=?", other.id); mirror(other, null); }
    run("UPDATE project_sources SET project_id=?, status='linked' WHERE id=?", pid, src.id);
    mirror(src, pid);
    writeLog("info", LOG, `${src.system === "plane" ? "Plane" : "Jira"}-Projekt ${src.key || src.name} → Projekt #${pid}`);
  }
  /* Quelle nach System + Kennung finden oder anlegen (für Aufrufe außerhalb der Projekte-Seite) */
  function sourceOf(system, extId, key, name) {
    extId = system === "jira" ? String(extId).toUpperCase() : String(extId);
    let src = one("SELECT * FROM project_sources WHERE system=? AND external_id=?", system, extId);
    if (!src) {
      run("INSERT INTO project_sources (system,external_id,key,name,status) VALUES (?,?,?,?,'new')", system, extId, (key || (system === "jira" ? extId : "")).toUpperCase() || null, name || key || extId);
      src = one("SELECT * FROM project_sources WHERE system=? AND external_id=?", system, extId);
    }
    return src;
  }
  function linkBy(system, extId, pid, key, name) { const src = sourceOf(system, extId, key, name); if (pid) linkSource(src, pid); else { run("UPDATE project_sources SET project_id=NULL, status='new' WHERE id=?", src.id); mirror(src, null); } return src; }
  /* Jira-Kürzel (Prefix eines Ticket-Keys) → Projekt. Reihenfolge: verknüpfte Jira-Quelle, jira_project, eigenes Kürzel; Nachfolger beachten */
  function projectByJiraKey(prefix) {
    const k = String(prefix || "").toUpperCase(); if (!k) return null;
    const r = one("SELECT project_id AS id FROM project_sources WHERE system='jira' AND status='linked' AND UPPER(key)=? AND project_id IS NOT NULL", k)
      || one("SELECT id FROM projects WHERE UPPER(jira_project)=? ORDER BY id LIMIT 1", k)
      || one("SELECT id FROM projects WHERE UPPER(jira_key)=? AND COALESCE(status,'')<>'archived' AND UPPER(COALESCE(plane_identifier,''))<>? ORDER BY id LIMIT 1", k, k)
      || one("SELECT id FROM projects WHERE UPPER(jira_key)=? ORDER BY CASE WHEN status='archived' THEN 1 ELSE 0 END, id LIMIT 1", k);
    return r ? redirect(r.id).project_id : null;
  }

  app.post("/api/sources/:id/unlink", (req, res) => {
    const src = one("SELECT * FROM project_sources WHERE id=?", Number(req.params.id));
    if (!src) return res.status(404).json({ error: "Quelle nicht gefunden" });
    run("UPDATE project_sources SET project_id=NULL, status='new' WHERE id=?", src.id); mirror(src, null);
    res.json({ ok: true });
  });
  app.post("/api/sources/:id/ignore", (req, res) => {
    run("UPDATE project_sources SET status=?, project_id=NULL WHERE id=?", req.body?.undo ? "new" : "ignored", Number(req.params.id));
    res.json({ ok: true });
  });

  /* Projekt-Einstellungen: Regel, Nachfolger, Abschluss */
  const RULES = ["default", "bridge", "plane", "tempo", "none"];
  app.put("/api/projects/:id/setup", (req, res) => {
    const id = Number(req.params.id), b = req.body || {};
    const p = one("SELECT * FROM projects WHERE id=?", id); if (!p) return res.status(404).json({ error: "Projekt nicht gefunden" });
    if (b.output_rule !== undefined) { if (!RULES.includes(b.output_rule)) return res.status(400).json({ error: "Unbekannte Regel" }); run("UPDATE projects SET output_rule=? WHERE id=?", b.output_rule, id); }
    if (b.successor_id !== undefined) {
      const sid = b.successor_id ? Number(b.successor_id) : null;
      if (sid === id) return res.status(400).json({ error: "Ein Projekt kann nicht sein eigener Nachfolger sein" });
      if (sid) { let cur = sid, n = 0; while (cur && n++ < 20) { if (cur === id) return res.status(400).json({ error: "Nachfolger würde einen Kreis bilden" }); cur = one("SELECT successor_id FROM projects WHERE id=?", cur)?.successor_id; } }
      run("UPDATE projects SET successor_id=? WHERE id=?", sid, id);
    }
    if (b.gone_ack !== undefined) run("UPDATE projects SET gone_ack=? WHERE id=?", b.gone_ack ? 1 : 0, id);
    if (b.muted !== undefined) run("UPDATE projects SET muted=? WHERE id=?", b.muted ? 1 : 0, id);
    if (b.closed !== undefined) run("UPDATE projects SET status=?, closed_at=? WHERE id=?", b.closed ? "archived" : "active", b.closed ? new Date().toISOString() : null, id);
    writeLog("info", LOG, `Projekt „${p.name}“ eingestellt: ${JSON.stringify(b)}`);
    res.json(projectRow(one("SELECT * FROM projects WHERE id=?", id)));
  });
  /* Alle offenen „Neue Projekte“ auf einmal stumm (ignorieren) – man nutzt erst mal nur ein paar */
  app.post("/api/sources/ignore-all", (req, res) => {
    const ids = Array.isArray(req.body?.except) ? req.body.except.map(Number) : [];
    const r = run(`UPDATE project_sources SET status='ignored' WHERE status='new' AND gone=0${ids.length ? ` AND id NOT IN (${ids.join(",")})` : ""}`);
    writeLog("info", LOG, `${r.changes} neue Projekte stumm geschaltet`);
    res.json({ ok: true, n: r.changes });
  });

  /* Projekt A in Projekt B zusammenführen: Buchungen, Aufgaben, Quellen, Regeln wandern zu B; A bleibt abgeschlossen als Verweis */
  app.post("/api/projects/:id/merge", (req, res) => {
    const a = one("SELECT * FROM projects WHERE id=?", Number(req.params.id)), b = one("SELECT * FROM projects WHERE id=?", Number(req.body?.into));
    if (!a || !b) return res.status(404).json({ error: "Projekt nicht gefunden" });
    if (a.id === b.id) return res.status(400).json({ error: "Ein Projekt kann nicht in sich selbst zusammengeführt werden" });
    const moved = {};
    const mv = (tbl, col = "project_id") => { try { moved[tbl] = run(`UPDATE ${tbl} SET ${col}=? WHERE ${col}=?`, b.id, a.id).changes; } catch (_) {} };
    try {
      run("BEGIN");
      mv("time_entries"); mv("tasks"); mv("ssh_sessions"); mv("plan_tasks"); mv("meeting_rules"); mv("timer_state");
      // Quellen: je System höchstens eine am Ziel – fehlt dort eine, übernimmt B die Quelle von A
      for (const src of all("SELECT * FROM project_sources WHERE project_id=? AND status='linked'", a.id)) {
        const has = one("SELECT * FROM project_sources WHERE project_id=? AND system=? AND status='linked'", b.id, src.system);
        // Konflikt (beide haben z. B. eine Jira-Quelle): keep_from = die Quelle von A behalten, die von B wird wieder „neu“
        const keepFrom = Array.isArray(req.body?.keep_from) && req.body.keep_from.includes(src.system);
        if (has && keepFrom) { run("UPDATE project_sources SET project_id=NULL, status='new' WHERE id=?", has.id); mirror(has, null); run("UPDATE project_sources SET project_id=? WHERE id=?", b.id, src.id); mirror(src, b.id); }
        else if (has) { run("UPDATE project_sources SET project_id=NULL, status='new' WHERE id=?", src.id); mirror(src, null); }
        else { run("UPDATE project_sources SET project_id=? WHERE id=?", b.id, src.id); mirror(src, b.id); }
      }
      if (!b.tempo_issue && a.tempo_issue) run("UPDATE projects SET tempo_issue=? WHERE id=?", a.tempo_issue, b.id);
      if (!b.tempo_account && a.tempo_account) run("UPDATE projects SET tempo_account=? WHERE id=?", a.tempo_account, b.id);
      run("UPDATE projects SET successor_id=? WHERE successor_id=?", b.id, a.id);
      run("UPDATE projects SET status='archived', closed_at=?, successor_id=?, merged_into=?, muted=1, plane_project_id=NULL, plane_identifier=NULL, jira_project=NULL WHERE id=?",
        new Date().toISOString(), b.id, b.id, a.id);
      run("COMMIT");
    } catch (e) { try { run("ROLLBACK"); } catch (_) {} return res.status(500).json({ error: e.message }); }
    writeLog("info", LOG, `Projekt „${a.name}“ in „${b.name}“ zusammengeführt: ${JSON.stringify(moved)}`);
    res.json({ ok: true, moved, into: b.id });
  });

  /* Namen angleichen: Projekt heißt wie seine Plane-Quelle (sonst Jira). {ids:[..]} oder alle */
  app.post("/api/projects/adopt-names", (req, res) => {
    const ids = Array.isArray(req.body?.ids) ? req.body.ids.map(Number) : null;
    const done = [];
    for (const p of all("SELECT * FROM projects WHERE COALESCE(status,'')<>'archived'")) {
      if (ids && !ids.includes(p.id)) continue;
      const ns = projectRow(p).name_source; if (!ns) continue;
      run("UPDATE projects SET name=? WHERE id=?", ns.name, p.id);
      done.push({ id: p.id, from: p.name, to: ns.name });
    }
    if (done.length) writeLog("info", LOG, `Projektnamen angeglichen: ${done.map(d => `„${d.from}“ → „${d.to}“`).join(", ")}`);
    res.json({ ok: true, renamed: done });
  });

  /* Lokale Zeiten: was kommt wo an? Je aktives Projekt: in Plane (mit Plane-Ticket), in Plane-Projekt aber ohne Ticket, Projekt (noch) nicht in Plane */
  function localTimes(from) {
    const out = [];
    for (const p of all("SELECT id, name, plane_identifier FROM projects WHERE COALESCE(status,'')<>'archived' AND COALESCE(muted,0)=0 AND merged_into IS NULL ORDER BY name")) {
      const pl = allowsPlane(p.id), pref = (p.plane_identifier || "").toUpperCase();
      const r = all(`SELECT e.duration_seconds AS s, t.jira_ticket AS k, t.plane_work_item_id AS wi FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id
                     WHERE e.project_id=? AND e.end_time IS NOT NULL AND COALESCE(e.duration_seconds,0)>0 AND e.date>=? AND COALESCE(e.source,'manual') NOT IN ('tempo','tempo_csv')`, p.id, from);
      if (!r.length) continue;
      const x = { project_id: p.id, name: p.name, in_plane: pl, plane: 0, jira_ticket: 0, no_ticket: 0, not_plane: 0, n_no_ticket: 0 };
      for (const e of r) {
        const k = String(e.k || "").trim().toUpperCase();
        if (!pl) x.not_plane += e.s;
        else if (e.wi || (pref && k.startsWith(pref + "-"))) x.plane += e.s;
        else if (k) x.jira_ticket += e.s;           // alte Buchung auf Jira-Ticket – bleibt in Tempo, wie sie ist
        else { x.no_ticket += e.s; x.n_no_ticket++; }
      }
      out.push(x);
    }
    return out;
  }
  app.get("/api/sources/local-times", (req, res) => {
    const from = /^\d{4}-\d{2}-\d{2}$/.test(req.query.from || "") ? req.query.from : (getSettings().tempo_push_since || "2000-01-01");
    res.json({ from, projects: localTimes(from) });
  });
  /* Buchungen ohne Plane-Ticket eines Plane-Projekts auf ein Plane-Ticket legen (danach übernimmt der Plane-Abgleich) */
  app.post("/api/sources/local-times/assign", (req, res) => {
    const pid = Number(req.body?.project_id), key = String(req.body?.ticket || "").trim().toUpperCase(), from = String(req.body?.from || "2000-01-01");
    const p = one("SELECT * FROM projects WHERE id=?", pid);
    if (!p || !allowsPlane(pid)) return res.status(400).json({ error: "Projekt ist nicht in Plane" });
    if (!p.plane_identifier || !key.startsWith(p.plane_identifier.toUpperCase() + "-")) return res.status(400).json({ error: `Ticket muss zu ${p.plane_identifier || "diesem Plane-Projekt"} gehören (z. B. ${p.plane_identifier || "PRJB"}-12)` });
    let t = one("SELECT id FROM tasks WHERE project_id=? AND UPPER(TRIM(jira_ticket))=?", pid, key);
    if (!t) t = { id: Number(run("INSERT INTO tasks (project_id,title,jira_ticket,status) VALUES (?,?,?,'open')", pid, String(req.body?.title || key).slice(0, 200), key).lastInsertRowid) };
    const ids = all(`SELECT e.id FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id WHERE e.project_id=? AND e.end_time IS NOT NULL AND e.date>=?
                     AND COALESCE(e.source,'manual') NOT IN ('tempo','tempo_csv') AND TRIM(COALESCE(t.jira_ticket,''))=''`, pid, from).map(r => r.id);   // nur Buchungen ganz ohne Ticket
    for (const id of ids) run("UPDATE time_entries SET task_id=? WHERE id=?", t.id, id);
    writeLog("info", LOG, `${ids.length} Buchungen von „${p.name}“ ohne Plane-Ticket auf ${key} gelegt`);
    res.json({ ok: true, n: ids.length, task_id: t.id });
  });

  app.post("/api/sources/duplicates/ignore", (req, res) => {
    const a = Number(req.body?.a), b = Number(req.body?.b); let ign = []; try { ign = JSON.parse(getSettings().dup_ignore || "[]"); } catch (_) {}
    ign.push(`${Math.min(a, b)}-${Math.max(a, b)}`);
    run("INSERT OR REPLACE INTO settings (key,value) VALUES ('dup_ignore',?)", JSON.stringify([...new Set(ign)])); res.json({ ok: true });
  });

  app.put("/api/sources/default-rule", (req, res) => {
    const r = String(req.body?.rule || ""); if (!RULES.slice(1).includes(r)) return res.status(400).json({ error: "Unbekannte Regel" });
    run("INSERT OR REPLACE INTO settings (key,value) VALUES ('output_rule_default',?)", r); res.json({ ok: true });
  });

  /* Hilfen für andere Module */
  function ruleOf(projectId) {
    const p = projectId ? one("SELECT output_rule FROM projects WHERE id=?", projectId) : null;
    const r = p?.output_rule && p.output_rule !== "default" ? p.output_rule : (getSettings().output_rule_default || "bridge");
    return r;
  }
  /* Nach Plane nur, wenn das Projekt WIRKLICH in Plane ist (verknüpfte, nicht archivierte Plane-Quelle).
     Projekte, die noch nicht nach Plane umgezogen sind, gehen nur nach Jira/Tempo. */
  const inPlane = pid => !!(pid && one("SELECT 1 FROM projects WHERE id=? AND plane_project_id IS NOT NULL", pid)
    && !one("SELECT 1 FROM project_sources WHERE project_id=? AND system='plane' AND status='linked' AND gone=1", pid));
  const allowsPlane = pid => ["bridge", "plane"].includes(ruleOf(pid)) && inPlane(pid);
  const allowsTempo = pid => ["bridge", "tempo"].includes(ruleOf(pid));
  /* Buchung auf geschlossenes Projekt → Nachfolger (Aufgabe nur behalten, wenn sie zum Nachfolger gehört) */
  function redirect(projectId, taskId) {
    let pid = projectId ? Number(projectId) : null, tid = taskId ? Number(taskId) : null, n = 0, from = null;
    while (pid && n++ < 10) {
      const p = one("SELECT id, name, status, successor_id FROM projects WHERE id=?", pid);
      if (!p || p.status !== "archived" || !p.successor_id) break;
      from = from || p.name; pid = p.successor_id;
      if (tid && one("SELECT project_id FROM tasks WHERE id=?", tid)?.project_id !== pid) tid = null;
    }
    return { project_id: pid, task_id: tid, redirected_from: pid !== (projectId ? Number(projectId) : null) ? from : null };
  }
  return { init, scan, ruleOf, inPlane, allowsPlane, allowsTempo, redirect, linkSource, linkBy, sourceOf, projectByJiraKey, duplicates };
};
