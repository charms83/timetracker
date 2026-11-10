/* ═══════════════════════════════════════════════════════════════════
   To-dos & Planung (v6.18.0)
   Spalten: Überfällig · Heute · Morgen · Diese Woche · Später · Eingang
   Karten ziehen = umplanen. ▶ = Timer auf dem Ticket. Vorschläge vom Assistenten.
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback } from "react";
import { Plus, Play, Check, X, Settings2, Sparkles, AlertTriangle, CalendarDays, Clock, Trash2, Pencil, Repeat, ChevronRight } from "lucide-react";
import { api, call, btn, TicketInput, projectFromTicket } from "./zp-ui.jsx";

const hm = m => { m = Math.max(0, Math.round(m || 0)); return m >= 60 ? `${Math.floor(m / 60)}:${String(m % 60).padStart(2, "0")} h` : `${m} min`; };
const dd = s => s ? new Date(s + "T12:00").toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit" }) : "";
const PRIO = { 1: ["Hoch", "#C4271B"], 2: ["Mittel", "#b58100"], 3: ["Niedrig", "#8e8e93"] };
const SYS = { plane: ["Plane", "#7c5cd6"], jira: ["Jira", "#2f6fd6"] };
const COLS = [["overdue", "Überfällig"], ["today", "Heute"], ["tomorrow", "Morgen"], ["week", "Diese Woche"], ["later", "Später"], ["inbox", "Eingang"]];
const WHEN = [["today", "Heute"], ["tomorrow", "Morgen"], ["week", "Diese Woche"], ["nextweek", "Nächste Woche"], ["", "Eingang"]];
const EST = [15, 30, 60, 90, 120, 240];
const CSS = `
.zp-tcols{display:grid;grid-template-columns:repeat(auto-fill,minmax(250px,1fr));gap:10px;align-items:start}
.zp-tcol{background:color-mix(in srgb,var(--text3) 6%,transparent);border-radius:10px;padding:8px;min-height:90px;transition:background .1s}
.zp-tcol.drop{background:var(--accent-soft);outline:2px dashed var(--accent)}
.zp-tcard{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:7px 9px;margin-top:6px;cursor:grab;position:relative}
.zp-tcard:hover{border-color:color-mix(in srgb,var(--accent) 45%,var(--line))}
.zp-tcard .acts{position:absolute;right:6px;top:6px;display:flex;gap:3px;opacity:0;transition:opacity .1s;background:var(--card)}
.zp-tcard:hover .acts{opacity:1}
.zp-tact{border:1px solid var(--line);background:var(--card);border-radius:6px;padding:2px 5px;cursor:pointer;color:var(--text2);display:inline-flex;align-items:center}
.zp-tact:hover{color:var(--accent);border-color:var(--accent)}
.zp-tq .zp-suggest input{width:100%;padding:7px 9px;box-sizing:border-box}
.zp-chip{display:inline-block;font-size:10.5px;border-radius:5px;padding:0 5px;margin-right:4px;font-weight:600}
`;

function Cap({ c }) {
  if (!c) return null;
  const free = Math.max(0, c.available - c.meetings - c.booked);
  const over = c.planned > free;
  return (
    <div title={`Soll ${hm(c.available)} − Termine ${hm(c.meetings)} − schon gebucht ${hm(c.booked)} = frei ${hm(free)}`} style={{ fontSize: 10.5, color: over ? "#C4271B" : "var(--text2)", marginTop: 2 }}>
      geplant {hm(c.planned)} von {hm(free)} frei{over ? " – zu viel" : ""}
      <div style={{ height: 3, borderRadius: 2, background: "color-mix(in srgb,var(--text3) 20%,transparent)", marginTop: 3 }}><div style={{ height: "100%", borderRadius: 2, width: `${free ? Math.min(100, c.planned / free * 100) : c.planned ? 100 : 0}%`, background: over ? "#C4271B" : "var(--accent)" }} /></div>
    </div>);
}

function Card({ t, today, onChanged, onEdit, setMsg }) {
  const [busy, setBusy] = useState(false);
  const due = t.due_date, late = due && due < today, soon = due && !late && due <= today;
  const act = async (fn) => { setBusy(true); const r = await fn(); setBusy(false); if (r?.error) setMsg({ err: 1, text: r.error }); onChanged(); };
  const [sn, sc] = t.system ? SYS[t.system] : [];
  const tracked = Math.round((t.tracked_seconds || 0) / 60);
  return (
    <div className="zp-tcard" draggable onDragStart={e => { e.dataTransfer.setData("text/todo", String(t.id)); e.dataTransfer.effectAllowed = "move"; }} style={{ opacity: busy ? .5 : 1, borderLeft: `3px solid ${PRIO[t.priority]?.[1] || "var(--line)"}` }}>
      <div style={{ display: "flex", gap: 7, alignItems: "flex-start" }}>
        <input type="checkbox" checked={t.status === "done"} onChange={e => act(() => call("PUT", `/todo/${t.id}`, { done: e.target.checked }))} style={{ marginTop: 3 }} title="Erledigt" />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 13, fontWeight: 550, lineHeight: 1.3, textDecoration: t.status === "done" ? "line-through" : "none", overflowWrap: "anywhere" }}>{t.title}</div>
          <div style={{ marginTop: 3, fontSize: 11, color: "var(--text2)" }}>
            {t.ticket && <span className="zp-chip" style={{ background: `color-mix(in srgb, ${sc} 14%, transparent)`, color: sc }} title={sn}>{t.ticket}</span>}
            {due && <span className="zp-chip" style={{ background: late ? "#C4271B22" : soon ? "#b5810022" : "transparent", color: late ? "#C4271B" : soon ? "#8a6200" : "var(--text2)", fontWeight: late || soon ? 700 : 500 }}>fällig {dd(due)}</span>}
            {t.block_start && <span style={{ marginRight: 6 }}><Clock size={10} style={{ verticalAlign: -1 }} /> {t.block_start}</span>}
            <span>{tracked ? `${hm(tracked)} / ` : ""}{hm(t.estimated_minutes)}</span>
            {t.project_name && !t.ticket && <span> · {t.project_name}</span>}
            {t.source === "recurring" && <Repeat size={10} style={{ marginLeft: 5, verticalAlign: -1 }} />}
            {t.moved_count >= 2 && <span style={{ color: "#8a6200" }}> · {t.moved_count}× verschoben</span>}
          </div>
          {t.source === "gaps" && t.notes && <div style={{ fontSize: 11, color: "#8a6200", marginTop: 2 }}>{t.notes}</div>}
          {t.sync_error && <div style={{ fontSize: 10.5, color: "#C4271B", marginTop: 2 }}><AlertTriangle size={10} style={{ verticalAlign: -1 }} /> {t.sync_error}</div>}
        </div>
      </div>
      <div className="acts">
        {t.source === "gaps"
          ? <button className="zp-tact" title="Im Assistenten nachtragen (mit Vorschlägen)" onClick={() => { location.hash = "assistent/nachtragen"; }}>Nachtragen ›</button>
          : <button className="zp-tact" title="Timer starten" onClick={() => act(() => call("POST", `/todo/${t.id}/start`))}><Play size={12} /></button>}
        <button className="zp-tact" title="Bearbeiten" onClick={() => onEdit(t)}><Pencil size={12} /></button>
      </div>
    </div>);
}

function Edit({ t, projects, onClose, onSaved }) {
  const [f, setF] = useState({ title: t.title || "", jira_ticket: t.ticket || "", project_id: t.project_id || "", due_date: t.due_date || "", estimated_minutes: t.estimated_minutes || 60,
    priority: t.priority || 2, block_start: t.block_start || "", notes: t.notes || "", date: t.scheduled_date || "" });
  const [err, setErr] = useState(null);
  const u = p => setF(x => ({ ...x, ...p }));
  const save = async () => {
    const body = { title: f.title, jira_ticket: f.jira_ticket || null, project_id: f.project_id ? Number(f.project_id) : null, due_date: f.due_date || null, estimated_minutes: Number(f.estimated_minutes) || 0,
      priority: Number(f.priority), block_start: f.block_start || null, notes: f.notes || null };
    if (f.date !== (t.scheduled_date || "")) body.when = f.date || "";
    const r = await call("PUT", `/todo/${t.id}`, body); if (r?.error) return setErr(r.error); onSaved();
  };
  const del = async () => { if (!confirm(`„${t.title}“ löschen?`)) return; await call("DELETE", `/todo/${t.id}`); onSaved(); };
  const L = ({ children }) => <label style={{ display: "block", fontSize: 11, color: "var(--text2)", margin: "8px 0 3px" }}>{children}</label>;
  return (
    <div style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.25)", zIndex: 200, display: "flex", alignItems: "flex-start", justifyContent: "center", paddingTop: 70 }} onMouseDown={e => e.target === e.currentTarget && onClose()}>
      <div className="zp-card" style={{ width: "min(480px, calc(100vw - 32px))", padding: "14px 16px", fontSize: 13 }}>
        <div style={{ display: "flex", alignItems: "center" }}><b style={{ flex: 1 }}>To-do bearbeiten</b><button className="zp-nav" onClick={onClose}><X size={14} /></button></div>
        <L>Was ist zu tun?</L><input value={f.title} onChange={e => u({ title: e.target.value })} style={{ width: "100%", padding: "6px 8px" }} />
        <L>Ticket (Plane oder Jira)</L>
        <TicketInput value={f.jira_ticket} jira projectId={f.project_id} onChange={v => { u({ jira_ticket: v }); const p = projectFromTicket(projects, v); if (p) u({ project_id: p.id }); }}
          onPick={it => u({ jira_ticket: it.key, ...(it.local_project_id ? { project_id: it.local_project_id } : {}) })} />
        <L>Projekt</L>
        <select value={f.project_id} onChange={e => u({ project_id: e.target.value })} style={{ width: "100%", padding: "6px 8px" }}>
          <option value="">–</option>{projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <div><L>Geplant am</L><input type="date" value={f.date} onChange={e => u({ date: e.target.value })} style={{ width: "100%", padding: "5px 7px" }} /></div>
          <div><L>Uhrzeit (Zeitblock, optional)</L><input type="time" value={f.block_start} onChange={e => u({ block_start: e.target.value })} style={{ width: "100%", padding: "5px 7px" }} /></div>
          <div><L>Fällig bis</L><input type="date" value={f.due_date} onChange={e => u({ due_date: e.target.value })} style={{ width: "100%", padding: "5px 7px" }} /></div>
          <div><L>Geschätzt</L><select value={f.estimated_minutes} onChange={e => u({ estimated_minutes: e.target.value })} style={{ width: "100%", padding: "6px 7px" }}>
            {[...new Set([...EST, Number(f.estimated_minutes)])].sort((a, b) => a - b).map(m => <option key={m} value={m}>{hm(m)}</option>)}</select></div>
          <div><L>Priorität</L><select value={f.priority} onChange={e => u({ priority: e.target.value })} style={{ width: "100%", padding: "6px 7px" }}>
            {[1, 2, 3].map(p => <option key={p} value={p}>{PRIO[p][0]}</option>)}</select></div>
        </div>
        <L>Notiz</L><textarea value={f.notes} onChange={e => u({ notes: e.target.value })} rows={2} style={{ width: "100%", padding: "6px 8px" }} />
        {err && <div style={{ color: "var(--stop)", fontSize: 12, marginTop: 6 }}>{err}</div>}
        <div style={{ display: "flex", gap: 8, marginTop: 12 }}>
          <button style={btn(true)} onClick={save}><Check size={14} /> Speichern</button>
          <button style={btn(false)} onClick={onClose}>Abbrechen</button>
          <div style={{ flex: 1 }} />
          <button style={{ ...btn(false), color: "var(--stop)" }} onClick={del}><Trash2 size={13} /> Löschen</button>
        </div>
        <div style={{ fontSize: 10.5, color: "var(--text2)", marginTop: 8 }}>Fälligkeit und geplante Zeit gehen – je nach Einstellung – an Plane bzw. Jira/Tempo.</div>
      </div>
    </div>);
}

/* Schnell hinzufügen: Text oder Ticket suchen; „+ Neu“ legt das Ticket im richtigen System an */
function QuickAdd({ projects, onAdded, defaultWhen = "today", compact }) {
  const [text, setText] = useState(""); const [picked, setPicked] = useState(null);
  const [when, setWhen] = useState(defaultWhen); const [est, setEst] = useState(60); const [due, setDue] = useState("");
  const [err, setErr] = useState(null);
  const add = async () => {
    const v = text.trim(); if (!v) return;
    const isKey = /^[A-Za-z][A-Za-z0-9_]*-\d+$/.test(v);
    const key = picked?.key || (isKey ? v.toUpperCase() : null);
    const p = key ? projectFromTicket(projects, key) : null;
    const r = await call("POST", "/todo", { title: picked?.name || (isKey ? v.toUpperCase() : v), jira_ticket: key, ticket_title: picked?.name || null, project_id: picked?.local_project_id || p?.id || null,
      task_id: picked?.task_id || null, when, estimated_minutes: est, due_date: due || picked?.due || null, due_from_ticket: !due && !!picked?.due });
    if (r?.error) return setErr(r.error);
    setText(""); setPicked(null); setDue(""); setErr(null); onAdded();
  };
  return (
    <div className="zp-tq">
      <div style={{ display: "grid", gridTemplateColumns: compact ? "1fr auto" : "minmax(220px,3fr) auto auto auto auto", gap: 6, alignItems: "center" }}>
        <TicketInput value={text} jira onChange={v => { setText(v); setPicked(null); }} onPick={it => { setPicked(it); setText(it.key); }} onEnter={add}
          hint="Was ist zu tun? – oder Ticket suchen (z. B. PRJA-581)" />
        {!compact && <select value={when} onChange={e => setWhen(e.target.value)} style={{ padding: "6px 7px" }}>{WHEN.map(([k, l]) => <option key={k} value={k}>{l}</option>)}</select>}
        {!compact && <select value={est} onChange={e => setEst(Number(e.target.value))} style={{ padding: "6px 7px" }} title="Geschätzt">{EST.map(m => <option key={m} value={m}>{hm(m)}</option>)}</select>}
        {!compact && <input type="date" value={due} onChange={e => setDue(e.target.value)} title="Fällig bis (optional)" style={{ padding: "5px 6px" }} />}
        <button style={btn(true)} onClick={add}><Plus size={14} />{compact ? "" : " Hinzufügen"}</button>
      </div>
      {picked && <div style={{ fontSize: 11, color: "var(--text2)", marginTop: 3 }}>{picked.key}: {picked.name}{picked.due ? ` · fällig ${dd(picked.due)}` : ""}</div>}
      {err && <div style={{ fontSize: 12, color: "var(--stop)", marginTop: 3 }}>{err}</div>}
    </div>);
}

/* Vorschlag übernehmen – per Knopf oder durch Ziehen in eine Spalte */
async function takeSuggestion(s, when) {
  if (s.kind === "stale") return call("PUT", `/todo/${s.todo_id}`, { when });
  if (s.kind === "recurring") return call("POST", "/todo/templates", { title: s.title, rule: s.rule });
  return call("POST", "/todo", { title: s.title, jira_ticket: s.ticket || null, ticket_title: s.ticket_title || null, project_id: s.project_id || null, when, due_date: s.due && s.kind === "ticket" ? s.due : null, due_from_ticket: s.kind === "ticket", priority: s.priority || 2, estimated_minutes: s.estimated_minutes || 60, source: s.kind, event_uid: s.event_uid || null });
}
function Suggestions({ onChanged, tick, compact }) {
  const [list, setList] = useState(null);
  const load = useCallback(fresh => api("GET", `/todo/suggest${fresh ? "?fresh=1" : ""}`).then(r => setList(Array.isArray(r) ? r : [])), []);
  useEffect(() => { load(); }, [load, tick]);
  if (!list) return <div style={{ fontSize: 12, color: "var(--text2)" }}>Suche Vorschläge …</div>;
  const take = async (s, when) => { await takeSuggestion(s, when); setList(l => l.filter(x => x.key !== s.key)); onChanged(); };
  const dismiss = async s => { await call("POST", "/todo/suggest/dismiss", { key: s.key, days: s.kind === "ticket" ? 7 : null }); setList(l => l.filter(x => x.key !== s.key)); };
  const shown = compact ? list.slice(0, 4) : list;
  if (!shown.length) return <div style={{ fontSize: 12, color: "var(--text2)" }}>Keine Vorschläge – alles eingeplant.</div>;
  const small = { ...btn(false), padding: "2px 8px", fontSize: 11.5 };
  return (
    <div>
      {shown.map(s => (
        <div key={s.key} draggable={s.kind !== "recurring"} title={s.kind !== "recurring" ? "In eine Spalte ziehen – oder Knopf unten" : undefined}
          onDragStart={e => { e.dataTransfer.setData("text/suggest", JSON.stringify(s)); e.dataTransfer.effectAllowed = "copy"; }}
          style={{ borderTop: "1px solid var(--line)", padding: "7px 0", fontSize: 12.5, cursor: s.kind !== "recurring" ? "grab" : "default" }}>
          <div style={{ display: "flex", gap: 6, alignItems: "flex-start" }}>
            <div style={{ flex: 1, minWidth: 0 }}>
              {s.ticket && <span className="zp-chip" style={{ background: `color-mix(in srgb, ${SYS[s.system]?.[1] || "#888"} 14%, transparent)`, color: SYS[s.system]?.[1] || "var(--text2)" }}>{s.ticket}</span>}
              <span style={{ fontWeight: 550 }}>{s.title}</span>
              <div style={{ fontSize: 11, color: s.score >= 80 ? "#C4271B" : "var(--text2)" }}>{s.why}</div>
            </div>
            <button className="zp-tact" title="Nicht vorschlagen" onClick={() => dismiss(s)}><X size={11} /></button>
          </div>
          <div style={{ display: "flex", gap: 5, marginTop: 4, flexWrap: "wrap" }}>
            {s.kind === "recurring" ? <button style={small} onClick={() => take(s)}><Repeat size={11} /> Als wiederkehrend anlegen</button>
              : <>{[["today", "Heute"], ["tomorrow", "Morgen"], ["week", "Woche"]].map(([k, l]) => <button key={k} style={{ ...small, ...(s.when === k ? { borderColor: "var(--accent)", color: "var(--accent)", fontWeight: 700 } : {}) }} onClick={() => take(s, k)}>+ {l}</button>)}
                {s.kind === "stale" && <button style={small} onClick={async () => { await call("PUT", `/todo/${s.todo_id}`, { done: true }); setList(l => l.filter(x => x.key !== s.key)); onChanged(); }}>Erledigt</button>}</>}
          </div>
        </div>))}
      {!compact && <button style={{ ...small, marginTop: 6 }} onClick={() => load(true)}>Neu suchen</button>}
    </div>);
}

function SyncSettings({ s, onChanged }) {
  const [tpl, setTpl] = useState([]);
  useEffect(() => { api("GET", "/todo/templates").then(r => setTpl(Array.isArray(r) ? r.filter(x => x.active) : [])); }, []);
  const set = async (k, v) => { await call("PUT", "/todo-settings", { [k]: v }); onChanged(); };
  const RULE = r => r === "month-last-workday" ? "letzter Arbeitstag im Monat" : r === "month-first-workday" ? "erster Arbeitstag im Monat" : /^month-(\d+)/.test(r) ? `am ${r.slice(6)}. im Monat` : /^week-(\d)/.test(r) ? `jeden ${["", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag"][+r.slice(5)]}` : r === "day" ? "jeden Arbeitstag" : r;
  const [nt, setNt] = useState(""); const [nr, setNr] = useState("week-1");
  return (
    <div className="zp-card" style={{ padding: "10px 14px", fontSize: 12.5 }}>
      <b>Abgleich mit Plane und Jira/Tempo</b>
      {[["sync_due", "todo_sync_due", "Fälligkeit ins Ticket schreiben (Plane: Zieldatum, Jira: Fälligkeitsdatum)"],
        ["sync_tempo_plan", "todo_sync_tempo_plan", "Geplante Zeit als Tempo-Plan eintragen (Jira-Tickets: Tag + Schätzung)"],
        ["sync_plane_start", "todo_sync_plane_start", "Geplanten Tag als Startdatum in Plane eintragen"]].map(([k, sk, l]) => (
        <label key={k} style={{ display: "flex", gap: 8, marginTop: 6 }}><input type="checkbox" checked={!!s[k]} onChange={e => set(sk, e.target.checked)} /> {l}</label>))}
      <div style={{ fontSize: 11, color: "var(--text2)", marginTop: 4 }}>Gebucht wird dadurch nichts. Fehler stehen am To-do und blockieren nie.</div>
      <div style={{ marginTop: 10 }}><b>Wiederkehrend</b></div>
      {tpl.map(x => <div key={x.id} style={{ display: "flex", gap: 6, alignItems: "center", marginTop: 4 }}><Repeat size={11} /> <span style={{ flex: 1 }}>{x.title} <span style={{ color: "var(--text2)" }}>· {RULE(x.rule)}</span></span>
        <button className="zp-tact" onClick={async () => { await call("DELETE", `/todo/templates/${x.id}`); setTpl(t => t.filter(y => y.id !== x.id)); }}><X size={11} /></button></div>)}
      <div style={{ display: "flex", gap: 6, marginTop: 6 }}>
        <input value={nt} onChange={e => setNt(e.target.value)} placeholder="z. B. Patchday prüfen" style={{ flex: 1, padding: "4px 6px" }} />
        <select value={nr} onChange={e => setNr(e.target.value)} style={{ padding: "4px 6px" }}>
          {["day", "week-1", "week-2", "week-3", "week-4", "week-5", "month-first-workday", "month-15", "month-last-workday"].map(r => <option key={r} value={r}>{RULE(r)}</option>)}</select>
        <button style={{ ...btn(false), padding: "3px 10px" }} disabled={!nt.trim()} onClick={async () => { await call("POST", "/todo/templates", { title: nt.trim(), rule: nr }); setNt(""); const r = await api("GET", "/todo/templates"); setTpl(r.filter(x => x.active)); onChanged(); }}>+</button>
      </div>
    </div>);
}

export function TodoView() {
  const [b, setB] = useState(null); const [projects, setProjects] = useState([]); const [edit, setEdit] = useState(null);
  const [msg, setMsg] = useState(null); const [drop, setDrop] = useState(null); const [gear, setGear] = useState(false); const [tick, setTick] = useState(0); const [showDone, setShowDone] = useState(false);
  const load = useCallback(() => api("GET", "/todo").then(r => r && !r.error && setB(r)), []);
  useEffect(() => { load(); api("GET", "/projects").then(p => setProjects((p || []).filter(x => x.status !== "archived"))); }, [load]);
  useEffect(() => { const t = setInterval(load, 60000); return () => clearInterval(t); }, [load]);
  const changed = () => { load(); setTick(t => t + 1); };
  if (!b) return <div style={{ padding: 20, color: "var(--text2)" }}>Lade To-dos …</div>;
  const dropOn = async (col, e) => {
    e.preventDefault(); setDrop(null);
    const when = { today: "today", tomorrow: "tomorrow", week: "week", later: "nextweek", inbox: "", overdue: null }[col]; if (when === null) return;
    const sg = e.dataTransfer.getData("text/suggest");
    if (sg) { const r = await takeSuggestion(JSON.parse(sg), when); if (r?.error) setMsg({ err: 1, text: r.error }); changed(); return; }
    const id = e.dataTransfer.getData("text/todo"); if (!id) return;
    const r = await call("PUT", `/todo/${id}`, { when }); if (r?.error) setMsg({ err: 1, text: r.error }); changed();
  };
  const cols = COLS.filter(([k]) => k !== "overdue" || b.columns.overdue.length);
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
      <style>{CSS}</style>
      <div className="zp-card" style={{ padding: "12px 14px" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 8 }}>
          <b style={{ fontSize: 15, flex: 1 }}>To-dos</b>
          <span style={{ fontSize: 11.5, color: "var(--text2)" }}>Karten ziehen zum Umplanen · ▶ startet den Timer auf dem Ticket</span>
          <button className="zp-nav" title="Abgleich & Wiederkehrendes" onClick={() => setGear(g => !g)}><Settings2 size={15} /></button>
        </div>
        <QuickAdd projects={projects} onAdded={changed} />
      </div>
      {gear && <SyncSettings s={b.settings} onChanged={changed} />}
      {msg && <div style={{ fontSize: 12, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.text}</div>}
      <div style={{ display: "grid", gridTemplateColumns: "minmax(0,1fr) 300px", gap: 12, alignItems: "start" }} className="zp-todo-main">
        <div className="zp-tcols">
          {cols.map(([k, l]) => { const list = b.columns[k]; const est = list.reduce((a, t) => a + (t.estimated_minutes || 0), 0); return (
            <div key={k} className={"zp-tcol" + (drop === k ? " drop" : "")} onDragOver={e => { if (k !== "overdue") { e.preventDefault(); setDrop(k); } }} onDragLeave={() => setDrop(d => d === k ? null : d)} onDrop={e => dropOn(k, e)}>
              <div style={{ display: "flex", alignItems: "baseline", gap: 6, padding: "0 2px" }}>
                <b style={{ fontSize: 12.5, color: k === "overdue" ? "#C4271B" : "var(--text)" }}>{l}</b>
                {k === "today" && <span style={{ fontSize: 11, color: "var(--text2)" }}>{dd(b.today)}</span>}
                {k === "tomorrow" && <span style={{ fontSize: 11, color: "var(--text2)" }}>{dd(b.tomorrow)}</span>}
                <span style={{ flex: 1 }} /><span style={{ fontSize: 11, color: "var(--text2)" }}>{list.length ? `${list.length} · ${hm(est)}` : ""}</span>
              </div>
              {k === "today" && <Cap c={b.capacity.today} />}{k === "tomorrow" && <Cap c={b.capacity.tomorrow} />}
              {list.map(t => <Card key={t.id} t={t} today={b.today} onChanged={changed} onEdit={setEdit} setMsg={setMsg} />)}
              {!list.length && <div style={{ fontSize: 11, color: "var(--text3)", padding: "10px 4px" }}>{k === "inbox" ? "Ideen und Aufgaben ohne Termin" : "hierher ziehen"}</div>}
            </div>); })}
        </div>
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <div className="zp-card" style={{ padding: "10px 12px" }}>
            <div style={{ display: "flex", alignItems: "center", gap: 6, marginBottom: 2 }}><Sparkles size={14} style={{ color: "var(--accent)" }} /><b style={{ fontSize: 13 }}>Vorschläge</b></div>
            <div style={{ fontSize: 11, color: "var(--text2)", marginBottom: 4 }}>Deine Tickets (fällig/dringend zuerst), Termine ohne Vorbereitung, Liegengebliebenes, Wiederkehrendes. Einfach in eine Spalte ziehen.</div>
            <Suggestions onChanged={changed} tick={tick} />
          </div>
          <div className="zp-card" style={{ padding: "10px 12px" }}>
            <button onClick={() => setShowDone(s => !s)} style={{ background: "none", border: "none", padding: 0, cursor: "pointer", fontSize: 12.5, fontWeight: 600, color: "var(--text)" }}>
              <ChevronRight size={13} style={{ verticalAlign: -2, transform: showDone ? "rotate(90deg)" : "none" }} /> Erledigt (7 Tage): {b.done.length}</button>
            {showDone && b.done.map(t => <div key={t.id} style={{ fontSize: 12, padding: "4px 0", borderTop: "1px solid var(--line)", display: "flex", gap: 6 }}>
              <input type="checkbox" checked onChange={async () => { await call("PUT", `/todo/${t.id}`, { done: false }); changed(); }} />
              <span style={{ flex: 1, textDecoration: "line-through", color: "var(--text2)" }}>{t.ticket ? t.ticket + " " : ""}{t.title}</span>
              <span style={{ color: "var(--text3)" }}>{hm(Math.round((t.tracked_seconds || 0) / 60))}</span></div>)}
          </div>
        </div>
      </div>
      <style>{`@media (max-width: 1100px){.zp-todo-main{grid-template-columns:1fr !important}}`}</style>
      {edit && <Edit t={edit} projects={projects} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); changed(); }} />}
    </div>);
}

/* Kompakt für „Heute“: heutige To-dos + Schnell-Eingabe */
export function TodayTodos({ onTimer }) {
  const [b, setB] = useState(null); const [projects, setProjects] = useState([]); const [edit, setEdit] = useState(null); const [msg, setMsg] = useState(null);
  const load = useCallback(() => api("GET", "/todo").then(r => r && !r.error && setB(r)), []);
  useEffect(() => { load(); api("GET", "/projects").then(p => setProjects((p || []).filter(x => x.status !== "archived"))); const t = setInterval(load, 60000); return () => clearInterval(t); }, [load]);
  if (!b) return null;
  const list = [...b.columns.overdue, ...b.columns.today];
  const changed = () => { load(); onTimer && onTimer(); };
  return (
    <div className="zp-card" style={{ padding: "10px 12px" }}>
      <style>{CSS}</style>
      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
        <CalendarDays size={14} style={{ color: "var(--accent)" }} /><b style={{ fontSize: 13, flex: 1 }}>Heute zu tun</b>
        <a href="#planung" style={{ fontSize: 11.5, color: "var(--accent)" }}>Planen ›</a>
      </div>
      <Cap c={b.capacity.today} />
      {list.map(t => <Card key={t.id} t={t} today={b.today} onChanged={changed} onEdit={setEdit} setMsg={setMsg} />)}
      {!list.length && <div style={{ fontSize: 12, color: "var(--text2)", padding: "6px 0" }}>Nichts geplant.</div>}
      <div style={{ marginTop: 8 }}><QuickAdd projects={projects} onAdded={load} compact /></div>
      {msg && <div style={{ fontSize: 11.5, color: "var(--stop)", marginTop: 4 }}>{msg.text}</div>}
      {edit && <Edit t={edit} projects={projects} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); load(); }} />}
    </div>);
}
