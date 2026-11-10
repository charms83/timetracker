/* ═══════════════════════════════════════════════════════════════════
   Kalender: Wochenansicht mit Buchungen und Terminen aus dem macOS-Kalender
   + Änderungsprotokoll (Einstellungen)
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback, useMemo } from "react";
import { ChevronLeft, ChevronRight, X, Check, RefreshCw, ShieldCheck, ShieldAlert, CalendarClock, Plus, ListChecks, Play, Flag } from "lucide-react";
import { api, call, Box, lab, btn, hm, localDate, durTxt, projColor, CreateTicketDialog, isServerMode, ImpreciseMark } from "./zp-ui.jsx";

const PX = 52;                                     // Pixel je Stunde
const DAYS = ["Mo", "Di", "Mi", "Do", "Fr", "Sa", "So"];
const monday = off => { const d = new Date(); d.setHours(12, 0, 0, 0); d.setDate(d.getDate() - ((d.getDay() + 6) % 7) + off * 7); return d; };
const minOfDay = iso => { const d = new Date(iso); return d.getHours() * 60 + d.getMinutes(); };
const FIELD = { project_id: "Projekt", task_id: "Aufgabe", start_time: "Beginn", end_time: "Ende", duration_seconds: "Dauer", comment: "Kommentar", billable: "Abrechenbar", date: "Datum", activity: "Tätigkeit", source: "Quelle" };
const ACTION = { create: "angelegt", update: "geändert", delete: "gelöscht", start: "Protokoll gestartet" };

/* Spalten-Layout für überlappende Blöcke */
function lanes(items) {
  const sorted = [...items].sort((a, b) => a.s - b.s || b.e - a.e);
  const out = []; let group = [], groupEnd = -1;
  const flush = () => { const cols = []; for (const it of group) { let c = cols.findIndex(end => end <= it.s); if (c < 0) { c = cols.length; cols.push(0); } cols[c] = it.e; it.col = c; } group.forEach(it => { it.cols = cols.length; out.push(it); }); group = []; };
  for (const it of sorted) { if (it.s >= groupEnd && group.length) flush(); group.push(it); groupEnd = Math.max(groupEnd, it.e); }
  if (group.length) flush();
  return out;
}

const ABS = { urlaub: ["Urlaub", "#0F752F"], krankheit: ["Krank", "#d64545"], feiertag: ["Feiertag", "#8aab00"], fortbildung: ["Fortbildung", "#419C5E"], sonstiges: ["Abwesend", "#9E9E9E"] };
export function CalendarView() {
  const [off, setOff] = useState(0);
  const [entries, setEntries] = useState([]);
  const [events, setEvents] = useState([]);
  const [absences, setAbsences] = useState([]);
  const [todos, setTodos] = useState([]);
  const [showTodo, setShowTodo] = useState(() => { try { return localStorage.getItem("zp-cal-todo") !== "0"; } catch (_) { return true; } });
  useEffect(() => { try { localStorage.setItem("zp-cal-todo", showTodo ? "1" : "0"); } catch (_) {} }, [showTodo]);
  const [timer, setTimer] = useState(null);
  const [projects, setProjects] = useState([]);
  const [sel, setSel] = useState(null);
  const [showCal, setShowCal] = useState(true);
  const [workweek, setWorkweek] = useState(() => { try { return localStorage.getItem("zp-cal-ww") === "1"; } catch (_) { return false; } });
  useEffect(() => { try { localStorage.setItem("zp-cal-ww", workweek ? "1" : "0"); } catch (_) {} }, [workweek]);
  const mon = monday(off);
  const days = [...Array(7)].map((_, i) => { const d = new Date(mon); d.setDate(d.getDate() + i); return localDate(d); });
  const from = days[0], to = days[6];

  const load = useCallback(() => {
    api("GET", `/time-entries?from=${from}&to=${to}`).then(r => setEntries(Array.isArray(r) ? r.filter(e => e.end_time) : []));
    api("GET", `/meetings/events?from=${from}&to=${to}`).then(r => setEvents(Array.isArray(r) ? r : []));
    api("GET", "/timer/status").then(setTimer);
    api("GET", `/absences?from=${from}&to=${to}`).then(r => setAbsences(Array.isArray(r) ? r : []));
    api("GET", `/todo/range?from=${from}&to=${to}`).then(r => setTodos(Array.isArray(r) ? r : []));
  }, [from, to]);
  useEffect(() => { load(); const t = setInterval(load, 60000); window.addEventListener("zp:entry-changed", load); return () => { clearInterval(t); window.removeEventListener("zp:entry-changed", load); }; }, [load]);
  useEffect(() => { api("GET", "/projects").then(p => setProjects(Array.isArray(p) ? p.filter(x => x.status !== "archived") : [])); }, []);

  const items = useMemo(() => {
    const list = entries.map(e => ({ kind: "entry", id: "e" + e.id, date: e.date, s: minOfDay(e.start_time), e: Math.max(minOfDay(e.start_time) + 10, minOfDay(e.end_time) || 24 * 60), x: e }));
    if (timer?.active && days.includes(localDate(new Date(timer.start_time))))
      list.push({ kind: "run", id: "run", date: localDate(new Date(timer.start_time)), s: minOfDay(timer.start_time), e: Math.max(minOfDay(timer.start_time) + 10, minOfDay(new Date().toISOString())), x: timer });
    return list;
  }, [entries, timer, from]);
  // Termine, die schon als Zeit gebucht sind (oder von einer Buchung überdeckt werden), nicht doppelt zeigen – die Buchung steht für sich
  const evItems = useMemo(() => {
    if (!showCal) return [];
    const list = events.map(e => ({ kind: "cal", id: "c" + e.uid, date: e.date, s: minOfDay(e.start_time), e: Math.max(minOfDay(e.start_time) + 15, localDate(new Date(e.end_time)) === e.date ? minOfDay(e.end_time) : 24 * 60), x: e }));
    return list.filter(ev => {
      if (ev.x.state === "booked") return false;
      return !items.some(b => b.date === ev.date && Math.max(0, Math.min(b.e, ev.e) - Math.max(b.s, ev.s)) >= 0.5 * (ev.e - ev.s));
    });
  }, [events, showCal, items]);

  // To-dos mit Uhrzeit = Zeitblock im Raster; ohne Uhrzeit = Zeile „ganzt.“ oben
  const tdItems = useMemo(() => !showTodo ? [] : todos.filter(t => t.scheduled_date && t.block_start && t.status !== "done").map(t => {
    const [h, m] = t.block_start.split(":").map(Number), st = h * 60 + m; return { kind: "todo", id: "t" + t.id, date: t.scheduled_date, s: st, e: st + Math.max(15, t.estimated_minutes || 30), x: t }; }), [todos, showTodo]);
  const dayTodos = d => !showTodo ? [] : todos.filter(t => (t.scheduled_date === d && !t.block_start) || (t.due_date === d && t.scheduled_date !== d && t.status !== "done"));
  const dropTodo = async (e, d, m) => {
    const id = e.dataTransfer.getData("text/caltodo"); if (!id) return false;
    e.preventDefault(); const r = await call("PUT", `/todo/${id}`, { when: d, block_start: m == null ? "" : `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}` });
    if (r?.error) alert(r.error); load(); return true;
  };
  const all = [...items, ...evItems, ...tdItems];
  const startH = Math.min(7, ...all.map(i => Math.floor(i.s / 60)));
  const endH = Math.max(19, ...all.map(i => Math.ceil(i.e / 60)));
  const hours = [...Array(endH - startH)].map((_, i) => startH + i);
  const today = localDate(new Date());
  const nowMin = minOfDay(new Date().toISOString());
  const sumDay = d => entries.filter(e => e.date === d).reduce((s, e) => s + (e.duration_seconds || 0), 0);

  const weekSum = entries.reduce((a, e) => a + (e.duration_seconds || 0), 0);
  const openEv = events.filter(e => !e.state && Date.parse(e.start_time) < Date.now()).length;
  const visDays = workweek ? days.slice(0, 5) : days;
  const cols = workweek ? "repeat(5, 1fr)" : "repeat(5, 1fr) repeat(2, .62fr)";

  const block = it => {
    const top = (it.s - startH * 60) / 60 * PX + 1, h = Math.max(20, (it.e - it.s) / 60 * PX - 3);
    const w = 100 / it.cols, left = it.col * w;
    const active = sel?.id === it.id;
    const base = { position: "absolute", top, height: h, left: `calc(${left}% + 3px)`, width: `calc(${w}% - 6px)`, borderRadius: 7, padding: h > 26 ? "4px 7px" : "1px 7px",
      fontSize: 11.5, overflow: "hidden", cursor: "pointer", lineHeight: 1.3, pointerEvents: "auto", zIndex: active ? 3 : 1, transition: "box-shadow .12s, transform .12s",
      boxShadow: active ? "0 0 0 2px var(--accent), 0 6px 16px rgba(0,0,0,.12)" : "0 1px 2px rgba(0,0,0,.06)" };
    const hover = { onMouseEnter: e => { if (!active) e.currentTarget.style.boxShadow = "0 4px 12px rgba(0,0,0,.12)"; }, onMouseLeave: e => { if (!active) e.currentTarget.style.boxShadow = base.boxShadow; } };
    if (it.kind === "todo") {
      const t = it.x, c = t.system === "plane" ? "#7c5cd6" : t.system === "jira" ? "#2f6fd6" : "var(--text2)";
      return <div key={it.id} draggable onDragStart={e => { e.stopPropagation(); e.dataTransfer.setData("text/caltodo", String(t.id)); }} onClick={() => setSel(it)} title={`To-do: ${t.ticket ? t.ticket + " " : ""}${t.title} – ziehen zum Verschieben`} {...hover}
        style={{ ...base, background: "var(--card)", border: `1.5px dashed ${c}`, color: "var(--text)", cursor: "grab" }}>
        <div style={{ fontWeight: 600, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", display: "flex", gap: 4, alignItems: "center" }}>
          <ListChecks size={11} style={{ color: c, flex: "0 0 auto" }} />{t.ticket && <span style={{ color: c, fontWeight: 700 }}>{t.ticket}</span>}{t.ticket_title && t.ticket ? (t.title === t.ticket_title ? "" : t.title) : t.title}</div>
        {h > 34 && <div style={{ color: "var(--text2)", fontSize: 10.5 }}>geplant {t.block_start} · {durTxt((t.estimated_minutes || 0) * 60)}</div>}
      </div>;
    }
    if (it.kind === "cal") {
      const booked = it.x.state === "booked", dis = it.x.state === "dismissed", fut = Date.parse(it.x.start_time) > Date.now();
      const c = booked ? "var(--accent)" : "#7c5cd6";
      return <div key={it.id} onClick={() => setSel(it)} title={it.x.title} {...hover}
        style={{ ...base, background: `repeating-linear-gradient(135deg, color-mix(in srgb, ${c} 9%, var(--card)) 0 7px, color-mix(in srgb, ${c} 4%, var(--card)) 7px 14px)`,
          border: `1px solid color-mix(in srgb, ${c} 35%, transparent)`, borderLeft: `3px solid ${c}`, color: "var(--text)", opacity: dis ? .4 : fut ? .85 : 1 }}>
        <div style={{ fontWeight: 600, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", display: "flex", gap: 4, alignItems: "center" }}>
          <CalendarClock size={11} style={{ color: c, flex: "0 0 auto" }} />{booked ? "✓ " : ""}{it.x.title}</div>
        {h > 34 && <div style={{ color: "var(--text2)", fontSize: 10.5 }}>{hm(it.x.start_time)} – {hm(it.x.end_time)}{it.x.location ? ` · ${it.x.location}` : ""}</div>}
      </div>;
    }
    const e = it.x, c = projColor(it.kind === "run" ? e.project?.id : e.project_id), run = it.kind === "run";
    const key = run ? e.task?.jira_ticket : e.jira_ticket;
    const title = run ? (e.task?.title || e.project?.name || "läuft") : (e.task_title || e.comment || e.project_name || "Buchung");
    return <div key={it.id} onClick={() => setSel(it)} title={`${key ? key + " " : ""}${title}`} {...hover}
      style={{ ...base, background: `color-mix(in srgb, ${c} 15%, var(--card))`, borderLeft: `3px solid ${c}`, color: "var(--text)",
        backgroundImage: run ? `repeating-linear-gradient(45deg, color-mix(in srgb, ${c} 12%, transparent) 0 6px, transparent 6px 12px)` : "none" }}>
      <div style={{ fontWeight: 600, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
        {key && <span style={{ color: c, fontWeight: 700, marginRight: 4, filter: "brightness(.8)" }}>{key}</span>}{title}</div>
      {h > 34 && <div style={{ color: "var(--text2)", fontSize: 10.5 }}>{run ? <><span style={{ display: "inline-block", width: 6, height: 6, borderRadius: 3, background: "var(--stop)", marginRight: 4, verticalAlign: 1 }} />{hm(e.start_time)} – läuft</> : `${hm(e.start_time)} – ${hm(e.end_time)} · ${durTxt(e.duration_seconds)}`}</div>}
      {h > 52 && !run && e.comment && e.comment !== title && <div style={{ color: "var(--text2)", fontSize: 10.5, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{e.comment}</div>}
      {h > 52 && !run && e.project_name && <div style={{ fontSize: 10, color: c, filter: "brightness(.8)", fontWeight: 600, marginTop: 1 }}>{e.project_name}</div>}
    </div>;
  };
  const navBtn = { width: 30, height: 30, borderRadius: 8, border: "1px solid var(--line)", background: "var(--card)", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer" };

  return (
    <div style={{ display: "flex", gap: 14, alignItems: "flex-start" }}>
      <div className="zp-card" style={{ flex: 1, minWidth: 0, padding: 0, overflow: "hidden" }}>
        {/* Kopf */}
        <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "14px 18px", borderBottom: "1px solid var(--line)", flexWrap: "wrap" }}>
          <div>
            <div className="zp-display" style={{ fontSize: 24, lineHeight: 1.1 }}>{mon.toLocaleDateString("de-DE", { month: "long", year: "numeric" })}</div>
            <div style={{ fontSize: 12, color: "var(--text2)" }}>KW {kw(mon)} · {new Date(from + "T12:00").toLocaleDateString("de-DE", { day: "2-digit", month: "2-digit" })} – {new Date(to + "T12:00").toLocaleDateString("de-DE", { day: "2-digit", month: "2-digit" })}</div>
          </div>
          <div style={{ display: "flex", gap: 6, marginLeft: 12 }}>
            <button style={navBtn} onClick={() => setOff(off - 1)} title="Vorige Woche"><ChevronLeft size={16} /></button>
            <button style={{ ...navBtn, width: "auto", padding: "0 12px", fontSize: 12, fontWeight: 600, color: off === 0 ? "var(--text2)" : "var(--accent)" }} onClick={() => setOff(0)}>Heute</button>
            <button style={navBtn} onClick={() => setOff(off + 1)} title="Nächste Woche"><ChevronRight size={16} /></button>
          </div>
          <div style={{ display: "flex", gap: 14, marginLeft: 16 }}>
            <div><div style={{ fontSize: 10, color: "var(--text2)", textTransform: "uppercase", letterSpacing: .4 }}>Gebucht</div><div style={{ fontWeight: 700 }}>{durTxt(weekSum)}</div></div>
            {showCal && <div><div style={{ fontSize: 10, color: "var(--text2)", textTransform: "uppercase", letterSpacing: .4 }}>Termine offen</div><div style={{ fontWeight: 700, color: openEv ? "#7c5cd6" : "var(--text)" }}>{openEv} von {events.length}</div></div>}
          </div>
          <div style={{ flex: 1 }} />
          <div style={{ display: "flex", background: "var(--bg)", borderRadius: 8, padding: 2 }}>
            {[[false, "7 Tage"], [true, "Mo–Fr"]].map(([v, l]) => <button key={l} onClick={() => setWorkweek(v)} style={{ border: "none", borderRadius: 6, padding: "4px 10px", fontSize: 12, fontWeight: 600, background: workweek === v ? "var(--card)" : "transparent", color: workweek === v ? "var(--text)" : "var(--text2)", boxShadow: workweek === v ? "0 1px 2px rgba(0,0,0,.08)" : "none" }}>{l}</button>)}
          </div>
          <label style={{ fontSize: 12, color: "var(--text2)", display: "flex", gap: 5, alignItems: "center" }}><input type="checkbox" checked={showCal} onChange={e => setShowCal(e.target.checked)} /> Termine</label>
          <label style={{ fontSize: 12, color: "var(--text2)", display: "flex", gap: 5, alignItems: "center" }}><input type="checkbox" checked={showTodo} onChange={e => setShowTodo(e.target.checked)} /> To-dos</label>
          <button style={navBtn} onClick={load} title="Neu laden"><RefreshCw size={14} /></button>
        </div>
        {/* Tageköpfe */}
        <div style={{ display: "grid", gridTemplateColumns: `52px ${cols}`, borderBottom: "1px solid var(--line)" }}>
          <div />
          {visDays.map((d, i) => { const isT = d === today, sum = sumDay(d); return (
            <div key={d} style={{ padding: "8px 0 8px", textAlign: "center", borderLeft: "1px solid var(--line)" }}>
              <div style={{ fontSize: 11, fontWeight: 600, color: isT ? "var(--accent)" : "var(--text2)", textTransform: "uppercase", letterSpacing: .5 }}>{DAYS[i]}</div>
              <div style={{ display: "inline-flex", width: 30, height: 30, borderRadius: 15, alignItems: "center", justifyContent: "center", fontSize: 16, fontWeight: 700, marginTop: 2,
                background: isT ? "var(--accent-fill)" : "transparent", color: isT ? "#fff" : "var(--text)" }}>{+d.slice(8)}</div>
              <div style={{ fontSize: 10.5, color: sum ? "var(--text)" : "var(--text3, var(--text2))", marginTop: 2, fontWeight: sum ? 600 : 400 }}>{sum ? durTxt(sum) : "–"}</div>
            </div>); })}
        </div>
        {/* Ganztägig: Abwesenheiten (Urlaub, Krank, Feiertag …) */}
        {visDays.some(d => absences.some(a => a.date_from <= d && a.date_to >= d) || dayTodos(d).length) && (
          <div style={{ display: "grid", gridTemplateColumns: `52px ${cols}`, borderBottom: "1px solid var(--line)" }}>
            <div style={{ fontSize: 10, color: "var(--text2)", padding: "6px 6px 0 0", textAlign: "right" }}>ganzt.</div>
            {visDays.map(d => (<div key={d} style={{ borderLeft: "1px solid var(--line)", padding: 3, minHeight: 26, display: "flex", flexDirection: "column", gap: 2 }}
              onDragOver={e => { if (e.dataTransfer.types.includes("text/caltodo")) e.preventDefault(); }} onDrop={e => dropTodo(e, d, null)}>
              {dayTodos(d).map(t => { const due = t.due_date === d && t.scheduled_date !== d, c = t.system === "plane" ? "#7c5cd6" : t.system === "jira" ? "#2f6fd6" : "var(--text2)"; return (
                <div key={"t" + t.id} draggable={!due} onDragStart={e => e.dataTransfer.setData("text/caltodo", String(t.id))} onClick={() => setSel({ kind: "todo", id: "t" + t.id, x: t })}
                  title={due ? `fällig: ${t.title}` : `To-do: ${t.title} – auf eine Uhrzeit ziehen, um es fest einzuplanen`}
                  style={{ fontSize: 11, fontWeight: 600, padding: "2px 6px", borderRadius: 6, cursor: due ? "pointer" : "grab", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis",
                    border: due ? "1px solid #C4271B66" : `1.5px dashed ${c}`, background: due ? "#C4271B12" : "var(--card)", color: t.status === "done" ? "var(--text2)" : "var(--text)", textDecoration: t.status === "done" ? "line-through" : "none" }}>
                  {due ? <Flag size={10} style={{ color: "#C4271B", verticalAlign: -1 }} /> : <ListChecks size={10} style={{ color: c, verticalAlign: -1 }} />} {t.ticket ? t.ticket + " " : ""}{t.title === t.ticket_title && t.ticket ? "" : t.title}</div>); })}
              {absences.filter(a => a.date_from <= d && a.date_to >= d).map(a => { const t = ABS[a.type] || ABS.sonstiges; return (
                <div key={a.id} title={`${t[0]}${a.note ? " – " + a.note : ""}${a.hours ? ` (${a.hours} h)` : ""}`} onClick={() => { location.hash = "abwesenheiten"; }}
                  style={{ fontSize: 11, fontWeight: 600, padding: "3px 7px", borderRadius: 6, cursor: "pointer", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis",
                    background: `color-mix(in srgb, ${t[1]} 18%, var(--card))`, borderLeft: `3px solid ${t[1]}`, color: "var(--text)" }}>
                  {t[0]}{a.note ? ` · ${a.note}` : ""}{a.hours ? ` · ${a.hours} h` : ""}</div>); })}
            </div>))}
          </div>)}
        {/* Raster */}
        <div style={{ display: "grid", gridTemplateColumns: `52px ${cols}`, maxHeight: "calc(100vh - 230px)", overflowY: "auto" }}>
          <div style={{ position: "relative", height: hours.length * PX }}>
            {hours.map(h => <div key={h} style={{ position: "absolute", top: (h - startH) * PX - 7, right: 8, fontSize: 10.5, color: "var(--text2)", fontVariantNumeric: "tabular-nums" }}>{h > startH ? `${String(h).padStart(2, "0")}:00` : ""}</div>)}
          </div>
          {visDays.map((d, idx) => {
            const both = lanes([...items.filter(i => i.date === d), ...evItems.filter(i => i.date === d), ...tdItems.filter(i => i.date === d)]);
            const weekend = idx >= 5, isT = d === today;
            return (
              <div key={d} style={{ position: "relative", height: hours.length * PX, borderLeft: "1px solid var(--line)",
                background: isT ? "color-mix(in srgb, var(--accent) 4%, transparent)" : weekend ? "color-mix(in srgb, var(--text2) 5%, transparent)" : "transparent" }}>
                {hours.map(h => <div key={h} style={{ position: "absolute", top: (h - startH) * PX, left: 0, right: 0, borderTop: "1px solid var(--line)", opacity: h === startH ? 0 : .8 }} />)}
                {hours.map(h => <div key={"h" + h} style={{ position: "absolute", top: (h - startH) * PX + PX / 2, left: 0, right: 0, borderTop: "1px dotted var(--line)", opacity: .5 }} />)}
                {!weekend && <div style={{ position: "absolute", left: 0, right: 0, top: Math.max(0, (8 - startH) * PX), height: (17 - 8) * PX, background: "color-mix(in srgb, var(--card) 60%, transparent)", pointerEvents: "none" }} />}
                <div title="Klicken: hier Zeit nachtragen · To-do hierher ziehen: fest einplanen" style={{ position: "absolute", inset: 0, cursor: "cell" }}
                  onDragOver={e => { if (e.dataTransfer.types.includes("text/caltodo")) e.preventDefault(); }}
                  onDrop={e => { const r = e.currentTarget.getBoundingClientRect(); const m = Math.floor(((e.clientY - r.top) / PX * 60 + startH * 60) / 15) * 15; dropTodo(e, d, m); }}
                  onClick={ev => { const r = ev.currentTarget.getBoundingClientRect(); const m = Math.floor(((ev.clientY - r.top) / PX * 60 + startH * 60) / 15) * 15; setSel({ kind: "new", id: "new", date: d, s: m }); }} />
                {sel?.kind === "new" && sel.date === d && <div style={{ position: "absolute", left: 3, right: 3, top: (sel.s - startH * 60) / 60 * PX + 1, height: PX - 3, border: "2px dashed var(--accent)", borderRadius: 7, background: "var(--accent-soft)", pointerEvents: "none", zIndex: 2, fontSize: 11, padding: "3px 7px", color: "var(--accent)", fontWeight: 600 }}>+ Neue Buchung</div>}
                <div style={{ position: "absolute", inset: 0, pointerEvents: "none" }}>{both.map(it => block(it))}</div>
                {isT && nowMin >= startH * 60 && nowMin <= endH * 60 && <div style={{ position: "absolute", left: 0, right: 0, top: (nowMin - startH * 60) / 60 * PX, borderTop: "2px solid var(--stop)", zIndex: 4, pointerEvents: "none" }}>
                  <div style={{ position: "absolute", left: -5, top: -6, width: 10, height: 10, borderRadius: 5, background: "var(--stop)" }} /></div>}
              </div>);
          })}
        </div>
        <div style={{ display: "flex", gap: 16, padding: "8px 18px", borderTop: "1px solid var(--line)", fontSize: 11, color: "var(--text2)", alignItems: "center" }}>
          <span style={{ display: "flex", gap: 5, alignItems: "center" }}><span style={{ width: 12, height: 12, borderRadius: 3, background: "color-mix(in srgb, #3651c9 18%, var(--card))", borderLeft: "3px solid #3651c9" }} /> Buchung (Farbe = Projekt)</span>
          <span style={{ display: "flex", gap: 5, alignItems: "center" }}><span style={{ width: 12, height: 12, borderRadius: 3, background: "color-mix(in srgb, #7c5cd6 10%, var(--card))", borderLeft: "3px solid #7c5cd6" }} /> Kalendertermin</span>
          <span style={{ display: "flex", gap: 5, alignItems: "center" }}><span style={{ width: 12, height: 12, borderRadius: 3, background: "var(--accent-soft)", borderLeft: "3px solid var(--accent)" }} /> Termin gebucht</span>
          <span style={{ display: "flex", gap: 5, alignItems: "center" }}><span style={{ width: 12, height: 12, borderRadius: 3, border: "1.5px dashed var(--text2)" }} /> To-do (ziehen = einplanen)</span>
          <span style={{ display: "flex", gap: 5, alignItems: "center" }}><Flag size={11} style={{ color: "#C4271B" }} /> fällig</span>
          <span style={{ marginLeft: "auto" }}>Freie Stelle anklicken = Zeit nachtragen · Termin anklicken = buchen</span>
        </div>
      </div>
      {sel && <SidePanel sel={sel} projects={projects} onClose={() => setSel(null)} onChanged={() => { load(); }} />}
    </div>
  );
}
function kw(d) { const t = new Date(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate())); const n = t.getUTCDay() || 7; t.setUTCDate(t.getUTCDate() + 4 - n); const y = new Date(Date.UTC(t.getUTCFullYear(), 0, 1)); return Math.ceil(((t - y) / 864e5 + 1) / 7); }

function SidePanel({ sel, projects, onClose, onChanged }) {
  const x = sel.x;
  return (
    <div className="zp-card" style={{ width: 320, flex: "0 0 320px", padding: "12px 14px", position: "sticky", top: 70 }}>
      <div style={{ display: "flex", alignItems: "center", marginBottom: 8 }}>
        <div style={{ fontWeight: 600, flex: 1 }}>{sel.kind === "new" ? "Zeit nachtragen" : sel.kind === "cal" ? "Kalendertermin" : sel.kind === "run" ? "Laufender Timer" : sel.kind === "todo" ? "To-do" : "Buchung"}</div>
        <button className="zp-nav" onClick={onClose}><X size={15} /></button>
      </div>
      {sel.kind === "todo" ? <TodoInfo key={x.id} t={x} onDone={() => { onChanged(); onClose(); }} /> : sel.kind === "new" ? <NewEntry key={sel.date + sel.s} date={sel.date} startMin={sel.s} projects={projects} onDone={() => { onChanged(); onClose(); }} />
       : sel.kind === "cal" ? <CalBook key={x.uid} ev={x} projects={projects} onDone={onChanged} /> : sel.kind === "run" ? (
        <div style={{ fontSize: 13 }}>{x.project?.name}{x.task ? ` · ${x.task.title}` : ""}<div style={{ color: "var(--text2)" }}>seit {hm(x.start_time)}</div></div>
      ) : <EntryInfo key={x.id} e={x} />}
    </div>);
}

const t2 = m => `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;
export function NewEntry({ date, startMin, endMin, projects, onDone }) {
  const [pid, setPid] = useState(""); const [tasks, setTasks] = useState([]); const [tid, setTid] = useState("");
  const [from, setFrom] = useState(t2(startMin)); const [to, setTo] = useState(t2(Math.min(24 * 60 - 1, endMin || startMin + 60)));
  const [comment, setComment] = useState(""); const [msg, setMsg] = useState(null);
  useEffect(() => { if (!pid) { setTasks([]); return; } api("GET", `/tasks?projectId=${pid}`).then(t => setTasks(Array.isArray(t) ? t.filter(x => x.status !== "done") : [])); }, [pid]);
  const save = async () => {
    const s = new Date(`${date}T${from}:00`), e = new Date(`${date}T${to}:00`);
    if (!(e > s)) return setMsg({ err: 1, text: "Ende muss nach dem Beginn liegen" });
    const r = await call("POST", "/time-entries", { project_id: Number(pid), task_id: tid ? Number(tid) : null, start_time: s.toISOString(), end_time: e.toISOString(),
      duration_seconds: Math.round((e - s) / 1000), comment: comment || null, date });
    if (r.error) setMsg({ err: 1, text: r.error }); else onDone();
  };
  return (
    <div style={{ fontSize: 13 }}>
      <div style={{ color: "var(--text2)" }}>{new Date(date + "T12:00").toLocaleDateString("de-DE", { weekday: "long", day: "2-digit", month: "2-digit" })}</div>
      <div style={{ display: "flex", gap: 6, alignItems: "center", marginTop: 6 }}>
        <input type="time" value={from} onChange={e => setFrom(e.target.value)} style={{ padding: "5px 7px" }} /> – <input type="time" value={to} onChange={e => setTo(e.target.value)} style={{ padding: "5px 7px" }} />
      </div>
      <label style={lab}>Projekt</label>
      <select value={pid} onChange={e => { setPid(e.target.value); setTid(""); }} style={{ width: "100%", padding: "6px 8px" }}>
        <option value="">Projekt wählen …</option>{projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
      </select>
      {tasks.length > 0 && <><label style={lab}>Aufgabe / Ticket</label>
        <select value={tid} onChange={e => setTid(e.target.value)} style={{ width: "100%", padding: "6px 8px" }}>
          <option value="">(keine)</option>{tasks.map(t => <option key={t.id} value={t.id}>{t.jira_ticket ? t.jira_ticket + " " : ""}{t.title}</option>)}
        </select></>}
      <label style={lab}>Was hast du gemacht?</label>
      <input value={comment} onChange={e => setComment(e.target.value)} style={{ width: "100%", padding: "6px 8px" }} onKeyDown={e => e.key === "Enter" && pid && save()} />
      <button style={{ ...btn(true), marginTop: 10 }} disabled={!pid} onClick={save}><Check size={14} /> Speichern</button>
      {msg && <div style={{ marginTop: 6, fontSize: 12, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.text}</div>}
      <div style={{ marginTop: 8, fontSize: 11, color: "var(--text2)" }}>Tipp: in eine freie Stelle im Kalender klicken – der Beginn wird auf 15 Minuten gerundet.</div>
    </div>);
}

function CalBook({ ev, projects, onDone }) {
  const [pid, setPid] = useState(ev.rule?.project_id || "");
  const [tasks, setTasks] = useState([]); const [tid, setTid] = useState(ev.rule?.task_id || "");
  const [msg, setMsg] = useState(null);
  const [sugg, setSugg] = useState(null); const [q, setQ] = useState(""); const [auto, setAuto] = useState(false); const [newT, setNewT] = useState(false);
  const pick = s => { setPid(String(s.project_id || "")); setTid(s.task_id ? String(s.task_id) : ""); };
  useEffect(() => { api("GET", `/meetings/suggest-task?text=${encodeURIComponent(ev.title)}`).then(r => {
    const l = Array.isArray(r) ? r : []; setSugg(l);
    if (!ev.rule && l[0] && l[0].score >= 80) { pick(l[0]); setAuto(true); } }); }, [ev.uid]);   // eslint-disable-line
  useEffect(() => { if (!q.trim()) return; const t = setTimeout(() => api("GET", `/meetings/suggest-task?text=${encodeURIComponent(q)}`).then(r => setSugg(Array.isArray(r) ? r : [])), 250); return () => clearTimeout(t); }, [q]);
  const future = Date.parse(ev.start_time) > Date.now();
  useEffect(() => { if (!pid) { setTasks([]); return; } api("GET", `/tasks?projectId=${pid}`).then(t => setTasks(Array.isArray(t) ? t.filter(x => x.status !== "done") : [])); }, [pid]);
  const book = async () => {
    const r = await call("POST", "/meetings/book", { key: ev.key, title: ev.title, project_id: pid, task_id: tid || null, start_time: ev.start_time, end_time: ev.end_time, activity: ev.rule?.activity || null });
    if (r.error) setMsg({ err: 1, text: r.error }); else { setMsg({ text: "Gebucht" }); onDone(); }
  };
  return (
    <div style={{ fontSize: 13 }}>
      <div style={{ fontWeight: 600 }}>{ev.title}</div>
      <div style={{ color: "var(--text2)" }}>{new Date(ev.start_time).toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit" })} · {hm(ev.start_time)}–{hm(ev.end_time)} · {durTxt((Date.parse(ev.end_time) - Date.parse(ev.start_time)) / 1000)}</div>
      {ev.calendar && <div style={{ color: "var(--text2)", wordBreak: "break-all" }}>Kalender: {ev.calendar}{ev.location ? ` · ${ev.location}` : ""}</div>}
      {ev.state === "booked" ? <div style={{ color: "var(--accent)", marginTop: 10 }}>✓ Bereits als Zeit gebucht</div>
        : future ? <div style={{ color: "var(--text2)", marginTop: 10 }}>Termin liegt in der Zukunft – buchen geht, sobald er begonnen hat.</div>
        : (<>
          <label style={lab}>Passendes Ticket {auto && <span style={{ color: "var(--accent)" }}>· automatisch zugeordnet</span>}</label>
          <input value={q} onChange={e => setQ(e.target.value)} placeholder="Ticket suchen (Titel, Key, Stichwort) …" style={{ width: "100%", padding: "6px 8px" }} />
          <div style={{ display: "flex", flexDirection: "column", gap: 4, marginTop: 6 }}>
            {sugg === null ? <span style={{ fontSize: 12, color: "var(--text2)" }}>Suche passende Tickets …</span>
              : !sugg.length ? <span style={{ fontSize: 12, color: "var(--text2)" }}>Kein passendes Ticket gefunden – unten von Hand wählen.</span>
              : sugg.map((s, i) => { const on = String(s.task_id || "") === String(tid) && String(s.project_id) === String(pid); return (
                <button key={i} onClick={() => { pick(s); setAuto(false); }} title={s.reasons.join(" · ")}
                  style={{ textAlign: "left", padding: "5px 8px", borderRadius: 7, border: `1px solid ${on ? "var(--accent)" : "var(--line)"}`, background: on ? "var(--accent-soft)" : "var(--card)", fontSize: 12, display: "flex", gap: 6, alignItems: "center" }}>
                  <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{s.key && <b style={{ color: "#3f76ff" }}>{s.key} </b>}{s.title || s.project_name}<span style={{ color: "var(--text2)" }}> · {s.project_name}</span></span>
                  <span style={{ fontSize: 10, fontWeight: 700, color: s.score >= 80 ? "var(--accent)" : "var(--text2)" }}>{s.score} %</span>
                </button>); })}
          </div>
          <label style={lab}>Projekt</label>
          <select value={pid} onChange={e => { setPid(e.target.value); setTid(""); setAuto(false); }} style={{ width: "100%", padding: "6px 8px" }}>
            <option value="">Projekt wählen …</option>{projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
          {tasks.length > 0 && <><label style={lab}>Aufgabe / Ticket</label>
            <select value={tid} onChange={e => setTid(e.target.value)} style={{ width: "100%", padding: "6px 8px" }}>
              <option value="">(keine)</option>{tasks.map(t => <option key={t.id} value={t.id}>{t.jira_ticket ? t.jira_ticket + " " : ""}{t.title}</option>)}
            </select></>}
          {ev.rule && <div style={{ fontSize: 11, color: "var(--text2)", marginTop: 4 }}>Vorbelegt wie beim letzten Mal</div>}
          <button style={{ ...btn(false), marginTop: 8, fontSize: 12 }} onClick={() => setNewT(true)}><Plus size={13} /> Kein passendes Ticket – neues anlegen</button>
          {newT && <CreateTicketDialog title={ev.title} projectId={pid} onClose={() => setNewT(false)}
            onDone={r => { setNewT(false); const np = String(r.local_project_id || ""); api("GET", `/tasks?projectId=${np}`).then(t => { setTasks(Array.isArray(t) ? t.filter(x => x.status !== "done") : []); setPid(np); setTid(r.task_id ? String(r.task_id) : ""); }); setAuto(false); setMsg({ text: `${r.key} angelegt` }); }} />}
          <button style={{ ...btn(true), marginTop: 10 }} disabled={!pid} onClick={book}><Check size={14} /> Termin als Zeit buchen</button>
        </>)}
      {msg && <div style={{ marginTop: 6, fontSize: 12, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.text}</div>}
    </div>);
}

function EntryInfo({ e }) {
  const [hist, setHist] = useState(null);
  useEffect(() => { api("GET", `/audit?entry=${e.id}&limit=50`).then(r => setHist(isServerMode() && !(r && Array.isArray(r.rows)) ? { rows: [], projects: [], tasks: [], unavailable: true } : r)); }, [e.id]);
  return (
    <div style={{ fontSize: 13 }}>
      <div style={{ display: "flex", gap: 6, alignItems: "center" }}><span style={{ width: 10, height: 10, borderRadius: 3, background: projColor(e.project_id) }} /><b>{e.project_name || "–"}</b></div>
      {(e.jira_ticket || e.task_title) && <div>{e.jira_ticket ? e.jira_ticket + " " : ""}{e.task_title}</div>}
      <div style={{ color: "var(--text2)" }}>{hm(e.start_time)}–{hm(e.end_time)}<ImpreciseMark e={e} /> · {durTxt(e.duration_seconds)}{e.activity ? ` · ${e.activity}` : ""}</div>
      {e.comment && <div style={{ marginTop: 4 }}>{e.comment}</div>}
      {!["tempo", "tempo_csv"].includes(e.source) && <button style={{ marginTop: 8, padding: "4px 12px", borderRadius: 7, border: "1px solid var(--line)", background: "var(--card)", color: "var(--text)", cursor: "pointer", fontWeight: 600 }}
        onClick={() => window.dispatchEvent(new CustomEvent("zp:edit-entry", { detail: { id: e.id, date: e.date } }))}>✎ Bearbeiten</button>}
      <div style={{ marginTop: 6, fontSize: 11, color: "var(--text2)" }}>Quelle: {e.source || "manuell"}{e.tempo_push_id ? " · in Tempo" : ""}{e.plane_worklog_id || e.plane_sum_key ? " · in Plane" : ""}</div>
      <div style={{ marginTop: 12, fontWeight: 600, fontSize: 12 }}>Änderungsverlauf</div>
      {!hist ? "…" : hist.unavailable ? <div style={{ fontSize: 12, color: "var(--text2)" }}>Der Änderungsverlauf kommt in Kürze auf dem Server.</div> : !hist.rows.length ? <div style={{ fontSize: 12, color: "var(--text2)" }}>Keine Änderungen protokolliert (vor Einführung des Protokolls angelegt oder aus Tempo übernommen).</div>
        : hist.rows.map(r => <AuditLine key={r.id} r={r} projects={hist.projects} tasks={hist.tasks} compact />)}
    </div>);
}

const fmtVal = (k, v, projects, tasks) => v == null || v === "" ? "–" : k === "project_id" ? (projects[v] || v) : k === "task_id" ? (tasks[v] || v)
  : /_time$/.test(k) ? new Date(v).toLocaleString("de-DE", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" }) : k === "duration_seconds" ? durTxt(v) : String(v);
function AuditLine({ r, projects, tasks, compact }) {
  const when = new Date(r.ts).toLocaleString("de-DE", { day: "2-digit", month: "2-digit", year: compact ? undefined : "numeric", hour: "2-digit", minute: "2-digit", second: compact ? undefined : "2-digit" });
  const snap = r.after || r.before;
  return (
    <div style={{ padding: "6px 0", borderTop: "1px solid var(--line)", fontSize: 12 }}>
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
        <span style={{ color: "var(--text2)", fontVariantNumeric: "tabular-nums" }}>{when}</span>
        <b style={{ color: r.action === "delete" ? "var(--stop)" : "var(--text)" }}>{ACTION[r.action] || r.action}</b>
        {!compact && r.row_id && <span style={{ color: "var(--text2)" }}>Buchung #{r.row_id}</span>}
        <span style={{ color: "var(--text2)" }}>{r.actor || ""}{r.origin && r.origin !== "system" ? ` · ${r.origin}` : r.origin === "system" ? " · automatisch" : ""}</span>
      </div>
      {r.changes ? Object.entries(r.changes).map(([k, [a, b]]) => (
        <div key={k} style={{ paddingLeft: 10 }}>{FIELD[k] || k}: <span style={{ textDecoration: "line-through", color: "var(--text2)" }}>{fmtVal(k, a, projects, tasks)}</span> → {fmtVal(k, b, projects, tasks)}</div>))
        : snap && r.tbl === "time_entries" ? <div style={{ paddingLeft: 10, color: "var(--text2)" }}>{projects[snap.project_id] || "–"} · {fmtVal("start_time", snap.start_time)}–{snap.end_time ? hm(snap.end_time) : "…"}{snap.comment ? ` · ${snap.comment}` : ""}</div>
        : r.after && r.tbl === "system" ? <div style={{ paddingLeft: 10, color: "var(--text2)" }}>{r.after.entries} Buchungen als Ausgangsstand</div> : null}
    </div>);
}

/* ═══ Einstellungen → Änderungsprotokoll ═══ */
export function AuditTab() {
  const [data, setData] = useState(null); const [ver, setVer] = useState(null); const [action, setAction] = useState("");
  const load = useCallback(() => { api("GET", `/audit?limit=200${action ? "&action=" + action : ""}`).then(setData); api("GET", "/audit/verify").then(setVer); }, [action]);
  useEffect(() => { load(); }, [load]);
  return (
    <div>
      <Box title="Änderungsprotokoll" sub="Jede Neuanlage, Änderung und Löschung einer Buchung wird mit Zeitpunkt, Benutzer und Vorher/Nachher festgehalten – wie bei Tempo darf geändert werden, aber nichts verschwindet spurlos. Das Protokoll kann nicht bearbeitet oder gelöscht werden und liegt zusätzlich als Datei neben der Datenbank."
        right={<button style={btn(false)} onClick={load}><RefreshCw size={13} /> Prüfen</button>}>
        {ver && (ver.ok
          ? <div style={{ display: "flex", gap: 8, alignItems: "center", color: "var(--accent)", fontSize: 13 }}><ShieldCheck size={16} /> Protokoll unverändert ({ver.rows} Einträge, Prüfsummen-Kette intakt{ver.file_ok === false ? " – aber Datei weicht ab!" : ""})</div>
          : <div style={{ display: "flex", gap: 8, alignItems: "center", color: "var(--stop)", fontSize: 13 }}><ShieldAlert size={16} /> Achtung: Kette unterbrochen bei Eintrag #{ver.broken_at} ({ver.ts}) – das Protokoll wurde außerhalb von Zeitprotokoll verändert.</div>)}
        {ver?.file && <div style={{ fontSize: 11, color: "var(--text2)", marginTop: 4 }}>Datei: {ver.file}</div>}
      </Box>
      <div className="zp-card" style={{ padding: "10px 16px" }}>
        <div style={{ display: "flex", gap: 6, marginBottom: 6 }}>
          {[["", "Alle"], ["update", "Geändert"], ["delete", "Gelöscht"], ["create", "Angelegt"]].map(([k, l]) => (
            <button key={k} onClick={() => setAction(k)} style={{ border: "1px solid " + (action === k ? "var(--accent)" : "var(--line)"), background: action === k ? "var(--accent-soft)" : "var(--card)", color: action === k ? "var(--accent)" : "var(--text2)", borderRadius: 20, padding: "3px 11px", fontSize: 12 }}>{l}</button>))}
          <span style={{ marginLeft: "auto", fontSize: 12, color: "var(--text2)" }}>{data ? `${data.rows.length} von ${data.total}` : ""}</span>
        </div>
        {!data ? "…" : data.rows.map(r => <AuditLine key={r.id} r={r} projects={data.projects} tasks={data.tasks} />)}
      </div>
    </div>);
}

function TodoInfo({ t, onDone }) {
  const [err, setErr] = useState(null);
  const act = async (m, p, b) => { const r = await call(m, p, b); if (r?.error) return setErr(r.error); onDone(); };
  return (
    <div style={{ fontSize: 13 }}>
      <div style={{ fontWeight: 600 }}>{t.ticket && <span style={{ color: t.system === "plane" ? "#7c5cd6" : "#2f6fd6", marginRight: 5 }}>{t.ticket}</span>}{t.title}</div>
      <div style={{ color: "var(--text2)", fontSize: 12, marginTop: 3 }}>
        {t.scheduled_date ? `geplant ${new Date(t.scheduled_date + "T12:00").toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit" })}${t.block_start ? " " + t.block_start : ""}` : "ohne Tag"}
        {t.due_date ? ` · fällig ${new Date(t.due_date + "T12:00").toLocaleDateString("de-DE", { day: "2-digit", month: "2-digit" })}` : ""} · {durTxt((t.estimated_minutes || 0) * 60)}</div>
      {t.notes && <div style={{ fontSize: 12, marginTop: 6 }}>{t.notes}</div>}
      <div style={{ display: "flex", gap: 6, marginTop: 10, flexWrap: "wrap" }}>
        {t.status !== "done" && <button style={btn(true)} onClick={() => act("POST", `/todo/${t.id}/start`)}><Play size={13} /> Timer starten</button>}
        {t.status !== "done" && <button style={btn(false)} onClick={() => act("PUT", `/todo/${t.id}`, { done: true })}><Check size={13} /> Erledigt</button>}
        {t.block_start && <button style={btn(false)} onClick={() => act("PUT", `/todo/${t.id}`, { block_start: "" })}>Uhrzeit entfernen</button>}
        <button style={btn(false)} onClick={() => { location.hash = "planung"; }}>Zu den To-dos</button>
      </div>
      {err && <div style={{ color: "var(--stop)", fontSize: 12, marginTop: 6 }}>{err}</div>}
      <div style={{ fontSize: 11, color: "var(--text2)", marginTop: 8 }}>Tipp: To-do im Kalender auf eine Uhrzeit ziehen = fest einplanen (und, falls eingeschaltet, als Tempo-Plan).</div>
    </div>);
}
