/* ═══════════════════════════════════════════════════════════════════
   Datenschutz & Transportsicherheit (v6.1.0)
   ───────────────────────────────────────────────────────────────────
   Zeitdaten sind personenbezogen und sensibel. Deshalb:
     • Verbindungen zu Jira/Tempo/Plane prüfen das TLS-Zertifikat.
       Firmen-CAs werden automatisch aus dem System übernommen
       (macOS-Schlüsselbund bzw. /etc/pki, /etc/ssl auf Red Hat/Debian),
       zusätzlich optional eine eigene CA-Datei (Einstellung tls_ca_file
       oder NODE_EXTRA_CA_CERTS). Unsicher nur, wenn ausdrücklich erlaubt.
     • Datenbank, Sicherungen und Protokolle nur für den eigenen Benutzer
       lesbar (Ordner 700, Dateien 600).
     • Der Server hört nur auf 127.0.0.1 (lokal).
═══════════════════════════════════════════════════════════════════ */
const fs = require("fs");
const path = require("path");
const tls = require("tls");
const https = require("https");
const { execFileSync } = require("child_process");
const crypto = require("crypto");

let systemCas = null, systemCaSource = "";
function loadSystemCas() {
  if (systemCas) return systemCas;
  const pems = [];
  try {
    if (process.platform === "darwin") {
      const out = execFileSync("/usr/bin/security", ["find-certificate", "-a", "-p", "/Library/Keychains/System.keychain", "/System/Library/Keychains/SystemRootCertificates.keychain"],
        { encoding: "utf8", timeout: 15000, maxBuffer: 32 * 1024 * 1024 });
      pems.push(...(out.match(/-----BEGIN CERTIFICATE-----[\s\S]+?-----END CERTIFICATE-----/g) || []));
      // auch Anmelde-Schlüsselbund (dort liegen Firmen-CAs oft) – nur CA-Zertifikate übernehmen
      try {
        const own = execFileSync("/usr/bin/security", ["find-certificate", "-a", "-p"], { encoding: "utf8", timeout: 15000, maxBuffer: 32 * 1024 * 1024 });
        for (const p of own.match(/-----BEGIN CERTIFICATE-----[\s\S]+?-----END CERTIFICATE-----/g) || []) {
          try { if (new crypto.X509Certificate(p).ca) pems.push(p); } catch (_) {}
        }
      } catch (_) {}
      systemCaSource = "macOS-Schlüsselbund (System + Anmeldung)";
    } else {
      for (const f of ["/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/certs/ca-certificates.crt", "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem"]) {
        if (fs.existsSync(f)) { pems.push(...(fs.readFileSync(f, "utf8").match(/-----BEGIN CERTIFICATE-----[\s\S]+?-----END CERTIFICATE-----/g) || [])); systemCaSource = f; break; }
      }
    }
  } catch (_) {}
  systemCas = pems;
  return pems;
}

module.exports = function createSecure({ getSettings, writeLog, DB_PATH, BAK_DIR, saveSetting }) {
  const ctxSaveSetting = (k, v) => saveSetting(k, v);
  let agentCache = { key: null, agent: null, info: null };

  /* Vom Server nicht mitgeschickte Zwischenzertifikate (wie der Browser über „CA Issuers“ nachladen).
     Es werden NUR Zwischenzertifikate übernommen – die Kette muss weiterhin bei einer vertrauten Root-CA enden. */
  const CHAIN_FILE = path.join(path.dirname(DB_PATH), "zwischenzertifikate.pem");
  const readPems = f => { try { return fs.readFileSync(f, "utf8").match(/-----BEGIN CERTIFICATE-----[\s\S]+?-----END CERTIFICATE-----/g) || []; } catch (_) { return []; } };
  const CHAIN_ERR = /unable to verify the first certificate|unable to get (local )?issuer certificate|self.signed certificate in certificate chain|UNABLE_TO_VERIFY_LEAF_SIGNATURE|UNABLE_TO_GET_ISSUER_CERT/i;
  const repaired = new Map();
  const TRUST_FILE = path.join(path.dirname(DB_PATH), "vertraute-ca.pem");
  let rootCandidate = null;                                   // host → Zeitpunkt (nicht ständig erneut versuchen)
  function peerChain(host, port) {
    return new Promise((resolve, reject) => {
      const sock = tls.connect({ host, port, servername: host, rejectUnauthorized: false, timeout: 10000 }, () => {
        const c = sock.getPeerCertificate(true); sock.end();
        const chain = []; let cur = c; const seen = new Set();
        while (cur && cur.raw && !seen.has(cur.fingerprint256)) { seen.add(cur.fingerprint256); chain.push(cur); cur = cur.issuerCertificate; }
        resolve(chain);
      });
      sock.on("error", reject); sock.on("timeout", () => { sock.destroy(); reject(new Error("Zeitüberschreitung")); });
    });
  }
  async function repairChain(urlStr) {
    const u = new URL(urlStr); const host = u.hostname, port = Number(u.port) || 443;
    const last = repaired.get(host); if (last && Date.now() - last.at < 10 * 60e3) return { added: 0, skipped: true, missingRoot: last.missingRoot };
    repaired.set(host, { at: Date.now(), missingRoot: null });
    const chain = await peerChain(host, port);
    if (!chain.length) return { added: 0 };
    let cert = new crypto.X509Certificate(chain[chain.length - 1].raw);
    const have = new Set(readPems(CHAIN_FILE).map(p => new crypto.X509Certificate(p).fingerprint256));
    const added = []; let missingRoot = null;
    for (let i = 0; i < 4; i++) {
      if (cert.issuer === cert.subject) break;                              // selbstsigniert = Root → nie automatisch vertrauen
      const m = /CA Issuers - URI:(\S+)/.exec(cert.infoAccess || ""); if (!m) break;
      const r = await fetch(m[1], { signal: AbortSignal.timeout(10000) }); if (!r.ok) break;
      const buf = Buffer.from(await r.arrayBuffer());
      let issuer; try { issuer = new crypto.X509Certificate(buf); } catch (_) { issuer = new crypto.X509Certificate(buf.toString("utf8")); }
      if (!cert.checkIssued(issuer) || !cert.verify(issuer.publicKey)) break;   // muss wirklich der Aussteller sein
      if (issuer.issuer === issuer.subject) {                               // Root: nur vormerken – vertrauen muss der Mensch (Fingerabdruck prüfen)
        missingRoot = issuer.subject.replace(/\n/g, ", ");
        rootCandidate = { host, subject: missingRoot, fingerprint: issuer.fingerprint256, valid_to: issuer.validTo, pem: issuer.toString() };
        break;
      }
      if (!have.has(issuer.fingerprint256)) { added.push(issuer.toString()); have.add(issuer.fingerprint256); }
      cert = issuer;
    }
    if (added.length) {
      fs.appendFileSync(CHAIN_FILE, added.join("\n") + "\n", { mode: 0o600 });
      agentCache.key = null;                                                // Agent neu aufbauen
      writeLog("info", "security", `${added.length} fehlende(s) Zwischenzertifikat(e) für ${host} nachgeladen`);
    }
    repaired.set(host, { at: Date.now(), missingRoot });
    return { added: added.length, missingRoot };
  }
  /* Anfrage einmal wiederholen, wenn nur das Zwischenzertifikat fehlte */
  async function withChainRepair(urlStr, fn) {
    try { return await fn(); }
    catch (e) {
      if (e?.code === "PIN_MISMATCH") { try { await captureServer(urlStr); } catch (_) {} throw e; }
      if (!CHAIN_ERR.test(String(e?.message || e?.code || "")) || !/^https:/.test(urlStr)) throw e;
      let r; try { r = await repairChain(urlStr); } catch (re) { e.message += ` (Zwischenzertifikat nachladen fehlgeschlagen: ${re.message})`; throw e; }
      const rootMsg = () => `Firmen-Root-Zertifikat „${r.missingRoot}“ ist auf diesem Rechner nicht als vertrauenswürdig hinterlegt – unter Einstellungen › Jira & Tempo das Zertifikat prüfen und mit einem Klick vertrauen`;
      if (r.added) { try { return await fn(); } catch (e2) { if (r.missingRoot && CHAIN_ERR.test(e2.message)) e2.message = rootMsg(); throw e2; } }
      if (r.missingRoot) { try { await captureServer(urlStr); } catch (_) {} e.message = rootMsg(); throw e; }
      try { await captureServer(urlStr); } catch (_) {}
      e.message = `Die Zertifikatskette von ${new URL(urlStr).hostname} ist unvollständig – unter Einstellungen › Jira & Tempo das Server-Zertifikat prüfen und mit einem Klick vertrauen`;
      throw e;
    }
  }

  function tlsConfig() {
    const s = getSettings();
    const insecure = s.tls_insecure === "1";
    const file = (s.tls_ca_file || "").trim();
    return { insecure, file };
  }
  /* Gemeinsamer HTTPS-Agent für alle Verbindungen nach außen */
  function agent() {
    const c = tlsConfig();
    let fileMtime = 0; try { if (c.file) fileMtime = fs.statSync(c.file).mtimeMs; } catch (_) {}
    const key = `${c.insecure}|${c.file}|${fileMtime}`;
    if (agentCache.key === key) return agentCache.agent;
    const ca = [...tls.rootCertificates, ...loadSystemCas(), ...readPems(CHAIN_FILE), ...readPems(TRUST_FILE)];
    let extra = 0, fileErr = null;
    if (c.file) { try {
      const buf = fs.readFileSync(c.file.replace(/^~(?=\/)/, require("os").homedir()));
      let p = buf.toString("utf8").match(/-----BEGIN CERTIFICATE-----[\s\S]+?-----END CERTIFICATE-----/g) || [];
      if (!p.length) p = [new crypto.X509Certificate(buf).toString()];          // .cer/.der aus Safari/Schlüsselbund
      ca.push(...p); extra = p.length; } catch (e) { fileErr = e.message; } }
    const a = new https.Agent({ keepAlive: true, maxSockets: 8, ca, rejectUnauthorized: !c.insecure });
    agentCache = { key, agent: a, info: { insecure: c.insecure, system_cas: (systemCas || []).length, system_source: systemCaSource, file: c.file || null, file_certs: extra, file_error: fileErr } };
    if (c.insecure) writeLog("warn", "security", "TLS-Zertifikatsprüfung ist AUSGESCHALTET (Einstellung tls_insecure) – nur zum Testen verwenden");
    return a;
  }
  /* Optionen für https.request: Agent mit Prüfung */
  /* Fest hinterlegtes Server-Zertifikat („Pinning“): für interne Server, deren Zertifikatskette
     unvollständig ist. Vertraut wird GENAU diesem Zertifikat (Fingerabdruck) – ändert es sich,
     wird die Verbindung abgelehnt, bevor irgendetwas (z. B. das Token) gesendet wird. */
  const pins = () => { try { return JSON.parse(getSettings().tls_pins || "{}"); } catch (_) { return {}; } };
  const pinnedAgents = new Map();
  class PinnedAgent extends https.Agent {
    constructor(host, fp) { super({ keepAlive: true, maxSockets: 8 }); this.pinHost = host; this.pinFp = fp; }
    createConnection(options, oncreate) {
      const sock = tls.connect({ ...options, rejectUnauthorized: false });
      let done = false; const finish = (err) => { if (done) return; done = true; if (err) { sock.destroy(); oncreate(err); } else oncreate(null, sock); };
      sock.once("secureConnect", () => {
        const cert = sock.getPeerCertificate();
        if (!cert || cert.fingerprint256 !== this.pinFp) return finish(Object.assign(new Error(`Das Zertifikat von ${this.pinHost} hat sich geändert (neu: ${cert?.fingerprint256 || "?"}) – Verbindung aus Sicherheitsgründen abgelehnt. Unter Einstellungen › Jira & Tempo neu bestätigen.`), { code: "PIN_MISMATCH" }));
        const idErr = tls.checkServerIdentity(this.pinHost, cert); if (idErr) return finish(idErr);
        if (Date.parse(cert.valid_to) < Date.now()) return finish(new Error(`Zertifikat von ${this.pinHost} ist abgelaufen (${cert.valid_to})`));
        finish(null);
      });
      sock.once("error", e => finish(e));
      return undefined;                                                     // Socket erst nach erfolgreicher Prüfung herausgeben
    }
  }
  function requestOptions(urlStr) {
    let host = null; try { host = new URL(urlStr).hostname; } catch (_) {}
    const fp = host && pins()[host];
    if (fp && !tlsConfig().insecure) {
      let a = pinnedAgents.get(host);
      if (!a || a.pinFp !== fp) { a = new PinnedAgent(host, fp); pinnedAgents.set(host, a); }
      return { agent: a };
    }
    return { agent: agent() };
  }
  let serverCandidate = null;
  async function captureServer(urlStr) {
    const u = new URL(urlStr); const chain = await peerChain(u.hostname, Number(u.port) || 443);
    const leaf = chain[0]; if (!leaf) return null;
    serverCandidate = { host: u.hostname, subject: leaf.subject?.CN || "", issuer: leaf.issuer?.CN || "", fingerprint: leaf.fingerprint256, valid_to: leaf.valid_to };
    return serverCandidate;
  }
  function trustServer(host, fingerprint) {
    if (!serverCandidate || serverCandidate.host !== host || serverCandidate.fingerprint !== fingerprint) throw new Error("Kein passender Kandidat – bitte erst „Verbindung testen“");
    const p = pins(); p[host] = fingerprint;
    ctxSaveSetting("tls_pins", JSON.stringify(p));
    writeLog("warn", "security", `Server-Zertifikat von ${host} (SHA-256 ${fingerprint}) von Hand als vertrauenswürdig festgelegt`);
    serverCandidate = null; return { ok: true };
  }
  function untrustServer(host) { const p = pins(); delete p[host]; ctxSaveSetting("tls_pins", JSON.stringify(p)); pinnedAgents.delete(host); return { ok: true }; }
  const tlsHint = e => /self.signed|unable to (get|verify)|certificate|CERT_/i.test(String(e?.message || e?.code || ""))
    && !/Root-Zertifikat/.test(String(e?.message)) ? " – Zertifikat nicht vertrauenswürdig: Firmen-CA im Schlüsselbund (System) installieren oder unter Einstellungen › Jira & Tempo › Tempo-Übertragung › Verbindungssicherheit eine CA-Datei (PEM) hinterlegen" : "";

  /* Dateirechte: nur der eigene Benutzer */
  function harden() {
    const res = { fixed: 0, errors: [] };
    const fix = (p, mode) => { try { if (!fs.existsSync(p)) return; const cur = fs.statSync(p).mode & 0o777; if (cur !== mode) { fs.chmodSync(p, mode); res.fixed++; } } catch (e) { res.errors.push(`${p}: ${e.message}`); } };
    const dir = path.dirname(DB_PATH);
    fix(dir, 0o700); fix(BAK_DIR, 0o700);
    for (const f of [DB_PATH, DB_PATH + "-wal", DB_PATH + "-shm", path.join(dir, "audit.log"), CHAIN_FILE, TRUST_FILE]) fix(f, 0o600);
    try { for (const f of fs.readdirSync(BAK_DIR)) fix(path.join(BAK_DIR, f), 0o600); } catch (_) {}
    return res;
  }
  /* Für die Selbstprüfung */
  function status() {
    agent();
    const info = agentCache.info;
    let disk = { ok: null, detail: "nicht prüfbar" };
    try {
      if (process.platform === "darwin") {
        const out = execFileSync("/usr/bin/fdesetup", ["status"], { encoding: "utf8", timeout: 5000 });
        disk = /FileVault is On/i.test(out) ? { ok: true, detail: "FileVault ist eingeschaltet" } : { ok: false, detail: "FileVault ist AUS – Systemeinstellungen › Datenschutz & Sicherheit › FileVault einschalten" };
      } else if (process.platform === "linux") {
        const out = execFileSync("lsblk", ["-o", "TYPE", "-n"], { encoding: "utf8", timeout: 5000 });
        disk = /crypt/.test(out) ? { ok: true, detail: "verschlüsseltes Laufwerk (LUKS) vorhanden" } : { ok: null, detail: "keine LUKS-Verschlüsselung erkannt – bei Container-/Cloud-Speicher Verschlüsselung der Plattform prüfen" };
      }
    } catch (_) {}
    const modes = [];
    const dir = path.dirname(DB_PATH);
    for (const [p, want] of [[dir, 0o700], [DB_PATH, 0o600], [BAK_DIR, 0o700]]) { try { const m = fs.statSync(p).mode & 0o777; if (m & 0o077) modes.push(`${path.basename(p)} ${m.toString(8)}`); } catch (_) {} }
    return { tls: info, disk, perms_ok: !modes.length, perms_open: modes };
  }
  harden();
  setInterval(harden, 60 * 60 * 1000);
  /* Root-Kandidat anzeigen / bewusst vertrauen */
  function serverInfo() { return { candidate: serverCandidate, pins: pins() }; }
  function candidate() { return rootCandidate ? { host: rootCandidate.host, subject: rootCandidate.subject, fingerprint: rootCandidate.fingerprint, valid_to: rootCandidate.valid_to } : null; }
  function trustRoot(fingerprint) {
    if (!rootCandidate || rootCandidate.fingerprint !== fingerprint) throw new Error("Kein passender Kandidat – bitte erst „Verbindung testen“");
    fs.appendFileSync(TRUST_FILE, rootCandidate.pem + "\n", { mode: 0o600 });
    writeLog("warn", "security", `Root-Zertifikat „${rootCandidate.subject}“ (SHA-256 ${fingerprint}) von Hand als vertrauenswürdig hinterlegt`);
    rootCandidate = null; agentCache.key = null; repaired.clear();
    return { ok: true };
  }
  function trusted() { return readPems(TRUST_FILE).map(p => { const c = new crypto.X509Certificate(p); return { subject: c.subject.replace(/\n/g, ", "), fingerprint: c.fingerprint256, valid_to: c.validTo }; }); }
  return { agent, requestOptions, tlsHint, harden, status, withChainRepair, repairChain, candidate, trustRoot, trusted, serverInfo, trustServer, untrustServer };
};
