import { useState, useEffect, useRef, useCallback } from "react";
import {
  Timer, FolderOpen, BarChart2, Play, Square, ListChecks,
  ArrowLeftRight, Plus, Pencil, Trash2, X,
  ChevronDown, ChevronUp, FileText, Check,
  CalendarClock, CalendarDays, PenLine, Download, RotateCcw, Coffee,
  ChevronLeft, ChevronRight, Settings, TrendingUp, TrendingDown,
  Minus, Umbrella, HeartPulse, Star, BookOpen, HelpCircle, Calendar, Terminal
  , CloudUpload, Upload, Compass, Inbox, Send, Users as UsersIcon, UserCog as UserCogIcon
} from "lucide-react";
import { ReportView as ReportViewNeu } from "./zp-report.jsx";
import { WeekGrid } from "./zp-week.jsx";
import { TempoSyncCard } from "./zp-tempo-sync.jsx";
import { TodoView, TodayTodos } from "./zp-todo.jsx";
import { PairApprove, AddonSettings } from "./zp-addon.jsx";
import { ServerView } from "./zp-server.jsx";
import { TempoSettingsTab, TempoOutboxView, PlaneArea } from "./zp-tempo.jsx";
import { MeetingSuggestions, MeetingSettingsTab, CurrentMeetingBar } from "./zp-meetings.jsx";
import { CalendarView, AuditTab } from "./zp-calendar.jsx";
import { JiraArea, JiraSettingsTab, useJiraOn } from "./zp-jira.jsx";
import { ProjectSourcesPanel } from "./zp-projects.jsx";
import { CleanupPanel, useAllowLocal } from "./zp-cleanup.jsx";
import { DayPanel, DaySummaryLine, PresenceToggle, BrowserToggle, OverlapCard, ReviewCard } from "./zp-day.jsx";
import { AssistantView, AssistantHint, AssistantToggle, useAssistantAutoOpen } from "./zp-assistant.jsx";
import TeamView from "./zp-team.jsx";
import PersonalView from "./zp-personal.jsx";
import { ServerUserBox, ServerBanner, ClientHint, MonthApprovalCard, useJiraModule } from "./zp-servermode.jsx";
import { LeaveRequestCard } from "./zp-leave.jsx";
import { DevicesPanel } from "./zp-devices.jsx";
import { call, api as uiApi, isServerMode, useServerMode, ImpreciseMark, NowCard, QuickStart, TimerActions, DayTimeline, PlaneSettingsTab, PlaneSyncView, TicketInput, projColor, projectFromTicket, resolveTask, ticketLabel, useActivities, reloadActivities, MissingBanner, BrowserTicketBar } from "./zp-ui.jsx";

/* ═══ API ══════════════════════════════════════ */
const api = uiApi;   // gemeinsamer Aufruf inkl. Plausibilitäts-Nachfrage (zp-ui.jsx)

/* ═══ UTILS ════════════════════════════════════ */
const pad      = n => String(n).padStart(2, "0");
const fmtDur   = s => { const h=Math.floor(s/3600),m=Math.floor((s%3600)/60); return h?`${h}h ${pad(m)}m`:`${m}m`; };
const fmtH     = s => { if(!s) return "0m"; const h=Math.floor(s/3600),m=Math.floor((s%3600)/60); if(h&&m)return`${h}h ${m}m`; if(h)return`${h}h`; return`${m}m`; };
const fmtHDec  = s => (s/3600).toFixed(1)+"h";
const fmtTime  = iso => iso ? new Date(iso).toLocaleTimeString("de-DE",{hour:"2-digit",minute:"2-digit"}) : "--:--";
const fmtDate  = d => new Date((d||today())+"T12:00").toLocaleDateString("de-DE",{weekday:"short",day:"2-digit",month:"2-digit"});
const ld       = d => `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())}`;
const today    = () => ld(new Date());
// ISO-Kalenderwoche (Donnerstag-Regel)
const isoWeek  = dateStr => { const d=new Date(dateStr+"T12:00:00Z"); const day=(d.getUTCDay()+6)%7; d.setUTCDate(d.getUTCDate()-day+3); const y=d.getUTCFullYear(); const f=new Date(Date.UTC(y,0,4)); return { year:y, week: 1+Math.round(((d-f)/864e5-3+((f.getUTCDay()+6)%7))/7) }; };
const now      = () => new Date().toISOString();
const toISO    = (dateStr, timeStr) => `${dateStr}T${timeStr}:00.000Z`;
const toLocal  = iso => { if(!iso) return ""; const d = new Date(iso); return `${pad(d.getHours())}:${pad(d.getMinutes())}`; };
const euro     = n => n.toLocaleString("de-DE",{style:"currency",currency:"EUR"});
const monthFrom= () => { const d=new Date(); return `${d.getFullYear()}-${pad(d.getMonth()+1)}-01`; };

const STATUS_COL = { active:"var(--green)", paused:"var(--accent)", done:"var(--text2)", archived:"var(--text3)" };
const STATUS_LBL = { active:"Aktiv", paused:"Pausiert", done:"Fertig", archived:"Archiviert" };
const TASK_COL   = { open:"var(--blue)", in_progress:"var(--accent)", done:"var(--green)" };
const TASK_LBL   = { open:"Offen", in_progress:"In Arbeit", done:"Fertig" };

/* Kurzes Projekt-Etikett: Plane-Kürzel (PRJA) statt „Beispiel Interne Prozesse Projekt“ */
const projShort = (projects, id, name) => {
  const p = (projects||[]).find(x => x.id === id);
  const k = p?.plane_identifier || p?.jira_key;
  return k ? String(k).toUpperCase() : (name && name.length > 18 ? name.slice(0, 16) + "…" : name);
};

/* ═══ SHARED COMPONENTS ════════════════════════ */
function Modal({ title, onClose, children, width=520 }) {
  return (
    <div style={M.overlay} onClick={e=>e.target===e.currentTarget&&onClose()}>
      <div style={{...M.box, maxWidth:width}}>
        <div style={M.header}>
          <span style={M.title}>{title}</span>
          <button style={M.closeBtn} onClick={onClose}><X size={16}/></button>
        </div>
        {children}
      </div>
    </div>
  );
}
const M = {
  overlay:  { position:"fixed",inset:0,background:"rgba(0,0,0,.25)",display:"flex",alignItems:"flex-start",justifyContent:"center",zIndex:100,padding:"60px 20px 20px" },
  box:      { background:"var(--card)",border:"1px solid var(--line)",borderRadius:12,width:"100%",maxHeight:"85vh",overflow:"auto",boxShadow:"0 10px 40px rgba(0,0,0,.25)",animation:"pop .2s ease" },
  header:   { display:"flex",alignItems:"center",justifyContent:"space-between",padding:"14px 18px",borderBottom:"1px solid var(--line)" },
  title:    { color:"var(--hi)",fontWeight:600,fontSize:14 },
  closeBtn: { background:"transparent",border:"none",color:"var(--text3)",display:"flex",alignItems:"center",padding:2 },
};

const FLD = { display:"flex",flexDirection:"column",gap:5,marginBottom:12 };
const LBL = { color:"var(--text2)",fontSize:11,fontWeight:500 };
const inp  = { background:"var(--card)",border:"1px solid var(--line)",borderRadius:7,padding:"7px 10px",color:"var(--text)",width:"100%" };
const sel  = { ...inp, appearance:"none" };

function Field({ label, children, hint }) {
  return (
    <div style={FLD}>
      {label && <div style={LBL}>{label}</div>}
      {children}
      {hint && <div style={{color:"var(--text3)",fontSize:10,marginTop:3}}>{hint}</div>}
    </div>
  );
}

function Btn({ children, onClick, variant="default", disabled, style:ex, size="md" }) {
  const base = { display:"inline-flex",alignItems:"center",gap:6,borderRadius:7,fontWeight:600,fontFamily:"inherit",border:"1px solid",opacity:disabled?.45:1,cursor:disabled?"default":"pointer",transition:"opacity 0.15s, background 0.15s",padding:size==="sm"?"5px 11px":"7px 15px",fontSize:size==="sm"?12:13 };
  const v = { default:{background:"var(--card)",borderColor:"var(--line)",color:"var(--text)"}, primary:{background:"var(--accent-fill)",borderColor:"var(--accent-fill)",color:"#fff"}, danger:{background:"rgba(217,69,59,.10)",borderColor:"transparent",color:"var(--stop)"}, accent:{background:"var(--accent-soft)",borderColor:"transparent",color:"var(--accent)"} };
  return <button style={{...base,...v[variant],...ex}} onClick={disabled?undefined:onClick}>{children}</button>;
}

function Badge({ label, color }) {
  return <span style={{background:"var(--bg)",color,border:"1px solid var(--line)",borderRadius:20,padding:"1px 8px",fontSize:10,fontWeight:600,whiteSpace:"nowrap"}}>{label}</span>;
}

function ProgressBar({ value, max, warn=0.8 }) {
  if(!max) return null;
  const pct = Math.min(1, value/max);
  const c = pct>=1?"var(--red)":pct>=warn?"var(--accent)":"var(--green)";
  return (
    <div style={{height:3,background:"var(--border)",borderRadius:2,overflow:"hidden"}}>
      <div style={{width:`${pct*100}%`,height:"100%",background:c,transition:"width 0.3s"}}/>
    </div>
  );
}

/* ═══ MANUAL / EDIT TIME ENTRY MODAL ══════════ */
function TimeEntryModal({ entry, projects, onSave, onClose, defaultDate }) {
  const isEdit = !!entry?.id;
  const defDate = entry?.date || defaultDate || today();
  const defStart = entry?.start_time ? toLocal(entry.start_time) : "";
  const defEnd   = entry?.end_time   ? toLocal(entry.end_time)   : "";

  const [projId,  setProjId]  = useState(entry?.project_id || "");
  const [taskId,  setTaskId]  = useState(entry?.task_id    || "");
  const [ticket,  setTicket]  = useState(entry?.jira_ticket || entry?.task_title || "");
  const [picked,  setPicked]  = useState(null);
  const [err,     setErr]     = useState("");
  const [tasks,   setTasks]   = useState([]);
  const [date,    setDate]    = useState(defDate);
  const [start,   setStart]   = useState(defStart);
  const [end,     setEnd]     = useState(defEnd);
  const [dur,     setDur]     = useState("");
  const [comment, setComment] = useState(entry?.comment    || "");
  const [billable,setBillable]= useState(entry?.billable !== 0);
  const [activity,setActivity]= useState(entry?.activity || "");
  const activities = useActivities();
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!projId) { setTasks([]); return; }
    api("GET", `/tasks?projectId=${projId}`).then(t => setTasks(Array.isArray(t)?t:[]));
  }, [projId]);

  // Dauer wie bei Tempo: „1h 30“, „1,5h“, „45m“, „90“ (Minuten), „1:30“
  const parseDur = v => {
    const t = String(v||"").trim().toLowerCase().replace(",",".");
    if (!t) return null;
    let m = t.match(/^(\d+):(\d{1,2})$/); if (m) return (+m[1])*60+(+m[2]);
    m = t.match(/^(?:(\d+(?:\.\d+)?)\s*(?:h|std|stunden?)\s*)?(?:(\d+)\s*(?:m|min|minuten?)?)?$/);
    if (!m || (m[1]==null && m[2]==null)) return NaN;
    return Math.round((m[1]?parseFloat(m[1])*60:0)+(m[2]?+m[2]:0));
  };
  const durMin = parseDur(dur);
  const save = async () => {
    if (!start && durMin==null) return;
    if (Number.isNaN(durMin) || durMin===0) { setErr("Dauer nicht lesbar – z. B. 1h 30, 45m, 1,5h."); return; }
    setLoading(true);
    // Aufgabe aus Ticket-Feld ermitteln (lokal oder aus Plane übernommen)
    let tId = null;
    const tk = ticket.trim();
    if (tk && projId) {
      const cur = tasks.find(t => t.id === taskId || String(t.id) === String(taskId));
      tId = (cur && (cur.jira_ticket === tk || cur.title === tk)) ? cur.id : await resolveTask(parseInt(projId), tk, picked?.name);
    }
    const autoPlace = !isEdit && !start && durMin > 0; // Tempo-Stil: nur Dauer → Server legt Uhrzeit in freie Lücke
    let startISO = null, endISO = null, durSecs = null;
    if (autoPlace) {
      durSecs = durMin * 60;
    } else {
      const startLocal = start || "08:00";
      startISO = new Date(`${date}T${startLocal}`).toISOString();
      endISO   = durMin ? new Date(new Date(startISO).getTime()+durMin*60000).toISOString() : end ? new Date(`${date}T${end}`).toISOString() : null;
      durSecs  = endISO ? Math.floor((new Date(endISO)-new Date(startISO))/1000) : null;
      if (durSecs !== null && durSecs <= 0) { setErr("Ende muss nach dem Start liegen."); setLoading(false); return; }
    }
    // Warnung bei sehr kurzen Einträgen (< 5min)
    if (durSecs !== null && durSecs < 300) {
      const ok = window.confirm(`Eintrag ist nur ${Math.floor(durSecs/60)} Minuten lang. Trotzdem speichern?`);
      if (!ok) { setLoading(false); return; }
    }

    if (isEdit) {
      const r = await api("PUT", `/time-entries/${entry.id}`, {
        project_id: projId ? parseInt(projId) : null, task_id: tId,
        start_time: startISO, end_time: endISO, date,
        duration_seconds: durSecs, comment, billable, activity: activity || null
      });
      if (!r || r.error) { setErr(r?.error || "Speichern fehlgeschlagen – läuft der Dienst?"); setLoading(false); return; }
    } else {
      const body = {
        project_id: projId ? parseInt(projId) : null, task_id: tId,
        duration_seconds: durSecs, comment, billable, date, activity: activity || null
      };
      if (!autoPlace) { body.start_time = startISO; body.end_time = endISO; }
      const r = await api("POST", "/time-entries", body);
      if (!r || r.error) { setErr(r?.error || "Speichern fehlgeschlagen – läuft der Dienst?"); setLoading(false); return; }
    }
    setLoading(false);
    onSave();
  };

  return (
    <Modal title={isEdit ? "⏱ Zeiteintrag bearbeiten" : "⏱ Zeit eintragen"} onClose={onClose} width={480}>
      <div style={{padding:"14px 18px 18px"}}>
        <div style={{display:"grid",gridTemplateColumns:"1fr 1fr",gap:12}}>
          <Field label="PROJEKT">
            <select style={sel} value={projId} onChange={e=>{setProjId(e.target.value);setTaskId("");}}>
              <option value="">– ohne Projekt / Ticket –</option>
              {projects.filter(p=>p.status!=="archived").map(p=>
                <option key={p.id} value={p.id}>{p.customer_name ? `${p.customer_name} – `:""}{p.name}</option>
              )}
            </select>
          </Field>
          <Field label="TICKET / AUFGABE">
            <div className="zp-fill zp-fill-lg"><TicketInput value={ticket} localTasks={tasks} projectId={projId}
              onChange={v=>{ setTicket(v); setPicked(null); const p=projectFromTicket(projects,v); if(p&&String(p.id)!==String(projId)) setProjId(String(p.id)); }}
              onPick={it=>{ setPicked(it); if(it.local_project_id) setProjId(String(it.local_project_id)); }}/></div>
          </Field>
        </div>
        <div style={{display:"grid",gridTemplateColumns:"1fr 1fr 1fr 1fr",gap:12}}>
          <Field label="DATUM">
            <input style={inp} type="date" value={date} onChange={e=>setDate(e.target.value)}/>
          </Field>
          <Field label="DAUER">
            <input style={inp} placeholder="1h 30" value={dur} onChange={e=>setDur(e.target.value)} title="wie Tempo: 1h 30, 45m, 1,5h – ohne Start/Ende legt der Assistent die Uhrzeit"/>
          </Field>
          <Field label="START (opt.)">
            <input style={inp} type="time" value={start} onChange={e=>setStart(e.target.value)}/>
          </Field>
          <Field label="ENDE (opt.)">
            <input style={inp} type="time" value={end} disabled={durMin>0} onChange={e=>setEnd(e.target.value)}/>
          </Field>
        </div>
        {!isEdit && !start && !!dur.trim() && (
          <div style={{fontSize:12,color:"var(--text2)",margin:"-4px 0 10px"}}>Ohne Start/Ende: Uhrzeit wird passend in eine freie Lücke des Tages gelegt.</div>
        )}
        <div style={{display:"grid",gridTemplateColumns:"2fr 1fr",gap:12}}>
          <Field label="KOMMENTAR">
            <input style={inp} placeholder="Was wurde gemacht?" value={comment} onChange={e=>setComment(e.target.value)} onKeyDown={e=>e.key==="Enter"&&save()}/>
          </Field>
          <Field label="TÄTIGKEIT">
            <select style={sel} value={activity} onChange={e=>setActivity(e.target.value)}>
              <option value="">– keine –</option>
              {[...new Set([...activities, ...(activity?[activity]:[])])].map(a=><option key={a} value={a}>{a}</option>)}
            </select>
          </Field>
        </div>
        <div style={{display:"flex",alignItems:"center",gap:8,marginBottom:14}}>
          <input type="checkbox" id="billable" checked={billable} onChange={e=>setBillable(e.target.checked)} style={{accentColor:"var(--green)"}}/>
          <label htmlFor="billable" style={{color:"var(--text2)",fontSize:12,cursor:"pointer"}}>Abrechenbar</label>
        </div>
        {err&&<div style={{color:"var(--stop)",fontSize:12,marginBottom:10}}>{err}</div>}
        <div style={{display:"flex",gap:8,justifyContent:"flex-end"}}>
          <Btn onClick={onClose}>Abbrechen</Btn>
          <Btn variant="primary" onClick={save} disabled={(!start&&!dur.trim())||loading}>
            <Check size={14}/> {loading?"…":isEdit?"Speichern":"Eintragen"}
          </Btn>
        </div>
      </div>
    </Modal>
  );
}

/* ═══ PAUSE MANUELL ERFASSEN ════════════════════
   Legt eine neue Pause mit frei wählbarem Start/Ende an. Auf reinen
   Buchungstagen (nur manuelle Zeiteinträge/Timer, kein Arbeitstag-Konzept
   mehr vorhanden) wird die Pause automatisch von den gebuchten Stunden
   abgezogen – z.B. durchgehender Eintrag 09-17 Uhr + Pause 12-13 Uhr = 7h. */
function PauseEntryModal({ date, onSave, onClose }) {
  const [pDate,   setPDate]   = useState(date || today());
  const [start,   setStart]   = useState("");
  const [end,     setEnd]     = useState("");
  const [loading, setLoading] = useState(false);
  const [error,   setError]   = useState("");

  const save = async () => {
    if (!start || !end) return;
    setError("");
    setLoading(true);
    const startISO = new Date(`${pDate}T${start}`).toISOString();
    const endISO   = new Date(`${pDate}T${end}`).toISOString();
    if (endISO <= startISO) {
      setError("Ende muss nach dem Start liegen");
      setLoading(false);
      return;
    }
    const res = await api("POST", "/pauses", { date: pDate, start_time: startISO, end_time: endISO });
    setLoading(false);
    if (!res || res.error) { setError(res?.error || "Fehler beim Speichern"); return; }
    onSave();
  };

  return (
    <Modal title="☕ Pause erfassen" onClose={onClose} width={400}>
      <div style={{padding:"14px 18px 18px"}}>
        <div style={{display:"grid",gridTemplateColumns:"1fr 1fr 1fr",gap:12}}>
          <Field label="DATUM">
            <input style={inp} type="date" value={pDate} onChange={e=>setPDate(e.target.value)}/>
          </Field>
          <Field label="START *">
            <input style={inp} type="time" value={start} onChange={e=>setStart(e.target.value)}/>
          </Field>
          <Field label="ENDE *">
            <input style={inp} type="time" value={end} onChange={e=>setEnd(e.target.value)} onKeyDown={e=>e.key==="Enter"&&save()}/>
          </Field>
        </div>
        <div style={{color:"var(--text3)",fontSize:11,marginTop:10,lineHeight:1.4}}>
          Wird automatisch von den gebuchten Stunden dieses Tages abgezogen.
        </div>
        {error && <div style={{color:"var(--red)",fontSize:11,marginTop:8}}>{error}</div>}
        <div style={{display:"flex",gap:8,justifyContent:"flex-end",marginTop:14}}>
          <Btn onClick={onClose}>Abbrechen</Btn>
          <Btn variant="primary" onClick={save} disabled={!start||!end||loading}>
            <Check size={13}/> {loading?"…":"Speichern"}
          </Btn>
        </div>
      </div>
    </Modal>
  );
}

/* ═══ PROJECT PICKER ════════════════════════════ */
function ProjectPicker({ projects, onSelect, onCancel, onProjectCreated, mode="start" }) {
  const [search,      setSearch]      = useState("");
  const [proj,        setProj]        = useState(null);
  const [planTask,    setPlanTask]    = useState(null);
  const [tasks,       setTasks]       = useState([]);
  const [task,        setTask]        = useState(null);
  const [comment,     setComment]     = useState("");
  const [creating,    setCreating]    = useState(false);
  const [creatingPlan,setCreatingPlan]= useState(false);
  const [planTasks,   setPlanTasks]   = useState([]);
  const ref = useRef(null);
  useEffect(() => { ref.current?.focus(); }, []);
  useEffect(() => { api("GET","/plan/today").then(d=>setPlanTasks(d?.todayTasks||[])); }, []);

  const active = projects.filter(p =>
    (p.status==="active"||p.status==="paused") &&
    [p.name, p.customer_name, p.jira_key].some(v=>v?.toLowerCase().includes(search.toLowerCase()))
  );
  const grouped = active.reduce((a,p)=>{ const k=p.customer_name||"– kein Kunde –"; (a[k]=a[k]||[]).push(p); return a; },{});
  const matchingPlanTasks = planTasks.filter(t =>
    !search.trim() || [t.title, t.project_name].some(v=>v?.toLowerCase().includes(search.toLowerCase())));
  const exactMatch = search.trim() && projects.some(p=>p.name.toLowerCase()===search.trim().toLowerCase());
  const exactPlanMatch = search.trim() && planTasks.some(t=>t.title.toLowerCase()===search.trim().toLowerCase());
  const allowLocal = useAllowLocal();
  const canCreate = allowLocal && search.trim().length>0 && !exactMatch;
  const canCreatePlan = search.trim().length>0 && !exactPlanMatch;

  const pick = async p => {
    const t = await api("GET",`/tasks?projectId=${p.id}`);
    setTasks((t||[]).filter(t=>t.status!=="done"));
    setPlanTask(null); setProj(p); setTask(null);
  };

  const pickPlanTask = pt => {
    setTasks([]); setTask(null);
    const matchedProj = pt.project_id ? projects.find(p=>p.id===pt.project_id) : null;
    setProj(matchedProj || (pt.project_id ? {id:pt.project_id, name:pt.project_name} : null));
    setPlanTask(pt);
  };

  const createAndPick = async () => {
    const name = search.trim();
    if (!name || creating) return;
    setCreating(true);
    const newProj = await api("POST","/projects",{name});
    setCreating(false);
    if (!newProj || newProj.error) return;
    onProjectCreated?.();
    setTasks([]); setPlanTask(null); setProj(newProj); setTask(null);
  };

  const createPlanAndPick = async () => {
    const title = search.trim();
    if (!title || creatingPlan) return;
    setCreatingPlan(true);
    const newTask = await api("POST","/plan/tasks",{title, scheduled_date: today()});
    setCreatingPlan(false);
    if (!newTask || newTask.error) return;
    pickPlanTask(newTask);
  };

  const submit = () => {
    if(!proj && !planTask) return;
    onSelect({projectId:proj?.id||null, taskId:task?.id||null, planTaskId:planTask?.id||null, comment:comment.trim()});
  };

  return (
    <Modal title={mode==="switch"?"⇄ Projekt wechseln":"▶ Timer starten"} onClose={onCancel} width={480}>
      <div style={{padding:"14px 18px 18px"}}>
        {!proj && !planTask ? (
          <>
            <input ref={ref} style={{...inp,marginBottom:12}} placeholder="Projekt/Aufgabe suchen… oder neuen Namen eintippen" value={search} onChange={e=>setSearch(e.target.value)}/>

            {matchingPlanTasks.length>0 && (
              <div style={{marginBottom:14}}>
                <div style={{color:"var(--text3)",fontSize:10,letterSpacing:"0.1em",fontWeight:600,padding:"0 4px 4px"}}>📋 HEUTE GEPLANT</div>
                {matchingPlanTasks.map(t=>(
                  <button key={t.id} onClick={()=>pickPlanTask(t)} style={{display:"flex",alignItems:"center",gap:10,width:"100%",background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:4,padding:"9px 10px",textAlign:"left",color:"var(--text)",cursor:"pointer",marginBottom:4}}>
                    <ListChecks size={14} color="var(--purple)" style={{flexShrink:0}}/>
                    <div style={{flex:1}}>
                      <div style={{color:"var(--hi)",fontWeight:600}}>{t.title}</div>
                      {t.project_name&&<div style={{color:"var(--text3)",fontSize:11}}>{t.project_name}</div>}
                    </div>
                    <span style={{color:"var(--text3)",fontSize:11}}>{Math.round((t.tracked_seconds||0)/60)}/{t.estimated_minutes} Min</span>
                  </button>
                ))}
              </div>
            )}
            {canCreatePlan && (
              <button onClick={createPlanAndPick} disabled={creatingPlan} style={{display:"flex",alignItems:"center",gap:10,width:"100%",background:"var(--bg2)",border:"1px dashed var(--purple)",borderRadius:5,padding:"9px 10px",textAlign:"left",color:"var(--text)",cursor:creatingPlan?"default":"pointer",marginBottom:10}}>
                <ListChecks size={14} color="var(--purple)"/>
                <span>{creatingPlan?"Wird angelegt…":<>Neue Planungsaufgabe <strong style={{color:"var(--hi)"}}>„{search.trim()}"</strong> für heute anlegen</>}</span>
              </button>
            )}
            {canCreate && (
              <button onClick={createAndPick} disabled={creating} style={{display:"flex",alignItems:"center",gap:10,width:"100%",background:"var(--bg2)",border:"1px dashed var(--border2)",borderRadius:5,padding:"9px 10px",textAlign:"left",color:"var(--text)",cursor:creating?"default":"pointer",marginBottom:10}}>
                <Plus size={14} color="var(--green)"/>
                <span>{creating?"Wird angelegt…":<>Neues Projekt <strong style={{color:"var(--hi)"}}>„{search.trim()}"</strong> anlegen</>}</span>
              </button>
            )}
            <div style={{maxHeight:280,overflow:"auto"}}>
              {Object.entries(grouped).map(([cust,list])=>(
                <div key={cust}>
                  <div style={{color:"var(--text3)",fontSize:10,letterSpacing:"0.1em",fontWeight:600,padding:"8px 4px 4px"}}>{cust}</div>
                  {list.map(p=>(
                    <button key={p.id} onClick={()=>pick(p)} style={{display:"flex",alignItems:"center",gap:10,width:"100%",background:"transparent",border:"none",borderBottom:"1px solid var(--border)",borderRadius:4,padding:"9px 10px",textAlign:"left",color:"var(--text)",cursor:"pointer"}}>
                      <span style={{width:8,height:8,borderRadius:"50%",background:STATUS_COL[p.status],flexShrink:0}}/>
                      <div style={{flex:1}}>
                        <div style={{color:"var(--hi)",fontWeight:600}}>{p.name}</div>
                        {p.jira_key&&<div style={{color:"var(--text3)",fontSize:11}}>{p.jira_key}</div>}
                      </div>
                      {p.budget_hours>0&&<span style={{color:"var(--text3)",fontSize:11}}>{fmtHDec(p.booked_seconds||0)}/{p.budget_hours}h</span>}
                    </button>
                  ))}
                </div>
              ))}
              {active.length===0&&matchingPlanTasks.length===0&&!canCreate&&!canCreatePlan&&<div style={{color:"var(--text3)",padding:"20px 0",textAlign:"center"}}>Keine aktiven Projekte</div>}
            </div>
          </>
        ) : (
          <>
            <button onClick={()=>{setProj(null);setPlanTask(null);}} style={{background:"transparent",border:"none",color:"var(--text3)",fontSize:11,marginBottom:12,cursor:"pointer"}}>← zurück</button>
            <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"10px 14px",marginBottom:14}}>
              {planTask && (
                <div style={{display:"flex",alignItems:"center",gap:6,color:"var(--purple)",fontSize:11,fontWeight:600,marginBottom:4}}>
                  <ListChecks size={12}/> GEPLANT: {Math.round((planTask.tracked_seconds||0)/60)}/{planTask.estimated_minutes} Min
                </div>
              )}
              <div style={{color:"var(--hi)",fontWeight:700}}>{planTask?.title || proj?.name}</div>
              {(proj?.customer_name||proj?.name)&&planTask&&<div style={{color:"var(--text3)",fontSize:11}}>{proj?.name}</div>}
              {!planTask&&proj?.customer_name&&<div style={{color:"var(--text3)",fontSize:11}}>{proj.customer_name}</div>}
            </div>
            {tasks.length>0&&(
              <Field label="AUFGABE (optional)">
                {tasks.map(t=>(
                  <button key={t.id} onClick={()=>setTask(task?.id===t.id?null:t)} style={{display:"flex",alignItems:"center",gap:8,width:"100%",background:task?.id===t.id?"var(--bg4)":"transparent",border:`1px solid ${task?.id===t.id?"var(--border2)":"var(--border)"}`,borderRadius:4,padding:"8px 10px",textAlign:"left",color:"var(--text)",cursor:"pointer",marginBottom:4}}>
                    {task?.id===t.id&&<Check size={14} color="var(--green)"/>}
                    {t.jira_ticket&&<span style={{color:"var(--blue)",fontWeight:700,fontSize:11}}>{t.jira_ticket}</span>}
                    <span style={{flex:1}}>{t.title}</span>
                    <Badge label={TASK_LBL[t.status]} color={TASK_COL[t.status]}/>
                  </button>
                ))}
              </Field>
            )}
            <Field label="KOMMENTAR">
              <input style={inp} placeholder="Was machst du?" value={comment} onChange={e=>setComment(e.target.value)} onKeyDown={e=>e.key==="Enter"&&submit()}/>
            </Field>
            <Btn variant="primary" onClick={submit}><Play size={14}/> {mode==="switch"?"Wechseln":"Starten"}</Btn>
          </>
        )}
      </div>
    </Modal>
  );
}

/* ═══ TIMER VIEW ════════════════════════════════ */
/* ════════════════════════════════════════════════
   WEEK VIEW (Hauptseite)
════════════════════════════════════════════════ */
function TimerView({ timer, elapsed, onStart, onStop, onSwitch, workday, todayData,
  onAddLog, onDelLog, onRefresh, projects, pauses,
  pauseState, pauseElapsed, onPauseStart, onPauseStop,
  viewDate, onDateChange, overview, weekOffset, onWeekChange, getWeekRange }) {

  const [expandedDay, setExpandedDay]   = useState(today());
  const [editEntry,   setEditEntry]     = useState(null);
  const [manualOpen,  setManual]        = useState(false);
  const [pauseOpen,   setPauseOpen]     = useState(false);
  const [manualDate,  setManualDate]    = useState(null);
  const [dayData,     setDayData]       = useState({});  // cache per date

  const isToday   = viewDate === today();

  // ── Booked time (Summe aller Buchungen – live Timer + Zeiteinträge) ──
  const bookedSecsRaw = (todayData?.timeEntries||[]).reduce((s,e)=>s+(e.duration_seconds||0),0)
                      + (timer.active&&isToday ? elapsed : 0);
  // Pausen des angezeigten Tages abziehen (analog zur Backend-Logik für andere Tage)
  const todayPauseSecs = (pauses||[]).filter(p=>p.end_time)
    .reduce((s,p)=>s+Math.floor((new Date(p.end_time)-new Date(p.start_time))/1000),0);
  const bookedSecs = Math.max(0, bookedSecsRaw - todayPauseSecs);

  // ── Week data ─────────────────────────────────
  const { from: wFrom, to: wTo } = getWeekRange ? getWeekRange(weekOffset) : { from:today(), to:today() };
  const weekDays  = overview?.days || [];
  const weekLabel = (() => {
    if (!weekDays.length) return "";
    const f = new Date(wFrom+"T12:00:00Z");
    const t = new Date(wTo+"T12:00:00Z");
    const kw = isoWeek(wFrom).week;
    return `KW ${kw} · ${f.toLocaleDateString("de-DE",{day:"2-digit",month:"2-digit"})} – ${t.toLocaleDateString("de-DE",{day:"2-digit",month:"2-digit",year:"numeric"})}`;
  })();

  // ── Load day detail on expand ─────────────────
  const loadDay = async (date) => {
    const d = await api("GET", `/workday?date=${date}`);
    setDayData(prev => ({ ...prev, [date]: {
      timeEntries: d?.timeEntries||[],
      dailyLogs:   d?.dailyLogs||[],
      pauses:      d?.pauses||[],
      workday:     d?.workday||null,
    }}));
  };

  // Today's data always from props
  useEffect(() => {
    setDayData(prev => ({ ...prev, [today()]: { workday, dailyLogs: todayData?.dailyLogs||[], timeEntries: todayData?.timeEntries||[], pauses } }));
  }, [workday, todayData, pauses]);

  const toggleDay = (date) => {
    if (expandedDay === date) { setExpandedDay(null); return; }
    setExpandedDay(date);
    onDateChange(date);
    // Always reload past days (not just first time) to get fresh data
    if (date !== today()) {
      setDayData(p=>{ const n={...p}; delete n[date]; return n; }); // invalidate cache
      loadDay(date);
    }
  };

  const addLog = async () => {}; // ungenutzt seit Entfernung des Stundenlogs, Platzhalter für onAddLog-Prop


  // ─────────────────────────────────────────────
  return (
    <>
    <div style={{display:"flex",gap:16,alignItems:"flex-start"}}>
    <div style={{flex:1,minWidth:0,display:"flex",flexDirection:"column",gap:10}}>

      {/* ══ JETZT · SCHNELLSTART · PAUSE/STOPP (wie Schnellfenster) ══ */}
      <MissingBanner/>
      {isToday&&<AssistantHint/>}
      {isToday&&!isServerMode()&&<CurrentMeetingBar timer={timer} onChanged={onRefresh}/>}
      {isToday&&!isServerMode()&&<MeetingSuggestions projects={projects} onChanged={onRefresh}/>}
      <div className="zp-panel">
        {isToday&&<BrowserTicketBar timer={timer} onChanged={onRefresh}/>}
        <NowCard timer={timer} pauseState={pauseState}/>
        <QuickStart projects={projects} onChanged={onRefresh} timer={timer}/>
        <TimerActions timer={timer} pauseState={pauseState} onChanged={onRefresh}
          onManual={()=>{ setManualDate(expandedDay||today()); setManual(true); }}
          onPauseEntry={()=>{ onDateChange(expandedDay||today()); setPauseOpen(true); }}/>
      </div>

      <DaySummaryLine date={today()} tick={Math.floor(bookedSecs/300)}/>
      <OverlapCard tick={Math.floor(bookedSecs/300)} onChanged={onRefresh}/>
      <ReviewCard tick={Math.floor(bookedSecs/300)} onChanged={onRefresh}/>
      {/* ══ WOCHE NAVIGATION + SALDO ═════════════ */}
      <div style={{display:"flex",alignItems:"center",gap:8,padding:"8px 2px 0"}}>
        <button className="zp-nav" onClick={()=>onWeekChange(-1)}><ChevronLeft size={17}/></button>
        <div style={{flex:1,fontWeight:600,fontSize:13}}>{weekLabel}</div>
        <button className="zp-nav" onClick={()=>onWeekChange(1)} disabled={weekOffset>=0}><ChevronRight size={17}/></button>
        {weekOffset!==0&&<button onClick={()=>onWeekChange(-weekOffset)} style={{background:"var(--accent-soft)",border:"none",borderRadius:7,padding:"5px 10px",color:"var(--accent)",fontSize:12,fontWeight:600}}>Heute</button>}

        {/* Saldo-Widget: GESAMT / MONAT / WOCHE */}
        {weekDays.length>0&&(()=>{
          const todayStr  = today();
          const todayLiveSecs = bookedSecs;

          // Saldo wie Tempo „bis heute“: abgeschlossene Tage + heute nur, wenn schon über dem Soll.
          // Zukunft zählt nie ins Minus. Dazu „noch offen“: Rest-Soll ab heute (wie viel Zeit noch zu leisten / übrig ist).
          const istOf = d => d.absence ? d.absenceH : d.date===todayStr ? todayLiveSecs/3600 : (d.workH||d.bookedH||0);
          const sumUp = list => { let delta=0, rest=0;
            list.filter(d=>!d.isBeforeTracking).forEach(d=>{
              if (d.date<todayStr) delta += istOf(d) - d.targetH;
              else if (d.date===todayStr) { const diff = istOf(d) - d.targetH; if (diff>0) delta += diff; else rest += -diff; }
              else rest += d.absence ? 0 : d.targetH;
            });
            return { delta, rest }; };
          const {from:wkFrom,to:wkTo} = getWeekRange ? getWeekRange(weekOffset) : {from:todayStr,to:todayStr};
          const W = sumUp(weekDays.filter(d=>d.date>=wkFrom&&d.date<=wkTo));
          const wDelta = W.delta;
          const curMonth = todayStr.slice(0,7);
          const M = sumUp(weekDays.filter(d=>d.date.startsWith(curMonth)));
          const mDelta = M.delta;

          // Gesamt-Saldo: kumulativ bis gestern (+ heutiges Plus)
          const yesterday = weekDays.filter(d=>d.date<todayStr).slice(-1)[0];
          const tdy = weekDays.find(d=>d.date===todayStr);
          const cumBal    = yesterday ? yesterday.cumulativeBalance + (tdy && !tdy.absence ? Math.max(0, todayLiveSecs/3600 - tdy.targetH) : 0) : null;
          const restTxt = h => h>0.05 ? `noch ${fmtH(h*3600)}` : "Soll erfüllt";

          const acctOk = cumBal==null || cumBal>=0;   // Konto im Plus → Defizite nur neutral/„noch offen“, rot nur bei negativem Konto
          const V = ({value, big, acct}) => value<0&&!acct&&acctOk
            ? <div className="v" style={{fontSize:13,color:"var(--text2)"}}>noch {Math.abs(value).toFixed(1)} h offen</div>
            : <div className="v" style={{fontSize:big?16:13,color:value>=0?"var(--green)":"var(--red)"}}>{value>=0?"+":""}{value.toFixed(1)} h</div>;
          const MONTHS=["Januar","Februar","März","April","Mai","Juni","Juli","August","September","Oktober","November","Dezember"];
          return (
            <div className="zp-saldo">
              {cumBal!=null&&<div><div className="l">Arbeitszeitkonto</div><V value={cumBal} big acct/></div>}
              <div title="Saldo bis heute · darunter: Rest-Soll bis Monatsende"><div className="l">{MONTHS[parseInt(curMonth.slice(5,7))-1]}</div><V value={mDelta}/><div style={{fontSize:10,color:"var(--text3)"}}>{restTxt(M.rest)}</div></div>
              <div title="Saldo bis heute · darunter: Rest-Soll dieser Woche"><div className="l">Woche</div><V value={wDelta}/><div style={{fontSize:10,color:"var(--text3)"}}>{weekOffset===0?restTxt(W.rest):"abgeschlossen"}</div></div>
            </div>
          );
        })()}
      </div>

      {/* ══ TAGE DER WOCHE ═══════════════════════ */}
      {weekDays.filter(d => {
        const {from:wf, to:wt} = getWeekRange ? getWeekRange(weekOffset) : {from:d.date,to:d.date};
        return d.date >= wf && d.date <= wt;
      }).map(dayInfo => {
        const isExp  = expandedDay === dayInfo.date;
        const isTdy  = dayInfo.date === today();
        const dow    = ["So","Mo","Di","Mi","Do","Fr","Sa"][dayInfo.dow];
        const datum  = new Date(dayInfo.date+"T12:00:00Z").toLocaleDateString("de-DE",{day:"2-digit",month:"2-digit"});
        const saldo  = dayInfo.balanceH;
        const acctPlus = (()=>{ const y=weekDays.filter(x=>x.date<today()).slice(-1)[0]; return !y || y.cumulativeBalance>=0; })();
        const isAbs  = !!dayInfo.absence;
        const absCfg = isAbs ? (ABS_TYPES[dayInfo.absence.type]||ABS_TYPES.sonstiges) : null;
        const cachedDay = isTdy
          ? { timeEntries: todayData?.timeEntries||[], dailyLogs: todayData?.dailyLogs||[], pauses: pauses||[] }
          : dayData[dayInfo.date] || null;

        return (
          <div key={dayInfo.date} className={"zp-day"+(isTdy?" today":"")}>
            {/* Day header */}
            <div className="zp-day-head" onClick={()=>toggleDay(dayInfo.date)}>
              <div style={{minWidth:74,display:"flex",alignItems:"baseline",gap:6}}>
                <span style={{color:isTdy?"var(--accent)":"var(--text2)",fontSize:12,fontWeight:600,width:20}}>{dow}</span>
                <span style={{color:isTdy?"var(--accent)":"var(--text)",fontWeight:600,fontSize:14}}>{datum}</span>
              </div>

              {/* Absence badge */}
              {isAbs&&<span style={{background:`${absCfg.color}18`,color:absCfg.color,border:`1px solid ${absCfg.color}44`,borderRadius:3,padding:"1px 7px",fontSize:10,fontWeight:700}}>
                {absCfg.label}
              </span>}

              {!isAbs&&dayInfo.targetH>0&&(dayInfo.workH>0||dayInfo.bookedH>0||isTdy)===false&&(
                <span style={{color:"var(--text3)",fontSize:11,flex:1}}>{dayInfo.date>today()?"":"kein Eintrag"}</span>
              )}
              {!isAbs&&dayInfo.targetH===0&&(dayInfo.workH>0||dayInfo.bookedH>0||isTdy)===false&&(
                <span style={{color:"var(--text3)",fontSize:11,flex:1,opacity:0.5}}>Wochenende</span>
              )}
              {!isAbs&&(dayInfo.workH>0||dayInfo.bookedH>0||isTdy)&&<div style={{flex:1}}/>}

              <div style={{display:"flex",alignItems:"center",gap:10,flexShrink:0}}>
                {(dayInfo.workH>0||dayInfo.bookedH>0||isTdy)&&(()=>{
                  const dispSecs = isTdy ? bookedSecs : (dayInfo.workH||dayInfo.bookedH||0)*3600;
                  return (
                    <div style={{display:"flex",alignItems:"center",gap:6}}>
                      <span style={{fontWeight:600,fontSize:13,fontVariantNumeric:"tabular-nums"}}>{fmtH(dispSecs)}</span>
                    </div>
                  );
                })()}
                {/* Saldo dot – heute live, andere Tage server */}
                {dayInfo.targetH>0&&!dayInfo.isBeforeTracking&&!isAbs&&dayInfo.date>today()&&(
                  <span style={{fontSize:10,color:"var(--text3)"}}>Soll {fmtH(dayInfo.targetH*3600)}</span>
                )}
                {dayInfo.targetH>0&&!dayInfo.isBeforeTracking&&isTdy&&bookedSecs/3600<dayInfo.targetH&&(
                  <span style={{fontSize:10,color:"#b58100",fontWeight:600}}>noch {fmtH((dayInfo.targetH-bookedSecs/3600)*3600)}</span>
                )}
                {dayInfo.targetH>0&&!dayInfo.isBeforeTracking&&dayInfo.date<=today()&&!(isTdy&&bookedSecs/3600<dayInfo.targetH)&&(()=>{
                  const liveSaldo = isTdy ? (bookedSecs/3600) - dayInfo.targetH : dayInfo.balanceH;
                  const calm = liveSaldo<0 && weekDays.length>0 && (()=>{ const y=weekDays.filter(x=>x.date<today()).slice(-1)[0]; return !y || y.cumulativeBalance>=0; })();
                  if (calm) return <span style={{fontSize:10,color:"var(--text2)"}}>noch {Math.abs(liveSaldo).toFixed(1)}h offen</span>;
                  return (
                    <div style={{display:"flex",alignItems:"center",gap:3}}>
                      <div style={{width:6,height:6,borderRadius:"50%",background:liveSaldo>=0?"var(--green)":"var(--red)"}}/>
                      <span style={{fontSize:10,color:liveSaldo>=0?"var(--green)":"var(--red)",fontWeight:600}}>
                        {liveSaldo>=0?"+":""}{liveSaldo.toFixed(1)}h
                      </span>
                    </div>
                  );
                })()}
                {/* Expand indicator */}
                <span style={{color:"var(--text3)",display:"flex"}}>{isExp?<ChevronUp size={15}/>:<ChevronDown size={15}/>}</span>
              </div>
            </div>

            {/* Expanded day detail */}
            {isExp&&(
              <div style={{borderTop:"1px solid var(--line)",paddingTop:4,paddingBottom:2}}>
                <DayTimeline date={dayInfo.date} entries={cachedDay?.timeEntries||[]} pauses={cachedDay?.pauses||[]}/>

                {/* Time entries + logs */}
                {(cachedDay?.timeEntries||[]).length===0&&(cachedDay?.dailyLogs||[]).length===0&&!cachedDay&&(
                  <div style={{color:"var(--text3)",padding:"14px",textAlign:"center",fontSize:11}}>Lade…</div>
                )}
                {(cachedDay?.timeEntries||[]).length===0&&(cachedDay?.dailyLogs||[]).length===0&&cachedDay&&(
                  <div style={{color:"var(--text3)",padding:"14px",textAlign:"center",fontSize:11}}>Keine Einträge</div>
                )}

                {/* Tages-Saldo: noch X oder +X */}
                {(()=>{
                  const dayWorkH = isTdy
                    ? (bookedSecs/3600)
                    : (dayInfo.workH||dayInfo.bookedH||0);
                  const remaining = dayInfo.targetH - dayWorkH;
                  const over      = dayWorkH - dayInfo.targetH;
                  if (dayInfo.targetH <= 0 || dayInfo.isBeforeTracking) return null;
                  return (
                    <div style={{display:"flex",alignItems:"center",gap:12,padding:"2px 14px 8px"}}>
                      {isTdy ? (
                        remaining > 0
                          ? <span style={{color:"var(--text2)",fontSize:12}}>⏳ Noch <b style={{color:"var(--accent)"}}>{fmtH(remaining*3600)}</b> bis Soll</span>
                          : <span style={{color:"var(--green)",fontSize:12}}>✓ Soll erfüllt · <b>+{fmtH(over*3600)}</b> Überstunden</span>
                      ) : (
                        <span style={{fontSize:12,color:dayInfo.balanceH>=0?"var(--green)":acctPlus?"var(--text2)":"var(--red)",fontWeight:600}}>
                          {dayInfo.balanceH>=0?`+${dayInfo.balanceH.toFixed(1)}h`:acctPlus?"":`${dayInfo.balanceH.toFixed(1)}h`}
                          <span style={{color:"var(--text3)",fontWeight:400,marginLeft:6}}>
                            {dayInfo.balanceH>=0?"Überstunden":`noch ${Math.abs(dayInfo.balanceH).toFixed(1)}h offen`}
                          </span>
                        </span>
                      )}
                    </div>
                  );
                })()}

                {[
                  ...(cachedDay?.timeEntries||[]).map(e=>({...e,_k:"entry",_t:e.start_time})),
                  ...(cachedDay?.dailyLogs||[]).map(l=>({...l,_k:"log",_t:l.timestamp})),
                  ...(cachedDay?.pauses||[]).filter(p=>p.end_time).map(p=>({...p,_k:"pause",_t:p.start_time})),
                ].sort((a,b)=>a._t.localeCompare(b._t)).map(item => item._k==="entry" ? (
                  <div key={item.id} className="zp-entry" style={{"--c":projColor(item.project_id)}}>
                    <div className="info">
                      <div className="t">
                        {(item.jira_ticket||item.task_title) ? ticketLabel(item.jira_ticket,item.task_title) : (item.project_name||"(kein Projekt)")}
                        {(item.jira_ticket||item.task_title)&&item.project_name&&<span className="zp-tag" title={item.project_name} style={{"--c":projColor(item.project_id)}}>{projShort(projects,item.project_id,item.project_name)}</span>}
                        {item.source==="tempo"&&<span className="zp-tag" style={{"--c":"#4a6cf7"}}>Tempo</span>}
                        {(item.source==="plane"||(!item.plane_skip&&item.plane_sum_key&&item.plane_sum_key===(item.jira_ticket||"").trim().toUpperCase()&&item.plane_sum_secs===item.duration_seconds))&&<span className="zp-tag" style={{"--c":"#3651c9"}} title="In der Plane-Summe enthalten">Plane ✓</span>}
                      </div>
                      <div className="m">{fmtTime(item.start_time)} – {item.end_time?fmtTime(item.end_time):"läuft"}<ImpreciseMark e={item}/>{item.activity?` · ${item.activity}`:""}{item.comment?` · ${item.comment}`:""}</div>
                    </div>
                    <div className="act">
                      <button style={TV.iconBtn} onClick={()=>setEditEntry(item)} title="Bearbeiten"><Pencil size={13}/></button>
                      <button style={{...TV.iconBtn,color:"var(--red)"}} title="Löschen" onClick={async()=>{
                        if(item.source==="tempo"&&!confirm("Tempo-Eintrag löschen? Wird beim nächsten Import nicht erneut importiert."))return;
                        if(item.plane_worklog_id&&!confirm("Dieser Eintrag ist ein eigener Plane-Eintrag. Löschen? (Er wird mit der nächsten Plane-Freigabe auch dort entfernt.)"))return;
                        await api("DELETE",`/time-entries/${item.id}`);
                        setDayData(p=>{ const n={...p}; delete n[dayInfo.date]; return n; });
                        onRefresh();
                      }}><Trash2 size={13}/></button>
                    </div>
                    <span className="dur">{item.end_time?fmtH(item.duration_seconds||0):fmtDur(elapsed)}</span>
                  </div>
                ) : item._k==="pause" ? (
                  <PauseRow key={`pause-${item.id}`} pause={item} onRefresh={onRefresh}/>
                ) : (
                  <div key={item.id} className="zp-entry" style={{"--c":"var(--line)",background:"transparent"}}>
                    <span style={{color:"var(--text3)",fontSize:10,minWidth:90}}>{fmtTime(item.timestamp)}</span>
                    <FileText size={11} color="var(--text3)"/>
                    <span style={{color:"var(--text2)",fontSize:11,flex:1}}>{item.text}</span>
                    <LogEntryButtons log={item} onRefresh={()=>{setDayData(p=>{const n={...p};delete n[dayInfo.date];return n;});onRefresh();}}/>
                  </div>
                ))}

                {/* Manuell erfassen Button */}
                <div style={{padding:"6px 10px 8px",display:"flex",gap:8,alignItems:"center"}}>
                  <Btn size="sm" onClick={()=>{onDateChange(dayInfo.date);setManualDate(dayInfo.date);setManual(true);}}><Plus size={11}/> Eintrag erfassen</Btn>
                  <Btn size="sm" onClick={()=>{onDateChange(dayInfo.date);setPauseOpen(true);}}><Coffee size={11}/> Pause erfassen</Btn>
                  {cachedDay?.timeEntries?.length>0&&(
                    <span style={{color:"var(--text3)",fontSize:11,marginLeft:"auto"}}>
                      {fmtH((cachedDay.timeEntries||[]).reduce((s,e)=>s+(e.duration_seconds||0),0)+(timer.active?elapsed:0))} gebucht
                    </span>
                  )}
                </div>
              </div>
            )}
          </div>
        );
      })}

    </div>

    {/* ── RECHTS (große Fenster): Tagesüberblick + Planung ── */}
    <div className="zp-today-side" style={{width:360,flexShrink:0,display:"flex",flexDirection:"column",gap:12,position:"sticky",top:12}}>
      <DayPanel initialDate={expandedDay||viewDate} projects={projects} tick={Math.floor(bookedSecs/300)} onChanged={onRefresh}/>
      <div className="zp-plan-wrap"><TodayTodos onTimer={onRefresh}/></div>
    </div>

      {editEntry&&<TimeEntryModal entry={editEntry} projects={projects} onSave={()=>{setEditEntry(null);onRefresh();setDayData({});if(expandedDay&&expandedDay!==today())loadDay(expandedDay);}} onClose={()=>setEditEntry(null)}/>}
      {manualOpen&&<TimeEntryModal projects={projects} defaultDate={manualDate} onSave={()=>{setManual(false);onRefresh();setDayData({});if(expandedDay&&expandedDay!==today())loadDay(expandedDay);}} onClose={()=>setManual(false)}/>}
      {pauseOpen&&<PauseEntryModal date={viewDate} onSave={()=>{setPauseOpen(false);onRefresh();setDayData({});if(expandedDay&&expandedDay!==today())loadDay(expandedDay);}} onClose={()=>setPauseOpen(false)}/>}
    </div>
    </>
  );
}

const TV = {
  card:       { background:"var(--bg2)",border:"1px solid #1a2e1c",borderRadius:8,padding:"16px 20px",display:"flex",alignItems:"center",justifyContent:"space-between",gap:20 },
  cardLeft:   { flex:1 },
  badgeRow:   { display:"flex",alignItems:"center",gap:10,marginBottom:8 },
  projName:   { color:"var(--hi)",fontWeight:700,fontSize:15 },
  custChip:   { background:"rgba(144,162,67,0.15)",color:"var(--purple)",border:"1px solid rgba(144,162,67,0.3)",borderRadius:3,padding:"2px 8px",fontSize:11 },
  taskLine:   { display:"flex",alignItems:"center",gap:8,color:"var(--text2)",fontSize:12,marginBottom:5 },
  jiraChip:   { background:"rgba(129,187,170,0.15)",color:"var(--blue)",border:"1px solid rgba(129,187,170,0.3)",borderRadius:3,padding:"1px 7px",fontSize:11,fontWeight:700 },
  commentLine:{ color:"var(--text3)",fontSize:11,marginBottom:5 },
  meta:       { color:"var(--text3)",fontSize:11 },
  cardRight:  { textAlign:"right",flexShrink:0 },
  elapsed:    { fontSize:46,fontWeight:700,color:"var(--accent)",letterSpacing:"0.03em",fontVariantNumeric:"tabular-nums",lineHeight:1,marginBottom:10 },
  idle:       { background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:8,padding:"18px 20px",display:"flex",alignItems:"center",justifyContent:"space-between" },
  wdBar:      { background:"rgba(15,117,47,0.07)",border:"1px solid rgba(15,117,47,0.2)",borderRadius:6,padding:"10px 14px",display:"flex",alignItems:"center",justifyContent:"space-between",gap:10,flexWrap:"wrap" },
  sectionTitle:{ color:"var(--text3)",fontSize:10,letterSpacing:"0.12em",fontWeight:700,padding:"8px 0 6px",borderBottom:"1px solid var(--border)",marginBottom:4 },
  row:        { display:"flex",alignItems:"center",gap:10,padding:"8px 6px",borderBottom:"1px solid #080f09" },
  rowActive:  { background:"rgba(15,117,47,0.05)" },
  logRow:     { display:"flex",alignItems:"center",gap:10,padding:"7px 6px",borderBottom:"1px solid #080f09",background:"rgba(0,0,0,0.15)" },
  rowTime:    { color:"var(--text3)",fontSize:11,whiteSpace:"nowrap",minWidth:90 },
  rowContent: { flex:1,display:"flex",flexWrap:"wrap",alignItems:"center",gap:6 },
  rowProj:    { color:"var(--hi)",fontWeight:600 },
  rowCust:    { color:"var(--purple)",fontSize:11 },
  rowComment: { color:"var(--text3)",fontSize:11,width:"100%" },
  rowDur:     { color:"var(--text)",fontWeight:700,fontSize:12,minWidth:40,textAlign:"right",flexShrink:0 },
  iconBtn:    { background:"transparent",border:"none",color:"var(--text3)",cursor:"pointer",display:"flex",alignItems:"center",padding:3,borderRadius:3 },
};

/* ═══ PROJECT FORM ══════════════════════════════ */
function ProjectForm({ project, onSave, onClose }) {
  const [f, setF] = useState({ customer_id:project?.customer_id||"", name:project?.name||"", jira_key:project?.jira_key||"", status:project?.status||"active", budget_hours:project?.budget_hours||"", hourly_rate:project?.hourly_rate||"", description:project?.description||"" });
  const s = k => e => setF(v=>({...v,[k]:e.target.value}));
  const save = async () => {
    if(!f.name.trim()) return;
    const d = {...f, budget_hours:parseFloat(f.budget_hours)||0, hourly_rate:parseFloat(f.hourly_rate)||0, customer_id:f.customer_id||null};
    project?.id ? await api("PUT",`/projects/${project.id}`,d) : await api("POST","/projects",d);
    onSave();
  };
  return (
    <Modal title={project?"Projekt bearbeiten":"Neues Projekt"} onClose={onClose}>
      <div style={{padding:"14px 18px 18px"}}>
        <Field label="STATUS"><select style={sel} value={f.status} onChange={s("status")}>{Object.entries(STATUS_LBL).map(([k,v])=><option key={k} value={k}>{v}</option>)}</select></Field>
        <Field label="PROJEKTNAME *"><input style={inp} value={f.name} onChange={s("name")} placeholder="z.B. Security Audit 2026" autoFocus/></Field>
        <div style={{display:"grid",gridTemplateColumns:"1fr 1fr 1fr",gap:12}}>
          <Field label="JIRA-KÜRZEL"><input style={inp} value={f.jira_key} onChange={s("jira_key")} placeholder="PROJ"/></Field>
          <Field label="BUDGET (h)"><input style={inp} type="number" value={f.budget_hours} onChange={s("budget_hours")} placeholder="0"/></Field>
          <Field label="STUNDENSATZ (€)"><input style={inp} type="number" value={f.hourly_rate} onChange={s("hourly_rate")} placeholder="0"/></Field>
        </div>
        <Field label="BESCHREIBUNG"><textarea style={{...inp,resize:"vertical",minHeight:60}} value={f.description} onChange={s("description")}/></Field>
        <div style={{display:"flex",gap:8,justifyContent:"flex-end",marginTop:6}}>
          <Btn onClick={onClose}>Abbrechen</Btn>
          <Btn variant="primary" onClick={save} disabled={!f.name.trim()}><Check size={14}/> Speichern</Btn>
        </div>
      </div>
    </Modal>
  );
}

/* ═══ TASK FORM ═════════════════════════════════ */
function TaskForm({ task, projectId, onSave, onClose }) {
  const [f, setF] = useState({ title:task?.title||"", jira_ticket:task?.jira_ticket||"", status:task?.status||"open", estimated_hours:task?.estimated_hours||"", description:task?.description||"" });
  const s = k => e => setF(v=>({...v,[k]:e.target.value}));
  const save = async () => {
    if(!f.title.trim()) return;
    const d = {...f,project_id:projectId,estimated_hours:parseFloat(f.estimated_hours)||0};
    task?.id ? await api("PUT",`/tasks/${task.id}`,d) : await api("POST","/tasks",d);
    onSave();
  };
  return (
    <Modal title={task?"Aufgabe bearbeiten":"Neue Aufgabe"} onClose={onClose} width={440}>
      <div style={{padding:"14px 18px 18px"}}>
        <Field label="TITEL *"><input style={inp} value={f.title} onChange={s("title")} placeholder="z.B. Risk Assessment" autoFocus/></Field>
        <div style={{display:"grid",gridTemplateColumns:"1fr 1fr 1fr",gap:12}}>
          <Field label="JIRA-TICKET"><input style={{...inp,color:"var(--blue)",fontWeight:700}} value={f.jira_ticket} onChange={s("jira_ticket")} placeholder="PROJ-42"/></Field>
          <Field label="STATUS"><select style={sel} value={f.status} onChange={s("status")}>{Object.entries(TASK_LBL).map(([k,v])=><option key={k} value={k}>{v}</option>)}</select></Field>
          <Field label="SCHÄTZUNG (h)"><input style={inp} type="number" value={f.estimated_hours} onChange={s("estimated_hours")} placeholder="0"/></Field>
        </div>
        <Field label="BESCHREIBUNG"><textarea style={{...inp,resize:"vertical",minHeight:50}} value={f.description} onChange={s("description")}/></Field>
        <div style={{display:"flex",gap:8,justifyContent:"flex-end",marginTop:6}}>
          <Btn onClick={onClose}>Abbrechen</Btn>
          <Btn variant="primary" onClick={save} disabled={!f.title.trim()}><Check size={14}/> Speichern</Btn>
        </div>
      </div>
    </Modal>
  );
}

/* ═══ PROJECTS VIEW ═════════════════════════════ */
function ProjectsView({ onRefreshNeeded }) {
  const [projects,  setProjects]  = useState([]);
  const [expanded,  setExpanded]  = useState({});
  const [detail,    setDetail]    = useState(null);
  const [showProj,  setShowProj]  = useState(false);
  const [editProj,  setEditProj]  = useState(null);
  const allowLocal = useAllowLocal();
  const [showTask,  setShowTask]  = useState(null);
  const [editTask,  setEditTask]  = useState(null);
  const [filter,    setFilter]    = useState("active");

  const load = useCallback(async()=>{ const p=await api("GET",`/projects${filter&&filter!=="all"?"?status="+filter:""}`); setProjects(p||[]); },[filter]);
  useEffect(()=>{ load(); },[load]);

  const loadDetail = async pid => { const d=await api("GET",`/projects/${pid}`); setDetail(d); };
  const toggleExp  = async pid => { if(expanded[pid]){setExpanded(v=>({...v,[pid]:false}));return;} setExpanded(v=>({...v,[pid]:true})); await loadDetail(pid); };

  return (
    <div>
      <div style={{display:"flex",alignItems:"center",justifyContent:"space-between",marginBottom:16}}>
        <div style={{display:"flex",gap:6}}>
          {["active","paused","done","all"].map(s=>(
            <button key={s} style={{background:"transparent",border:"1px solid",borderColor:filter===s?"var(--accent)":"var(--border)",color:filter===s?"var(--accent)":"var(--text3)",borderRadius:4,padding:"4px 12px",fontSize:11,fontFamily:"inherit",cursor:"pointer"}}>
              <span onClick={()=>setFilter(s)}>{s==="all"?"Alle":STATUS_LBL[s]}</span>
            </button>
          ))}
        </div>
        {allowLocal ? <Btn variant="primary" onClick={()=>setShowProj(true)}><Plus size={14}/> Projekt</Btn>
          : <span style={{fontSize:12,color:"var(--text2)"}}>Neue Projekte kommen aus Plane/Jira (oben „Neue Projekte gefunden“)</span>}
      </div>

      {projects.length===0&&<div style={{color:"var(--text3)",textAlign:"center",padding:"40px 0"}}>Keine Projekte. Erstell dein erstes!</div>}

      {Object.entries(projects.reduce((a,p)=>{ const k=p.customer_name||"– kein Kunde –"; (a[k]=a[k]||[]).push(p); return a; },{})).map(([cust,list])=>(
        <div key={cust} style={{marginBottom:20}}>
          <div style={{color:"var(--text3)",fontSize:10,letterSpacing:"0.1em",fontWeight:700,padding:"6px 0 8px",borderBottom:"1px solid var(--border)",marginBottom:6}}>{cust}</div>
          {list.map(p=>(
            <div key={p.id} style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,marginBottom:6,overflow:"hidden"}}>
              <div style={{display:"flex",alignItems:"center",gap:12,padding:"11px 14px",cursor:"pointer"}} onClick={()=>toggleExp(p.id)}>
                <span style={{width:8,height:8,borderRadius:"50%",background:STATUS_COL[p.status],flexShrink:0}}/>
                <div style={{flex:1}}>
                  <div style={{display:"flex",alignItems:"center",gap:8}}>
                    <span style={{color:"var(--hi)",fontWeight:600}}>{p.name}</span>
                    {p.jira_key&&<span style={{color:"var(--text3)",fontSize:11}}>[{p.jira_key}]</span>}
                    <Badge label={STATUS_LBL[p.status]} color={STATUS_COL[p.status]}/>
                  </div>
                  {p.budget_hours>0&&<div style={{marginTop:5}}><ProgressBar value={p.booked_seconds/3600} max={p.budget_hours}/><div style={{color:"var(--text3)",fontSize:10,marginTop:2}}>{fmtHDec(p.booked_seconds)}/{p.budget_hours}h Budget</div></div>}
                </div>
                <div style={{display:"flex",alignItems:"center",gap:8,flexShrink:0}}>
                  <span style={{color:"var(--accent)",fontWeight:700,fontSize:13}}>{fmtH(p.booked_seconds||0)}</span>
                  {p.task_count>0&&<span style={{color:"var(--text3)",fontSize:11}}>{p.task_count} Aufg.</span>}
                  <button style={TV.iconBtn} onClick={e=>{e.stopPropagation();setEditProj(p);}}><Pencil size={13}/></button>
                  <button style={{...TV.iconBtn,color:"var(--red)"}} onClick={e=>{e.stopPropagation();if(confirm(`Projekt „${p.name}“ löschen?${p.booked_seconds?`\n\nEs hat ${fmtH(p.booked_seconds)} Buchungen – die bleiben erhalten, verlieren aber ihr Projekt. Besser: in Projekte › Quellen „abschließen“.`:""}`))api("DELETE",`/projects/${p.id}`).then(()=>{load();onRefreshNeeded();});}}><Trash2 size={13}/></button>
                  {expanded[p.id]?<ChevronUp size={13}/>:<ChevronDown size={13}/>}
                </div>
              </div>
              {expanded[p.id]&&detail?.id===p.id&&(
                <div style={{borderTop:"1px solid var(--border)"}}>
                  {!(detail.tasks?.length)&&<div style={{color:"var(--text3)",padding:"10px 14px",fontSize:12}}>Noch keine Aufgaben.</div>}
                  {(detail.tasks||[]).map(t=>(
                    <div key={t.id} style={{display:"flex",alignItems:"center",gap:10,padding:"9px 14px",borderBottom:"1px solid #080f09"}}>
                      <Badge label={TASK_LBL[t.status]} color={TASK_COL[t.status]}/>
                      {t.jira_ticket&&<span style={{color:"var(--blue)",fontWeight:700,fontSize:11}}>{t.jira_ticket}</span>}
                      <span style={{flex:1,color:"var(--text)"}}>{t.title}</span>
                      {t.estimated_hours>0&&<span style={{color:"var(--text3)",fontSize:11}}>{fmtH(t.booked_seconds||0)}/{t.estimated_hours}h</span>}
                      <button style={TV.iconBtn} onClick={()=>setEditTask({task:t,pid:p.id})}><Pencil size={13}/></button>
                      <button style={{...TV.iconBtn,color:"var(--red)"}} onClick={()=>{if(confirm("Aufgabe löschen?"))api("DELETE",`/tasks/${t.id}`).then(()=>loadDetail(p.id));}}><Trash2 size={13}/></button>
                    </div>
                  ))}
                  <div style={{padding:"8px 14px"}}>
                    <Btn size="sm" onClick={()=>setShowTask({pid:p.id,refresh:()=>loadDetail(p.id)})}><Plus size={12}/> Aufgabe</Btn>
                  </div>
                </div>
              )}
            </div>
          ))}
        </div>
      ))}
      {showProj&&<ProjectForm onClose={()=>setShowProj(false)} onSave={()=>{setShowProj(false);load();onRefreshNeeded();}}/>}
      {editProj&&<ProjectForm project={editProj} onClose={()=>setEditProj(null)} onSave={()=>{setEditProj(null);load();onRefreshNeeded();}}/>}
      {showTask&&<TaskForm projectId={showTask.pid} onClose={()=>setShowTask(null)} onSave={()=>{setShowTask(null);showTask.refresh();}}/>}
      {editTask&&<TaskForm task={editTask.task} projectId={editTask.pid} onClose={()=>setEditTask(null)} onSave={()=>{setEditTask(null);loadDetail(editTask.pid);}}/>}
    </div>
  );
}


/* ═══ REPORT VIEW ═══════════════════════════════ */
/* ═══ STUNDENZETTEL VIEW (Woche / Monat) v4.9.0 ═══ */
/* ═══ TAGES-EINTRÄGE BEARBEITEN (aus Stundenzettel) ═══════════
   Öffnet sich beim Klick auf einen Tag im Stundenzettel und erlaubt,
   die Zeiteinträge dieses Tages hinzuzufügen/zu bearbeiten/zu löschen –
   dieselbe Funktion, die im Mini-Fenster und in der Tagesansicht schon
   existiert, nur eben auch aus der Wochen-/Monatsansicht heraus. */
function DayEntriesModal({ date, projects, onClose, onChanged }) {
  const [entries, setEntries] = useState(null);
  const [form,    setForm]    = useState(null);   // null=zu, {}=neu, {…}=bearbeiten

  const load = () => api("GET", `/time-entries?from=${date}&to=${date}`).then(r => setEntries(r||[]));
  useEffect(() => { load(); }, [date]);

  const del = async id => {
    if (!confirm("Diesen Eintrag löschen?")) return;
    await api("DELETE", `/time-entries/${id}`);
    load(); onChanged();
  };

  const dd = new Date(date+"T12:00").toLocaleDateString("de-DE",{weekday:"long",day:"2-digit",month:"2-digit",year:"numeric"});

  return (
    <Modal title={`📅 ${dd}`} onClose={onClose} width={560}>
      <div style={{padding:"14px 18px 18px"}}>
        {entries===null && <div style={{color:"var(--text3)",padding:"12px 0"}}>Lade…</div>}
        {entries && entries.length===0 && <div style={{color:"var(--text3)",padding:"12px 0"}}>Noch keine Einträge an diesem Tag.</div>}
        {entries && entries.map(e => (
          <div key={e.id} style={{display:"flex",alignItems:"center",gap:10,padding:"9px 10px",marginBottom:6,borderRadius:6,background:"var(--bg2)",border:"1px solid var(--border)"}}>
            <div style={{flex:1,minWidth:0}}>
              <div style={{color:"var(--text)",fontWeight:600,fontSize:12,whiteSpace:"nowrap",overflow:"hidden",textOverflow:"ellipsis"}}>
                {e.project_name || "Ohne Projekt"}{e.jira_ticket ? ` · ${e.jira_ticket}` : ""}
              </div>
              <div style={{color:"var(--text3)",fontSize:11}}>{toLocal(e.start_time)}–{e.end_time?toLocal(e.end_time):"…"}<ImpreciseMark e={e}/>{e.comment ? " · "+e.comment : ""}</div>
            </div>
            <button style={TV.iconBtn} onClick={()=>setForm(e)} title="Bearbeiten"><Pencil size={12}/></button>
            <button style={{...TV.iconBtn,color:"var(--red)"}} onClick={()=>del(e.id)} title="Löschen"><Trash2 size={12}/></button>
          </div>
        ))}
        <div style={{display:"flex",justifyContent:"flex-end",marginTop:10}}>
          <Btn size="sm" variant="primary" onClick={()=>setForm({date})}><Plus size={13}/> Eintrag hinzufügen</Btn>
        </div>
      </div>
      {form && <TimeEntryModal entry={form.id?form:{date}} projects={projects} onSave={()=>{setForm(null);load();onChanged();}} onClose={()=>setForm(null)}/>}
    </Modal>
  );
}

function TimesheetView() {
  const ld = d => `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())}`;
  const monday = d => { const x = new Date(d); x.setHours(12); x.setDate(x.getDate() - ((x.getDay()+6)%7)); return x; };
  const [mode,   setMode]   = useState("woche");
  const [anchor, setAnchor] = useState(() => new Date());
  const [fmt,    setFmt]    = useState("dez");      // dez | hm
  const [data,   setData]   = useState(null);
  const [toast,  setToast]  = useState("");
  const [projects, setProjects] = useState([]);
  const [dayModal, setDayModal] = useState(null);   // Datum (yyyy-mm-dd) oder null
  useEffect(() => { api("GET","/projects").then(p=>setProjects((p||[]).filter(x=>x.status!=="archived"))); }, []);

  const range = (() => {
    if (mode === "woche") { const m = monday(anchor); const s = new Date(m); s.setDate(m.getDate()+6); return { from: ld(m), to: ld(s) }; }
    const f = new Date(anchor.getFullYear(), anchor.getMonth(), 1, 12), t = new Date(anchor.getFullYear(), anchor.getMonth()+1, 0, 12);
    return { from: ld(f), to: ld(t) };
  })();
  const refresh = () => api("GET", `/timesheet?from=${range.from}&to=${range.to}`).then(setData);
  useEffect(() => { setData(null); refresh(); }, [range.from, range.to]);

  const move = n => { const d = new Date(anchor); if (mode === "woche") d.setDate(d.getDate() + 7*n); else d.setMonth(d.getMonth() + n, 1); setAnchor(d); };
  const kw = d => { const x = new Date(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate())); const n = x.getUTCDay() || 7; x.setUTCDate(x.getUTCDate() + 4 - n); const y = new Date(Date.UTC(x.getUTCFullYear(),0,1)); return Math.ceil(((x - y) / 86400000 + 1) / 7); };
  const label = mode === "woche"
    ? `KW ${kw(new Date(range.from+"T12:00"))} · ${new Date(range.from+"T12:00").toLocaleDateString("de-DE",{day:"2-digit",month:"2-digit"})} – ${new Date(range.to+"T12:00").toLocaleDateString("de-DE",{day:"2-digit",month:"2-digit",year:"numeric"})}`
    : new Date(range.from+"T12:00").toLocaleDateString("de-DE",{month:"long",year:"numeric"});

  // Zahlenformat
  const num = (h, empty="") => { if (!h) return empty; if (fmt === "hm") { const m = Math.round(h*60); return `${Math.floor(m/60)}:${pad(m%60)}`; } return h.toFixed(2).replace(".", ","); };
  const sgn = h => (h > 0 ? "+" : h < 0 ? "−" : "") + num(Math.abs(h), fmt === "hm" ? "0:00" : "0,00");
  const hh  = s => s / 3600;
  const tm  = iso => iso ? new Date(iso).toLocaleTimeString("de-DE",{hour:"2-digit",minute:"2-digit"}) : "";
  const wd  = ["So","Mo","Di","Mi","Do","Fr","Sa"];
  const ABS = { urlaub:"Urlaub", krankheit:"Krank", feiertag:"Feiertag", fortbildung:"Fortbildung", sonstiges:"Abwesend" };

  const copy = async (text, what="Kopiert") => {
    try { await navigator.clipboard.writeText(text); }
    catch (_) { const t = document.createElement("textarea"); t.value = text; document.body.appendChild(t); t.select(); document.execCommand("copy"); t.remove(); }
    setToast(`${what}: ${text.length > 40 ? "Tabelle" : text}`); setTimeout(() => setToast(""), 1800);
  };

  // Tabellen als Zeilen (für Kopieren/CSV)
  const weekMatrix = () => {
    const days = data.days;
    const head = ["Projekt","Ticket", ...days.map(d => `${wd[d.dow]} ${d.date.slice(8,10)}.${d.date.slice(5,7)}.`), "Summe"];
    const body = data.rows.map(r => [r.project_key ? `${r.project_key} ${r.project}` : r.project, r.ticket, ...days.map(d => num(hh(r.days[d.date]||0))), num(hh(r.total_seconds))]);
    const foot = [
      ["Arbeitszeit","", ...days.map(d => num(hh(d.work_seconds))), num(hh(data.totals.work_seconds))],
      ["Beginn","", ...days.map(d => tm(d.first_start)), ""],
      ["Ende","", ...days.map(d => tm(d.last_end)), ""],
      ["Pause","", ...days.map(d => num(hh(d.pause_seconds))), num(hh(data.totals.pause_seconds))],
      ["Soll","", ...days.map(d => num(d.target_h)), num(data.totals.target_h)],
      ["+/−","", ...days.map(d => !d.future && (d.target_h || d.ist_h) ? sgn(d.balance_h) : ""), sgn(data.totals.balance_h)],
    ];
    return [head, ...body, ...foot];
  };
  const monthMatrix = () => [
    ["Datum","Tag","Beginn","Ende","Pause","Arbeitszeit","Soll","+/−","Abwesenheit","Tätigkeiten"],
    ...data.days.map(d => [d.date.split("-").reverse().join("."), wd[d.dow], tm(d.first_start), tm(d.last_end), num(hh(d.pause_seconds)), num(hh(d.work_seconds)), num(d.target_h), !d.future && (d.target_h || d.ist_h) ? sgn(d.balance_h) : "", d.absence ? ABS[d.absence.type] || d.absence.type : "", d.activities.map(a => `${a.label} ${num(hh(a.seconds))}`).join("; ")]),
    ["Summe","","","", num(hh(data.totals.pause_seconds)), num(hh(data.totals.work_seconds)), num(data.totals.target_h), sgn(data.totals.balance_h), "", ""],
  ];
  const matrix = () => mode === "woche" ? weekMatrix() : monthMatrix();
  const copyTable = () => copy(matrix().map(r => r.join("\t")).join("\n"), "Tabelle kopiert");
  const csv = () => {
    const c = matrix().map(r => r.map(x => `"${String(x).replace(/"/g,'""')}"`).join(";")).join("\n");
    Object.assign(document.createElement("a"), { href: "data:text/csv;charset=utf-8,﻿" + encodeURIComponent(c), download: `stundenzettel-${range.from}-${range.to}.csv` }).click();
  };

  const today = ld(new Date());
  const th = { padding:"8px 10px", color:"var(--text3)", fontSize:10, letterSpacing:"0.08em", fontWeight:600, textAlign:"right", whiteSpace:"nowrap", borderBottom:"1px solid var(--border)" };
  const td = { padding:"7px 10px", textAlign:"right", fontVariantNumeric:"tabular-nums", whiteSpace:"nowrap", borderBottom:"1px solid var(--border)" };
  const cell = (val, raw, title) => (
    <td style={{...td, cursor: val ? "copy" : "default", color: val ? "var(--text)" : "var(--text3)"}} title={title || (val ? "Klicken zum Kopieren" : "")}
        onClick={() => val && copy(String(raw ?? val))}>{val || "·"}</td>
  );

  return (
    <div style={{maxWidth:"none"}}>
      <div style={{display:"flex",alignItems:"center",gap:10,marginBottom:16,flexWrap:"wrap"}}>
        <div style={{display:"flex",border:"1px solid var(--border2)",borderRadius:6,overflow:"hidden"}}>
          {[["woche","Woche"],["monat","Monat"]].map(([k,l]) => (
            <button key={k} onClick={() => setMode(k)} style={{padding:"7px 16px",border:"none",fontFamily:"inherit",fontSize:12,fontWeight:600,cursor:"pointer",background: mode===k ? "rgba(15,117,47,0.15)" : "var(--bg2)", color: mode===k ? "var(--green)" : "var(--text2)"}}>{l}</button>
          ))}
        </div>
        <Btn size="sm" onClick={() => move(-1)}><ChevronLeft size={14}/></Btn>
        <div style={{color:"var(--hi)",fontWeight:700,fontSize:14,minWidth:230,textAlign:"center"}}>{label}</div>
        <Btn size="sm" onClick={() => move(1)}><ChevronRight size={14}/></Btn>
        <Btn size="sm" onClick={() => setAnchor(new Date())}>Heute</Btn>
        <div style={{flex:1}}/>
        <Btn size="sm" onClick={() => setDayModal(today >= range.from && today <= range.to ? today : range.from)}><Plus size={13}/> Eintrag</Btn>
        <Btn size="sm" onClick={() => setFmt(fmt === "dez" ? "hm" : "dez")}>{fmt === "dez" ? "Dezimal 1,50" : "Std:Min 1:30"}</Btn>
        <Btn size="sm" variant="primary" onClick={() => data && copyTable()}><FileText size={13}/> Tabelle kopieren</Btn>
        <Btn size="sm" onClick={() => data && csv()}><Download size={13}/> CSV</Btn>
      </div>

      {toast && <div style={{position:"fixed",bottom:24,left:"50%",transform:"translateX(-50%)",background:"var(--honi-green)",color:"#fff",padding:"8px 16px",borderRadius:6,fontSize:12,zIndex:50}}>{toast}</div>}
      {!data && <div style={{color:"var(--text3)",padding:"20px 0"}}>Lade…</div>}

      {data && (
        <>
          <div style={{display:"flex",gap:24,background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"12px 16px",marginBottom:16,flexWrap:"wrap"}}>
            {[["ARBEITSZEIT",num(hh(data.totals.work_seconds),"0"),"var(--accent)"],["SOLL",num(data.totals.target_h,"0"),"var(--hi)"],["+/− BIS HEUTE",sgn(data.totals.balance_h), data.totals.balance_h < 0 ? "var(--red)" : "var(--green)"],["PAUSEN",num(hh(data.totals.pause_seconds),"0"),"var(--text2)"],["PROJEKTE",String(data.rows.length),"var(--hi)"]].map(([l,v,c]) => (
              <div key={l}><div style={{color:"var(--text3)",fontSize:10,letterSpacing:"0.1em"}}>{l}</div><div style={{color:c,fontWeight:700,fontSize:18}}>{v}</div></div>
            ))}
          </div>

          <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,overflow:"auto"}}>
            {mode === "woche" ? (
              <table style={{width:"100%",borderCollapse:"collapse",fontSize:12}}>
                <thead><tr>
                  <th style={{...th,textAlign:"left"}}>PROJEKT / TICKET</th>
                  {data.days.map(d => <th key={d.date} style={{...th, cursor:"pointer", color: d.date===today ? "var(--green)" : d.dow===0||d.dow===6 ? "var(--text3)" : "var(--text2)"}} title="Einträge dieses Tages bearbeiten" onClick={()=>setDayModal(d.date)}>{wd[d.dow].toUpperCase()}<br/><span style={{fontWeight:400}}>{d.date.slice(8,10)}.{d.date.slice(5,7)}.</span>{d.absence && <div style={{color:"var(--accent)",fontWeight:600}}>{ABS[d.absence.type]||""}</div>}</th>)}
                  <th style={th}>SUMME</th>
                </tr></thead>
                <tbody>
                  {data.rows.length === 0 && <tr><td colSpan={9} style={{...td,textAlign:"center",color:"var(--text3)",padding:"24px"}}>Keine Buchungen in dieser Woche.</td></tr>}
                  {data.rows.map(r => (
                    <tr key={r.key}>
                      <td style={{...td,textAlign:"left",whiteSpace:"normal",minWidth:200}}>
                        <div style={{color:"var(--text)",fontWeight:600}}>{r.project}</div>
                        {(r.ticket || r.project_key) && <div style={{color:"var(--text3)",fontSize:11,cursor:"copy"}} title="Ticket kopieren" onClick={() => copy(r.ticket || r.project_key)}>{r.ticket || r.project_key}</div>}
                      </td>
                      {data.days.map(d => cell(num(hh(r.days[d.date]||0)), null, (r.comments[d.date]||[]).join("\n") || undefined))}
                      <td style={{...td,fontWeight:700,color:"var(--hi)",cursor:"copy"}} onClick={() => copy(num(hh(r.total_seconds)))}>{num(hh(r.total_seconds))}</td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  {[
                    ["Arbeitszeit", d => num(hh(d.work_seconds)), num(hh(data.totals.work_seconds)), true],
                    ["Beginn",      d => tm(d.first_start), ""],
                    ["Ende",        d => tm(d.last_end), ""],
                    ["Pause",       d => num(hh(d.pause_seconds)), num(hh(data.totals.pause_seconds))],
                    ["Soll",        d => num(d.target_h), num(data.totals.target_h)],
                    ["+/−",         d => !d.future && (d.target_h || d.ist_h) ? sgn(d.balance_h) : "", sgn(data.totals.balance_h), true],
                  ].map(([l, f, total, bold]) => (
                    <tr key={l} style={{background: bold ? "rgba(15,117,47,0.06)" : "transparent"}}>
                      <td style={{...td,textAlign:"left",color:"var(--text2)",fontWeight: bold ? 700 : 400}}>{l}</td>
                      {data.days.map(d => { const v = f(d); return <td key={d.date} style={{...td, fontWeight: bold ? 700 : 400, cursor: v ? "copy" : "default", color: l==="+/−" ? (d.balance_h < 0 ? "var(--red)" : "var(--green)") : "var(--text)"}} onClick={() => v && copy(v)}>{v || ""}</td>; })}
                      <td style={{...td,fontWeight:700,color: l==="+/−" ? (data.totals.balance_h < 0 ? "var(--red)" : "var(--green)") : "var(--hi)"}}>{total}</td>
                    </tr>
                  ))}
                </tfoot>
              </table>
            ) : (
              <table style={{width:"100%",borderCollapse:"collapse",fontSize:12}}>
                <thead><tr>
                  {["DATUM","BEGINN","ENDE","PAUSE","ARBEIT","SOLL","+/−"].map((h,i) => <th key={h} style={{...th, textAlign: i===0 ? "left" : "right"}}>{h}</th>)}
                  <th style={{...th,textAlign:"left"}}>TÄTIGKEITEN</th>
                </tr></thead>
                <tbody>
                  {data.days.map(d => {
                    const we = d.dow===0 || d.dow===6;
                    return (
                      <tr key={d.date} style={{background: d.date===today ? "rgba(15,117,47,0.08)" : we ? "rgba(0,0,0,0.02)" : "transparent", opacity: we && !d.work_seconds ? .55 : 1}}>
                        <td style={{...td,textAlign:"left",color:"var(--text)",cursor:"pointer"}} title="Einträge dieses Tages bearbeiten" onClick={()=>setDayModal(d.date)}>
                          <Pencil size={10} style={{marginRight:5,verticalAlign:-1,color:"var(--text3)"}}/><b>{wd[d.dow]}</b> {d.date.slice(8,10)}.{d.date.slice(5,7)}.{d.absence && <span style={{marginLeft:8,color:"var(--accent)",fontSize:11,fontWeight:600}}>{ABS[d.absence.type]||d.absence.type}</span>}
                        </td>
                        {cell(tm(d.first_start))}
                        {cell(tm(d.last_end))}
                        {cell(num(hh(d.pause_seconds)))}
                        <td style={{...td,fontWeight:700,color:"var(--hi)",cursor: d.work_seconds ? "copy" : "default"}} onClick={() => d.work_seconds && copy(num(hh(d.work_seconds)))}>{num(hh(d.work_seconds)) || "·"}</td>
                        {cell(num(d.target_h))}
                        <td style={{...td,color: d.balance_h < 0 ? "var(--red)" : "var(--green)"}}>{!d.future && (d.target_h || d.ist_h) ? sgn(d.balance_h) : ""}</td>
                        <td style={{...td,textAlign:"left",whiteSpace:"normal",color:"var(--text2)",fontSize:11}}>{d.activities.map(a => <span key={a.label} style={{display:"inline-block",marginRight:10}}>{a.label} <b style={{color:"var(--text)"}}>{num(hh(a.seconds))}</b></span>)}</td>
                      </tr>
                    );
                  })}
                </tbody>
                <tfoot><tr style={{background:"rgba(15,117,47,0.06)"}}>
                  <td style={{...td,textAlign:"left",fontWeight:700}}>Summe</td><td style={td}/><td style={td}/>
                  <td style={{...td,fontWeight:700}}>{num(hh(data.totals.pause_seconds))}</td>
                  <td style={{...td,fontWeight:700,color:"var(--hi)"}}>{num(hh(data.totals.work_seconds))}</td>
                  <td style={{...td,fontWeight:700}}>{num(data.totals.target_h)}</td>
                  <td style={{...td,fontWeight:700,color: data.totals.balance_h < 0 ? "var(--red)" : "var(--green)"}}>{sgn(data.totals.balance_h)}</td>
                  <td style={td}/>
                </tr></tfoot>
              </table>
            )}
          </div>
          <div style={{color:"var(--text3)",fontSize:11,marginTop:8}}>Tipp: Auf eine Zahl klicken kopiert sie · auf ein Datum klicken bearbeitet die Einträge dieses Tages · Mauszeiger über einer Zelle zeigt die Tätigkeiten · „Tabelle kopieren“ lässt sich direkt in Excel einfügen.</div>
        </>
      )}
      {dayModal && <DayEntriesModal date={dayModal} projects={projects} onClose={()=>setDayModal(null)} onChanged={refresh}/>}
    </div>
  );
}

/* ═══ SSH-SITZUNGEN VIEW (v4.8.0) ══════════════ */
function SshView() {
  const [from,     setFrom]     = useState(monthFrom());
  const [to,       setTo]       = useState(today());
  const [data,     setData]     = useState(null);
  const [loading,  setLoading]  = useState(false);
  const [projects, setProjects] = useState([]);
  const [tasks,    setTasks]    = useState({});      // projectId → [tasks]
  const [selected, setSelected] = useState({});      // sessionId → true
  const [msg,      setMsg]      = useState("");
  const [onlyOpen, setOnlyOpen] = useState(false);   // nur ohne Projekt
  const [tracking, setTracking] = useState(null);     // {enabled} - Ein/Aus-Schalter
  const loadTracking = () => api("GET","/ssh-tracking").then(setTracking);
  const toggleTracking = () => api("POST","/ssh-tracking",{enabled: !tracking?.enabled}).then(setTracking);

  const load = async () => {
    setLoading(true);
    const d = await api("GET", `/ssh-sessions?from=${from}&to=${to}`);
    setData(d); setLoading(false);
    const pids = [...new Set((d?.sessions||[]).map(s=>s.project_id).filter(Boolean))];
    pids.forEach(loadTasks);
  };
  const loadTasks = async pid => {
    if (!pid || tasks[pid]) return;
    const t = await api("GET", `/tasks?projectId=${pid}`);
    setTasks(v => ({...v, [pid]: t||[]}));
  };
  useEffect(()=>{ load(); loadTracking(); api("GET","/projects").then(p=>setProjects((p||[]).filter(x=>x.status!=="archived"))); },[]);
  useEffect(()=>{ const t=setInterval(load, 60000); return ()=>clearInterval(t); },[from,to]);

  const update = async (id, body) => {
    await api("PUT", `/ssh-sessions/${id}`, body);
    if (body.project_id) loadTasks(body.project_id);
    load();
  };
  const toEntries = async (ids, merge) => {
    const r = await call("POST", "/ssh-sessions/to-entries", { ids, merge: !!merge });
    setMsg(r.error ? r.error : `${r.created.length} Zeiteintrag/-einträge angelegt${merge?" (zusammengefasst)":""}${r.skipped.length?`, ${r.skipped.length} übersprungen (offen, schon gebucht oder im Timer enthalten)`:""}.`);
    setSelected({}); load();
    setTimeout(()=>setMsg(""), 6000);
  };
  const exportCSV = () => {
    const rows = [["Datum","Start","Ende","Dauer (min)","Server","Ziel","User","Port","Befehl","Projekt","Ticket","Notiz","Status"],
      ...(data?.sessions||[]).map(s=>[s.date,fmtTime(s.start_time),fmtTime(s.end_time),Math.round(s.effective_seconds/60),s.server,s.target,s.username||"",s.port||"",s.remote_cmd||"",s.project_name||"",s.task_ticket||s.task_title||"",s.note||"",s.status])];
    const csv = rows.map(r=>r.map(c=>`"${String(c).replace(/"/g,'""')}"`).join(";")).join("\n");
    Object.assign(document.createElement("a"),{href:"data:text/csv;charset=utf-8,﻿"+encodeURIComponent(csv),download:`ssh-sitzungen-${from}-${to}.csv`}).click();
  };

  const sessions = (data?.sessions||[]).filter(s => !onlyOpen || !s.project_id);
  const byDay = sessions.reduce((a,s)=>{ (a[s.date] ||= []).push(s); return a; }, {});
  const selIds = Object.keys(selected).filter(k=>selected[k]).map(Number);
  const unassigned = (data?.sessions||[]).filter(s=>!s.project_id).length;

  const statusBadge = s => {
    if (s.status==="open")    return <span style={{color:"var(--accent)",fontSize:10,fontWeight:700}}>● LÄUFT</span>;
    if (s.status==="unknown") return <span style={{color:"var(--text3)",fontSize:10}} title="Kein Ende protokolliert">Ende unbekannt</span>;
    if (s.time_entry_id)      return <span style={{color:"var(--green)",fontSize:10}}>✓ gebucht</span>;
    if (s.assign_source==="timer") return <span style={{color:"var(--green)",fontSize:10}} title="Lief während des Timers – Zeit ist dort schon enthalten">im Timer enthalten</span>;
    return null;
  };

  return (
    <div>
      <div style={{display:"flex",alignItems:"flex-end",gap:12,marginBottom:16,flexWrap:"wrap"}}>
        <Field label="VON"><input style={{...inp,width:140}} type="date" value={from} onChange={e=>setFrom(e.target.value)}/></Field>
        <Field label="BIS"><input style={{...inp,width:140}} type="date" value={to} onChange={e=>setTo(e.target.value)}/></Field>
        <Btn onClick={load} style={{marginBottom:12}}>Anzeigen</Btn>
        {data?.sessions?.length>0&&<Btn onClick={exportCSV} style={{marginBottom:12}}><Download size={14}/> CSV</Btn>}
        <label style={{display:"flex",alignItems:"center",gap:6,color:"var(--text2)",fontSize:12,marginBottom:20,cursor:"pointer"}}>
          <input type="checkbox" checked={onlyOpen} onChange={e=>setOnlyOpen(e.target.checked)}/> nur ohne Projekt
        </label>
        <div style={{flex:1}}/>
        {tracking&&(
          <button onClick={toggleTracking} title="SSH-Sitzungen werden automatisch als Zeit erfasst - hier ausschaltbar"
            style={{display:"flex",alignItems:"center",gap:8,marginBottom:20,background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:20,padding:"6px 12px 6px 6px",cursor:"pointer",fontFamily:"inherit"}}>
            <span style={{width:32,height:18,borderRadius:9,background:tracking.enabled?"var(--honi-green)":"var(--border2)",position:"relative",transition:"background .15s",flexShrink:0}}>
              <span style={{position:"absolute",top:2,left:tracking.enabled?16:2,width:14,height:14,borderRadius:"50%",background:"#fff",transition:"left .15s"}}/>
            </span>
            <span style={{color:"var(--text2)",fontSize:12}}>SSH-Zeiterfassung {tracking.enabled?"an":"aus"}</span>
          </button>
        )}
      </div>

      {data && !data.log_exists && (
        <div style={{background:"rgba(180,210,41,0.08)",border:"1px solid rgba(180,210,41,0.3)",borderRadius:6,padding:"12px 16px",marginBottom:16,color:"var(--text2)",fontSize:12,lineHeight:1.6}}>
          <b style={{color:"var(--accent)"}}>SSH-Protokoll noch nicht aktiv.</b> Einmal im Terminal ausführen, dann neues Terminal-Fenster öffnen:<br/>
          <code style={{color:"var(--hi)"}}>echo 'source ~/zeitprotokoll/scripts/ssh-log.zsh' &gt;&gt; ~/.zshrc</code>
        </div>
      )}

      {loading&&!data&&<div style={{color:"var(--text3)",padding:"20px 0"}}>Lade…</div>}

      {data&&(
        <>
          <div style={{display:"flex",gap:24,background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"12px 16px",marginBottom:16,flexWrap:"wrap"}}>
            {[["SSH-ZEIT",fmtH(data.total_seconds),"var(--accent)"],["SITZUNGEN",String(data.sessions.length),"var(--hi)"],["SERVER",String(data.byHost.length),"var(--hi)"],["OHNE PROJEKT",String(unassigned),unassigned?"var(--red)":"var(--green)"]].map(([l,v,c])=>(
              <div key={l}><div style={{color:"var(--text3)",fontSize:10,letterSpacing:"0.1em"}}>{l}</div><div style={{color:c,fontWeight:700,fontSize:18}}>{v}</div></div>
            ))}
          </div>

          {data.byHost.length>0&&(
            <div style={{display:"grid",gridTemplateColumns:"1fr 1fr",gap:12,marginBottom:16}}>
              {[["NACH SERVER",data.byHost],["NACH PROJEKT",data.byProject||[]]].map(([title,list])=>(
                <div key={title} style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"10px 14px"}}>
                  <div style={{...LBL,marginBottom:8}}>{title}</div>
                  {list.slice(0,10).map(h=>(
                    <div key={h.key} style={{display:"flex",justifyContent:"space-between",gap:8,padding:"3px 0",fontSize:12}}>
                      <span style={{color:"var(--text)",overflow:"hidden",textOverflow:"ellipsis",whiteSpace:"nowrap"}}>{h.key}</span>
                      <span style={{color:"var(--text2)",whiteSpace:"nowrap"}}>{h.count}× · {fmtH(h.seconds)}</span>
                    </div>
                  ))}
                </div>
              ))}
            </div>
          )}

          {selIds.length>0&&(
            <div style={{display:"flex",alignItems:"center",gap:12,marginBottom:12}}>
              <span style={{color:"var(--text2)",fontSize:12}}>{selIds.length} ausgewählt</span>
              <Btn size="sm" variant="primary" onClick={()=>toEntries(selIds)}><Plus size={12}/> Als Zeiteinträge übernehmen</Btn>
              {selIds.length>1&&<Btn size="sm" onClick={()=>toEntries(selIds,true)} title="Mehrere kurze Sitzungen (z. B. nur kurz auf einem anderen Server nachgeschaut) als EINE Buchung je Tag">Zu einer Buchung zusammenfassen</Btn>}
              <Btn size="sm" onClick={()=>setSelected({})}>Auswahl aufheben</Btn>
            </div>
          )}
          {msg&&<div style={{color:"var(--green)",fontSize:12,marginBottom:12}}>{msg}</div>}

          {Object.keys(byDay).length===0&&<div style={{color:"var(--text3)",textAlign:"center",padding:"30px 0"}}>Keine SSH-Sitzungen im Zeitraum.</div>}
          {Object.entries(byDay).sort((a,b)=>a[0]<b[0]?1:-1).map(([day,list])=>{
            const sum = list.reduce((s,x)=>s+x.effective_seconds,0);
            return (
              <div key={day} style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,marginBottom:10,overflow:"hidden"}}>
                <div style={{display:"flex",justifyContent:"space-between",padding:"10px 16px",borderBottom:"1px solid var(--border)"}}>
                  <span style={{color:"var(--hi)",fontWeight:700}}>{fmtDate(day)}</span>
                  <span style={{color:"var(--text2)",fontSize:12}}>{list.length} Sitzungen · {fmtH(sum)} · {fmtTime(list[0].start_time)}–{fmtTime(list[list.length-1].end_time||list[list.length-1].start_time)}</span>
                </div>
                {list.map(s=>{
                  const bookable = s.status==="closed" && !s.time_entry_id && s.assign_source!=="timer";
                  return (
                    <div key={s.id} style={{display:"flex",alignItems:"center",gap:10,padding:"8px 16px",borderBottom:"1px solid var(--border)",flexWrap:"wrap"}}>
                      <input type="checkbox" disabled={!bookable} checked={!!selected[s.id]} onChange={e=>setSelected(v=>({...v,[s.id]:e.target.checked}))}/>
                      <span style={{color:"var(--text2)",fontVariantNumeric:"tabular-nums",minWidth:92,fontSize:12}}>{fmtTime(s.start_time)}–{s.end_time?fmtTime(s.end_time):"…"}</span>
                      <span style={{color:"var(--text2)",minWidth:52,textAlign:"right",fontSize:12}} title={s.measured?`Aktive Zeit (Eingaben am Terminal). Fenster war ${fmtH(s.window_seconds)} offen.`:"Fensterzeit (keine Aktivität gemessen)"}>{fmtH(s.effective_seconds)}</span>
                      {s.measured&&s.window_seconds-s.effective_seconds>120&&<span style={{color:"var(--text3)",fontSize:10,whiteSpace:"nowrap"}} title="Zeit ohne Eingabe wird nicht gezählt">von {fmtH(s.window_seconds)}</span>}
                      {s.end_reason&&<span className="zp-tag" style={{"--c":s.end_reason==="vergessen"?"#8a6400":"#8e8e93"}} title={s.end_reason==="vergessen"?"Fenster blieb lange ohne Eingabe offen – Sitzung endet bei der letzten Eingabe":"Prozess war ohne Ende-Zeile verschwunden – Sitzung endet beim letzten Lebenszeichen"}>{s.end_reason}</span>}
                      <div style={{flex:"1 1 180px",minWidth:0}}>
                        <div style={{color:"var(--text)",overflow:"hidden",textOverflow:"ellipsis",whiteSpace:"nowrap"}} title={`${s.username||""}@${s.hostname||s.target}:${s.port||22}`}>{s.server}</div>
                        {s.remote_cmd&&<div style={{color:"var(--text3)",fontSize:10,fontFamily:"monospace",overflow:"hidden",textOverflow:"ellipsis",whiteSpace:"nowrap"}}>$ {s.remote_cmd}</div>}
                      </div>
                      <select style={{...sel,width:170,padding:"4px 8px",fontSize:11}} value={s.project_id||""} disabled={!!s.time_entry_id}
                              onChange={e=>update(s.id,{project_id:e.target.value||null, task_id:null})}>
                        <option value="">– Projekt –</option>
                        {projects.map(p=><option key={p.id} value={p.id}>{p.jira_key?`${p.jira_key} · `:""}{p.name}</option>)}
                      </select>
                      {s.project_id&&(tasks[s.project_id]||[]).length>0&&(
                        <select style={{...sel,width:140,padding:"4px 8px",fontSize:11}} value={s.task_id||""} disabled={!!s.time_entry_id}
                                onChange={e=>update(s.id,{task_id:e.target.value||null})}>
                          <option value="">– Ticket –</option>
                          {tasks[s.project_id].map(t=><option key={t.id} value={t.id}>{t.jira_ticket?`${t.jira_ticket} · `:""}{t.title}</option>)}
                        </select>
                      )}
                      <input style={{...inp,width:150,padding:"4px 8px",fontSize:11}} placeholder="Notiz…" defaultValue={s.note||""}
                             onBlur={e=>{ if((e.target.value||"")!==(s.note||"")) update(s.id,{note:e.target.value}); }}/>
                      <div style={{minWidth:110,textAlign:"right"}}>
                        {statusBadge(s) || (bookable && <Btn size="sm" variant="primary" onClick={()=>toEntries([s.id])} style={{padding:"3px 8px"}}><Plus size={11}/> Buchen</Btn>)}
                      </div>
                    </div>
                  );
                })}
              </div>
            );
          })}
        </>
      )}
    </div>
  );
}

function ReportView() {
  const [from,    setFrom]    = useState(monthFrom());
  const [to,      setTo]      = useState(today());
  const [data,    setData]    = useState(null);
  const [loading, setLoading] = useState(false);

  const load = async () => { setLoading(true); const d=await api("GET",`/report?from=${from}&to=${to}`); setData(d); setLoading(false); };
  useEffect(()=>{ load(); },[]);

  const byCustomer = (data?.summary||[]).reduce((a,r)=>{
    const k=r.customer_name||"– kein Kunde –";
    if(!a[k]) a[k]={total:0,billable:0,rows:[]};
    a[k].total+=r.total_seconds; a[k].billable+=r.billable_seconds; a[k].rows.push(r); return a;
  },{});
  const grandTotal    = Object.values(byCustomer).reduce((s,v)=>s+v.total,0);
  const grandBillable = Object.values(byCustomer).reduce((s,v)=>s+v.billable,0);
  const grandRevenue  = (data?.summary||[]).reduce((s,r)=>s+(r.billable_seconds/3600*(r.hourly_rate||0)),0);

  const exportCSV = () => {
    if(!data) return;
    const rows = [
      ["Datum","Kunde","Projekt","Aufgabe","Jira","Kommentar","Start","Ende","Dauer (h)","Abrechenbar","Betrag (€)"],
      ...(data.entries||[]).map(e=>[e.date,e.customer_name||"",e.project_name||"",e.task_title||"",e.jira_ticket||"",e.comment||"",fmtTime(e.start_time),fmtTime(e.end_time),((e.duration_seconds||0)/3600).toFixed(2),e.billable?"Ja":"Nein",e.billable?((e.duration_seconds/3600)*(e.hourly_rate||0)).toFixed(2):"0"])
    ];
    const csv=rows.map(r=>r.map(c=>`"${String(c).replace(/"/g,'""')}"`).join(";")).join("\n");
    Object.assign(document.createElement("a"),{href:"data:text/csv;charset=utf-8,\uFEFF"+encodeURIComponent(csv),download:`example-zeitprotokoll-${from}-${to}.csv`}).click();
  };

  return (
    <div>
      <div style={{display:"flex",alignItems:"flex-end",gap:12,marginBottom:20,flexWrap:"wrap"}}>
        <Field label="VON"><input style={{...inp,width:140}} type="date" value={from} onChange={e=>setFrom(e.target.value)}/></Field>
        <Field label="BIS"><input style={{...inp,width:140}} type="date" value={to} onChange={e=>setTo(e.target.value)}/></Field>
        <Btn onClick={load} style={{marginBottom:12}}>Auswerten</Btn>
        {data&&<Btn onClick={exportCSV} style={{marginBottom:12}}><Download size={14}/> CSV Export</Btn>}
      </div>

      {loading&&<div style={{color:"var(--text3)",padding:"20px 0"}}>Lade…</div>}

      {data&&!loading&&(
        <>
          <div style={{display:"flex",gap:20,background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"12px 16px",marginBottom:16,flexWrap:"wrap"}}>
            {[["GESAMT",fmtHDec(grandTotal),"var(--accent)"],["ABRECHENBAR",fmtHDec(grandBillable),"var(--green)"],["UMSATZ",euro(grandRevenue),"var(--hi)"]].map(([l,v,c])=>(
              <div key={l}><div style={{color:"var(--text3)",fontSize:10,letterSpacing:"0.1em"}}>{l}</div><div style={{color:c,fontWeight:700,fontSize:18}}>{v}</div></div>
            ))}
          </div>
          {Object.keys(byCustomer).length===0&&<div style={{color:"var(--text3)",textAlign:"center",padding:"30px 0"}}>Keine Buchungen im Zeitraum.</div>}
          {Object.entries(byCustomer).map(([cust,cv])=>(
            <div key={cust} style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,marginBottom:10,overflow:"hidden"}}>
              <div style={{display:"flex",alignItems:"center",justifyContent:"space-between",padding:"12px 16px",borderBottom:"1px solid var(--border)"}}>
                <span style={{color:"var(--hi)",fontWeight:700}}>{cust}</span>
                <div style={{display:"flex",gap:16}}>
                  <span style={{color:"var(--text2)",fontSize:12}}>∑ {fmtHDec(cv.total)}</span>
                  <span style={{color:"var(--green)",fontWeight:700}}>{fmtHDec(cv.billable)} abrechenbar</span>
                </div>
              </div>
              {cv.rows.map(r=>(
                <div key={r.project_id} style={{display:"flex",alignItems:"center",gap:14,padding:"9px 16px",borderBottom:"1px solid #080f09"}}>
                  <div style={{flex:1}}>
                    <div style={{color:"var(--text)"}}>{r.project_name}</div>
                    {r.jira_key&&<span style={{color:"var(--text3)",fontSize:11}}>{r.jira_key}</span>}
                  </div>
                  {r.budget_hours>0&&<div style={{minWidth:130}}><ProgressBar value={r.total_seconds/3600} max={r.budget_hours}/><div style={{color:"var(--text3)",fontSize:10,marginTop:2}}>{fmtHDec(r.total_seconds)}/{r.budget_hours}h</div></div>}
                  <span style={{color:"var(--text2)",minWidth:50,textAlign:"right"}}>{fmtHDec(r.total_seconds)}</span>
                  {r.hourly_rate>0&&<span style={{color:"var(--green)",minWidth:70,textAlign:"right"}}>{euro((r.billable_seconds/3600)*r.hourly_rate)}</span>}
                </div>
              ))}
            </div>
          ))}
        </>
      )}
    </div>
  );
}

/* ═══ MAIN APP ══════════════════════════════════ */
export default function App() {
  const [pvKey,     setPvKey]     = useState(0);
  const [view,      setView]      = useState(() => (typeof location!=="undefined" && location.hash.slice(1).split("/")[0]) || "timer");
  const [hashKey, setHashKey] = useState(0);
  useEffect(()=>{ const h=()=>{ if(location.hash.slice(1)) { setView(location.hash.slice(1).split("/")[0]); setHashKey(k=>k+1); } }; window.addEventListener("hashchange",h); return ()=>window.removeEventListener("hashchange",h); },[]);
  const [timer,     setTimer]     = useState({active:false});
  const [elapsed,   setElapsed]   = useState(0);
  const [pauseState,setPauseState]= useState({active:false});
  const [pauseElapsed,setPauseElapsed] = useState(0);
  const [workday,   setWorkday]   = useState(null);
  const [todayData, setTodayData] = useState({timeEntries:[],dailyLogs:[],pauses:[]});
  const [projects,  setProjects]  = useState([]);
  const [picker,    setPicker]    = useState(null);
  const [version,   setVersion]   = useState("");
  useEffect(()=>{ api("GET","/health").then(h=>h?.version&&setVersion(h.version)); },[]);
  // Neue Version auf dem Server (nach Update) → Seite neu laden, sobald gerade nichts getippt wird (alte Tabs im Browser)
  useEffect(()=>{ if(!version) return; const t=setInterval(async()=>{ const h=await api("GET","/health"); if(!h?.version||h.version===version) return;
    const busy=document.activeElement && /INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName);
    if(!busy) location.reload(); },60000); return ()=>clearInterval(t); },[version]);
  const timerRef = useRef(null);

  const [viewDate,    setViewDate]    = useState(today());
  const [overview,    setOverview]    = useState(null);

  const loadTimer    = useCallback(async()=>{ const t=await api("GET","/timer/status"); setTimer(t||{active:false}); },[]);
  const loadPause    = useCallback(async()=>{ const p=await api("GET","/pause/status"); setPauseState(p||{active:false}); },[]);
  const loadToday    = useCallback(async()=>{ const d=await api("GET",`/workday?date=${today()}`); setWorkday(d?.workday||null); setTodayData({timeEntries:d?.timeEntries||[],dailyLogs:d?.dailyLogs||[],pauses:d?.pauses||[]}); },[]);

  const [weekOffset, setWeekOffset] = useState(0); // 0=current week, -1=last week etc.

  const getWeekRange = (offset=0) => {
    const now = new Date();
    const dow = now.getDay(); // 0=Sun
    const mon = new Date(now);
    mon.setDate(now.getDate() - (dow===0?6:dow-1) + offset*7);
    const sun = new Date(mon); sun.setDate(mon.getDate()+6);
    return {
      from: ld(mon),
      to:   ld(sun),
    };
  };

  const loadOverview = useCallback(async()=>{
    const { from: weekFrom, to } = getWeekRange(weekOffset);
    // Immer ab Monatsanfang laden für Monatssaldo
    const now2  = new Date();
    const monthStart = `${now2.getFullYear()}-${String(now2.getMonth()+1).padStart(2,'0')}-01`;
    // Falls wir in einer vergangenen Woche sind, Monatsstart der angezeigten Woche nehmen
    const viewedMonth = weekFrom.slice(0,7);
    const from = viewedMonth < monthStart.slice(0,7) ? weekFrom : monthStart;
    // bis Monatsende laden, sonst zählt „noch offen“ im Monat nur bis zum Wochenende
    const [ty,tm] = to.slice(0,7).split("-").map(Number);
    const monthEnd = `${ty}-${String(tm).padStart(2,'0')}-${String(new Date(ty,tm,0).getDate()).padStart(2,'0')}`;
    const ov = await api("GET",`/overview?from=${from}&to=${monthEnd>to?monthEnd:to}`);
    setOverview(ov);
  },[weekOffset]);
  const loadProjects = useCallback(async()=>{ const p=await api("GET","/projects"); setProjects(p||[]); },[]);
  const loadAll      = useCallback(()=>{ loadTimer();loadPause();loadToday();loadProjects();loadOverview(); },[loadTimer,loadPause,loadToday,loadProjects,loadOverview]);
  // Buchung von überall bearbeiten (Übertragen, Kalender …): window.dispatchEvent(new CustomEvent("zp:edit-entry",{detail:{id,date}}))
  const [anyEdit, setAnyEdit] = useState(null);
  useEffect(()=>{ const h=async ev=>{ const {id,date}=ev.detail||{}; if(ev.detail?.new){ setAnyEdit({ date, start_time: ev.detail.start, end_time: ev.detail.end }); return; } if(!id) return; const l=await api("GET",`/time-entries?${date?`date=${date}`:""}`); const e=Array.isArray(l)&&l.find(x=>x.id===id); if(e) setAnyEdit(e); };
    window.addEventListener("zp:edit-entry",h); return ()=>window.removeEventListener("zp:edit-entry",h); },[]);

  // Reload overview when week changes
  useEffect(()=>{ loadOverview(); },[weekOffset]);

  useEffect(()=>{ loadAll(); },[loadAll]);
  // Mit dem Schnellfenster synchron bleiben: regelmäßig + beim Fokus neu laden
  useEffect(()=>{
    let lastDay = today();
    const t = setInterval(()=>{ loadTimer(); loadPause(); if (today()!==lastDay) { lastDay=today(); loadAll(); } }, 15000);
    const f = ()=>loadAll();
    window.addEventListener("focus", f);
    return ()=>{ clearInterval(t); window.removeEventListener("focus", f); };
  },[loadAll,loadTimer,loadPause]);

  useEffect(()=>{
    clearInterval(timerRef.current);
    if(timer.active&&timer.start_time){
      const tick=()=>setElapsed(Math.floor((Date.now()-new Date(timer.start_time))/1000));
      tick(); timerRef.current=setInterval(tick,1000);
    } else setElapsed(0);
    return ()=>clearInterval(timerRef.current);
  },[timer.active,timer.start_time]);

  const pauseTimerRef = useRef(null);
  useEffect(()=>{
    clearInterval(pauseTimerRef.current);
    if(pauseState.active&&pauseState.start_time){
      const tick=()=>setPauseElapsed(Math.floor((Date.now()-new Date(pauseState.start_time))/1000));
      tick(); pauseTimerRef.current=setInterval(tick,60000);
    } else setPauseElapsed(0);
    return ()=>clearInterval(pauseTimerRef.current);
  },[pauseState.active,pauseState.start_time]);

  const startPause = async () => { await api("POST","/pause/start"); loadPause(); loadTimer(); loadToday(); };
  const stopPause  = async () => { await api("POST","/pause/stop");  loadPause(); loadToday(); loadOverview(); };

  const handleTimerSelect = async({projectId,taskId,planTaskId,comment})=>{
    await api("POST","/timer/start",{project_id:projectId,task_id:taskId,plan_task_id:planTaskId,comment});
    setPicker(null); loadTimer(); loadToday();
  };

  const jiraOn = useJiraOn();
  const { serverMode, me: srvMe } = useServerMode();
  const [assistOn, setAssistOn] = useState(false);
  useEffect(()=>{ api("GET","/assistant").then(r=>r&&!r.error&&setAssistOn(!!r.enabled)); },[]);
  useAssistantAutoOpen(setView);
  // Einfach: nur der Alltag. Erweitert: zusätzliche Ansichten (Stundenzettel, Planung, SSH-Rohdaten, Ticket-Bereiche)
  const [uiMode, setUiMode] = useState(() => { try { return localStorage.getItem("zp-ui-mode") || "einfach"; } catch (_) { return "einfach"; } });
  const toggleMode = () => { const m = uiMode==="einfach" ? "erweitert" : "einfach"; setUiMode(m); try { localStorage.setItem("zp-ui-mode", m); } catch (_) {} };
  const adv = uiMode==="erweitert";
  // Servermodus: Bereiche „Team“ (Teamleitung) und „Personal“ je Rolle; bei Rollenkombination untereinander mit Überschriften
  const isLead = serverMode && !!srvMe?.is_lead, isHr = serverMode && !!srvMe?.is_hr;
  const roleAreas = isLead || isHr;
  const showEmpl = !(roleAreas && srvMe?.productive === false);   // Konten ohne Zeiterfassung sehen den Mitarbeiter-Bereich nicht
  const emplL = roleAreas ? "Mitarbeiter · " : "";
  useEffect(()=>{ if(serverMode&&srvMe&&!showEmpl&&!["team","personal","einstellungen"].includes(view)){ const v=isLead?"team":"personal"; location.hash=v; setView(v); } },[serverMode,srvMe,showEmpl,view,isLead]);
  const NAV_GROUPS = [
    ...(showEmpl ? [
    { label:emplL+"Erfassen",  ids:["timer","planung","woche","kalender","abwesenheiten", ...(adv?["stundenzettel"]:[])] },
    { label:emplL+"Prüfen",    ids:["vorschlaege","uebertragen"] },
    { label:emplL+"Auswerten", ids:["report","projects", ...(adv?["ssh"]:[])] },
    ...(adv ? [{ label:emplL+"Tickets", ids:["plane","jira"] }] : []),
    ] : []),
    ...(isLead ? [{ label:"Team", ids:["team"] }] : []),
    ...(isHr ? [{ label:"Personal", ids:["personal"] }] : []),
    { label:"",          ids:[...(showEmpl?["assistent"]:[]),"einstellungen"] },
  ];
  const nav = [
    ...(isLead ? [{id:"team", label:"Team", icon:<UsersIcon size={16}/>}] : []),
    ...(isHr ? [{id:"personal", label:"Personal", icon:<UserCogIcon size={16}/>}] : []),
    {id:"timer",       label:"Heute",        icon:<Timer size={16}/>},
    {id:"vorschlaege", label:"Vorschläge",   icon:<Inbox size={16}/>},
    {id:"uebertragen", label:"Übertragen",   icon:<Send size={16}/>},
    {id:"woche",       label:"Woche",        icon:<CalendarClock size={16}/>},
    {id:"kalender",    label:"Kalender",     icon:<CalendarDays size={16}/>},
    {id:"projects",    label:"Projekte",     icon:<FolderOpen size={16}/>},
    {id:"stundenzettel", label:"Stundenzettel", icon:<Calendar size={16}/>},
    {id:"report",      label:"Auswertung",   icon:<BarChart2 size={16}/>},
    {id:"ssh",         label:"SSH-Sitzungen", icon:<Terminal size={16}/>},
    {id:"planung",      label:"To-dos",          icon:<ListChecks size={16}/>},
    {id:"abwesenheiten", label:"Abwesenheiten",   icon:<Umbrella size={16}/>},
    {id:"plane",          label:"Plane",           icon:<CloudUpload size={16}/>},
    ...(jiraOn===false ? [] : [{id:"jira",           label:"Jira · Tempo",     icon:<Upload size={16}/>}]),
    ...(assistOn ? [{id:"assistent", label:"Assistent", icon:<Compass size={16}/>}] : []),
    {id:"einstellungen",  label:"Einstellungen",   icon:<Settings size={16}/>},
  ];
  // Desktop und Mobile dieselbe Rollen-Filterung (NAV_GROUPS), sonst sieht man falsche Einträge
  const navAllowed = new Set(NAV_GROUPS.flatMap(g => g.ids));
  const navShown = nav.filter(n => navAllowed.has(n.id));

  return (
    <div style={{minHeight:"100vh",position:"relative"}}>
      {/* Grid */}

      {/* ── Seitenleiste (Desktop ≥ 980 px) ── */}
      <aside className="zp-side" aria-label="Hauptnavigation">
        <div className="drag"/>
        <div className="brand">
          <img src="/example-icon-duo.svg" alt=""/>
          <div><div className="name">Zeitprotokoll</div><div className="sub" title="Version – Details unter Einstellungen › Server">Beispiel{version?` · v${version}`:""}</div></div>
        </div>
        <nav>
          {NAV_GROUPS.map(g=>{ const items=g.ids.map(id=>nav.find(n=>n.id===id)).filter(Boolean); if(!items.length) return null; return (
            <div key={g.label}>
              <div className="grp">{g.label}</div>
              {items.map(n=>(
                <button key={n.id} className="item" aria-current={view===n.id||(n.id==="jira"&&view==="tempo")?"page":undefined} onClick={()=>{ location.hash=n.id; setView(n.id); }}>
                  {n.icon}<span>{n.label}</span>
                </button>))}
            </div>); })}
        </nav>
        <button onClick={toggleMode} title="Weitere Ansichten ein-/ausblenden" style={{margin:"8px 12px 0",border:"1px dashed var(--line)",background:"transparent",color:"var(--text2)",borderRadius:8,padding:"6px 10px",fontSize:12,textAlign:"left",cursor:"pointer"}}>
          {adv ? "◂ Einfache Ansicht" : "▸ Mehr Ansichten (Stundenzettel, Planung, SSH, Tickets)"}</button>
        {timer.active&&(
          <button className="run" onClick={()=>{ location.hash="timer"; setView("timer"); }} style={{border:"none",textAlign:"left",color:"var(--text)"}}>
            <div style={{display:"flex",alignItems:"center",gap:7}}><span style={SB.timerDot}/><b style={{fontVariantNumeric:"tabular-nums"}}>{fmtDur(elapsed)}</b><span style={{color:"var(--text2)"}}>läuft</span></div>
            <div style={{color:"var(--text2)",overflow:"hidden",textOverflow:"ellipsis",whiteSpace:"nowrap",marginTop:2}}>{timer.task?.jira_ticket?timer.task.jira_ticket+" · ":""}{timer.project?.name}</div>
          </button>)}
        {serverMode&&<ServerUserBox/>}
      </aside>

      {/* ── Kompakte Titelleiste (< 980 px) ── */}
      <header className="topbar zp-compact" style={SB.topbar}>
        <div style={SB.brand}>
          <img src="/example-icon-duo.svg" alt="" style={{width:34,height:26}}/>
          <div>
            <div style={SB.brandName} className="zp-display">Zeitprotokoll</div>
            <div style={SB.brandSub} title="Version – Details unter Einstellungen → Server">Beispiel{version?` · v${version}`:""}</div>
          </div>
        </div>

        <nav style={SB.tabs}>
          {navShown.map(n=>(
            <button key={n.id} style={{...SB.tabBtn,...(view===n.id?SB.tabActive:{})}} onClick={()=>{ location.hash=n.id; setView(n.id); }}>
              <span style={SB.navIcon}>{n.icon}</span>
              <span>{n.label}</span>
            </button>
          ))}
        </nav>

        <div style={{flex:1}}/>

        {timer.active&&(
          <div style={SB.timerMini}>
            <span style={SB.timerDot}/>
            <span style={{fontWeight:600,fontVariantNumeric:"tabular-nums",fontSize:13}}>{fmtDur(elapsed)}</span>
            <span style={{color:"var(--text3)",fontSize:11,maxWidth:140,overflow:"hidden",textOverflow:"ellipsis",whiteSpace:"nowrap"}}>{timer.project?.name}</span>
          </div>
        )}
        {serverMode&&<ServerUserBox compact/>}
      </header>

      {/* ── Inhalt ── */}
      <main className="zp-main" style={{padding:"18px 24px 28px",position:"relative",zIndex:1,minWidth:0}}>
        <div style={{maxWidth: view==="stundenzettel"||view==="woche"||view==="timer"||view==="kalender" ? 1400 : 1100, margin: "0 auto"}}>
          {serverMode&&<ServerBanner/>}
          {view==="timer"&&(
            <TimerView
              timer={timer} elapsed={elapsed} projects={projects}
              pauses={todayData?.pauses||[]}
              pauseState={pauseState} pauseElapsed={pauseElapsed}
              onPauseStart={startPause} onPauseStop={stopPause}
              viewDate={viewDate}
              onDateChange={d=>{setViewDate(d);}}
              overview={overview}
              onStart={()=>setPicker("start")} onStop={async()=>{await api("POST","/timer/stop");loadTimer();loadToday();}}
              onSwitch={()=>setPicker("switch")} workday={workday} todayData={todayData}
              onAddLog={async text=>{await api("POST","/daily-log",{text});loadToday();}}
              onDelLog={async id=>{await api("DELETE",`/daily-log/${id}`);loadToday();}}
              onRefresh={()=>{loadTimer();loadPause();loadToday();loadOverview();loadProjects();}}
              weekOffset={weekOffset} onWeekChange={delta=>{setWeekOffset(v=>v+delta);}}
              getWeekRange={getWeekRange}
            />
          )}
          {view==="team"&&isLead&&<TeamView key={hashKey}/>}
          {view==="personal"&&isHr&&<PersonalView key={hashKey}/>}
          {view==="woche"&&<WeekGrid/>}
          {view==="kalender"&&<CalendarView/>}
          {view==="projects"&&<><CleanupPanel onChanged={()=>{ loadProjects(); setPvKey(k=>k+1); }}/><div style={{height:12}}/><ProjectSourcesPanel onChanged={()=>{ loadProjects(); setPvKey(k=>k+1); }}/><ProjectsView key={pvKey} onRefreshNeeded={()=>{loadProjects();}}/></>}
          {view==="stundenzettel"&&<TimesheetView/>}
          {view==="report"&&<ReportViewNeu/>}
          {view==="ssh"&&(serverMode?<ClientHint title="SSH-Sitzungen brauchen den Mac-Client">SSH-Sitzungen werden auf deinem Rechner erkannt (Terminal-Protokoll). Der Server nimmt sie nur als Vorschlag vom Client entgegen – im Browser ist diese Ansicht nicht verfügbar.</ClientHint>:<SshView/>)}
          {view==="planung"&&<TodoView/>}
          {view==="koppeln"&&(serverMode?<ClientHint title="Kopplung braucht den Mac-Client">Das Browser-Add-on wird mit dem Mac-Client gekoppelt.</ClientHint>:<PairApprove nonce={location.hash.split("/")[1]||""} onDone={()=>{ location.hash="timer"; setView("timer"); }}/>)}
          {view==="abwesenheiten"&&<AbwesenheitenView/>}
          {view==="plane"&&<PlaneArea onOpenSettings={()=>{ location.hash="einstellungen/plane"; setView("einstellungen"); }}/>}
          {(view==="jira"||view==="tempo")&&<JiraArea key={view} initialTab={view==="tempo"?"tempo":"tickets"} onOpenSettings={()=>{ location.hash="einstellungen/jira"; setView("einstellungen"); }}/>}
          {view==="einstellungen"&&<EinstellungenView key={hashKey}/>}
          {view==="assistent"&&<AssistantView key={hashKey}/>}
          {anyEdit&&<TimeEntryModal entry={anyEdit} projects={projects} onSave={()=>{ setAnyEdit(null); loadAll(); window.dispatchEvent(new Event("zp:entry-changed")); }} onClose={()=>setAnyEdit(null)}/>}
          {view==="vorschlaege"&&<VorschlaegeView projects={projects} onChanged={()=>{loadToday();loadOverview();}}/>}
          {view==="uebertragen"&&<UebertragenView key={hashKey}/>}
        </div>
      </main>

      {picker&&<ProjectPicker projects={projects} mode={picker} onSelect={handleTimerSelect} onCancel={()=>setPicker(null)} onProjectCreated={loadProjects}/>}
    </div>
  );
}

const SB = {
  topbar:    { display:"flex",alignItems:"center",gap:4,background:"var(--card)",borderBottom:"1px solid var(--line)",padding:"0 14px 0 86px",height:52,position:"sticky",top:0,zIndex:20,WebkitAppRegion:"drag" },
  brand:     { display:"flex",alignItems:"center",gap:8,paddingRight:14,marginRight:6,height:"100%",flexShrink:0 },
  logoMark:  { width:26,height:26,borderRadius:7,background:"var(--honi-green)",color:"#fff",fontWeight:700,fontSize:13,display:"flex",alignItems:"center",justifyContent:"center",flexShrink:0 },
  brandName: { color:"var(--text)",fontSize:18,lineHeight:1.1 },
  brandSub:  { color:"var(--text2)",fontSize:10 },
  tabs:      { display:"flex",alignItems:"center",gap:2,height:"100%",overflowX:"auto",WebkitAppRegion:"no-drag" },
  tabBtn:    { display:"flex",alignItems:"center",gap:6,background:"transparent",border:"none",color:"var(--text2)",padding:"6px 10px",borderRadius:7,cursor:"pointer",fontSize:13,fontWeight:500,fontFamily:"inherit",whiteSpace:"nowrap",flexShrink:0 },
  tabActive: { color:"var(--accent)",background:"var(--accent-soft)",fontWeight:600 },
  navIcon:   { display:"flex",alignItems:"center",flexShrink:0 },
  timerMini: { display:"flex",alignItems:"center",gap:7,background:"var(--sel)",borderRadius:20,padding:"5px 12px 5px 10px",WebkitAppRegion:"no-drag" },
  timerDot:  { width:7,height:7,borderRadius:"50%",background:"#4a6cf7",flexShrink:0,animation:"pulse 1.6s ease-in-out infinite" },
};

/* ═══ ABSENCE TYPE CONFIG ══════════════════════ */
const ABS_TYPES = {
  urlaub:      { label:"Urlaub",       icon:"Umbrella",    color:"#0F752F" },
  krankheit:   { label:"Krankheit",    icon:"HeartPulse",  color:"#d64545" },
  feiertag:    { label:"Feiertag",     icon:"Star",        color:"#8aab00" },
  fortbildung: { label:"Fortbildung",  icon:"BookOpen",    color:"#419C5E" },
  sonstiges:   { label:"Sonstiges",    icon:"HelpCircle",  color:"#9E9E9E" },
};
// Icon renderer für ABS_TYPES (JSX darf nicht auf Modul-Ebene stehen)
function AbsIcon({ type, size=13 }) {
  const icons = { Umbrella, HeartPulse, Star, BookOpen, HelpCircle };
  const Ic = icons[type] || HelpCircle;
  return <Ic size={size}/>;
}
const DAYS_DE = ["So","Mo","Di","Mi","Do","Fr","Sa"];
const DAYS_FULL = ["Sonntag","Montag","Dienstag","Mittwoch","Donnerstag","Freitag","Samstag"];
const SETTING_KEYS = ["target_mon","target_tue","target_wed","target_thu","target_fri","target_sat","target_sun"];
const SETTING_LABELS = ["Montag","Dienstag","Mittwoch","Donnerstag","Freitag","Samstag","Sonntag"];

/* ═══ PLANUNG VIEW ══════════════════════════════ */
/* Einstellungen › Arbeitszeit: Sollstunden, Start, Saldo, Urlaubsanspruch, Tätigkeiten – an genau einer Stelle */
function ArbeitszeitSettings() {
  const [v, setV] = useState(null); const [ok, setOk] = useState(false);
  useEffect(() => { api("GET","/settings").then(s => s && setV(s.vacation_days_per_year || "30")); }, []);
  return (<div>
    <AbwesenheitenView only="arbeitszeit"/>
    <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"14px 20px",margin:"12px 0"}}><div style={{fontWeight:600,marginBottom:6}}>Zeiten aus Tempo (Arbeitszeitkonto von Anfang an)</div><TempoSyncCard/></div>
    {v!=null && <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"14px 20px",margin:"12px 0",display:"flex",gap:12,alignItems:"flex-end"}}>
      <Field label="URLAUBSANSPRUCH (Tage/Jahr)"><input style={{...inp,width:140}} type="number" value={v} onChange={e=>setV(e.target.value)}/></Field>
      <Btn variant="primary" onClick={async()=>{ await api("PUT","/settings",{vacation_days_per_year:v}); setOk(true); setTimeout(()=>setOk(false),2000); }}><Check size={14}/> {ok?"Gespeichert ✓":"Speichern"}</Btn>
    </div>}
    <ActivitiesEditor/>
  </div>);
}

/* Vorschläge: Termine, erkannte Meetings, SSH-Sitzungen – das sind KEINE Buchungen, erst „Buchen“ macht Zeit daraus */
function VorschlaegeView({ projects, onChanged }) {
  const [tab, setTab] = useState(() => location.hash.split("/")[1] === "ssh" ? "ssh" : "termine");
  const chip = (k, l) => <button key={k} onClick={()=>setTab(k)} style={{border:"1px solid "+(tab===k?"var(--accent)":"var(--line)"),background:tab===k?"var(--accent-soft)":"var(--card)",color:tab===k?"var(--accent)":"var(--text2)",borderRadius:20,padding:"5px 14px",fontSize:12,fontWeight:tab===k?600:500,cursor:"pointer"}}>{l}</button>;
  return (<div>
    <div className="zp-card" style={{padding:"12px 16px",marginBottom:12,fontSize:13}}>
      <b>Vorschläge sind noch keine Buchungen.</b> <span style={{color:"var(--text2)"}}>Termine, erkannte Meetings und SSH-Sitzungen sammelt das System nur als Hinweis. Erst wenn du „Buchen“ wählst, wird daraus Zeit – sonst einfach ignorieren.</span>
    </div>
    <div style={{display:"flex",gap:6,marginBottom:12}}>{chip("termine","Termine & Meetings")}{chip("ssh","SSH-Sitzungen")}</div>
    {tab==="termine" && (isServerMode() ? <ClientHint title="Termine und Meetings brauchen den Mac-Client">Meetings und Kalendertermine erkennt der Mac-Client auf deinem Rechner und schlägt sie dort vor. Im Browser gibt es diese Vorschläge nicht.</ClientHint> : <MeetingSuggestions projects={projects} onChanged={onChanged} days={14} emptyText="Keine offenen Termine oder Meetings der letzten 14 Tage."/>)}
    {tab==="ssh" && (isServerMode() ? <ClientHint title="SSH-Sitzungen brauchen den Mac-Client"/> : <SshView/>)}
  </div>);
}

/* Übertragen: was geht nach Tempo, was nach Plane – an einer Stelle */
function UebertragenView() {
  const jiraOn = useJiraOn();
  const [tab, setTab] = useState(() => location.hash.split("/")[1] === "plane" ? "plane" : "tempo");
  const t = jiraOn===false ? "plane" : tab;
  const chip = (k, l, d) => <button key={k} onClick={()=>setTab(k)} style={{flex:1,textAlign:"left",border:"1px solid "+(t===k?"var(--accent)":"var(--line)"),background:t===k?"var(--accent-soft)":"var(--card)",borderRadius:10,padding:"10px 14px",cursor:"pointer",color:"var(--text)"}}>
    <div style={{fontWeight:600}}>{l}</div><div style={{fontSize:12,color:"var(--text2)"}}>{d}</div></button>;
  return (<div>
    {isServerMode()&&<MonthApprovalCard/>}
    <div style={{display:"flex",gap:8,marginBottom:14,flexWrap:"wrap"}}>
      {jiraOn!==false && chip("tempo","Nach Tempo","Abrechnung – jede Buchung als Worklog (Kunden in Jira direkt, Plane-Projekte aufs Übergangs-Ticket)")}
      {chip("plane","Nach Plane","Zeiten auf Plane-Tickets – nur Projekte, die in Plane sind")}
    </div>
    {t==="tempo" ? <TempoOutboxView onOpenSettings={()=>{location.hash="einstellungen/tempo";}}/> : <PlaneSyncView onOpenSettings={()=>{location.hash="einstellungen/plane";}}/>}
  </div>);
}

function AbwesenheitenView({ only = "abwesenheiten" }) {
  const [tab,       setTab]       = useState(only);
  const [settings,  setSettings]  = useState(null);
  const [absences,  setAbsences]  = useState([]);
  const [editAbs,   setEditAbs]   = useState(null);
  const [absForm,   setAbsForm]   = useState({date_from:"",date_to:"",type:"urlaub",hours:"",note:""});
  const [saving,    setSaving]    = useState(false);
  const [localSettings, setLocal] = useState(null);

  const [importMsg, setImportMsg] = useState("");
  const [importing, setImporting] = useState(false);

  const [corrections,   setCorrections]   = useState([]);
  const [corrForm,      setCorrForm]      = useState({date:today(), balance_hours:"", note:""});
  const [corrSaving,    setCorrSaving]    = useState(false);

  const load = useCallback(async () => {
    const [s, a, c] = await Promise.all([
      api("GET", "/settings"),
      api("GET", `/absences?from=${nDaysAgo(180)}&to=${nDaysAhead(365)}`),
      api("GET", "/balance-corrections"),
    ]);
    setSettings(s); setLocal(s); setAbsences(a||[]); setCorrections(c||[]);
  }, []);

  const addCorrection = async () => {
    if (!corrForm.date || corrForm.balance_hours==="") return;
    setCorrSaving(true);
    await api("POST", "/balance-corrections", corrForm);
    setCorrForm({date:today(), balance_hours:"", note:""});
    setCorrSaving(false);
    load();
  };

  const delCorrection = async id => {
    if (!confirm("Korrektur löschen?")) return;
    await api("DELETE", `/balance-corrections/${id}`);
    load();
  };

  const importHolidays = async (year) => {
    setImporting(true); setImportMsg("");
    const r = await api("POST", "/absences/import-holidays", { year });
    if (r) setImportMsg(`✓ ${r.imported} importiert, ${r.skipped} bereits vorhanden`);
    else   setImportMsg("❌ Fehler beim Import");
    setImporting(false);
    load();
  };
  useEffect(() => { load(); }, [load]);

  const nDaysAgo   = n => { const d=new Date(); d.setDate(d.getDate()-n); return ld(d); };
  const nDaysAhead = n => { const d=new Date(); d.setDate(d.getDate()+n); return ld(d); };

  const weeklyTotal = localSettings
    ? SETTING_KEYS.reduce((s,k) => s + (parseFloat(localSettings[k])||0), 0)
    : 0;

  const saveSettings = async () => {
    setSaving(true);
    await api("PUT", "/settings", localSettings);
    setSaving(false);
  };

  const saveAbsence = async () => {
    if (!absForm.date_from || !absForm.type) return;
    if (editAbs?.id) await api("PUT", `/absences/${editAbs.id}`, absForm);
    else             await api("POST", "/absences", absForm);
    setEditAbs(null);
    setAbsForm({date_from:"",date_to:"",type:"urlaub",hours:"",note:""});
    load();
  };

  const delAbsence = async id => {
    const a = absences.find(x => x.id === id);
    const fmt = d => d ? new Date(d+"T12:00").toLocaleDateString("de-DE") : "";
    if (!confirm(a ? `${ABS_TYPES[a.type]?.label||a.type} ${fmt(a.date_from)}${a.date_to!==a.date_from?` – ${fmt(a.date_to)}`:""}${a.note?` („${a.note}“)`:""} löschen?` : "Abwesenheit löschen?")) return;
    await api("DELETE", `/absences/${id}`);
    load();
  };

  const openEditAbs = a => {
    setEditAbs(a);
    setAbsForm({date_from:a.date_from,date_to:a.date_to,type:a.type,hours:a.hours||"",note:a.note||""});
  };

  const exportCSV = async () => {
    const from = nDaysAgo(30);
    const to   = today();
    const ov   = await api("GET", `/overview?from=${from}&to=${to}`);
    if (!ov?.days) return;

    // Group by week
    const rows = [["Datum","Wochentag","Typ","Soll (h)","Ist (h)","Saldo (h)","Abwesenheit"]];
    let weekRows = [];
    let weekSoll = 0, weekIst = 0;

    const flush = (label) => {
      if (!weekRows.length) return;
      rows.push([]);
      weekRows.forEach(r => rows.push(r));
      rows.push(["",label+" Summe","",weekSoll.toFixed(1),weekIst.toFixed(1),(weekIst-weekSoll).toFixed(1),""]);
      weekRows = []; weekSoll = 0; weekIst = 0;
    };

    let lastWeek = null;
    for (const d of ov.days) {
      const dt    = new Date(d.date + "T12:00:00Z");
      const dow   = dt.getUTCDay();
      // KW ermitteln
      const kw    = isoWeek(d.date).week;
      if (lastWeek !== null && kw !== lastWeek) flush(`KW ${lastWeek}`);
      lastWeek = kw;

      const absLabel = d.absence ? `${ABS_TYPES[d.absence.type]?.label||d.absence.type}${d.absence.note?` (${d.absence.note})`:""}` : "";
      weekRows.push([d.date, DAYS_FULL[dow], d.absence?"Abwesenheit":"Arbeitstag",
        d.targetH.toFixed(1), d.bookedH.toFixed(2), d.balanceH.toFixed(2), absLabel]);
      weekSoll += d.targetH; weekIst += d.absence ? d.absenceH : d.bookedH;
    }
    if (lastWeek) flush(`KW ${lastWeek}`);

    const csv = rows.map(r => r.map(c=>`"${String(c).replace(/"/g,'""')}"`).join(";")).join("\n");
    Object.assign(document.createElement("a"),{
      href:"data:text/csv;charset=utf-8,\uFEFF"+encodeURIComponent(csv),
      download:`zeitprotokoll-30tage-${today()}.csv`
    }).click();
  };

  return (
    <div>
      <div style={{display:"flex",alignItems:"center",justifyContent:"space-between",marginBottom:16}}>
        <div style={{fontSize:13,color:"var(--text2)"}}>{only==="abwesenheiten" ? <>Urlaub, Krankheit, Feiertage … – Sollstunden und Urlaubsanspruch: <a href="#einstellungen/arbeitszeit">Einstellungen › Arbeitszeit</a></> : "Sollstunden je Wochentag, Erfassungsbeginn und Saldo-Start"}</div>
        {only==="abwesenheiten" && <Btn onClick={exportCSV}><Download size={14}/> CSV 30 Tage</Btn>}
      </div>

      {/* ── Arbeitszeitmodell ── */}
      {tab==="arbeitszeit" && localSettings && (
        <div>
          <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"18px 20px",marginBottom:12}}>
            <div style={{display:"flex",alignItems:"center",justifyContent:"space-between",marginBottom:16}}>
              <div>
                <div style={{color:"var(--hi)",fontWeight:700,fontSize:14}}>Wochenstunden-Modell</div>
                <div style={{color:"var(--text3)",fontSize:11,marginTop:2}}>Soll-Stunden pro Wochentag für die +/− Berechnung</div>
              </div>
              <div style={{textAlign:"right"}}>
                <div style={{color:"var(--accent)",fontWeight:700,fontSize:22}}>{weeklyTotal.toFixed(1)}h</div>
                <div style={{color:"var(--text3)",fontSize:10}}>Woche gesamt</div>
              </div>
            </div>
            <div style={{display:"grid",gridTemplateColumns:"repeat(7,1fr)",gap:8}}>
              {SETTING_KEYS.map((k,i) => (
                <div key={k} style={{textAlign:"center"}}>
                  <div style={{color:"var(--text3)",fontSize:10,marginBottom:6,letterSpacing:"0.06em"}}>{SETTING_LABELS[i].slice(0,2).toUpperCase()}</div>
                  <input
                    type="number" min="0" max="24" step="0.5"
                    style={{...inp,textAlign:"center",padding:"8px 4px",fontSize:15,fontWeight:700,color:"var(--hi)",width:"100%"}}
                    value={localSettings[k]||0}
                    onChange={e => setLocal(v=>({...v,[k]:e.target.value}))}
                  />
                  <div style={{color:"var(--text3)",fontSize:9,marginTop:3}}>h</div>
                </div>
              ))}
            </div>
            <div style={{display:"flex",justifyContent:"flex-end",marginTop:14}}>
              <Btn variant="primary" onClick={saveSettings} disabled={saving}>
                <Check size={14}/> {saving?"Gespeichert":"Speichern"}
              </Btn>
            </div>
          </div>
          {/* Visuelle Vorschau */}
          <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"14px 20px"}}>
            <div style={{color:"var(--text3)",fontSize:10,letterSpacing:"0.1em",fontWeight:600,marginBottom:10}}>VERTEILUNG</div>
            <div style={{display:"flex",gap:6,alignItems:"flex-end",height:60}}>
              {SETTING_KEYS.map((k,i) => {
                const h = parseFloat(localSettings[k])||0;
                const max = Math.max(...SETTING_KEYS.map(k2=>parseFloat(localSettings[k2])||0), 1);
                return (
                  <div key={k} style={{flex:1,display:"flex",flexDirection:"column",alignItems:"center",gap:4}}>
                    <div style={{fontSize:9,color:"var(--accent)",fontWeight:700}}>{h>0?h+"h":""}</div>
                    <div style={{width:"100%",height:`${(h/max)*48}px`,background:h>0?"var(--green)":"var(--border)",borderRadius:"3px 3px 0 0",transition:"height 0.3s"}}/>
                    <div style={{fontSize:9,color:"var(--text3)"}}>{SETTING_LABELS[i].slice(0,2)}</div>
                  </div>
                );
              })}
            </div>
          </div>

          {/* Arbeitszeitkonto: manueller Startsaldo */}
          <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"18px 20px",marginTop:12}}>
            <div style={{color:"var(--hi)",fontWeight:700,fontSize:14,marginBottom:2}}>Arbeitszeitkonto – Startwerte</div>
            <div style={{color:"var(--text3)",fontSize:11,marginBottom:16}}>
              Falls du bereits ein Über-/Unterstunden-Konto aus einem anderen System mitbringst,
              trag es hier ein – es wird als Basis für das laufende Arbeitszeitkonto verwendet.
            </div>
            <div style={{display:"grid",gridTemplateColumns:"1fr 1fr",gap:16}}>
              <Field label="STARTDATUM DER ZEITERFASSUNG" hint="Ab wann soll gerechnet werden? Leer = erster Eintrag">
                <input type="date" style={inp} value={localSettings.tracking_start_date||""} onChange={e=>setLocal(v=>({...v,tracking_start_date:e.target.value}))}/>
              </Field>
              <Field label="START-SALDO (STUNDEN)" hint="Positiv = Überstunden, negativ = Minusstunden">
                <input type="number" step="0.5" style={inp} value={localSettings.opening_balance_hours||0} onChange={e=>setLocal(v=>({...v,opening_balance_hours:e.target.value}))}/>
              </Field>
            </div>
            {/* v6.36: „Arbeitstag starten“ ist optional – gerechnet wird mit der gebuchten Zeit */}
            <div style={{display:"grid",gap:8,marginTop:14,fontSize:12.5}}>
              <label style={{display:"flex",gap:8,alignItems:"flex-start",cursor:"pointer"}}>
                <input type="checkbox" checked={String(localSettings.ist_from_workday)==="1"} onChange={e=>setLocal(v=>({...v,ist_from_workday:e.target.checked?"1":"0"}))} style={{marginTop:2}}/>
                <span><b>Arbeitstag-Fenster als Ist zählen</b><br/><span style={{color:"var(--text3)"}}>Aus (Standard): Ist = gebuchte Zeit, wie in Tempo. An: Ist = Arbeitstag Start bis Ende minus Pausen – nur für Tage, an denen ein Arbeitstag gestartet und beendet wurde.</span></span>
              </label>
              <label style={{display:"flex",gap:8,alignItems:"flex-start",cursor:"pointer"}}>
                <input type="checkbox" checked={String(localSettings.workday_prompt)==="1"} onChange={e=>setLocal(v=>({...v,workday_prompt:e.target.checked?"1":"0"}))} style={{marginTop:2}}/>
                <span><b>Beim Anmelden fragen „Arbeitstag starten?“</b><br/><span style={{color:"var(--text3)"}}>Aus (Standard): kein Fenster am Morgen. Von wann bis wann du da warst, erkennt das Programm ohnehin über die Zeit am Mac.</span></span>
              </label>
            </div>
            <div style={{display:"flex",justifyContent:"flex-end",marginTop:14}}>
              <Btn variant="primary" onClick={saveSettings} disabled={saving}>
                <Check size={14}/> {saving?"Gespeichert":"Speichern"}
              </Btn>
            </div>
          </div>

          {/* Saldo-Korrekturen: jederzeit einen Stichtag setzen */}
          <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"18px 20px",marginTop:12}}>
            <div style={{color:"var(--hi)",fontWeight:700,fontSize:14,marginBottom:2}}>Saldo-Korrekturen</div>
            <div style={{color:"var(--text3)",fontSize:11,marginBottom:16}}>
              Setze das Arbeitszeitkonto jederzeit auf einen Stichtag – z.B. „am 01.09. Saldo auf +5h".
              Ab dem Folgetag läuft die normale Berechnung von diesem Wert aus weiter.
            </div>

            {corrections.length>0 && (
              <div style={{marginBottom:14}}>
                {corrections.map(c=>(
                  <div key={c.id} style={{display:"flex",alignItems:"center",gap:10,padding:"8px 10px",borderBottom:"1px solid var(--border)"}}>
                    <span style={{color:"var(--text2)",fontSize:12,minWidth:90}}>{fmtDate(c.date)}</span>
                    <span style={{color:c.balance_hours>=0?"var(--green)":"var(--red)",fontWeight:700,fontSize:13,minWidth:60}}>
                      {c.balance_hours>=0?"+":""}{c.balance_hours}h
                    </span>
                    {c.note&&<span style={{color:"var(--text3)",fontSize:11,flex:1}}>{c.note}</span>}
                    <button style={{background:"transparent",border:"none",color:"var(--red)",cursor:"pointer",padding:4,marginLeft:"auto"}} onClick={()=>delCorrection(c.id)}>
                      <Trash2 size={13}/>
                    </button>
                  </div>
                ))}
              </div>
            )}

            <div style={{display:"grid",gridTemplateColumns:"1fr 1fr 2fr auto",gap:10,alignItems:"end"}}>
              <Field label="STICHTAG">
                <input type="date" style={inp} value={corrForm.date} onChange={e=>setCorrForm(v=>({...v,date:e.target.value}))}/>
              </Field>
              <Field label="SALDO (H)">
                <input type="number" step="0.5" style={inp} placeholder="z.B. 5" value={corrForm.balance_hours} onChange={e=>setCorrForm(v=>({...v,balance_hours:e.target.value}))}/>
              </Field>
              <Field label="NOTIZ (OPTIONAL)">
                <input style={inp} placeholder="z.B. Übernahme aus altem System" value={corrForm.note} onChange={e=>setCorrForm(v=>({...v,note:e.target.value}))}/>
              </Field>
              <Btn variant="primary" onClick={addCorrection} disabled={corrSaving||!corrForm.date||corrForm.balance_hours===""}>
                <Plus size={14}/> {corrSaving?"…":"Hinzufügen"}
              </Btn>
            </div>
          </div>
        </div>
      )}

      {/* ── Abwesenheiten ── */}
      {tab==="abwesenheiten" && (
        <div>
          {isServerMode()&&<div style={{marginBottom:12}}><LeaveRequestCard/></div>}
          {!isServerMode()&&<div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"12px 18px",marginBottom:12,display:"flex",gap:12,alignItems:"center",flexWrap:"wrap"}}>
            <CalendarDays size={18} style={{color:"var(--accent)"}}/>
            <div style={{flex:"1 1 260px",fontSize:13}}><b>Im Mac-Kalender anzeigen</b><div style={{fontSize:12,color:"var(--text2)"}}>Einmal abonnieren – danach erscheinen neue und geänderte Abwesenheiten automatisch (Kalender „Zeitprotokoll Abwesenheiten“, stündlich aktualisiert).<br/>So geht’s: Adresse kopieren → im Kalender <b>Ablage › Neues Kalenderabonnement …</b> → einfügen → bei „Ort“ <b>Auf meinem Mac</b> wählen (nicht iCloud – iCloud kommt an deinen Rechner nicht heran).</div></div>
            <AboUrl url={`http://127.0.0.1:${location.port || 3747}/api/calendar/abwesenheiten.ics`}/>
            <a href="/api/calendar/abwesenheiten.ics?download=1" style={{fontSize:12}}>.ics herunterladen</a>
          </div>}
          {/* Formular */}
          <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"16px 18px",marginBottom:12}}>
            <div style={{color:"var(--hi)",fontWeight:700,fontSize:12,marginBottom:12}}>{editAbs?"Abwesenheit bearbeiten":"Neue Abwesenheit"}</div>
            <div style={{display:"grid",gridTemplateColumns:"1fr 1fr 1fr 1fr",gap:10,marginBottom:10}}>
              <Field label="VON">
                <input style={inp} type="date" value={absForm.date_from} onChange={e=>setAbsForm(v=>({...v,date_from:e.target.value,date_to:v.date_to||e.target.value}))}/>
              </Field>
              <Field label="BIS">
                <input style={inp} type="date" value={absForm.date_to} onChange={e=>setAbsForm(v=>({...v,date_to:e.target.value}))}/>
              </Field>
              <Field label="TYP">
                <select style={sel} value={absForm.type} onChange={e=>setAbsForm(v=>({...v,type:e.target.value}))}>
                  {Object.entries(ABS_TYPES).map(([k,v])=><option key={k} value={k}>{v.label}</option>)}
                </select>
              </Field>
              <Field label="STUNDEN (leer = Soll)" hint="leer = ganzer Tag">
                <input style={inp} type="number" min="0" max="24" step="0.5" value={absForm.hours} onChange={e=>setAbsForm(v=>({...v,hours:e.target.value}))} placeholder="auto"/>
              </Field>
            </div>
            <Field label="NOTIZ">
              <input style={inp} placeholder="z.B. Resturlaub 2025" value={absForm.note} onChange={e=>setAbsForm(v=>({...v,note:e.target.value}))} onKeyDown={e=>e.key==="Enter"&&saveAbsence()}/>
            </Field>
            <div style={{display:"flex",gap:8,justifyContent:"flex-end"}}>
              {editAbs&&<Btn onClick={()=>{setEditAbs(null);setAbsForm({date_from:"",date_to:"",type:"urlaub",hours:"",note:""});}}>Abbrechen</Btn>}
              <Btn variant="primary" onClick={saveAbsence} disabled={!absForm.date_from}><Check size={14}/> {editAbs?"Speichern":"Eintragen"}</Btn>
            </div>
          </div>

          {/* Urlaubs-Übersicht */}
          {settings?.vacation_days_per_year > 0 && (() => {
            const used = absences.filter(a => a.type==="urlaub" && a.date_from.startsWith(new Date().getFullYear())).length;
            const total = settings.vacation_days_per_year;
            const remain = total - used;
            return (
              <div style={{display:"flex",gap:16,background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"12px 16px",marginBottom:12}}>
                {[["Gesamt",total,"var(--text2)"],["Genommen",used,"var(--accent)"],["Rest",remain,remain>0?"var(--green)":"var(--red)"]].map(([l,v,c])=>(
                  <div key={l}>
                    <div style={{color:"var(--text3)",fontSize:10,letterSpacing:"0.08em"}}>{l.toUpperCase()}</div>
                    <div style={{color:c,fontWeight:700,fontSize:20}}>{v}</div>
                    <div style={{color:"var(--text3)",fontSize:10}}>Tage</div>
                  </div>
                ))}
                <div style={{width:1,background:"var(--border)",margin:"0 4px"}}/>
                <div style={{flex:1,display:"flex",alignItems:"center"}}>
                  <div style={{width:"100%"}}>
                    <div style={{height:6,background:"var(--border)",borderRadius:3,overflow:"hidden"}}>
                      <div style={{width:`${Math.min(100,(used/total)*100)}%`,height:"100%",background:remain>5?"var(--green)":remain>0?"var(--accent)":"var(--red)",borderRadius:3,transition:"width 0.4s"}}/>
                    </div>
                    <div style={{color:"var(--text3)",fontSize:10,marginTop:3}}>{new Date().getFullYear()}</div>
                  </div>
                </div>
              </div>
            );
          })()}

          {/* Feiertage Niedersachsen importieren */}
          <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"12px 16px",marginBottom:12}}>
            <div style={{display:"flex",alignItems:"center",justifyContent:"space-between",flexWrap:"wrap",gap:10}}>
              <div>
                <div style={{color:"var(--hi)",fontWeight:600,fontSize:12}}>🏖 Niedersachsen-Feiertage</div>
                <div style={{color:"var(--text3)",fontSize:11,marginTop:2}}>Karfreitag, Ostermontag, Reformationstag etc. automatisch importieren</div>
              </div>
              <div style={{display:"flex",gap:8,flexWrap:"wrap"}}>
                {[2024,2025,2026,2027].map(y=>(
                  <Btn key={y} size="sm" onClick={()=>importHolidays(y)} disabled={importing}>
                    <Star size={11}/> {y}
                  </Btn>
                ))}
                <Btn size="sm" variant="accent" onClick={()=>importHolidays(null)} disabled={importing}>
                  <Star size={11}/> Alle Jahre
                </Btn>
              </div>
            </div>
            {importMsg&&<div style={{marginTop:8,fontSize:11,color:importMsg.startsWith("✓")?"var(--green)":"var(--red)"}}>{importMsg}</div>}
          </div>

          {/* Abwesenheitsliste */}
          {absences.length===0 && <div style={{color:"var(--text3)",textAlign:"center",padding:"30px 0"}}>Noch keine Abwesenheiten eingetragen.</div>}
          {absences.map(a => {
            const cfg = ABS_TYPES[a.type]||ABS_TYPES.sonstiges;
            const days = Math.round((new Date(a.date_to)-new Date(a.date_from))/86400000)+1;
            return (
              <div key={a.id} style={{display:"flex",alignItems:"center",gap:12,background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"10px 14px",marginBottom:6}}>
                <span style={{color:cfg.color}}><AbsIcon type={cfg.icon}/></span>
                <div style={{flex:1}}>
                  <div style={{display:"flex",alignItems:"center",gap:8}}>
                    <span style={{color:"var(--hi)",fontWeight:600}}>{cfg.label}</span>
                    {a.note&&<span style={{color:"var(--text3)",fontSize:11}}>– {a.note}</span>}
                  </div>
                  <div style={{color:"var(--text3)",fontSize:11,marginTop:2}}>
                    {fmtDate(a.date_from)}{a.date_from!==a.date_to?` – ${fmtDate(a.date_to)}`:""} · {days} Tag{days!==1?"e":""}
                    {a.hours!=null&&` · ${a.hours}h`}
                  </div>
                </div>
                <button style={TV.iconBtn} onClick={()=>openEditAbs(a)}><Pencil size={13}/></button>
                <button style={{...TV.iconBtn,color:"var(--red)"}} onClick={()=>delAbsence(a.id)}><Trash2 size={13}/></button>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

/* ═══ MONATSABSCHLUSS (in Report) ═══════════════ */
/* ═══ EINSTELLUNGEN VIEW ════════════════════════ */
function EinstellungenView() {
  const sm = isServerMode();   // Servermodus: Technik/Server/Update gehören nach /admin, Erkennung braucht den Mac-Client
  const jiraMod = useJiraModule();   // null = lädt; false = Administrator hat das Jira-Modul abgeschaltet
  const OLD = { plane:"verbindungen", jira:"verbindungen", tempo:"verbindungen", tempocsvimport:"verbindungen", audit:"wartung", server:"wartung", system:"wartung", integrity:"wartung", update:"wartung", logs:"wartung" };
  const h1 = location.hash.split("/")[1] || "verbindungen";
  const [tab,     setTab]     = useState(() => OLD[h1] || h1);
  const [sub,     setSub]     = useState(() => OLD[h1] ? h1 : (location.hash.split("/")[2] || ""));
  const connRaw = ["plane","jira","tempo","tempocsvimport"].includes(sub) ? sub : "jira";
  const conn = !sm ? connRaw : jiraMod===false ? "plane" : connRaw==="tempocsvimport" ? "jira" : connRaw;
  const wt = sm ? "system" : (["audit","server","system","integrity","update","logs"].includes(sub) ? sub : "server");
  const [settings,setSettings]= useState(null);
  const [local,   setLocal]   = useState({});
  const [saved,   setSaved]   = useState(false);

  // Update state
  const [updateFile, setUpdateFile]  = useState(null);
  const [updating,   setUpdating]    = useState(false);
  const [updateMsg,  setUpdateMsg]   = useState("");

  const load = async () => {
    const s = await api("GET","/settings");
    setSettings(s); setLocal(s||{});
  };
  useEffect(()=>{ load(); },[]);

  const save = async () => {
    await api("PUT","/settings", local);
    setSaved(true);
    setTimeout(()=>setSaved(false), 2000);
  };

  const doUpdate = async () => {
    if (!updateFile) return;
    setUpdating(true);
    setUpdateMsg("Update wird hochgeladen…");
    try {
      const res = await fetch("/api/admin/update", {
        method:"POST",
        headers:{"Content-Type":"application/octet-stream"},
        body: updateFile,
      });
      const data = await res.json();
      if (data.ok) {
        setUpdateMsg("✓ "+data.message+" Browser lädt in 5s neu…");
        setTimeout(()=>window.location.reload(), 6000);
      } else {
        setUpdateMsg("❌ "+data.error);
        setUpdating(false);
      }
    } catch(e) {
      setUpdateMsg("❌ Verbindungsfehler: "+e.message);
      setUpdating(false);
    }
  };

  const tempoStatus = async () => {
    const s = await api("GET","/integrations/tempo/status");
    alert(`Tempo Status:\n\n${JSON.stringify(s,null,2)}`);
  };

  const TABS = [
    ["verbindungen","Verbindungen"],
    ["arbeitszeit","Arbeitszeit"],
    ["erkennung","Erkennung"],
    ["wartung", sm ? "Allgemein" : "Wartung"],
  ];
  const pill = (on, l, fn) => <button key={l} onClick={fn} style={{border:"none",borderBottom:"2px solid "+(on?"var(--accent)":"transparent"),background:"transparent",color:on?"var(--accent)":"var(--text2)",padding:"4px 10px",fontSize:12,fontWeight:on?600:500,cursor:"pointer"}}>{l}</button>;
  const go = (t, s="") => { setTab(t); setSub(s); history.replaceState(null,"",`#einstellungen/${s||t}`); };

  return (
    <div>
      <div style={{display:"flex",gap:6,marginBottom:16}}>
        {TABS.map(([k,l])=>(
          <button key={k} style={{border:"1px solid "+(tab===k?"var(--accent)":"var(--line)"),background:tab===k?"var(--accent-soft)":"var(--card)",color:tab===k?"var(--accent)":"var(--text2)",borderRadius:20,padding:"5px 14px",fontSize:12,fontWeight:tab===k?600:500,cursor:"pointer"}} onClick={()=>go(k)}>{l}</button>
        ))}
      </div>
      {tab==="verbindungen" && !(sm && jiraMod===false) && <div style={{display:"flex",gap:4,margin:"-6px 0 12px",borderBottom:"1px solid var(--line)"}}>
        {pill(conn!=="plane","Jira & Tempo",()=>go("verbindungen","jira"))}{pill(conn==="plane","Plane",()=>go("verbindungen","plane"))}</div>}
      {tab==="wartung" && !sm && <div style={{display:"flex",gap:4,margin:"-6px 0 12px",borderBottom:"1px solid var(--line)",flexWrap:"wrap"}}>
        {[["server","Server & Sicherung"],["audit","Änderungsprotokoll"],["integrity","Datenkonsistenz"],["update","Update"],["logs","System-Log"],["system","Allgemein"]].map(([k,l])=>pill(wt===k,l,()=>go("wartung",k)))}</div>}

      {tab==="verbindungen" && conn==="plane" && <PlaneSettingsTab/>}
      {tab==="verbindungen" && conn!=="plane" && <JiraTempoSettings initial={conn==="tempo"?"tempo":conn==="tempocsvimport"?"csv":"jira"}/>}
      {tab==="arbeitszeit" && <ArbeitszeitSettings/>}
      {tab==="erkennung" && (sm
        ? <><DevicesPanel/><div style={{height:12}}/><ClientHint title="Erkennung braucht den Mac-Client">Meetings, SSH-Sitzungen, Zeit am Mac (Anwesenheit), Browser-Add-on und Kopplung laufen auf deinem Rechner im Mac-Client. Hier legst du Geräte-Token an; der Client meldet Vorschläge an den Server. Zeiterfassung, Freigabe und Übertragung funktionieren im Browser vollständig.</ClientHint></>
        : <><PresenceToggle/><AddonSettings/><BrowserToggle/><MeetingSettingsTab/></>)}
      {tab==="wartung" && !sm && wt==="audit" && <AuditTab/>}
      {tab==="wartung" && !sm && wt==="server" && <ServerView/>}

      {/* ── System ── */}
      {tab==="wartung" && wt==="system" && local && (
        <div>
          <AssistantToggle/>
        </div>
      )}

      {/* ── Update ── */}
      {tab==="wartung" && !sm && wt==="update" && (
        <div>
          <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"18px 20px"}}>
            <div style={{color:"var(--hi)",fontWeight:700,marginBottom:6}}>In-App Update</div>
            <div style={{color:"var(--text2)",fontSize:12,marginBottom:16}}>
              Lade eine neue Version als .tar.gz hoch – kein Terminal nötig. Der Server baut das Frontend automatisch neu und startet danach neu.
            </div>

            <div style={{border:"2px dashed var(--border2)",borderRadius:6,padding:"28px 20px",textAlign:"center",marginBottom:16,background:"var(--bg3)"}}
              onDragOver={e=>{e.preventDefault();}}
              onDrop={e=>{e.preventDefault(); const f=e.dataTransfer.files[0]; if(f)setUpdateFile(f);}}>
              {updateFile ? (
                <div>
                  <div style={{color:"var(--green)",fontWeight:700,fontSize:14,marginBottom:4}}>✓ {updateFile.name}</div>
                  <div style={{color:"var(--text3)",fontSize:11}}>{(updateFile.size/1024).toFixed(0)} KB</div>
                </div>
              ) : (
                <div>
                  <Download size={24} color="var(--text3)" style={{margin:"0 auto 8px",display:"block"}}/>
                  <div style={{color:"var(--text3)",fontSize:12}}>Datei hier hineinziehen oder auswählen</div>
                </div>
              )}
            </div>

            <div style={{display:"flex",gap:10,alignItems:"center",flexWrap:"wrap"}}>
              <label style={{display:"inline-flex",alignItems:"center",gap:6,background:"var(--bg3)",border:"1px solid var(--border2)",borderRadius:4,padding:"8px 14px",cursor:"pointer",fontSize:12,color:"var(--text)"}}>
                <Plus size={13}/> Datei auswählen
                <input type="file" accept=".tar.gz,.tgz" style={{display:"none"}} onChange={e=>setUpdateFile(e.target.files[0])}/>
              </label>
              <Btn variant="primary" onClick={doUpdate} disabled={!updateFile||updating}>
                {updating ? "Installiere…" : "Update installieren"}
              </Btn>
              {updateFile&&!updating&&<button style={{background:"transparent",border:"none",color:"var(--text3)",cursor:"pointer",fontSize:11}} onClick={()=>setUpdateFile(null)}>✕ Abbrechen</button>}
            </div>

            {updateMsg&&(
              <div style={{marginTop:12,padding:"10px 14px",background:"var(--bg3)",border:"1px solid var(--border)",borderRadius:4,fontSize:12,color:updateMsg.startsWith("✓")?"var(--green)":updateMsg.startsWith("❌")?"var(--red)":"var(--text2)"}}>
                {updateMsg}
              </div>
            )}
            {(updating || updateMsg) && (
              <div style={{marginTop:12}}>
                <LogViewer category="update" autoRefresh={updating} title="Update Log" maxLines={60}/>
              </div>
            )}
          </div>
        </div>
      )}


      {/* ── Tempo CSV Import ── */}


      {/* ── Datenkonsistenz ── */}
      {tab==="wartung" && !sm && wt==="integrity" && <IntegrityTab/>}


      {/* ── System Log ── */}
      {tab==="wartung" && !sm && wt==="logs" && (
        <div>
          <div style={{display:"flex",gap:8,marginBottom:12,flexWrap:"wrap"}}>
            {["import","update","system"].map(cat => (
              <Btn key={cat} size="sm" onClick={()=>api("DELETE",`/logs?category=${cat}`).then(()=>{})} variant="default">
                <Trash2 size={11}/> {cat} löschen
              </Btn>
            ))}
            <Btn size="sm" onClick={()=>api("POST","/logs/cleanup")} variant="default">
              <RotateCcw size={11}/> {">"} 7 Tage bereinigen
            </Btn>
          </div>
          <div style={{display:"flex",flexDirection:"column",gap:10}}>
            <LogViewer category="import" title="Import Log" maxLines={50} autoRefresh={false}/>
            <LogViewer category="update" title="Update Log" maxLines={50} autoRefresh={false}/>
            <LogViewer category="system" title="System Log" maxLines={30} autoRefresh={false}/>
          </div>
        </div>
      )}
    </div>
  );
}

/* ═══ TÄTIGKEITSARTEN ═══════════════════════════ */
function ActivitiesEditor() {
  const list = useActivities();
  const [txt, setTxt] = useState(null);
  const [ok, setOk] = useState(false);
  const val = txt ?? list.join("\n");
  const save = async () => {
    await api("PUT", "/activities", val.split("\n").map(s=>s.trim()).filter(Boolean));
    await reloadActivities(); setTxt(null); setOk(true); setTimeout(()=>setOk(false), 2000);
  };
  return (
    <div className="zp-card" style={{padding:"16px 18px",marginBottom:12}}>
      <div style={{fontWeight:600,fontSize:14}}>Tätigkeitsarten</div>
      <div style={{color:"var(--text2)",fontSize:12,margin:"2px 0 10px"}}>Eine pro Zeile. Erscheinen beim Timer, beim Nachtragen, im Wochenraster und als Gruppierung in der Auswertung.</div>
      <textarea rows={7} style={{...inp,fontFamily:"inherit",resize:"vertical",maxWidth:360}} value={val} onChange={e=>setTxt(e.target.value)}/>
      <div style={{marginTop:10}}><Btn variant="primary" size="sm" onClick={save}><Check size={13}/> {ok?"Gespeichert ✓":"Tätigkeiten speichern"}</Btn></div>
    </div>
  );
}

/* ═══ SYSTEM LOG VIEWER ═════════════════════════ */
const LOG_COLORS = {
  info:"var(--text2)", progress:"var(--blue)", warn:"var(--accent)",
  error:"var(--red)", success:"var(--green)",
};
const LOG_ICONS = { info:"ℹ", progress:"⟳", warn:"⚠", error:"✗", success:"✓" };

function LogViewer({ category, autoRefresh=false, maxLines=80, title }) {
  const [logs, setLogs] = useState([]);
  const bottomRef = useRef(null);
  const timerRef  = useRef(null);

  const fetch = async () => {
    const params = new URLSearchParams({ category, limit: maxLines });
    const r = await api("GET", `/logs?${params}`);
    if (r?.logs) setLogs(r.logs);
  };

  useEffect(() => {
    fetch();
  }, [category]);

  useEffect(() => {
    if (!autoRefresh) return;
    timerRef.current = setInterval(fetch, 1500);
    return () => clearInterval(timerRef.current);
  }, [autoRefresh, category]);

  useEffect(() => {
    if (autoRefresh && logs.length)
      bottomRef.current?.scrollIntoView({ behavior:"smooth" });
  }, [logs.length, autoRefresh]);

  const clear = async () => {
    await api("DELETE", `/logs?category=${category}`);
    setLogs([]);
  };

  return (
    <div style={{ background:"var(--bg)", border:"1px solid var(--border)", borderRadius:6, overflow:"hidden" }}>
      <div style={{ display:"flex",alignItems:"center",justifyContent:"space-between",padding:"7px 12px",borderBottom:"1px solid var(--border)",background:"var(--bg2)" }}>
        <span style={{ color:"var(--hi)", fontWeight:600, fontSize:12 }}>{title||"Log"}</span>
        <div style={{ display:"flex",gap:10 }}>
          <button style={{ background:"transparent",border:"none",color:"var(--text3)",cursor:"pointer",fontSize:11,fontFamily:"inherit" }} onClick={fetch}>↻</button>
          <button style={{ background:"transparent",border:"none",color:"var(--text3)",cursor:"pointer",fontSize:11,fontFamily:"inherit" }} onClick={clear}>Löschen</button>
        </div>
      </div>
      <div style={{ maxHeight:240,overflow:"auto",fontFamily:"'IBM Plex Mono','Fira Code',monospace",fontSize:11,lineHeight:1.7 }}>
        {logs.length === 0 && (
          <div style={{ color:"var(--text3)",padding:"14px",textAlign:"center" }}>
            {autoRefresh ? "Warte…" : "Keine Einträge"}
          </div>
        )}
        {logs.map(l => (
          <div key={l.id} style={{ display:"flex",gap:8,padding:"1px 12px",background:"transparent" }}>
            <span style={{ color:"var(--text3)",flexShrink:0,minWidth:62,fontSize:10 }}>{(l.timestamp||"").slice(11,19)}</span>
            <span style={{ color:LOG_COLORS[l.level]||"var(--text2)",flexShrink:0,fontSize:12 }}>{LOG_ICONS[l.level]||"·"}</span>
            <span style={{ color:LOG_COLORS[l.level]||"var(--text2)",flex:1,wordBreak:"break-all" }}>{l.message}</span>
          </div>
        ))}
        <div ref={bottomRef}/>
      </div>
    </div>
  );
}

/* ═══ TEMPO IMPORT TAB ══════════════════════════ */
/* ═══ CSV IMPORT TAB ════════════════════════════ */
/* ═══ INTEGRITY CHECK TAB ═══════════════════════ */
function IntegrityTab() {
  const [data,    setData]    = useState(null);
  const [loading, setLoading] = useState(false);

  const check = async () => {
    setLoading(true);
    const r = await api("GET", "/check/integrity");
    setData(r); setLoading(false);
  };

  useEffect(()=>{ check(); },[]);

  const SEV_COLOR  = { error:"var(--red)", warn:"var(--accent)", info:"var(--blue)" };
  const SEV_ICON   = { error:"✗", warn:"⚠", info:"ℹ" };
  const TYPE_LABEL = {
    overlap:"Überschneidung", missing_end:"Fehlendes Ende",
    negative_duration:"Negative Dauer", no_workday:"Kein Arbeitstag", too_long:"Sehr langer Eintrag",
    pause_overlap:"Pause-Überschneidung"
  };

  return (
    <div>
      <div style={{display:"flex",alignItems:"center",gap:12,marginBottom:16}}>
        <Btn onClick={check} disabled={loading}><RotateCcw size={14}/> {loading?"Prüfe…":"Konsistenz prüfen"}</Btn>
        {data&&<span style={{color:"var(--text3)",fontSize:11}}>Geprüft: {new Date(data.checkedAt).toLocaleTimeString("de-DE")}</span>}
      </div>

      {data&&(
        <>
          <div style={{display:"flex",gap:16,marginBottom:16}}>
            {[["Fehler",data.summary.errors,"var(--red)"],["Warnungen",data.summary.warnings,"var(--accent)"],["Hinweise",data.summary.infos,"var(--blue)"]].map(([l,v,c])=>(
              <div key={l} style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"10px 16px"}}>
                <div style={{color:"var(--text3)",fontSize:10}}>{l.toUpperCase()}</div>
                <div style={{color:v>0?c:"var(--green)",fontWeight:700,fontSize:22}}>{v}</div>
              </div>
            ))}
            {data.summary.total===0&&(
              <div style={{color:"var(--green)",fontSize:13,display:"flex",alignItems:"center",gap:6}}>
                <Check size={16}/> Alles in Ordnung – keine Probleme gefunden
              </div>
            )}
          </div>

          {data.issues.map((issue,i)=>(
            <div key={i} style={{display:"flex",gap:10,padding:"9px 14px",background:"var(--bg2)",border:`1px solid ${SEV_COLOR[issue.severity]}30`,borderRadius:5,marginBottom:5}}>
              <span style={{color:SEV_COLOR[issue.severity],flexShrink:0,fontWeight:700}}>{SEV_ICON[issue.severity]}</span>
              <div style={{flex:1}}>
                <span style={{color:"var(--text3)",fontSize:10,marginRight:8}}>{TYPE_LABEL[issue.type]||issue.type}</span>
                <span style={{color:"var(--text)",fontSize:12}}>{issue.message}</span>
              </div>
              {issue.type==="overlap"&&<a href="#assistent" style={{fontSize:11,color:"var(--accent)",flexShrink:0}}>lösen ›</a>}
              <span style={{color:"var(--text3)",fontSize:11,flexShrink:0}}>{issue.date}</span>
            </div>
          ))}
        </>
      )}
    </div>
  );
}

/* ═══ TEMPO CSV IMPORT TAB ══════════════════════ */
function TempoCsvImportTab() {
  const [csv,     setCsv]     = useState("");
  const [dryRun,  setDryRun]  = useState(true);
  const [result,  setResult]  = useState(null);
  const [loading, setLoading] = useState(false);
  const [showLog, setShowLog] = useState(false);
  const [projects, setProjects] = useState([]);
  const [showMapping, setShowMapping] = useState(false);
  const [rbFrom,  setRbFrom]  = useState("2026-01-01");
  const [rbTo,    setRbTo]    = useState("2026-05-17");
  const [rolling, setRolling] = useState(false);
  const [rbMsg,   setRbMsg]   = useState("");
  const fileRef = useRef(null);

  useEffect(()=>{ api("GET","/projects").then(p=>setProjects(p||[])); },[]);

  const doRollback = async () => {
    if (!rbFrom || !rbTo) return;
    if ((prompt(`ACHTUNG: Alle Arbeitstage + Buchungen von ${rbFrom} bis ${rbTo} werden gelöscht.\nNur wenn dieser Zeitraum nur aus CSV-Import besteht!\n\nZum Bestätigen LÖSCHEN eintippen:`)||"").trim().toUpperCase()!=="LÖSCHEN") return;
    setRolling(true); setRbMsg("");
    const r = await api("DELETE", `/admin/reset-range?from=${rbFrom}&to=${rbTo}`);
    if (r?.deleted != null) {
      setRbMsg(`✓ ${r.deleted.workdays} Arbeitstage + ${r.deleted.entries} Buchungen gelöscht – jetzt Projekte anlegen + CSV importieren`);
    } else {
      setRbMsg("❌ Fehler – Server neu starten");
    }
    setRolling(false);
  };

  const doImport = async () => {
    if (!csv.trim()) return;
    setLoading(true); setResult(null);
    const r = await api("POST", "/tempo-csv-import", { csv, dryRun });
    setResult(r || { error: "Keine Antwort vom Server" });
    setLoading(false);
  };

  const loadFile = e => {
    const f = e.target.files[0];
    if (!f) return;
    const reader = new FileReader();
    reader.onload = ev => setCsv(ev.target.result);
    reader.readAsText(f, "utf-8");
  };

  // Extract unique project keys from CSV for mapping hint
  const csvProjects = (() => {
    if (!csv) return [];
    const lines = csv.split("\n").slice(1).filter(l=>l.trim());
    const keys = new Set();
    lines.forEach(l => {
      const cols = l.split(",");
      // Project Key is usually column 18 or find by header
      const key = (cols[18]||"").replace(/"/g,"").trim();
      if (key && key !== "HAP") keys.add(key);
    });
    return [...keys];
  })();

  const unmappedKeys = csvProjects.filter(k =>
    !projects.some(p => p.jira_key?.toUpperCase()===k.toUpperCase())
  );

  return (
    <div style={{display:"flex",flexDirection:"column",gap:12}}>
      {/* Info */}
      <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"14px 18px"}}>
        <div style={{color:"var(--hi)",fontWeight:700,marginBottom:6}}>Tempo Benutzer-Export importieren</div>
        <div style={{color:"var(--text2)",fontSize:12,lineHeight:1.7}}>
          Aus Tempo exportieren: <b>Tempo → Berichte → Benutzerbericht → Als CSV exportieren</b><br/>
          Der Import erkennt automatisch Urlaubstage (HAP-Tickets), berechnet Start/Ende aus Datum + Dauer
          und legt Arbeitstage an. Duplikate werden übersprungen.
        </div>
      </div>

      {/* Projekt-Mapping Warnung */}
      {unmappedKeys.length > 0 && (
        <div style={{background:"rgba(180,210,41,0.08)",border:"1px solid rgba(180,210,41,0.3)",borderRadius:6,padding:"12px 14px"}}>
          <div style={{color:"var(--accent)",fontWeight:700,fontSize:12,marginBottom:6}}>
            ⚠ {unmappedKeys.length} Projekt-Kürzel ohne Mapping
          </div>
          <div style={{color:"var(--text2)",fontSize:11,marginBottom:8}}>
            Diese Kürzel aus der CSV haben kein passendes Projekt in Zeitprotokoll:
            {unmappedKeys.map(k=>(
              <span key={k} style={{background:"rgba(91,156,246,0.15)",color:"var(--blue)",border:"1px solid rgba(91,156,246,0.3)",borderRadius:3,padding:"1px 8px",fontSize:11,fontWeight:700,marginLeft:6}}>{k}</span>
            ))}
          </div>
          <div style={{color:"var(--text3)",fontSize:11}}>
            → Lege in <b>Projekte</b> ein Projekt an und trag das Jira-Kürzel ein. Dann nochmal importieren.
            Oder importiere erst und ordne nachträglich zu – Buchungen ohne Projekt werden trotzdem gespeichert.
          </div>
        </div>
      )}

      {/* Upload */}
      <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"16px 18px"}}>
        <div style={{display:"flex",gap:10,alignItems:"center",marginBottom:10}}>
          <label style={{display:"inline-flex",alignItems:"center",gap:6,background:"var(--bg3)",border:"1px solid var(--border2)",borderRadius:4,padding:"8px 14px",cursor:"pointer",fontSize:12,color:"var(--text)"}}>
            <Plus size={13}/> Tempo CSV laden
            <input ref={fileRef} type="file" accept=".csv" style={{display:"none"}} onChange={loadFile}/>
          </label>
          {csv && <span style={{color:"var(--green)",fontSize:11}}>✓ {csv.split("\n").length-1} Zeilen geladen</span>}
        </div>

        <div style={{display:"flex",gap:10,alignItems:"center",flexWrap:"wrap"}}>
          <label style={{display:"flex",alignItems:"center",gap:6,cursor:"pointer",fontSize:12,color:"var(--text2)"}}>
            <input type="checkbox" checked={dryRun} onChange={e=>setDryRun(e.target.checked)} style={{accentColor:"var(--green)"}}/>
            Testlauf (nichts wird gespeichert)
          </label>
          <Btn variant={dryRun?"default":"primary"} onClick={doImport} disabled={loading||!csv.trim()}>
            {loading?"⟳ Importiere…":dryRun?"Testlauf starten":"Jetzt importieren"}
          </Btn>
          {loading && <Btn variant="danger" onClick={()=>api("POST","/integrations/tempo/import/cancel")} size="sm">
            <Square size={12}/> Stopp
          </Btn>}
          {csv && <button style={{background:"transparent",border:"none",color:"var(--text3)",cursor:"pointer",fontSize:11,fontFamily:"inherit"}} onClick={()=>{setCsv("");setResult(null);}}>✕ Leeren</button>}
        </div>
      </div>

      {/* Result */}
      {result && (
        <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"14px 18px"}}>
          {result.error ? (
            <div style={{color:"var(--red)",fontSize:12}}>❌ {result.error}</div>
          ) : (
            <>
              <div style={{color:"var(--hi)",fontWeight:700,marginBottom:10}}>
                {result.dryRun ? "📋 Testlauf" : "✓ Importiert"} – {result.message}
              </div>
              <div style={{display:"flex",gap:20,flexWrap:"wrap",marginBottom:12}}>
                {[
                  ["Verarbeitet",  result.total,              "var(--text2)"],
                  ["Arbeitstage",  result.workdays,           "var(--hi)"],
                  ["Buchungen",    result.imported,           "var(--green)"],
                  ["Urlaubstage",  result.vacations,          "var(--blue)"],
                  ["Duplikate",    result.skipped_duplicate,  "var(--text3)"],
                  ["Fehler",       result.errors?.length||0,  "var(--red)"],
                ].map(([l,v,c])=>(
                  <div key={l}>
                    <div style={{color:"var(--text3)",fontSize:10}}>{l.toUpperCase()}</div>
                    <div style={{color:v>0?c:"var(--text3)",fontWeight:700,fontSize:20}}>{v}</div>
                  </div>
                ))}
              </div>

              {/* Preview */}
              {result.dryRun && result.preview?.length > 0 && (
                <div>
                  <div style={{color:"var(--text2)",fontWeight:700,fontSize:11,marginBottom:8}}>
                    Vorschau – {result.preview.filter(p=>p.type==="arbeitstag").length} Arbeitstage:
                  </div>
                  <div style={{maxHeight:320,overflow:"auto",border:"1px solid var(--border)",borderRadius:4}}>
                    {result.preview.sort((a,b)=>a.date.localeCompare(b.date)).map((day,di)=>(
                      <div key={di} style={{borderBottom:"1px solid var(--border)"}}>
                        <div style={{display:"flex",gap:12,padding:"7px 12px",background:"var(--bg3)",alignItems:"center"}}>
                          <span style={{color:"var(--hi)",fontWeight:700,minWidth:90}}>{fmtDate(day.date)}</span>
                          {day.type==="urlaub"
                            ? <span style={{color:"var(--blue)"}}>🏖 Urlaub {day.hours}h</span>
                            : <>
                                <span style={{color:"var(--text3)",fontSize:11}}>{day.wdStart}–{day.wdEnd}</span>
                                <span style={{color:"var(--accent)",fontSize:11,marginLeft:"auto"}}>{day.entries} Buchungen</span>
                              </>
                          }
                        </div>
                        {day.entries_detail?.map((e,i)=>(
                          <div key={i} style={{display:"flex",gap:10,padding:"4px 12px",borderTop:"1px solid #0a0c0e",fontSize:11}}>
                            <span style={{color:"var(--text3)",minWidth:90}}>{e.start}–{e.end}</span>
                            {e.ticket&&<span style={{color:"var(--blue)",fontWeight:700,minWidth:90}}>{e.ticket}</span>}
                            {e.project&&<span style={{color:"var(--hi)",minWidth:120}}>{e.project}</span>}
                            <span style={{color:"var(--text2)",flex:1,overflow:"hidden",textOverflow:"ellipsis",whiteSpace:"nowrap"}}>{e.comment}</span>
                            <span style={{color:"var(--accent)",fontWeight:700,flexShrink:0}}>{fmtH(e.durSecs||0)}</span>
                          </div>
                        ))}
                      </div>
                    ))}
                  </div>
                  <div style={{marginTop:8,color:"var(--green)",fontSize:11}}>
                    ✓ Alles korrekt? Haken bei "Testlauf" entfernen und importieren.
                  </div>
                </div>
              )}

              {result.errors?.length>0&&(
                <div style={{marginTop:8}}>
                  {result.errors.slice(0,5).map((e,i)=>(
                    <div key={i} style={{color:"var(--red)",fontSize:11}}>✗ {e.date} {e.time}: {e.error}</div>
                  ))}
                </div>
              )}
            </>
          )}
        </div>
      )}

      {/* ══ VOLLSTÄNDIGER RESET ═════════════════════ */}
      <div style={{background:"var(--bg2)",border:"2px solid rgba(214,69,69,0.5)",borderRadius:6,padding:"16px 18px"}}>
        <div style={{color:"var(--red)",fontWeight:700,fontSize:13,marginBottom:6}}>
          🗑 Zeitraum vollständig zurücksetzen
        </div>
        <div style={{color:"var(--text2)",fontSize:12,marginBottom:12,lineHeight:1.7}}>
          Löscht <b>alle</b> Arbeitstage, Buchungen und Logs im gewählten Zeitraum.<br/>
          <span style={{color:"var(--accent)"}}>⚠ Nur für Zeiträume verwenden die ausschließlich per CSV importiert wurden.</span><br/>
          <span style={{color:"var(--text3)"}}>Danach: Projekte anlegen → CSVs reimportieren → alles stimmt.</span>
        </div>
        <div style={{display:"flex",gap:10,alignItems:"flex-end",flexWrap:"wrap",marginBottom:10}}>
          <Field label="VON">
            <input style={{...inp,width:130}} type="date" value={rbFrom} onChange={e=>setRbFrom(e.target.value)}/>
          </Field>
          <Field label="BIS (inkl.)">
            <input style={{...inp,width:130}} type="date" value={rbTo} onChange={e=>setRbTo(e.target.value)}/>
          </Field>
          <div style={{paddingBottom:12}}>
            <Btn variant="danger" onClick={doRollback} disabled={!rbFrom||!rbTo||rolling}>
              <Trash2 size={13}/> {rolling?"Lösche…":"Alles löschen in diesem Zeitraum"}
            </Btn>
          </div>
        </div>
        {rbMsg&&<div style={{fontSize:12,color:rbMsg.startsWith("✓")?"var(--green)":"var(--red)",fontWeight:600}}>{rbMsg}</div>}
      </div>

      <div style={{display:"flex",gap:8}}>
        <button style={{background:"transparent",border:"none",color:"var(--blue)",cursor:"pointer",fontSize:11,fontFamily:"inherit",textDecoration:"underline"}} onClick={()=>setShowLog(v=>!v)}>
          {showLog?"▲ Log":"▼ Import-Log"}
        </button>
      </div>
      {showLog&&<LogViewer category="import" autoRefresh={loading} title="Import Log"/>}
    </div>
  );
}


/* ═══ PLANUNGS-SIDEBAR ══════════════════════════
   Erscheint rechts neben der Timer-Ansicht
   Zeigt: Heute geplant + Woche offen
═══════════════════════════════════════════════ */
const PRIORITY_COLORS = { 1:"var(--red)", 2:"var(--accent)", 3:"var(--text3)" };
const PRIORITY_LABELS = { 1:"Hoch", 2:"Mittel", 3:"Niedrig" };
const STATUS_COLORS   = { open:"var(--text2)", done:"var(--green)", moved:"var(--text3)" };

function PlanSidebar({ date, projects, onStartTimer }) {
  const [data,    setData]    = useState(null);
  const [adding,  setAdding]  = useState(false);
  const [newTitle,setNewTitle]= useState("");
  const [newEst,  setNewEst]  = useState(60);
  const [newPrio, setNewPrio] = useState(2);
  const [newProj, setNewProj] = useState("");

  const load = async () => {
    const r = await api("GET", `/plan/today?date=${date||today()}`);
    setData(r);
  };
  useEffect(() => { load(); }, [date]);

  const addTask = async () => {
    if (!newTitle.trim()) return;
    await api("POST", "/plan/tasks", {
      title: newTitle.trim(),
      estimated_minutes: newEst,
      priority: newPrio,
      project_id: newProj || null,
      scheduled_date: date || today(),
    });
    setNewTitle(""); setNewEst(60); setNewPrio(2); setNewProj(""); setAdding(false);
    load();
  };

  const toggleDone = async (task) => {
    const newStatus = task.status === "done" ? "open" : "done";
    await api("PUT", `/plan/tasks/${task.id}`, {
      ...task,
      status: newStatus,
      completed_at: newStatus === "done" ? now() : null,
    });
    load();
  };

  const moveTask = async (task) => {
    await api("POST", `/plan/tasks/${task.id}/move`);
    load();
  };

  const now = () => new Date().toISOString();
  const totalEstMin = data?.todayTasks?.reduce((s,t)=>s+(t.estimated_minutes||0),0)||0;
  const doneMin     = data?.todayTasks?.filter(t=>t.status==='done').reduce((s,t)=>s+(t.estimated_minutes||0),0)||0;

  return (
    <div style={{width:260,flexShrink:0,display:"flex",flexDirection:"column",gap:8}}>

      {/* HEUTE GEPLANT */}
      <div className="zp-card" style={{overflow:"hidden"}}>
        <div style={{padding:"10px 12px",borderBottom:"1px solid var(--line)",display:"flex",alignItems:"center",justifyContent:"space-between"}}>
          <span style={{fontWeight:600,fontSize:13}}>Heute geplant</span>
          <div style={{display:"flex",alignItems:"center",gap:6}}>
            {totalEstMin>0&&<span style={{color:"var(--text3)",fontSize:10}}>{fmtH(totalEstMin*60)}</span>}
            <button onClick={()=>setAdding(v=>!v)} style={{background:"var(--accent-fill)",color:"#fff",border:"none",borderRadius:6,width:22,height:22,cursor:"pointer",fontSize:14,lineHeight:"18px",textAlign:"center",display:"flex",alignItems:"center",justifyContent:"center"}}>
              <Plus size={11}/>
            </button>
          </div>
        </div>

        {/* Progress bar */}
        {totalEstMin>0&&(
          <div style={{height:2,background:"var(--border)"}}>
            <div style={{width:`${Math.min(100,(doneMin/totalEstMin)*100)}%`,height:"100%",background:"var(--green)",transition:"width 0.3s"}}/>
          </div>
        )}

        {/* Quick-Add */}
        {adding&&(
          <div style={{padding:"8px 10px",borderBottom:"1px solid var(--border)",background:"rgba(15,117,47,0.04)"}}>
            <input autoFocus style={{...inp,fontSize:11,marginBottom:6,width:"100%"}}
              placeholder="Aufgabe…" value={newTitle}
              onChange={e=>setNewTitle(e.target.value)}
              onKeyDown={e=>{if(e.key==="Enter")addTask(); if(e.key==="Escape")setAdding(false);}}/>
            <div style={{display:"flex",gap:5}}>
              <select style={{...sel,flex:1,fontSize:10}} value={newPrio} onChange={e=>setNewPrio(Number(e.target.value))}>
                <option value={1}>Hoch</option>
                <option value={2}>Mittel</option>
                <option value={3}>Niedrig</option>
              </select>
              <input type="number" style={{...inp,width:50,fontSize:10}} value={newEst} onChange={e=>setNewEst(Number(e.target.value))} min={5} step={15} title="Minuten"/>
              <Btn size="sm" variant="primary" onClick={addTask}>+</Btn>
            </div>
          </div>
        )}

        {/* Tasks */}
        {!data?.todayTasks?.length&&!adding&&(
          <div style={{color:"var(--text3)",fontSize:11,padding:"12px",textAlign:"center"}}>
            Keine Aufgaben heute
          </div>
        )}
        {(data?.todayTasks||[]).map(task=>(
          <div key={task.id} style={{display:"flex",alignItems:"flex-start",gap:6,padding:"7px 10px",borderBottom:"1px solid #0a0c0e",
            opacity:task.status==="done"?0.55:1}}>
            <button onClick={()=>toggleDone(task)}
              style={{width:14,height:14,borderRadius:3,border:`2px solid ${PRIORITY_COLORS[task.priority]||"var(--text3)"}`,
                background:task.status==="done"?"var(--green)":"transparent",cursor:"pointer",flexShrink:0,marginTop:2,
                display:"flex",alignItems:"center",justifyContent:"center"}}>
              {task.status==="done"&&<Check size={9} color="#fff"/>}
            </button>
            <div style={{flex:1,minWidth:0}}>
              <div style={{fontSize:11,color:task.status==="done"?"var(--text3)":"var(--text)",
                textDecoration:task.status==="done"?"line-through":"none",
                wordBreak:"break-word",lineHeight:1.4}}>{task.title}</div>
              <div style={{display:"flex",gap:6,marginTop:2,alignItems:"center",flexWrap:"wrap"}}>
                {task.jira_ticket&&<span style={{color:"var(--blue)",fontSize:9,fontWeight:700}}>{task.jira_ticket}</span>}
                {task.project_name&&<span style={{color:"var(--text3)",fontSize:9}}>{task.project_name}</span>}
                {task.estimated_minutes&&<span style={{color:"var(--text3)",fontSize:9}}>{task.estimated_minutes}m</span>}
              </div>
            </div>
            {task.status!=="done"&&(
              <button onClick={()=>moveTask(task)} title="Auf morgen verschieben"
                style={{background:"transparent",border:"none",color:"var(--text3)",cursor:"pointer",fontSize:10,flexShrink:0,padding:2}}>&rarr;</button>
            )}
          </div>
        ))}
      </div>

      {/* INBOX */}
      {data?.inbox?.length>0&&(
        <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,overflow:"hidden"}}>
          <div style={{padding:"6px 12px",borderBottom:"1px solid var(--border)"}}>
            <span style={{color:"var(--text3)",fontWeight:700,fontSize:10,letterSpacing:"0.05em"}}>INBOX</span>
          </div>
          {data.inbox.map(task=>(
            <div key={task.id} style={{display:"flex",alignItems:"center",gap:6,padding:"5px 10px",borderBottom:"1px solid #0a0c0e"}}>
              <div style={{width:6,height:6,borderRadius:"50%",background:PRIORITY_COLORS[task.priority],flexShrink:0}}/>
              <span style={{fontSize:10,color:"var(--text2)",flex:1,overflow:"hidden",textOverflow:"ellipsis",whiteSpace:"nowrap"}}>{task.title}</span>
              <button onClick={async()=>{await api("PUT",`/plan/tasks/${task.id}`,{...task,scheduled_date:date||today()});load();}}
                style={{background:"transparent",border:"none",color:"var(--green)",cursor:"pointer",fontSize:10}}>+</button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/* ═══ PLANUNGS-VOLLANSICHT ══════════════════════ */
function PlanView() {
  const [tab,      setTab]      = useState("heute");
  const [tasks,    setTasks]    = useState([]);
  const [projects, setProjects] = useState([]);
  const [editTask, setEditTask] = useState(null);
  const [form,     setForm]     = useState({title:"",project_id:"",jira_ticket:"",estimated_minutes:60,priority:2,scheduled_date:today(),recurrence:null,notes:""});

  const viewDate = tab==="heute" ? today() : null;
  const viewWeek = tab==="woche" ? getISOWeek(new Date()) : null;

  const load = async () => {
    const params = tab==="heute" ? `date=${today()}&status=all`
                 : tab==="woche" ? `week=${getISOWeek(new Date())}&status=all`
                 : "status=all";
    const [t, p] = await Promise.all([
      api("GET", `/plan/tasks?${params}`),
      api("GET", "/projects"),
    ]);
    setTasks(t||[]); setProjects(p||[]);
  };
  useEffect(()=>{ load(); },[tab]);

  function getISOWeek(d) {
    const w = isoWeek(ld(d));
    return `${w.year}-W${String(w.week).padStart(2,"0")}`;
  }

  const save = async () => {
    if (editTask?.id) await api("PUT", `/plan/tasks/${editTask.id}`, form);
    else              await api("POST", "/plan/tasks", {...form, scheduled_date: viewDate, scheduled_week: viewWeek});
    setEditTask(null); setForm({title:"",project_id:"",jira_ticket:"",estimated_minutes:60,priority:2,scheduled_date:today(),recurrence:null,notes:""});
    load();
  };

  const toggle = async t => {
    const ns = t.status==="done"?"open":"done";
    await api("PUT", `/plan/tasks/${t.id}`, {...t, status:ns, completed_at:ns==="done"?new Date().toISOString():null});
    load();
  };

  const groupedByDate = tasks.reduce((acc,t)=>{
    const k = t.scheduled_date||"Inbox";
    (acc[k]=acc[k]||[]).push(t); return acc;
  },{});

  return (
    <div>
      <div style={{display:"flex",gap:6,marginBottom:16,flexWrap:"wrap"}}>
        {[["heute","Heute"],["woche","Diese Woche"],["alle","Alle offen"]].map(([k,l])=>(
          <button key={k} style={{border:"1px solid "+(tab===k?"var(--accent)":"var(--line)"),background:tab===k?"var(--accent-soft)":"var(--card)",color:tab===k?"var(--accent)":"var(--text2)",borderRadius:20,padding:"5px 14px",fontSize:12,fontWeight:tab===k?600:500,cursor:"pointer"}} onClick={()=>setTab(k)}>{l}</button>
        ))}
        <div style={{marginLeft:"auto"}}>
          <Btn variant="primary" size="sm" onClick={()=>{setEditTask({}); setForm({...form,scheduled_date:today()});}}>
            <Plus size={13}/> Neue Aufgabe
          </Btn>
        </div>
      </div>

      {/* Neues/Edit Formular */}
      {editTask&&(
        <div style={{background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:6,padding:"14px 16px",marginBottom:12}}>
          <div style={{display:"grid",gridTemplateColumns:"2fr 1fr 1fr",gap:10,marginBottom:10}}>
            <Field label="TITEL">
              <input style={inp} value={form.title} onChange={e=>setForm(v=>({...v,title:e.target.value}))} placeholder="Aufgabentitel…" autoFocus/>
            </Field>
            <Field label="PRIORITÄT">
              <select style={sel} value={form.priority} onChange={e=>setForm(v=>({...v,priority:Number(e.target.value)}))}>
                <option value={1}>🔴 Hoch</option>
                <option value={2}>🟡 Mittel</option>
                <option value={3}>⚪ Niedrig</option>
              </select>
            </Field>
            <Field label="SCHÄTZUNG (min)">
              <input style={inp} type="number" min={5} step={15} value={form.estimated_minutes} onChange={e=>setForm(v=>({...v,estimated_minutes:Number(e.target.value)}))}/>
            </Field>
          </div>
          <div style={{display:"grid",gridTemplateColumns:"1fr 1fr 1fr",gap:10,marginBottom:10}}>
            <Field label="PROJEKT">
              <select style={sel} value={form.project_id||""} onChange={e=>setForm(v=>({...v,project_id:e.target.value}))}>
                <option value="">– kein –</option>
                {projects.map(p=><option key={p.id} value={p.id}>{p.name}</option>)}
              </select>
            </Field>
            <Field label="JIRA TICKET">
              <input style={inp} value={form.jira_ticket||""} onChange={e=>setForm(v=>({...v,jira_ticket:e.target.value}))} placeholder="ISMS-42"/>
            </Field>
            <Field label="DATUM">
              <input style={inp} type="date" value={form.scheduled_date||""} onChange={e=>setForm(v=>({...v,scheduled_date:e.target.value}))}/>
            </Field>
          </div>
          <Field label="NOTIZEN">
            <input style={inp} value={form.notes||""} onChange={e=>setForm(v=>({...v,notes:e.target.value}))} placeholder="Optionale Notizen…"/>
          </Field>
          <div style={{display:"flex",gap:8,justifyContent:"flex-end",marginTop:10}}>
            <Btn onClick={()=>setEditTask(null)}>Abbrechen</Btn>
            <Btn variant="primary" onClick={save} disabled={!form.title.trim()}><Check size={13}/> Speichern</Btn>
          </div>
        </div>
      )}

      {/* Aufgaben */}
      {Object.entries(groupedByDate).sort().map(([dateKey, dayTasks])=>(
        <div key={dateKey} style={{marginBottom:12}}>
          {tab==="alle"&&<div style={{color:"var(--text3)",fontSize:10,fontWeight:700,letterSpacing:"0.1em",marginBottom:6,paddingBottom:4,borderBottom:"1px solid var(--border)"}}>{dateKey==="Inbox"?"INBOX":fmtDate(dateKey)}</div>}
          {dayTasks.map(task=>(
            <div key={task.id} style={{display:"flex",alignItems:"center",gap:10,padding:"8px 12px",background:"var(--bg2)",border:"1px solid var(--border)",borderRadius:5,marginBottom:4,opacity:task.status==="done"?0.6:1}}>
              <button onClick={()=>toggle(task)}
                style={{width:18,height:18,borderRadius:4,border:`2px solid ${PRIORITY_COLORS[task.priority]||"var(--text3)"}`,background:task.status==="done"?"var(--green)":"transparent",cursor:"pointer",flexShrink:0,display:"flex",alignItems:"center",justifyContent:"center"}}>
                {task.status==="done"&&<Check size={11} color="#fff"/>}
              </button>
              <div style={{flex:1,minWidth:0}}>
                <div style={{fontSize:12,color:"var(--text)",textDecoration:task.status==="done"?"line-through":"none",fontWeight:task.status!=="done"?500:400}}>{task.title}</div>
                <div style={{display:"flex",gap:8,marginTop:2,alignItems:"center"}}>
                  {task.jira_ticket&&<span style={{color:"var(--blue)",fontSize:10,fontWeight:700}}>{task.jira_ticket}</span>}
                  {task.project_name&&<span style={{color:"var(--text3)",fontSize:10}}>{task.project_name}</span>}
                  <span style={{color:"var(--text3)",fontSize:10}}>{task.estimated_minutes}m</span>
                </div>
              </div>
              <div style={{display:"flex",gap:4}}>
                <button onClick={()=>{setEditTask(task);setForm({...task,project_id:task.project_id||"",jira_ticket:task.jira_ticket||"",notes:task.notes||"",recurrence:task.recurrence||null});}}
                  style={TV.iconBtn}><Pencil size={12}/></button>
                <button onClick={async()=>{await api("POST",`/plan/tasks/${task.id}/move`);load();}} title="Verschieben"
                  style={TV.iconBtn}>&rarr;</button>
                <button onClick={async()=>{if(confirm(`Aufgabe „${task.title||""}“ löschen?`))await api("DELETE",`/plan/tasks/${task.id}`);load();}}
                  style={{...TV.iconBtn,color:"var(--red)"}}><Trash2 size={12}/></button>
              </div>
            </div>
          ))}
        </div>
      ))}
      {tasks.length===0&&!editTask&&(
        <div style={{color:"var(--text3)",textAlign:"center",padding:"30px 0",fontSize:12}}>Keine Aufgaben – klick „Neue Aufgabe" um zu starten</div>
      )}
    </div>
  );
}

/* ═══ PAUSE ROW – bearbeiten & löschen ═════════ */
function PauseRow({ pause, onRefresh }) {
  const [editing, setEditing] = useState(false);
  const [start,   setStart]   = useState(toLocal(pause.start_time));
  const [end,     setEnd]     = useState(toLocal(pause.end_time));
  const [error,   setError]   = useState("");
  const date = pause.date || (pause.start_time ? ld(new Date(pause.start_time)) : today());

  const save = async () => {
    setError("");
    const startISO = new Date(`${date}T${start}:00`).toISOString();
    const endISO   = end ? new Date(`${date}T${end}:00`).toISOString() : null;
    const res = await api("PUT", `/pauses/${pause.id}`, { start_time: startISO, end_time: endISO });
    if (!res || res.error) { setError(res?.error || "Fehler beim Speichern"); return; }
    setEditing(false);
    onRefresh();
  };

  const del = async () => {
    if (!confirm("Pause löschen?")) return;
    await api("DELETE", `/pauses/${pause.id}`);
    onRefresh();
  };

  if (editing) return (
    <div className="zp-entry pause" style={{display:"block"}}>
      <div style={{display:"flex",alignItems:"center",gap:8}}>
        <Coffee size={12} color="var(--accent)"/>
        <input type="time" value={start} onChange={e=>setStart(e.target.value)}
          style={{...inp, width:90, fontSize:11, padding:"2px 6px"}}/>
        <span style={{color:"var(--text3)"}}>–</span>
        <input type="time" value={end} onChange={e=>setEnd(e.target.value)}
          style={{...inp, width:90, fontSize:11, padding:"2px 6px"}}/>
        <Btn size="sm" variant="primary" onClick={save}><Check size={11}/></Btn>
        <Btn size="sm" onClick={()=>{setEditing(false);setError("");}}><X size={11}/></Btn>
      </div>
      {error && <div style={{color:"var(--red)",fontSize:11,marginTop:4}}>{error}</div>}
    </div>
  );

  const durSecs = pause.end_time
    ? Math.floor((new Date(pause.end_time) - new Date(pause.start_time)) / 1000)
    : null;

  return (
    <div className="zp-entry pause">
      <div className="info">
        <div className="t">Pause</div>
        <div className="m">{fmtTime(pause.start_time)} – {pause.end_time ? fmtTime(pause.end_time) : "läuft"}</div>
      </div>
      <div className="act">
        <button onClick={()=>setEditing(true)} style={TV.iconBtn} title="Pause bearbeiten">
          <Pencil size={11}/>
        </button>
        <button onClick={del} style={{...TV.iconBtn, color:"var(--red)"}} title="Pause löschen">
          <Trash2 size={11}/>
        </button>
      </div>
      {durSecs && <span className="dur">{fmtH(durSecs)}</span>}
    </div>
  );
}

/* ═══ LOG ROW – Stundenlog bearbeiten & löschen ══════════ */
function LogRow({ log, onRefresh }) {
  const [editing, setEditing] = useState(false);
  const [text,    setText]    = useState(log.text);

  const save = async () => {
    await api("PUT", `/daily-log/${log.id}`, { text: text.trim() });
    setEditing(false); onRefresh();
  };
  const del = async () => {
    if (!confirm("Log-Eintrag löschen?")) return;
    await api("DELETE", `/daily-log/${log.id}`);
    onRefresh();
  };

  if (editing) return (
    <div style={{display:"flex",alignItems:"center",gap:8,padding:"5px 14px",borderBottom:"1px solid #0a0c0e",background:"rgba(0,0,0,0.07)"}}>
      <span style={{color:"var(--text3)",fontSize:10,minWidth:90}}>{fmtTime(log.timestamp)}</span>
      <input autoFocus value={text} onChange={e=>setText(e.target.value)}
        onKeyDown={e=>{if(e.key==="Enter")save();if(e.key==="Escape")setEditing(false);}}
        style={{...inp,flex:1,fontSize:11,padding:"2px 6px"}}/>
      <Btn size="sm" variant="primary" onClick={save}><Check size={11}/></Btn>
      <Btn size="sm" onClick={()=>setEditing(false)}><X size={11}/></Btn>
    </div>
  );

  return (
    <div style={{display:"flex",alignItems:"center",gap:8,padding:"5px 14px",borderBottom:"1px solid #0a0c0e",background:"rgba(0,0,0,0.05)"}}>
      <span style={{color:"var(--text3)",fontSize:10,minWidth:90}}>{fmtTime(log.timestamp)}</span>
      <FileText size={11} color="var(--text3)"/>
      <span style={{color:"var(--text2)",fontSize:11,flex:1}}>{log.text}</span>
      <button style={TV.iconBtn} onClick={()=>setEditing(true)} title="Bearbeiten"><Pencil size={11}/></button>
      <button style={{...TV.iconBtn,color:"var(--red)"}} onClick={del} title="Löschen"><Trash2 size={11}/></button>
    </div>
  );
}

/* ═══ LOG ENTRY BUTTONS – inline für alle Tage ═══ */
function LogEntryButtons({ log, onRefresh }) {
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(log.text);

  if (editing) return (
    <>
      <input autoFocus value={text} onChange={e=>setText(e.target.value)}
        onKeyDown={e=>{if(e.key==="Enter"){api("PUT",`/daily-log/${log.id}`,{text:text.trim()}).then(onRefresh);setEditing(false);}if(e.key==="Escape")setEditing(false);}}
        style={{...inp,flex:"none",width:200,fontSize:11,padding:"1px 6px"}}/>
      <Btn size="sm" variant="primary" onClick={()=>{api("PUT",`/daily-log/${log.id}`,{text:text.trim()}).then(()=>{setEditing(false);onRefresh();})}}><Check size={10}/></Btn>
      <Btn size="sm" onClick={()=>setEditing(false)}><X size={10}/></Btn>
    </>
  );
  return (
    <>
      <button style={TV.iconBtn} onClick={()=>setEditing(true)} title="Bearbeiten"><Pencil size={11}/></button>
      <button style={{...TV.iconBtn,color:"var(--red)"}} title="Löschen"
        onClick={()=>{if(confirm("Eintrag löschen?"))api("DELETE",`/daily-log/${log.id}`).then(onRefresh);}}>
        <Trash2 size={11}/>
      </button>
    </>
  );
}


/* Einstellungen › Jira & Tempo: eine Anbindung, drei Bereiche */
function JiraTempoSettings({ initial }) {
  const [sub, setSub] = useState(initial || "jira");
  const chip = (k, l) => (
    <button key={k} onClick={()=>setSub(k)} style={{border:"1px solid "+(sub===k?"var(--accent)":"var(--line)"),background:sub===k?"var(--accent-soft)":"var(--card)",color:sub===k?"var(--accent)":"var(--text2)",borderRadius:8,padding:"5px 12px",fontSize:12,fontWeight:sub===k?600:500}}>{l}</button>);
  return (
    <div>
      <div style={{display:"flex",gap:6,marginBottom:12,alignItems:"center"}}>
        {chip("jira","Zugang & Projekte")}{chip("tempo","Tempo-Übertragung")}{!isServerMode()&&chip("csv","Tempo-Import (CSV)")}
        <span style={{marginLeft:"auto",fontSize:11,color:"var(--text2)"}}>Jira und Tempo nutzen dieselbe Anbindung</span>
      </div>
      {sub==="jira" && <JiraSettingsTab/>}
      {sub==="tempo" && <TempoSettingsTab/>}
      {sub==="csv" && !isServerMode() && <TempoCsvImportTab/>}
    </div>);
}

/* Kalender-Abo: Adresse zum Kopieren (webcal:// versucht der Mac-Kalender per https – das geht lokal nicht) */
function AboUrl({ url }) {
  const [ok, setOk] = useState(false);
  const copy = async e => { try { await navigator.clipboard.writeText(url); } catch (_) { const i = e.currentTarget.previousSibling; i.select(); document.execCommand("copy"); } setOk(true); setTimeout(() => setOk(false), 2000); };
  return <span style={{display:"inline-flex",gap:6,alignItems:"center",flexWrap:"wrap"}}>
    <input readOnly value={url} onFocus={e=>e.target.select()} style={{width:330,maxWidth:"100%",fontSize:12,padding:"6px 8px",fontFamily:"ui-monospace, monospace"}}/>
    <button onClick={copy} style={{padding:"7px 14px",borderRadius:7,fontWeight:600,border:"none",background:"var(--accent-fill, var(--accent))",color:"#fff",cursor:"pointer"}}>{ok ? "Kopiert ✓" : "Adresse kopieren"}</button>
  </span>;
}
