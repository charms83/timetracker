/* ═══════════════════════════════════════════════════════════════════
   Jira-Bereich (v6.2.0) – Übergang, bis alles in Plane läuft
   ───────────────────────────────────────────────────────────────────
   Tickets ansehen, suchen, auswählen (Timer starten), anlegen,
   kommentieren, Status ändern; Jira-Projekte lokalen Projekten zuordnen.
   Abschaltbar mit einem Schalter (jira_enabled=0) – dann verschwindet
   der Bereich, Zeitprotokoll läuft ohne Jira weiter.
   Schreibende Aktionen werden im System-Log protokolliert.
═══════════════════════════════════════════════════════════════════ */
module.exports = function registerJira(app, ctx) {
  const { one, all, run, getSettings, writeLog, tempoPush } = ctx;
  const LOG = "jira";
  const FIELDS = "summary,status,priority,project,assignee,reporter,updated,created,issuetype,duedate";
  const on = () => { const s = getSettings(); return s.jira_enabled !== "0" && !!(s.jira_base_url && s.jira_token); };
  const base = () => String(getSettings().jira_base_url || "").replace(/\/+$/, "");
  const req = (m, p, b) => tempoPush.request(m, p, b);
  const wrap = fn => async (req_, res) => {
    if (!on()) return res.status(400).json({ error: "Jira-Anbindung ist ausgeschaltet oder nicht eingerichtet (Einstellungen › Jira)" });
    try { await fn(req_, res); } catch (e) { res.status(e.status && e.status < 500 ? 400 : 502).json({ error: e.message }); }
  };
  const KEY = /^[A-Z][A-Z0-9_]*-\d+$/;

  /* gebuchte Zeit je Jira-Key (lokal) */
  function localSums(keys) {
    if (!keys.length) return {};
    const q = keys.map(() => "?").join(",");
    const rows = all(`SELECT UPPER(COALESCE(t.jira_link, t.jira_ticket)) AS k, t.id AS task_id, SUM(COALESCE(e.duration_seconds,0)) AS secs
                      FROM tasks t LEFT JOIN time_entries e ON e.task_id=t.id
                      WHERE UPPER(COALESCE(t.jira_link, t.jira_ticket)) IN (${q}) GROUP BY t.id`, ...keys);
    const out = {}; for (const r of rows) { const o = out[r.k] ||= { secs: 0, task_id: r.task_id }; o.secs += r.secs || 0; }
    return out;
  }
  const shape = (i, sums) => ({ key: i.key, summary: i.fields?.summary, status: i.fields?.status?.name, status_cat: i.fields?.status?.statusCategory?.key,
    priority: i.fields?.priority?.name, type: i.fields?.issuetype?.name, project: i.fields?.project?.key, project_name: i.fields?.project?.name,
    assignee: i.fields?.assignee?.displayName || null, updated: i.fields?.updated, due: i.fields?.duedate || null,
    booked_seconds: sums[i.key]?.secs || 0, task_id: sums[i.key]?.task_id || null });

  app.get("/api/jira/status", (_, res) => {
    const s = getSettings();
    res.json({ enabled: s.jira_enabled !== "0", configured: !!(s.jira_base_url && s.jira_token), base: base() || null, user: s.jira_username || null });
  });

  /* Suche: mine=1 → meine offenen; q = Key oder Text; jql = eigene Abfrage */
  app.get("/api/jira/search", wrap(async (r, res) => {
    const q = String(r.query.q || "").trim(), start = Math.max(0, parseInt(r.query.startAt) || 0);
    let jql = String(r.query.jql || "").trim();
    if (!jql) {
      const parts = [];
      if (q && KEY.test(q.toUpperCase())) parts.push(`key = "${q.toUpperCase()}"`);
      else if (q) parts.push(`text ~ "${q.replace(/["\\]/g, " ")}"`);
      if (r.query.mine === "1" || !q) parts.push("assignee = currentUser()");
      if (r.query.open !== "0") parts.push("resolution = Unresolved");
      jql = parts.join(" AND ") + " ORDER BY updated DESC";
    }
    const d = await req("GET", `/rest/api/2/search?startAt=${start}&maxResults=50&fields=${FIELDS}&jql=${encodeURIComponent(jql)}`);
    const issues = d?.issues || [];
    const sums = localSums(issues.map(i => i.key));
    res.json({ total: d?.total || 0, startAt: start, jql, issues: issues.map(i => shape(i, sums)), base: base() });
  }));

  /* Details eines Tickets inkl. Kommentare und möglicher Statuswechsel */
  app.get("/api/jira/issue/:key", wrap(async (r, res) => {
    const key = String(r.params.key).toUpperCase(); if (!KEY.test(key)) return res.status(400).json({ error: "Ungültiger Key" });
    const i = await req("GET", `/rest/api/2/issue/${key}?fields=${FIELDS},description,comment`);
    let transitions = [];
    try { transitions = ((await req("GET", `/rest/api/2/issue/${key}/transitions`))?.transitions || []).map(t => ({ id: t.id, name: t.name, to: t.to?.name })); } catch (_) {}
    const sums = localSums([key]);
    const entries = all(`SELECT e.id, e.date, e.start_time, e.end_time, e.duration_seconds, e.comment FROM time_entries e JOIN tasks t ON t.id=e.task_id
                         WHERE UPPER(COALESCE(t.jira_link,t.jira_ticket))=? ORDER BY e.start_time DESC LIMIT 30`, key);
    res.json({ ...shape(i, sums), description: i.fields?.description || "", reporter: i.fields?.reporter?.displayName || null, created: i.fields?.created,
      comments: (i.fields?.comment?.comments || []).slice(-20).map(c => ({ id: c.id, author: c.author?.displayName, created: c.created, body: c.body })),
      transitions, entries, url: `${base()}/browse/${key}` });
  }));

  app.post("/api/jira/issue/:key/comment", wrap(async (r, res) => {
    const key = String(r.params.key).toUpperCase(), body = String(r.body?.body || "").trim();
    if (!KEY.test(key) || !body) return res.status(400).json({ error: "Kommentar fehlt" });
    await req("POST", `/rest/api/2/issue/${key}/comment`, { body: body.slice(0, 30000) });
    writeLog("info", LOG, `Kommentar zu ${key} geschrieben`);
    res.json({ ok: true });
  }));
  app.post("/api/jira/issue/:key/transition", wrap(async (r, res) => {
    const key = String(r.params.key).toUpperCase();
    await req("POST", `/rest/api/2/issue/${key}/transitions`, { transition: { id: String(r.body?.id || "") } });
    writeLog("info", LOG, `Status von ${key} geändert (${r.body?.name || r.body?.id})`);
    res.json({ ok: true });
  }));
  /* Ticket für die Zeiterfassung auswählen → lokale Aufgabe sicherstellen */
  app.post("/api/jira/issue/:key/task", wrap(async (r, res) => {
    const key = String(r.params.key).toUpperCase();
    const i = await req("GET", `/rest/api/2/issue/${key}?fields=summary,project`);
    res.json(tempoPush.ensureTaskForIssue(i, false));
  }));
  /* Neues Ticket anlegen (mir zugewiesen) */
  app.post("/api/jira/issues", wrap(async (r, res) => {
    const b = r.body || {}, s = getSettings();
    const project = String(b.project || "").trim().toUpperCase(), summary = String(b.summary || "").trim();
    if (!project || !summary) return res.status(400).json({ error: "Projekt und Titel angeben" });
    const fields = { project: { key: project }, summary: summary.slice(0, 250), issuetype: { name: b.issuetype || s.jira_new_issuetype || "Task" }, ...(b.description ? { description: String(b.description) } : {}) };
    const out = await tempoPush.createIssue(fields, tempoPush.cfg());   // setzt Tempo-Account, wenn Jira ihn verlangt
    writeLog("success", LOG, `Jira-Ticket ${out?.key} angelegt: ${summary}`);
    const t = tempoPush.ensureTaskForIssue({ key: out.key, fields: { summary, project: { key: project } } }, true);
    res.json({ key: out.key, ...t });
  }));

  /* Projekte verknüpfen (wie bei Plane) */
  app.get("/api/jira/projects", wrap(async (_, res) => {
    const ps = await req("GET", "/rest/api/2/project");
    if (Array.isArray(ps)) run("INSERT OR REPLACE INTO settings (key,value) VALUES ('jira_project_keys',?)", ps.map(p => p.key).join(","));
    const locals = all("SELECT id, name, jira_key, jira_project, plane_identifier, status FROM projects WHERE COALESCE(status,'')<>'archived' ORDER BY name");
    res.json({ jira: (ps || []).map(p => ({ key: p.key, name: p.name, linked: locals.find(l => (l.jira_project || "").toUpperCase() === p.key)?.id || null })), locals });
  }));
  app.put("/api/jira/projects", (r, res) => {
    const links = r.body?.links || {};
    // v6.8.0: dieselbe Verknüpfung wie Projekte › Quellen
    for (const [jkey, pid] of Object.entries(links)) ctx.hooks.sources.linkBy("jira", jkey, pid ? Number(pid) : null, jkey);
    res.json({ ok: true });
  });
  return {};
};
