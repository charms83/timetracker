/* ═══════════════════════════════════════════════════════════════════
   Einstellungen → Server: Version, Status, Datenbank, Sicherungen,
   Plane-Anbindung und alle Terminal-Befehle mit Erklärung
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback } from "react";
import { RefreshCw, Copy, Check, Database, Server, CloudUpload, HardDrive, Terminal, ShieldCheck } from "lucide-react";
import { api, call, isServerMode } from "./zp-ui.jsx";

const dt = iso => iso ? new Date(iso).toLocaleString("de-DE", { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit" }) : "–";
const mb = b => b == null ? "–" : b > 1048576 ? (b / 1048576).toFixed(1).replace(".", ",") + " MB" : Math.round(b / 1024) + " KB";
const dur = s => { const d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60); return d ? `${d} T ${h} h` : h ? `${h} h ${m} min` : `${m} min`; };

function Card({ icon, title, children, right }) {
  return (
    <div className="zp-card" style={{ padding: "14px 16px" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
        <span style={{ color: "var(--accent)", display: "flex" }}>{icon}</span>
        <span style={{ fontWeight: 600, fontSize: 14, flex: 1 }}>{title}</span>
        {right}
      </div>
      {children}
    </div>
  );
}
const Row = ({ k, v, mono, warn }) => (
  <div style={{ display: "flex", gap: 10, padding: "4px 0", borderTop: "1px solid var(--line)", fontSize: 12 }}>
    <span style={{ color: "var(--text2)", minWidth: 130 }}>{k}</span>
    <span style={{ flex: 1, minWidth: 0, wordBreak: "break-all", fontFamily: mono ? "ui-monospace,SFMono-Regular,Menlo,monospace" : "inherit", fontSize: mono ? 11 : 12, color: warn ? "var(--stop)" : "var(--text)" }}>{v ?? "–"}</span>
  </div>
);

function CopyBtn({ text }) {
  const [ok, setOk] = useState(false);
  return (
    <button title="Befehl kopieren" onClick={() => { navigator.clipboard?.writeText(text).then(() => { setOk(true); setTimeout(() => setOk(false), 1500); }); }}
      style={{ border: "1px solid var(--line)", background: "var(--card)", borderRadius: 6, padding: "3px 7px", display: "inline-flex", alignItems: "center", gap: 4, fontSize: 11, color: ok ? "var(--accent)" : "var(--text2)" }}>
      {ok ? <Check size={12} /> : <Copy size={12} />}{ok ? "kopiert" : "kopieren"}
    </button>
  );
}

const PLIST = "~/Library/LaunchAgents/com.user.zeitprotokoll.server.plist";
const COMMANDS = [
  { group: "Server prüfen", items: [
    ["Läuft der Server, welche Version?", "curl -s http://localhost:3747/api/health", "Antwort {\"ok\":true,\"version\":\"…\"}. Keine Antwort = Server läuft nicht. Nach einem Neustart 5 Sekunden warten."],
    ["Status des Startdienstes", "launchctl print gui/$(id -u)/com.user.zeitprotokoll.server | grep -E \"state =|last exit|program =\"", "„state = running“ = läuft. „last exit code“ zeigt, ob er zuletzt abgestürzt ist."],
    ["Fehlerprotokoll ansehen", "tail -30 /tmp/zeitprotokoll-err.log", "Die letzten Fehlermeldungen des Servers. EADDRINUSE = Port 3747 war schon belegt (meist harmlos, alte Instanz)."],
    ["Server-Ausgabe ansehen", "tail -30 /tmp/zeitprotokoll.log", "Normale Ausgabe des Servers, z. B. die Startzeile mit Version."],
  ]},
  { group: "Server steuern", items: [
    ["Server neu starten", `launchctl bootout gui/$(id -u) ${PLIST}\nlaunchctl bootstrap gui/$(id -u) ${PLIST}`, "Nötig nach jedem Update. „Boot-out failed: 5“ ist harmlos (Dienst war nicht geladen)."],
    ["Server stoppen", `launchctl bootout gui/$(id -u) ${PLIST}`, "Beendet den Server bis zum nächsten Start bzw. Neuanmelden. Die App startet ihn sonst automatisch wieder."],
    ["Server von Hand starten (Notlösung)", "cd ~/zeitprotokoll/backend && node server.js", "Läuft nur, solange das Terminal-Fenster offen ist. Beenden mit Ctrl + C."],
  ]},
  { group: "Updates und App", items: [
    ["Update-Paket einspielen", "cd ~/zeitprotokoll/zeitprotokoll-<version> && ./update.sh", "Sichert Code + Datenbank, kopiert neue Dateien, baut die Oberfläche, startet den Server neu."],
    ["Oberfläche neu bauen", "cd ~/zeitprotokoll/frontend && npm run build", "Nur nötig, wenn Dateien in frontend/src geändert wurden. Danach im Browser Cmd + R."],
    ["Mac-App auf Ordner umstellen (einmalig)", "~/zeitprotokoll/desktop/app-umstellen.sh", "Danach lädt die App Lasche und Schnellfenster direkt aus ~/zeitprotokoll/desktop – Updates ohne Neubau."],
    ["Mac-App beenden / starten", "osascript -e 'quit app \"Zeitprotokoll\"'\nopen /Applications/Zeitprotokoll.app", "Neustart der App, z. B. damit das Schnellfenster neue Dateien lädt."],
    ["Mac-App auf Auslieferungsstand zurück", "cd ~/zeitprotokoll/desktop && ./install-app.sh", "Installiert die App aus dem Release-Paket neu (macht die Umstellung rückgängig)."],
  ]},
  { group: "Daten und Sicherung", items: [
    ["Datenbank-Ort", "open ~/Documents/Zeitprotokoll", "Hier liegen zeitprotokoll.db und der Ordner backup. Diesen Ordner nie im laufenden Betrieb verschieben."],
    ["Sicherungen anzeigen", "ls -lt ~/Documents/Zeitprotokoll/backup | head", "Die App behält die letzten 30 Sicherungen. „Jetzt sichern“ oben legt sofort eine an."],
    ["Sicherung zurückspielen", `launchctl bootout gui/$(id -u) ${PLIST}\ncp ~/Documents/Zeitprotokoll/backup/<datei>.db ~/Documents/Zeitprotokoll/zeitprotokoll.db\nlaunchctl bootstrap gui/$(id -u) ${PLIST}`, "Erst Server stoppen, dann Datei ersetzen, dann starten. <datei> durch den Namen der Sicherung ersetzen."],
  ]},
];

/* ═══ Admin-Aktionen: direkt auf der Server-Seite ausführen statt Befehle zu kopieren ═══ */
const GROUPS = ["Prüfen", "Daten", "Update", "Server"];
function waitForServer(then) {
  let tries = 0;
  const t = setInterval(async () => {
    tries++;
    try { const r = await fetch("/api/health", { cache: "no-store" }); if (r.ok) { clearInterval(t); then(); } } catch (_) {}
    if (tries > 40) clearInterval(t);
  }, 1500);
}
function AdminActions({ onDone }) {
  const [info, setInfo] = useState(null);
  const [busy, setBusy] = useState("");
  const [ask, setAsk] = useState("");
  const [res, setRes] = useState(null);
  const [backup, setBackup] = useState("");
  const [waiting, setWaiting] = useState(false);
  useEffect(() => { api("GET", "/admin/actions").then(r => { if (r && !r.error) { setInfo(r); setBackup(r.backups?.[0] || ""); } }); }, []);

  const exec = async (a) => {
    setAsk(""); setBusy(a.id); setRes(null);
    const r = await call("POST", `/admin/actions/${a.id}`, a.needs === "backup" ? { file: backup } : {});
    setBusy("");
    setRes({ ...r, title: a.title, ok: r.ok ?? !r.error, message: r.message || r.error });
    if (r.restart) { setWaiting(true); setTimeout(() => waitForServer(() => location.reload()), 2500); }
    else onDone?.();
  };
  if (!info) return null;
  const click = a => { if (a.confirm) setAsk(a.id); else exec(a); };
  const disabled = a => !!busy || waiting || (a.id === "restart" && !info.launchd) || (a.id.startsWith("git") && !info.git) || (a.needs === "backup" && !backup);

  return (
    <div className="zp-card" style={{ padding: "14px 16px" }}>
      <div style={{ fontWeight: 600, fontSize: 14 }}>Verwaltung</div>
      <div style={{ color: "var(--text2)", fontSize: 12, margin: "2px 0 6px" }}>Alles läuft direkt hier auf dem Server – nichts mehr ins Terminal kopieren.</div>
      {GROUPS.map(g => (
        <div key={g} style={{ marginTop: 10 }}>
          <div style={{ fontSize: 11, fontWeight: 600, color: "var(--text2)", textTransform: "uppercase", letterSpacing: ".04em", marginBottom: 4 }}>{g}</div>
          {info.actions.filter(a => a.group === g).map(a => (
            <div key={a.id} style={{ display: "grid", gridTemplateColumns: "minmax(200px, 260px) 1fr auto", gap: 12, alignItems: "center", borderTop: "1px solid var(--line)", padding: "7px 0" }}>
              <div style={{ fontWeight: 500, fontSize: 13 }}>{a.title}</div>
              <div style={{ color: "var(--text2)", fontSize: 11 }}>
                {a.help}
                {a.id === "restart" && !info.launchd && <span style={{ color: "var(--stop)" }}> Nicht möglich: Der Server läuft nicht unter dem Startdienst.</span>}
                {a.id.startsWith("git") && !info.git && <span style={{ color: "var(--text3)" }}> (Installation ohne Git – hier nicht verfügbar)</span>}
              </div>
              <div style={{ display: "flex", gap: 6, alignItems: "center", justifyContent: "flex-end" }}>
                {a.needs === "backup" && (
                  <select value={backup} onChange={e => setBackup(e.target.value)} style={{ padding: "4px 6px", fontSize: 11, maxWidth: 210 }}>
                    {info.backups.map(b => <option key={b} value={b}>{b.replace(/^zeitprotokoll-/, "").replace(/\.db$/, "")}</option>)}
                  </select>)}
                {ask === a.id ? (<>
                  <span style={{ fontSize: 11, color: a.danger ? "var(--stop)" : "var(--text2)", maxWidth: 240 }}>{a.confirm}</span>
                  <button className="zp-btn-start" style={{ height: 28, fontSize: 12, background: a.danger ? "var(--stop)" : undefined }} onClick={() => exec(a)}>Ja</button>
                  <button style={{ height: 28, fontSize: 12, border: "1px solid var(--line)", background: "var(--card)", borderRadius: 7, padding: "0 10px" }} onClick={() => setAsk("")}>Nein</button>
                </>) : (
                  <button disabled={disabled(a)} onClick={() => click(a)}
                    style={{ height: 28, fontSize: 12, fontWeight: 600, borderRadius: 7, padding: "0 14px", border: "1px solid var(--line)", background: "var(--card)", opacity: disabled(a) ? .45 : 1, whiteSpace: "nowrap" }}>
                    {busy === a.id ? "Läuft…" : "Ausführen"}
                  </button>)}
              </div>
            </div>))}
        </div>))}

      {waiting && <div style={{ marginTop: 12, padding: "10px 12px", background: "var(--accent-soft)", borderRadius: 8, fontSize: 12 }}>⟳ Server startet neu … die Seite lädt automatisch neu, sobald er wieder da ist.</div>}
      {res && !waiting && (
        <div style={{ marginTop: 12, border: "1px solid " + (res.ok ? "var(--line)" : "var(--stop)"), borderRadius: 8, padding: "10px 12px" }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13, fontWeight: 600, color: res.ok ? "var(--accent)" : "var(--stop)" }}>
            <span>{res.ok ? "✓" : "✗"}</span><span>{res.title}: {res.message}</span>
            <span style={{ flex: 1 }} /><button className="zp-nav" onClick={() => setRes(null)} title="Schließen">✕</button>
          </div>
          {res.checks && res.checks.map(c => (
            <div key={c.name} style={{ display: "flex", gap: 8, fontSize: 12, padding: "3px 0", borderTop: "1px solid var(--line)", marginTop: 3 }}>
              <span style={{ width: 14, color: !c.ok ? "var(--stop)" : c.warn ? "#8a6400" : "var(--accent)" }}>{!c.ok ? "✗" : c.warn ? "!" : "✓"}</span>
              <span style={{ width: 120, fontWeight: 500 }}>{c.name}</span>
              <span style={{ flex: 1, color: "var(--text2)" }}>{c.detail}</span>
            </div>))}
          {res.lines && res.lines.length > 0 && (
            <pre style={{ margin: "8px 0 0", background: "var(--bg)", border: "1px solid var(--line)", borderRadius: 6, padding: "6px 8px", fontSize: 11, whiteSpace: "pre-wrap", wordBreak: "break-word", maxHeight: 220, overflow: "auto" }}>{res.lines.join("\n")}</pre>)}
        </div>)}
    </div>
  );
}

/* Automatische Selbstprüfung (Serverstart, alle 6 h, per Knopf) */
export function SelfCheckCard() { return isServerMode() ? null : <SelfCheckCardNode />; }   // Servermodus: Selbsttest liegt unter /admin
function SelfCheckCardNode() {
  const [r, setR] = useState(null); const [busy, setBusy] = useState(false);
  const load = useCallback(() => api("GET", "/selfcheck").then(setR), []);
  useEffect(() => { load(); const t = setInterval(load, 60000); return () => clearInterval(t); }, [load]);
  const runNow = async () => { setBusy(true); setR(await api("POST", "/selfcheck/run")); setBusy(false); };
  const color = !r || r.pending ? "var(--text2)" : r.ok ? "var(--accent)" : "var(--stop)";
  return (
    <Card icon={<ShieldCheck size={16} />} title="Selbstprüfung"
      right={<button className="zp-nav" style={{ width: "auto", padding: "0 10px", fontSize: 12 }} disabled={busy} onClick={runNow}>{busy ? "Prüfe…" : "Jetzt prüfen"}</button>}>
      <div style={{ fontSize: 13, color, fontWeight: 600 }}>{!r ? "…" : r.pending ? "Läuft kurz nach dem Serverstart automatisch" : `${r.ok ? "✓" : "✗"} ${r.message}`}</div>
      {r?.at && <div style={{ fontSize: 11, color: "var(--text2)", marginBottom: 6 }}>{new Date(r.at).toLocaleString("de-DE")} · {r.trigger} · {r.ms} ms · automatisch beim Start und alle 6 Stunden</div>}
      {(r?.checks || []).map(c => (
        <div key={c.name} style={{ display: "flex", gap: 10, padding: "3px 0", borderTop: "1px solid var(--line)", fontSize: 12 }}>
          <span style={{ width: 16, color: !c.ok ? "var(--stop)" : c.warn ? "#b58100" : "var(--accent)" }}>{!c.ok ? "✗" : c.warn ? "!" : "✓"}</span>
          <span style={{ minWidth: 140, color: "var(--text2)" }}>{c.name}</span><span style={{ flex: 1 }}>{c.detail}</span>
        </div>))}
    </Card>);
}

export function ServerView() {
  const [d, setD] = useState(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => {
    const r = await api("GET", "/admin/server-info");
    if (!r || r.error) { setErr(r?.error || "Server nicht erreichbar"); setD(null); } else { setErr(""); setD(r); }
  }, []);
  useEffect(() => { load(); const t = setInterval(load, 30000); return () => clearInterval(t); }, [load]);
  const backup = async () => { setBusy(true); await api("POST", "/admin/backup"); setBusy(false); load(); };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
      {err && <div className="zp-card" style={{ padding: 16, color: "var(--stop)" }}>✗ {err} – Läuft der Server? Notlösung im Terminal siehe unten.</div>}
      <SelfCheckCard />
      <AdminActions onDone={load} />
      {d && (
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(320px, 1fr))", gap: 12 }}>
          <Card icon={<Server size={16} />} title="Server" right={<button className="zp-nav" title="Aktualisieren" onClick={load}><RefreshCw size={14} /></button>}>
            <div style={{ display: "flex", alignItems: "baseline", gap: 10, marginBottom: 8 }}>
              <span style={{ fontSize: 22, fontWeight: 600 }}>v{d.version}</span>
              <span className="zp-pill synced">● läuft</span>
            </div>
            <Row k="Oberfläche" v={`v${d.frontend_version || "?"} · gebaut ${dt(d.frontend_built)}`} />
            <Row k="Läuft seit" v={`${dt(d.started_at)} (${dur(d.uptime_s)})`} />
            <Row k="Adresse" v={`http://localhost:${d.port}`} mono />
            <Row k="Node.js" v={`${d.node} · ${d.platform}`} />
            <Row k="Rechner" v={d.hostname} />
            <Row k="Prozess" v={`PID ${d.pid} · ${d.memory_mb} MB Speicher`} />
            <Row k="Programmordner" v={d.install_dir} mono />
          </Card>
          <Card icon={<Database size={16} />} title="Datenbank">
            <Row k="Datei" v={d.db.path} mono />
            <Row k="Größe" v={`${mb(d.db.size)} · geändert ${dt(d.db.mtime)}`} />
            <Row k="Buchungen" v={`${d.counts.entries} (seit ${d.first_entry ? new Date(d.first_entry + "T12:00").toLocaleDateString("de-DE") : "–"})`} />
            <Row k="Projekte / Aufgaben" v={`${d.counts.projects} / ${d.counts.tasks}`} />
            <Row k="Abwesenheiten" v={d.counts.absences} />
            <Row k="SSH-Sitzungen" v={d.counts.ssh_sessions} />
          </Card>
          <Card icon={<HardDrive size={16} />} title="Sicherungen" right={<button onClick={backup} disabled={busy} className="zp-btn-start" style={{ height: 28, fontSize: 12 }}>{busy ? "Sichere…" : "Jetzt sichern"}</button>}>
            <Row k="Ordner" v={d.backups.dir} mono />
            <Row k="Anzahl" v={`${d.backups.count} (die letzten 30 werden behalten)`} />
            <Row k="Letzte Sicherung" v={d.backups.last ? `${dt(d.backups.last_time)} · ${d.backups.last}` : "noch keine"} warn={!d.backups.last} />
          </Card>
          <Card icon={<CloudUpload size={16} />} title="Plane-Anbindung">
            <Row k="Status" v={d.plane.configured ? "eingerichtet" : "nicht eingerichtet"} warn={!d.plane.configured} />
            <Row k="Adresse / Workspace" v={d.plane.url ? `${d.plane.url} · ${d.plane.workspace}` : "–"} />
            <Row k="Übertragung" v={d.plane.mode === "auto" ? "automatisch" : "manuell freigeben"} />
            <Row k="Tickets mit Summe" v={`${d.plane.tickets}${d.plane.errors ? ` · ${d.plane.errors} mit Fehler` : ""}`} warn={d.plane.errors > 0} />
            <Row k="Letzte Übertragung" v={dt(d.plane.last_sync)} />
          </Card>
          <Card icon={<Terminal size={16} />} title="Protokolldateien">
            <Row k="Server-Ausgabe" v={d.log_files.stdout} mono />
            <Row k="Server-Fehler" v={d.log_files.stderr} mono />
            <Row k="Mac-App" v={d.log_files.desktop} mono />
            <Row k="SSH-Protokoll" v={`${d.ssh_log.path}${d.ssh_log.size != null ? " · " + mb(d.ssh_log.size) : ""}`} mono />
          </Card>
        </div>
      )}

      <details className="zp-card" style={{ padding: "14px 16px" }}>
        <summary style={{ fontWeight: 600, fontSize: 14, cursor: "pointer" }}>Befehle im Terminal (nur für Notfälle oder wenn der Server nicht läuft)</summary>
        <div style={{ color: "var(--text2)", fontSize: 12, margin: "6px 0 10px" }}>Das meiste geht oben per Knopf. Diese Befehle brauchst du, wenn der Server selbst nicht startet – mit „kopieren“ übernehmen und im Terminal einfügen.</div>
        {COMMANDS.map(g => (
          <div key={g.group} style={{ marginTop: 12 }}>
            <div style={{ fontSize: 11, fontWeight: 600, color: "var(--text2)", textTransform: "uppercase", letterSpacing: ".04em", marginBottom: 6 }}>{g.group}</div>
            {g.items.map(([title, cmd, help]) => (
              <div key={title} style={{ borderTop: "1px solid var(--line)", padding: "8px 0", display: "grid", gridTemplateColumns: "minmax(180px, 240px) 1fr auto", gap: 12, alignItems: "start" }}>
                <div>
                  <div style={{ fontWeight: 500, fontSize: 13 }}>{title}</div>
                  <div style={{ color: "var(--text2)", fontSize: 11, marginTop: 2 }}>{help}</div>
                </div>
                <pre style={{ margin: 0, background: "var(--bg)", border: "1px solid var(--line)", borderRadius: 6, padding: "6px 8px", fontSize: 11, whiteSpace: "pre-wrap", wordBreak: "break-all", fontFamily: "ui-monospace,SFMono-Regular,Menlo,monospace" }}>{cmd}</pre>
                <CopyBtn text={cmd} />
              </div>
            ))}
          </div>
        ))}
      </details>
    </div>
  );
}
