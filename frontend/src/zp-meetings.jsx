/* ═══════════════════════════════════════════════════════════════════
   Meetings & Kalender: Buchungsvorschläge + Einstellungen „Erkennung“
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback } from "react";
import { CalendarClock, Video, Check, X, RefreshCw, Plus } from "lucide-react";
import { api, call, Box, lab, inpS, btn, hm, CreateTicketDialog } from "./zp-ui.jsx";

const hmInput = iso => { const d = new Date(iso); return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`; };
const withTime = (iso, t) => { const d = new Date(iso); const [h, m] = t.split(":").map(Number); d.setHours(h, m, 0, 0); return d.toISOString(); };
const dayLbl = d => { const t = new Date(); const k = x => x.toISOString().slice(0, 10); const dd = new Date(d + "T12:00:00");
  const loc = x => `${x.getFullYear()}-${String(x.getMonth() + 1).padStart(2, "0")}-${String(x.getDate()).padStart(2, "0")}`;
  return d === loc(t) ? "Heute" : dd.toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit" }); };

function Row({ s, projects, onDone }) {
  const [pid, setPid] = useState(s.rule?.project_id || s.best?.project_id || "");
  const [tasks, setTasks] = useState([]);
  const [tid, setTid] = useState(s.rule?.task_id || s.best?.task_id || "");
  const [from, setFrom] = useState(hmInput(s.start_time));
  const [to, setTo] = useState(hmInput(s.end_time));
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false); const [newT, setNewT] = useState(false); const [note, setNote] = useState("");
  useEffect(() => { if (!pid) { setTasks([]); return; } api("GET", `/tasks?projectId=${pid}`).then(t => setTasks(Array.isArray(t) ? t.filter(x => x.status !== "done" && x.status !== "closed") : [])); }, [pid]);

  const book = async () => {
    setBusy(true); setErr("");
    const r = await call("POST", "/meetings/book", { key: s.key, title: s.title, project_id: pid, task_id: tid || null, comment: note.trim() || undefined,
      start_time: withTime(s.start_time, from), end_time: withTime(s.end_time, to), activity: s.rule?.activity || null });
    setBusy(false);
    if (r.error) setErr(r.error); else onDone();
  };
  const dismiss = async () => { await call("POST", "/meetings/dismiss", { key: s.key }); onDone(); };

  return (
    <div style={{ padding: "10px 0", borderTop: "1px solid var(--line)" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        {s.cal ? <CalendarClock size={15} style={{ color: "var(--accent)" }} /> : <Video size={15} style={{ color: "var(--accent)" }} />}
        <div style={{ fontWeight: 600, flex: 1, minWidth: 140 }}>{s.title}</div>
        <span style={{ color: "var(--text2)", fontSize: 12 }}>{dayLbl(s.date)} · {s.sources.join(" + ")}{s.running ? " · läuft noch" : ""}</span>
      </div>
      <div style={{ display: "flex", gap: 6, alignItems: "center", flexWrap: "wrap", marginTop: 6 }}>
        <input type="time" value={from} onChange={e => setFrom(e.target.value)} style={{ padding: "5px 7px", width: 112 }} />
        <span>–</span>
        <input type="time" value={to} onChange={e => setTo(e.target.value)} style={{ padding: "5px 7px", width: 112 }} />
        <select value={pid} onChange={e => { setPid(e.target.value); setTid(""); }} style={{ padding: "5px 7px", minWidth: 150, flex: 1 }}>
          <option value="">Projekt wählen …</option>
          {projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
        </select>
        {tasks.length > 0 && <select value={tid} onChange={e => setTid(e.target.value)} style={{ padding: "5px 7px", minWidth: 150, flex: 1 }}>
          <option value="">(keine Aufgabe)</option>
          {tasks.map(t => <option key={t.id} value={t.id}>{t.jira_ticket ? `${t.jira_ticket} ` : ""}{t.title}</option>)}
        </select>}
        <button disabled={busy || !pid} onClick={book} style={{ ...btn(true), opacity: pid ? 1 : 0.5 }}><Check size={14} /> Buchen</button>
        <button onClick={() => setNewT(true)} title="Kein passendes Ticket – neues anlegen" style={btn(false)}><Plus size={14} /> Ticket</button>
        <button onClick={dismiss} title="Nicht buchen" style={btn(false)}><X size={14} /></button>
        {newT && <CreateTicketDialog title={s.title} projectId={pid} onClose={() => setNewT(false)}
          onDone={r => { setNewT(false); const np = String(r.local_project_id || ""); api("GET", `/tasks?projectId=${np}`).then(t => { setTasks(Array.isArray(t) ? t.filter(x => x.status !== "done" && x.status !== "closed") : []); setPid(np); setTid(r.task_id ? String(r.task_id) : ""); }); }} />}
      </div>
      <input value={note} onChange={e => setNote(e.target.value)} placeholder="Kommentar zur Buchung (optional)" style={{ padding: "5px 7px", width: "100%", marginTop: 6, boxSizing: "border-box" }} />
      {s.rule && <div style={{ color: "var(--text2)", fontSize: 11, marginTop: 3 }}>Wie beim letzten Mal vorbelegt ({s.rule.hits}×)</div>}
      {!s.rule && s.best && <div style={{ color: "var(--text2)", fontSize: 11, marginTop: 3 }}>Vorschlag: {s.best.key ? s.best.key + " " : ""}{s.best.title} ({s.best.score} % – {s.best.reasons.join(", ")})</div>}
      {err && <div style={{ color: "var(--red)", fontSize: 12, marginTop: 3 }}>{err}</div>}
    </div>
  );
}

/* Laufender Termin → Timer vorbelegt starten (ab Terminbeginn oder jetzt) */
export function CurrentMeetingBar({ timer, onChanged }) {
  const [d, setD] = useState(null); const [hidden, setHidden] = useState(() => { try { return sessionStorage.getItem("zp-mtg-hide") || ""; } catch (_) { return ""; } });
  const [msg, setMsg] = useState("");
  const load = useCallback(() => api("GET", "/meetings/now").then(r => setD(r && !r.error ? r : null)), []);
  useEffect(() => { load(); const t = setInterval(load, 60000); return () => clearInterval(t); }, [load]);
  const ev = d?.event;
  if (!ev || d.state === "booked" || hidden === ev.uid) return null;
  const pick = d.rule ? { project_id: d.rule.project_id, task_id: d.rule.task_id, key: null, title: null, project_name: d.rule.project_name, why: `so ${d.rule.hits}× gebucht` }
    : d.best ? { ...d.best, why: d.best.reasons.join(", ") } : null;
  const onIt = timer?.active && (timer.comment === ev.title || (pick && timer.task?.id && timer.task.id === pick.task_id));
  if (onIt) return null;
  const started = Date.parse(ev.start_time) <= Date.now();
  const start = async from => {
    if (!pick?.project_id) { location.hash = "kalender"; return; }
    const r = await call("POST", "/timer/start", { project_id: pick.project_id, task_id: pick.task_id || null, comment: ev.title, activity: d.rule?.activity || null, start_time: from ? ev.start_time : undefined });
    if (r.error) setMsg(r.error); else { setMsg(""); onChanged && onChanged(); }
  };
  const hide = () => { try { sessionStorage.setItem("zp-mtg-hide", ev.uid); } catch (_) {} setHidden(ev.uid); };
  return (
    <div className="zp-card" style={{ padding: "10px 14px", display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap", borderLeft: "3px solid var(--accent)" }}>
      <CalendarClock size={18} style={{ color: "var(--accent)", flexShrink: 0 }} />
      <div style={{ flex: 1, minWidth: 220 }}>
        <div style={{ fontWeight: 600 }}>{started ? "Termin läuft" : "Termin gleich"}: {ev.title} <span style={{ fontWeight: 400, color: "var(--text2)" }}>{hm(ev.start_time)}–{hm(ev.end_time)}</span></div>
        <div style={{ fontSize: 12, color: "var(--text2)" }}>{pick ? <>Vorbelegt: {pick.key && <b>{pick.key} </b>}{pick.title || ""} · {pick.project_name} <span title={pick.why}>({pick.why})</span></> : "Kein passendes Ticket gefunden – im Kalender wählen"}</div>
        {msg && <div style={{ fontSize: 12, color: "var(--stop)" }}>{msg}</div>}
      </div>
      {pick && started && <button style={btn(true)} onClick={() => start(true)}>Ab {hm(ev.start_time)} buchen</button>}
      {pick && <button style={btn(!started)} onClick={() => start(false)}>Jetzt starten</button>}
      <button style={btn(false)} onClick={() => { location.hash = "kalender"; }}>{pick ? "Anderes Ticket" : "Ticket wählen"}</button>
      <button className="zp-nav" title="Für diesen Termin ausblenden" aria-label="Ausblenden" onClick={hide}><X size={14} /></button>
    </div>);
}

/* Karte oben im Heute-Bereich: erscheint nur, wenn es etwas vorzuschlagen gibt */
export function MeetingSuggestions({ projects = [], onChanged, days = 3, emptyText }) {
  const [list, setList] = useState([]);
  const load = useCallback(() => api("GET", `/meetings/suggestions?days=${days}`).then(r => setList(Array.isArray(r) ? r : [])), [days]);
  useEffect(() => { load(); const t = setInterval(load, 60000); return () => clearInterval(t); }, [load]);
  if (!list.length) return emptyText ? <div className="zp-card" style={{ padding: "12px 16px", color: "var(--text2)", fontSize: 13 }}>{emptyText}</div> : null;
  const done = () => { load(); onChanged && onChanged(); };
  return (
    <div className="zp-card" style={{ padding: "12px 16px" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <div style={{ fontWeight: 600, fontSize: 14, flex: 1 }}>Termine & Meetings – buchen?</div>
        <span style={{ color: "var(--text2)", fontSize: 12 }}>{list.length} Vorschlag{list.length === 1 ? "" : "e"}</span>
        <button onClick={async () => { await call("POST", "/meetings/dismiss", { keys: list.map(x => x.key) }); done(); }} style={{ ...btn(false), padding: "3px 9px", fontSize: 12 }}>Alle ignorieren</button>
      </div>
      {list.slice(0, 6).map(s => <Row key={s.key} s={s} projects={projects} onDone={done} />)}
      {list.length > 6 && <div style={{ color: "var(--text2)", fontSize: 12, paddingTop: 6 }}>… und {list.length - 6} weitere (erst die oberen erledigen)</div>}
    </div>
  );
}

/* ═══ Einstellungen → Erkennung ═══ */
export function MeetingSettingsTab() {
  const [f, setF] = useState({ meeting_detect: "1", meeting_ask: "1", meeting_browser: "1", meeting_calendar: "1", meeting_calendar_exclude: "", meeting_min_minutes: "3" });
  const [st, setSt] = useState(null);
  const [msg, setMsg] = useState(null);
  const [evs, setEvs] = useState([]);
  const loadSt = useCallback(() => { api("GET", "/meetings/status").then(setSt); api("GET", "/meetings/events?days=2").then(r => setEvs(Array.isArray(r) ? r : [])); }, []);
  useEffect(() => {
    api("GET", "/settings").then(s => s && setF(v => ({ ...v, meeting_detect: s.meeting_detect === "0" ? "0" : "1", meeting_ask: s.meeting_ask === "0" ? "0" : "1", meeting_browser: s.meeting_browser === "0" ? "0" : "1", meeting_calendar: s.meeting_calendar === "0" ? "0" : "1",
      meeting_calendar_exclude: s.meeting_calendar_exclude ?? "Geburtstage,Birthdays,Feiertage,Holidays,Deutsche Feiertage,Siri Suggestions", meeting_min_minutes: s.meeting_min_minutes || "3" })));
    loadSt(); const t = setInterval(loadSt, 10000); return () => clearInterval(t);
  }, [loadSt]);
  const save = async () => { const r = await call("PUT", "/settings", f); setMsg(r.error ? { err: 1, text: r.error } : { text: "Gespeichert" }); loadSt(); };
  const toggle = (k, label, desc) => (
    <button onClick={() => setF(v => ({ ...v, [k]: v[k] === "1" ? "0" : "1" }))}
      style={{ display: "block", width: "100%", textAlign: "left", padding: "10px 12px", borderRadius: 8, marginBottom: 8,
        border: `1px solid ${f[k] === "1" ? "var(--accent)" : "var(--line)"}`, background: f[k] === "1" ? "var(--accent-soft)" : "var(--card)" }}>
      <div style={{ fontWeight: 600 }}>{f[k] === "1" ? "● " : "○ "}{label}</div>
      <div style={{ color: "var(--text2)", fontSize: 12 }}>{desc}</div>
    </button>);
  const d = st?.diag || {};
  const age = st?.signal_age_s;
  return (
    <div>
      <Box title="Meetings & Kalender erkennen" sub="Es wird nichts mitgehört – kein Mikrofon, keine Kamera. Die Mac-App schaut nur, welche Meeting-Programme laufen, und fragt dann nach. Gebucht wird nie ungefragt.">
        {toggle("meeting_detect", "Meeting-Programme erkennen", "Teams, Zoom, Webex, Slack: beim Start des Programms; bei Zoom zusätzlich, sobald eine Besprechung läuft.")}
        {toggle("meeting_ask", "Nachfragen „Timer starten?“", "Beim Start eines Meeting-Programms bzw. Meetings fragt das Schnellfenster nach; am Ende eines erkannten Meetings „Timer stoppen?“.")}
        {toggle("meeting_browser", "Meetings im Browser erkennen", "Prüft lokal die Adresse des aktiven Tabs (Chrome, Safari, Edge, Brave) auf Google Meet, Jitsi, BigBlueButton, Teams/Zoom im Web. Weitergegeben wird nur der Dienstname, nie die Adresse.")}
        {toggle("meeting_calendar", "macOS-Kalender einlesen", "Termine der letzten 7 Tage als Buchungsvorschlag. Ganztägige Termine werden ignoriert.")}
        <label style={lab}>Kalender ausschließen (kommagetrennt)</label>
        <input style={inpS} value={f.meeting_calendar_exclude} onChange={e => setF(v => ({ ...v, meeting_calendar_exclude: e.target.value }))} />
        <label style={lab}>Mindestdauer erkannter Meetings (Minuten)</label>
        <input style={{ ...inpS, width: 100 }} type="number" min="1" max="60" value={f.meeting_min_minutes} onChange={e => setF(v => ({ ...v, meeting_min_minutes: e.target.value }))} />
        <div style={{ marginTop: 12, display: "flex", gap: 8, alignItems: "center" }}>
          <button style={btn(true)} onClick={save}>Speichern</button>
          <button style={btn(false)} onClick={async () => { await call("POST", "/meetings/resync"); setMsg({ text: "Kalender wird beim nächsten Takt (≤ 30 s) neu gelesen" }); }}><RefreshCw size={14} /> Kalender neu lesen</button>
          {msg && <span style={{ color: msg.err ? "var(--red)" : "var(--green)", fontSize: 12 }}>{msg.text}</span>}
        </div>
      </Box>

      <Box title="Status der Erkennung" sub="Hilft bei der Fehlersuche, wenn nichts vorgeschlagen wird.">
        {!st ? "…" : (
          <div style={{ display: "grid", gridTemplateColumns: "150px 1fr", gap: "4px 10px", fontSize: 13 }}>
            <div style={{ color: "var(--text2)" }}>Mac-App meldet</div><div>{age == null ? "noch nie (App aktualisiert & neu gestartet?)" : age < 90 ? `ja (vor ${age} s)` : `zuletzt vor ${Math.round(age / 60)} min`}</div>
            <div style={{ color: "var(--text2)" }}>Programme offen</div><div>{(d.running || []).join(", ") || "keins"}</div>
            <div style={{ color: "var(--text2)" }}>Browser-Zugriff</div><div>{d.browser || "–"}</div>
            <div style={{ color: "var(--text2)" }}>Kalender</div><div>{d.cal || st.calendar_sync?.diag?.cal || "–"}{st.calendar_sync ? ` · ${st.calendar_sync.count} Termine, Stand ${hm(st.calendar_sync.at)}` : ""}</div>
            <div style={{ color: "var(--text2)" }}>Aktuell erkannt</div><div>{st.open_meeting ? `${st.open_meeting.app} seit ${hm(st.open_meeting.start_time)}` : "kein Meeting"}</div>
          </div>)}
        <div style={{ color: "var(--text2)", fontSize: 12, marginTop: 10 }}>
          Kalender: Die Mac-App liest alle 10 Minuten die App „Kalender“ (inkl. dort eingebundener Exchange-/Google-Konten) – ohne Zusatzsoftware. Beim ersten Mal fragt macOS nach Kalenderzugriff („Vollzugriff“). Kommt keine Frage oder steht dort „verweigert“: im Terminal <code>bash ~/zeitprotokoll/desktop/kalender-freigeben.sh</code>, danach Systemeinstellungen › Datenschutz &amp; Sicherheit › Kalender › Zeitprotokoll prüfen.
        </div>
      </Box>

      <Box title="Eingelesene Termine (gestern & heute)" sub="So siehst du, ob der Kalender ankommt. Ganztägige Termine und ausgeschlossene Kalender fehlen absichtlich."
        right={<button style={{ ...btn(false), padding: "3px 9px", fontSize: 12 }} onClick={loadSt}><RefreshCw size={12} /></button>}>
        {!evs.length ? <div style={{ color: "var(--text2)", fontSize: 13 }}>Noch keine Termine eingelesen. Prüfe oben „Kalender“ im Status.</div> :
          evs.map(e => (
            <div key={e.uid} style={{ display: "flex", gap: 10, padding: "4px 0", borderTop: "1px solid var(--line)", fontSize: 13 }}>
              <div style={{ width: 120, color: "var(--text2)" }}>{new Date(e.start_time).toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit" })} {hm(e.start_time)}–{hm(e.end_time)}</div>
              <div style={{ flex: 1 }}>{e.title}</div>
              <div style={{ color: "var(--text2)" }}>{e.calendar || ""}</div>
              <div style={{ width: 90, textAlign: "right", color: e.state === "booked" ? "var(--green)" : "var(--text2)" }}>{e.state === "booked" ? "gebucht" : e.state === "dismissed" ? "ignoriert" : Date.parse(e.start_time) > Date.now() ? "geplant" : "offen"}</div>
            </div>))}
      </Box>

      {st?.rules?.length > 0 && (
        <Box title="Gelernte Zuordnungen" sub="Wiederkehrende Termine werden beim nächsten Mal vorbelegt.">
          {st.rules.map(r => (
            <div key={r.pattern} style={{ display: "flex", gap: 8, alignItems: "center", padding: "4px 0", borderTop: "1px solid var(--line)" }}>
              <div style={{ flex: 1 }}>{r.pattern}</div><div style={{ color: "var(--text2)" }}>{r.project_name || "–"} · {r.hits}×</div>
              <button style={{ ...btn(false), padding: "2px 8px" }} onClick={async () => { await call("DELETE", `/meetings/rules/${encodeURIComponent(r.pattern)}`); loadSt(); }}><X size={12} /></button>
            </div>))}
        </Box>)}
    </div>
  );
}
