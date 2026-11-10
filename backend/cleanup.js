/* ═══════════════════════════════════════════════════════════════════
   Aufräumen: lokale Projekte → richtige Projekte (v6.11.0)
   ───────────────────────────────────────────────────────────────────
   Projekte gibt es grundsätzlich nur, wenn sie in Plane und/oder Jira
   existieren. Was früher lokal als „Projekt“ angelegt wurde, war oft ein
   Ticket (z. B. „Onyx“ = Jira-Ticket in IU). Hier wird je lokalem Projekt
   entschieden – die Zeiten werden dabei immer gerettet:
     • ist ein Ticket   → Buchungen in das richtige Projekt, auf dieses Ticket
     • gehört zu Projekt → Buchungen ins richtige Projekt (Tickets bleiben)
     • ist ein Projekt  → mit einer Plane-/Jira-Quelle verbinden
     • leer             → entfernen
     • lokal behalten   → nur wenn „lokale Projekte erlauben“ an ist
   Vor jeder Änderung wird eine Sicherung angelegt; jede verschobene
   Buchung steht im Änderungsprotokoll.
═══════════════════════════════════════════════════════════════════ */
const { textSim, nameSim } = require("./textmatch");

module.exports = function registerCleanup(app, ctx) {
  const { one, all, run, getSettings, writeLog, hooks, tempoPush, createBackup } = ctx;
  const LOG = "aufräumen";
  const KEY_RE = /^[A-Z][A-Z0-9_]*-\d+$/;
  const up = v => String(v || "").trim().toUpperCase();

  const hasSource = pid => !!one("SELECT 1 FROM project_sources WHERE project_id=? AND status='linked'", pid);
  const targets = () => all(`SELECT p.id, p.name, GROUP_CONCAT(s.system || ':' || COALESCE(s.key,''), ',') AS srcs FROM projects p
    JOIN project_sources s ON s.project_id=p.id AND s.status='linked'
    WHERE COALESCE(p.status,'')<>'archived' AND p.merged_into IS NULL GROUP BY p.id ORDER BY p.name`)
    .map(p => ({ id: p.id, name: p.name, sources: (p.srcs || "").split(",").filter(Boolean).map(x => { const [system, key] = x.split(":"); return { system, key }; }) }));

  /* Welches Projekt passt zu einem Ticket-Key? (Quelle mit gleichem Kürzel) */
  function projectForKey(key) {
    const pre = up(key).split("-")[0]; if (!pre) return null;
    const r = one("SELECT project_id AS id FROM project_sources WHERE status='linked' AND UPPER(key)=? AND project_id IS NOT NULL ORDER BY CASE system WHEN 'plane' THEN 0 ELSE 1 END LIMIT 1", pre);
    return r?.id || hooks.sources?.projectByJiraKey(pre) || null;
  }

  function localProjects() {
    const tg = targets();
    return all("SELECT * FROM projects WHERE COALESCE(status,'')<>'archived' AND merged_into IS NULL ORDER BY name").filter(p => !hasSource(p.id)).map(p => {
      const used = one("SELECT COUNT(*) AS n, COALESCE(SUM(duration_seconds),0) AS s, MIN(date) AS first, MAX(date) AS last FROM time_entries WHERE project_id=?", p.id);
      const tasks = all(`SELECT t.id, t.title, UPPER(TRIM(COALESCE(t.jira_ticket,''))) AS key, COUNT(e.id) AS n, COALESCE(SUM(e.duration_seconds),0) AS s
        FROM tasks t LEFT JOIN time_entries e ON e.task_id=t.id WHERE t.project_id=? GROUP BY t.id ORDER BY s DESC`, p.id);
      const noTicket = one(`SELECT COUNT(*) AS n, COALESCE(SUM(e.duration_seconds),0) AS s FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id
        WHERE e.project_id=? AND TRIM(COALESCE(t.jira_ticket,''))=''`, p.id);
      // Vorschlag 1: Tickets der Aufgaben zeigen schon auf ein Projekt mit Quelle
      const votes = {};
      for (const t of tasks) if (KEY_RE.test(t.key)) { const pid = projectForKey(t.key); if (pid) votes[pid] = (votes[pid] || 0) + (t.s || 1); }
      const byTickets = Object.entries(votes).sort((a, b) => b[1] - a[1])[0];
      // Vorschlag 2: ein Ticket (aus „Meine Tickets“ / Plane / Tempo-Historie) heißt wie das Projekt
      const tk = all(`SELECT t.jira_ticket AS key, t.title, t.project_id FROM tasks t JOIN project_sources s ON s.project_id=t.project_id AND s.status='linked'
        WHERE TRIM(COALESCE(t.jira_ticket,''))<>'' GROUP BY UPPER(t.jira_ticket)`)
        .map(t => ({ ...t, key: up(t.key), score: Math.round(Math.max(textSim(p.name, t.title), nameSim(p.name, t.title)) * 100) }))
        .filter(t => t.score >= 60).sort((a, b) => b.score - a.score).slice(0, 3);
      // Vorschlag 3: Name ähnlich einer Quelle (ist eigentlich ein Projekt)
      const src = all("SELECT id, system, key, name, status FROM project_sources WHERE status IN ('new','ignored') AND gone=0")
        .map(s => ({ ...s, score: Math.round(Math.max(textSim(p.name, s.name), nameSim(p.name, s.name), up(p.jira_key) && up(p.jira_key) === up(s.key) ? 0.9 : 0) * 100) }))
        .filter(s => s.score >= 60).sort((a, b) => b.score - a.score).slice(0, 3);
      let suggest = null;
      if (!used.n && !tasks.length) suggest = { action: "remove", reason: "keine Buchungen, keine Aufgaben" };
      else if (byTickets && Object.keys(votes).length === 1 && !noTicket.n) suggest = { action: "move", target: Number(byTickets[0]), reason: "alle Tickets gehören dorthin" };
      else if (tk[0]) suggest = { action: "ticket", target: tk[0].project_id, ticket: tk[0].key, title: tk[0].title, reason: `Ticket ${tk[0].key} „${tk[0].title}“ (${tk[0].score} %)` };
      else if (src[0]) suggest = { action: "link", source_id: src[0].id, reason: `${src[0].system === "plane" ? "Plane" : "Jira"}-Projekt ${src[0].key} „${src[0].name}“ (${src[0].score} %)` };
      else if (byTickets) suggest = { action: "move", target: Number(byTickets[0]), reason: "die meisten Tickets gehören dorthin" };
      return { id: p.id, name: p.name, entries: used.n, seconds: used.s, first: used.first, last: used.last, no_ticket: noTicket,
        tasks: tasks.slice(0, 12), ticket_suggestions: tk, source_suggestions: src, suggest };
    });
  }

  app.get("/api/cleanup/local-projects", (_, res) => {
    res.json({ allow_local: getSettings().allow_local_projects === "1", projects: localProjects(), targets: targets(),
      sources: all("SELECT id, system, key, name, status FROM project_sources WHERE status IN ('new','ignored') AND gone=0 ORDER BY system, name") });
  });

  /* Jira-Suche nach einem Ticket zum Projektnamen */
  app.get("/api/cleanup/search", async (req, res) => {
    const q = String(req.query.q || "").replace(/["\\]/g, " ").trim(); if (!q) return res.json([]);
    const out = [];
    for (const t of all(`SELECT t.jira_ticket AS key, t.title, t.project_id, p.name AS project FROM tasks t JOIN projects p ON p.id=t.project_id
        WHERE TRIM(COALESCE(t.jira_ticket,''))<>'' AND (t.title LIKE ? OR t.jira_ticket LIKE ?) LIMIT 8`, `%${q}%`, `%${q}%`))
      out.push({ key: up(t.key), title: t.title, project_id: hasSource(t.project_id) ? t.project_id : projectForKey(t.key), project: t.project, from: "lokal" });
    try {
      const jql = KEY_RE.test(up(q)) ? `key = "${up(q)}"` : `text ~ "${q}" ORDER BY updated DESC`;
      const r = await tempoPush.request("GET", `/rest/api/2/search?maxResults=8&fields=summary,project,status&jql=${encodeURIComponent(jql)}`);
      for (const i of r?.issues || []) if (!out.some(o => o.key === i.key))
        out.push({ key: i.key, title: i.fields?.summary || "", project_id: projectForKey(i.key), project: i.fields?.project?.name || "", status: i.fields?.status?.name || "", from: "Jira" });
    } catch (e) { out.push({ error: "Jira-Suche: " + e.message }); }
    res.json(out);
  });

  /* Ausführen. {project_id, action: ticket|move|link|remove|keep, target_project_id?, ticket?, title?, source_id?} */
  app.post("/api/cleanup/apply", (req, res) => {
    const b = req.body || {}, pid = Number(b.project_id);
    const p = one("SELECT * FROM projects WHERE id=?", pid); if (!p) return res.status(404).json({ error: "Projekt nicht gefunden" });
    const target = b.target_project_id ? one("SELECT * FROM projects WHERE id=?", Number(b.target_project_id)) : null;
    if (["ticket", "move"].includes(b.action)) {
      if (!target || target.id === pid) return res.status(400).json({ error: "Zielprojekt wählen" });
      if (!hasSource(target.id)) return res.status(400).json({ error: "Zielprojekt hat keine Plane-/Jira-Quelle" });
    }
    if (b.action === "ticket" && !KEY_RE.test(up(b.ticket))) return res.status(400).json({ error: "Ticket-Key wie IU-123 angeben" });
    if (b.action === "keep" && getSettings().allow_local_projects !== "1") return res.status(400).json({ error: "Lokale Projekte sind ausgeschaltet (Einstellungen › Arbeitszeit)" });
    const bak = createBackup ? createBackup("vor-aufraeumen") : null;
    const moved = { entries: 0, tasks: 0 };
    try {
      run("BEGIN");
      if (b.action === "ticket" || b.action === "move") {
        let ticketTask = null;
        if (b.action === "ticket") {
          const key = up(b.ticket);
          ticketTask = one("SELECT id FROM tasks WHERE project_id=? AND UPPER(TRIM(jira_ticket))=?", target.id, key)?.id
            || one("SELECT id FROM tasks WHERE UPPER(TRIM(jira_ticket))=? AND project_id<>?", key, pid)?.id;
          if (!ticketTask) {
            const isJira = !one("SELECT 1 FROM project_sources WHERE project_id=? AND system='plane' AND UPPER(key)=?", target.id, key.split("-")[0]);
            ticketTask = Number(run("INSERT INTO tasks (project_id,title,jira_ticket,status,jira_link,ticket_system) VALUES (?,?,?,'open',?,?)",
              target.id, String(b.title || p.name).slice(0, 200), key, isJira ? key : null, isJira ? "jira" : null).lastInsertRowid);
          }
          // Buchungen ohne eigenes Ticket → auf das Ticket
          moved.entries += run(`UPDATE time_entries SET task_id=?, project_id=? WHERE project_id=? AND (task_id IS NULL OR task_id IN
            (SELECT id FROM tasks WHERE project_id=? AND TRIM(COALESCE(jira_ticket,''))=''))`, ticketTask, target.id, pid, pid).changes;
          run("DELETE FROM tasks WHERE project_id=? AND TRIM(COALESCE(jira_ticket,''))='' AND id NOT IN (SELECT task_id FROM time_entries WHERE task_id IS NOT NULL)", pid);
        }
        // Aufgaben mit eigenem Ticket wandern ins Ziel (gibt es das Ticket dort schon, werden die Buchungen umgehängt)
        for (const t of all("SELECT * FROM tasks WHERE project_id=?", pid)) {
          const k = up(t.jira_ticket);
          const twin = k ? one("SELECT id FROM tasks WHERE project_id=? AND UPPER(TRIM(jira_ticket))=?", target.id, k) : null;
          if (twin) { moved.entries += run("UPDATE time_entries SET task_id=?, project_id=? WHERE task_id=?", twin.id, target.id, t.id).changes; run("DELETE FROM tasks WHERE id=?", t.id); }
          else { run("UPDATE tasks SET project_id=? WHERE id=?", target.id, t.id); moved.tasks++; }
        }
        moved.entries += run("UPDATE time_entries SET project_id=? WHERE project_id=?", target.id, pid).changes;
        for (const tbl of ["ssh_sessions", "plan_tasks", "meeting_rules", "timer_state"]) { try { run(`UPDATE ${tbl} SET project_id=? WHERE project_id=?`, target.id, pid); } catch (_) {} }
        run("UPDATE projects SET status='archived', closed_at=?, successor_id=?, merged_into=?, muted=1 WHERE id=?", new Date().toISOString(), target.id, target.id, pid);
      } else if (b.action === "link") {
        const src = one("SELECT * FROM project_sources WHERE id=?", Number(b.source_id)); if (!src) throw new Error("Quelle nicht gefunden");
        hooks.sources.linkSource(src, pid);
      } else if (b.action === "remove") {
        const n = one("SELECT COUNT(*) AS n FROM time_entries WHERE project_id=?", pid).n;
        if (n) throw new Error("Projekt hat Buchungen – erst verschieben");
        run("UPDATE projects SET status='archived', closed_at=?, muted=1 WHERE id=?", new Date().toISOString(), pid);
      } else if (b.action === "keep") {
        run("UPDATE projects SET output_rule='none' WHERE id=?", pid);
      } else throw new Error("Unbekannte Aktion");
      run("COMMIT");
    } catch (e) { try { run("ROLLBACK"); } catch (_) {} return res.status(400).json({ error: e.message }); }
    writeLog("info", LOG, `„${p.name}“: ${b.action}${target ? ` → „${target.name}“` : ""}${b.ticket ? ` auf ${up(b.ticket)}` : ""} · ${moved.entries} Buchungen, ${moved.tasks} Aufgaben${bak ? ` · Sicherung ${bak}` : ""}`);
    res.json({ ok: true, moved, backup: bak });
  });

  app.put("/api/cleanup/allow-local", (req, res) => {
    run("INSERT OR REPLACE INTO settings (key,value) VALUES ('allow_local_projects',?)", req.body?.on ? "1" : "0"); res.json({ ok: true });
  });

  return { localProjects };
};
