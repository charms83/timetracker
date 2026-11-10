/* ═══════════════════════════════════════════════════════════════════
   Zugang zur lokalen Schnittstelle (v6.20.0)
   ───────────────────────────────────────────────────────────────────
   Wer darf mit dem Zeitprotokoll-Server auf 127.0.0.1 sprechen?
   • eigene Oberfläche (http://127.0.0.1:PORT / localhost)       → ja
   • Mac-App (Tracker-Fenster, file://)                          → nur mit App-Schlüssel
     (~/.zeitprotokoll/app-token, 0600; die App setzt ihn selbst)
   • Browser-Add-on (moz-extension:// / chrome-extension://)     → nur gekoppelt (eigener Schlüssel,
     einmal in der Oberfläche erlaubt, jederzeit widerrufbar; gespeichert nur als SHA-256)
   • fremde Webseiten                                            → nie
   • Host-Header muss 127.0.0.1/localhost sein (Schutz vor DNS-Rebinding)
   Programme ohne Browser (curl, Terminal-Erweiterung) bleiben erlaubt – sie
   laufen ohnehin als derselbe Benutzer auf diesem Rechner.
═══════════════════════════════════════════════════════════════════ */
const crypto = require("crypto");
const fs = require("fs");
const path = require("path");

const LOCAL_ORIGIN = /^https?:\/\/(localhost|127\.0\.0\.1)(:\d+)?$/;
const EXT_ORIGIN = /^(moz-extension|chrome-extension|safari-web-extension):\/\/[a-z0-9-]+$/i;
const LOCAL_HOST = /^(localhost|127\.0\.0\.1)(:\d+)?$/;
const sha = s => crypto.createHash("sha256").update(String(s)).digest("hex");
const eq = (a, b) => { try { return a.length === b.length && crypto.timingSafeEqual(Buffer.from(a), Buffer.from(b)); } catch (_) { return false; } };

module.exports = function createPairing({ PORT, getSettings, saveSetting, writeLog }) {
  /* App-Schlüssel für die Mac-App (stabil, Datei nur für den Benutzer lesbar) */
  const TOKEN_FILE = path.join(process.env.HOME || "/tmp", ".zeitprotokoll", "app-token");
  let appToken = null;
  try { appToken = fs.readFileSync(TOKEN_FILE, "utf8").trim(); } catch (_) {}
  if (!appToken || appToken.length < 32) {
    appToken = crypto.randomBytes(32).toString("hex");
    try { fs.mkdirSync(path.dirname(TOKEN_FILE), { recursive: true }); fs.writeFileSync(TOKEN_FILE, appToken, { mode: 0o600 }); } catch (_) {}
  }
  try { fs.chmodSync(TOKEN_FILE, 0o600); } catch (_) {}

  const clients = () => { try { return JSON.parse(getSettings().paired_clients || "[]"); } catch (_) { return []; } };
  const saveClients = l => saveSetting("paired_clients", JSON.stringify(l));
  const pending = new Map();   // nonce → {client, ext_version, origin, created, token?, denied?}
  const sweep = () => { for (const [k, v] of pending) if (Date.now() - v.created > 5 * 60e3) pending.delete(k); };
  let lastTouch = {};
  function clientFor(token) {
    if (!token) return null;
    const h = sha(token), c = clients().find(x => eq(x.hash, h));
    if (c && (!lastTouch[c.id] || Date.now() - lastTouch[c.id] > 10 * 60e3)) {
      lastTouch[c.id] = Date.now();
      saveClients(clients().map(x => x.id === c.id ? { ...x, last_used: new Date().toISOString() } : x));
    }
    return c || null;
  }

  /* CORS: nur eigene Oberfläche, Mac-App (null) und Add-ons – fremde Seiten bekommen keine Freigabe */
  const corsOptions = {
    origin: (o, cb) => cb(null, !o || o === "null" || LOCAL_ORIGIN.test(o) || EXT_ORIGIN.test(o)),
    allowedHeaders: ["Content-Type", "X-ZP-Token"],
  };

  /* Wächter vor allen Routen */
  function guard(req, res, next) {
    if (!LOCAL_HOST.test(String(req.headers.host || ""))) return res.status(403).json({ error: "Falscher Host" });
    if (req.method === "OPTIONS") return next();
    const o = req.headers.origin, tok = String(req.headers["x-zp-token"] || "");
    req.zpClient = "local";
    if (!o || LOCAL_ORIGIN.test(o)) return next();                       // eigene Oberfläche / Programme ohne Browser
    if (o === "null") {                                                   // Mac-App (file://)
      if (eq(tok, appToken)) { req.zpClient = "app"; return next(); }
      return res.status(403).json({ error: "Kein Zugang (Mac-App neu starten)" });
    }
    if (EXT_ORIGIN.test(o)) {                                             // Browser-Add-on
      if (/^\/api\/pair\/(start|poll)$/.test(req.path)) return next();
      const c = clientFor(tok);
      if (!c) return res.status(401).json({ error: "Add-on nicht (mehr) verbunden", pair: true });
      req.zpClient = "addon:" + c.id;
      if (!/^\/api\/(ext\/state|plane\/resolve|ticket\/start|timer\/(stop|status)|pause\/(start|stop|status)|todo)$/.test(req.path)) return res.status(403).json({ error: "Für das Add-on nicht freigegeben" });
      return next();
    }
    return res.status(403).json({ error: "Fremder Origin" });
  }
  const ownUi = req => !req.headers.origin || LOCAL_ORIGIN.test(req.headers.origin);

  function routes(app, { self }) {
    app.post("/api/pair/start", (req, res) => {
      sweep(); if (pending.size > 20) return res.status(429).json({ error: "Zu viele offene Anfragen" });
      const nonce = crypto.randomBytes(16).toString("hex");
      pending.set(nonce, { client: String(req.body?.client || "Browser").slice(0, 40), ext_version: String(req.body?.ext_version || "").slice(0, 20), origin: String(req.headers.origin || ""), created: Date.now() });
      res.json({ nonce, approve_url: `http://127.0.0.1:${PORT}/#koppeln/${nonce}` });
    });
    app.get("/api/pair/poll", (req, res) => {
      sweep(); const p = pending.get(String(req.query.nonce || ""));
      if (!p || p.denied) { if (p) pending.delete(String(req.query.nonce)); return res.status(404).json({ error: "abgelaufen oder abgelehnt" }); }
      if (!p.token) return res.json({ pending: true });
      pending.delete(String(req.query.nonce)); res.json({ token: p.token });   // genau einmal
    });
    app.get("/api/pair/info", (req, res) => {
      if (!ownUi(req)) return res.status(403).json({ error: "nur in Zeitprotokoll" });
      sweep(); const p = pending.get(String(req.query.nonce || "")); if (!p) return res.status(404).json({ error: "Anfrage abgelaufen – im Add-on erneut „verbinden“ klicken" });
      res.json({ client: p.client, ext_version: p.ext_version, origin: p.origin });
    });
    app.post("/api/pair/approve", (req, res) => {
      if (!ownUi(req)) return res.status(403).json({ error: "nur in Zeitprotokoll" });
      sweep(); const n = String(req.body?.nonce || ""), p = pending.get(n); if (!p) return res.status(404).json({ error: "Anfrage abgelaufen" });
      if (!req.body?.allow) { p.denied = true; writeLog("warn", "zugang", `Add-on-Verbindung abgelehnt (${p.client})`); return res.json({ ok: true }); }
      const token = crypto.randomBytes(32).toString("hex"), id = crypto.randomBytes(4).toString("hex");
      saveClients([...clients(), { id, name: p.client, origin: p.origin, hash: sha(token), created: new Date().toISOString(), last_used: null }]);
      p.token = token;
      writeLog("info", "zugang", `Browser-Add-on verbunden: ${p.client} (${p.origin})`);
      res.json({ ok: true });
    });
    app.get("/api/pair/clients", (req, res) => { if (!ownUi(req) && req.zpClient !== "app") return res.status(403).end(); res.json(clients().map(({ hash, ...c }) => c)); });
    app.delete("/api/pair/clients/:id", (req, res) => {
      if (!ownUi(req)) return res.status(403).end();
      const l = clients(), c = l.find(x => x.id === req.params.id);
      saveClients(l.filter(x => x.id !== req.params.id)); if (c) writeLog("warn", "zugang", `Browser-Add-on getrennt: ${c.name}`);
      res.json({ ok: true });
    });
    /* Kompakter Zustand fürs Add-on */
    app.get("/api/ext/state", async (req, res) => {
      try {
        const [t, p] = await Promise.all([self("GET", "/timer/status"), self("GET", "/pause/status").catch(() => ({}))]);
        const s = getSettings(), host = u => { try { return new URL(u).host; } catch (_) { return null; } };
        res.json({ timer: { active: !!t.active, start_time: t.start_time || t.timer?.start_time || null, ticket: t.task?.jira_ticket || null, title: t.task?.title || t.planTask?.title || t.comment || null, project_name: t.project?.name || null },
          pause: { active: !!p.active, start_time: p.start_time || p.pause?.start_time || null }, hosts: { jira: host(s.jira_base_url), plane: host(s.plane_base_url) } });
      } catch (e) { res.status(500).json({ error: e.message }); }
    });
  }
  return { corsOptions, guard, routes, appTokenFile: TOKEN_FILE };
};
