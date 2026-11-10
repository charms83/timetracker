/* ═══════════════════════════════════════════════════════════════════
   Tempo-Ausgang (Jira Data Center + Tempo Timesheets)
   ───────────────────────────────────────────────────────────────────
   Das Zeitprotokoll bleibt die Erfassung. Jede abgeschlossene Buchung
   wird als EIN Tempo-Worklog geschrieben – auf das passende Jira-Ticket:
     1. Jira-Ticket der Aufgabe (eigene Jira-Tickets, Tempo-Import, Zuordnung)
     2. Plane-Ticket ohne Jira-Gegenstück → Jira-Ticket wird im festen
        Projekt (z. B. ZEIT) angelegt: „[PRJA-123] Titel“ und gemerkt
     3. sonst Buchungsticket des Projekts bzw. Sammel-Buchungsticket
   Ablauf wie bei Plane: lokal zuerst, dann Freigabe (oder automatisch).
   API:  POST/PUT/DELETE {jira}/rest/tempo-timesheets/4/worklogs[/{id}]
   Auth: Bearer-Token (derselbe wie beim Tempo-Import, Einstellung jira_token)
═══════════════════════════════════════════════════════════════════ */
const https = require("https");
const http = require("http");
const crypto = require("crypto");

const EXCLUDED = ["tempo", "tempo_csv"];          // kommen aus Tempo – nie zurückschreiben
const pad = n => String(n).padStart(2, "0");

module.exports = function registerTempoPush(app, ctx) {
  const { one, all, run, getSettings, writeLog, localDateOf, secure, hooks } = ctx;
  const LOG = "tempo";
  let pushing = false, autoTimer = null, justCreated = false;

  /* ── Schema ─────────────────────────────────────────────────────── */
  function init() {
    for (const m of [
      "ALTER TABLE time_entries ADD COLUMN tempo_push_id TEXT",
      "ALTER TABLE time_entries ADD COLUMN tempo_push_hash TEXT",
      "ALTER TABLE time_entries ADD COLUMN tempo_push_synced_at TEXT",
      "ALTER TABLE time_entries ADD COLUMN tempo_push_error TEXT",
      "ALTER TABLE time_entries ADD COLUMN tempo_skip INTEGER DEFAULT 0",
      "ALTER TABLE projects ADD COLUMN tempo_issue TEXT",
      "ALTER TABLE time_entries ADD COLUMN tempo_dup TEXT",
      "ALTER TABLE time_entries ADD COLUMN tempo_push_target TEXT",
      "ALTER TABLE time_entries ADD COLUMN tempo_target TEXT",        // von Hand / per Vorschlag gewähltes Jira-Ticket für diese Buchung
      "ALTER TABLE projects ADD COLUMN tempo_account TEXT",          // Tempo-Account (Pflichtattribut bei Beispiel, z. B. IMPROVEMENTS)
      "ALTER TABLE tasks ADD COLUMN jira_link TEXT",
      "ALTER TABLE time_entries ADD COLUMN tempo_account TEXT",     // Tempo-Account nur für diese Buchung (überschreibt Projekt/Standard)
      "ALTER TABLE projects ADD COLUMN jira_project TEXT",          // verknüpftes Jira-Projekt (Einstellungen › Jira)                  // Jira-Gegenstück (eigenes Jira-Ticket oder für Plane angelegt)
      "ALTER TABLE tasks ADD COLUMN ticket_system TEXT",              // 'jira' = aus „Meine Jira-Tickets“
      "ALTER TABLE tasks ADD COLUMN jira_mine INTEGER DEFAULT 0",          // Hinweis: in Tempo gibt es schon einen Worklog zur selben Zeit ('ok' = trotzdem übertragen)
    ]) { try { run(m); } catch (_) {} }
    run(`CREATE TABLE IF NOT EXISTS tempo_push_deletions (
      id INTEGER PRIMARY KEY AUTOINCREMENT, worklog_id TEXT NOT NULL, label TEXT,
      created_at TEXT DEFAULT (datetime('now')), error TEXT)`);
    run(`CREATE TABLE IF NOT EXISTS tempo_month_tickets (month TEXT NOT NULL, worker TEXT NOT NULL, issue_key TEXT NOT NULL,
      created_at TEXT DEFAULT (datetime('now')), closed_at TEXT, close_error TEXT, desc_hash TEXT, PRIMARY KEY (month, worker))`);
    setInterval(() => { const c = cfg(); if (ready(c) && c.enabled) closeMonthTickets(c).catch(() => {}); }, 6 * 3600e3);
    run(`CREATE TABLE IF NOT EXISTS tempo_months (month TEXT PRIMARY KEY, locked INTEGER DEFAULT 0, reason TEXT, remote_count INTEGER, checked_at TEXT)`);
    setInterval(scheduleAuto, 10 * 60 * 1000);
    setInterval(() => { const c = cfg(); if (ready(c) && c.myTickets) syncMyTickets().catch(e => writeLog("warn", LOG, "Meine Jira-Tickets: " + e.message)); }, 30 * 60 * 1000);
    setTimeout(() => { const c = cfg(); if (ready(c) && c.myTickets) syncMyTickets().catch(() => {}); }, 30 * 1000);
  }

  /* ── Konfiguration ──────────────────────────────────────────────── */
  function cfg() {
    const s = getSettings();
    const since = /^\d{4}-\d{2}-\d{2}$/.test(s.tempo_push_since || "") ? s.tempo_push_since : localDateOf(Date.now()).slice(0, 8) + "01";
    return {
      base: (s.jira_base_url || "").trim().replace(/\/+$/, ""),
      token: (s.jira_token || "").trim(),
      worker: (s.jira_username || "").trim(),
      // Tempo (Jira 8+) erwartet den Benutzer-Schlüssel (z. B. JIRAUSER12345), nicht den Login-Namen
      workerKey: (s.jira_userkey || s.jira_username || "").trim(),
      enabled: s.tempo_push_enabled === "1" || s.tempo_push_enabled === "true",
      mode: s.tempo_push_mode === "auto" ? "auto" : "manual",
      issue: (s.tempo_booking_issue || "").trim().toUpperCase(),
      since,
      newProject: (s.jira_new_project || "").trim().toUpperCase(),
      account: (s.tempo_account || "").trim(),
      accountAttrId: parseInt(s.tempo_account_attr_id) || 2,                      // TEMPO_ACCOUNT_ATTRIBUTE_ID
      issueType: (s.jira_new_issuetype || "").trim() || "Task",
      // Plane-Zeiten in Tempo: „month“ = ein Übergangs-Ticket je Person und Monat (Standard), „ticket“ = Jira-Gegenstück je Plane-Ticket
      planeMode: s.tempo_plane_mode === "ticket" ? "ticket" : "month",
      closeDays: Math.max(0, Math.min(31, parseInt(s.tempo_month_close_days ?? "5") || 0)),
      planeUrl: (s.plane_base_url || "").replace(/\/+$/, "") && s.plane_workspace ? `${s.plane_base_url.replace(/\/+$/, "")}/${s.plane_workspace}` : "",
      myTickets: s.jira_my_tickets !== "0" && s.jira_enabled !== "0",
      jql: (s.jira_my_jql || "").trim() || "assignee = currentUser() AND resolution = Unresolved ORDER BY updated DESC",
      jiraKeys: new Set(String(s.jira_project_keys || "").split(",").map(x => x.trim().toUpperCase()).filter(Boolean)),
    };
  }
  const ready = c => !!(c.base && c.token);

  /* ── HTTP ───────────────────────────────────────────────────────── */
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  function requestOnce(method, path, body) {
    const c = cfg();
    if (!ready(c)) return Promise.reject(Object.assign(new Error("Jira-URL oder Token fehlt (Einstellungen › Jira & Tempo)"), { status: 0 }));
    const url = new URL(c.base + path);
    const lib = url.protocol === "http:" ? http : https;
    const payload = body ? JSON.stringify(body) : null;
    return new Promise((resolve, reject) => {
      const req = lib.request({
        method, hostname: url.hostname, port: url.port || undefined, path: url.pathname + url.search,
        headers: { "Authorization": "Bearer " + c.token, "Accept": "application/json",
          ...(payload ? { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(payload) } : {}) },
        ...(url.protocol === "https:" && secure ? secure.requestOptions(url.href) : {}), timeout: 20000,     // Zertifikat wird geprüft (Firmen-CA aus dem System)
      }, res => {
        let data = "";
        res.on("data", d => data += d);
        res.on("end", () => {
          let json = null; try { json = data ? JSON.parse(data) : null; } catch (_) {}
          if (res.statusCode >= 200 && res.statusCode < 300) return resolve(json);
          const msg = (json && (json.message || json.errorMessages?.join(", ") || json.error || (json.errors && JSON.stringify(json.errors)))) || data.slice(0, 200) || "";
          const hint = res.statusCode === 401 ? " – Token prüfen" : res.statusCode === 403 ? " – keine Berechtigung (Tempo-Rolle, gesperrter Zeitraum?)"
                     : res.statusCode === 404 ? " – Adresse, Ticket oder Worklog nicht gefunden" : "";
          const err = new Error(`${path.startsWith("/rest/api/") ? "Jira" : "Tempo"} HTTP ${res.statusCode}${msg ? ": " + String(msg).slice(0, 220) : ""}${hint}`);
          err.status = res.statusCode; err.retryAfterMs = (Number(res.headers["retry-after"]) || 0) * 1000;
          reject(err);
        });
      });
      req.on("timeout", () => req.destroy(Object.assign(new Error("Zeitüberschreitung (20 s)"), { status: 0 })));
      req.on("error", e => { if (secure) e.message += secure.tlsHint(e); reject(Object.assign(e, { status: e.status || 0 })); });
      if (payload) req.write(payload);
      req.end();
    });
  }
  async function request(method, path, body) {
    for (let attempt = 0; ; attempt++) {
      try { return await (secure ? secure.withChainRepair(cfg().base, () => requestOnce(method, path, body)) : requestOnce(method, path, body)); }
      catch (e) {
        if (e.status !== 429 || attempt >= 3) throw e;
        await sleep(Math.min(30000, Math.max(2000, e.retryAfterMs || 4000 * (attempt + 1))));
      }
    }
  }

  /* ── Einträge → Worklog-Daten ───────────────────────────────────── */
  function rows(where, ...params) {
    return all(`SELECT e.*, t.jira_ticket AS ticket, t.title AS task_title, p.name AS project_name, p.tempo_issue AS proj_issue, p.tempo_account AS proj_account,
                       t.jira_link, t.jira_origin, t.plane_work_item_id, t.ticket_system
                FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id LEFT JOIN projects p ON p.id=e.project_id
                WHERE ${where}`, ...params);
  }
  const localStamp = iso => { const d = new Date(iso); return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.000`; };
  function describe(e) {
    const key = (e.ticket || "").trim().toUpperCase();
    const head = key ? `[${key}]` : e.project_name ? `[${e.project_name}]` : "";
    const title = (e.task_title || "").trim();
    const showTitle = title && title.toUpperCase() !== key;
    const text = (e.comment || "").trim();
    let s = [head, showTitle ? title : "", text ? (showTitle ? "– " : "") + text : ""].filter(Boolean).join(" ").trim();
    return (s || "Arbeit").replace(/\s+/g, " ").slice(0, 900);
  }
  const KEY_RE = /^[A-Z][A-Z0-9_]*-\d+$/;
  /* Wohin geht die Buchung? */
  function targetOf(e, c) {
    const up = v => String(v || "").trim().toUpperCase();
    const fallback = () => {
      // Regel (29.10.2026): In Jira gibt es keine Buchung ohne Ticket. Aufs Sammel-/Übergangs-Ticket
      // geht nur, was über Plane kommt – alles andere muss erst einem Jira-Ticket zugeordnet werden.
      if (!isPlaneBooking(e)) return { target: "", kind: "no-ticket" };
      const t = up(e.proj_issue || c.issue);
      if (t) return { target: t, kind: "collector" };
      // kein Sammelticket vorhanden → legt das System selbst eines an (je Projekt, im Jira-Projekt für neue Tickets)
      return c.newProject && e.project_id ? { target: "NEU:" + c.newProject, kind: "create-collector" } : { target: "", kind: "none" };
    };
    if (KEY_RE.test(up(e.tempo_target))) return { target: up(e.tempo_target), kind: "ticket" };
    // Eigenes Jira-Ticket (kein Plane-Work-Item) → immer auf das Ticket selbst, nie aufs Sammel-/Übergangs-Ticket –
    // auch wenn das Projekt mit Plane verknüpft ist (v6.27.1)
    if (e.task_id && !e.plane_work_item_id) {
      const k = up(e.ticket);
      const jk = KEY_RE.test(up(e.jira_link)) ? up(e.jira_link) : KEY_RE.test(up(e.jira_origin)) ? up(e.jira_origin)
        : KEY_RE.test(k) && (e.ticket_system === "jira" || c.jiraKeys.has(k.split("-")[0])) ? k : null;
      if (jk) return { target: jk, kind: "ticket" };
    }
    // Plane-Buchung → Übergangs-Ticket des Monats (alle Plane-Infos stehen im Worklog und in der Ticket-Beschreibung)
    if (c.planeMode === "month" && isPlaneBooking(e)) {
      const mt = monthTicketOf(monthOf(e.date), c);
      if (e.tempo_push_id && e.tempo_push_target && e.tempo_push_target !== mt) return { target: up(e.tempo_push_target), kind: "ticket" };   // früher anders übertragen → dort lassen
      if (mt) return { target: mt, kind: "month" };
      return c.newProject && c.worker ? { target: `NEU:${c.newProject} Monat ${monthOf(e.date)}`, kind: "create-month" } : { target: "", kind: "none" };
    }
    if (!e.task_id) return fallback();
    if (KEY_RE.test(up(e.jira_link))) return { target: up(e.jira_link), kind: "ticket" };
    if (KEY_RE.test(up(e.jira_origin))) return { target: up(e.jira_origin), kind: "ticket" };
    if (e.plane_work_item_id) {
      const mapped = one("SELECT jira_key FROM ticket_map WHERE UPPER(plane_key)=? AND status='mapped' LIMIT 1", up(e.ticket))?.jira_key;   // aus der Tempo-Zuordnung
      if (mapped) return { target: up(mapped), kind: "ticket" };
      return c.newProject && KEY_RE.test(up(e.ticket)) ? { target: "NEU:" + c.newProject, kind: "create" } : fallback();
    }
    const k = up(e.ticket);
    if (KEY_RE.test(k) && (e.ticket_system === "jira" || c.jiraKeys.has(k.split("-")[0]))) return { target: k, kind: "ticket" };
    return fallback();
  }
  function isPlaneBooking(e) {
    if (e.plane_work_item_id) return true;
    return !!(hooks?.sources && e.project_id && hooks.sources.allowsPlane(e.project_id));
  }
  const monthTicketOf = (month, c) => one("SELECT issue_key FROM tempo_month_tickets WHERE month=? AND worker=?", month, c.worker || "")?.issue_key || null;
  const planeLink = (key, c) => c.planeUrl && KEY_RE.test(key) ? `${c.planeUrl}/browse/${key}/` : "";
  function monthComment(e, c) {
    const key = String(e.ticket || "").trim().toUpperCase();
    const link = planeLink(key, c);
    return [describe(e), e.project_name ? `· ${e.project_name}` : "", link ? `· Plane: ${link}` : ""].filter(Boolean).join(" ").slice(0, 900);
  }
  function plan(e, c) {
    const { target, kind } = targetOf(e, c);
    const secs = Math.max(60, Math.round((e.duration_seconds || 0) / 60) * 60);
    const started = localStamp(e.start_time);
    // auf dem eigenen Ticket genügt Titel/Kommentar; auf dem Sammelticket steht der Bezug vorne
    const comment = kind === "month" || kind === "create-month" ? monthComment(e, c) : kind === "collector" ? describe(e)
      : ((e.comment || "").trim() || (e.task_title || "").trim() || "Arbeit").replace(/\s+/g, " ").slice(0, 900);
    // Account: 1. für diese Buchung gewählt · 2. am Projekt hinterlegt · 3. auf echtem Jira-Ticket: der Account des Tickets (Tempo setzt ihn selbst) · 4. Standard
    const own = kind === "ticket";
    const account = String(e.tempo_account || e.proj_account || (own ? "" : c.account) || "").trim();
    const account_from = e.tempo_account ? "Buchung" : e.proj_account ? "Projekt" : own ? "Ticket" : c.account ? "Standard" : "";
    // alter Hash bleibt gleich (früher wurde der Standard-Account immer mitgeschickt) → keine Massen-„Geändert“
    const hash = crypto.createHash("sha1").update(JSON.stringify([target, started, secs, comment, account || (own ? String(c.account || "").trim() : "")])).digest("hex").slice(0, 16);
    return { target, kind, secs, started, comment, account, account_from, hash };
  }
  const LOCK_RE = /period|periode|zeitraum|gesperrt|locked|approved|genehmigt|submitted|eingereicht|frozen/i;
  const monthOf = d => String(d).slice(0, 7);
  function lockedMonths() { return Object.fromEntries(all("SELECT * FROM tempo_months WHERE locked=1").map(r => [r.month, r])); }
  function statusOf(e, p, locks = {}) {
    if (e.tempo_skip) return "skipped";
    if (!e.tempo_push_id && hooks?.sources && !hooks.sources.allowsTempo(e.project_id)) return "norule";
    if (!e.tempo_push_id && e.check_flags && !e.check_note) return "review";   // Plausibilität: erst bestätigen   // Projekt-Regel: nicht nach Tempo
    if (e.tempo_push_id && e.tempo_push_hash === p.hash) return "synced";
    if (locks[monthOf(e.date)] || (e.tempo_push_error && LOCK_RE.test(e.tempo_push_error) && /HTTP 4\d\d/.test(e.tempo_push_error))) return "locked";
    if (!e.tempo_push_id && e.tempo_dup && e.tempo_dup !== "ok") return "dup";
    if (!p.target) return "needs";
    if (!e.tempo_push_id) return e.tempo_push_error ? "error" : "new";
    if (e.tempo_push_hash !== p.hash) return "changed";
    return "synced";
  }
  function candidates(c, extra = "1=1", ...params) {
    return rows(`e.end_time IS NOT NULL AND COALESCE(e.duration_seconds,0)>0 AND COALESCE(e.source,'manual') NOT IN ('tempo','tempo_csv')
                 AND e.date>=? AND ${extra} ORDER BY e.start_time`, c.since, ...params);
  }
  const idOf = r => { const x = Array.isArray(r) ? r[0] : r; return x && (x.tempoWorklogId ?? x.id ?? x.worklogId); };

  /* ── Monat prüfen: Sperre (Freigabe/Periodenabschluss) + schon vorhandene Worklogs ── */
  const lastDay = m => { const [y, mo] = m.split("-").map(Number); return `${m}-${pad(new Date(y, mo, 0).getDate())}`; };
  async function checkMonth(month, c) {
    const from = month + "-01", to = lastDay(month);
    let locked = 0, reason = null, remote = null;
    // 1. Stundenzettel-Freigabe (eingereicht/genehmigt = gesperrt)
    try {
      const a = await request("GET", `/rest/tempo-timesheets/4/timesheet-approval/current?periodStartDate=${from}&userKey=${encodeURIComponent(c.workerKey)}`);
      const key = String(a?.status?.key || a?.status || "").toLowerCase();
      if (/approved|waiting|submitted/.test(key)) { locked = 1; reason = key.includes("approved") ? "Stundenzettel genehmigt" : "Stundenzettel eingereicht"; }
    } catch (_) {}
    // 2. Was steht in Tempo schon? (Doppelte vermeiden – z. B. früher von Hand gebucht)
    try {
      const wl = await request("POST", "/rest/tempo-timesheets/4/worklogs/search", { from, to, worker: [c.workerKey] });
      const own = new Set(all("SELECT tempo_push_id FROM time_entries WHERE tempo_push_id IS NOT NULL").map(r => String(r.tempo_push_id)));
      const others = (Array.isArray(wl) ? wl : []).filter(w => !own.has(String(w.tempoWorklogId ?? w.id)))
        .map(w => { const s = Date.parse(String(w.started).replace(" ", "T")); return { id: w.tempoWorklogId ?? w.id, s, e: s + (w.timeSpentSeconds || 0) * 1000, key: w.issue?.key || w.originTaskId || "", text: w.comment || "" }; })
        .filter(w => Number.isFinite(w.s));
      remote = others.length;
      for (const e of candidates(c, "e.date BETWEEN ? AND ? AND e.tempo_push_id IS NULL AND COALESCE(e.tempo_dup,'')<>'ok'", from, to)) {
        const s = Date.parse(e.start_time), en = Date.parse(e.end_time);
        const hit = others.find(w => Math.max(0, Math.min(en, w.e) - Math.max(s, w.s)) >= 0.5 * Math.min(en - s, w.e - w.s));
        run("UPDATE time_entries SET tempo_dup=? WHERE id=?", hit ? `${hit.key} ${hm(hit.s)}–${hm(hit.e)} ${String(hit.text).slice(0, 80)}`.trim() : null, e.id);
      }
    } catch (err) { if ([401, 403].includes(err.status)) { locked = locked || 0; } }
    run("INSERT OR REPLACE INTO tempo_months (month,locked,reason,remote_count,checked_at) VALUES (?,?,?,?,datetime('now'))", month, locked, reason, remote);
    return { month, locked: !!locked, reason, remote };
  }
  const hm = ms => { const d = new Date(ms); return `${pad(d.getHours())}:${pad(d.getMinutes())}`; };
  function lockFromError(month, msg) {
    run("INSERT INTO tempo_months (month,locked,reason,checked_at) VALUES (?,1,?,datetime('now')) ON CONFLICT(month) DO UPDATE SET locked=1, reason=excluded.reason, checked_at=excluded.checked_at",
      month, "Tempo lehnt ab: " + String(msg).slice(0, 160));
  }

  /* ══ Ticket-Vorschläge: welches Jira-Ticket passt zu dieser Buchung? ══════════
     Lernt aus der eigenen Tempo-Historie (importierte Worklogs: Ticket, Titel, Kommentare,
     Projekt, Häufigkeit, Aktualität), aus „Meine Jira-Tickets“ und der Tempo→Plane-Zuordnung;
     auf Wunsch zusätzlich Volltextsuche in Jira. Nichts wird ungefragt übernommen. */
  const { toks, textSim } = require("./textmatch");
  let corpusCache = { at: 0, data: null };
  function buildCorpus() {
    if (corpusCache.data && Date.now() - corpusCache.at < 5 * 60e3) return corpusCache.data;
    const by = new Map();
    const add = (k, title, pid, comment, date) => {
      k = String(k || "").toUpperCase(); if (!KEY_RE.test(k)) return;
      const o = by.get(k) || { key: k, title: "", comments: [], projects: new Map(), count: 0, last: "" };
      if (title && (!o.title || o.title.toUpperCase() === k)) o.title = title;
      if (comment && o.comments.length < 40 && !o.comments.includes(comment)) o.comments.push(comment);
      if (pid) o.projects.set(pid, (o.projects.get(pid) || 0) + 1);
      if (date) { o.count++; if (date > o.last) o.last = date; }
      by.set(k, o);
    };
    for (const r of all(`SELECT COALESCE(t.jira_link, t.jira_origin, t.jira_ticket) AS k, t.title, e.project_id, e.comment, e.date FROM time_entries e JOIN tasks t ON t.id=e.task_id
                         WHERE e.source IN ('tempo','tempo_csv') AND e.date >= date('now','-400 day')`)) add(r.k, r.title, r.project_id, (r.comment || "").trim(), r.date);
    for (const r of all(`SELECT jira_link AS k, title, project_id FROM tasks WHERE jira_link IS NOT NULL AND (jira_mine=1 OR ticket_system='jira')`)) add(r.k, r.title, r.project_id, null, null);
    for (const r of all(`SELECT jira_key AS k, title FROM ticket_map WHERE status='mapped'`)) add(r.k, r.title, null, null, null);
    corpusCache = { at: Date.now(), data: [...by.values()] };
    return corpusCache.data;
  }
  const entryText = e => [e.task_title, e.comment].filter(Boolean).join(" ");
  function suggestLocal(e, corpus) {
    const txt = entryText(e); const today = Date.now();
    const out = [];
    for (const c of corpus) {
      const simTitle = textSim(txt, c.title);
      let simCom = 0; for (const cm of c.comments) { const v = textSim(txt, cm); if (v > simCom) simCom = v; if (simCom > .95) break; }
      const sim = Math.max(simTitle, simCom * 0.9);
      const projHits = e.project_id ? (c.projects.get(e.project_id) || 0) : 0;
      if (sim < 0.2 && !projHits) continue;
      const freq = Math.min(1, Math.log(1 + c.count) / Math.log(30));
      const days = c.last ? (today - Date.parse(c.last + "T12:00:00")) / 864e5 : 999;
      const rec = days < 400 ? Math.max(0, 1 - days / 120) : 0;
      const score = 0.6 * sim + (projHits ? 0.22 : 0) + 0.1 * freq + 0.08 * rec;
      if (score < 0.3) continue;
      const reasons = [];
      if (sim >= 0.3) reasons.push(simTitle >= simCom * 0.9 ? `Titel ähnlich (${Math.round(simTitle * 100)} %)` : `ähnlicher Tempo-Kommentar`);
      if (projHits) reasons.push(`im selben Projekt ${projHits}× gebucht`);
      else if (c.count) reasons.push(`${c.count}× gebucht`);
      if (c.last && days < 30) reasons.push("kürzlich");
      out.push({ key: c.key, summary: c.title && c.title.toUpperCase() !== c.key ? c.title : "", score: Math.round(Math.min(1, score) * 100), reasons, source: "historie" });
    }
    return out.sort((a, b) => b.score - a.score).slice(0, 5);
  }
  async function suggestRemote(e, c) {
    const words = [...new Set(toks(entryText(e)))].slice(0, 4);
    if (!words.length) return [];
    const proj = e.project_id ? one("SELECT jira_project FROM projects WHERE id=?", e.project_id)?.jira_project : null;
    const jql = `text ~ "${words.join(" ")}"${proj ? ` AND project = "${proj}"` : ""} ORDER BY updated DESC`;
    let r; try { r = await request("GET", `/rest/api/2/search?maxResults=8&fields=summary,project,status&jql=${encodeURIComponent(jql)}`); } catch (_) { return []; }
    return (r?.issues || []).map(i => {
      const sim = textSim(entryText(e), i.fields?.summary || "");
      const score = Math.round(Math.min(1, 0.7 * sim + (proj ? 0.2 : 0.05) + 0.05) * 100);
      return { key: i.key, summary: i.fields?.summary || "", score, reasons: [`Jira-Suche${proj ? ` in ${proj}` : ""}`, `Titel ähnlich (${Math.round(sim * 100)} %)`], source: "jira" };
    });
  }
  const wrapS = fn => (req, res) => Promise.resolve(fn(req, res)).catch(e => res.status(e.status && e.status < 500 ? 400 : 500).json({ error: e.message }));
  app.post("/api/integrations/tempo-push/suggest", wrapS(async (req, res) => {
    const c = cfg(); const ids = (req.body?.ids || []).map(Number).slice(0, 40);
    const rowsAll = ids.length ? rows(`e.id IN (${ids.map(() => "?").join(",")})`, ...ids) : [];
    const corpus = buildCorpus(); const out = {}; const seenTask = new Map();
    for (const e of rowsAll) {
      const tkey = e.task_id ? "t" + e.task_id : "e" + e.id;
      if (seenTask.has(tkey)) { out[e.id] = seenTask.get(tkey); continue; }
      let list = suggestLocal(e, corpus);
      if (req.body?.remote !== false && ready(c) && (!list.length || list[0].score < 70)) {
        const more = await suggestRemote(e, c);
        for (const m of more) { const ex = list.find(x => x.key === m.key); if (ex) { ex.score = Math.min(99, Math.max(ex.score, m.score) + 8); ex.reasons.push("auch in Jira gefunden"); } else list.push(m); }
        list.sort((a, b) => b.score - a.score);
      }
      out[e.id] = list.slice(0, 5); seenTask.set(tkey, out[e.id]);
    }
    res.json({ suggestions: out });
  }));
  /* Ziel festlegen: scope task = für die Aufgabe (alle ihre Buchungen), entry = nur diese Buchungen, project = Sammelticket des Projekts */
  app.post("/api/integrations/tempo-push/target", wrapS(async (req, res) => {
    const key = String(req.body?.key || "").trim().toUpperCase(), scope = req.body?.scope || "task";
    const ids = (req.body?.ids || []).map(Number);
    if (!KEY_RE.test(key)) return res.status(400).json({ error: "Ticket-Key im Format ABC-123" });
    let issue; try { issue = await request("GET", `/rest/api/2/issue/${encodeURIComponent(key)}?fields=summary`); }
    catch (e) { return res.status(400).json({ error: `${key} in Jira nicht gefunden: ${e.message}` }); }
    let n = 0;
    for (const e of rows(`e.id IN (${ids.map(() => "?").join(",") || "NULL"})`, ...ids)) {
      if (scope === "task" && e.task_id) { run("UPDATE tasks SET jira_link=? WHERE id=? OR (plane_work_item_id IS NOT NULL AND plane_work_item_id=?)", key, e.task_id, e.plane_work_item_id || "-"); n++; }
      else if (scope === "project" && e.project_id) { run("UPDATE projects SET tempo_issue=? WHERE id=?", key, e.project_id); n++; }
      else { run("UPDATE time_entries SET tempo_target=?, tempo_push_error=NULL WHERE id=?", key, e.id); n++; }
    }
    corpusCache.at = 0;
    writeLog("info", LOG, `Tempo-Ziel ${key} (${issue?.fields?.summary || ""}) für ${n} Buchung(en) festgelegt (${scope})`);
    res.json({ ok: true, key, summary: issue?.fields?.summary || "", n });
  }));

  function saveMe(me) {
    if (me?.name) run("INSERT OR REPLACE INTO settings (key,value) VALUES ('jira_username',?)", me.name);
    if (me?.key) run("INSERT OR REPLACE INTO settings (key,value) VALUES ('jira_userkey',?)", me.key);
  }
  async function ensureMe(c) {
    if (getSettings().jira_userkey) return c;
    try { saveMe(await request("GET", "/rest/api/2/myself")); } catch (_) {}
    return cfg();
  }

  /* ── Tempo-Account als Pflichtfeld beim Anlegen von Jira-Tickets ──
     Manche Jira-Projekte verlangen beim Anlegen das Tempo-Feld „Account“ (customfield_…).
     Wert: Standard-Account des Jira-Projekts (Tempo-Verknüpfung), sonst der eingestellte Tempo-Account. */
  const accCache = {};
  async function accountIdFor(projectKey, c) {
    if (accCache[projectKey] !== undefined) return accCache[projectKey];
    let id = null;
    try {
      const pr = await request("GET", `/rest/api/2/project/${encodeURIComponent(projectKey)}`);
      const links = await request("GET", `/rest/tempo-accounts/1/link/project/${pr.id}`);
      const l = (Array.isArray(links) ? links : []).find(x => x.defaultAccount) || (Array.isArray(links) ? links : [])[0];
      id = l?.accountId || l?.account?.id || null;
    } catch (_) {}
    if (!id && c.account) {
      try { const a = await request("GET", `/rest/tempo-accounts/1/account/key/${encodeURIComponent(c.account)}`); id = a?.id || null; } catch (_) {}
      if (!id) try { const all_ = await request("GET", "/rest/tempo-accounts/1/account"); id = (all_ || []).find(a => a.key === c.account)?.id || null; } catch (_) {}
    }
    return (accCache[projectKey] = id);
  }
  const accKeyCache = {};
  async function accountKeyFor(projectKey, c) {
    if (accKeyCache[projectKey] !== undefined) return accKeyCache[projectKey];
    let key = null; const id = await accountIdFor(projectKey, { ...c, account: "" });
    if (id) try { const a = await request("GET", `/rest/tempo-accounts/1/account/${id}`); key = a?.key || null; } catch (_) {}
    return (accKeyCache[projectKey] = key || c.account || null);
  }
  /* Ticket anlegen; meldet Jira „Account is required“ für ein Feld, wird der Account gesetzt und erneut versucht */
  async function createIssue(fields, c) {
    const tries = [{ ...fields, assignee: { name: c.worker } }, fields];
    let lastErr;
    for (const f of tries) {
      try { return await request("POST", "/rest/api/2/issue", { fields: f }); }
      catch (err) {
        lastErr = err;
        const m = String(err.message).match(/"(customfield_\d+)"\s*:\s*"[^"]*account[^"]*"/i);
        if (err.status === 400 && m) {
          const id = await accountIdFor(fields.project.key, c);
          if (!id) throw new Error(`Jira verlangt einen Tempo-Account für Tickets in ${fields.project.key} – bitte unter Tempo-Übertragung „Automatisch einrichten“ einen Standard-Account wählen`);
          for (const v of [Number(id), String(id), { id: String(id) }]) {
            try { return await request("POST", "/rest/api/2/issue", { fields: { ...f, [m[1]]: v } }); }
            catch (e2) { lastErr = e2; if (e2.status !== 400) throw e2; }
          }
          continue;
        }
        if (err.status !== 400) throw err;
      }
    }
    throw lastErr;
  }

  /* ── Jira-Ticket für ein Plane-Ticket anlegen (oder wiederfinden) ── */
  async function ensureJira(e, c) {
    const known = one("SELECT jira_link FROM tasks WHERE id=? AND jira_link IS NOT NULL", e.task_id)?.jira_link;
    if (known) return known;
    const planeKey = String(e.ticket || "").trim().toUpperCase();
    const title = String(e.task_title || planeKey).trim();
    const summary = `[${planeKey}] ${title.toUpperCase() === planeKey ? "" : title}`.trim().slice(0, 250);
    let key = null;
    try {
      const jql = `project = "${c.newProject}" AND summary ~ "\\"[${planeKey}]\\""`;
      const r = await request("GET", `/rest/api/2/search?maxResults=5&fields=summary&jql=${encodeURIComponent(jql)}`);
      const hit = (r?.issues || []).find(i => String(i.fields?.summary || "").toUpperCase().startsWith(`[${planeKey}]`));
      if (hit) key = hit.key;
    } catch (_) {}
    if (!key) {
      const fields = { project: { key: c.newProject }, summary, issuetype: { name: c.issueType },
        description: `Gegenstück zum Plane-Ticket ${planeKey}. Angelegt von Zeitprotokoll für die Zeitbuchung in Tempo.` };
      const r = await createIssue(fields, c);
      key = r?.key;
      if (!key) throw new Error("Jira hat keinen Ticket-Key geliefert");
      writeLog("success", LOG, `Jira-Ticket ${key} für Plane ${planeKey} angelegt`); justCreated = true;
    }
    run("UPDATE tasks SET jira_link=? WHERE plane_work_item_id=? OR id=?", key, e.plane_work_item_id, e.task_id);
    return key;
  }

  /* Sammelticket für ein Projekt anlegen (oder wiederfinden) und am Projekt merken */
  async function ensureCollector(e, c) {
    const fresh = one("SELECT tempo_issue FROM projects WHERE id=?", e.project_id)?.tempo_issue;
    if (fresh) return fresh;
    const name = String(e.project_name || "Allgemein").trim();
    const summary = `Zeitprotokoll: ${name} – Sammelbuchungen`.slice(0, 250);
    let key = null;
    try {
      const jql = `project = "${c.newProject}" AND summary ~ "\\"${summary.replace(/["\\]/g, " ")}\\""`;
      const r = await request("GET", `/rest/api/2/search?maxResults=5&fields=summary&jql=${encodeURIComponent(jql)}`);
      key = (r?.issues || []).find(i => String(i.fields?.summary || "") === summary)?.key || null;
    } catch (_) {}
    if (!key) {
      const fields = { project: { key: c.newProject }, summary, issuetype: { name: c.issueType },
        description: `Sammelticket für Zeitbuchungen ohne eigenes Ticket im Projekt „${name}“. Angelegt von Zeitprotokoll.` };
      const r = await createIssue(fields, c);
      key = r?.key; if (!key) throw new Error("Jira hat keinen Ticket-Key geliefert");
      writeLog("success", LOG, `Sammelticket ${key} für Projekt „${name}“ angelegt`); justCreated = true;
    }
    run("UPDATE projects SET tempo_issue=? WHERE id=?", key, e.project_id);
    return key;
  }

  /* ══ Übergangs-Ticket je Person und Monat ══════════════════════════════════
     „Übergang Zeiten user01 2026-10“ – beim ersten Buchen des Monats angelegt (dir zugewiesen),
     Beschreibung listet alle Plane-Tickets mit Zeit, geschlossen nach Monatssperre bzw. Nachfrist. */
  async function ensureMonthTicket(month, c) {
    const known = monthTicketOf(month, c); if (known) return known;
    const summary = `Übergang Zeiten ${c.worker} ${month}`;
    let key = null;
    try {
      const jql = `project = "${c.newProject}" AND summary ~ "\\"${summary}\\""`;
      const r = await request("GET", `/rest/api/2/search?maxResults=5&fields=summary&jql=${encodeURIComponent(jql)}`);
      key = (r?.issues || []).find(i => String(i.fields?.summary || "").trim() === summary)?.key || null;
    } catch (_) {}
    if (!key) {
      const fields = { project: { key: c.newProject }, summary, issuetype: { name: c.issueType },
        description: `Übergangs-Ticket für Zeiten von ${c.worker} im Monat ${month}, die in Plane gebucht wurden.\nJeder Worklog nennt das Plane-Ticket, den Titel, das Projekt und den Link; die Tabelle unten wird von Zeitprotokoll aktuell gehalten.\nDer Kunde steht im Tempo-Account des Worklogs. Wird nach der Monatssperre automatisch geschlossen.` };
      const r = await createIssue(fields, c);
      key = r?.key; if (!key) throw new Error("Jira hat keinen Ticket-Key geliefert");
      writeLog("success", LOG, `Übergangs-Ticket ${key} für ${month} angelegt`); justCreated = true;
    }
    run("INSERT OR REPLACE INTO tempo_month_tickets (month,worker,issue_key) VALUES (?,?,?)", month, c.worker, key);
    return key;
  }
  /* Beschreibung: Tabelle aller Plane-Tickets des Monats (Ticket, Titel, Projekt, Zeit, Link) */
  async function updateMonthDescription(month, c) {
    const mt = one("SELECT * FROM tempo_month_tickets WHERE month=? AND worker=?", month, c.worker); if (!mt) return;
    const list = rows("e.tempo_push_target=? AND e.date BETWEEN ? AND ? ORDER BY e.date", mt.issue_key, month + "-01", lastDay(month));
    if (!list.length) return;
    const by = new Map();
    for (const e of list) {
      const k = String(e.ticket || "").trim().toUpperCase() || "(ohne Ticket)";
      const x = by.get(k) || { key: k, title: e.task_title || "", project: e.project_name || "", secs: 0, n: 0, last: e.date };
      x.secs += e.duration_seconds || 0; x.n++; x.last = e.date; by.set(k, x);
    }
    const h = s => (Math.round(s / 360) / 10).toLocaleString("de-DE");
    const esc = t => String(t || "").replace(/[|[\]{}]/g, " ").trim();
    const total = list.reduce((a, e) => a + (e.duration_seconds || 0), 0);
    const desc = [`Übergangs-Ticket für Zeiten von ${c.worker} im Monat ${month}, die in Plane gebucht wurden. Wird von Zeitprotokoll aktuell gehalten – bitte nicht von Hand ändern.`,
      "", `*Summe: ${h(total)} h in ${list.length} Buchungen*`, "", "||Plane-Ticket||Titel||Projekt||Stunden||Buchungen||zuletzt||",
      ...[...by.values()].sort((a, b) => b.secs - a.secs).map(x => {
        const link = planeLink(x.key, c);
        return `|${link ? `[${x.key}|${link}]` : esc(x.key)}|${esc(x.title) || " "}|${esc(x.project) || " "}|${h(x.secs)}|${x.n}|${x.last}|`;
      })].join("\n");
    const hash = crypto.createHash("sha1").update(desc).digest("hex").slice(0, 16);
    if (hash === mt.desc_hash) return;
    try { await request("PUT", `/rest/api/2/issue/${encodeURIComponent(mt.issue_key)}`, { fields: { description: desc } });
          run("UPDATE tempo_month_tickets SET desc_hash=? WHERE month=? AND worker=?", hash, month, c.worker); }
    catch (err) { writeLog("warn", LOG, `Beschreibung von ${mt.issue_key} nicht aktualisiert: ${err.message}`); }
  }
  /* Schließen: wenn Tempo den Monat gesperrt hat – oder nach der Nachfrist, sofern nichts mehr offen ist */
  const DONE_RE = /erledigt|fertig|done|closed|geschlossen|resolved|abgeschlossen/i;
  async function closeMonthTickets(c) {
    const today = localDateOf(Date.now()), cur = today.slice(0, 7), out = [];
    for (const mt of all("SELECT * FROM tempo_month_tickets WHERE worker=? AND closed_at IS NULL AND month<?", c.worker || "", cur)) {
      const locked = !!one("SELECT 1 FROM tempo_months WHERE month=? AND locked=1", mt.month);
      const d = new Date(lastDay(mt.month) + "T12:00:00"); d.setDate(d.getDate() + c.closeDays);
      const graceOver = today > localDateOf(d.getTime());
      if (!locked && !graceOver) continue;
      const open = candidates(c, "e.date BETWEEN ? AND ? AND COALESCE(e.tempo_skip,0)=0", mt.month + "-01", lastDay(mt.month))
        .filter(e => { const p = plan(e, c); return (p.kind === "month" || p.kind === "create-month") && ["new", "changed", "error", "needs"].includes(statusOf(e, p, {})); });
      if (!locked && open.length) continue;          // Nachtrag wartet noch → erst übertragen
      try {
        await updateMonthDescription(mt.month, c);
        const t = await request("GET", `/rest/api/2/issue/${encodeURIComponent(mt.issue_key)}/transitions`);
        const tr = (t?.transitions || []).find(x => x.to?.statusCategory?.key === "done") || (t?.transitions || []).find(x => DONE_RE.test(x.to?.name || "") || DONE_RE.test(x.name || ""));
        if (!tr) throw new Error("kein Übergang nach „Erledigt“ gefunden");
        await request("POST", `/rest/api/2/issue/${encodeURIComponent(mt.issue_key)}/transitions`, { transition: { id: tr.id } });
        run("UPDATE tempo_month_tickets SET closed_at=datetime('now'), close_error=NULL WHERE month=? AND worker=?", mt.month, c.worker);
        writeLog("success", LOG, `Übergangs-Ticket ${mt.issue_key} (${mt.month}) geschlossen – ${locked ? "Monat in Tempo gesperrt" : `${c.closeDays} Tage Nachfrist vorbei`}`);
        out.push(mt.issue_key);
      } catch (err) {
        if (mt.close_error !== err.message) writeLog("warn", LOG, `${mt.issue_key} nicht geschlossen: ${err.message}`);
        run("UPDATE tempo_month_tickets SET close_error=? WHERE month=? AND worker=?", err.message, mt.month, c.worker);
      }
    }
    return out;
  }

  /* Lokale Aufgabe zu einem Jira-Ticket finden oder anlegen (Projekt über Verknüpfung bzw. Projektschlüssel) */
  function ensureTaskForIssue(i, mine = false) {
    const key = String(i.key).toUpperCase(), pkey = String(i.fields?.project?.key || key.split("-")[0]).toUpperCase();
    const summary = String(i.fields?.summary || key).slice(0, 240);
    let t = one("SELECT * FROM tasks WHERE UPPER(jira_link)=? OR UPPER(jira_origin)=? ORDER BY id LIMIT 1", key, key)
         || one("SELECT * FROM tasks WHERE UPPER(TRIM(jira_ticket))=? AND plane_work_item_id IS NULL ORDER BY id LIMIT 1", key);
    if (t) {
      run(`UPDATE tasks SET jira_link=COALESCE(jira_link,?), jira_mine=CASE WHEN ? THEN 1 ELSE jira_mine END,
             status=CASE WHEN status='done' AND ticket_system='jira' THEN 'open' ELSE status END WHERE id=?`, key, mine ? 1 : 0, t.id);
      return { key, task_id: t.id, project_id: t.project_id, created: false };
    }
    let projectCreated = false;
    const pid = hooks?.sources ? hooks.sources.projectByJiraKey(pkey)
      : (one("SELECT id FROM projects WHERE UPPER(jira_project)=? ORDER BY id LIMIT 1", pkey) || one("SELECT id FROM projects WHERE UPPER(jira_key)=? ORDER BY id LIMIT 1", pkey))?.id;
    let p = pid ? { id: pid } : null;
    if (!p) {
      const r = run("INSERT INTO projects (name,jira_key,status) VALUES (?,?,'active')", String(i.fields?.project?.name || pkey).slice(0, 120), pkey); p = { id: Number(r.lastInsertRowid) }; projectCreated = true;
      // gleich als Jira-Quelle verknüpfen, damit es nicht zusätzlich unter „Neue Projekte“ auftaucht
      if (hooks?.sources) hooks.sources.linkBy("jira", pkey, p.id, pkey, i.fields?.project?.name); else run("UPDATE projects SET jira_project=? WHERE id=?", pkey, p.id);
    }
    const clash = one("SELECT 1 FROM tasks WHERE project_id=? AND UPPER(TRIM(jira_ticket))=?", p.id, key);   // gleicher Key existiert als Plane-Ticket
    const r = run("INSERT INTO tasks (project_id,title,jira_ticket,status,jira_link,ticket_system,jira_mine) VALUES (?,?,?,'open',?,'jira',?)", p.id, (clash ? "Jira: " : "") + summary, key, key, mine ? 1 : 0);
    return { key, task_id: Number(r.lastInsertRowid), project_id: p.id, created: true, projectCreated };
  }

  /* ── Meine Jira-Tickets in die Auswahl holen ── */
  let lastMine = null;
  async function syncMyTickets() {
    const c = cfg();
    if (!ready(c)) throw Object.assign(new Error("Jira-Adresse und Token fehlen"), { status: 400 });
    try {                                                                          // Jira-Projektschlüssel merken (Erkennung „ist ein Jira-Key“)
      const ps = await request("GET", "/rest/api/2/project");
      if (Array.isArray(ps)) run("INSERT OR REPLACE INTO settings (key,value) VALUES ('jira_project_keys',?)", ps.map(p => p.key).join(","));
    } catch (_) {}
    const issues = [];
    for (let at = 0; at < 1000; at += 100) {
      const r = await request("GET", `/rest/api/2/search?startAt=${at}&maxResults=100&fields=summary,project,status&jql=${encodeURIComponent(c.jql)}`);
      issues.push(...(r?.issues || []));
      if (!r || at + 100 >= (r.total || 0)) break;
    }
    const seen = new Set(); let created = 0, projectsNew = 0;
    let mutedSkip = 0;
    for (const i of issues) {
      const pk = String(i.fields?.project?.key || String(i.key).split("-")[0]).toUpperCase();
      // stumm geschaltete Jira-Projekte (ignoriert bzw. Projekt stumm) nicht in die Auswahl holen
      if (one("SELECT 1 FROM project_sources WHERE system='jira' AND external_id=? AND status='ignored'", pk)
          || one("SELECT 1 FROM project_sources s JOIN projects p ON p.id=s.project_id WHERE s.system='jira' AND s.external_id=? AND COALESCE(p.muted,0)=1", pk)) { mutedSkip++; continue; }
      const r = ensureTaskForIssue(i, true);
      seen.add(r.key); if (r.created) created++; if (r.projectCreated) projectsNew++;
    }
    // nicht mehr mir zugewiesen / erledigt → aus der Auswahl nehmen (Buchungen bleiben)
    let gone = 0;
    for (const t of all("SELECT id, jira_link, ticket_system FROM tasks WHERE jira_mine=1")) {
      if (seen.has(String(t.jira_link || "").toUpperCase())) continue;
      run("UPDATE tasks SET jira_mine=0, status=CASE WHEN ticket_system='jira' THEN 'done' ELSE status END WHERE id=?", t.id); gone++;
    }
    lastMine = { at: new Date().toISOString(), count: issues.length, created, projects_new: projectsNew, gone };
    writeLog("info", LOG, `Meine Jira-Tickets: ${issues.length} gefunden, ${created} neu${projectsNew ? `, ${projectsNew} Projekt(e) angelegt` : ""}${gone ? `, ${gone} nicht mehr zugewiesen` : ""}`);
    return lastMine;
  }

  /* ── Übertragen ─────────────────────────────────────────────────── */
  async function push({ ids, before } = {}) {
    const c = cfg();
    if (!c.enabled) { const e = new Error("Tempo-Übertragung ist ausgeschaltet (Einstellungen › Jira & Tempo)"); e.status = 400; throw e; }
    if (!c.worker) { const e = new Error("Jira-Benutzername unbekannt – in Einstellungen › Jira & Tempo erst „Verbindung testen“"); e.status = 400; throw e; }
    if (pushing) return { busy: true };
    Object.assign(c, await ensureMe(c));
    pushing = true;
    const out = { ok: 0, errors: 0, deleted: 0, messages: [] };
    try {
      /* 0. Entwurfs-Tickets: lokale Aufgaben ohne Ticket jetzt in Plane anlegen (erst beim Freigeben), dann normal weiter */
      if (hooks?.plane?.linkTaskToPlane) {
        const idList = ids && ids.length ? ids.map(Number).filter(Number.isFinite) : null;
        const drafts = all(`SELECT DISTINCT t.id, t.title FROM time_entries e JOIN tasks t ON t.id=e.task_id
          WHERE e.end_time IS NOT NULL AND e.tempo_push_id IS NULL AND COALESCE(e.is_absence,0)=0
            AND (t.jira_ticket IS NULL OR TRIM(t.jira_ticket)='') AND t.plane_work_item_id IS NULL`
          + (idList ? ` AND e.id IN (${idList.join(",")})` : ""));
        for (const t of drafts) {
          try { const r = await hooks.plane.linkTaskToPlane(t.id); if (r?.jira_ticket) { out.drafts = (out.drafts || 0) + 1; writeLog("success", LOG, `Ticket „${t.title}“ in Plane angelegt: ${r.jira_ticket}`); } }
          catch (err) { out.messages.push(`Ticket „${t.title}“ nicht angelegt: ${err.message}`); }
        }
      }
      /* 1. vorgemerkte Löschungen */
      for (const d of all("SELECT * FROM tempo_push_deletions ORDER BY id")) {
        try { await request("DELETE", `/rest/tempo-timesheets/4/worklogs/${encodeURIComponent(d.worklog_id)}`).catch(err => { if (err.status !== 404) throw err; });
              run("DELETE FROM tempo_push_deletions WHERE id=?", d.id); out.deleted++; }
        catch (err) { run("UPDATE tempo_push_deletions SET error=? WHERE id=?", err.message, d.id); out.errors++; out.messages.push(err.message);
              // Löschung im gesperrten Zeitraum bleibt vorgemerkt, blockiert aber nicht die übrigen Buchungen
              if ([0, 401].includes(err.status) || (err.status === 403 && !LOCK_RE.test(err.message))) return out; }
      }
      /* 2. neue und geänderte Buchungen */
      let list = candidates(c);
      if (ids && ids.length) list = list.filter(e => ids.includes(e.id));
      if (before) list = list.filter(e => e.end_time && new Date(e.end_time).getTime() < before);
      // Monate vorher prüfen (höchstens alle 30 Minuten)
      const months = [...new Set(list.filter(e => !e.tempo_skip).map(e => monthOf(e.date)))];
      for (const m of months) {
        const r = one("SELECT checked_at FROM tempo_months WHERE month=?", m);
        if (!r || Date.now() - Date.parse(r.checked_at.replace(" ", "T") + "Z") > 30 * 60e3) await checkMonth(m, c).catch(() => {});
      }
      list = candidates(c).filter(e => list.some(x => x.id === e.id));       // tempo_dup neu laden
      let locks = lockedMonths();
      const touched = new Set();
      for (const e of list) {
        const p = plan(e, c), st = statusOf(e, p, locks);
        if (st === "synced" || st === "skipped" || st === "dup" || st === "norule") continue;
        if (st === "review") { out.review = (out.review || 0) + 1; continue; }
        if (st === "locked") { out.locked = (out.locked || 0) + 1; continue; }
        if (!p.target) { out.needs = (out.needs || 0) + 1; continue; }        // erst Ticket wählen (Vorschläge im Tempo-Ausgang)
        try {
          if (p.kind === "create") {                       // Plane-Ticket ohne Jira-Gegenstück → jetzt anlegen
            justCreated = false; const key = await ensureJira(e, c);
            Object.assign(e, { jira_link: key }); Object.assign(p, plan(e, c)); if (justCreated) out.created = (out.created || 0) + 1;
          } else if (p.kind === "create-month") {          // erste Plane-Buchung des Monats → Übergangs-Ticket anlegen
            justCreated = false; await ensureMonthTicket(monthOf(e.date), c);
            Object.assign(p, plan(e, c)); if (justCreated) out.created = (out.created || 0) + 1;
          } else if (p.kind === "create-collector") {      // Buchung ohne Ticket und noch kein Sammelticket → anlegen
            justCreated = false; const key = await ensureCollector(e, c);
            Object.assign(e, { proj_issue: key }); Object.assign(p, plan(e, c)); if (justCreated) out.created = (out.created || 0) + 1;
          }
          const body = { worker: c.workerKey, originTaskId: p.target, started: p.started, timeSpentSeconds: p.secs, comment: p.comment,
            ...(p.account ? { attributes: { _Account_: { name: "Account", workAttributeId: c.accountAttrId, value: p.account } } } : {}) };
          let id = e.tempo_push_id;
          const prevTarget = e.tempo_push_target || (id ? c.issue : null);
          if (id && prevTarget && prevTarget !== p.target) {   // anderes Ticket → alten Worklog löschen, neu anlegen
            await request("DELETE", `/rest/tempo-timesheets/4/worklogs/${encodeURIComponent(id)}`).catch(err => { if (err.status !== 404) throw err; });
            id = null;
          }
          if (id) {
            try { await request("PUT", `/rest/tempo-timesheets/4/worklogs/${encodeURIComponent(id)}`, body); }
            catch (err) { if (err.status !== 404) throw err; id = null; }     // in Tempo gelöscht → neu anlegen
          }
          if (!id) {
            let r;
            try { r = await request("POST", "/rest/tempo-timesheets/4/worklogs/", body); }
            catch (err) {
              // Tempo verlangt einen Account, aber keiner eingestellt → Standard-Account des Jira-Projekts nehmen
              if (err.status !== 400 || !/account/i.test(err.message) || p.account) throw err;
              const key = await accountKeyFor(p.target.split("-")[0], c);
              if (!key) throw new Error("Tempo verlangt einen Account – unter Tempo-Übertragung „Automatisch einrichten“ einen Standard-Account wählen");
              body.attributes = { _Account_: { name: "Account", workAttributeId: c.accountAttrId, value: key } };
              r = await request("POST", "/rest/tempo-timesheets/4/worklogs/", body);
            }
            id = idOf(r); if (!id) throw new Error("Tempo hat keine Worklog-ID geliefert");
          }
          run("UPDATE time_entries SET tempo_push_id=?, tempo_push_hash=?, tempo_push_target=?, tempo_push_synced_at=datetime('now'), tempo_push_error=NULL WHERE id=?", String(id), p.hash, p.target, e.id);
          if (p.kind === "month") touched.add(monthOf(e.date));
          out.ok++;
        } catch (err) {
          if (err.status === 400 && /originTaskId|issue not found/i.test(err.message) && p?.target && p.kind === "ticket")
            err.message = `Ticket ${p.target} gibt es in Jira nicht – ist es ein Plane-Ticket? Dann Work-Item verknüpfen oder per Stift ein Jira-Ticket wählen (${err.message})`;
          run("UPDATE time_entries SET tempo_push_error=? WHERE id=?", err.message, e.id);
          if ([400, 403].includes(err.status) && LOCK_RE.test(err.message)) {     // gesperrter Zeitraum → ganzen Monat überspringen
            lockFromError(monthOf(e.date), err.message); locks = lockedMonths(); out.locked = (out.locked || 0) + 1; continue;
          }
          out.errors++; out.messages.push(err.message);
          if ([0, 401, 403].includes(err.status)) break;           // Netz/Token/Rechte: nicht jede Buchung einzeln scheitern lassen
        }
        await sleep(120);
      }
      for (const m of touched) await updateMonthDescription(m, c);
      await closeMonthTickets(c).catch(() => {});
    } finally { pushing = false; }
    writeLog(out.errors ? "warn" : "success", LOG, `Tempo-Übertragung: ${out.ok} Worklogs, ${out.errors} Fehler, ${out.locked || 0} gesperrt, ${out.deleted} gelöscht${out.messages[0] ? " – " + out.messages[0] : ""}`);
    return out;
  }

  function scheduleAuto() {
    const c = cfg();
    if (!c.enabled || c.mode !== "auto" || !ready(c)) return;
    clearTimeout(autoTimer);
    autoTimer = setTimeout(() => push({ before: Date.now() - 5 * 60e3 }).catch(e => writeLog("warn", LOG, "Tempo (automatisch): " + e.message)), 60e3);
  }

  /* Eintrag wird gelöscht → Tempo-Worklog zum Löschen vormerken */
  function beforeDelete(entryId) {
    const e = one("SELECT * FROM time_entries WHERE id=?", entryId);
    if (e?.tempo_push_id)
      run("INSERT INTO tempo_push_deletions (worklog_id,label) VALUES (?,?)", e.tempo_push_id, `${e.date} ${(e.comment || "").slice(0, 60)}`);
  }

  /* ── Routen ─────────────────────────────────────────────────────── */
  const wrap = fn => (req, res) => Promise.resolve(fn(req, res)).catch(e => res.status(e.status && e.status < 500 ? 400 : 500).json({ error: e.message }));

  /* Verbindung + Buchungsticket prüfen, Jira-Benutzername merken */
  app.get("/api/integrations/tempo-push/status", wrap(async (_, res) => {
    const c = cfg();
    if (!ready(c)) return res.json({ ok: false, message: "Jira-Adresse und Token eintragen" });
    let me;
    try { me = await request("GET", "/rest/api/2/myself"); }
    catch (e) { return res.json({ ok: false, message: "Jira: " + e.message }); }
    saveMe(me);
    const out = { ok: true, user: me?.displayName || me?.name, message: `Jira OK – ${me?.displayName || me?.name}` };
    const targets = new Set([c.issue, ...all("SELECT tempo_issue FROM projects WHERE tempo_issue IS NOT NULL AND TRIM(tempo_issue)<>''").map(r => r.tempo_issue.trim().toUpperCase())].filter(Boolean));
    out.issues = [];
    for (const k of targets) {
      try { const i = await request("GET", `/rest/api/2/issue/${encodeURIComponent(k)}?fields=summary,status`); out.issues.push({ key: k, ok: true, summary: i?.fields?.summary || "" }); }
      catch (e) { out.issues.push({ key: k, ok: false, error: e.message }); out.ok = false; }
    }
    if (c.newProject) {
      try { const pr = await request("GET", `/rest/api/2/project/${encodeURIComponent(c.newProject)}`); out.new_project = { key: c.newProject, ok: true, name: pr?.name || "" }; }
      catch (e) { out.new_project = { key: c.newProject, ok: false, error: e.message }; out.ok = false; }
    }
    try { const ps = await request("GET", "/rest/api/2/project"); if (Array.isArray(ps)) run("INSERT OR REPLACE INTO settings (key,value) VALUES ('jira_project_keys',?)", ps.map(p => p.key).join(",")); } catch (_) {}
    if (!targets.size && !c.newProject) { out.ok = false; out.message += " – bitte ein Jira-Projekt für neue Tickets (oder ein Sammel-Buchungsticket) festlegen"; }
    res.json(out);
  }));

  /* ══ Automatisch einrichten: Vorschläge aus Jira/Tempo und der eigenen Tempo-Historie ══
     • Jira-Projekt + Vorgangstyp für neue Tickets (wo du anlegen darfst; bekannte Namen wie ZEIT/ORGA bevorzugt)
     • Tempo-Account: am häufigsten benutzt – insgesamt und je Projekt (aus deinen Worklogs der letzten 6 Monate)
     • Account-Attribut-ID aus Tempo, „ab Datum“ = erster nicht gesperrter Monat */
  app.get("/api/integrations/tempo-push/autosetup", wrap(async (_, res) => {
    const c = cfg();
    if (!ready(c)) return res.status(400).json({ error: "Erst Jira-Adresse und Token eintragen (Zugang & Projekte)" });
    const out = { notes: [] };
    let me = null; try { me = await request("GET", "/rest/api/2/myself"); saveMe(me); } catch (e) { return res.status(400).json({ error: "Jira: " + e.message }); }
    const worker = me?.key || me?.name || c.workerKey;
    // 1. Projekt für neue Tickets
    let projects = []; try { projects = await request("GET", "/rest/api/2/project") || []; } catch (_) {}
    const PREF = [/^ZEIT/, /^TIME/, /^ZP$/, /^ORGA/, /^INT/, /^PRJB/, /^ADM/, /^IT$/];
    const rank = p => { const i = PREF.findIndex(r => r.test(String(p.key).toUpperCase()) || r.test(String(p.name || "").toUpperCase())); return i < 0 ? 99 : i; };
    const cands = [...projects].sort((a, b) => rank(a) - rank(b)).slice(0, 8);
    out.projects = cands.map(p => ({ key: p.key, name: p.name }));
    for (const p of c.newProject ? [{ key: c.newProject }, ...cands] : cands) {
      try {
        const t = await request("GET", `/rest/api/2/issue/createmeta/${encodeURIComponent(p.key)}/issuetypes`);
        const types = (t?.values || t?.issueTypes || []).filter(x => !x.subtask).map(x => x.name);
        if (types.length) { out.new_project = p.key; out.issuetype = types.find(n => /^(task|aufgabe)$/i.test(n)) || types[0]; out.issuetypes = types; break; }
      } catch (_) {}
    }
    if (!out.new_project && cands[0]) { out.new_project = c.newProject || cands[0].key; out.issuetype = c.issueType; out.notes.push("Anlegerechte konnten nicht geprüft werden – Projekt bitte kontrollieren"); }
    // 2. Account-Attribut
    try {
      const wa = await request("GET", "/rest/tempo-core/1/work-attribute");
      const acc = (Array.isArray(wa) ? wa : []).find(x => x.key === "_Account_" || /account/i.test(x.name || ""));
      if (acc?.id) out.account_attr_id = acc.id;
    } catch (_) {}
    // 3. Accounts: Liste + Historie
    let accounts = [];
    try { const a = await request("GET", "/rest/tempo-accounts/1/account"); accounts = (Array.isArray(a) ? a : []).filter(x => !x.status || /open/i.test(x.status)).map(x => ({ key: x.key, name: x.name })); } catch (_) {}
    out.accounts = accounts.slice(0, 300);
    const from = new Date(Date.now() - 183 * 864e5).toISOString().slice(0, 10), to = localDateOf(Date.now());
    const total = {}, perProj = {};
    try {
      const wl = await request("POST", "/rest/tempo-timesheets/4/worklogs/search", { from, to, worker: [worker] });
      for (const w of Array.isArray(wl) ? wl : []) {
        const a = w.attributes?._Account_?.value || Object.values(w.attributes || {}).find(x => /account/i.test(x?.name || x?.key || ""))?.value;
        if (!a) continue;
        total[a] = (total[a] || 0) + (w.timeSpentSeconds || 0);
        const key = String(w.issue?.key || w.originTaskId || ""), pk = key.split("-")[0];
        const pid = pk && hooks?.sources ? hooks.sources.projectByJiraKey(pk) : null;
        if (pid) { (perProj[pid] ||= {})[a] = (perProj[pid][a] || 0) + (w.timeSpentSeconds || 0); }
      }
    } catch (_) { out.notes.push("Tempo-Historie nicht lesbar – Accounts bitte selbst wählen"); }
    const top = o => Object.entries(o).sort((a, b) => b[1] - a[1])[0]?.[0] || null;
    out.account = c.account || top(total);
    out.account_usage = Object.entries(total).sort((a, b) => b[1] - a[1]).slice(0, 10).map(([k, s]) => ({ key: k, hours: Math.round(s / 360) / 10 }));
    out.project_accounts = Object.entries(perProj).map(([pid, o]) => {
      const p = one("SELECT id, name, tempo_account FROM projects WHERE id=?", Number(pid));
      return p ? { project_id: p.id, name: p.name, current: p.tempo_account || null, suggest: top(o) } : null;
    }).filter(x => x && x.suggest && x.suggest !== x.current && x.suggest !== out.account);
    // 4. ab wann übertragen: ältester nicht gesperrter Monat der letzten 3
    const months = []; for (let i = 0; i < 3; i++) { const d = new Date(); d.setDate(1); d.setMonth(d.getMonth() - i); months.unshift(localDateOf(d.getTime()).slice(0, 7)); }
    let since = null;
    for (const m of months) { const r = await checkMonth(m, { ...c, worker }).catch(() => null); if (r && !r.locked && !since) since = m + "-01"; }
    out.since = c.since && c.since >= (since || "9999") ? c.since : since || c.since;
    out.months = all("SELECT * FROM tempo_months WHERE month>=? ORDER BY month", months[0]);
    out.plane_mode = c.planeMode; out.worker = me?.name || c.worker;
    res.json(out);
  }));
  app.post("/api/integrations/tempo-push/autosetup", (req, res) => {
    const b = req.body || {}, set = (k, v) => v != null && v !== "" && run("INSERT OR REPLACE INTO settings (key,value) VALUES (?,?)", k, String(v));
    set("jira_new_project", b.new_project && String(b.new_project).toUpperCase()); set("jira_new_issuetype", b.issuetype);
    set("tempo_account", b.account); set("tempo_account_attr_id", b.account_attr_id); set("tempo_push_since", b.since);
    if (b.plane_mode) set("tempo_plane_mode", b.plane_mode === "ticket" ? "ticket" : "month");
    if (b.enable) set("tempo_push_enabled", "1");
    for (const pa of Array.isArray(b.project_accounts) ? b.project_accounts : []) run("UPDATE projects SET tempo_account=? WHERE id=?", String(pa.account || "").trim() || null, Number(pa.project_id));
    writeLog("info", LOG, `Tempo automatisch eingerichtet: ${JSON.stringify({ ...b, project_accounts: (b.project_accounts || []).length })}`);
    res.json({ ok: true });
  });

  /* Tempo-Accounts zur Auswahl: offene Accounts, deine meistgenutzten zuerst (1 h zwischengespeichert) */
  let accList = { at: 0, data: null };
  app.get("/api/integrations/tempo-push/accounts", wrap(async (_, res) => {
    if (accList.data && Date.now() - accList.at < 3600e3) return res.json(accList.data);
    const c = cfg(); let list = [];
    try { const a = await request("GET", "/rest/tempo-accounts/1/account"); list = (Array.isArray(a) ? a : []).filter(x => !x.status || /open/i.test(x.status)).map(x => ({ key: x.key, name: x.name })); } catch (e) { return res.status(400).json({ error: e.message }); }
    // Nutzung: lokale Buchungen mit Account + was die Tempo-Historie hergibt
    const use = {};
    for (const r of all("SELECT COALESCE(e.tempo_account, p.tempo_account) AS a, SUM(e.duration_seconds) AS s FROM time_entries e LEFT JOIN projects p ON p.id=e.project_id WHERE e.date>=? GROUP BY 1", localDateOf(Date.now() - 183 * 864e5))) if (r.a) use[r.a] = (use[r.a] || 0) + r.s;
    if (c.account) use[c.account] = (use[c.account] || 0) + 1;
    list.sort((a, b) => (use[b.key] || 0) - (use[a.key] || 0) || a.key.localeCompare(b.key));
    accList = { at: Date.now(), data: list.map(x => ({ ...x, used: use[x.key] ? Math.round(use[x.key] / 360) / 10 : 0 })) };
    res.json(accList.data);
  }));
  /* Account für Buchungen setzen: {ids, account, remember: "project"|undefined} */
  app.post("/api/integrations/tempo-push/entry-account", (req, res) => {
    const ids = (req.body?.ids || []).map(Number).filter(Boolean), acc = String(req.body?.account || "").trim() || null;
    for (const id of ids) run("UPDATE time_entries SET tempo_account=? WHERE id=?", acc, id);
    if (req.body?.remember === "project" && ids[0]) {
      const pid = one("SELECT project_id FROM time_entries WHERE id=?", ids[0])?.project_id;
      if (pid) { run("UPDATE projects SET tempo_account=? WHERE id=?", acc, pid); run("UPDATE time_entries SET tempo_account=NULL WHERE project_id=? AND tempo_account=?", pid, acc); }
    }
    writeLog("info", LOG, `Tempo-Account ${acc || "(Standard)"} für ${ids.length} Buchung(en)${req.body?.remember === "project" ? " – fürs Projekt gemerkt" : ""}`);
    res.json({ ok: true });
  });

  app.get("/api/integrations/tempo-push/outbox", (req, res) => {
    const c = cfg();
    const locks = lockedMonths();
    const rowsById = {};
    const list = candidates(c).map(e => {
      rowsById[e.id] = e;
      const p = plan(e, c), status = statusOf(e, p, locks);
      return { id: e.id, date: e.date, start_time: e.start_time, end_time: e.end_time, minutes: Math.round(p.secs / 60), status, target: p.target || null, kind: p.kind,
        ticket: e.ticket || null, project_name: e.project_name, comment: p.comment, error: e.tempo_push_error || null, tempo_id: e.tempo_push_id || null,
        synced_at: e.tempo_push_synced_at || null, dup: status === "dup" ? e.tempo_dup : null, account: p.account || null, account_from: p.account_from || "", project_id: e.project_id,
        // v6.34: das eigene Ticket sichtbar machen (nicht nur das Tempo-Ziel)
        task_id: e.task_id || null, task_title: e.task_title || null, note: (e.comment || "").trim() || null, system: e.plane_work_item_id ? "plane" : (e.ticket && KEY_RE.test(String(e.ticket).toUpperCase())) ? "jira" : null };
    });
    const counts = {}, mins = {};
    for (const r of list) { counts[r.status] = (counts[r.status] || 0) + 1; mins[r.status] = (mins[r.status] || 0) + r.minutes; }
    const byDay = {};
    for (const r of list) (byDay[r.date] ||= []).push(r);
    const days = Object.keys(byDay).sort().reverse().map(d => ({ date: d, entries: byDay[d], minutes: byDay[d].reduce((s, r) => s + (r.status === "skipped" ? 0 : r.minutes), 0),
      open: byDay[d].filter(r => ["new", "changed", "error"].includes(r.status)).length, needs: byDay[d].filter(r => r.status === "needs").length,
      // Lücken im Tag (am Mac bzw. zwischen Buchungen, ab 10 min) – zum direkten Nachtragen
      gaps: (() => { try { const ov = hooks?.presence?.dayOverview(d); return (ov?.gaps || []).filter(g => g.e - g.s >= 10); } catch (_) { return []; } })() }));
    // Vorschläge (lokal, schnell) für Buchungen ohne festes Jira-Ziel
    const corpus = buildCorpus();
    for (const r of list) if (r.status === "needs" || r.kind === "create" || r.kind === "create-collector") {   // Übergangs-Ticket braucht keinen Vorschlag
      const e = rowsById[r.id]; if (!e) continue;
      r.suggest = suggestLocal(e, corpus).slice(0, 3);
    }
    res.json({ month_tickets: all("SELECT * FROM tempo_month_tickets WHERE worker=? ORDER BY month DESC LIMIT 12", c.worker || ""), plane_mode: c.planeMode,
      configured: ready(c), enabled: c.enabled, mode: c.mode, issue: c.issue || null, new_project: c.newProject || null, since: c.since, worker: c.worker || null, counts, minutes: mins, days,
      deletions: all("SELECT * FROM tempo_push_deletions ORDER BY id"),
      months: all("SELECT * FROM tempo_months WHERE month>=? ORDER BY month DESC", monthOf(c.since)),
      oldest: one("SELECT MIN(date) AS d FROM time_entries WHERE end_time IS NOT NULL AND COALESCE(source,'manual') NOT IN ('tempo','tempo_csv')")?.d || null });
  });

  app.post("/api/integrations/tempo-push/push", wrap(async (req, res) => {
    const b = req.body || {};
    res.json(await push({ ids: Array.isArray(b.ids) ? b.ids.map(Number) : undefined }));
  }));

  /* Einträge von Tempo ausnehmen (oder wieder aufnehmen). Schon übertragene werden in Tempo gelöscht. */
  app.post("/api/integrations/tempo-push/skip", (req, res) => {
    const { ids = [], skip = true } = req.body || {};
    for (const id of ids.map(Number)) {
      const e = one("SELECT * FROM time_entries WHERE id=?", id);
      if (!e) continue;
      if (skip && e.tempo_push_id) {
        run("INSERT INTO tempo_push_deletions (worklog_id,label) VALUES (?,?)", e.tempo_push_id, `${e.date} ${(e.comment || "").slice(0, 60)}`);
        run("UPDATE time_entries SET tempo_push_id=NULL, tempo_push_hash=NULL, tempo_push_synced_at=NULL WHERE id=?", id);
      }
      run("UPDATE time_entries SET tempo_skip=?, tempo_push_error=NULL WHERE id=?", skip ? 1 : 0, id);
    }
    res.json({ ok: true });
  });

  app.delete("/api/integrations/tempo-push/deletions/:id", (req, res) => {
    run("DELETE FROM tempo_push_deletions WHERE id=?", Number(req.params.id));
    res.json({ ok: true });
  });

  /* Zeitraum prüfen (Sperren + Doppelte), optional Startdatum zurücksetzen → ältere Buchungen einbeziehen */
  app.post("/api/integrations/tempo-push/check", wrap(async (req, res) => {
    const since = req.body?.since;
    if (since) {
      if (!/^\d{4}-\d{2}-\d{2}$/.test(since)) return res.status(400).json({ error: "Datum im Format JJJJ-MM-TT" });
      run("INSERT OR REPLACE INTO settings (key,value) VALUES ('tempo_push_since',?)", since);
    }
    const c = cfg();
    if (!ready(c)) return res.status(400).json({ error: "Jira-Adresse und Token fehlen" });
    if (!c.worker) return res.status(400).json({ error: "Erst in Einstellungen › Jira & Tempo „Verbindung testen“" });
    const months = []; for (let d = new Date(c.since.slice(0, 7) + "-15T12:00:00"); monthOf(localDateOf(d.getTime())) <= monthOf(localDateOf(Date.now())); d.setMonth(d.getMonth() + 1)) months.push(monthOf(localDateOf(d.getTime())));
    const out = [];
    for (const m of months) out.push(await checkMonth(m, c));
    res.json({ ok: true, since: c.since, months: out });
  }));
  app.post("/api/integrations/jira/my-tickets/sync", wrap(async (_, res) => res.json(await syncMyTickets())));
  app.get("/api/integrations/jira/my-tickets/status", (_, res) => res.json({ last: lastMine, count: one("SELECT COUNT(*) AS n FROM tasks WHERE jira_mine=1")?.n || 0, jql: cfg().jql }));

  /* Doppel-Hinweis: trotzdem übertragen ('ok') bzw. ausnehmen */
  app.post("/api/integrations/tempo-push/dup", (req, res) => {
    const { ids = [], action } = req.body || {};
    for (const id of ids.map(Number)) {
      if (action === "push") run("UPDATE time_entries SET tempo_dup='ok' WHERE id=?", id);
      else if (action === "skip") run("UPDATE time_entries SET tempo_skip=1 WHERE id=?", id);
    }
    res.json({ ok: true });
  });
  /* Monatssperre zurücksetzen (z. B. nachdem der Zeitraum in Tempo wieder geöffnet wurde) */
  app.delete("/api/integrations/tempo-push/months/:month", (req, res) => {
    run("DELETE FROM tempo_months WHERE month=?", req.params.month);
    run("UPDATE time_entries SET tempo_push_error=NULL WHERE substr(date,1,7)=? AND tempo_push_id IS NULL", req.params.month);
    res.json({ ok: true });
  });

  /* Buchungsticket je Projekt (leer = globales Buchungsticket) */
  app.put("/api/integrations/tempo-push/project/:id", (req, res) => {
    const v = String(req.body?.issue || "").trim().toUpperCase();
    if (v && !/^[A-Z][A-Z0-9_]*-\d+$/.test(v)) return res.status(400).json({ error: "Ticket-Key im Format ABC-123 angeben" });
    run("UPDATE projects SET tempo_issue=? WHERE id=?", v || null, Number(req.params.id));
    if (req.body && "account" in req.body) run("UPDATE projects SET tempo_account=? WHERE id=?", String(req.body.account || "").trim() || null, Number(req.params.id));
    res.json({ ok: true, issue: v || null });
  });

  /* Jira-Buchungen ohne Ticket (für den Assistenten) */
  function noTicket() {
    const c = cfg(); if (!c.since) return [];
    return candidates(c, "e.tempo_push_id IS NULL AND COALESCE(e.tempo_skip,0)=0").filter(e => {
      if (hooks?.sources && !hooks.sources.allowsTempo(e.project_id)) return false;
      return targetOf(e, c).kind === "no-ticket";
    });
  }
  return { init, beforeDelete, scheduleAuto, push, request, ensureTaskForIssue, syncMyTickets, cfg, createIssue, noTicket };
};
