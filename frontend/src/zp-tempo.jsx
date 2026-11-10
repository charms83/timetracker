/* ═══════════════════════════════════════════════════════════════════
   Tempo-Ausgang: Einstellungen + Freigabe-Ansicht (Jira Data Center)
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback } from "react";
import { Upload, RefreshCw, EyeOff, Eye, ChevronDown, ChevronRight, Pencil, Trash2 } from "lucide-react";
import { api, call, Box, lab, inpS, btn, hm, deleteEntry, dupIds, isServerMode, usePlaneWorkspacesActive, wsQ } from "./zp-ui.jsx";
import { JiraInstancesPanel } from "./zp-servermode.jsx";

const PILL = { new: ["Neu", "new"], changed: ["Geändert", "changed"], synced: ["In Tempo", "synced"], error: ["Fehler", "error"], skipped: ["Ausgenommen", ""],
  dup: ["Schon in Tempo?", "changed"], locked: ["Monat gesperrt", ""], needs: ["Ticket wählen", "error"], norule: ["nicht für Tempo (Projekt-Regel)", ""], review: ["Bitte prüfen (Heute)", "error"] };
const monTxt = m => new Date(m + "-15T12:00:00").toLocaleDateString("de-DE", { month: "long", year: "numeric" });
const SYS_COLOR = { jira: "#2f6fd6", plane: "#7c5cd6" };
const minOfIso = iso => { const d = new Date(iso); return d.getHours() * 60 + d.getMinutes(); };
const hhmm2 = m => `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;
/* Buchungen und Lücken eines Tages in zeitlicher Reihenfolge */
const withGaps = d => [...d.entries.map(e => ({ ...e, _s: minOfIso(e.start_time) })), ...(d.gaps || []).map(g => ({ gap: true, s: g.s, e: g.e, _s: g.s }))].sort((a, b) => a._s - b._s);
/* Lücke im Tag: „13:35–14:08 · nichts gebucht“ + Vorschlag des Assistenten + Eintragen */
function GapRow({ d, g, sugg, onDone }) {
  const [busy, setBusy] = useState(false); const [err, setErr] = useState(null);
  const it = (sugg || []).map(x => ({ x, ov: Math.min(x.e, g.e) - Math.max(x.s, g.s) })).filter(o => o.ov > 0).sort((a, b) => b.ov - a.ov)[0]?.x;
  const c = it?.candidates?.[0];
  const iso = m => new Date(`${d.date}T${hhmm2(m)}:00`).toISOString();
  const take = async () => {
    setBusy(true); setErr(null);
    const s = Math.max(g.s, it.s), e = Math.min(g.e, it.e);
    const r = await call("POST", "/time-entries", { project_id: c.project_id, task_id: c.task_id, start_time: iso(s), end_time: iso(e), duration_seconds: (e - s) * 60, comment: it.comment || c.last_comment || null, date: d.date });
    if (r.error) { setErr(r.error); setBusy(false); return; }
    api("POST", "/gap-suggest/learn", { kind: it.kind, ref: it.ref || null, key: it.key || null, entry_id: r.id, task_id: c.task_id, project_id: c.project_id });
    setBusy(false); onDone();
  };
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "5px 14px 5px 38px", borderTop: "1px dashed var(--line)", background: "color-mix(in srgb, var(--text3) 5%, transparent)", fontSize: 12 }}>
      <span style={{ color: "var(--text2)", width: 92, flex: "0 0 auto", fontVariantNumeric: "tabular-nums" }}>{hhmm2(g.s)}–{hhmm2(g.e)}</span>
      <span style={{ color: "var(--text2)", flex: "0 0 auto" }}>{fmtMin(g.e - g.s)} nichts gebucht</span>
      <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: "var(--text2)" }} title={c?.why?.join(" · ")}>
        {c ? <>Vorschlag: <b style={{ color: "var(--text)" }}>{c.label}</b>{it.kind === "ssh" || it.kind === "event" ? ` · ${it.title}` : ""}</> : null}</span>
      {err && <span style={{ color: "var(--stop)" }}>{err}</span>}
      {c && <button style={{ ...btn(true), padding: "2px 10px", fontSize: 12 }} disabled={busy} onClick={take}>{busy ? "…" : "übernehmen"}</button>}
      <button style={{ ...btn(false), padding: "2px 10px", fontSize: 12 }} onClick={() => window.dispatchEvent(new CustomEvent("zp:edit-entry", { detail: { new: true, date: d.date, start: iso(g.s), end: iso(g.e) } }))}>+ eintragen</button>
    </div>);
}
const fmtMin = m => { m = Math.round(m || 0); const h = Math.floor(m / 60); return h ? `${h} h ${String(m % 60).padStart(2, "0")} min` : `${m} min`; };
const dayTxt = d => { const x = new Date(d + "T12:00:00"); return x.toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit", year: "numeric" }); };

/* ═══ Automatisch einrichten: liest Jira/Tempo und deine Tempo-Historie, schlägt alles vor ═══ */
export function TempoAutoSetup({ onApplied, compact }) {
  const [r, setR] = useState(null); const [busy, setBusy] = useState(false); const [msg, setMsg] = useState(null);
  const [v, setV] = useState(null); const [pa, setPa] = useState({});
  const run = async () => {
    setBusy(true); setMsg(null);
    const x = await call("GET", "/integrations/tempo-push/autosetup"); setBusy(false);
    if (x.error) return setMsg({ err: 1, text: x.error });
    setR(x); setV({ new_project: x.new_project || "", issuetype: x.issuetype || "Task", account: x.account || "", since: x.since || "", plane_mode: x.plane_mode || "month", account_attr_id: x.account_attr_id || "" });
    setPa(Object.fromEntries((x.project_accounts || []).map(p => [p.project_id, p.suggest])));
  };
  const apply = async () => {
    const b = { ...v, enable: true, project_accounts: Object.entries(pa).filter(([, a]) => a).map(([project_id, account]) => ({ project_id, account })) };
    const x = await call("POST", "/integrations/tempo-push/autosetup", b);
    setMsg(x.error ? { err: 1, text: x.error } : { text: "Übernommen – Tempo-Übertragung ist eingeschaltet" }); if (!x.error) { setR(null); onApplied && onApplied(); }
  };
  const row = (l, el, hint) => (<div style={{ display: "grid", gridTemplateColumns: compact ? "1fr" : "220px 1fr", gap: compact ? 2 : 12, alignItems: "center", padding: "6px 0", borderTop: "1px solid var(--line)" }}>
    <div style={{ fontSize: 13, fontWeight: 600 }}>{l}{hint && <div style={{ fontWeight: 400, fontSize: 11, color: "var(--text2)" }}>{hint}</div>}</div><div>{el}</div></div>);
  return (
    <div>
      {!r && <div style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
        <button style={btn(true)} disabled={busy} onClick={run}><RefreshCw size={13} /> {busy ? "Lese Jira & Tempo…" : "Automatisch einrichten"}</button>
        <span style={{ fontSize: 12, color: "var(--text2)" }}>Liest deine Tempo-Buchungen der letzten 6 Monate, die Accounts und deine Jira-Rechte und schlägt alles vor. Du bestätigst danach.</span>
      </div>}
      {r && v && (<div>
        {row("Plane-Zeiten nach Tempo", (
          <div style={{ display: "grid", gap: 6 }}>
            {[["month", "Ein Übergangs-Ticket pro Monat (empfohlen)", `„Übergang Zeiten ${r.worker || "du"} JJJJ-MM“ – beim ersten Buchen angelegt, dir zugewiesen, nach der Monatssperre geschlossen. Jeder Worklog nennt Plane-Ticket, Titel, Projekt und Link.`],
              ["ticket", "Je Plane-Ticket ein Jira-Gegenstück", "Legt für jedes Plane-Ticket ein eigenes Jira-Ticket an (alte Variante)."]].map(([k, l, d]) => (
              <button key={k} onClick={() => setV({ ...v, plane_mode: k })} style={{ textAlign: "left", padding: "8px 10px", borderRadius: 8, color: "var(--text)", border: `1px solid ${v.plane_mode === k ? "var(--accent)" : "var(--line)"}`, background: v.plane_mode === k ? "var(--accent-soft)" : "var(--card)" }}>
                <div style={{ fontWeight: 600, fontSize: 13 }}>{v.plane_mode === k ? "● " : "○ "}{l}</div><div style={{ fontSize: 12, color: "var(--text2)" }}>{d}</div></button>))}
          </div>))}
        {row("Jira-Projekt für neue Tickets", (<div style={{ display: "flex", gap: 8 }}>
          <select style={{ ...inpS, flex: 1 }} value={v.new_project} onChange={e => setV({ ...v, new_project: e.target.value })}>
            {[...new Set([v.new_project, ...(r.projects || []).map(p => p.key)])].filter(Boolean).map(k => <option key={k} value={k}>{k} – {(r.projects || []).find(p => p.key === k)?.name || ""}</option>)}
          </select>
          <select style={{ ...inpS, width: 150 }} value={v.issuetype} onChange={e => setV({ ...v, issuetype: e.target.value })}>
            {[...new Set([v.issuetype, ...(r.issuetypes || [])])].map(t => <option key={t}>{t}</option>)}</select></div>), "Übergangs- und Sammeltickets landen hier – du darfst dort anlegen")}
        {row("Tempo-Account (Standard)", (<select style={inpS} value={v.account} onChange={e => setV({ ...v, account: e.target.value })}>
            <option value="">– keiner –</option>
            {[...new Set([v.account, ...(r.account_usage || []).map(a => a.key), ...(r.accounts || []).map(a => a.key)])].filter(Boolean).map(k => {
              const u = (r.account_usage || []).find(a => a.key === k), n = (r.accounts || []).find(a => a.key === k)?.name;
              return <option key={k} value={k}>{k}{n ? ` – ${n}` : ""}{u ? ` (${u.hours} h von dir zuletzt)` : ""}</option>; })}
          </select>), "am häufigsten von dir benutzt")}
        {(r.project_accounts || []).length > 0 && row("Account je Projekt", (<div style={{ display: "grid", gap: 4 }}>
          {r.project_accounts.map(p => (<label key={p.project_id} style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 13 }}>
            <input type="checkbox" checked={!!pa[p.project_id]} onChange={e => setPa({ ...pa, [p.project_id]: e.target.checked ? p.suggest : "" })} />
            <span style={{ flex: 1 }}>{p.name}</span><b>{p.suggest}</b>{p.current && <span style={{ fontSize: 11, color: "var(--text2)" }}>bisher {p.current}</span>}</label>))}
          </div>), "so hast du bisher in Tempo gebucht")}
        {row("Übertragen ab", (<div><input type="date" style={{ ...inpS, width: 180 }} value={v.since} onChange={e => setV({ ...v, since: e.target.value })} />
          <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 4 }}>{(r.months || []).map(m => `${monTxt(m.month)}: ${m.locked ? "gesperrt" : "offen"}`).join(" · ")}</div></div>), "gesperrte Monate werden übersprungen")}
        {(r.notes || []).map((n, i) => <div key={i} style={{ fontSize: 12, color: "#b58100", marginTop: 4 }}>! {n}</div>)}
        <div style={{ display: "flex", gap: 8, marginTop: 10 }}>
          <button style={btn(true)} onClick={apply}>Übernehmen & einschalten</button>
          <button style={btn(false)} onClick={() => setR(null)}>Abbrechen</button>
        </div>
      </div>)}
      {msg && <div style={{ fontSize: 12, marginTop: 8, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</div>}
    </div>);
}

/* Übergangs-Tickets der letzten Monate */
function MonthTickets() {
  const [o, setO] = useState(null);
  useEffect(() => { api("GET", "/integrations/tempo-push/outbox").then(x => x && !x.error && setO(x)); }, []);
  if (!o || o.plane_mode !== "month") return null;
  const l = o.month_tickets || [];
  return (<div style={{ marginTop: 10, fontSize: 12 }}>
    <div style={{ color: "var(--text2)", marginBottom: 4 }}>Übergangs-Tickets</div>
    {!l.length ? <div style={{ color: "var(--text2)" }}>Noch keins – wird bei der ersten Plane-Buchung des Monats angelegt.</div> : l.map(t => (
      <div key={t.month} style={{ display: "flex", gap: 10, padding: "3px 0", borderTop: "1px solid var(--line)" }}>
        <b style={{ minWidth: 90 }}>{t.issue_key}</b><span style={{ flex: 1 }}>{monTxt(t.month)}</span>
        <span style={{ color: t.close_error ? "var(--stop)" : "var(--text2)" }}>{t.closed_at ? "geschlossen" : t.close_error ? `nicht geschlossen: ${t.close_error}` : "offen"}</span></div>))}
  </div>);
}

/* ═══ Einstellungen → Tempo ═══ */
export function TempoSettingsTab() {
  const sm = isServerMode();   // Servermodus: Instanz-Werte je Jira-Instanz, keine lokale TLS-Einstellung
  const [f, setF] = useState({ jira_base_url: "", jira_token: "", tempo_push_enabled: "0", tempo_booking_issue: "", tempo_push_since: "", tempo_push_mode: "manual",
    jira_new_project: "", jira_new_issuetype: "Task", tempo_account: "", jira_my_tickets: "1", jira_my_jql: "" });
  const [acc, setAcc] = useState({});
  const [mine, setMine] = useState(null);
  const [tlsSt, setTlsSt] = useState(null);
  const loadTls = () => api("GET", "/security/tls").then(r => r && !r.error && setTlsSt(r));
  const [projects, setProjects] = useState([]);
  const [over, setOver] = useState({});
  const [msg, setMsg] = useState(null);
  const [test, setTest] = useState(null);
  const [busy, setBusy] = useState("");
  const set = k => e => setF(v => ({ ...v, [k]: e.target.value }));

  useEffect(() => {
    api("GET", "/settings").then(s => s && setF(v => ({ ...v,
      jira_base_url: s.jira_base_url || "", jira_token: s.jira_token || "", tempo_push_enabled: s.tempo_push_enabled === "1" ? "1" : "0",
      tempo_booking_issue: s.tempo_booking_issue || "", tempo_push_since: s.tempo_push_since || "", tempo_push_mode: s.tempo_push_mode === "auto" ? "auto" : "manual",
      jira_new_project: s.jira_new_project || "", jira_new_issuetype: s.jira_new_issuetype || "Task", tempo_account: s.tempo_account || "",
      tempo_plane_mode: s.tempo_plane_mode === "ticket" ? "ticket" : "month", jira_my_tickets: s.jira_my_tickets === "0" ? "0" : "1", jira_my_jql: s.jira_my_jql || "", tls_ca_file: s.tls_ca_file || "", tls_insecure: s.tls_insecure === "1" ? "1" : "0" })));
    api("GET", "/integrations/jira/my-tickets/status").then(r => r && !r.error && setMine(r));
    loadTls();
    api("GET", "/projects").then(p => { const l = Array.isArray(p) ? p.filter(x => x.status !== "archived") : []; setProjects(l); setOver(Object.fromEntries(l.map(x => [x.id, x.tempo_issue || ""]))); setAcc(Object.fromEntries(l.map(x => [x.id, x.tempo_account || ""]))); });
  }, []);

  const save = async (andTest) => {
    setBusy("save"); setMsg(null);
    const { jira_base_url, jira_token, ...rest } = f;
    let body = { ...rest, tempo_booking_issue: f.tempo_booking_issue.trim().toUpperCase(), jira_new_project: f.jira_new_project.trim().toUpperCase() };
    if (sm) { const { tempo_booking_issue, jira_new_project, jira_new_issuetype, tempo_account, tls_ca_file, tls_insecure, ...keep } = body; body = keep; }   // je Instanz bzw. nur lokal am Mac
    const r = await call("PUT", "/settings", body);
    if (r.error) { setMsg({ err: true, text: r.error }); setBusy(""); return; }
    for (const p of projects) {
      const v = (over[p.id] || "").trim().toUpperCase();
      const a = (acc[p.id] || "").trim();
      if (v !== (p.tempo_issue || "") || a !== (p.tempo_account || "")) {
        const x = await call("PUT", `/integrations/tempo-push/project/${p.id}`, { issue: v, account: a });
        if (x.error) { setMsg({ err: true, text: `${p.name}: ${x.error}` }); setBusy(""); return; }
      }
    }
    setMsg({ text: "Gespeichert" });
    if (andTest) await doTest();
    setBusy("");
  };
  const doTest = async () => {
    setTest(null);
    const r = await call("GET", "/integrations/tempo-push/status");
    setTest(r); loadTls();
  };

  return (
    <div>
      <div style={{ fontSize: 12, color: "var(--text2)", margin: "0 2px 10px" }}>Jira-Adresse und Token: oben „Zugang & Projekte“ (gilt für Jira und Tempo).</div>

      <Box title="So kommt deine Zeit nach Tempo" sub="Das System entscheidet je Buchung selbst – du musst nur einmal die Grundlagen festlegen.">
        <div style={{ display: "grid", gap: 6, fontSize: 13, marginBottom: 12 }}>
          <div><b>Kunde noch in Jira</b> → direkt auf das Jira-Ticket (aus „Meine Jira-Tickets“, Tempo-Historie oder Vorschlag im Ausgang).</div>
          <div><b>Projekt schon in Plane</b> → {f.tempo_plane_mode === "ticket" ? "auf ein Jira-Gegenstück je Plane-Ticket" : "auf dein Übergangs-Ticket des Monats; Plane-Ticket, Titel, Projekt und Link stehen im Worklog"}. Der Kunde kommt über den Tempo-Account mit.</div>
          <div><b>Ganz ohne Ticket</b> → Sammelticket des Projekts (legt das System bei Bedarf selbst an).</div>
        </div>
        <TempoAutoSetup onApplied={() => location.reload()} />
        <MonthTickets />
      </Box>

      <Box title="Zeit nach Tempo schreiben" sub="Jede abgeschlossene Buchung wird ein eigener Tempo-Worklog – wohin, steht oben unter „So kommt deine Zeit nach Tempo“.">
        <label style={lab}>Übertragung</label>
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
          {[["0", "Aus", "Es wird nichts nach Tempo geschrieben."],
            ["1", "Ein", "Buchungen erscheinen im Reiter „Tempo“ zur Freigabe."]].map(([k, l, d]) => (
            <button key={k} onClick={() => setF(v => ({ ...v, tempo_push_enabled: k }))}
              style={{ flex: 1, minWidth: 220, textAlign: "left", padding: "10px 12px", borderRadius: 8,
                border: `1px solid ${f.tempo_push_enabled === k ? "var(--accent)" : "var(--line)"}`, background: f.tempo_push_enabled === k ? "var(--accent-soft)" : "var(--card)" }}>
              <div style={{ fontWeight: 600 }}>{f.tempo_push_enabled === k ? "● " : "○ "}{l}</div>
              <div style={{ color: "var(--text2)", fontSize: 12 }}>{d}</div>
            </button>))}
        </div>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
            <div><label style={lab}>Übertragen ab (ältere Buchungen: früher setzen; gesperrte Monate werden übersprungen)</label>
              <input style={inpS} type="date" value={f.tempo_push_since} onChange={set("tempo_push_since")} /></div>
          </div>
        {f.tempo_push_enabled === "1" && (<>
          <label style={lab}>Freigabe</label>
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
            {[["manual", "Manuell freigeben (empfohlen)", "Du gibst im Reiter „Tempo“ tageweise oder gesammelt frei."],
              ["auto", "Automatisch", "Abgeschlossene Buchungen gehen nach wenigen Minuten selbst nach Tempo."]].map(([k, l, d]) => (
              <button key={k} onClick={() => setF(v => ({ ...v, tempo_push_mode: k }))}
                style={{ flex: 1, minWidth: 220, textAlign: "left", padding: "10px 12px", borderRadius: 8,
                  border: `1px solid ${f.tempo_push_mode === k ? "var(--accent)" : "var(--line)"}`, background: f.tempo_push_mode === k ? "var(--accent-soft)" : "var(--card)" }}>
                <div style={{ fontWeight: 600 }}>{f.tempo_push_mode === k ? "● " : "○ "}{l}</div>
                <div style={{ color: "var(--text2)", fontSize: 12 }}>{d}</div>
              </button>))}
          </div>
        </>)}
      </Box>

      <details style={{ margin: "4px 0 12px" }}>
      <summary style={{ cursor: "pointer", fontSize: 13, fontWeight: 600, color: "var(--text2)", padding: "8px 2px" }}>{sm ? "Erweitert: Feineinstellungen, Ticket/Account je Projekt" : "Erweitert: Feineinstellungen, Ticket/Account je Projekt, Zertifikate"}</summary>
      <Box title="Feineinstellungen" sub="Wird von „Automatisch einrichten“ befüllt – hier nur, wenn du etwas von Hand ändern willst.">
        {sm && <div style={{ marginBottom: 10 }}>
          <div style={{ fontSize: 12, color: "var(--text2)", marginBottom: 8 }}>Sammel-Buchungsticket, Jira-Projekt für neue Tickets, Vorgangstyp und Tempo-Account gelten je Jira-Instanz:</div>
          <JiraInstancesPanel mode="tempo" />
        </div>}
        {!sm && <><label style={lab}>Festes Sammel-Buchungsticket (leer = legt das System je Projekt selbst an)</label>
        <input style={inpS} placeholder="leer lassen" value={f.tempo_booking_issue} onChange={set("tempo_booking_issue")} /></>}
        <label style={lab}>Plane-Zeiten nach Tempo</label>
        <select style={inpS} value={f.tempo_plane_mode || "month"} onChange={set("tempo_plane_mode")}>
          <option value="month">Ein Übergangs-Ticket pro Monat (empfohlen)</option><option value="ticket">Je Plane-Ticket ein Jira-Gegenstück</option></select>
        {!sm && <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr 1fr", gap: 10 }}>
          <div><label style={lab}>Jira-Projekt für neue Tickets (leer = Sammelticket)</label>
            <input style={inpS} placeholder="z. B. ZEIT" value={f.jira_new_project} onChange={set("jira_new_project")} /></div>
          <div><label style={lab}>Vorgangstyp</label>
            <input style={inpS} placeholder="Task" value={f.jira_new_issuetype} onChange={set("jira_new_issuetype")} /></div>
          <div><label style={lab}>Tempo-Account (Pflichtfeld in Tempo)</label>
            <input style={inpS} placeholder="z. B. IMPROVEMENTS" value={f.tempo_account} onChange={set("tempo_account")} /></div>
        </div>}
        <label style={lab}>Meine Jira-Tickets in der Ticket-Auswahl</label>
        <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
          <button onClick={() => setF(v => ({ ...v, jira_my_tickets: v.jira_my_tickets === "1" ? "0" : "1" }))} style={{ ...btn(false), borderColor: f.jira_my_tickets === "1" ? "var(--accent)" : undefined }}>
            {f.jira_my_tickets === "1" ? "● an" : "○ aus"} – alle 30 Minuten holen</button>
          <button style={btn(false)} disabled={busy === "mine"} onClick={async () => { setBusy("mine"); const r = await call("POST", "/integrations/jira/my-tickets/sync"); setBusy("");
            setMsg(r.error ? { err: true, text: r.error } : { text: `${r.count} Jira-Tickets geholt, ${r.created} neu${r.projects_new ? `, ${r.projects_new} Projekt(e) angelegt` : ""}` });
            api("GET", "/integrations/jira/my-tickets/status").then(x => x && setMine(x)); }}>{busy === "mine" ? "Hole…" : "Jetzt holen"}</button>
          {mine && <span style={{ fontSize: 12, color: "var(--text2)" }}>{mine.count} in der Auswahl{mine.last ? ` · zuletzt ${hm(mine.last.at)}` : ""}</span>}
        </div>
        <input style={{ ...inpS, marginTop: 6, fontSize: 12 }} placeholder="assignee = currentUser() AND resolution = Unresolved ORDER BY updated DESC" value={f.jira_my_jql} onChange={set("jira_my_jql")} />
      </Box>

      {f.tempo_push_enabled === "1" && projects.length > 0 && (
        <Box title="Buchungsticket und Account je Projekt (optional)" sub="Ticket: für Buchungen ohne eigenes Ticket (leer = Sammel-Buchungsticket). Account: Tempo-Account dieses Projekts (leer = Standard oben).">
          <div style={{ display: "grid", gridTemplateColumns: "1fr 150px 170px", gap: "6px 10px", alignItems: "center" }}>
            {projects.map(p => (<div key={p.id} style={{ display: "contents" }}>
              <div style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{p.name}</div>
              <input style={inpS} placeholder={f.tempo_booking_issue || "ABC-123"} value={over[p.id] || ""} onChange={e => setOver(v => ({ ...v, [p.id]: e.target.value }))} />
              <input style={inpS} placeholder={f.tempo_account || "Account"} value={acc[p.id] || ""} onChange={e => setAcc(v => ({ ...v, [p.id]: e.target.value }))} />
            </div>))}
          </div>
        </Box>
      )}

      {!sm && <Box title="Verbindungssicherheit" sub="Zeitdaten sind sensibel: Verbindungen zu Jira, Tempo und Plane prüfen das Zertifikat. Firmen-Zertifikate aus dem macOS-Schlüsselbund (System) werden automatisch übernommen. Nur falls das nicht reicht: CA-Datei (PEM) hinterlegen.">
        <label style={lab}>Zusätzliche CA-Datei (optional, Pfad zu .pem)</label>
        <input style={inpS} placeholder="/Users/…/example-ca.pem" value={f.tls_ca_file || ""} onChange={set("tls_ca_file")} />
        <label style={{ display: "flex", gap: 6, alignItems: "center", fontSize: 12, marginTop: 8, color: f.tls_insecure === "1" ? "var(--stop)" : "var(--text2)" }}>
          <input type="checkbox" checked={f.tls_insecure === "1"} onChange={e => setF(v => ({ ...v, tls_insecure: e.target.checked ? "1" : "0" }))} />
          Zertifikatsprüfung ausschalten (unsicher – nur zum Testen)</label>
        {tlsSt?.candidate && (
          <div style={{ marginTop: 12, padding: "10px 12px", borderRadius: 8, border: "1px solid var(--pause-line, #e5c46b)", background: "var(--pause-bg, #fff7e0)" }}>
            <div style={{ fontWeight: 600, fontSize: 13 }}>Unbekanntes Root-Zertifikat von {tlsSt.candidate.host}</div>
            <div style={{ fontSize: 12, marginTop: 4 }}>{tlsSt.candidate.subject} · gültig bis {tlsSt.candidate.valid_to}</div>
            <div style={{ fontSize: 11, marginTop: 4, fontFamily: "ui-monospace,Menlo,monospace", wordBreak: "break-all" }}>SHA-256: {tlsSt.candidate.fingerprint}</div>
            <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 6 }}>Nur vertrauen, wenn der Fingerabdruck mit dem eurer Firmen-CA übereinstimmt (z. B. bei der IT erfragen oder im Browser: Schloss › Zertifikat › Stammzertifikat).</div>
            <button style={{ ...btn(true), marginTop: 8 }} onClick={async () => {
              if (!confirm(`Root-Zertifikat „${tlsSt.candidate.subject}“ als vertrauenswürdig hinterlegen?\n\nSHA-256:\n${tlsSt.candidate.fingerprint}`)) return;
              const r = await call("POST", "/security/trust-root", { fingerprint: tlsSt.candidate.fingerprint });
              if (r.error) setMsg({ err: true, text: r.error }); else { await doTest(); }
            }}>Diesem Root-Zertifikat vertrauen</button>
          </div>)}
        {tlsSt?.trusted?.length > 0 && <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 8 }}>Von Hand vertraut: {tlsSt.trusted.map(t => t.subject).join(" · ")}</div>}
      </Box>}
      </details>

      <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
        <button style={btn(true)} onClick={() => save(true)} disabled={!!busy}>{busy === "save" ? "Speichere…" : "Speichern & Verbindung testen"}</button>
        {msg && <span style={{ fontSize: 12, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</span>}
      </div>
      {test && (
        <div className="zp-card" style={{ padding: "12px 16px", marginTop: 12, fontSize: 12 }}>
          <div style={{ color: test.ok ? "var(--accent)" : "var(--stop)", fontWeight: 600 }}>{test.ok ? "✓ " : "✗ "}{test.error || test.message}</div>
          {(test.issues || []).map(i => (
            <div key={i.key} style={{ marginTop: 4, color: i.ok ? "var(--text2)" : "var(--stop)" }}>
              {i.ok ? "✓" : "✗"} Buchungsticket {i.key}{i.ok ? ` – ${i.summary}` : ` – ${i.error}`}
            </div>))}
          {test.new_project && <div style={{ marginTop: 4, color: test.new_project.ok ? "var(--text2)" : "var(--stop)" }}>{test.new_project.ok ? "✓" : "✗"} Jira-Projekt für neue Tickets {test.new_project.key}{test.new_project.ok ? ` – ${test.new_project.name}` : ` – ${test.new_project.error}`}</div>}
        </div>
      )}
    </div>
  );
}

/* ═══ Reiter „Tempo“: Freigabe ═══ */
/* Tempo-Account je Buchung: zeigt, welcher Account genommen wird und woher; anklicken = anderen wählen */
let accCache = null;
function AccountChip({ e, onDone }) {
  const [open, setOpen] = useState(false); const [list, setList] = useState(accCache); const [q, setQ] = useState(""); const [remember, setRemember] = useState(false);
  const show = async () => { setOpen(!open); if (!accCache) { const r = await call("GET", "/integrations/tempo-push/accounts"); if (Array.isArray(r)) { accCache = r; setList(r); } } };
  const pick = async acc => { await call("POST", "/integrations/tempo-push/entry-account", { ids: [e.id], account: acc, remember: remember ? "project" : undefined }); setOpen(false); onDone && onDone(); };
  const label = e.account ? e.account : e.account_from === "Ticket" ? "Account vom Ticket" : "kein Account";
  const from = { Buchung: "für diese Buchung", Projekt: "vom Projekt", Ticket: "", Standard: "Standard" }[e.account_from] || "";
  const f = (list || []).filter(a => !q || `${a.key} ${a.name}`.toLowerCase().includes(q.toLowerCase())).slice(0, 40);
  return (
    <div style={{ fontSize: 11, marginTop: 2, position: "relative" }}>
      <button onClick={show} title="Tempo-Account ändern" style={{ fontSize: 11, padding: "0 7px", borderRadius: 10, border: "1px solid var(--line)", background: "var(--card)", color: e.account_from === "Buchung" ? "var(--accent)" : "var(--text2)", cursor: "pointer" }}>
        Account: <b>{label}</b>{from ? ` · ${from}` : ""} ▾</button>
      {open && (
        <div className="zp-card" style={{ marginTop: 4, maxWidth: 420, padding: 8, boxShadow: "0 4px 16px rgba(0,0,0,.12)" }}>
          <input autoFocus value={q} onChange={x => setQ(x.target.value)} placeholder="Account suchen (z. B. OPERATIONS, HIRI, travel) …" style={{ width: "100%", padding: "5px 7px", fontSize: 12 }} />
          <div style={{ maxHeight: 240, overflowY: "auto", marginTop: 6 }}>
            {!list ? <div style={{ color: "var(--text2)" }}>Lade Accounts …</div> : f.map(a => (
              <div key={a.key} onClick={() => pick(a.key)} style={{ padding: "4px 6px", borderRadius: 6, cursor: "pointer", display: "flex", gap: 6 }}
                onMouseEnter={x => x.currentTarget.style.background = "var(--accent-soft)"} onMouseLeave={x => x.currentTarget.style.background = "transparent"}>
                <b style={{ whiteSpace: "nowrap" }}>{a.key}</b><span style={{ flex: 1, color: "var(--text2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{a.name}</span>{a.used > 0 && <span style={{ color: "var(--accent)" }}>{a.used} h</span>}</div>))}
          </div>
          <div style={{ display: "flex", gap: 8, alignItems: "center", borderTop: "1px solid var(--line)", marginTop: 6, paddingTop: 6 }}>
            <label style={{ flex: 1, display: "flex", gap: 5, alignItems: "center" }}><input type="checkbox" checked={remember} onChange={x => setRemember(x.target.checked)} /> für das ganze Projekt merken</label>
            {e.account_from === "Buchung" && <button onClick={() => pick("")} style={{ fontSize: 11, border: "none", background: "none", color: "var(--text2)", cursor: "pointer" }}>zurücksetzen</button>}
          </div>
        </div>)}
    </div>);
}

export function TempoOutboxView({ onOpenSettings }) {
  const [data, setData] = useState(null);
  const [more, setMore] = useState({});
  const [filter, setFilter] = useState("open");
  const [openDays, setOpenDays] = useState({});
  const [gapSugg, setGapSugg] = useState({});   // Datum → Vorschläge des Assistenten (nur für aufgeklappte Tage mit Lücken)
  const [busy, setBusy] = useState("");
  const [msg, setMsg] = useState(null);
  const load = useCallback(async () => {
    const r = await api("GET", "/integrations/tempo-push/outbox");
    setData(r && !r.error ? r : { failed: true, error: r?.error || "Server nicht erreichbar" });
  }, []);
  useEffect(() => { load(); const h = () => load(); window.addEventListener("zp:entry-changed", h); return () => window.removeEventListener("zp:entry-changed", h); }, [load]);
  // Vorschläge für Lücken nur für aufgeklappte Tage laden (lokal, ohne Plane/Jira)
  useEffect(() => {
    if (!data?.days) return;
    for (const d of data.days) {
      const open = openDays[d.date] ?? (filter === "open" && d.open > 0);
      if (open && d.gaps?.length) api("GET", `/gap-suggest?date=${d.date}`).then(r => setGapSugg(v => ({ ...v, [d.date]: r?.items || [] })));
    }
  }, [data, openDays, filter]);   // eslint-disable-line react-hooks/exhaustive-deps

  if (!data) return <div style={{ color: "var(--text3)", padding: 30, textAlign: "center" }}>Lade…</div>;
  if (data.failed) return (
    <div className="zp-card" style={{ padding: 24, textAlign: "center" }}>
      <div style={{ color: "var(--stop)", marginBottom: 10 }}>✗ {data.error}</div>
      <button style={btn(false)} onClick={load}><RefreshCw size={13} /> Erneut versuchen</button>
    </div>);
  if (!data.configured) return (
    <div className="zp-card" style={{ padding: 30, textAlign: "center" }}>
      <div style={{ fontWeight: 600, fontSize: 15, marginBottom: 6 }}>Tempo ist noch nicht eingerichtet</div>
      <div style={{ color: "var(--text2)", marginBottom: 14 }}>
        {!data.configured ? "Jira-Adresse und Token fehlen." : !data.enabled ? "Die Übertragung nach Tempo ist ausgeschaltet." : "Es ist noch kein Jira-Projekt für neue Tickets festgelegt."}
        {" "}Das stellst du unter Einstellungen › Jira & Tempo ein.
      </div>
      <button style={btn(true)} onClick={onOpenSettings}>Zu den Einstellungen</button>
    </div>);

  const c = data.counts || {};
  const pending = (c.new || 0) + (c.changed || 0) + (c.error || 0);
  const needs = c.needs || 0;
  const attention = pending + (c.dup || 0) + needs;
  const pendingMin = (data.minutes?.new || 0) + (data.minutes?.changed || 0) + (data.minutes?.error || 0);
  const days = data.days.filter(d => filter === "all" ? true : d.open > 0 || d.needs > 0 || d.entries.some(x => x.status === "dup"));

  const push = async (ids, label) => {
    setBusy(label || "all"); setMsg(null);
    const r = await call("POST", "/integrations/tempo-push/push", ids ? { ids } : {});
    setBusy("");
    if (r.busy) setMsg({ text: "Eine Übertragung läuft gerade." });
    else setMsg(r.error ? { err: true, text: r.error } : { err: r.errors > 0,
      text: `${r.ok} Worklog${r.ok === 1 ? "" : "s"} übertragen${r.deleted ? `, ${r.deleted} in Tempo gelöscht` : ""}${r.errors ? `, ${r.errors} Fehler${r.messages?.[0] ? ": " + r.messages[0] : ""}` : ""}` });
    load();
  };
  const skip = async (id, on) => { await call("POST", "/integrations/tempo-push/skip", { ids: [id], skip: on }); load(); };
  const allEntries = data.days.flatMap(d => d.entries);
  const applyTarget = async (e, key, scope) => {
    const r = await call("POST", "/integrations/tempo-push/target", { ids: [e.id], key, scope: scope || (e.ticket ? "task" : "entry") });
    setMsg(r.error ? { err: true, text: r.error } : { text: `${key} ${r.summary || ""} übernommen` }); load();
  };
  const findMore = async e => { setMore(m => ({ ...m, [e.id]: "…" })); const r = await call("POST", "/integrations/tempo-push/suggest", { ids: [e.id] }); setMore(m => ({ ...m, [e.id]: r.suggestions?.[e.id] || [] })); };
  const applyAllGood = async () => {
    const done = new Set(); let n = 0;
    for (const e of allEntries) { const s = e.suggest?.[0]; const k = (e.ticket || "") + "|" + e.id;
      if (!s || s.score < 80 || done.has(e.ticket || k)) continue; done.add(e.ticket || k);
      const r = await call("POST", "/integrations/tempo-push/target", { ids: [e.id], key: s.key, scope: e.ticket ? "task" : "entry" }); if (!r.error) n++; }
    setMsg({ text: `${n} Vorschläge übernommen` }); load();
  };
  const goodCount = allEntries.filter(e => e.suggest?.[0]?.score >= 80).length;
  const dupAct = async (id, action) => { await call("POST", "/integrations/tempo-push/dup", { ids: [id], action }); load(); };
  const check = async since => {
    setBusy("check"); setMsg(null);
    const r = await call("POST", "/integrations/tempo-push/check", since ? { since } : {});
    setBusy("");
    if (r.error) setMsg({ err: true, text: r.error });
    else { const l = r.months.filter(m => m.locked).length, dups = data.counts?.dup || 0;
      setMsg({ text: `${r.months.length} Monat${r.months.length === 1 ? "" : "e"} geprüft · ${l} gesperrt${dups ? "" : ""} – Details unten` }); }
    load();
  };
  const unlock = async m => { await call("DELETE", `/integrations/tempo-push/months/${m}`); load(); };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <div className="zp-card" style={{ padding: "14px 16px", display: "flex", alignItems: "center", gap: 14, flexWrap: "wrap" }}>
        <div style={{ flex: 1, minWidth: 260 }}>
          <div style={{ fontWeight: 600, fontSize: 14 }}>Tempo-Ausgang · einzelne Worklogs</div>
          <div style={{ color: "var(--text2)", fontSize: 12 }}>
            Jede Buchung geht als eigener Worklog auf ihr Jira-Ticket. Plane-Work-Items gehen aufs Übergangs-Ticket des Monats; Buchungen ohne Ticket brauchen erst ein Ticket.
          </div>
          <div style={{ color: "var(--text2)", fontSize: 12, marginTop: 4 }}>
            {data.mode === "auto" ? "Automatische Übertragung ist aktiv · " : ""}{pending} Buchung{pending === 1 ? "" : "en"} offen ({fmtMin(pendingMin)}) · ab {dayTxt(data.since)}
            {data.deletions.length ? ` · ${data.deletions.length} Löschung(en) vorgemerkt` : ""}
          </div>
        </div>
        <button style={btn(true)} onClick={() => { if (confirm(`${pending} Buchung(en)${data.deletions.length ? ` und ${data.deletions.length} Löschung(en)` : ""} jetzt nach Tempo übertragen?`)) push(null); }} disabled={!!busy || (!pending && !data.deletions.length)}>
          <Upload size={14} /> {busy === "all" ? "Übertrage…" : `Alle freigeben (${pending + data.deletions.length})`}
        </button>
      </div>
      {!data.enabled && <div className="zp-card" style={{ padding: "10px 16px", display: "flex", gap: 10, alignItems: "center", borderLeft: "3px solid #e5a000" }}>
        <div style={{ flex: 1, fontSize: 13 }}>Die Übertragung nach Tempo ist ausgeschaltet. Du siehst hier trotzdem, wohin jede Buchung gehen würde.</div>
        <button style={btn(true)} onClick={async () => { await call("PUT", "/settings", { tempo_push_enabled: "1" }); load(); }}>Einschalten</button></div>}
      {needs > 0 && <div className="zp-card" style={{ padding: "10px 16px", display: "flex", gap: 10, alignItems: "center", borderLeft: "3px solid var(--stop)" }}>
        <div style={{ flex: 1, fontSize: 13 }}><b>{needs} Buchung{needs === 1 ? "" : "en"} ohne Jira-Ticket.</b> In Jira gibt es keine Zeit ohne Ticket – aufs Übergangs-Ticket geht nur, was über Plane kommt. Zeitprotokoll schlägt passende Tickets aus deiner Tempo-Historie vor – ein Klick übernimmt.</div>
        {goodCount > 0 && <button style={btn(true)} onClick={applyAllGood}>Sichere Vorschläge übernehmen ({goodCount})</button>}</div>}
      {msg && <div style={{ fontSize: 12, color: msg.err ? "var(--stop)" : "var(--accent)", padding: "0 4px" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</div>}

      <History data={data} busy={busy} onCheck={check} onUnlock={unlock} />

      <div style={{ display: "flex", gap: 6, flexWrap: "wrap", alignItems: "center" }}>
        {[["open", `Offen (${attention})`], ["all", `Alle (${data.days.reduce((s, d) => s + d.entries.length, 0)})`]].map(([k, l]) => (
          <button key={k} onClick={() => setFilter(k)} style={{ border: "1px solid " + (filter === k ? "var(--accent)" : "var(--line)"), background: filter === k ? "var(--accent-soft)" : "var(--card)",
            color: filter === k ? "var(--accent)" : "var(--text2)", borderRadius: 20, padding: "4px 12px", fontSize: 12, fontWeight: filter === k ? 600 : 500 }}>{l}</button>))}
        <button style={{ ...btn(false), padding: "4px 10px", marginLeft: "auto" }} onClick={load}><RefreshCw size={13} /> Neu laden</button>
      </div>

      {!days.length && <div className="zp-card" style={{ padding: 24, textAlign: "center", color: "var(--text2)" }}>{filter === "open" ? "Alles übertragen ✓" : "Keine Buchungen im gewählten Zeitraum"}</div>}
      {days.map(d => {
        const isOpen = openDays[d.date] ?? (filter === "open" && d.open > 0);
        return (
          <div key={d.date} className="zp-card" style={{ overflow: "hidden" }}>
            <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "9px 14px", cursor: "pointer" }} onClick={() => setOpenDays(v => ({ ...v, [d.date]: !isOpen }))}>
              {isOpen ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
              <div style={{ fontWeight: 600, flex: 1 }}>{dayTxt(d.date)}</div>
              <span style={{ color: "var(--text2)", fontSize: 12 }}>{fmtMin(d.minutes)}</span>
              {d.open > 0
                ? <button style={{ ...btn(true), padding: "4px 12px" }} disabled={!!busy} onClick={e => { e.stopPropagation(); push(d.entries.filter(x => ["new", "changed", "error"].includes(x.status)).map(x => x.id), d.date); }}>
                    {busy === d.date ? "…" : `Tag freigeben (${d.open})`}</button>
                : d.needs > 0 ? <span className="zp-pill error">Ticket wählen</span>
                : d.entries.some(x => x.status === "dup") ? <span className="zp-pill changed">bitte prüfen</span>
                : d.entries.some(x => x.status === "locked") ? <span className="zp-pill">Monat gesperrt</span>
                : <span className="zp-pill synced">✓ In Tempo</span>}
            </div>
            {isOpen && (() => { const dup = dupIds(d.entries); return withGaps(d).map(e => e.gap ? <GapRow key={"g" + e.s} d={d} g={e} sugg={gapSugg[d.date]} onDone={load} /> : (
              <div key={e.id} style={{ display: "flex", alignItems: "center", gap: 10, padding: "7px 14px 7px 38px", borderTop: "1px solid var(--line)", opacity: e.status === "skipped" ? .55 : 1 }}>
                <span style={{ color: "var(--text2)", fontSize: 12, width: 92, flex: "0 0 auto", fontVariantNumeric: "tabular-nums" }}>{hm(e.start_time)}–{hm(e.end_time)}</span>
                <div style={{ flex: 1, minWidth: 0 }}>
                  {/* v6.34: zuerst das eigene Ticket (farbig wie in der Woche), dann Text, Tempo-Ziel nur klein */}
                  <div style={{ display: "flex", gap: 6, alignItems: "baseline", minWidth: 0 }} title={e.comment}>
                    {e.ticket ? <span style={{ flex: "none", fontSize: 11, fontWeight: 700, padding: "1px 6px", borderRadius: 5, color: "#fff", background: SYS_COLOR[e.system] || "var(--text3)" }}>{e.ticket}</span>
                      : <span style={{ flex: "none", fontSize: 11, padding: "1px 6px", borderRadius: 5, border: "1px dashed var(--line)", color: "var(--text2)" }}>ohne Ticket</span>}
                    <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontWeight: 600 }}>{e.task_title && e.task_title.toUpperCase() !== String(e.ticket || "").toUpperCase() ? e.task_title : (e.note || e.project_name || "–")}</span>
                  </div>
                  <div style={{ fontSize: 11.5, color: "var(--text2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {e.note && e.note !== e.task_title ? e.note : e.project_name}
                    {e.target && e.target !== e.ticket && <span> · → {e.kind === "create" ? `neues Jira-Ticket in ${e.target.slice(4)}` : e.kind === "create-collector" ? `Sammelticket in ${e.target.slice(4)}` : e.kind === "month" || e.kind === "create-month" ? `Übergangs-Ticket ${e.target.startsWith("NEU:") ? "(neu)" : e.target}` : e.kind === "collector" ? `${e.target} (Sammelticket)` : e.target}</span>}
                  </div>
                  {e.status !== "skipped" && e.status !== "norule" && !["Ticket", "Projekt"].includes(e.account_from) && <AccountChip e={e} onDone={load} />}
                  {e.error && <div style={{ color: "var(--stop)", fontSize: 11 }}>{e.error}</div>}
                  {(e.status === "needs" || e.kind === "create" || e.kind === "create-collector") && (
                    <div style={{ display: "flex", gap: 4, flexWrap: "wrap", marginTop: 3, alignItems: "center" }}>
                      {[...(e.suggest || []), ...(Array.isArray(more[e.id]) ? more[e.id].filter(m => !(e.suggest || []).some(x => x.key === m.key)) : [])].slice(0, 5).map(sg => (
                        <button key={sg.key} title={sg.reasons.join(" · ")} onClick={() => applyTarget(e, sg.key)}
                          style={{ fontSize: 11, padding: "1px 8px", borderRadius: 10, border: `1px solid ${sg.score >= 80 ? "var(--accent)" : "var(--line)"}`, background: sg.score >= 80 ? "var(--accent-soft)" : "var(--card)", maxWidth: 320, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                          <b>{sg.key}</b> {sg.summary} · {sg.score} %</button>))}
                      <button onClick={() => findMore(e)} style={{ fontSize: 11, padding: "1px 8px", borderRadius: 10, border: "1px dashed var(--line)", background: "transparent", color: "var(--text2)" }}>{more[e.id] === "…" ? "suche…" : "in Jira suchen"}</button>
                      <button onClick={() => { const k = prompt("Jira-Ticket (z. B. OPS-12):"); if (k) applyTarget(e, k.trim().toUpperCase()); }} style={{ fontSize: 11, padding: "1px 8px", borderRadius: 10, border: "1px dashed var(--line)", background: "transparent", color: "var(--text2)" }}>Key eingeben</button>
                    </div>)}
                  {e.status === "dup" && <div style={{ fontSize: 11, color: "#8a6400", display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
                    In Tempo steht zur selben Zeit schon: {e.dup}
                    <button style={{ ...btn(false), padding: "1px 8px", fontSize: 11 }} onClick={() => dupAct(e.id, "skip")}>Nicht übertragen</button>
                    <button style={{ ...btn(false), padding: "1px 8px", fontSize: 11 }} onClick={() => dupAct(e.id, "push")}>Trotzdem übertragen</button></div>}
                </div>
                <span style={{ color: "var(--text2)", fontSize: 12, whiteSpace: "nowrap" }}>{fmtMin(e.minutes)}</span>
                <span className={`zp-pill ${PILL[e.status][1]}`}>{PILL[e.status][0]}</span>
                {dup.has(e.id) && <span className="zp-pill changed" title="Gleiche Aufgabe zur gleichen Zeit – vermutlich doppelt">doppelt?</span>}
                <button className="zp-nav" title="Buchung bearbeiten (Zeit, Ticket, Text)" onClick={() => window.dispatchEvent(new CustomEvent("zp:edit-entry", { detail: { id: e.id, date: e.date } }))}><Pencil size={14} /></button>
                <button className="zp-nav" title="Buchung löschen" onClick={() => deleteEntry(e)}><Trash2 size={14} /></button>
                <button className="zp-nav" title={e.status === "skipped" ? "Wieder aufnehmen" : "Von Tempo ausnehmen"} onClick={() => skip(e.id, e.status !== "skipped")}>
                  {e.status === "skipped" ? <Eye size={14} /> : <EyeOff size={14} />}</button>
              </div>)); })()}
          </div>);
      })}

      {data.deletions.length > 0 && (
        <div className="zp-card" style={{ padding: "12px 16px" }}>
          <div style={{ fontWeight: 600, fontSize: 13 }}>Vorgemerkte Löschungen in Tempo</div>
          {data.deletions.map(x => (
            <div key={x.id} style={{ fontSize: 12, color: x.error ? "var(--stop)" : "var(--text2)", padding: "3px 0" }}>
              {x.label || `Worklog ${x.worklog_id}`}{x.error ? ` – ${x.error}` : ""}
            </div>))}
        </div>)}
    </div>
  );
}

/* Ältere Buchungen einbeziehen + Monatssperren in Tempo */
function History({ data, busy, onCheck, onUnlock }) {
  const [since, setSince] = useState(data.since);
  const [open, setOpen] = useState(false);
  const months = data.months || [];
  const locked = months.filter(m => m.locked);
  const cur = new Date(); const curM = `${cur.getFullYear()}-${String(cur.getMonth() + 1).padStart(2, "0")}`;
  return (
    <div className="zp-card" style={{ padding: "12px 16px" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 10, cursor: "pointer" }} onClick={() => setOpen(!open)}>
        {open ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
        <div style={{ flex: 1 }}>
          <div style={{ fontWeight: 600, fontSize: 13 }}>Ältere Buchungen & Monatssperren</div>
          <div style={{ color: "var(--text2)", fontSize: 12 }}>Ab {dayTxt(data.since)}{data.oldest && data.oldest < data.since ? ` · lokal gibt es Buchungen ab ${dayTxt(data.oldest)}` : ""}
            {locked.length ? ` · gesperrt: ${locked.map(m => monTxt(m.month)).join(", ")}` : months.length ? " · keine Sperre gefunden" : " · noch nicht geprüft"}</div>
        </div>
      </div>
      {open && (<div style={{ marginTop: 10 }}>
        <div style={{ color: "var(--text2)", fontSize: 12, marginBottom: 8 }}>
          Vor der Übertragung prüft Zeitprotokoll je Monat in Tempo: Ist der Stundenzettel eingereicht/genehmigt oder der Zeitraum geschlossen, wird dieser Monat <b>nicht</b> angefasst.
          Außerdem wird verglichen, was in Tempo schon steht – Buchungen, die es zur selben Zeit dort bereits gibt (z. B. früher von Hand gebucht), werden als „Schon in Tempo?“ markiert und nur auf deinen Klick übertragen.
        </div>
        <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
          <label style={{ fontSize: 12, color: "var(--text2)" }}>Buchungen einbeziehen ab</label>
          <input type="date" value={since} onChange={e => setSince(e.target.value)} style={{ padding: "5px 8px" }} />
          {data.oldest && <button style={{ ...btn(false), padding: "4px 10px", fontSize: 12 }} onClick={() => setSince(data.oldest.slice(0, 8) + "01")}>Alles (ab {dayTxt(data.oldest)})</button>}
          <button style={btn(true)} disabled={!!busy} onClick={() => onCheck(since)}>{busy === "check" ? "Prüfe Tempo…" : "Übernehmen & in Tempo prüfen"}</button>
        </div>
        {months.length > 0 && (
          <div style={{ marginTop: 10 }}>
            {months.map(m => (
              <div key={m.month} style={{ display: "flex", gap: 10, alignItems: "center", padding: "5px 0", borderTop: "1px solid var(--line)", fontSize: 13 }}>
                <div style={{ width: 150 }}>{monTxt(m.month)}{m.month === curM ? " (aktuell)" : ""}</div>
                {m.locked ? <span className="zp-pill error">gesperrt</span> : <span className="zp-pill synced">offen</span>}
                <div style={{ flex: 1, color: "var(--text2)", fontSize: 12 }}>{m.reason || (m.remote_count ? `${m.remote_count} andere Worklogs in Tempo` : "")}</div>
                <span style={{ color: "var(--text3)", fontSize: 11 }}>geprüft {m.checked_at?.slice(5, 16).replace("T", " ")}</span>
                {m.locked ? <button style={{ ...btn(false), padding: "2px 8px", fontSize: 11 }} title="Erneut prüfen, z. B. nachdem der Zeitraum in Tempo wieder geöffnet wurde" onClick={() => onUnlock(m.month)}>Sperre neu prüfen</button> : null}
              </div>))}
          </div>)}
      </div>)}
    </div>);
}

/* ═══ Plane-Bereich: Übertragung + Zuordnung der Tempo-Altdaten ═══ */
import { PlaneSyncView } from "./zp-ui.jsx";
import { TicketBrowser } from "./zp-jira.jsx";

export function PlaneArea({ onOpenSettings }) {
  const [tab, setTab] = useState("tickets");
  const [openCount, setOpenCount] = useState(null);
  useEffect(() => { api("GET", "/integrations/plane/map").then(r => r && !r.error && setOpenCount(r.open.length)); }, [tab]);
  const chip = (k, l) => (
    <button key={k} onClick={() => setTab(k)} style={{ border: "1px solid " + (tab === k ? "var(--accent)" : "var(--line)"), background: tab === k ? "var(--accent-soft)" : "var(--card)",
      color: tab === k ? "var(--accent)" : "var(--text2)", borderRadius: 20, padding: "5px 14px", fontSize: 12, fontWeight: tab === k ? 600 : 500 }}>{l}</button>);
  return (
    <div>
      <div style={{ display: "flex", gap: 6, marginBottom: 12 }}>
        {chip("tickets", "Tickets")}
        {chip("sync", "Übertragung")}
        {chip("map", `Zuordnung Tempo-Altdaten${openCount ? ` (${openCount})` : ""}`)}
      </div>
      {tab === "tickets" ? <TicketBrowser kind="plane" onOpenSettings={onOpenSettings} /> : tab === "sync" ? <PlaneSyncView onOpenSettings={onOpenSettings} /> : <TicketMapPanel />}
    </div>
  );
}

const KEYRE = /^[A-Za-z][A-Za-z0-9_]*-\d+$/;
const pct = s => `${Math.round(s * 100)} %`;

export function TicketMapPanel() {
  const sm = isServerMode();
  const workspaces = usePlaneWorkspacesActive();
  const [wsId, setWsId] = useState("");
  const [data, setData] = useState(null);
  const [sugg, setSugg] = useState({});        // jira_key → [Kandidaten] | "…" | {error}
  const [pick, setPick] = useState({});        // jira_key → gewählter Plane-Key
  const [manual, setManual] = useState({});
  const [createPid, setCreatePid] = useState("");
  const [busy, setBusy] = useState("");
  const [msg, setMsg] = useState(null);
  const [show, setShow] = useState("open");

  useEffect(() => {
    if (sm && workspaces.length === 1 && !wsId) setWsId(String(workspaces[0].id));
  }, [sm, workspaces, wsId]);
  const load = useCallback(async () => {
    const r = await api("GET", `/integrations/plane/map${wsQ(wsId)}`);
    setData(r && !r.error ? r : { failed: true, error: r?.error || "Server nicht erreichbar" });
    if (r?.projects?.length) setCreatePid(v => v || String(r.projects[0].id));
  }, [wsId]);
  useEffect(() => { load(); }, [load]);

  const suggest = async (row) => {
    setSugg(v => ({ ...v, [row.jira_key]: "…" }));
    const r = await call("GET", `/integrations/plane/map/suggest?key=${encodeURIComponent(row.jira_key)}&title=${encodeURIComponent(row.title || "")}${wsQ(wsId, false)}`);
    if (r.error) { setSugg(v => ({ ...v, [row.jira_key]: { error: r.error } })); return; }
    setSugg(v => ({ ...v, [row.jira_key]: r }));
    if (r[0] && r[0].score >= 0.5) setPick(v => ({ ...v, [row.jira_key]: v[row.jira_key] || r[0].key }));
  };
  const suggestAll = async () => {
    setBusy("sugg"); setMsg(null);
    const todo = data.open.filter(r => !Array.isArray(sugg[r.jira_key])).slice(0, 15);
    for (const r of todo) { await suggest(r); await new Promise(x => setTimeout(x, 1500)); }   // Plane erlaubt ca. 60 Anfragen/Min.
    setBusy("");
    if (data.open.length > todo.length) setMsg({ text: `${todo.length} Tickets geprüft – „Vorschläge laden“ nochmal drücken für die nächsten.` });
  };

  const apply = async (row, body, label) => {
    setBusy(row.jira_key); setMsg(null);
    const r = await call("POST", "/integrations/plane/map/apply", { jira_key: row.jira_key, ...body });
    setBusy("");
    if (r.error) { setMsg({ err: true, text: `${row.jira_key}: ${r.error}` }); return false; }
    setMsg({ text: label || `${row.jira_key} → ${r.plane_key || ""} zugeordnet` });
    load(); return true;
  };
  const takeBest = async () => {
    const rows = data.open.filter(r => Array.isArray(sugg[r.jira_key]) && sugg[r.jira_key][0]?.score >= 0.7);
    if (!rows.length) { setMsg({ err: true, text: "Keine Vorschläge mit hoher Trefferquote (ab 70 %). Erst „Vorschläge laden“." }); return; }
    setBusy("best"); let ok = 0;
    for (const r of rows) { const x = await call("POST", "/integrations/plane/map/apply", { jira_key: r.jira_key, plane_key: sugg[r.jira_key][0].key }); if (!x.error) ok++; }
    setBusy(""); setMsg({ text: `${ok} von ${rows.length} Tickets zugeordnet` }); load();
  };

  if (!data) return <div style={{ color: "var(--text3)", padding: 30, textAlign: "center" }}>Lade…</div>;
  if (data.failed) return <div className="zp-card" style={{ padding: 24, textAlign: "center", color: "var(--stop)" }}>✗ {data.error}</div>;
  if (!data.configured) return <div className="zp-card" style={{ padding: 30, textAlign: "center", color: "var(--text2)" }}>Plane ist noch nicht eingerichtet (Einstellungen → Plane).</div>;

  const totalMin = data.open.reduce((s, r) => s + r.minutes, 0);
  const rows = show === "open" ? data.open : show === "skipped" ? data.skipped : [];

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <div className="zp-card" style={{ padding: "14px 16px", display: "flex", alignItems: "center", gap: 14, flexWrap: "wrap" }}>
        <div style={{ flex: 1, minWidth: 260 }}>
          <div style={{ fontWeight: 600, fontSize: 14 }}>Tempo-Altdaten den Plane-Work-Items zuordnen</div>
          <div style={{ color: "var(--text2)", fontSize: 12 }}>
            Das Zeitprotokoll hält die Zeit, Plane bekommt sie als Summe pro Work-Item. Hier legst du einmal je Jira-Ticket fest, wohin die alte Zeit gehört.
            Danach läuft sie über den Reiter „Übertragung“ mit nach Plane. Nicht zugeordnete Zeiten bleiben lokal.
          </div>
          <div style={{ color: "var(--text2)", fontSize: 12, marginTop: 4 }}>{data.open.length} Ticket{data.open.length === 1 ? "" : "s"} offen · {fmtMin(totalMin)}</div>
        </div>
        {sm && workspaces.length > 1 && (
          <select value={wsId} onChange={e => setWsId(e.target.value)} style={{ ...inpS, width: "auto", minWidth: 160 }} title="Plane-Workspace">
            <option value="">Alle Workspaces</option>
            {workspaces.map(w => <option key={w.id} value={w.id}>{w.name}</option>)}
          </select>
        )}
        <button style={btn(false)} onClick={suggestAll} disabled={!!busy || !data.open.length}>{busy === "sugg" ? "Suche…" : "Vorschläge laden"}</button>
        <button style={btn(true)} onClick={takeBest} disabled={!!busy}>Sichere Treffer übernehmen</button>
      </div>
      {msg && <div style={{ fontSize: 12, color: msg.err ? "var(--stop)" : "var(--accent)", padding: "0 4px" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</div>}

      <div style={{ display: "flex", gap: 6, alignItems: "center", flexWrap: "wrap" }}>
        {[["open", `Offen (${data.open.length})`], ["mapped", `Zugeordnet (${data.mapped.length})`], ["skipped", `Lokal belassen (${data.skipped.length})`]].map(([k, l]) => (
          <button key={k} onClick={() => setShow(k)} style={{ border: "1px solid " + (show === k ? "var(--accent)" : "var(--line)"), background: show === k ? "var(--accent-soft)" : "var(--card)",
            color: show === k ? "var(--accent)" : "var(--text2)", borderRadius: 20, padding: "4px 12px", fontSize: 12, fontWeight: show === k ? 600 : 500 }}>{l}</button>))}
      </div>

      {show === "mapped" && (data.mapped.length ? data.mapped.map(m => (
        <div key={m.jira_key} className="zp-card" style={{ padding: "9px 14px", display: "flex", alignItems: "center", gap: 10 }}>
          <b>{m.jira_key}</b><span style={{ color: "var(--text3)" }}>→</span><b style={{ color: "var(--accent)" }}>{m.plane_key}</b>
          <span style={{ flex: 1, minWidth: 0, color: "var(--text2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{m.title}</span>
          <span style={{ color: "var(--text2)", fontSize: 12 }}>{m.entries} Buchungen · {fmtMin(m.minutes)}</span>
          <button style={{ ...btn(false), padding: "4px 10px" }} disabled={!!busy} onClick={() => apply({ jira_key: m.jira_key }, { undo: true }, `${m.jira_key}: Zuordnung aufgehoben`)}>Rückgängig</button>
        </div>)) : <div className="zp-card" style={{ padding: 24, textAlign: "center", color: "var(--text2)" }}>Noch nichts zugeordnet</div>)}

      {show !== "mapped" && !rows.length && <div className="zp-card" style={{ padding: 24, textAlign: "center", color: "var(--text2)" }}>
        {show === "open" ? "Alles zugeordnet ✓ – oder es sind keine Tempo-Daten mit Ticket-Key vorhanden. Tempo-Import nochmal laufen lassen, damit der Key mitkommt." : "Nichts hier"}</div>}

      {show !== "mapped" && rows.map(row => {
        const s = sugg[row.jira_key], cands = Array.isArray(s) ? s : [];
        const chosen = pick[row.jira_key] || "";
        const man = (manual[row.jira_key] || "").trim().toUpperCase();
        const target = man || chosen;
        return (
          <div key={row.jira_key} className="zp-card" style={{ padding: "10px 14px", opacity: busy === row.jira_key ? .6 : 1 }}>
            <div style={{ display: "flex", alignItems: "baseline", gap: 10, flexWrap: "wrap" }}>
              <b style={{ fontSize: 14 }}>{row.jira_key}</b>
              <span style={{ flex: 1, minWidth: 160, color: row.title ? "var(--text)" : "var(--text3)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{row.title || "kein Titel bekannt"}</span>
              <span style={{ color: "var(--text2)", fontSize: 12 }}>{row.entries} Buchungen · {fmtMin(row.minutes)} · {row.first?.slice(0, 7)}{row.last && row.last.slice(0, 7) !== row.first?.slice(0, 7) ? ` – ${row.last.slice(0, 7)}` : ""}{row.projects ? ` · ${row.projects}` : ""}</span>
            </div>
            {show === "open" ? (
              <div style={{ display: "flex", gap: 8, marginTop: 8, flexWrap: "wrap", alignItems: "center" }}>
                <select style={{ ...inpS, flex: "1 1 260px", width: "auto" }} value={chosen} onChange={e => setPick(v => ({ ...v, [row.jira_key]: e.target.value }))} disabled={s === "…"}>
                  <option value="">{s === "…" ? "Suche in Plane…" : cands.length ? "Vorschlag wählen …" : "– noch keine Vorschläge –"}</option>
                  {cands.map(c => <option key={c.id} value={c.key}>{c.key} · {c.name} ({c.score >= .5 ? pct(c.score) + " · " : ""}{c.via})</option>)}
                </select>
                <input style={{ ...inpS, width: 120 }} placeholder="oder Key" value={manual[row.jira_key] || ""} onChange={e => setManual(v => ({ ...v, [row.jira_key]: e.target.value }))} />
                <button style={btn(true)} disabled={!!busy || !KEYRE.test(target)} onClick={() => apply(row, { plane_key: target })}>Zuordnen</button>
                <button style={btn(false)} disabled={!!busy || s === "…"} onClick={() => suggest(row)} title="In Plane nach passenden Work-Items suchen">Suchen</button>
                <button style={btn(false)} disabled={!!busy || !createPid} onClick={() => apply(row, { create: true, local_project_id: +createPid })} title="Neues Work-Item in Plane anlegen (Projekt unten gewählt)">Neu in Plane</button>
                <button style={btn(false)} disabled={!!busy} onClick={() => apply(row, { skip: true }, `${row.jira_key} bleibt lokal`)}>Lokal lassen</button>
              </div>
            ) : (
              <div style={{ marginTop: 6 }}><button style={{ ...btn(false), padding: "4px 10px" }} onClick={() => apply(row, { undo: true }, `${row.jira_key} wieder offen`)}>Wieder offen</button></div>
            )}
            {s && s.error && <div style={{ color: "var(--stop)", fontSize: 11, marginTop: 4 }}>{s.error}</div>}
          </div>);
      })}

      {show === "open" && data.projects.length > 0 && (
        <div style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 12, color: "var(--text2)" }}>
          „Neu in Plane“ legt das Work-Item an in:
          <select style={{ ...inpS, width: "auto" }} value={createPid} onChange={e => setCreatePid(e.target.value)}>
            {data.projects.map(p => <option key={p.id} value={p.id}>{p.plane_identifier ? `${p.plane_identifier} · ` : ""}{p.name}</option>)}
          </select>
        </div>)}
    </div>
  );
}
