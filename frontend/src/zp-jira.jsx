/* ═══════════════════════════════════════════════════════════════════
   Jira-Bereich (Übergang bis alles in Plane läuft): Tickets ansehen,
   auswählen, anlegen, kommentieren, Status ändern + Tempo-Übertragung.
   Einstellungen › Jira: Zugang, Schalter „aktiv“, Projekte verknüpfen.
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback, useRef } from "react";
import { Search, RefreshCw, ExternalLink, Play, Plus, X, Send, Link2 } from "lucide-react";
import { api, call, Box, lab, inpS, btn, hm, durTxt, isServerMode, newRequestKey } from "./zp-ui.jsx";
import { JiraInstancesPanel } from "./zp-servermode.jsx";
import { TempoOutboxView } from "./zp-tempo.jsx";

let jiraCache = null;
export function useJiraOn() {
  const [on, setOn] = useState(jiraCache);
  useEffect(() => { api("GET", "/jira/status").then(s => { jiraCache = !!(s && s.enabled); setOn(jiraCache); }); }, []);
  return on;
}
const CAT = { new: ["#5e6c84", "#dfe1e6"], indeterminate: ["#0052cc", "#deebff"], done: ["#006644", "#e3fcef"] };
const Status = ({ name, cat }) => { const [c, b] = CAT[cat] || CAT.new; return <span style={{ fontSize: 10, fontWeight: 700, textTransform: "uppercase", padding: "2px 7px", borderRadius: 4, color: c, background: b, whiteSpace: "nowrap" }}>{name}</span>; };
const ago = iso => { if (!iso) return ""; const m = (Date.now() - Date.parse(iso)) / 60e3; return m < 60 ? `vor ${Math.round(m)} min` : m < 1440 ? `vor ${Math.round(m / 60)} h` : new Date(iso).toLocaleDateString("de-DE", { day: "2-digit", month: "2-digit" }); };
const chip = (on, label, onClick) => <button onClick={onClick} style={{ border: "1px solid " + (on ? "var(--accent)" : "var(--line)"), background: on ? "var(--accent-soft)" : "var(--card)", color: on ? "var(--accent)" : "var(--text2)", borderRadius: 20, padding: "4px 12px", fontSize: 12, fontWeight: on ? 600 : 500 }}>{label}</button>;

/* Gleiche Ansicht für Jira und Plane – nur die Adressen unterscheiden sich */
const SRC = {
  jira:  { search: "/jira/search", one: k => `/jira/issue/${k}`, color: "#0052cc", name: "Jira" },
  plane: { search: "/plane/tickets", one: k => `/plane/ticket/${k}`, color: "#3f76ff", name: "Plane" },
};
// Servermodus mit mehreren Jira-Instanzen: Instanz als ?instance=<Id> an die Ticket-Aufrufe hängen (Pfad bleibt sonst unverändert)
const instQ = inst => isServerMode() && inst ? `?instance=${inst}` : "";
const GROUP_CAT = { backlog: "new", unstarted: "new", started: "indeterminate", completed: "done", cancelled: "done" };
async function startTimer(key, comment, kind = "jira", inst) {
  const t = await call("POST", `${SRC[kind].one(key)}/task${kind === "jira" ? instQ(inst) : ""}`);
  if (t.error) return t;
  return call("POST", "/timer/start", { project_id: t.project_id, task_id: t.task_id, comment: comment || null });
}

export function JiraArea({ onOpenSettings, initialTab }) {
  const [tab, setTab] = useState(initialTab || "tickets");
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
        {chip(tab === "tickets", "Tickets", () => setTab("tickets"))}
        {chip(tab === "tempo", "Übertragung nach Tempo", () => setTab("tempo"))}
        <span style={{ marginLeft: "auto", fontSize: 11, color: "var(--text2)" }}>Übergangslösung – abschaltbar unter Einstellungen › Jira & Tempo</span>
      </div>
      {tab === "tickets" ? <Tickets onOpenSettings={onOpenSettings} /> : <TempoOutboxView onOpenSettings={onOpenSettings} />}
    </div>);
}

export function TicketBrowser({ kind = "jira", onOpenSettings }) { return <Tickets kind={kind} onOpenSettings={onOpenSettings} />; }
function Tickets({ onOpenSettings, kind = "jira" }) {
  const S = SRC[kind];
  const [q, setQ] = useState(""); const [mine, setMine] = useState(true); const [open, setOpen] = useState(true); const [jqlMode, setJqlMode] = useState(false);
  const [data, setData] = useState(null); const [sel, setSel] = useState(null); const [selInst, setSelInst] = useState(null); const [creating, setCreating] = useState(false); const [msg, setMsg] = useState(null);
  const load = useCallback(async (query = q) => {
    setData(d => d ? { ...d, loading: true } : null);
    const p = jqlMode ? `jql=${encodeURIComponent(query)}` : `q=${encodeURIComponent(query)}&mine=${mine ? 1 : 0}&open=${open ? 1 : 0}`;
    const r = await call("GET", `${S.search}?${p}`);
    setData(r);
  }, [q, mine, open, jqlMode]);
  useEffect(() => { load(); }, [mine, open]);   // eslint-disable-line
  const start = async (key, title, inst) => { const r = await startTimer(key, null, kind, inst); setMsg(r.error ? { err: 1, text: r.error } : { text: `Timer läuft auf ${key} ${title || ""}` }); load(); };

  if (data?.error && /ausgeschaltet|nicht eingerichtet/.test(data.error)) return (
    <div className="zp-card" style={{ padding: 30, textAlign: "center" }}><div style={{ marginBottom: 12 }}>{data.error}</div><button style={btn(true)} onClick={onOpenSettings}>Zu den Einstellungen</button></div>);
  return (
    <div style={{ display: "flex", gap: 12, alignItems: "flex-start" }}>
      <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 10 }}>
        <div className="zp-card" style={{ padding: "12px 14px" }}>
          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <div style={{ position: "relative", flex: 1 }}>
              <Search size={15} style={{ position: "absolute", left: 10, top: 9, color: "var(--text2)" }} />
              <input value={q} onChange={e => setQ(e.target.value)} onKeyDown={e => e.key === "Enter" && load()} placeholder={jqlMode ? "JQL, z. B. project = OPS AND status = \"In Arbeit\"" : kind === "plane" ? "Ticket-Key (PRJA-581) oder Suchtext …" : "Ticket-Key (OPS-12) oder Suchtext …"}
                style={{ ...inpS, paddingLeft: 32, fontFamily: jqlMode ? "ui-monospace,Menlo,monospace" : "inherit" }} />
            </div>
            <button style={btn(true)} onClick={() => load()}>Suchen</button>
            <button style={btn(false)} onClick={() => setCreating(true)}><Plus size={14} /> Neues Ticket</button>
          </div>
          <div style={{ display: "flex", gap: 6, marginTop: 8, alignItems: "center", flexWrap: "wrap" }}>
            {!jqlMode && chip(mine, "Nur mir zugewiesen", () => setMine(!mine))}
            {!jqlMode && chip(open, "Nur offene", () => setOpen(!open))}
            {kind === "jira" && chip(jqlMode, "JQL", () => setJqlMode(!jqlMode))}
            <span style={{ marginLeft: "auto", fontSize: 12, color: "var(--text2)" }}>{data && !data.error ? `${data.total} Ticket${data.total === 1 ? "" : "s"}` : ""}</span>
            <button className="zp-nav" onClick={() => load()} title="Neu laden"><RefreshCw size={14} /></button>
          </div>
        </div>
        {msg && <div style={{ fontSize: 12, color: msg.err ? "var(--stop)" : "var(--accent)", padding: "0 4px" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</div>}
        <div className="zp-card" style={{ overflow: "hidden" }}>
          {!data ? <div style={{ padding: 24, textAlign: "center", color: "var(--text2)" }}>Lade…</div>
            : data.error ? <div style={{ padding: 16, color: "var(--stop)" }}>✗ {data.error}</div>
            : !data.issues.length ? <div style={{ padding: 24, textAlign: "center", color: "var(--text2)" }}>Keine Tickets gefunden</div>
            : data.issues.map(i => (
              <div key={(i.instance_id || "") + i.key} onClick={() => { setSel(i.key); setSelInst(i.instance_id || null); }} style={{ display: "flex", gap: 10, alignItems: "center", padding: "9px 14px", borderTop: "1px solid var(--line)", cursor: "pointer", background: sel === i.key ? "var(--sel)" : "transparent" }}>
                <span style={{ fontWeight: 700, fontSize: 12, color: S.color, minWidth: 78, fontVariantNumeric: "tabular-nums" }}>{i.key}</span>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{i.summary}</div>
                  <div style={{ fontSize: 11, color: "var(--text2)" }}>{isServerMode() && i.instance ? `${i.instance} · ` : ""}{i.type ? `${i.type} · ` : ""}{i.project_name}{i.priority ? ` · ${i.priority}` : ""}{i.assignee && !mine ? ` · ${i.assignee}` : ""} · {ago(i.updated)}</div>
                </div>
                {i.booked_seconds > 0 && <span style={{ fontSize: 12, color: "var(--text2)", whiteSpace: "nowrap" }}>{durTxt(i.booked_seconds)}</span>}
                <Status name={i.status} cat={i.status_cat || GROUP_CAT[i.state_group]} />
                <button className="zp-nav" title="Timer auf diesem Ticket starten" onClick={e => { e.stopPropagation(); start(i.key, i.summary, i.instance_id); }}><Play size={14} /></button>
              </div>))}
        </div>
      </div>
      {sel && <Detail key={sel + ":" + (selInst || "")} k={sel} inst={selInst} kind={kind} onClose={() => setSel(null)} onChanged={() => load()} onStart={start} />}
      {creating && kind === "plane" && <PlaneCreateDialog onClose={() => setCreating(false)} onDone={(key, started) => { setCreating(false); setMsg({ text: `${key} angelegt${started ? " – Timer läuft" : ""}` }); setSel(key); load(); }} />}
      {creating && kind === "jira" && <CreateDialog onClose={() => setCreating(false)} onDone={(key, started) => { setCreating(false); setMsg({ text: `${key} angelegt${started ? " – Timer läuft" : ""}` }); setSel(key); load(); }} />}
    </div>);
}

function Detail({ k, kind = "jira", inst, onClose, onChanged, onStart }) {
  const S = SRC[kind];
  const q = kind === "jira" ? instQ(inst) : "";
  const [d, setD] = useState(null); const [comment, setComment] = useState(""); const [busy, setBusy] = useState(""); const [err, setErr] = useState("");
  const load = useCallback(() => call("GET", S.one(k) + q).then(setD), [k, q]);   // eslint-disable-line
  useEffect(() => { load(); }, [load]);
  const act = async (label, fn) => { setBusy(label); setErr(""); const r = await fn(); setBusy(""); if (r?.error) setErr(r.error); else { await load(); onChanged(); } };
  return (
    <div className="zp-card" style={{ width: 400, flex: "0 0 400px", padding: "14px 16px", position: "sticky", top: 70, maxHeight: "calc(100vh - 90px)", overflow: "auto" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <span style={{ fontWeight: 700, color: S.color }}>{k}</span>
        {d?.url && <a href={d.url} target="_blank" rel="noreferrer" title={`In ${S.name} öffnen`} style={{ color: "var(--text2)", display: "flex" }}><ExternalLink size={14} /></a>}
        <div style={{ flex: 1 }} /><button className="zp-nav" onClick={onClose}><X size={15} /></button>
      </div>
      {!d ? "…" : d.error ? <div style={{ color: "var(--stop)" }}>{d.error}</div> : (<>
        <div style={{ fontWeight: 600, fontSize: 15, margin: "6px 0" }}>{d.summary}</div>
        <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
          <Status name={d.status} cat={d.status_cat || GROUP_CAT[d.state_group]} />
          {(() => { const opts = d.transitions ? d.transitions.map(t => ({ id: t.id, label: t.name + (t.to && t.to !== t.name ? ` → ${t.to}` : ""), name: t.name }))
              : (d.states || []).filter(x => x.id !== d.state_id).map(x => ({ id: x.id, label: x.name, name: x.name }));
            return opts.length > 0 && <select value="" disabled={!!busy} onChange={e => { const t = opts.find(x => x.id === e.target.value); if (!t) return;
              act("tr", () => kind === "jira" ? call("POST", `/jira/issue/${k}/transition${q}`, { id: t.id, name: t.name }) : call("POST", `/plane/ticket/${k}/state`, { state_id: t.id, name: t.name })); }} style={{ padding: "3px 6px", fontSize: 12 }}>
              <option value="">Status ändern …</option>{opts.map(t => <option key={t.id} value={t.id}>{t.label}</option>)}
            </select>; })()}
          <button style={{ ...btn(true), padding: "4px 10px", marginLeft: "auto" }} onClick={() => onStart(k, d.summary, inst)}><Play size={13} /> Timer starten</button>
        </div>
        <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 8 }}>{d.type ? `${d.type} · ` : ""}{d.project_name} · {d.priority || "–"}<br />Bearbeiter: {d.assignee || "–"}{d.reporter ? ` · Melder: ${d.reporter}` : ""}{d.due ? ` · fällig ${d.due}` : ""}</div>
        <div style={{ fontSize: 12, marginTop: 6 }}>Bei dir gebucht: <b>{durTxt(d.booked_seconds)}</b></div>
        {d.description && <div style={{ marginTop: 10, fontSize: 13, whiteSpace: "pre-wrap", background: "var(--bg)", borderRadius: 6, padding: "8px 10px", maxHeight: 260, overflow: "auto" }}>{d.description}</div>}
        <div style={{ fontWeight: 600, fontSize: 12, marginTop: 14 }}>Kommentare ({d.comments.length})</div>
        {d.comments.map(c => (
          <div key={c.id} style={{ borderTop: "1px solid var(--line)", padding: "6px 0", fontSize: 12 }}>
            <div style={{ color: "var(--text2)" }}><b style={{ color: "var(--text)" }}>{c.author}</b> · {ago(c.created)}</div>
            <div style={{ whiteSpace: "pre-wrap" }}>{c.body}</div>
          </div>))}
        <textarea value={comment} onChange={e => setComment(e.target.value)} placeholder="Kommentar schreiben …" rows={3} style={{ width: "100%", marginTop: 6, padding: "6px 8px", fontSize: 13, resize: "vertical" }} />
        <button style={{ ...btn(false), marginTop: 4 }} disabled={!comment.trim() || !!busy} onClick={() => act("c", async () => { const r = await call("POST", `${S.one(k)}/comment${q}`, { body: comment }); if (!r.error) setComment(""); return r; })}><Send size={13} /> Kommentieren</button>
        {err && <div style={{ color: "var(--stop)", fontSize: 12, marginTop: 6 }}>{err}</div>}
        {d.entries.length > 0 && (<>
          <div style={{ fontWeight: 600, fontSize: 12, marginTop: 14 }}>Deine Buchungen</div>
          {d.entries.map(e => <div key={e.id} style={{ display: "flex", gap: 8, fontSize: 12, borderTop: "1px solid var(--line)", padding: "4px 0" }}>
            <span style={{ color: "var(--text2)", width: 120 }}>{new Date(e.start_time).toLocaleDateString("de-DE", { day: "2-digit", month: "2-digit" })} {hm(e.start_time)}–{hm(e.end_time)}</span>
            <span style={{ flex: 1 }}>{e.comment || ""}</span><span>{durTxt(e.duration_seconds)}</span></div>)}
        </>)}
      </>)}
    </div>);
}

function CreateDialog({ onClose, onDone }) {
  const sm = isServerMode();
  const [projects, setProjects] = useState([]); const [f, setF] = useState({ project: "", summary: "", description: "", start: true }); const [err, setErr] = useState(""); const [busy, setBusy] = useState(false);
  const [insts, setInsts] = useState([]); const [instId, setInstId] = useState("");   // Servermodus: Jira-Instanz wählen
  const rk = useRef({ sig: "", key: newRequestKey() });   // je Dialog-Öffnung eine Auftrags-Kennung (Wiederholung = dieselbe)
  useEffect(() => { api("GET", "/jira/projects").then(r => r && !r.error && setProjects(r.jira || []));
    if (sm) api("GET", "/jira/instances").then(l => { if (Array.isArray(l)) { setInsts(l); if (l.length === 1) setInstId(String(l[0].id)); } }); }, []);
  const shown = sm && instId ? projects.filter(p => !p.instance_id || String(p.instance_id) === instId) : projects;
  const save = async () => {
    if (sm && insts.length > 1 && !instId) { setErr("Bitte die Jira-Instanz wählen"); return; }
    setBusy(true); setErr("");
    let body = f;
    if (sm) {   // Go-Server: nur nach ausdrücklichem Klick (confirm), idempotent über request_key
      const sig = JSON.stringify([f.project, f.summary, f.description, instId]);
      if (rk.current.sig !== sig) rk.current = { sig, key: newRequestKey() };
      body = { project: f.project, summary: f.summary, description: f.description, confirm: true, request_key: rk.current.key, ...(instId ? { instance_id: +instId } : {}) };
    }
    const r = await call("POST", "/jira/issues", body);
    if (r.error) { setErr(r.error); setBusy(false); return; }
    if (f.start) await call("POST", "/timer/start", { project_id: r.project_id, task_id: r.task_id });
    onDone(r.key, f.start);
  };
  return (
    <div style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.3)", zIndex: 50, display: "flex", alignItems: "center", justifyContent: "center" }} onClick={onClose}>
      <div className="zp-card" style={{ width: 480, padding: "16px 18px" }} onClick={e => e.stopPropagation()}>
        <div style={{ fontWeight: 600, fontSize: 15 }}>Neues Jira-Ticket</div>
        {sm && insts.length > 1 && (<><label style={lab}>Jira-Instanz</label>
          <select value={instId} onChange={e => { setInstId(e.target.value); setF(v => ({ ...v, project: "" })); }} style={{ ...inpS }}><option value="">Instanz wählen …</option>{insts.map(i => <option key={i.id} value={i.id}>{i.name}</option>)}</select></>)}
        <label style={lab}>Projekt</label>
        <select value={f.project} onChange={e => setF({ ...f, project: e.target.value })} style={{ ...inpS }}><option value="">Projekt wählen …</option>{shown.map(p => <option key={(p.instance_id || "") + p.key} value={p.key}>{p.key} – {p.name}</option>)}</select>
        <label style={lab}>Titel</label><input style={inpS} value={f.summary} onChange={e => setF({ ...f, summary: e.target.value })} autoFocus />
        <label style={lab}>Beschreibung (optional)</label><textarea rows={4} style={{ ...inpS, resize: "vertical" }} value={f.description} onChange={e => setF({ ...f, description: e.target.value })} />
        <label style={{ display: "flex", gap: 6, alignItems: "center", fontSize: 12, marginTop: 8 }}><input type="checkbox" checked={f.start} onChange={e => setF({ ...f, start: e.target.checked })} /> Danach gleich den Timer darauf starten</label>
        {err && <div style={{ color: "var(--stop)", fontSize: 12, marginTop: 6 }}>{err}</div>}
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", marginTop: 12 }}>
          <button style={btn(false)} onClick={onClose}>Abbrechen</button>
          <button style={btn(true)} disabled={!f.project || !f.summary.trim() || busy} onClick={save}>{busy ? "Lege an…" : "Anlegen"}</button>
        </div>
      </div>
    </div>);
}

function PlaneCreateDialog({ onClose, onDone }) {
  const sm = isServerMode();
  const rk = useRef({ sig: "", key: newRequestKey() });
  const [projects, setProjects] = useState([]); const [f, setF] = useState({ local_project_id: "", name: "", description: "", start: true }); const [err, setErr] = useState(""); const [busy, setBusy] = useState(false);
  useEffect(() => { api("GET", "/projects").then(p => setProjects(Array.isArray(p) ? p.filter(x => x.plane_project_id && x.status !== "archived") : [])); }, []);
  const save = async () => {
    setBusy(true); setErr("");
    let extra = {};
    if (sm) {   // Go-Server: nur nach ausdrücklichem Klick (confirm), idempotent über request_key
      const sig = JSON.stringify([f.local_project_id, f.name, f.description]);
      if (rk.current.sig !== sig) rk.current = { sig, key: newRequestKey() };
      extra = { confirm: true, request_key: rk.current.key };
    }
    const r = await call("POST", "/integrations/plane/work-items", { local_project_id: Number(f.local_project_id), name: f.name, description: f.description, ...extra });
    if (r.error) { setErr(r.error); setBusy(false); return; }
    if (f.start) await call("POST", "/timer/start", { project_id: r.local_project_id, task_id: r.task_id });
    onDone(r.key, f.start);
  };
  return (
    <div style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.3)", zIndex: 50, display: "flex", alignItems: "center", justifyContent: "center" }} onClick={onClose}>
      <div className="zp-card" style={{ width: 480, padding: "16px 18px" }} onClick={e => e.stopPropagation()}>
        <div style={{ fontWeight: 600, fontSize: 15 }}>Neues Plane-Ticket</div>
        <label style={lab}>Projekt</label>
        <select value={f.local_project_id} onChange={e => setF({ ...f, local_project_id: e.target.value })} style={{ ...inpS }}><option value="">Projekt wählen …</option>{projects.map(p => <option key={p.id} value={p.id}>{(p.plane_identifier || "").toUpperCase()} – {p.name}</option>)}</select>
        <label style={lab}>Titel</label><input style={inpS} value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus />
        <label style={lab}>Beschreibung (optional)</label><textarea rows={4} style={{ ...inpS, resize: "vertical" }} value={f.description} onChange={e => setF({ ...f, description: e.target.value })} />
        <label style={{ display: "flex", gap: 6, alignItems: "center", fontSize: 12, marginTop: 8 }}><input type="checkbox" checked={f.start} onChange={e => setF({ ...f, start: e.target.checked })} /> Danach gleich den Timer darauf starten</label>
        {err && <div style={{ color: "var(--stop)", fontSize: 12, marginTop: 6 }}>{err}</div>}
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", marginTop: 12 }}>
          <button style={btn(false)} onClick={onClose}>Abbrechen</button>
          <button style={btn(true)} disabled={!f.local_project_id || !f.name.trim() || busy} onClick={save}>{busy ? "Lege an…" : "Anlegen"}</button>
        </div>
      </div>
    </div>);
}

/* Zertifikat eines internen Servers bestätigen (unvollständige Kette) */
export function TlsTrust({ reloadKey, onTrusted }) {
  const [t, setT] = useState(null);
  const load = () => api("GET", "/security/tls").then(r => r && !r.error && setT(r));
  useEffect(() => { load(); }, [reloadKey]);   // eslint-disable-line
  if (!t) return null;
  const c = t.server?.candidate, rc = t.candidate, pins = Object.entries(t.server?.pins || {});
  const box = (title, lines, fp, onClick) => (
    <div style={{ marginTop: 12, padding: "10px 12px", borderRadius: 8, border: "1px solid #e5c46b", background: "color-mix(in srgb, #f5c542 14%, var(--card))" }}>
      <div style={{ fontWeight: 600, fontSize: 13 }}>{title}</div>
      {lines.map((l, i) => <div key={i} style={{ fontSize: 12, marginTop: 2 }}>{l}</div>)}
      <div style={{ fontSize: 11, marginTop: 4, fontFamily: "ui-monospace,Menlo,monospace", wordBreak: "break-all" }}>SHA-256: {fp}</div>
      <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 6 }}>Vergleich: In Safari die Jira-Seite öffnen › Schloss › Zertifikat einblenden › Details › „Fingerabdrücke SHA-256“. Stimmt er überein, kannst du vertrauen.</div>
      <button style={{ ...btn(true), marginTop: 8 }} onClick={onClick}>Diesem Zertifikat vertrauen</button>
    </div>);
  return (<>
    {c && box(`Zertifikat von ${c.host} bestätigen`, [`Ausgestellt für ${c.subject} von ${c.issuer} · gültig bis ${c.valid_to}`, "Der Server schickt seine Zertifikatskette nicht vollständig mit. Nach Bestätigung vertraut Zeitprotokoll genau diesem Zertifikat – wird es ausgetauscht, fragt es wieder nach."], c.fingerprint,
      async () => { if (!confirm(`Zertifikat von ${c.host} vertrauen?\n\nSHA-256:\n${c.fingerprint}`)) return; await call("POST", "/security/trust-server", { host: c.host, fingerprint: c.fingerprint }); await load(); onTrusted && onTrusted(); })}
    {!c && rc && box(`Unbekanntes Root-Zertifikat von ${rc.host}`, [`${rc.subject} · gültig bis ${rc.valid_to}`], rc.fingerprint,
      async () => { if (!confirm(`Root-Zertifikat „${rc.subject}“ vertrauen?\n\nSHA-256:\n${rc.fingerprint}`)) return; await call("POST", "/security/trust-root", { fingerprint: rc.fingerprint }); await load(); onTrusted && onTrusted(); })}
    {pins.length > 0 && <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 8 }}>Bestätigte Server-Zertifikate: {pins.map(([h, fp]) => (
      <span key={h} style={{ marginRight: 10 }}>{h} <span title={fp} style={{ fontFamily: "ui-monospace,Menlo,monospace" }}>({fp.slice(0, 11)}…)</span> <a href="#" onClick={async e => { e.preventDefault(); if (confirm(`Bestätigung für ${h} entfernen?`)) { await call("POST", "/security/untrust-server", { host: h }); load(); } }}>entfernen</a></span>))}</div>}
  </>);
}

/* ═══ Einstellungen → Jira ═══ */
export function JiraSettingsTab() {
  const sm = isServerMode();
  const [f, setF] = useState({ jira_enabled: "1", jira_base_url: "", jira_token: "" }); const [msg, setMsg] = useState(null); const [test, setTest] = useState(null);
  const [pr, setPr] = useState(null); const [links, setLinks] = useState({});
  useEffect(() => { api("GET", "/settings").then(s => s && setF({ jira_enabled: s.jira_enabled === "0" ? "0" : "1", jira_base_url: s.jira_base_url || "", jira_token: s.jira_token || "" })); }, []);
  const loadProjects = async () => { const r = await call("GET", "/jira/projects"); setPr(r); if (!r.error) setLinks(Object.fromEntries(r.jira.map(p => [p.key, p.linked || ""]))); };
  const save = async () => {
    // Servermodus: Adresse und Token je Instanz (Administrator bzw. eigene Token-Liste) – hier nur der Schalter „aktiv“
    const r = await call("PUT", "/settings", sm ? { jira_enabled: f.jira_enabled } : { ...f, jira_base_url: f.jira_base_url.trim().replace(/\/+$/, "") });
    if (r.error) return setMsg({ err: 1, text: r.error });
    jiraCache = f.jira_enabled === "1";
    setMsg({ text: "Gespeichert" }); setTest(await call("GET", "/integrations/tempo-push/status"));
  };
  return (
    <div>
      <Box title="Jira- & Tempo-Anbindung" sub="Eine Anbindung für beides: Jira-Tickets und Tempo-Zeiten. Übergangslösung: Solange abgerechnet wird über Jira/Tempo, kannst du hier Tickets ansehen, auswählen und bearbeiten. Später einfach ausschalten – Zeitprotokoll läuft dann ohne Jira weiter, alle Buchungen bleiben erhalten.">
        <div style={{ display: "flex", gap: 8 }}>
          {[["1", "Aktiv", "Bereich „Jira“ mit Tickets, Tempo-Übertragung und -Import"], ["0", "Aus", "Kein Zugriff auf Jira/Tempo, Bereich ausgeblendet"]].map(([k, l, d]) => (
            <button key={k} onClick={() => setF(v => ({ ...v, jira_enabled: k }))} style={{ flex: 1, textAlign: "left", padding: "10px 12px", borderRadius: 8, border: `1px solid ${f.jira_enabled === k ? "var(--accent)" : "var(--line)"}`, background: f.jira_enabled === k ? "var(--accent-soft)" : "var(--card)" }}>
              <div style={{ fontWeight: 600 }}>{f.jira_enabled === k ? "● " : "○ "}{l}</div><div style={{ color: "var(--text2)", fontSize: 12 }}>{d}</div></button>))}
        </div>
        {sm ? (<>
          <div style={{ fontSize: 12, color: "var(--text2)", margin: "12px 0 8px" }}>Jira-Adressen legt der Administrator fest. Du trägst je Instanz dein eigenes Token ein.</div>
          <JiraInstancesPanel mode="tokens" status={test} />
        </>) : (<>
        <label style={lab}>Jira-Adresse</label>
        <input style={inpS} placeholder="https://jira.example.com" value={f.jira_base_url} onChange={e => setF({ ...f, jira_base_url: e.target.value })} />
        <label style={lab}>Token (Jira Personal Access Token – bleibt lokal, wird nur maskiert angezeigt)</label>
        <input style={inpS} type="password" value={f.jira_token} onChange={e => setF({ ...f, jira_token: e.target.value })} /></>)}
        <div style={{ display: "flex", gap: 8, alignItems: "center", marginTop: 12 }}>
          <button style={btn(true)} onClick={save}>{sm ? "Speichern & Verbindungen testen" : "Speichern & Verbindung testen"}</button>
          {msg && <span style={{ fontSize: 12, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</span>}
        </div>
        {test && !sm && <div style={{ marginTop: 8, fontSize: 12, color: test.user ? "var(--accent)" : "var(--stop)" }}>{test.user ? `✓ Verbunden als ${test.user}` : `✗ ${test.message || test.error}`}</div>}
        {test?.error && sm && <div style={{ marginTop: 8, fontSize: 12, color: "var(--stop)" }}>✗ {test.error}</div>}
        {!sm && <TlsTrust reloadKey={test} onTrusted={async () => setTest(await call("GET", "/integrations/tempo-push/status"))} />}
        <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 8 }}>Tempo-Account, Buchungsregeln und Zertifikate: oben „Tempo-Übertragung“.</div>
      </Box>
      <Box title="Projekte verknüpfen" sub="Verknüpft wird jetzt an genau einer Stelle: Projekte › Quellen. Dort siehst du Plane- und Jira-Projekte nebeneinander, übernimmst neue, schaltest ungenutzte stumm und legst fest, wohin die Zeit geht.">
        <button style={btn(true)} onClick={() => { location.hash = "projects"; }}>Zu Projekte › Quellen</button>
      </Box>
    </div>);
}
