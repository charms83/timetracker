/* ═══════════════════════════════════════════════════════════════════
   Tagesüberblick (v6.12.0) – große Ansicht: Zeitleiste des Tages
   • Anwesenheit am Mac (optional) = von wann bis wann gearbeitet
   • Buchungen  • offene Termine  • noch nicht gebuchte SSH-Sitzungen
   • am Mac, aber nichts gebucht → anklicken = eintragen
   Kleine Ansicht: eine Zeile mit den Zahlen.
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback } from "react";
import { ChevronLeft, ChevronRight, Terminal, CalendarClock, Plus, X } from "lucide-react";
import { api, projColor, localDate, askConfirm } from "./zp-ui.jsx";
import { NewEntry } from "./zp-calendar.jsx";

const hhmm = m => `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;
const dur = m => { m = Math.max(0, Math.round(m)); const h = Math.floor(m / 60); return h ? `${h}:${String(m % 60).padStart(2, "0")} h` : `${m} min`; };
const shift = (d, n) => { const x = new Date(d + "T12:00:00"); x.setDate(x.getDate() + n); return localDate(x); };
const getOpt = () => { try { return localStorage.getItem("zp-day-presence") !== "0"; } catch (_) { return true; } };

export function useDayOverview(date, tick) {
  const [d, setD] = useState(null);
  const load = useCallback(() => api("GET", `/day-overview?date=${date}`).then(r => r && !r.error && setD(r)), [date]);
  useEffect(() => { load(); const t = setInterval(load, 60000); return () => clearInterval(t); }, [load, tick]);
  return [d, load];
}

/* Kleine Ansicht: eine Zeile */
export function DaySummaryLine({ date, tick }) {
  const [d] = useDayOverview(date, tick);
  if (!d) return null;
  const parts = [];
  if (d.work) parts.push(`Arbeitstag ${hhmm(d.work.s)}–${d.now != null && d.work.e >= d.now ? "jetzt" : hhmm(d.work.e)}`);
  parts.push(`gebucht ${dur(d.booked_minutes)}`);
  if (d.gap_minutes) parts.push(`noch eintragen ${dur(d.gap_minutes)}`);
  if (d.ssh.length) parts.push(`${d.ssh.length} SSH offen`);
  if (d.events.length) parts.push(`${d.events.length} Termine offen`);
  return <a href="#kalender" className="zp-day-compact" style={{ display: "none", fontSize: 12, color: "var(--text2)", padding: "2px 4px", textDecoration: "none" }}>{parts.join(" · ")} ›</a>;
}

export function DayPanel({ initialDate, projects, tick, onChanged }) {
  const [date, setDate] = useState(initialDate || localDate(new Date()));
  useEffect(() => { if (initialDate) setDate(initialDate); }, [initialDate]);
  const [d, load] = useDayOverview(date, tick);
  const [showPres, setShowPres] = useState(getOpt);
  const [fill, setFill] = useState(null);
  useEffect(() => { try { localStorage.setItem("zp-day-presence", showPres ? "1" : "0"); } catch (_) {} }, [showPres]);
  if (!d) return <div className="zp-card" style={{ padding: 14, color: "var(--text2)", fontSize: 12 }}>Lade Tag …</div>;

  const pts = [d.work?.s, d.work?.e, ...d.entries.flatMap(e => [e.s, e.e]), ...d.events.flatMap(e => [e.s, e.e]), ...d.ssh.flatMap(e => [e.s, e.e])].filter(v => v != null);
  const startH = Math.min(7, ...pts.map(m => Math.floor(m / 60)));
  const endH = Math.max(18, ...pts.map(m => Math.ceil(m / 60)));
  const PX = 38, top = m => (m - startH * 60) / 60 * PX, H = (endH - startH) * PX;
  const today = date === localDate(new Date());
  const box = (k, s, e, style, children, onClick, title) => (
    <div key={k} title={title} onClick={onClick} style={{ position: "absolute", top: top(s) + 1, height: Math.max(16, (e - s) / 60 * PX - 2), overflow: "hidden", borderRadius: 6, fontSize: 11, lineHeight: 1.25, padding: "2px 6px", cursor: onClick ? "pointer" : "default", ...style }}>{children}</div>);

  return (
    <div className="zp-card" style={{ padding: 0, overflow: "hidden" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "10px 12px", borderBottom: "1px solid var(--line)" }}>
        <button className="zp-nav" onClick={() => setDate(shift(date, -1))}><ChevronLeft size={15} /></button>
        <div style={{ flex: 1, textAlign: "center", fontWeight: 600, fontSize: 13 }}>{today ? "Heute" : new Date(date + "T12:00").toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit" })}</div>
        <button className="zp-nav" onClick={() => setDate(shift(date, 1))} disabled={today}><ChevronRight size={15} /></button>
      </div>
      {/* Zahlen */}
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr 1fr", gap: 4, padding: "8px 12px", fontSize: 11, color: "var(--text2)", borderBottom: "1px solid var(--line)" }}>
        <div>Arbeitstag<div style={{ fontSize: 13, fontWeight: 700, color: "var(--text)" }}>{d.work ? `${hhmm(d.work.s)}–${d.now != null && d.work.e >= d.now ? "jetzt" : hhmm(d.work.e)}` : "–"}</div></div>
        <div>Gebucht<div style={{ fontSize: 13, fontWeight: 700, color: "var(--text)" }}>{dur(d.booked_minutes)}</div></div>
        <div>Noch eintragen<div style={{ fontSize: 13, fontWeight: 700, color: d.gap_minutes ? "#b58100" : "var(--accent)" }}>{d.gap_minutes ? dur(d.gap_minutes) : "nichts"}</div></div>
      </div>
      <label style={{ display: "flex", gap: 6, alignItems: "center", fontSize: 11, color: "var(--text2)", padding: "6px 12px" }}>
        <input type="checkbox" checked={showPres} onChange={e => setShowPres(e.target.checked)} /> Zeit am Mac zeigen
        {!d.tracking && <span style={{ color: "#b58100" }}>(Erfassung aus – Einstellungen › Erkennung)</span>}
      </label>
      {/* Zeitleiste */}
      <div style={{ position: "relative", height: H, margin: "4px 10px 10px 40px" }}>
        {[...Array(endH - startH + 1)].map((_, i) => <div key={i} style={{ position: "absolute", top: i * PX, left: -36, right: 0, borderTop: "1px solid var(--line)", fontSize: 10, color: "var(--text2)" }}><span style={{ position: "relative", top: -7, background: "var(--card)", paddingRight: 4 }}>{String(startH + i).padStart(2, "0")}:00</span></div>)}
        {/* Anwesenheit */}
        {showPres && d.presence.map((p, i) => box("p" + i, p.s, p.e, { left: 0, width: 5, padding: 0, borderRadius: 3, background: "color-mix(in srgb, var(--accent) 55%, transparent)" }, null, null, `Am Mac ${hhmm(p.s)}–${hhmm(p.e)}`))}
        {!showPres || !d.presence.length ? d.work && box("w", d.work.s, d.work.e, { left: 0, width: 5, padding: 0, borderRadius: 3, background: "color-mix(in srgb, var(--accent) 25%, transparent)" }, null, null, "Arbeitstag") : null}
        {/* Pausen */}
        {d.pauses.map((p, i) => box("pa" + i, p.s, p.e, { left: 9, right: 0, background: "repeating-linear-gradient(45deg, color-mix(in srgb, #e0a800 14%, transparent) 0 5px, transparent 5px 10px)", color: "var(--text2)" }, "Pause"))}
        {/* am Mac, nichts gebucht */}
        {showPres && d.gaps.map((g, i) => box("g" + i, g.s, g.e, { left: 9, right: "38%", border: "1.5px dashed #b58100", background: "color-mix(in srgb, #b58100 6%, transparent)", color: "#8a6200", fontWeight: 600 },
          <span><Plus size={10} style={{ verticalAlign: -1 }} /> {hhmm(g.s)}–{hhmm(g.e)} · {dur(g.e - g.s)} eintragen</span>, () => setFill(g), "Am Mac, aber nichts gebucht – anklicken zum Eintragen"))}
        {/* Buchungen (links) */}
        {d.entries.map(e => { const c = projColor(e.project_id); return box("e" + e.id, e.s, e.e, { left: 9, right: "38%", background: `color-mix(in srgb, ${c} 16%, var(--card))`, borderLeft: `3px solid ${c}`, color: "var(--text)" },
          <><b style={{ color: c, filter: "brightness(.8)" }}>{e.jira_ticket || ""}</b> {e.task_title || e.comment || e.project_name}</>, null, `${hhmm(e.s)}–${hhmm(e.e)} ${e.jira_ticket || ""} ${e.task_title || ""} ${e.comment || ""}`); })}
        {/* Offen (rechts): Termine, SSH */}
        {d.events.map(ev => box("c" + ev.uid, ev.s, ev.e, { left: "63%", right: 0, background: "color-mix(in srgb, #7c5cd6 10%, var(--card))", border: "1px solid color-mix(in srgb, #7c5cd6 35%, transparent)", color: "var(--text)" },
          <><CalendarClock size={10} style={{ color: "#7c5cd6", verticalAlign: -1 }} /> {ev.title}</>, () => { location.hash = "kalender"; }, `Termin (nicht gebucht): ${ev.title} ${hhmm(ev.s)}–${hhmm(ev.e)}`))}
        {d.ssh.map(x => box("s" + x.id, x.s, x.e, { left: "63%", right: 0, background: "color-mix(in srgb, var(--text2) 10%, var(--card))", border: "1px solid var(--line)", color: "var(--text)" },
          <><Terminal size={10} style={{ verticalAlign: -1 }} /> {x.server}</>, () => { location.hash = "vorschlaege/ssh"; }, `SSH (nicht gebucht): ${x.server} ${hhmm(x.s)}–${hhmm(x.e)}`))}
        {d.now != null && d.now >= startH * 60 && <div style={{ position: "absolute", top: top(d.now), left: -4, right: 0, borderTop: "2px solid var(--stop)", zIndex: 3 }} />}
      </div>
      <div style={{ display: "flex", gap: 10, flexWrap: "wrap", padding: "0 12px 10px", fontSize: 10.5, color: "var(--text2)" }}>
        <span><span style={{ display: "inline-block", width: 8, height: 8, borderRadius: 2, background: "var(--accent)", opacity: .6 }} /> am Mac</span>
        <span>links: gebucht</span><span>rechts: offen (Termin, SSH)</span><span style={{ color: "#8a6200" }}>gestrichelt: noch nicht eingetragen</span>
      </div>
      {fill && (
        <div style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.25)", zIndex: 200, display: "flex", alignItems: "flex-start", justifyContent: "center", paddingTop: 90 }} onMouseDown={e => e.target === e.currentTarget && setFill(null)}>
          <div className="zp-card" style={{ width: "min(440px, calc(100vw - 32px))", padding: "14px 16px" }}>
            <div style={{ display: "flex", alignItems: "center" }}><b style={{ flex: 1 }}>Zeit eintragen</b><button className="zp-nav" onClick={() => setFill(null)}><X size={14} /></button></div>
            <NewEntry date={date} startMin={fill.s} endMin={fill.e} projects={projects} onDone={() => { setFill(null); load(); onChanged && onChanged(); }} />
          </div>
        </div>)}
    </div>);
}

/* Einstellungen › Erkennung: Anwesenheit am Mac ein/aus */
export function PresenceToggle() {
  const [on, setOn] = useState(null);
  useEffect(() => { api("GET", "/settings").then(s => s && setOn(s.presence_track !== "0")); }, []);
  if (on == null) return null;
  return (
    <div className="zp-card" style={{ padding: "12px 16px", marginBottom: 12 }}>
      <label style={{ display: "flex", gap: 10, alignItems: "flex-start", cursor: "pointer" }}>
        <input type="checkbox" checked={on} style={{ marginTop: 3 }} onChange={async e => { const v = e.target.checked; await api("PUT", "/settings", { presence_track: v ? "1" : "0" }); setOn(v); }} />
        <span><b>Zeit am Mac erfassen</b> (für „von wann bis wann gearbeitet“ und was noch einzutragen ist)
          <span style={{ display: "block", fontSize: 12, color: "var(--text2)" }}>Die Mac-App merkt sich je Minute nur, ob jemand am Rechner ist (Eingabe in den letzten 5 Minuten, Bildschirm nicht gesperrt). Keine Programme, keine Inhalte. Wird nach 120 Tagen gelöscht.</span></span>
      </label>
    </div>);
}

/* Doppelt gebucht? Überschneidende Buchungen der letzten 14 Tage – zusammenführen, abschneiden oder so lassen */
const Empty = ({ text }) => <div style={{ fontSize: 12.5, color: "var(--accent)", padding: "6px 4px" }}>✓ {text}</div>;
export function OverlapCard({ tick, onChanged, days = 14, emptyText }) {
  const [list, setList] = useState(null); const [msg, setMsg] = useState(null);
  const load = useCallback(() => api("GET", "/overlaps?days=" + days).then(r => setList(Array.isArray(r) ? r : [])), [days]);
  useEffect(() => { load(); }, [load, tick]);
  if (!list) return null;
  if (!list.length) return msg || emptyText ? <Empty text={msg || emptyText} /> : null;
  const t = iso => { const d = new Date(iso); return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`; };
  const day = d => new Date(d + "T12:00").toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit" });
  const lbl = e => `${t(e.start_time)}–${t(e.end_time)} ${e.jira_ticket ? e.jira_ticket + " " : ""}${e.task_title || e.comment || e.project_name || ""}`;
  const act = async (o, mode) => {
    if (mode === "merge" && !confirm(`Zu einer Buchung zusammenführen?\n\n${lbl(o.a)}\n${lbl(o.b)}\n\n→ ${t(o.a.start_time < o.b.start_time ? o.a.start_time : o.b.start_time)}–${t(o.a.end_time > o.b.end_time ? o.a.end_time : o.b.end_time)}, Texte werden zusammengelegt.`)) return;
    const r = mode === "ok" ? await api("POST", "/overlaps/ignore", { a: o.a.id, b: o.b.id }) : await api("POST", "/time-entries/resolve-overlap", { a: o.a.id, b: o.b.id, mode });
    setMsg(r?.error ? null : mode === "merge" ? "Zusammengeführt" : mode === "trim" ? "Überlappung abgeschnitten" : "Bleibt so");
    if (r?.error) alert(r.error);
    load(); onChanged && onChanged();
  };
  return (
    <div className="zp-card" style={{ padding: "10px 14px", borderLeft: "3px solid #b58100" }}>
      <div style={{ fontWeight: 600, fontSize: 13 }}>Doppelt gebucht? {list.length} Überschneidung{list.length === 1 ? "" : "en"}</div>
      <div style={{ fontSize: 11.5, color: "var(--text2)", marginBottom: 4 }}>Meist derselbe Termin zweimal (Timer + Kalender) – dann zusammenführen. Zwei verschiedene Tickets: Überlappung bei der kürzeren abschneiden.</div>
      {list.slice(0, 5).map(o => (
        <div key={o.a.id + "-" + o.b.id} style={{ borderTop: "1px solid var(--line)", padding: "6px 0", fontSize: 12.5 }}>
          <div style={{ color: "var(--text2)", fontSize: 11 }}>{day(o.a.date)} · {o.minutes} min doppelt{o.same ? " · gleiches Ticket" : ""}</div>
          <div>{lbl(o.a)}</div><div>{lbl(o.b)}</div>
          <div style={{ display: "flex", gap: 6, marginTop: 4, flexWrap: "wrap" }}>
            {[["merge", "Zusammenführen"], ["trim", "Überlappung abschneiden"], ["ok", "Ist richtig so"]].filter(([k]) => !(k === "trim" && o.same)).map(([k, l]) => (
              <button key={k} onClick={() => act(o, k)} style={{ fontSize: 12, padding: "3px 10px", borderRadius: 7, cursor: "pointer", fontWeight: o.suggest === k ? 700 : 500,
                border: `1px solid ${o.suggest === k ? "var(--accent)" : "var(--line)"}`, background: o.suggest === k ? "var(--accent-soft)" : "var(--card)", color: o.suggest === k ? "var(--accent)" : "var(--text)" }}>{l}{o.suggest === k ? " (Vorschlag)" : ""}</button>))}
          </div>
        </div>))}
      {list.length > 5 && <div style={{ fontSize: 11, color: "var(--text2)" }}>… und {list.length - 5} weitere</div>}
    </div>);
}

/* Bitte prüfen: Buchungen, die die Plausibilitätsprüfung markiert hat (z. B. Timer > 10 h, Urlaubstag) – gehen erst nach Bestätigung nach Tempo */
export function ReviewCard({ tick, onChanged, emptyText }) {
  const [list, setList] = useState(null);
  const load = useCallback(() => api("GET", "/checks/open").then(r => setList(Array.isArray(r) ? r.filter(e => e.issues.some(i => i.code !== "overlap")) : [])), []);
  useEffect(() => { load(); }, [load, tick]);
  if (!list) return null;
  if (!list.length) return emptyText ? <Empty text={emptyText} /> : null;
  const t = iso => { const d = new Date(iso); return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`; };
  const ack = async e => {
    const ans = await askConfirm(e.issues.filter(i => i.code !== "overlap").map(i => ({ ...i, level: "confirm" })));
    const reason = ans?.reason; if (!reason) return;
    await api("POST", "/checks/ack", { id: e.id, reason }); load(); onChanged && onChanged();
  };
  return (
    <div className="zp-card" style={{ padding: "10px 14px", borderLeft: "3px solid #C4271B" }}>
      <div style={{ fontWeight: 600, fontSize: 13 }}>Bitte prüfen: {list.length} Buchung{list.length === 1 ? "" : "en"}</div>
      <div style={{ fontSize: 11.5, color: "var(--text2)", marginBottom: 4 }}>Diese Buchungen fallen aus dem Rahmen und gehen erst nach deiner Bestätigung nach Tempo. Stimmt etwas nicht: im Tag öffnen und korrigieren.</div>
      {list.slice(0, 6).map(e => (
        <div key={e.id} style={{ borderTop: "1px solid var(--line)", padding: "6px 0", fontSize: 12.5, display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
          <div style={{ flex: "1 1 260px" }}>
            <div>{new Date(e.date + "T12:00").toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit" })} · {t(e.start_time)}–{e.end_time ? t(e.end_time) : "…"} · {e.jira_ticket || e.project_name || ""} {e.comment ? `· ${e.comment}` : ""}</div>
            <div style={{ color: "#C4271B", fontSize: 11.5 }}>{e.issues.filter(i => i.code !== "overlap").map(i => i.text).join(" · ")}</div>
          </div>
          <button onClick={() => ack(e)} style={{ fontSize: 12, padding: "3px 10px", borderRadius: 7, border: "1px solid var(--line)", background: "var(--card)", color: "var(--text)", cursor: "pointer" }}>Stimmt so</button>
        </div>))}
    </div>);
}

/* Einstellungen › Erkennung: Ticket aus dem Browser übernehmen ein/aus */
export function BrowserToggle() {
  const [on, setOn] = useState(null);
  useEffect(() => { api("GET", "/settings").then(s => s && setOn(s.browser_detect !== "0")); }, []);
  if (on == null) return null;
  return (
    <div className="zp-card" style={{ padding: "12px 16px", marginBottom: 12 }}>
      <label style={{ display: "flex", gap: 10, alignItems: "flex-start", cursor: "pointer" }}>
        <input type="checkbox" checked={on} style={{ marginTop: 3 }} onChange={async e => { const v = e.target.checked; await api("PUT", "/settings", { browser_detect: v ? "1" : "0" }); setOn(v); }} />
        <span><b>Safari: Ticket erkennen</b> – Jira-/Plane-Ticket in Safari offen → Tracker öffnen → „▶ Tracken“ (Safari bekommt kein Add-on)
          <span style={{ display: "block", fontSize: 12, color: "var(--text2)" }}>Nur wenn du den Tracker öffnest, liest die Mac-App Adresse und Titel des aktiven Tabs (Safari, Chrome, Arc, Edge, Brave). Behalten wird nur eine Jira-/Plane-Ticketnummer deiner eingestellten Adressen – alles andere wird sofort verworfen, nichts gespeichert. macOS fragt einmal je Browser um Erlaubnis.</span></span>
      </label>
    </div>);
}
