/* ═══════════════════════════════════════════════════════════════════
   Einrichtungs-Assistent (v6.8.0)
   Führt Schritt für Schritt durch die Einrichtung – mit Schwerpunkt auf
   Projekten und dem Übergang Jira → Plane. Danach bleibt er als
   „Was ist offen?“ erreichbar. Abschaltbar (Einstellungen › System).
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback } from "react";
import { Check, ChevronLeft, ChevronRight, Compass, X, ExternalLink, RefreshCw } from "lucide-react";
import { api, call, btn, lab, inpS, isServerMode, PlaneWorkspacesPanel } from "./zp-ui.jsx";
import { JiraInstancesPanel, ClientHint } from "./zp-servermode.jsx";
import { TlsTrust } from "./zp-jira.jsx";
import { ProjectSourcesPanel, DuplicatesPanel, PendingPanel } from "./zp-projects.jsx";
import { OverlapCard, ReviewCard } from "./zp-day.jsx";
import { GapFiller } from "./zp-gaps.jsx";
import { TempoSyncCard } from "./zp-tempo-sync.jsx";
import { CleanupPanel } from "./zp-cleanup.jsx";
import { SelfCheckCard } from "./zp-server.jsx";
import { TempoAutoSetup } from "./zp-tempo.jsx";

const MODES = [
  ["jira", "Nur Jira + Tempo", "Zeitprotokoll als Tempo-Begleiter oder -Ersatz. Tickets aus Jira, Zeiten gehen nach Tempo. Kein Plane."],
  ["bridge", "Übergang: Plane + Jira", "Für jetzt. Projekte, die schon in Plane sind, buchen nach Plane und Tempo. Alle anderen buchen nur nach Jira/Tempo. Projekte ziehen nach und nach um."],
  ["plane", "Nur Plane", "Ziel. Alles läuft über Plane, Jira und Tempo sind aus. Erst wählen, wenn alle Projekte umgezogen sind."],
];

/* Schritte je Betriebsart */
const ALL_STEPS = [
  { id: "mode", title: "Arbeitsweise", modes: null },
  { id: "jira", title: "Jira & Tempo verbinden", modes: ["jira", "bridge"] },
  { id: "plane", title: "Plane verbinden", modes: ["bridge", "plane"] },
  { id: "projects", title: "Projekte übernehmen", modes: null },
  { id: "migration", title: "Plane oder Jira – wohin geht die Zeit?", modes: ["bridge"] },
  { id: "tempo", title: "Tempo-Übertragung", modes: ["jira", "bridge"] },
  { id: "meetings", title: "Kalender & Meetings", modes: null },
  { id: "ssh", title: "SSH-Sitzungen", modes: null },
  { id: "worktime", title: "Arbeitszeit", modes: null },
  { id: "finish", title: "Prüfen & fertig", modes: null },
];

const Dot = ({ st }) => {
  const c = st === "ok" ? "var(--accent)" : st === "warn" ? "#b58100" : st === "todo" ? "var(--stop)" : "var(--text2)";
  return <span style={{ width: 9, height: 9, borderRadius: 9, background: st === "skip" ? "transparent" : c, border: `2px solid ${c}`, flex: "none" }} />;
};
const Para = ({ children }) => <p style={{ margin: "0 0 10px", color: "var(--text2)", fontSize: 13, lineHeight: 1.5 }}>{children}</p>;
const Msg = ({ m }) => m ? <div style={{ fontSize: 12, marginTop: 8, color: m.err ? "var(--stop)" : "var(--accent)" }}>{m.err ? "✗ " : "✓ "}{m.text}</div> : null;
const Toggle = ({ on, onChange, label, sub }) => (
  <label style={{ display: "flex", gap: 10, alignItems: "flex-start", padding: "8px 0", borderTop: "1px solid var(--line)", cursor: "pointer" }}>
    <input type="checkbox" checked={!!on} onChange={e => onChange(e.target.checked)} style={{ marginTop: 3 }} />
    <span><b style={{ fontWeight: 600 }}>{label}</b>{sub && <span style={{ display: "block", color: "var(--text2)", fontSize: 12 }}>{sub}</span>}</span>
  </label>);
const go = hash => { location.hash = hash; };

/* Status je Schritt aus den Fakten */
function stepState(id, f) {
  if (!f) return "";
  const m = f.mode;
  switch (id) {
    case "mode": return f.mode_set ? "ok" : m ? "warn" : "todo";
    case "jira": return f.jira.configured ? "ok" : "todo";
    case "plane": return f.plane.configured ? "ok" : "todo";
    case "projects": return !f.sources.last_scan ? "todo" : f.sources.pending || f.projects.gone_open.length ? "warn" : "ok";
    case "migration": return f.projects.only_jira.length ? "warn" : "ok";
    case "tempo": return f.tempo.push ? "ok" : "todo";
    case "meetings": return f.meetings.detect || f.meetings.calendar ? "ok" : "skip";
    case "ssh": return f.ssh.enabled ? "ok" : "skip";
    case "worktime": return f.worktime.targets ? "ok" : "todo";
    case "finish": return f.done.finish ? "ok" : "";
    default: return "";
  }
}

/* Kleiner Hinweis auf der Timer-Seite, solange etwas offen ist */
export function AssistantHint() {
  const [f, setF] = useState(null);
  const [tasks, setTasks] = useState([]);
  useEffect(() => { api("GET", "/assistant").then(r => r && !r.error && setF(r)); api("GET", "/assistant/tasks").then(r => Array.isArray(r) && setTasks(r)); }, []);
  if (!f || !f.enabled || !tasks.length) return null;
  const todo = tasks.filter(o => o.level !== "info").length;
  return (
    <button onClick={() => go("assistent")} className="zp-panel" style={{ display: "flex", gap: 10, alignItems: "center", textAlign: "left", padding: "10px 14px", border: "1px solid var(--line)", cursor: "pointer", color: "var(--text)" }}>
      <Compass size={18} style={{ color: "var(--accent)", flex: "none" }} />
      <span style={{ flex: 1, fontSize: 13 }}><b>Assistent:</b> {todo ? `${todo} Aufgabe${todo === 1 ? "" : "n"}` : "Hinweise"} – {tasks[0].title}{tasks.length > 1 ? ` (+${tasks.length - 1})` : ""}</span>
      <ChevronRight size={16} style={{ color: "var(--text2)" }} />
    </button>);
}

/* Erster Start: noch nichts erfasst, Assistent an und nie gestartet → direkt hinführen */
export function useAssistantAutoOpen(setView) {
  useEffect(() => {
    if (location.hash.slice(1)) return;
    api("GET", "/assistant").then(f => { if (f && !f.error && f.enabled && !f.started && f.entries === 0) { location.hash = "assistent"; setView("assistent"); } });
  }, []);   // eslint-disable-line
}

/* ══ Aufgaben: der Assistent im Alltag – zeigt nur, was gerade etwas zu tun hat ══ */
const TASK_UI = {
  absence_hints: { body: p => <AbsenceHints {...p} /> },
  absence_tempo: { body: () => <div style={{ fontSize: 12.5, color: "var(--text2)" }}>Diese Abwesenheit steht nur hier. Für Tempo bitte wie gewohnt das Urlaubs-/Krankheits-Ticket im Abwesenheitsprojekt anlegen bzw. buchen – danach verschwindet der Hinweis beim nächsten Abgleich. Das Arbeitszeitkonto hier stimmt schon.</div> },
  todo_overdue: { body: () => <button style={btn(true)} onClick={() => go("planung")}><ExternalLink size={13} /> Zu den To-dos</button> },
  todo_plan_tomorrow: { body: () => <button style={btn(true)} onClick={() => go("planung")}><ExternalLink size={13} /> Jetzt planen</button> },
  todo_plan_week: { body: () => <button style={btn(true)} onClick={() => go("planung")}><ExternalLink size={13} /> Woche planen</button> },
  tempo_sync: { body: p => <TempoSyncCard {...p} /> },
  monthend: { body: p => <GapFiller {...p} /> },
  duplicates: { body: p => <DuplicatesPanel {...p} /> },
  pending: { body: p => <PendingPanel {...p} /> },
  cleanup: { body: p => <CleanupPanel {...p} /> },
  review: { body: p => <ReviewCard {...p} emptyText="Nichts mehr offen – alle markierten Buchungen sind bestätigt." /> },
  overlaps: { body: p => <OverlapCard {...p} days={31} emptyText="Keine Überschneidungen mehr offen." /> },
  tempo_errors: { body: () => <button style={btn(true)} onClick={() => go("uebertragen")}><ExternalLink size={13} /> Zu Übertragen › Nach Tempo</button> },
  tempo_noticket: { body: () => <button style={btn(true)} onClick={() => go("uebertragen")}><ExternalLink size={13} /> Ticket zuordnen (Übertragen › Nach Tempo)</button> },
  selfcheck: { body: () => <SelfCheckCard /> },
};
function TasksView({ onSetup }) {
  const [list, setList] = useState(null); const [open, setOpen] = useState({}); const [tick, setTick] = useState(0);
  const load = useCallback(fresh => api("GET", `/assistant/tasks${fresh ? "?fresh=1" : ""}`).then(r => setList(Array.isArray(r) ? r : [])), []);
  useEffect(() => { load(true); }, [load]);
  if (!list) return <div style={{ color: "var(--text2)", fontSize: 13 }}>Prüfe …</div>;
  const changed = () => { setTick(t => t + 1); load(true); };
  if (!list.length) return <div className="zp-panel" style={{ padding: 18, fontSize: 14 }}><Check size={16} style={{ color: "var(--accent)", verticalAlign: -3 }} /> Alles erledigt – nichts zu tun.</div>;
  return (<div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
    {list.map((t, i) => { const ui = TASK_UI[t.id]; const isOpen = open[t.id] ?? i === 0; return (
      <div key={t.id} className="zp-panel" style={{ padding: "10px 14px" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10, cursor: "pointer" }} onClick={() => ui ? setOpen({ ...open, [t.id]: !isOpen }) : onSetup(t.id.slice(6))}>
          <Dot st={t.level === "info" ? "" : t.level} />
          <div style={{ flex: 1 }}><b style={{ fontSize: 14 }}>{t.title}</b>{t.count > 1 && <span style={{ marginLeft: 6, fontSize: 12, color: "var(--text2)" }}>({t.count})</span>}
            {t.text && <div style={{ fontSize: 12, color: "var(--text2)" }}>{t.text}</div>}</div>
          {ui ? <ChevronRight size={16} style={{ transform: isOpen ? "rotate(90deg)" : "none", transition: "transform .15s", color: "var(--text2)" }} />
            : <span style={{ fontSize: 12, color: "var(--accent)", fontWeight: 600 }}>in der Einrichtung öffnen ›</span>}
        </div>
        {ui && isOpen && <div style={{ marginTop: 10 }}>{ui.body({ onChanged: changed, tick })}</div>}
      </div>); })}
    <div style={{ fontSize: 11, color: "var(--text2)" }}>Geprüft wird nur die eigene Datenbank, ohne Netz – neu, wenn sich Buchungen oder Projekte ändern. Plane/Jira liest das System einmal im Monat oder auf Knopfdruck ein.</div>
  </div>);
}

export function AssistantView() {
  const [f, setF] = useState(null);
  const [cur, setCur] = useState(null);
  const [tab, setTab] = useState(null);
  const load = useCallback(() => api("GET", "/assistant").then(r => { if (r && !r.error) setF(r); return r; }), []);
  useEffect(() => { load().then(r => { if (r && !r.started) call("PUT", "/assistant", { started: true }); }); }, [load]);
  const sub = (typeof location !== "undefined" ? location.hash.slice(1).split("/") : []);
  if (sub[1] === "nachtragen") return (
    <div style={{ maxWidth: 1100, margin: "0 auto" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 14 }}>
        <Compass size={22} style={{ color: "var(--accent)" }} />
        <div style={{ flex: 1 }}><h1 className="zp-display" style={{ margin: 0, fontSize: 22 }}>Nachtragen</h1>
          <div style={{ color: "var(--text2)", fontSize: 12 }}>Der Assistent zeigt je Tag, was am Mac lief und was noch fehlt – und schlägt passende Tickets vor.</div></div>
        <a href="#assistent" style={{ fontSize: 12, color: "var(--accent)", fontWeight: 600 }}>Alle Aufgaben ›</a>
      </div>
      <GapFiller initialDate={/^\d{4}-\d{2}-\d{2}$/.test(sub[2] || "") ? sub[2] : null} />
    </div>);
  if (!f) return <div style={{ padding: 20, color: "var(--text2)" }}>Lade Assistent…</div>;
  const view = tab || (f.done?.finish || (f.mode_set && f.started) ? "aufgaben" : "einrichtung");
  const tabs = (
    <div style={{ display: "flex", gap: 6, marginBottom: 14 }}>
      {[["aufgaben", "Aufgaben"], ["einrichtung", "Einrichtung"]].map(([k, l]) => (
        <button key={k} onClick={() => setTab(k)} style={{ border: "1px solid " + (view === k ? "var(--accent)" : "var(--line)"), background: view === k ? "var(--accent-soft)" : "var(--card)", color: view === k ? "var(--accent)" : "var(--text2)", borderRadius: 20, padding: "5px 14px", fontSize: 12, fontWeight: view === k ? 600 : 500, cursor: "pointer" }}>{l}</button>))}
    </div>);
  if (view === "aufgaben") return (
    <div style={{ maxWidth: 1100, margin: "0 auto" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 10 }}>
        <Compass size={22} style={{ color: "var(--accent)" }} />
        <div style={{ flex: 1 }}><h1 className="zp-display" style={{ margin: 0, fontSize: 22 }}>Assistent</h1>
          <div style={{ color: "var(--text2)", fontSize: 12 }}>Was gerade zu tun ist – direkt hier erledigen.</div></div>
      </div>
      {tabs}
      <TasksView onSetup={stepId => { setTab("einrichtung"); setCur(stepId); }} />
    </div>);
  const steps = ALL_STEPS.filter(s => !s.modes || !f.mode || s.modes.includes(f.mode));
  const idx = Math.max(0, steps.findIndex(s => s.id === (cur || steps.find(s => !f.done[s.id] && stepState(s.id, f) !== "ok")?.id || "mode")));
  const step = steps[idx];
  const next = async (mark = true) => {
    if (mark) await call("PUT", "/assistant", { done: { [step.id]: true } });
    await load(); setCur(steps[Math.min(steps.length - 1, idx + 1)].id);
  };
  const put = async b => { const r = await call("PUT", "/assistant", b); if (!r.error) setF(r); return r; };
  const props = { f, reload: load, put, next };

  return (
    <div style={{ maxWidth: 1100, margin: "0 auto" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 14 }}>
        <Compass size={22} style={{ color: "var(--accent)" }} />
        <div style={{ flex: 1 }}>
          <h1 className="zp-display" style={{ margin: 0, fontSize: 22 }}>Assistent</h1>
          <div style={{ color: "var(--text2)", fontSize: 12 }}>Schritt für Schritt. Du kannst jederzeit springen, überspringen oder später weitermachen.</div>
        </div>
        <button style={btn(false)} title="Assistent ausschalten (wieder einschalten: Einstellungen › System)"
          onClick={async () => { if (!confirm("Assistent ausschalten?\n\nMenüpunkt und Hinweise verschwinden. Wieder einschalten unter Einstellungen › System.")) return; await put({ enabled: false }); go("timer"); location.reload(); }}>
          <X size={13} /> Ausschalten</button>
      </div>

      {tabs}
      {f.open?.length > 0 && (
        <div className="zp-panel" style={{ padding: "10px 14px", marginBottom: 14 }}>
          <div style={{ fontSize: 11, fontWeight: 700, color: "var(--text2)", textTransform: "uppercase", letterSpacing: ".04em", marginBottom: 4 }}>Offen</div>
          {f.open.map((o, i) => (
            <div key={i} style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 13, padding: "2px 0" }}>
              <Dot st={o.level === "info" ? "" : o.level} /><span style={{ flex: 1 }}>{o.text}</span>
              {steps.some(s => s.id === o.step) && <button onClick={() => setCur(o.step)} style={{ fontSize: 12, border: "none", background: "none", color: "var(--accent-text, var(--accent))", cursor: "pointer", fontWeight: 600 }}>ansehen</button>}
            </div>))}
        </div>)}

      <div style={{ display: "grid", gridTemplateColumns: "minmax(190px, 230px) 1fr", gap: 16, alignItems: "start" }} className="zp-assist-grid">
        <nav className="zp-panel" style={{ padding: 6 }}>
          {steps.map((s, i) => (
            <button key={s.id} onClick={() => setCur(s.id)} style={{ display: "flex", gap: 9, alignItems: "center", width: "100%", textAlign: "left", padding: "8px 10px", borderRadius: 7, border: "none",
              background: i === idx ? "var(--accent-soft)" : "transparent", color: "var(--text)", fontWeight: i === idx ? 600 : 400, cursor: "pointer" }}>
              <span style={{ width: 18, color: "var(--text2)", fontSize: 12 }}>{i + 1}</span>
              <span style={{ flex: 1, fontSize: 13 }}>{s.title}</span>
              {f.done[s.id] || stepState(s.id, f) === "ok" ? <Check size={14} style={{ color: "var(--accent)" }} /> : <Dot st={stepState(s.id, f)} />}
            </button>))}
        </nav>

        <section className="zp-panel" style={{ padding: "16px 18px" }}>
          <div style={{ fontSize: 11, color: "var(--text2)" }}>Schritt {idx + 1} von {steps.length}</div>
          <h2 style={{ margin: "2px 0 12px", fontSize: 18 }}>{step.title}</h2>
          {step.id === "mode" && <StepMode {...props} />}
          {step.id === "jira" && <StepJira {...props} />}
          {step.id === "plane" && <StepPlane {...props} />}
          {step.id === "projects" && <StepProjects {...props} />}
          {step.id === "migration" && <StepMigration {...props} />}
          {step.id === "tempo" && <StepTempo {...props} />}
          {step.id === "meetings" && <StepMeetings {...props} />}
          {step.id === "ssh" && <StepSsh {...props} />}
          {step.id === "worktime" && <StepWorktime {...props} />}
          {step.id === "finish" && <StepFinish {...props} />}
          <div style={{ display: "flex", gap: 8, marginTop: 18, paddingTop: 12, borderTop: "1px solid var(--line)" }}>
            <button style={btn(false)} disabled={idx === 0} onClick={() => setCur(steps[idx - 1].id)}><ChevronLeft size={14} /> Zurück</button>
            <span style={{ flex: 1 }} />
            {step.id !== "finish" && <button style={btn(false)} onClick={() => next(false)}>Überspringen</button>}
            {step.id !== "finish" && <button style={btn(true)} onClick={() => next(true)}>Weiter <ChevronRight size={14} /></button>}
          </div>
        </section>
      </div>
    </div>);
}

/* ── 1 Arbeitsweise ── */
function StepMode({ f, put }) {
  const [msg, setMsg] = useState(null);
  const choose = async k => {
    if (k === "plane" && f.jira.enabled && !confirm("„Nur Plane“ schaltet Jira und die Tempo-Übertragung aus.\nBuchungen bleiben erhalten. Fortfahren?")) return;
    const r = await put({ mode: k });
    setMsg(r.error ? { err: 1, text: r.error } : { text: "Gespeichert – Standard-Regel für Projekte angepasst" });
  };
  return (<>
    <Para>Wie arbeitest du? Das legt fest, welche Schritte folgen und wohin Zeiten standardmäßig gehen. Einzelne Projekte kannst du später abweichend einstellen. Ein Wechsel ist jederzeit möglich.</Para>
    <div style={{ display: "grid", gap: 8 }}>
      {MODES.map(([k, t, d]) => (
        <button key={k} onClick={() => choose(k)} style={{ textAlign: "left", padding: "12px 14px", borderRadius: 9, cursor: "pointer", color: "var(--text)",
          border: `1px solid ${f.mode === k ? "var(--accent)" : "var(--line)"}`, background: f.mode === k ? "var(--accent-soft)" : "var(--card)" }}>
          <div style={{ fontWeight: 600 }}>{f.mode === k ? "● " : "○ "}{t}{k === "bridge" && <span style={{ color: "var(--text2)", fontWeight: 400 }}> · empfohlen für Beispiel jetzt</span>}</div>
          <div style={{ color: "var(--text2)", fontSize: 12, marginTop: 2 }}>{d}</div>
        </button>))}
    </div>
    <Msg m={msg} />
  </>);
}

/* ── 2 Jira & Tempo ── */
function StepJira({ f, reload }) {
  const sm = isServerMode();
  const [v, setV] = useState({ jira_base_url: "", jira_token: "" }); const [st, setSt] = useState(null); const [busy, setBusy] = useState(false);
  useEffect(() => { api("GET", "/settings").then(s => s && setV({ jira_base_url: s.jira_base_url || "", jira_token: s.jira_token || "" })); if (f.jira.configured) test(); }, []);   // eslint-disable-line
  const test = async () => { setBusy(true); setSt(await call("GET", "/integrations/tempo-push/status")); setBusy(false); };
  const save = async () => { await call("PUT", "/settings", sm ? { jira_enabled: "1" } : { jira_enabled: "1", jira_base_url: v.jira_base_url.trim().replace(/\/+$/, ""), jira_token: v.jira_token }); await test(); reload(); };
  if (sm) return (<>
    <Para>Jira-Adressen legt der Administrator fest. Du trägst je Jira-Instanz dein persönliches Token ein (Jira › Profil › Personal Access Tokens). Es wird verschlüsselt gespeichert und nie wieder angezeigt.</Para>
    <JiraInstancesPanel mode="tokens" status={st} />
    <div style={{ display: "flex", gap: 8, marginTop: 10 }}>
      <button style={btn(true)} onClick={save} disabled={busy}>{busy ? "Prüfe…" : "Verbindungen testen"}</button>
      <button style={btn(false)} onClick={() => go("einstellungen/jira")}><ExternalLink size={13} /> Alle Jira-Einstellungen</button>
    </div>
  </>);
  return (<>
    <Para>Eine Anbindung für Jira-Tickets und Tempo-Zeiten. Du brauchst die Jira-Adresse und einen <b>Personal Access Token</b> (Jira › Profil › Personal Access Tokens). Der Token bleibt verschlüsselt übertragen und wird nur maskiert angezeigt.</Para>
    <label style={lab}>Jira-Adresse</label>
    <input style={inpS} placeholder="https://jira.example.com" value={v.jira_base_url} onChange={e => setV({ ...v, jira_base_url: e.target.value })} />
    <label style={lab}>Personal Access Token</label>
    <input style={inpS} type="password" value={v.jira_token} onChange={e => setV({ ...v, jira_token: e.target.value })} />
    <div style={{ display: "flex", gap: 8, marginTop: 10 }}>
      <button style={btn(true)} onClick={save} disabled={busy}>{busy ? "Prüfe…" : "Speichern & testen"}</button>
      <button style={btn(false)} onClick={() => go("einstellungen/jira")}><ExternalLink size={13} /> Alle Jira-Einstellungen</button>
    </div>
    {st && <Msg m={st.user ? { text: `Verbunden als ${st.user}` } : { err: 1, text: st.message || st.error || "Keine Verbindung" }} />}
    <Para><span style={{ display: "block", marginTop: 10 }}>Meldet die Prüfung ein Zertifikatsproblem (interner Server schickt die Kette nicht vollständig), erscheint hier ein Knopf zum Bestätigen.</span></Para>
    <TlsTrust reloadKey={st} onTrusted={test} />
  </>);
}

/* ── 3 Plane ── */
function StepPlane({ f, reload }) {
  const sm = isServerMode();
  const [v, setV] = useState({ plane_base_url: "", plane_workspace: "", plane_api_key: "" }); const [st, setSt] = useState(null); const [busy, setBusy] = useState(false);
  useEffect(() => { api("GET", "/settings").then(s => s && setV({ plane_base_url: s.plane_base_url || "", plane_workspace: s.plane_workspace || "", plane_api_key: s.plane_api_key || "" })); if (f.plane.configured) test(); }, []);   // eslint-disable-line
  const test = async () => {
    setBusy(true);
    if (sm) {
      const list = await api("GET", "/plane/workspaces");
      const arr = Array.isArray(list) ? list : [];
      if (!arr.length) { setSt({ connected: false, message: "Kein Workspace eingerichtet" }); setBusy(false); return; }
      let ok = 0; let msg = "";
      for (const w of arr) {
        const r = await call("GET", `/integrations/plane/status?workspace=${w.id}`);
        if (r.configured && r.connected) { ok++; if (!msg) msg = r.message; }
        else if (!msg) msg = r.error || r.message || `„${w.name}“ nicht verbunden`;
      }
      setSt(ok ? { connected: true, message: arr.length === 1 ? msg : `${ok} von ${arr.length} Workspaces verbunden` } : { connected: false, message: msg || "Keine Verbindung" });
    } else {
      setSt(await call("GET", "/integrations/plane/status"));
    }
    setBusy(false);
  };
  const save = async () => { await call("PUT", "/settings", { ...v, plane_base_url: v.plane_base_url.trim().replace(/\/+$/, "").replace(/\/api(\/v1)?$/, ""), plane_workspace: v.plane_workspace.trim() }); await test(); reload(); };
  if (sm) return (<>
    <Para>Plane ist das Hauptsystem. Der Administrator trägt nur Adresse und Workspace-Kennung ein – <b>ohne</b> Plane-Token. Du trägst deinen persönlichen API-Schlüssel ein (Plane › Profil › API Tokens) und schaltest danach einzeln ein, welche Workspaces du nutzt. Workspaces auf derselben Adresse teilen den Schlüssel.</Para>
    <PlaneWorkspacesPanel />
    <div style={{ display: "flex", gap: 8, marginTop: 10 }}>
      <button style={btn(true)} onClick={async () => { await test(); reload(); }} disabled={busy}>{busy ? "Prüfe…" : "Verbindungen testen"}</button>
      <button style={btn(false)} onClick={() => go("einstellungen/plane")}><ExternalLink size={13} /> Alle Plane-Einstellungen</button>
    </div>
    {st && <Msg m={st.connected ? { text: st.message } : { err: 1, text: st.error || st.message || "Keine Verbindung" }} />}
  </>);
  return (<>
    <Para>Plane ist das Hauptsystem. Adresse, Workspace (steht in der URL, z. B. „example“) und ein API-Token (Plane › Profil › API Tokens).</Para>
    <label style={lab}>Plane-Adresse</label>
    <input style={inpS} placeholder="https://plane.example.com" value={v.plane_base_url} onChange={e => setV({ ...v, plane_base_url: e.target.value })} />
    <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
      <div><label style={lab}>Workspace</label><input style={inpS} placeholder="example" value={v.plane_workspace} onChange={e => setV({ ...v, plane_workspace: e.target.value })} /></div>
      <div><label style={lab}>API-Token</label><input style={inpS} type="password" value={v.plane_api_key} onChange={e => setV({ ...v, plane_api_key: e.target.value })} /></div>
    </div>
    <div style={{ display: "flex", gap: 8, marginTop: 10 }}>
      <button style={btn(true)} onClick={save} disabled={busy}>{busy ? "Prüfe…" : "Speichern & testen"}</button>
      <button style={btn(false)} onClick={() => go("einstellungen/plane")}><ExternalLink size={13} /> Alle Plane-Einstellungen</button>
    </div>
    {st && <Msg m={st.connected ? { text: st.message } : { err: 1, text: st.error || st.message || "Keine Verbindung" }} />}
    <TlsTrust reloadKey={st} onTrusted={test} />
  </>);
}

/* ── 4 Projekte ── */
function StepProjects({ f, reload }) {
  return (<>
    <Para>
      <b>Deine Projekte hier sind die echten Projekte</b> (z. B. „Beispiel Marketing“). Plane- und Jira-Projekte sind nur <b>Quellen</b> für Tickets.
      Zeitprotokoll liest sie einmal im Monat (oder per Knopf) ein und legt dort selbst nichts an.
    </Para>
    <ul style={{ margin: "0 0 12px 18px", padding: 0, fontSize: 13, lineHeight: 1.6 }}>
      <li><b>Übernehmen</b>: als neues Projekt, oder an ein bestehendes hängen. Gibt es den Kunden in Plane <i>und</i> Jira, hänge beide an <i>dasselbe</i> Projekt (Vorschlag „gleiches Kürzel“).</li>
      <li><b>Stumm</b>: alles, was du gerade nicht nutzt. „Alle übrigen stumm“ räumt in einem Schritt auf; zurückholen geht jederzeit.</li>
      <li>Ein Kunde = ein Plane-Projekt. Je System hängt höchstens eine Quelle an einem Projekt.</li>
    </ul>
    <div style={{ fontSize: 12, color: "var(--text2)", marginBottom: 8 }}>
      Stand: {f.sources.linked_plane} Plane- und {f.sources.linked_jira} Jira-Quellen verknüpft · {f.sources.ignored} stumm · {f.sources.pending} offen
    </div>
    <CleanupPanel onChanged={reload} />
    <div style={{ height: 10 }} />
    <ProjectSourcesPanel onChanged={reload} />
    <button style={{ ...btn(false), marginTop: 8 }} onClick={() => go("projects")}><ExternalLink size={13} /> Zur Projekte-Seite (Regeln, Zusammenführen, Abschluss)</button>
  </>);
}

/* ── 5 Übergang Plane ↔ Jira ── */
function StepMigration({ f, put, reload }) {
  const [data, setData] = useState(null);
  const load = () => api("GET", "/sources").then(r => r && !r.error && setData(r));
  useEffect(() => { load(); }, []);
  const rows = (data?.projects || []).filter(p => !p.closed && !p.muted);
  const setRule = async (id, rule) => { await call("PUT", `/projects/${id}/setup`, { output_rule: rule }); load(); reload(); };
  const tag = (on, t) => <span style={{ fontSize: 11, padding: "1px 7px", borderRadius: 10, border: "1px solid var(--line)", color: on ? "var(--accent)" : "var(--text2)", opacity: on ? 1 : .6 }}>{on ? "✓" : "–"} {t}</span>;
  return (<>
    <Para>
      Die wichtigste Regel: <b>Nach Plane geht nur Zeit von Projekten, die in Plane wirklich existieren</b> (verknüpfte, nicht archivierte Plane-Quelle).
      Alles andere geht nach Jira/Tempo. Ziehen Kunden nach Plane um, legt ihr das Projekt in Plane an. Beim nächsten Einlesen erscheint es unter „Neue Projekte“ –
      an <i>dasselbe</i> Zeitprotokoll-Projekt hängen, und ab dann geht die Zeit nach Plane <i>und</i> (für die Abrechnung) nach Tempo.
    </Para>
    <div style={{ display: "flex", gap: 14, fontSize: 13, margin: "6px 0 10px", flexWrap: "wrap" }}>
      <span><b>{f.projects.in_plane}</b> von {f.projects.active} aktiven Projekten in Plane</span>
      <span><b>{f.projects.only_jira.length}</b> nur Jira</span>
      {f.projects.no_source.length > 0 && <span style={{ color: "#b58100" }}><b>{f.projects.no_source.length}</b> ohne Quelle (nur hier)</span>}
    </div>
    <div style={{ height: 8, borderRadius: 8, background: "var(--line)", overflow: "hidden", marginBottom: 12 }}>
      <div style={{ width: `${f.projects.active ? Math.round(100 * f.projects.in_plane / f.projects.active) : 0}%`, height: "100%", background: "var(--accent)" }} />
    </div>
    {!data ? <div style={{ color: "var(--text2)", fontSize: 12 }}>Lade…</div> : rows.map(p => (
      <div key={p.id} style={{ display: "flex", gap: 8, alignItems: "center", padding: "6px 0", borderTop: "1px solid var(--line)", flexWrap: "wrap" }}>
        <span style={{ flex: "1 1 180px", fontWeight: 500 }}>{p.name}</span>
        {tag(p.in_plane, "in Plane")}
        <span style={{ fontSize: 12, color: "var(--text2)" }}>Zeit geht nach:</span>
        {tag(p.effective.plane, "Plane")}{tag(p.effective.tempo, "Tempo")}
        <select value={p.output_rule} onChange={e => setRule(p.id, e.target.value)} style={{ padding: "3px 6px", fontSize: 12 }}>
          {[["default", `Standard (${{ bridge: "Plane + Tempo", plane: "nur Plane", tempo: "nur Tempo", none: "nur hier" }[f.rule_default] || f.rule_default})`], ["bridge", "Plane + Tempo"], ["plane", "nur Plane"], ["tempo", "nur Tempo"], ["none", "nur hier"]].map(([k, l]) => <option key={k} value={k}>{l}</option>)}
        </select>
      </div>))}
    <Para><span style={{ display: "block", marginTop: 10 }}>„Plane + Tempo“ bei einem Projekt, das noch nicht in Plane ist, wirkt wie „nur Tempo“ – das ist Absicht. Sobald es in Plane verknüpft ist, geht die Zeit automatisch auch dorthin.</span></Para>
    <NamesAndLocal rows={rows} onChanged={() => { load(); reload(); }} />
    {f.projects.active > 0 && !f.projects.only_jira.length && f.projects.in_plane === f.projects.active && (
      <div className="zp-panel" style={{ padding: 12, border: "1px solid var(--accent)" }}>
        <b>Alle aktiven Projekte sind in Plane.</b> Wenn die Abrechnung nicht mehr über Tempo läuft, kannst du auf „Nur Plane“ umstellen.
        <div><button style={{ ...btn(true), marginTop: 8 }} onClick={async () => { if (confirm("Auf „Nur Plane“ umstellen? Jira und Tempo-Übertragung werden ausgeschaltet.")) await put({ mode: "plane" }); }}>Auf „Nur Plane“ umstellen</button></div>
      </div>)}
  </>);
}

/* Namen wie in Plane + lokale Zeiten sinnvoll nach Plane (und Tempo) bringen */
const hrs = s => (Math.round((s || 0) / 360) / 10).toLocaleString("de-DE") + " h";
function NamesAndLocal({ rows, onChanged }) {
  const [lt, setLt] = useState(null); const [pick, setPick] = useState({}); const [msg, setMsg] = useState(null);
  const load = () => api("GET", "/sources/local-times").then(r => r && !r.error && setLt(r));
  useEffect(() => { load(); }, []);
  const diff = rows.filter(p => p.name_source);
  const adopt = async ids => { const r = await call("POST", "/projects/adopt-names", { ids }); setMsg(r.error ? { err: 1, text: r.error } : { text: `${r.renamed.length} Projekt(e) umbenannt` }); onChanged(); load(); };
  const assign = async p => {
    const r = await call("POST", "/sources/local-times/assign", { project_id: p.project_id, ticket: pick[p.project_id], from: lt.from });
    setMsg(r.error ? { err: 1, text: r.error } : { text: `${r.n} Buchungen von „${p.name}“ auf ${pick[p.project_id]} gelegt – gehen beim nächsten Plane-Abgleich mit` }); load();
  };
  const stuck = (lt?.projects || []).filter(p => p.no_ticket > 0);
  return (<div style={{ marginTop: 16 }}>
    <h3 style={{ fontSize: 15, margin: "0 0 4px" }}>Projektnamen wie in Plane</h3>
    {!diff.length ? <Para>✓ Alle Projekte heißen wie ihre Quelle in Plane (bzw. Jira).</Para> : (<>
      <Para>Damit es später keine zwei Namen für dasselbe gibt, heißen die Projekte hier genau wie in Plane (solange ein Projekt noch nicht in Plane ist: wie in Jira). Buchungen und Regeln bleiben erhalten.</Para>
      {diff.map(p => (<div key={p.id} style={{ display: "flex", gap: 8, alignItems: "center", padding: "5px 0", borderTop: "1px solid var(--line)", fontSize: 13, flexWrap: "wrap" }}>
        <span style={{ flex: "1 1 160px" }}>{p.name}</span><span style={{ color: "var(--text2)" }}>→</span>
        <b style={{ flex: "1 1 160px" }}>{p.name_source.name}</b><span style={{ fontSize: 11, color: "var(--text2)" }}>{p.name_source.system === "plane" ? "Plane" : "Jira"}</span>
        <button style={btn(false)} onClick={() => adopt([p.id])}>Übernehmen</button></div>))}
      {diff.length > 1 && <button style={{ ...btn(true), marginTop: 6 }} onClick={() => adopt(diff.map(p => p.id))}>Alle {diff.length} angleichen</button>}
    </>)}
    <h3 style={{ fontSize: 15, margin: "16px 0 4px" }}>Lokale Zeiten {lt ? `seit ${new Date(lt.from + "T12:00").toLocaleDateString("de-DE")}` : ""}</h3>
    <Para>Was hier gebucht ist, kommt so an: <b>mit Plane-Ticket</b> → Plane und Tempo (Übergangs-Ticket). <b>Alte Buchungen auf Jira-Tickets</b> bleiben in Tempo, wie sie sind. <b>Projekt noch nicht in Plane</b> → nur Tempo, nach dem Umzug automatisch auch Plane. <b>In Plane, aber ohne Ticket</b> → nur Tempo, für Plane fehlt das Ticket: unten ein Plane-Ticket wählen (z. B. ein allgemeines „Betreuung“-Ticket des Kunden).</Para>
    {lt && (lt.projects || []).map(p => (<div key={p.project_id} style={{ display: "flex", gap: 10, padding: "4px 0", borderTop: "1px solid var(--line)", fontSize: 12, flexWrap: "wrap" }}>
      <span style={{ flex: "1 1 160px", fontWeight: 500, fontSize: 13 }}>{p.name}</span>
      {p.plane > 0 && <span style={{ color: "var(--accent)" }}>Plane {hrs(p.plane)}</span>}
      {p.jira_ticket > 0 && <span style={{ color: "var(--text2)" }}>Jira-Tickets {hrs(p.jira_ticket)}</span>}
      {p.not_plane > 0 && <span style={{ color: "var(--text2)" }}>nur Tempo {hrs(p.not_plane)}</span>}
      {p.no_ticket > 0 && <span style={{ color: "#b58100" }}>ohne Plane-Ticket {hrs(p.no_ticket)} ({p.n_no_ticket})</span>}
    </div>))}
    {stuck.map(p => (<div key={"s" + p.project_id} style={{ display: "flex", gap: 8, alignItems: "center", marginTop: 8, flexWrap: "wrap" }}>
      <span style={{ fontSize: 13, flex: "1 1 180px" }}>{p.name}: {p.n_no_ticket} Buchungen ohne Ticket auf</span>
      <input style={{ ...inpS, width: 140 }} placeholder="Plane-Ticket, z. B. PRJB-12" value={pick[p.project_id] || ""} onChange={e => setPick({ ...pick, [p.project_id]: e.target.value })} />
      <button style={btn(true)} disabled={!pick[p.project_id]} onClick={() => assign(p)}>legen</button>
    </div>))}
    <Msg m={msg} />
  </div>);
}

/* ── 6 Tempo ── */
function StepTempo({ f, reload }) {
  const [v, setV] = useState(null); const [msg, setMsg] = useState(null);
  const load = () => api("GET", "/settings").then(s => s && setV({ tempo_push_enabled: s.tempo_push_enabled === "1" || s.tempo_push_enabled === "true", tempo_push_mode: s.tempo_push_mode || "manual", jira_my_tickets: s.jira_my_tickets !== "0" }));
  useEffect(() => { load(); }, []);
  if (!v) return null;
  const set = async (k, val) => { const nv = { ...v, [k]: val }; setV(nv); const r = await call("PUT", "/settings", { [k]: typeof val === "boolean" ? (val ? "1" : "0") : val }); setMsg(r.error ? { err: 1, text: r.error } : null); reload(); };
  return (<>
    <Para>Das System entscheidet je Buchung selbst, wohin sie in Tempo geht:</Para>
    <ul style={{ margin: "0 0 12px 18px", padding: 0, fontSize: 13, lineHeight: 1.6 }}>
      <li><b>Kunde noch in Jira</b>: direkt auf das Jira-Ticket.</li>
      <li><b>Projekt schon in Plane</b>: auf dein <b>Übergangs-Ticket des Monats</b> („Übergang Zeiten {f.jira.user || "du"} JJJJ-MM“). Es wird beim ersten Buchen angelegt und dir zugewiesen. Nach der Monatssperre wird es geschlossen. Im Worklog stehen Plane-Ticket, Titel, Projekt und Link, der Kunde kommt über den Tempo-Account mit.</li>
      <li><b>Ohne Ticket</b>: Sammelticket des Projekts, legt das System selbst an.</li>
    </ul>
    <Para>Den Rest liest „Automatisch einrichten“ aus Jira und deiner Tempo-Historie: Projekt für neue Tickets, Vorgangstyp, Tempo-Account (auch je Projekt) und ab wann übertragen wird.</Para>
    <TempoAutoSetup compact onApplied={() => { load(); reload(); }} />
    <div style={{ marginTop: 12 }}>
      <Toggle on={v.tempo_push_enabled} onChange={x => set("tempo_push_enabled", x)} label="Tempo-Übertragung eingeschaltet" sub="Buchungen erscheinen unter Jira · Tempo › Ausgang" />
      <Toggle on={v.tempo_push_mode === "auto"} onChange={x => set("tempo_push_mode", x ? "auto" : "manual")} label="Automatisch übertragen" sub="Aus = du gibst im Ausgang frei (empfohlen am Anfang)" />
      <Toggle on={v.jira_my_tickets} onChange={x => set("jira_my_tickets", x)} label="Meine Jira-Tickets zur Auswahl holen" sub="Mir zugewiesen und offen. Stumme Projekte werden übersprungen." />
    </div>
    <Msg m={msg} />
    <button style={{ ...btn(false), marginTop: 8 }} onClick={() => go("einstellungen/tempo")}><ExternalLink size={13} /> Feineinstellungen</button>
  </>);
}

/* ── 7 Kalender & Meetings ── */
function StepMeetings({ reload }) {
  const [v, setV] = useState(null); const [st, setSt] = useState(null);
  const load = () => { api("GET", "/meetings/config").then(r => r && !r.error && setV(r)); api("GET", "/meetings/status").then(r => r && !r.error && setSt(r)); };
  useEffect(load, []);
  if (!v) return isServerMode() ? <ClientHint title="Meeting-Erkennung braucht den Mac-Client" /> : null;
  const set = async (k, x) => { await call("PUT", "/settings", { [k]: x ? "1" : "0" }); load(); reload(); };
  const age = st?.signal_age_s;
  return (<>
    <Para>Erkennt laufende Meetings am <b>Programmstart</b> (Teams, Zoom, Webex, Slack, Jitsi) und an der Browser-Adresse. Dann fragt es „Timer starten?“. <b>Kein Mikrofon, keine Kamera, kein Bildschirminhalt.</b> Termine aus dem macOS-Kalender werden im Kalender angezeigt und im Tracker vorbelegt.</Para>
    <Toggle on={v.detect} onChange={x => set("meeting_detect", x)} label="Meeting-Programme erkennen" />
    <Toggle on={v.browser} onChange={x => set("meeting_browser", x)} label="Meetings im Browser erkennen" sub="z. B. meet.example.net, Teams/Meet im Browser" />
    <Toggle on={v.calendar} onChange={x => set("meeting_calendar", x)} label="macOS-Kalender einlesen" sub="Einmalig im Terminal: bash ~/zeitprotokoll/desktop/kalender-freigeben.sh" />
    <Toggle on={v.ask} onChange={x => set("meeting_ask", x)} label="Nachfragen, ob ein Timer starten soll" />
    {st && <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 8 }}>
      Mac-App meldet sich: {age == null ? "noch nie" : age < 180 ? "✓ gerade eben" : `vor ${Math.round(age / 60)} min`} ·
      Kalender: {st.calendar_sync?.at ? `✓ ${st.calendar_sync.count ?? st.counts?.calendar_events ?? "?"} Termine gelesen` : "noch nicht gelesen (Mac-App muss laufen)"}
    </div>}
    <button style={{ ...btn(false), marginTop: 8 }} onClick={() => go("einstellungen/erkennung")}><ExternalLink size={13} /> Details & Regeln</button>
  </>);
}

/* ── 8 SSH ── */
function StepSsh({ f, reload }) {
  if (isServerMode()) return <ClientHint title="SSH-Sitzungen brauchen den Mac-Client" />;
  const set = async x => { await call("PUT", "/settings", { ssh_watch_enabled: x ? "1" : "0" }); reload(); };
  return (<>
    <Para>Zeichnet SSH-Sitzungen aus dem Terminal auf. Mehrere kurze Sitzungen („nur mal nachgeschaut“) lassen sich unter SSH zu einer Buchung je Tag zusammenführen.</Para>
    <Toggle on={f.ssh.enabled} onChange={set} label="SSH-Sitzungen erfassen" />
    <button style={{ ...btn(false), marginTop: 8 }} onClick={() => go("ssh")}><ExternalLink size={13} /> Zur SSH-Seite</button>
  </>);
}

/* ── 9 Arbeitszeit ── */
function StepWorktime({ reload }) {
  const D = [["mon", "Mo"], ["tue", "Di"], ["wed", "Mi"], ["thu", "Do"], ["fri", "Fr"], ["sat", "Sa"], ["sun", "So"]];
  const [v, setV] = useState(null); const [msg, setMsg] = useState(null);
  useEffect(() => { api("GET", "/settings").then(s => s && setV(Object.fromEntries([...D.map(([k]) => ["target_" + k, s["target_" + k] || "0"]), ["tracking_start_date", s.tracking_start_date || ""], ["vacation_days_per_year", s.vacation_days_per_year || "30"]]))); }, []);   // eslint-disable-line
  if (!v) return null;
  const save = async () => { const r = await call("PUT", "/settings", v); setMsg(r.error ? { err: 1, text: r.error } : { text: "Gespeichert" }); reload(); };
  return (<>
    <Para>Sollstunden je Wochentag, damit Saldo und „fehlende Zeit“ stimmen. Feiertage und Abwesenheiten trägst du unter Abwesenheiten ein.</Para>
    <div style={{ display: "grid", gridTemplateColumns: "repeat(7, 1fr)", gap: 6 }}>
      {D.map(([k, l]) => <div key={k}><label style={lab}>{l}</label><input style={inpS} type="number" step="0.25" min="0" value={v["target_" + k]} onChange={e => setV({ ...v, ["target_" + k]: e.target.value })} /></div>)}
    </div>
    <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
      <div><label style={lab}>Erfassung ab</label><input style={inpS} type="date" value={v.tracking_start_date} onChange={e => setV({ ...v, tracking_start_date: e.target.value })} /></div>
      <div><label style={lab}>Urlaubstage pro Jahr</label><input style={inpS} type="number" value={v.vacation_days_per_year} onChange={e => setV({ ...v, vacation_days_per_year: e.target.value })} /></div>
    </div>
    <button style={{ ...btn(true), marginTop: 12 }} onClick={save}>Speichern</button>
    <Msg m={msg} />
  </>);
}

/* ── 10 Prüfen & fertig ── */
function StepFinish({ f, put, reload }) {
  return (<>
    <Para>Zum Schluss prüft Zeitprotokoll Datenbank, Sicherungen und die Verbindungen. Danach bleibt der Assistent im Menü und meldet sich auf der Timer-Seite, wenn etwas offen ist, z. B. neue Projekte beim monatlichen Einlesen.</Para>
    <SelfCheckCard />
    <div style={{ display: "flex", gap: 8, marginTop: 14, flexWrap: "wrap" }}>
      <button style={btn(true)} onClick={async () => { await put({ done: { finish: true } }); go("timer"); }}><Check size={14} /> Fertig</button>
      <button style={btn(false)} onClick={async () => { await call("POST", "/assistant/reset"); reload(); }}><RefreshCw size={13} /> Von vorn</button>
      <button style={btn(false)} onClick={async () => { await put({ enabled: false, done: { finish: true } }); go("timer"); location.reload(); }}>Fertig & Assistent ausschalten</button>
    </div>
  </>);
}

/* Einstellungen › System: Schalter */
export function AssistantToggle() {
  const [on, setOn] = useState(null);
  useEffect(() => { api("GET", "/assistant").then(r => r && !r.error && setOn(r.enabled)); }, []);
  if (on == null) return null;
  return (
    <div style={{ background: "var(--bg2)", border: "1px solid var(--border)", borderRadius: 6, padding: "14px 20px", marginBottom: 12 }}>
      <Toggle on={on} onChange={async x => { await call("PUT", "/assistant", { enabled: x }); setOn(x); location.reload(); }}
        label="Einrichtungs-Assistent anzeigen" sub="Menüpunkt „Assistent“ und Hinweise auf der Timer-Seite, wenn etwas offen ist (z. B. neue Projekte)" />
      {on && <button style={{ ...btn(false), marginTop: 6 }} onClick={() => go("assistent")}>Assistent öffnen</button>}
    </div>);
}

/* Abwesenheiten aus dem Kalender: übernehmen oder „nicht meins“ (z. B. Urlaub von Kollegen im geteilten Kalender) */
function AbsenceHints({ onChanged }) {
  const [list, setList] = useState(null);
  const load = useCallback(() => api("GET", "/absence-hints").then(r => setList(r?.calendar || [])), []);
  useEffect(() => { load(); }, [load]);
  if (!list) return null;
  if (!list.length) return <div style={{ fontSize: 12.5, color: "var(--accent)" }}>✓ Nichts mehr offen.</div>;
  const dd = s => new Date(s + "T12:00").toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit" });
  const act = async (h, take, type) => { await call("POST", `/absence-hints/${encodeURIComponent(h.key)}`, { take, type }); load(); onChanged && onChanged(); };
  const L = { urlaub: "Urlaub", krankheit: "Krank", fortbildung: "Fortbildung", sonstiges: "Abwesend" };
  return (<div>
    {list.map(h => (
      <div key={h.key} style={{ borderTop: "1px solid var(--line)", padding: "7px 0", fontSize: 12.5 }}>
        <div><b>{h.title}</b> <span style={{ color: "var(--text2)" }}>· {dd(h.date_from)}{h.date_to !== h.date_from ? ` – ${dd(h.date_to)}` : ""}{h.calendar === "Tempo" ? " · aus Tempo" : h.calendar ? ` · Kalender „${h.calendar}“` : ""}</span></div>
        <div style={{ display: "flex", gap: 6, marginTop: 5, flexWrap: "wrap" }}>
          <button style={{ ...btn(true), padding: "3px 10px" }} onClick={() => act(h, true, h.type)}><Check size={13} /> Als {L[h.type] || "Abwesenheit"} übernehmen</button>
          {h.type !== "krankheit" && <button style={{ ...btn(false), padding: "3px 10px" }} onClick={() => act(h, true, "krankheit")}>Als Krank</button>}
          <button style={{ ...btn(false), padding: "3px 10px" }} onClick={() => act(h, false)}>Nicht meins</button>
        </div>
      </div>))}
    <div style={{ fontSize: 11, color: "var(--text2)", marginTop: 4 }}>Fürs Arbeitszeitkonto zählt nur Urlaub, der hier im Programm steht. Erkannt wird er aus ganztägigen Kalenderterminen („Urlaub“, „Krank“, „Abwesend“) und aus Tempo-Urlaubstickets – übernommen wird erst nach deinem Klick.</div>
  </div>);
}
