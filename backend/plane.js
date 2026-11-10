"use strict";
/* ═══════════════════════════════════════════════════════════════════
   PLANE-ANBINDUNG  (Zeitprotokoll ↔ Plane Worklogs)

   Prinzip „lokal zuerst“ + „Summe pro Ticket“ (wie Tempo ↔ Jira):
   - Jede Buchung liegt zuerst in der lokalen SQLite-DB auf dem Mac.
     Das Zeitprotokoll ist die Wahrheit für wer/wann/wie lange.
   - Nach Plane geht pro Ticket und Person genau EIN Worklog, dessen
     Dauer die App immer auf die lokale Gesamtsumme setzt ([zp-sum]).
     Damit stimmt die Summe in Plane immer – egal wann übertragen wurde.
   - Übertragen wird erst nach Freigabe (manuell) bzw. im Auto-Modus.
   - Worklogs, die direkt in Plane gebucht wurden, holt „Aus Plane holen“
     als eigene Einträge (source='plane'); sie zählen nicht zur Summe.

   Das Modul ist bewusst eigenständig (nur ctx-Abhängigkeiten), damit es
   später 1:1 in einen Server-Dienst / eine Plane-App umziehen kann.
═══════════════════════════════════════════════════════════════════ */
const https = require("https");
const http  = require("http");

const PUSH_SOURCES_EXCLUDED = ["tempo", "tempo_csv"];   // kommen aus Jira – nicht nach Plane spiegeln
const KEY_RE = /^([A-Za-z][A-Za-z0-9_]*)-(\d+)$/;

module.exports = function registerPlane(app, ctx) {
  const { one, all, run, getSettings, writeLog, localDateOf, secure, hooks } = ctx;
  const LOG = "plane";

  /* ── Schema (wird nach dem Öffnen der DB aufgerufen) ─────────────── */
  function init() {
  const mig = [
    "ALTER TABLE projects ADD COLUMN plane_project_id TEXT",
    "ALTER TABLE projects ADD COLUMN plane_identifier TEXT",
    "ALTER TABLE tasks ADD COLUMN plane_work_item_id TEXT",
    "ALTER TABLE tasks ADD COLUMN plane_project_id TEXT",
    "ALTER TABLE time_entries ADD COLUMN plane_worklog_id TEXT",
    "ALTER TABLE time_entries ADD COLUMN plane_work_item_id TEXT",
    "ALTER TABLE time_entries ADD COLUMN plane_project_id TEXT",
    "ALTER TABLE time_entries ADD COLUMN plane_hash TEXT",
    "ALTER TABLE time_entries ADD COLUMN plane_synced_at TEXT",
    "ALTER TABLE time_entries ADD COLUMN plane_error TEXT",
    "ALTER TABLE time_entries ADD COLUMN plane_skip INTEGER DEFAULT 0",
    "ALTER TABLE time_entries ADD COLUMN plane_sum_key TEXT",
    "ALTER TABLE time_entries ADD COLUMN plane_sum_secs INTEGER",
    "ALTER TABLE time_entries ADD COLUMN plane_include INTEGER DEFAULT 0",   // Tempo-Altdaten, die nach Plane übertragen werden sollen
    "ALTER TABLE tasks ADD COLUMN jira_origin TEXT",                          // ursprünglicher Jira-Key nach der Zuordnung zu Plane
  ];
  for (const m of mig) { try { run(m); } catch (_) {} }
  run(`CREATE TABLE IF NOT EXISTS plane_deletions (
    id INTEGER PRIMARY KEY AUTOINCREMENT, worklog_id TEXT NOT NULL, work_item_id TEXT, project_id TEXT,
    label TEXT, created_at TEXT DEFAULT (datetime('now')), error TEXT)`);
  try { run("CREATE INDEX IF NOT EXISTS idx_te_plane_wl ON time_entries(plane_worklog_id)"); } catch (_) {}
  run(`CREATE TABLE IF NOT EXISTS plane_ticket_sync (
    ticket TEXT PRIMARY KEY, work_item_id TEXT, project_id TEXT, worklog_id TEXT,
    synced_minutes INTEGER, synced_at TEXT, error TEXT, title TEXT)`);
  try { run("ALTER TABLE plane_ticket_sync ADD COLUMN desc_hash TEXT"); } catch (_) {}
  run(`CREATE TABLE IF NOT EXISTS ticket_map (
    jira_key TEXT PRIMARY KEY, status TEXT NOT NULL, plane_key TEXT, plane_work_item_id TEXT, plane_project_id TEXT,
    title TEXT, mapped_at TEXT DEFAULT (datetime('now')))`);
  setInterval(scheduleAuto, 10 * 60 * 1000);
  }

  /* ── Konfiguration ──────────────────────────────────────────────── */
  // Workspace darf auch als ganze URL eingetragen werden: https://plane.firma.de/example/... → "example"
  const normWs = v => {
    v = String(v || "").trim();
    if (/^https?:\/\//i.test(v)) { try { v = new URL(v).pathname; } catch (_) {} }
    return (v.split("/").filter(Boolean)[0] || "").trim();
  };
  function cfg() {
    const s = getSettings();
    return {
      base: (s.plane_base_url || "").trim().replace(/\/+$/, ""),
      key: (s.plane_api_key || "").trim(),
      ws: normWs(s.plane_workspace),
      mode: s.plane_sync_mode === "auto" ? "auto" : "manual",
      insecure: s.plane_insecure_tls === "1" || s.plane_insecure_tls === "true",
      meId: s.plane_me_id || "",
    };
  }
  const configured = c => !!(c.base && c.key && c.ws);

  /* ── HTTP-Client ────────────────────────────────────────────────── */
  /* Plane begrenzt die API (ca. 60 Anfragen/Minute). Bei HTTP 429 kurz warten und erneut versuchen. */
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  /* v6.32: Drosselung VOR dem Limit statt danach warten.
     • Hintergrund-Abfragen (Abgleich, „Meine Work-Items“) halten sich an max. 45/Minute und warten selbst.
     • Interaktive Abfragen (Suche beim Tippen) gehen sofort raus; ist Plane gerade gesperrt, kommt sofort
       eine Meldung statt 40 s Warten – sonst blockieren hängende Anfragen den Browser (max. 6 Verbindungen). */
  const sent = []; let blockedUntil = 0, lastLimitLog = 0;
  async function pace(interactive) {
    for (;;) {
      const now = Date.now();
      while (sent.length && now - sent[0] > 60000) sent.shift();
      if (now < blockedUntil) { if (interactive) { const e = new Error(`Plane ist kurz ausgelastet – in ${Math.ceil((blockedUntil - now) / 1000)} s nochmal`); e.status = 429; e.fast = true; throw e; } await sleep(blockedUntil - now); continue; }
      if (interactive ? sent.length < 58 : sent.length < 45) { sent.push(now); return; }
      if (interactive) { const e = new Error("Plane ist kurz ausgelastet – bitte gleich nochmal"); e.status = 429; e.fast = true; throw e; }
      await sleep(Math.max(500, 60000 - (now - sent[0]) + 50));
    }
  }
  async function request(method, path, body, opts = {}) {
    for (let attempt = 0; ; attempt++) {
      await pace(!!opts.interactive);
      try { return await (secure && !cfg().insecure ? secure.withChainRepair(cfg().base, () => requestOnce(method, path, body)) : requestOnce(method, path, body)); }
      catch (err) {
        if (err.status !== 429) throw err;
        const wait = Math.min(65000, Math.max(2000, err.retryAfterMs || 5000 * (attempt + 1)));
        blockedUntil = Math.max(blockedUntil, Date.now() + wait);
        if (Date.now() - lastLimitLog > 30000) { lastLimitLog = Date.now(); writeLog("info", LOG, `Plane-Anfragelimit erreicht – Pause ${Math.round(wait / 1000)} s`); }
        if (opts.interactive || attempt >= 4) throw err;
      }
    }
  }
  function requestOnce(method, path, body) {
    const c = cfg();
    if (!configured(c)) return Promise.reject(new Error("Plane ist nicht konfiguriert (Einstellungen → Plane)"));
    const url = new URL(c.base + path);
    const lib = url.protocol === "http:" ? http : https;
    const payload = body ? JSON.stringify(body) : null;
    return new Promise((resolve, reject) => {
      const req = lib.request({
        method, hostname: url.hostname, port: url.port || undefined, path: url.pathname + url.search,
        headers: { "X-API-Key": c.key, "Accept": "application/json",
          ...(payload ? { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(payload) } : {}) },
        ...(url.protocol === "https:" && secure && !c.insecure ? secure.requestOptions(url.href) : { rejectUnauthorized: !c.insecure }), timeout: 20000,
      }, res => {
        let data = "";
        res.on("data", d => data += d);
        res.on("end", () => {
          let json = null; try { json = data ? JSON.parse(data) : null; } catch (_) {}
          if (res.statusCode >= 200 && res.statusCode < 300) return resolve(json);
          const msg = (json && (json.detail || json.error || json.message)) || data.slice(0, 200) || "";
          const hint = res.statusCode === 404 ? ` – Adresse „${c.base}“ oder Workspace „${c.ws}“ prüfen` : res.statusCode === 401 || res.statusCode === 403 ? " – API-Key prüfen" : "";
          const err = new Error(`Plane HTTP ${res.statusCode}${msg ? ": " + (typeof msg === "string" ? msg : JSON.stringify(msg)) : ""}${hint}`);
          err.status = res.statusCode;
          if (res.statusCode === 429) {
            const ra = Number(res.headers["retry-after"]), reset = Number(res.headers["x-ratelimit-reset"]);
            err.retryAfterMs = ra > 0 ? ra * 1000 : reset > 1e9 ? reset * 1000 - Date.now() + 500 : reset > 0 ? reset * 1000 : 0;
            err.message = "Plane-Anfragelimit erreicht – bitte kurz warten";
          }
          reject(err);
        });
      });
      req.on("timeout", () => req.destroy(new Error("Zeitüberschreitung bei Plane")));
      req.on("error", reject);
      if (payload) req.write(payload);
      req.end();
    });
  }
  const ws = () => `/api/v1/workspaces/${encodeURIComponent(cfg().ws)}`;
  const list = r => Array.isArray(r) ? r : (r?.results || r?.issues || []);

  async function listAll(path) {
    const out = []; let cursor = null;
    for (let i = 0; i < 50; i++) {
      const sep = path.includes("?") ? "&" : "?";
      const r = await request("GET", `${path}${sep}per_page=100${cursor ? "&cursor=" + encodeURIComponent(cursor) : ""}`);
      out.push(...list(r));
      if (!r || Array.isArray(r) || !r.next_page_results || !r.next_cursor) break;
      cursor = r.next_cursor;
    }
    return out;
  }

  async function me() {
    const r = await request("GET", "/api/v1/users/me/");
    if (r?.id) run("INSERT OR REPLACE INTO settings (key,value) VALUES ('plane_me_id',?)", r.id);
    return r;
  }

  /* ── Hilfsfunktionen ────────────────────────────────────────────── */
  const hm = iso => { const d = new Date(iso); return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`; };
  const minutesOf = e => Math.max(1, Math.round((e.duration_seconds || 0) / 60));

  function entryRows(where, ...params) {
    return all(`SELECT e.*, t.jira_ticket AS ticket, t.title AS task_title, t.plane_work_item_id AS task_wi,
                       t.plane_project_id AS task_pp, p.name AS project_name, p.plane_project_id AS proj_pp
                FROM time_entries e
                LEFT JOIN tasks t ON t.id=e.task_id
                LEFT JOIN projects p ON p.id=e.project_id
                WHERE ${where}`, ...params);
  }

  function describe(e) {
    const text = (e.comment || e.task_title || "Arbeit").trim();
    const span = e.end_time ? `${hm(e.start_time)}–${hm(e.end_time)}` : hm(e.start_time);
    return `${text} · ${e.date} ${span} [zp#${e.id}]`;
  }
  const hashOf = (e, workItemId) => `${minutesOf(e)}|${describe(e)}|${workItemId || e.task_wi || ""}`;

  function statusOf(e) {
    if (!e.end_time || !e.duration_seconds) return "running";
    if (PUSH_SOURCES_EXCLUDED.includes(e.source) && !e.plane_include) return "external";
    if (e.plane_skip) return "skipped";
    const key = (e.ticket || "").trim().toUpperCase();
    if (!KEY_RE.test(key)) return "no_ticket";
    // Kernregel: nur Projekte, die wirklich in Plane sind (und deren Regel Plane erlaubt), gehen nach Plane – sonst Jira/Tempo
    if (!e.plane_worklog_id && hooks?.sources && !hooks.sources.allowsPlane(e.project_id)) return "not_plane";
    if (e.plane_error) return "error";
    if (!e.plane_worklog_id) return "new";
    if (e.plane_hash !== hashOf(e, e.task_wi || e.plane_work_item_id)) return "changed";
    return "synced";
  }

  /* Ticket-Schlüssel (PRJB-12) → Plane Work-Item (mit Cache in tasks) */
  async function resolveWorkItem(e) {
    const key = (e.ticket || "").trim().toUpperCase();
    if (e.task_wi && e.task_pp) return { id: e.task_wi, project: e.task_pp };
    const wi = await request("GET", `${ws()}/work-items/${encodeURIComponent(key)}/`);
    const project = wi?.project || wi?.project_id;
    if (!wi?.id || !project) throw new Error(`Work-Item ${key} nicht gefunden`);
    run("UPDATE tasks SET plane_work_item_id=?, plane_project_id=? WHERE id=?", wi.id, project, e.task_id);
    // keine stille Projekt-Verknüpfung mehr (v6.8.0) – Verknüpfen nur über Projekte › Quellen
    if (e.proj_pp && project !== e.proj_pp) throw new Error(`${key} liegt in einem anderen Plane-Projekt als das gebuchte Projekt`);
    return { id: wi.id, project, name: wi.name };
  }
  const wlPath = (project, wi, wl) => `${ws()}/projects/${project}/work-items/${wi}/worklogs/${wl ? wl + "/" : ""}`;

  async function pushOne(e) {
    const st = statusOf(e);
    if (st !== "new" && st !== "changed" && st !== "error") return { id: e.id, status: st, skipped: true };
    try {
      const wi = await resolveWorkItem(e);
      const body = { duration: minutesOf(e), description: describe(e) };
      let wlId = e.plane_worklog_id;
      if (wlId && e.plane_work_item_id && e.plane_work_item_id !== wi.id) {
        // Ticket wurde geändert → alten Worklog entfernen, neuen anlegen
        await request("DELETE", wlPath(e.plane_project_id, e.plane_work_item_id, wlId)).catch(err => { if (err.status !== 404) throw err; });
        wlId = null;
      }
      if (wlId) {
        try { await request("PATCH", wlPath(wi.project, wi.id, wlId), body); }
        catch (err) { if (err.status === 404) wlId = null; else throw err; }
      }
      if (!wlId) {
        const r = await request("POST", wlPath(wi.project, wi.id), body);
        wlId = r?.id;
        if (!wlId) throw new Error("Plane hat keine Worklog-ID zurückgegeben");
      }
      run(`UPDATE time_entries SET plane_worklog_id=?, plane_work_item_id=?, plane_project_id=?, plane_hash=?,
           plane_synced_at=?, plane_error=NULL WHERE id=?`, wlId, wi.id, wi.project, hashOf(e, wi.id), new Date().toISOString(), e.id);
      return { id: e.id, status: "synced", worklog_id: wlId };
    } catch (err) {
      run("UPDATE time_entries SET plane_error=? WHERE id=?", err.message.slice(0, 300), e.id);
      writeLog("error", LOG, `Eintrag #${e.id} (${e.ticket}): ${err.message}`);
      return { id: e.id, status: "error", error: err.message };
    }
  }

  async function processDeletions() {
    const res = [];
    for (const d of all("SELECT * FROM plane_deletions ORDER BY id")) {
      try {
        await request("DELETE", wlPath(d.project_id, d.work_item_id, d.worklog_id)).catch(err => { if (err.status !== 404) throw err; });
        run("DELETE FROM plane_deletions WHERE id=?", d.id);
        res.push({ id: d.id, status: "deleted" });
      } catch (err) {
        run("UPDATE plane_deletions SET error=? WHERE id=?", err.message.slice(0, 300), d.id);
        res.push({ id: d.id, status: "error", error: err.message });
      }
    }
    return res;
  }

  /* ═══ Summen pro Ticket ═══════════════════════════════════════════ */
  const SUM_TAG = "[zp-sum]";
  /* Beschreibung des Summen-Worklogs: Kopfzeile + je Buchung Datum, Dauer und der Text, den du dazu geschrieben hast */
  function sumDescription(t) {
    const ids = String(t.ids || "").split(",").map(Number).filter(Boolean);
    const head = `Zeitprotokoll: ${t.n} ${t.n === 1 ? "Eintrag" : "Einträge"}, ${fmtMin(t.minutes)} (${deDate(t.first)}${t.last !== t.first ? "–" + deDate(t.last) : ""})`;
    const rows = ids.length ? all(`SELECT date, start_time, duration_seconds, comment, activity FROM time_entries WHERE id IN (${ids.join(",")}) ORDER BY start_time`) : [];
    const lines = []; let len = head.length, more = 0;
    for (const e of rows) {
      const txt = [e.activity, (e.comment || "").replace(/\s+/g, " ").trim()].filter(Boolean).join(": ");
      const l = `• ${deDate(e.date)} ${hm(e.start_time)} · ${fmtMin(Math.round((e.duration_seconds || 0) / 60))}${txt ? " · " + txt.slice(0, 200) : ""}`;
      if (len + l.length > 1800) { more++; continue; }
      lines.push(l); len += l.length + 1;
    }
    if (more) lines.push(`… und ${more} weitere`);
    return [head, ...lines, `Summe wird automatisch gepflegt ${SUM_TAG}`].join("\n");
  }
  const descHash = d => require("crypto").createHash("sha1").update(d).digest("hex").slice(0, 16);
  const deDate = d => d ? `${d.slice(8, 10)}.${d.slice(5, 7)}.${d.slice(0, 4)}` : "";
  const fmtMin = m => `${Math.floor(m / 60)}:${String(m % 60).padStart(2, "0")} h`;

  /* Lokale Summen je Ticket (nur eigene Buchungen; Tempo-/Plane-Importe zählen nicht) */
  function localSums() {
    // Projekt-Regel: Projekte, deren Zeit nicht nach Plane soll, bleiben außen vor
    const blocked = hooks?.sources ? all("SELECT id FROM projects").map(r => r.id).filter(id => !hooks.sources.allowsPlane(id)) : [];
    const rows = all(`SELECT UPPER(TRIM(t.jira_ticket)) AS ticket, SUM(e.duration_seconds) AS secs, COUNT(*) AS n,
                             MIN(e.date) AS first, MAX(e.date) AS last, MAX(t.title) AS title,
                             MAX(t.plane_work_item_id) AS wi, MAX(t.plane_project_id) AS pp,
                             GROUP_CONCAT(DISTINCT p.name) AS projects, GROUP_CONCAT(e.id) AS ids
                      FROM time_entries e JOIN tasks t ON t.id=e.task_id LEFT JOIN projects p ON p.id=e.project_id
                      WHERE e.end_time IS NOT NULL AND e.duration_seconds > 0
                        AND (COALESCE(e.source,'manual') NOT IN ('tempo','tempo_csv','plane') OR COALESCE(e.plane_include,0)=1)
                        AND COALESCE(e.plane_skip,0)=0 AND t.jira_ticket IS NOT NULL AND TRIM(t.jira_ticket) <> ''
                        AND COALESCE(e.project_id,0) NOT IN (${blocked.length ? blocked.map(Number).join(",") : "-1"})
                        AND (t.plane_work_item_id IS NOT NULL OR EXISTS (SELECT 1 FROM projects tp WHERE tp.id=t.project_id AND tp.plane_project_id IS NOT NULL
                             AND tp.plane_identifier IS NOT NULL AND UPPER(TRIM(t.jira_ticket)) LIKE UPPER(tp.plane_identifier) || '-%'))
                      GROUP BY UPPER(TRIM(t.jira_ticket))`).filter(r => KEY_RE.test(r.ticket));
    const legacy = all(`SELECT UPPER(TRIM(t.jira_ticket)) AS ticket, COUNT(*) AS n FROM time_entries e JOIN tasks t ON t.id=e.task_id
                        WHERE e.plane_worklog_id IS NOT NULL AND COALESCE(e.source,'manual') <> 'plane' GROUP BY 1`);
    const leg = Object.fromEntries(legacy.filter(l => l.ticket && KEY_RE.test(l.ticket)).map(l => [l.ticket, l.n]));
    const syncs = Object.fromEntries(all("SELECT * FROM plane_ticket_sync").map(r => [r.ticket, r]));
    const out = {};
    for (const r of rows) { const o = { ...r, minutes: Math.round(r.secs / 60), legacy: leg[r.ticket] || 0, sync: syncs[r.ticket] || null }; o.desc = sumDescription(o); o.dhash = descHash(o.desc); out[r.ticket] = o; }
    // Tickets, die in Plane eine Summe haben, lokal aber nichts mehr → Summe muss weg
    for (const k of Object.keys(syncs)) if (!out[k]) out[k] = { ticket: k, secs: 0, minutes: 0, n: 0, title: syncs[k].title, legacy: leg[k] || 0, sync: syncs[k] };
    for (const k of Object.keys(leg)) if (!out[k]) out[k] = { ticket: k, secs: 0, minutes: 0, n: 0, legacy: leg[k], sync: null };
    return out;
  }

  function ticketStatus(t) {
    const sy = t.sync;
    if (sy?.error) return "error";
    if (!sy?.worklog_id) return t.minutes > 0 ? "new" : (t.legacy ? "changed" : "synced");
    if (t.minutes !== sy.synced_minutes || t.legacy || (t.dhash && sy.desc_hash !== t.dhash)) return "changed";   // auch wenn sich nur ein Text geändert hat
    return "synced";
  }

  async function resolveKey(key, hint) {
    if (hint?.wi && hint?.pp) return { id: hint.wi, project: hint.pp };
    const wi = await request("GET", `${ws()}/work-items/${encodeURIComponent(key)}/`);
    const project = wi?.project || wi?.project_id;
    if (!wi?.id || !project) throw new Error(`Work-Item ${key} nicht gefunden`);
    run("UPDATE tasks SET plane_work_item_id=?, plane_project_id=? WHERE UPPER(TRIM(jira_ticket))=?", wi.id, project, key);
    if (wi.name) run("UPDATE tasks SET title=? WHERE UPPER(TRIM(jira_ticket))=? AND (title IS NULL OR UPPER(TRIM(title))=?)", wi.name, key, key);
    return { id: wi.id, project, name: wi.name };
  }
  const del404 = p => request("DELETE", p).catch(err => { if (err.status !== 404) throw err; });

  async function pushTicket(t) {
    const key = t.ticket;
    try {
      const wi = await resolveKey(key, { wi: t.wi || t.sync?.work_item_id, pp: t.pp || t.sync?.project_id });
      // 1) Altbestand aus v5.0.0 (ein Worklog je Eintrag) aufräumen
      for (const e of all(`SELECT e.id, e.plane_worklog_id, e.plane_work_item_id, e.plane_project_id FROM time_entries e JOIN tasks t ON t.id=e.task_id
                           WHERE e.plane_worklog_id IS NOT NULL AND COALESCE(e.source,'manual') <> 'plane' AND UPPER(TRIM(t.jira_ticket))=?`, key)) {
        await del404(wlPath(e.plane_project_id || wi.project, e.plane_work_item_id || wi.id, e.plane_worklog_id));
        run("UPDATE time_entries SET plane_worklog_id=NULL, plane_work_item_id=NULL, plane_project_id=NULL, plane_hash=NULL WHERE id=?", e.id);
      }
      let sy = t.sync || {};
      let wlId = sy.worklog_id;
      if (wlId && sy.work_item_id && sy.work_item_id !== wi.id) { await del404(wlPath(sy.project_id, sy.work_item_id, wlId)); wlId = null; }

      // 2) Summe ist 0 → Summen-Worklog entfernen
      if (!t.minutes) {
        if (wlId) await del404(wlPath(wi.project, wi.id, wlId));
        run("DELETE FROM plane_ticket_sync WHERE ticket=?", key);
        run("UPDATE time_entries SET plane_sum_key=NULL WHERE plane_sum_key=?", key);
        return { ticket: key, status: "removed" };
      }

      const body = { duration: t.minutes, description: t.desc || sumDescription(t) };
      if (wlId) {
        try { await request("PATCH", wlPath(wi.project, wi.id, wlId), body); }
        catch (err) { if (err.status === 404) wlId = null; else throw err; }
      }
      if (!wlId) {
        // vorhandene eigene Summe wiederverwenden (z.B. nach Neuinstallation) statt doppelt anzulegen
        const meId = cfg().meId || (await me())?.id;
        const ex = (await listAll(wlPath(wi.project, wi.id))).find(w =>
          (w.description || "").includes(SUM_TAG) && (!meId || [w.logged_by, w.logged_by_id, w.created_by, w.created_by_id].includes(meId)));
        if (ex) { wlId = ex.id; await request("PATCH", wlPath(wi.project, wi.id, wlId), body); }
        else {
          const r = await request("POST", wlPath(wi.project, wi.id), body);
          wlId = r?.id;
          if (!wlId) throw new Error("Plane hat keine Worklog-ID zurückgegeben");
        }
      }
      run(`INSERT INTO plane_ticket_sync (ticket,work_item_id,project_id,worklog_id,synced_minutes,synced_at,error,title)
           VALUES (?,?,?,?,?,?,NULL,?) ON CONFLICT(ticket) DO UPDATE SET work_item_id=excluded.work_item_id, project_id=excluded.project_id,
           worklog_id=excluded.worklog_id, synced_minutes=excluded.synced_minutes, synced_at=excluded.synced_at, error=NULL, title=excluded.title`,
        key, wi.id, wi.project, wlId, t.minutes, new Date().toISOString(), t.title || wi.name || null);
      run("UPDATE plane_ticket_sync SET desc_hash=? WHERE ticket=?", t.dhash || descHash(body.description), key);
      // Einträge markieren, die in dieser Summe stecken (für „Plane ✓“ in der Tagesliste)
      // nur die Einträge markieren, die tatsächlich in dieser Summe stecken (Stand beim Berechnen)
      run("UPDATE time_entries SET plane_sum_key=NULL WHERE plane_sum_key=?", key);
      const ids = String(t.ids || "").split(",").map(Number).filter(Boolean);
      for (let i = 0; i < ids.length; i += 400) {
        const part = ids.slice(i, i + 400);
        run(`UPDATE time_entries SET plane_sum_key=?, plane_sum_secs=duration_seconds WHERE id IN (${part.map(() => "?").join(",")})`, key, ...part);
      }
      return { ticket: key, status: "synced", minutes: t.minutes };
    } catch (err) {
      run(`INSERT INTO plane_ticket_sync (ticket,error,title) VALUES (?,?,?) ON CONFLICT(ticket) DO UPDATE SET error=excluded.error`,
        key, err.message.slice(0, 300), t.title || null);
      writeLog("error", LOG, `${key}: ${err.message}`);
      return { ticket: key, status: "error", error: err.message };
    }
  }

  let pushing = false, pushAgain = false;
  async function push({ tickets } = {}) {
    if (pushing) { pushAgain = true; return { busy: true, results: [], deletions: [], ok: 0, errors: 0 }; }
    pushing = true;
    try {
      // Altbestand ohne gültiges Ticket (Ticket entfernt/Aufgabe gelöscht): alte Einzel-Worklogs zur Löschung vormerken
      for (const e of all(`SELECT e.id FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id
                           WHERE e.plane_worklog_id IS NOT NULL AND COALESCE(e.source,'manual') <> 'plane'
                             AND (t.id IS NULL OR t.jira_ticket IS NULL OR TRIM(t.jira_ticket)='')`)) {
        beforeDelete(e.id);
        run("UPDATE time_entries SET plane_worklog_id=NULL, plane_work_item_id=NULL, plane_project_id=NULL WHERE id=?", e.id);
      }
      const sums = localSums();
      const want = tickets?.length ? new Set(tickets.map(k => String(k).toUpperCase())) : null;
      const results = [];
      for (const t of Object.values(sums)) {
        if (want && !want.has(t.ticket)) continue;
        if (t.sync?.error) run("UPDATE plane_ticket_sync SET error=NULL WHERE ticket=?", t.ticket), t.sync.error = null;
        if (ticketStatus(t) !== "synced") results.push(await pushTicket(t));
      }
      // direkt in Plane gebuchte Einträge (source='plane'), die lokal geändert wurden
      if (!want) for (const e of entryRows("e.source='plane' AND e.plane_worklog_id IS NOT NULL"))
        if (statusOf(e) === "changed") results.push({ ticket: e.ticket, ...(await pushOne(e)) });
      const deletions = want ? [] : await processDeletions();
      const ok = results.filter(r => r.status === "synced" || r.status === "removed").length, bad = results.filter(r => r.status === "error").length;
      if (results.length || deletions.length)
        writeLog(bad ? "warn" : "success", LOG, `Übertragen: ${ok} Tickets ok, ${bad} Fehler, ${deletions.filter(d => d.status === "deleted").length} gelöscht`);
      return { results, deletions, ok, errors: bad };
    } finally {
      pushing = false;
      if (pushAgain) { pushAgain = false; scheduleAuto(); }
    }
  }

  /* ── Hooks für server.js ────────────────────────────────────────── */
  let autoTimer = null;
  function scheduleAuto() {
    if (cfg().mode !== "auto" || !configured(cfg())) return;
    clearTimeout(autoTimer);
    autoTimer = setTimeout(() => {
      push({}).catch(e => writeLog("warn", LOG, "Auto-Sync: " + e.message));
    }, 8000);
  }
  function beforeDelete(entryId) {
    const e = one("SELECT * FROM time_entries WHERE id=?", entryId);
    // Normale Einträge stecken in der Ticket-Summe → nichts vormerken, die Summe wird kleiner.
    // Nur eigene Plane-Worklogs (direkt gebucht oder Altbestand) müssen einzeln gelöscht werden.
    if (e?.plane_worklog_id)
      run("INSERT INTO plane_deletions (worklog_id,work_item_id,project_id,label) VALUES (?,?,?,?)",
        e.plane_worklog_id, e.plane_work_item_id, e.plane_project_id, `${e.date} ${(e.comment || "").slice(0, 60)}`);
  }

  /* ── Routen ─────────────────────────────────────────────────────── */
  const wrap = fn => (req, res) => Promise.resolve(fn(req, res)).catch(e => res.status(e.status && e.status < 500 ? 400 : 500).json({ error: e.message }));

  app.get("/api/integrations/plane/status", wrap(async (_, res) => {
    const c = cfg();
    if (!configured(c)) return res.json({ configured: false, mode: c.mode, message: "Noch nicht eingerichtet" });
    const u = await me();
    const projects = list(await request("GET", `${ws()}/projects/`));
    res.json({ configured: true, connected: true, mode: c.mode,
      user: { id: u?.id, name: u?.display_name || u?.first_name || u?.email, email: u?.email },
      projects: projects.length,
      message: `Verbunden als ${u?.display_name || u?.email || "?"} – ${projects.length} Projekte im Workspace „${c.ws}“` });
  }));

  app.get("/api/integrations/plane/projects", wrap(async (_, res) => {
    const remote = list(await request("GET", `${ws()}/projects/`));
    const locals = all("SELECT id,name,jira_key,plane_project_id,plane_identifier,status FROM projects");
    res.json(remote.map(p => {
      const local = locals.find(l => l.plane_project_id === p.id)
        || locals.find(l => !l.plane_project_id && (l.plane_identifier || l.jira_key || "").toUpperCase() === (p.identifier || "").toUpperCase());
      return { id: p.id, name: p.name, identifier: p.identifier, local_id: local?.id || null, local_name: local?.name || null,
               linked: !!(local && local.plane_project_id === p.id) };
    }));
  }));

  /* Verknüpfen: {links:[{plane_project_id, identifier, local_id|null, create:true}]} */
  app.post("/api/integrations/plane/projects/link", wrap(async (req, res) => {
    const links = req.body?.links || [];
    let linked = 0, created = 0;
    // v6.8.0: läuft über dieselbe Verknüpfung wie Projekte › Quellen (eine Wahrheit: project_sources)
    for (const l of links) {
      if (!l.plane_project_id) continue;
      let id = l.local_id ? Number(l.local_id) : null;
      if (!id && l.create && l.name) {
        id = Number(run("INSERT INTO projects (name,status) VALUES (?,'active')", l.name).lastInsertRowid);
        created++;
      }
      hooks.sources.linkBy("plane", l.plane_project_id, id, l.identifier, l.name);
      if (id) linked++;
    }
    writeLog("info", LOG, `Projekte verknüpft: ${linked} (neu angelegt: ${created})`);
    res.json({ ok: true, linked, created });
  }));

  /* Meine offenen Work-Items (mir zugewiesen, nicht erledigt) – 5 Minuten zwischengespeichert */
  let myCache = { at: 0, items: null, key: "" };
  const stateCache = {};
  async function statesOf(pid) {
    if (stateCache[pid] && Date.now() - stateCache[pid].at < 3600e3) return stateCache[pid].map;
    const map = Object.fromEntries(list(await request("GET", `${ws()}/projects/${pid}/states/`)).map(s => [s.id, { group: s.group, name: s.name }]));
    stateCache[pid] = { at: Date.now(), map };
    return map;
  }
  /* „Meine Work-Items“: nur EINE Abfrage gleichzeitig; ältere Liste sofort zurückgeben und im Hintergrund auffrischen */
  let myInflight = null;
  app.get("/api/integrations/plane/my-items", wrap(async (req, res) => {
    const c = cfg(), ck = `${c.base}|${c.ws}|${c.key.slice(-6)}`;
    if (!myCache.items) { try { const c0 = JSON.parse(getSettings().plane_my_items_cache || "null"); if (c0?.items) myCache = c0; } catch (_) {} }   // nach Neustart sofort da
    const have = myCache.items && myCache.key === ck;
    if (have && !req.query.refresh && Date.now() - myCache.at < 5 * 60e3) return res.json(myCache.items);
    if (!myInflight) myInflight = loadMyItems(c, ck).catch(e => { writeLog("warn", LOG, `Meine Work-Items: ${e.message}`); return null; }).finally(() => { myInflight = null; });
    if (have && Date.now() - myCache.at < 60 * 60e3) return res.json(myCache.items);   // veraltet, aber sofort
    const r = await myInflight; res.json(r || myCache.items || []);
  }));
  async function loadMyItems(c, ck) {
    const meId = c.meId || (await me())?.id;
    const projects = all("SELECT id, name, plane_project_id, plane_identifier, jira_key FROM projects WHERE plane_project_id IS NOT NULL AND COALESCE(muted,0)=0");
    const out = [];
    for (const p of projects) {
      const states = await statesOf(p.plane_project_id).catch(e => { writeLog("warn", LOG, `Status-Liste ${p.name}: ${e.message}`); return null; });
      if (!states) continue;
      let cursor = "";
      for (let page = 0; page < 6; page++) {
        const r = await request("GET", `${ws()}/projects/${p.plane_project_id}/work-items/?per_page=100&order_by=-updated_at${cursor ? "&cursor=" + encodeURIComponent(cursor) : ""}`);
        for (const i of list(r)) {
          const ass = (i.assignees || []).map(a => typeof a === "string" ? a : a?.id);
          const st = states[typeof i.state === "string" ? i.state : i.state?.id] || {};
          if (!ass.includes(meId) || ["completed", "cancelled"].includes(st.group)) continue;
          out.push({ id: i.id, key: `${(p.plane_identifier || p.jira_key || "").toUpperCase()}-${i.sequence_id}`, name: i.name,
                     state: st.name || null, updated_at: i.updated_at, plane_project_id: p.plane_project_id, local_project_id: p.id, mine: true });
        }
        if (Array.isArray(r) || !r?.next_page_results || !r.next_cursor) break;
        cursor = r.next_cursor;
      }
    }
    out.sort((a, b) => String(b.updated_at || "").localeCompare(String(a.updated_at || "")));
    myCache = { at: Date.now(), items: out.slice(0, 40), key: ck };
    try { run("INSERT OR REPLACE INTO settings (key,value) VALUES ('plane_my_items_cache',?)", JSON.stringify(myCache)); } catch (_) {}
    return myCache.items;
  }

  /* Neues Work-Item direkt in Plane anlegen (mir zugewiesen) + lokale Aufgabe dazu */
  const escHtml = t => String(t || "").replace(/[&<>"]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  /* Work-Item in Plane anlegen (zugewiesen an mich) – gemeinsam für Dialog und automatische Anlage */
  async function createWorkItem(p, title, description) {
    const meId = cfg().meId || (await me())?.id;
    const body = { name: title, assignees: meId ? [meId] : [] };
    const d = String(description || "").trim();
    if (d) body.description_html = d.split(/\n{2,}/).map(par => `<p>${escHtml(par).replace(/\n/g, "<br>")}</p>`).join("");
    const wi = await request("POST", `${ws()}/projects/${p.plane_project_id}/work-items/`, body);
    if (!wi?.id) throw new Error("Plane hat kein Work-Item zurückgegeben");
    const ident = (p.plane_identifier || p.jira_key || "").toUpperCase();
    const key = wi.sequence_id ? `${ident}-${wi.sequence_id}` : null;
    myCache.at = 0;   // „Meine Work-Items“ neu laden
    writeLog("success", LOG, `Work-Item angelegt: ${key || wi.id} „${title}“`);
    return { wi, key, name: wi.name || title };
  }
  /* Lokale Aufgabe ohne Ticket in einem Plane-Projekt → Work-Item in Plane anlegen und Aufgabe verknüpfen.
     Gibt bei Erfolg die aktualisierte Aufgabe zurück, sonst null (Aufgabe bleibt lokal). */
  async function linkTaskToPlane(taskId) {
    const t = one("SELECT * FROM tasks WHERE id=?", taskId);
    if (!t || t.jira_ticket || t.plane_work_item_id) return null;
    const p = one("SELECT * FROM projects WHERE id=? AND plane_project_id IS NOT NULL", t.project_id);
    if (!p || !configured(cfg())) return null;
    const r = await createWorkItem(p, t.title, t.description);
    run("UPDATE tasks SET title=?, jira_ticket=?, plane_work_item_id=?, plane_project_id=? WHERE id=?",
      r.name, r.key, r.wi.id, p.plane_project_id, taskId);
    return one("SELECT * FROM tasks WHERE id=?", taskId);
  }
  app.post("/api/integrations/plane/work-items", wrap(async (req, res) => {
    const { local_project_id, name, description } = req.body || {};
    const title = String(name || "").trim();
    if (!title) return res.status(400).json({ error: "Titel fehlt" });
    if (title.length > 255) return res.status(400).json({ error: "Titel ist zu lang (max. 255 Zeichen)" });
    const p = one("SELECT * FROM projects WHERE id=? AND plane_project_id IS NOT NULL", Number(local_project_id));
    if (!p) return res.status(400).json({ error: "Projekt ist nicht mit Plane verknüpft (Einstellungen → Plane)" });
    const { wi, key, name: wname } = await createWorkItem(p, title, description);
    const r = run("INSERT INTO tasks (project_id,title,jira_ticket,status,plane_work_item_id,plane_project_id) VALUES (?,?,?,'open',?,?)",
      p.id, wname, key, wi.id, p.plane_project_id);
    res.json({ key, name: wname, id: wi.id, task_id: Number(r.lastInsertRowid), local_project_id: p.id });
  }));
  /* Lokale Aufgaben ohne Ticket in Plane-Projekten (zum Nachholen) */
  app.get("/api/integrations/plane/local-only", (req, res) => {
    res.json(all(`SELECT t.id, t.title, t.project_id, p.name AS project_name, p.plane_identifier
      FROM tasks t JOIN projects p ON p.id=t.project_id
      WHERE p.plane_project_id IS NOT NULL AND (t.jira_ticket IS NULL OR TRIM(t.jira_ticket)='') AND t.plane_work_item_id IS NULL
        AND t.status<>'done' ORDER BY t.id DESC LIMIT 50`));
  });
  app.post("/api/integrations/plane/local-only/:id/push", wrap(async (req, res) => {
    const t = await linkTaskToPlane(Number(req.params.id));
    if (!t) return res.status(400).json({ error: "Aufgabe hat schon ein Ticket oder ihr Projekt ist nicht mit Plane verknüpft" });
    res.json(t);
  }));


  /* ═══ Zuordnung Jira-Ticket (Tempo-Altdaten) → Plane-Work-Item ═══════════
     Das Zeitprotokoll hält die Zeit; Plane bekommt sie als Summe pro Work-Item.
     Tempo-Importe tragen den Jira-Key. Hier wird je Ticket festgelegt, an welches
     Plane-Work-Item die Zeit gehört. Danach läuft die normale Plane-Übertragung. */
  const words = t => String(t || "").toLowerCase().replace(/[^a-z0-9äöüß ]+/g, " ").split(/\s+/).filter(w => w.length >= 3);
  const similarity = (a, b) => { const A = new Set(words(a)), B = new Set(words(b)); if (!A.size || !B.size) return 0;
    let inter = 0; for (const w of A) if (B.has(w)) inter++; return inter / (A.size + B.size - inter); };

  function mapRows() {
    const rows = all(`SELECT UPPER(TRIM(t.jira_ticket)) AS jira_key, MAX(t.title) AS title, COUNT(e.id) AS entries, SUM(e.duration_seconds) AS secs,
                             MIN(e.date) AS first, MAX(e.date) AS last, GROUP_CONCAT(DISTINCT p.name) AS projects
                      FROM time_entries e JOIN tasks t ON t.id=e.task_id LEFT JOIN projects p ON p.id=e.project_id
                      WHERE e.source IN ('tempo','tempo_csv') AND COALESCE(e.plane_include,0)=0 AND e.end_time IS NOT NULL AND e.duration_seconds>0
                        AND t.jira_ticket IS NOT NULL AND TRIM(t.jira_ticket)<>''
                      GROUP BY UPPER(TRIM(t.jira_ticket))`).filter(r => KEY_RE.test(r.jira_key));
    const maps = Object.fromEntries(all("SELECT * FROM ticket_map").map(m => [m.jira_key, m]));
    return rows.map(r => ({ jira_key: r.jira_key, title: r.title && r.title.toUpperCase() !== r.jira_key ? r.title : null, entries: r.entries,
      minutes: Math.round((r.secs || 0) / 60), first: r.first, last: r.last, projects: r.projects, status: maps[r.jira_key]?.status || "open" }))
      .sort((a, b) => b.minutes - a.minutes);
  }

  app.get("/api/integrations/plane/map", (req, res) => {
    const open = mapRows();
    const mapped = all(`SELECT m.*, (SELECT COUNT(*) FROM time_entries e JOIN tasks t ON t.id=e.task_id WHERE t.jira_origin=m.jira_key AND e.plane_include=1) AS entries,
                               (SELECT COALESCE(SUM(e.duration_seconds),0) FROM time_entries e JOIN tasks t ON t.id=e.task_id WHERE t.jira_origin=m.jira_key AND e.plane_include=1) AS secs
                        FROM ticket_map m WHERE m.status='mapped' ORDER BY m.mapped_at DESC`)
      .map(m => ({ jira_key: m.jira_key, plane_key: m.plane_key, title: m.title, entries: m.entries, minutes: Math.round((m.secs || 0) / 60), mapped_at: m.mapped_at }));
    res.json({ configured: configured(cfg()), open: open.filter(r => r.status === "open"), skipped: open.filter(r => r.status === "skipped"), mapped,
      projects: all("SELECT id,name,plane_identifier FROM projects WHERE plane_project_id IS NOT NULL AND COALESCE(status,'') <> 'archived' ORDER BY name") });
  });

  /* Vorschläge für ein Jira-Ticket: gleicher Key in Plane + Suche nach Titel/Key */
  app.get("/api/integrations/plane/map/suggest", wrap(async (req, res) => {
    const key = String(req.query.key || "").trim().toUpperCase();
    if (!KEY_RE.test(key)) return res.status(400).json({ error: "Ungültiger Ticket-Key" });
    const title = String(req.query.title || "").trim();
    const found = new Map();
    const add = (id, k, name, via, base) => {
      if (!id || !k) return;
      const sim = title ? similarity(title, name) : 0;
      const keyInName = name && name.toUpperCase().includes(key) ? 0.4 : 0;
      const score = Math.min(1, base + sim * 0.6 + keyInName);
      const cur = found.get(id);
      if (!cur || score > cur.score) found.set(id, { id, key: k, name, via, score: Math.round(score * 100) / 100 });
    };
    try {
      const wi = await request("GET", `${ws()}/work-items/${encodeURIComponent(key)}/`);
      if (wi?.id) add(wi.id, key, wi.name, "gleicher Key in Plane", 0.2);
    } catch (e) { if (e.status !== 404) throw e; }
    const queries = [key]; if (words(title).length) queries.push(words(title).slice(0, 6).join(" "));
    for (const q of queries) {
      const r = await request("GET", `${ws()}/work-items/search/?search=${encodeURIComponent(q)}&limit=6&workspace_search=true`);
      for (const i of list(r)) add(i.id, `${(i.project__identifier || "").toUpperCase()}-${i.sequence_id}`, i.name, q === key ? "Key im Text" : "ähnlicher Titel", 0);
    }
    res.json([...found.values()].sort((a, b) => b.score - a.score).slice(0, 5));
  }));

  async function applyMap(jiraKey, planeKey) {
    let wi;
    try { wi = await request("GET", `${ws()}/work-items/${encodeURIComponent(planeKey)}/`); }
    catch (e) { if (e.status === 404) throw Object.assign(new Error(`Work-Item ${planeKey} gibt es in Plane nicht`), { status: 404 }); throw e; }
    const project = wi?.project || wi?.project_id;
    if (!wi?.id || !project) throw Object.assign(new Error(`Work-Item ${planeKey} nicht gefunden`), { status: 404 });
    const taskIds = all("SELECT id FROM tasks WHERE UPPER(TRIM(jira_ticket))=?", jiraKey).map(t => t.id);
    if (!taskIds.length) throw new Error(`Keine Aufgabe zu ${jiraKey} gefunden`);
    const q = taskIds.map(() => "?").join(",");
    run(`UPDATE tasks SET jira_origin=COALESCE(jira_origin, jira_ticket), jira_ticket=?, plane_work_item_id=?, plane_project_id=?,
         title=CASE WHEN title IS NULL OR UPPER(TRIM(title))=? THEN ? ELSE title END WHERE id IN (${q})`,
      planeKey, wi.id, project, jiraKey, wi.name || planeKey, ...taskIds);
    run(`UPDATE time_entries SET plane_include=1, plane_sum_key=NULL WHERE task_id IN (${q}) AND source IN ('tempo','tempo_csv')`, ...taskIds);
    run("INSERT OR REPLACE INTO ticket_map (jira_key,status,plane_key,plane_work_item_id,plane_project_id,title) VALUES (?,?,?,?,?,?)",
      jiraKey, "mapped", planeKey, wi.id, project, wi.name || null);
    return { jira_key: jiraKey, plane_key: planeKey, name: wi.name };
  }

  app.post("/api/integrations/plane/map/apply", wrap(async (req, res) => {
    const b = req.body || {};
    const jiraKey = String(b.jira_key || "").trim().toUpperCase();
    if (!KEY_RE.test(jiraKey)) return res.status(400).json({ error: "Ungültiger Jira-Key" });
    if (b.undo) {
      const ids = all("SELECT id FROM tasks WHERE jira_origin=?", jiraKey).map(t => t.id);
      if (ids.length) {
        const q = ids.map(() => "?").join(",");
        run(`UPDATE time_entries SET plane_include=0, plane_sum_key=NULL WHERE task_id IN (${q}) AND source IN ('tempo','tempo_csv')`, ...ids);
        run(`UPDATE tasks SET jira_ticket=jira_origin, jira_origin=NULL, plane_work_item_id=NULL, plane_project_id=NULL WHERE id IN (${q})`, ...ids);
      }
      run("DELETE FROM ticket_map WHERE jira_key=?", jiraKey);
      return res.json({ ok: true });
    }
    if (b.skip) {
      run("INSERT OR REPLACE INTO ticket_map (jira_key,status) VALUES (?,'skipped')", jiraKey);
      return res.json({ ok: true });
    }
    let planeKey = String(b.plane_key || "").trim().toUpperCase();
    if (b.create) {
      const p = one("SELECT * FROM projects WHERE id=? AND plane_project_id IS NOT NULL", Number(b.local_project_id));
      if (!p) return res.status(400).json({ error: "Projekt für das neue Work-Item wählen (mit Plane verknüpft)" });
      const t = one("SELECT MAX(title) AS title FROM tasks WHERE UPPER(TRIM(jira_ticket))=?", jiraKey);
      const title = t?.title && t.title.toUpperCase() !== jiraKey ? t.title : `${jiraKey} (aus Jira übernommen)`;
      const c = await createWorkItem(p, title, `Übernommen aus Jira: ${jiraKey}`);
      planeKey = c.key;
    }
    if (!KEY_RE.test(planeKey)) return res.status(400).json({ error: "Plane-Key im Format ABC-123 angeben" });
    const out = await applyMap(jiraKey, planeKey);
    writeLog("success", LOG, `Zuordnung: ${jiraKey} → ${planeKey}`);
    res.json(out);
  }));

  app.get("/api/integrations/plane/search", wrap(async (req, res) => {
    const q = (req.query.q || "").trim();
    if (q.length < 2) return res.json([]);
    const r = await request("GET", `${ws()}/work-items/search/?search=${encodeURIComponent(q)}&limit=15&workspace_search=true`, null, { interactive: true });
    const locals = all("SELECT id,jira_key,plane_project_id,plane_identifier FROM projects");
    res.json(list(r).map(i => {
      const ident = i.project__identifier || "";
      const local = locals.find(l => l.plane_project_id === i.project_id)
        || locals.find(l => (l.plane_identifier || l.jira_key || "").toUpperCase() === ident.toUpperCase());
      return { id: i.id, key: `${ident}-${i.sequence_id}`, name: i.name, plane_project_id: i.project_id, local_project_id: local?.id || null };
    }));
  }));

  /* ═══ Ticket-Ansicht (wie bei Jira): suchen, ansehen, kommentieren, Status, Timer ═══ */
  const stripHtml = h => String(h || "").replace(/<br\s*\/?>/gi, "\n").replace(/<\/(p|li|h\d|div)>/gi, "\n").replace(/<li[^>]*>/gi, "• ")
    .replace(/<[^>]+>/g, "").replace(/&nbsp;/g, " ").replace(/&amp;/g, "&").replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, '"').replace(/\n{3,}/g, "\n\n").trim();
  function planeSums(keys) {
    if (!keys.length) return {};
    const q = keys.map(() => "?").join(",");
    const out = {};
    for (const r of all(`SELECT UPPER(t.jira_ticket) AS k, t.id AS task_id, SUM(COALESCE(e.duration_seconds,0)) AS secs FROM tasks t LEFT JOIN time_entries e ON e.task_id=t.id
                         WHERE UPPER(t.jira_ticket) IN (${q}) AND t.plane_work_item_id IS NOT NULL GROUP BY t.id`, ...keys)) {
      const o = out[r.k] ||= { secs: 0, task_id: r.task_id }; o.secs += r.secs || 0; }
    return out;
  }
  async function shapeItem(i, p, meId) {
    const states = await statesOf(i.project || i.project_id || p.plane_project_id).catch(() => ({}));
    const st = states[typeof i.state === "string" ? i.state : i.state?.id] || {};
    const ident = (p?.plane_identifier || p?.jira_key || i.project__identifier || "").toUpperCase();
    const ass = (i.assignees || []).map(a => typeof a === "string" ? a : a?.id);
    return { id: i.id, key: `${ident}-${i.sequence_id}`, summary: i.name, status: st.name || null, state_group: st.group || null, priority: ({ urgent: "Dringend", high: "Hoch", medium: "Mittel", low: "Niedrig" })[i.priority] || null,
      project_name: p?.name || ident, plane_project_id: i.project || i.project_id || p?.plane_project_id, local_project_id: p?.id || null, updated: i.updated_at, mine: !!meId && ass.includes(meId),
      due: i.target_date || null };
  }
  app.get("/api/plane/tickets", wrap(async (req, res) => {
    const c = cfg(); const q = String(req.query.q || "").trim(); const mine = req.query.mine !== "0", open = req.query.open !== "0";
    const meId = c.meId || (await me())?.id;
    const projects = all("SELECT id, name, plane_project_id, plane_identifier, jira_key FROM projects WHERE plane_project_id IS NOT NULL AND COALESCE(muted,0)=0");
    let items = [];
    if (q) {
      const r = await request("GET", `${ws()}/work-items/search/?search=${encodeURIComponent(q)}&limit=15&workspace_search=true`, null, { interactive: true });
      for (const hit of list(r).slice(0, 15)) {
        const p = projects.find(x => x.plane_project_id === hit.project_id);
        let wi = hit; try { wi = await request("GET", `${ws()}/projects/${hit.project_id}/work-items/${hit.id}/`); } catch (_) {}
        items.push(await shapeItem({ ...wi, project__identifier: hit.project__identifier }, p || { plane_identifier: hit.project__identifier, plane_project_id: hit.project_id }, meId));
      }
    } else {
      for (const p of projects) {
        let cursor = "";
        for (let page = 0; page < 4; page++) {
          const r = await request("GET", `${ws()}/projects/${p.plane_project_id}/work-items/?per_page=100&order_by=-updated_at${cursor ? "&cursor=" + encodeURIComponent(cursor) : ""}`);
          for (const i of list(r)) items.push(await shapeItem(i, p, meId));
          if (Array.isArray(r) || !r?.next_page_results || !r.next_cursor) break;
          cursor = r.next_cursor;
        }
      }
    }
    if (mine && !q) items = items.filter(i => i.mine);
    if (open) items = items.filter(i => !["completed", "cancelled"].includes(i.state_group));
    items.sort((a, b) => String(b.updated || "").localeCompare(String(a.updated || "")));
    items = items.slice(0, 200);
    const sums = planeSums(items.map(i => i.key));
    res.json({ total: items.length, issues: items.map(i => ({ ...i, booked_seconds: sums[i.key]?.secs || 0, task_id: sums[i.key]?.task_id || null })), base: c.base, ws: c.ws });
  }));
  async function workItemByKey(key) {
    const wi = await request("GET", `${ws()}/work-items/${encodeURIComponent(key)}/`);
    const pid = wi.project || wi.project_id;
    const p = one("SELECT * FROM projects WHERE plane_project_id=?", pid);
    return { wi, pid, p };
  }
  app.get("/api/plane/ticket/:key", wrap(async (req, res) => {
    const key = String(req.params.key).toUpperCase();
    const { wi, pid, p } = await workItemByKey(key);
    const c = cfg(); const meId = c.meId || null;
    const item = await shapeItem(wi, p || { plane_project_id: pid, plane_identifier: key.split("-")[0] }, meId);
    const states = await statesOf(pid).catch(() => ({}));
    let comments = [];
    try { comments = list(await request("GET", `${ws()}/projects/${pid}/work-items/${wi.id}/comments/`)).slice(-20)
      .map(x => ({ id: x.id, author: x.actor_detail?.display_name || x.created_by_detail?.display_name || "", created: x.created_at, body: stripHtml(x.comment_html || x.comment_stripped || "") })); } catch (_) {}
    let members = {};
    try { members = Object.fromEntries(list(await request("GET", `${ws()}/members/`)).map(m => [m.id || m.member?.id, m.display_name || m.member?.display_name || m.first_name])); } catch (_) {}
    comments = comments.map(x => ({ ...x, author: x.author || "" }));
    const sums = planeSums([key]);
    const entries = all(`SELECT e.id, e.date, e.start_time, e.end_time, e.duration_seconds, e.comment FROM time_entries e JOIN tasks t ON t.id=e.task_id
                         WHERE UPPER(t.jira_ticket)=? AND t.plane_work_item_id IS NOT NULL ORDER BY e.start_time DESC LIMIT 30`, key);
    const ass = (wi.assignees || []).map(a => typeof a === "string" ? members[a] || a : a?.display_name).filter(Boolean);
    res.json({ ...item, description: stripHtml(wi.description_html || wi.description_stripped || ""), assignee: ass.join(", ") || null,
      states: Object.entries(states).map(([id, s]) => ({ id, name: s.name, group: s.group })), state_id: typeof wi.state === "string" ? wi.state : wi.state?.id,
      comments, entries, booked_seconds: sums[key]?.secs || 0, url: `${c.base}/${c.ws}/browse/${key}/` });
  }));
  app.post("/api/plane/ticket/:key/comment", wrap(async (req, res) => {
    const key = String(req.params.key).toUpperCase(), body = String(req.body?.body || "").trim();
    if (!body) return res.status(400).json({ error: "Kommentar fehlt" });
    const { wi, pid } = await workItemByKey(key);
    await request("POST", `${ws()}/projects/${pid}/work-items/${wi.id}/comments/`, { comment_html: body.split(/\n{2,}/).map(par => `<p>${escHtml(par).replace(/\n/g, "<br>")}</p>`).join("") });
    writeLog("info", LOG, `Kommentar zu ${key} geschrieben`);
    res.json({ ok: true });
  }));
  app.post("/api/plane/ticket/:key/state", wrap(async (req, res) => {
    const key = String(req.params.key).toUpperCase();
    const { wi, pid } = await workItemByKey(key);
    await request("PATCH", `${ws()}/projects/${pid}/work-items/${wi.id}/`, { state: String(req.body?.state_id || "") });
    myCache.at = 0;
    writeLog("info", LOG, `Status von ${key} geändert (${req.body?.name || ""})`);
    res.json({ ok: true });
  }));
  /* Plane-Adresse ohne Ticketnummer (…/projects/<uuid>/issues/<uuid> oder ?peekId=<uuid>) → „PRJA-581“ + Titel.
     Für Browser-Erkennung und Add-on; nur lesen, mit Zwischenspeicher. */
  const resolveCache = new Map();
  app.get("/api/plane/resolve", wrap(async (req, res) => {
    const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
    const pid = String(req.query.project || ""), iid = String(req.query.item || "");
    if (!uuid.test(iid) || (pid && !uuid.test(pid))) return res.status(400).json({ error: "Work-Item-Kennung nötig" });
    const ck = (pid || "*") + "/" + iid; const hit = resolveCache.get(ck); if (hit && Date.now() - hit.at < 30 * 60e3) return hit.v.error ? res.status(404).json(hit.v) : res.json(hit.v);
    // zuerst lokal: kennen wir das Work-Item schon? (keine Plane-Anfrage)
    const loc = one("SELECT jira_ticket, title FROM tasks WHERE plane_work_item_id=? AND jira_ticket IS NOT NULL LIMIT 1", iid);
    if (loc) { const v = { key: String(loc.jira_ticket).toUpperCase(), title: loc.title || "" }; resolveCache.set(ck, { at: Date.now(), v }); return res.json(v); }
    // Halbe Ansicht (Vorschau) in „Meine Work-Items“, Ansichten usw.: Adresse ohne Projekt → in den verknüpften Plane-Projekten suchen
    let wi = null, pidFound = pid;
    if (pid) wi = await request("GET", `${ws()}/projects/${pid}/work-items/${iid}/`);
    else for (const p of all("SELECT plane_project_id FROM projects WHERE plane_project_id IS NOT NULL")) {
      try { wi = await request("GET", `${ws()}/projects/${p.plane_project_id}/work-items/${iid}/`); pidFound = p.plane_project_id; break; } catch (_) {}
    }
    if (!wi) { const v = { error: "Work-Item in den verknüpften Plane-Projekten nicht gefunden" }; resolveCache.set(ck, { at: Date.now() - 20 * 60e3, v }); return res.status(404).json(v); }   // „nicht gefunden“ 10 min merken
    const pid2 = pidFound;
    let ident = one("SELECT plane_identifier FROM projects WHERE plane_project_id=?", pid2)?.plane_identifier;
    if (!ident) { try { ident = (await request("GET", `${ws()}/projects/${pid2}/`))?.identifier; } catch (_) {} }
    if (!ident || wi?.sequence_id == null) return res.status(404).json({ error: "Work-Item nicht gefunden" });
    const v = { key: `${String(ident).toUpperCase()}-${wi.sequence_id}`, title: wi.name || "" };
    resolveCache.set(ck, { at: Date.now(), v }); res.json(v);
  }));
  /* Ticket für die Zeiterfassung auswählen → lokale Aufgabe sicherstellen */
  app.post("/api/plane/ticket/:key/task", wrap(async (req, res) => {
    const key = String(req.params.key).toUpperCase();
    let t = one("SELECT * FROM tasks WHERE UPPER(jira_ticket)=? AND plane_work_item_id IS NOT NULL ORDER BY id LIMIT 1", key);
    if (t) return res.json({ task_id: t.id, project_id: t.project_id });
    const { wi, pid, p } = await workItemByKey(key);
    if (!p) return res.status(400).json({ error: "Das Plane-Projekt ist noch keinem lokalen Projekt zugeordnet (Einstellungen › Plane › Projekte verknüpfen)" });
    const r = run("INSERT INTO tasks (project_id,title,jira_ticket,status,plane_work_item_id,plane_project_id) VALUES (?,?,?,'open',?,?)", p.id, wi.name, key, wi.id, pid);
    res.json({ task_id: Number(r.lastInsertRowid), project_id: p.id });
  }));

  /* Freigabe-Übersicht: eine Zeile je Ticket (lokal vs. Plane) */
  app.get("/api/integrations/plane/outbox", (req, res) => {
    const tickets = Object.values(localSums()).map(t => ({
      ticket: t.ticket, title: t.title || t.sync?.title || null, projects: t.projects || null, entries: t.n,
      local_minutes: t.minutes, plane_minutes: t.sync?.worklog_id ? t.sync.synced_minutes : 0,
      first: t.first || null, last: t.last || null, legacy: t.legacy, status: ticketStatus(t),
      error: t.sync?.error || null, synced_at: t.sync?.synced_at || null,
    })).sort((a, b) => (b.last || "").localeCompare(a.last || "") || a.ticket.localeCompare(b.ticket));
    const counts = tickets.reduce((a, r) => (a[r.status] = (a[r.status] || 0) + 1, a), {});
    const since = localDateOf(Date.now() - 31 * 864e5);
    const noTicket = one(`SELECT COUNT(*) AS n, COALESCE(SUM(e.duration_seconds),0) AS s FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id
                          WHERE e.date>=? AND e.end_time IS NOT NULL AND COALESCE(e.source,'manual') NOT IN ('tempo','tempo_csv','plane')
                            AND (t.jira_ticket IS NULL OR TRIM(t.jira_ticket)='')`, since);
    const skipped = entryRows("COALESCE(e.plane_skip,0)=1 ORDER BY e.start_time DESC LIMIT 100").map(e => ({
      id: e.id, date: e.date, start_time: e.start_time, end_time: e.end_time, minutes: Math.round((e.duration_seconds || 0) / 60),
      ticket: e.ticket, comment: e.comment, project_name: e.project_name }));
    const deletions = all("SELECT * FROM plane_deletions ORDER BY id");
    res.json({ mode: cfg().mode, configured: configured(cfg()), counts, tickets, deletions, skipped,
      without_ticket: { count: noTicket?.n || 0, minutes: Math.round((noTicket?.s || 0) / 60), since } });
  });

  /* Einträge eines Tickets (Aufklappen in der Übersicht) */
  app.get("/api/integrations/plane/outbox/:ticket", (req, res) => {
    const key = String(req.params.ticket).toUpperCase();
    res.json(entryRows(`UPPER(TRIM(t.jira_ticket))=? ORDER BY e.start_time DESC`, key).map(e => ({
      id: e.id, date: e.date, start_time: e.start_time, end_time: e.end_time, minutes: Math.round((e.duration_seconds || 0) / 60),
      comment: e.comment, project_name: e.project_name, source: e.source || "manual", skipped: !!e.plane_skip,
      counted: !!e.end_time && !e.plane_skip && (!["tempo", "tempo_csv", "plane"].includes(e.source) || (e.source !== "plane" && !!e.plane_include)),
      in_plane: e.plane_sum_key === key && e.plane_sum_secs === e.duration_seconds })));
  });

  app.post("/api/integrations/plane/push", wrap(async (req, res) => res.json(await push(req.body || {}))));

  /* Einträge (ids) oder ganze Tickets (tickets) von der Übertragung ausnehmen */
  app.post("/api/integrations/plane/skip", (req, res) => {
    const { ids = [], tickets = [], skip = true } = req.body || {};
    for (const id of ids) run("UPDATE time_entries SET plane_skip=?, plane_error=NULL WHERE id=?", skip ? 1 : 0, Number(id));
    for (const k of tickets) run(`UPDATE time_entries SET plane_skip=? WHERE task_id IN (SELECT id FROM tasks WHERE UPPER(TRIM(jira_ticket))=?)`, skip ? 1 : 0, String(k).toUpperCase());
    scheduleAuto();
    res.json({ ok: true });
  });

  app.delete("/api/integrations/plane/deletions/:id", (req, res) => {
    run("DELETE FROM plane_deletions WHERE id=?", Number(req.params.id));
    res.json({ ok: true });
  });

  /* PULL: Worklogs aus Plane (nur eigene) in den Tracker holen */
  app.post("/api/integrations/plane/pull", wrap(async (req, res) => {
    const { from = "0000-00-00", to = "9999-12-31", dryRun = false } = req.body || {};
    const u = await me();
    const stats = { checked: 0, imported: 0, linked: 0, skipped_known: 0, skipped_other_user: 0, skipped_range: 0, errors: [] };
    const projects = all("SELECT * FROM projects WHERE plane_project_id IS NOT NULL");
    for (const p of projects) {
      let totals;
      try { totals = list(await request("GET", `${ws()}/projects/${p.plane_project_id}/total-worklogs/`)); }
      catch (e) { stats.errors.push(`${p.name}: ${e.message}`); continue; }
      for (const t of totals.filter(t => (t.duration || 0) > 0)) {
        const wiId = t.issue_id || t.work_item_id;
        let worklogs;
        try { worklogs = await listAll(wlPath(p.plane_project_id, wiId)); }
        catch (e) { stats.errors.push(`${p.name}/${wiId}: ${e.message}`); continue; }
        let wi = null;
        for (const w of worklogs) {
          stats.checked++;
          const owner = w.logged_by || w.logged_by_id || w.created_by || w.created_by_id;
          if (!u?.id || !owner || owner !== u.id) { stats.skipped_other_user++; continue; }
          if (one("SELECT id FROM time_entries WHERE plane_worklog_id=?", w.id)) { stats.skipped_known++; continue; }
          const desc = w.description || "";
          // eigene Summen-Worklogs: nie als Einträge importieren, aber (z.B. nach Neuinstallation) wieder zuordnen
          if (desc.includes(SUM_TAG)) {
            if (!one("SELECT ticket FROM plane_ticket_sync WHERE worklog_id=?", w.id)) {
              if (!wi) { try { wi = await request("GET", `${ws()}/projects/${p.plane_project_id}/work-items/${wiId}/`); } catch (_) { wi = { id: wiId }; } }
              const k = wi.sequence_id ? `${p.plane_identifier || p.jira_key}-${wi.sequence_id}`.toUpperCase() : null;
              if (k && !dryRun) run(`INSERT INTO plane_ticket_sync (ticket,work_item_id,project_id,worklog_id,synced_minutes,synced_at,title)
                VALUES (?,?,?,?,?,?,?) ON CONFLICT(ticket) DO UPDATE SET work_item_id=excluded.work_item_id, project_id=excluded.project_id,
                worklog_id=excluded.worklog_id, synced_minutes=excluded.synced_minutes`, k, wiId, p.plane_project_id, w.id, Number(w.duration) || 0, new Date().toISOString(), wi.name || null);
              stats.linked++;
            } else stats.skipped_known++;
            continue;
          }
          // Eigene, früher übertragene Einträge wiedererkennen
          const zp = /\[zp#(\d+)\]/.exec(desc);
          if (zp && one("SELECT id FROM time_entries WHERE id=? AND plane_worklog_id IS NULL AND ABS(ROUND(duration_seconds/60.0) - ?) <= 1", Number(zp[1]), Number(w.duration) || 0)) {
            if (!dryRun) {
              run("UPDATE time_entries SET plane_worklog_id=?, plane_work_item_id=?, plane_project_id=?, plane_synced_at=? WHERE id=?",
                w.id, wiId, p.plane_project_id, new Date().toISOString(), Number(zp[1]));
              const e = entryRows("e.id=?", Number(zp[1]))[0];
              if (e) run("UPDATE time_entries SET plane_hash=? WHERE id=?", hashOf(e, wiId) === `${w.duration}|${desc}|${wiId}` ? hashOf(e, wiId) : "remote", e.id);
            }
            stats.linked++; continue;
          }
          // Datum/Zeit: aus Beschreibung „YYYY-MM-DD HH:MM–HH:MM“ oder created_at
          const mins = Math.max(1, Number(w.duration) || 0);
          const m = /(\d{4}-\d{2}-\d{2}) (\d{2}:\d{2})(?:[–-](\d{2}:\d{2}))?/.exec(desc);
          let start, end;
          if (m) { start = new Date(`${m[1]}T${m[2]}:00`); end = new Date(start.getTime() + mins * 60000); }
          else { end = new Date(w.created_at); start = new Date(end.getTime() - mins * 60000); }
          const date = localDateOf(start.getTime());
          if (date < from || date > to) { stats.skipped_range++; continue; }
          if (!wi) {
            try { wi = await request("GET", `${ws()}/projects/${p.plane_project_id}/work-items/${wiId}/`); }
            catch (_) { wi = { id: wiId, name: "Plane-Work-Item" }; }
          }
          const key = wi.sequence_id ? `${p.plane_identifier || p.jira_key}-${wi.sequence_id}`.toUpperCase() : null;
          if (dryRun) { stats.imported++; continue; }
          let task = key ? one("SELECT * FROM tasks WHERE UPPER(jira_ticket)=? AND project_id=?", key, p.id) : null;
          if (!task) {
            const r = run("INSERT INTO tasks (project_id,title,jira_ticket,status,plane_work_item_id,plane_project_id) VALUES (?,?,?,'open',?,?)",
              p.id, wi.name || key || "Plane", key, wiId, p.plane_project_id);
            task = { id: Number(r.lastInsertRowid) };
          }
          const comment = desc.replace(/\s*·\s*\d{4}-\d{2}-\d{2}.*$/, "").trim() || null;
          const r = run(`INSERT INTO time_entries (project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,source,
                           plane_worklog_id,plane_work_item_id,plane_project_id,plane_synced_at)
                         VALUES (?,?,?,?,?,?,1,?,'plane',?,?,?,?)`,
            p.id, task.id, start.toISOString(), end.toISOString(), mins * 60, comment, date, w.id, wiId, p.plane_project_id, new Date().toISOString());
          const e = entryRows("e.id=?", Number(r.lastInsertRowid))[0];
          run("UPDATE time_entries SET plane_hash=? WHERE id=?", hashOf(e, wiId), e.id);
          stats.imported++;
        }
      }
    }
    writeLog(stats.errors.length ? "warn" : "success", LOG,
      `${dryRun ? "[TESTLAUF] " : ""}Abgleich aus Plane: ${stats.imported} neu, ${stats.linked} verknüpft, ${stats.skipped_known} bekannt`);
    res.json(stats);
  }));

  const api = { configured: () => configured(cfg()), projects: async () => list(await request("GET", `${ws()}/projects/`)),
    // Felder eines Work-Items setzen (z. B. start_date, target_date) – für To-dos
    setFields: async (key, fields) => { const { wi, pid } = await workItemByKey(String(key).toUpperCase()); await request("PATCH", `${ws()}/projects/${pid}/work-items/${wi.id}/`, fields); myCache.at = 0; } };
  return { init, beforeDelete, scheduleAuto, statusOf, linkTaskToPlane, api };
};
