/* ═══════════════════════════════════════════════════════════════════
   Gemeinsame Teile für „Team“ (Teamleitung) und „Personal“: nur im Servermodus eingebunden.
   Monatswahl, Status-Badge, Personentabelle, Personen-Detail, Kommentar-Dialog, Fehlertexte.
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback, useMemo } from "react";
import { ChevronLeft, ChevronRight, ArrowLeft, RefreshCw, Info, Lock, PencilLine } from "lucide-react";
import { call, Box, lab, btn, hm } from "./zp-ui.jsx";

/* ── Monate, Zahlen, Zeit ── */
const MONTHS = ["Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"];
export const monthLabel = m => `${MONTHS[+m.slice(5, 7) - 1]} ${m.slice(0, 4)}`;
export const shiftMonth = (m, d) => { const x = new Date(+m.slice(0, 4), +m.slice(5, 7) - 1 + d, 1); return `${x.getFullYear()}-${String(x.getMonth() + 1).padStart(2, "0")}`; };
export const curMonth = () => { const x = new Date(); return `${x.getFullYear()}-${String(x.getMonth() + 1).padStart(2, "0")}`; };
export const prevMonth = () => shiftMonth(curMonth(), -1);
const monthRange = m => { const y = +m.slice(0, 4), mo = +m.slice(5, 7); return { from: `${m}-01`, to: `${m}-${String(new Date(y, mo, 0).getDate()).padStart(2, "0")}` }; };
const num = v => (typeof v === "number" && isFinite(v) ? v : (v == null || v === "" || isNaN(+v) ? null : +v));
export const fmtH = v => { const n = num(v); return n == null ? "–" : n.toLocaleString("de-DE", { minimumFractionDigits: 2, maximumFractionDigits: 2 }) + " h"; };
export const fmtSigned = v => { const n = num(v); return n == null ? "–" : (n > 0 ? "+" : "") + n.toLocaleString("de-DE", { minimumFractionDigits: 2, maximumFractionDigits: 2 }) + " h"; };
export const when = iso => { try { return new Date(iso).toLocaleString("de-DE", { day: "2-digit", month: "2-digit", year: "2-digit", hour: "2-digit", minute: "2-digit" }); } catch (_) { return ""; } };
const deDay = d => `${d.slice(8, 10)}.${d.slice(5, 7)}.`;
const WD = ["So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"];
const ABS = { urlaub: "Urlaub", krankheit: "Krankheit", feiertag: "Feiertag", fortbildung: "Fortbildung", sonstiges: "Sonstiges" };
const ACT = { submit: "eingereicht", withdraw: "zurückgezogen", approve: "freigegeben", reject: "abgelehnt", reopen: "wiedereröffnet" };

/* ── Fehlertexte: 404 = keine Berechtigung (Server verbirgt Unberechtigtes) ── */
export const friendly = (err, fallback = "Keine Berechtigung für diese Ansicht.") =>
  !err ? "" : /nicht gefunden|HTTP 404/i.test(err) ? fallback : err;

/* ── Status ── */
export const STATUS = {
  open: ["offen", ""], submitted: ["eingereicht", "changed"], approved: ["freigegeben", "synced"],
  rejected: ["abgelehnt", "error"], closed: ["geschlossen", "new"], unknown: ["unbekannt", ""],
};
export function StatusBadge({ status, raw }) {
  const [lbl, cls] = STATUS[status] || STATUS.unknown;
  return <span className={`zp-pill ${cls}`} title={raw && raw !== status ? `Status laut Server: ${raw}` : undefined}>{lbl}</span>;
}

/* ── Monatswahl (Pfeile wie in der Monatsfreigabe; kein Blättern in die Zukunft) ── */
export function MonthNav({ month, onChange }) {
  const arrow = (d, dis) => <button className="zp-nav" disabled={dis} onClick={() => onChange(shiftMonth(month, d))} title={d < 0 ? "Vormonat" : "Folgemonat"}>{d < 0 ? <ChevronLeft size={16} /> : <ChevronRight size={16} />}</button>;
  return <div style={{ display: "flex", alignItems: "center", gap: 2 }}>{arrow(-1, false)}<b style={{ minWidth: 118, textAlign: "center", fontSize: 13 }}>{monthLabel(month)}</b>{arrow(1, month >= curMonth())}</div>;
}
export const RefreshBtn = ({ onClick, busy }) => <button className="zp-nav" onClick={onClick} disabled={busy} title="Aktualisieren"><RefreshCw size={15} /></button>;

export const Note = ({ children, icon = <Info size={15} />, warn }) => (
  <div role="status" style={{ display: "flex", gap: 8, alignItems: "flex-start", fontSize: 13, padding: "9px 12px", borderRadius: 9, marginBottom: 12,
    background: warn ? "var(--pause-bg, #fff7e0)" : "var(--accent-soft)", border: `1px solid ${warn ? "var(--pause-line, #e5c46b)" : "transparent"}` }}>
    <span style={{ flex: "none", marginTop: 1, display: "flex", color: warn ? "#8a6400" : "var(--accent)" }}>{icon}</span><span>{children}</span>
  </div>);
export const Empty = ({ children }) => <div style={{ color: "var(--text2)", fontSize: 13, padding: "18px 4px", textAlign: "center" }}>{children}</div>;
export const Err = ({ children }) => <div style={{ color: "var(--stop)", fontSize: 13, padding: "10px 2px" }}>✗ {children}</div>;

/* ── Kommentar-/Begründungsdialog (Pflicht, mindestens 3 Zeichen) ── */
export function ReasonDialog({ title, text, label = "Kommentar", confirmLabel, danger, min = 3, onSubmit, onClose }) {
  const [v, setV] = useState(""); const [busy, setBusy] = useState(false); const [err, setErr] = useState("");
  const ok = v.trim().length >= min;
  const go = async () => {
    if (!ok || busy) return;
    setBusy(true); setErr("");
    const e = await onSubmit(v.trim());
    setBusy(false);
    if (e) setErr(e); else onClose();
  };
  return (
    <div role="dialog" aria-modal="true" aria-label={title} onClick={e => e.target === e.currentTarget && !busy && onClose()}
      style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.35)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 200, padding: 16 }}>
      <div className="zp-card" style={{ width: "100%", maxWidth: 460, padding: "18px 20px", boxShadow: "0 10px 40px rgba(0,0,0,.25)" }}>
        <div style={{ fontWeight: 600, fontSize: 15 }}>{title}</div>
        {text && <div style={{ color: "var(--text2)", fontSize: 12.5, marginTop: 4, lineHeight: 1.5 }}>{text}</div>}
        <label style={lab}>{label} (Pflicht, mindestens {min} Zeichen)</label>
        <textarea autoFocus rows={3} style={{ width: "100%", padding: "7px 9px", resize: "vertical" }} value={v} onChange={e => setV(e.target.value)} />
        {err && <div style={{ color: "var(--stop)", fontSize: 12, marginTop: 6 }}>✗ {err}</div>}
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", marginTop: 14 }}>
          <button style={btn(false)} disabled={busy} onClick={onClose}>Abbrechen</button>
          <button style={{ ...btn(true), ...(danger ? { background: "var(--stop)" } : {}), opacity: ok && !busy ? 1 : .5 }} disabled={!ok || busy} onClick={go}>{busy ? "Sende…" : confirmLabel}</button>
        </div>
      </div>
    </div>);
}

/* ── Personentabelle (Team und Personal) ── */
const th = { textAlign: "left", fontSize: 11, fontWeight: 600, color: "var(--text2)", padding: "6px 8px", borderBottom: "1px solid var(--line)", whiteSpace: "nowrap" };
const thr = { ...th, textAlign: "right" };
const td = { padding: "8px 8px", borderBottom: "1px solid var(--line)", verticalAlign: "middle", fontSize: 13 };
const tdr = { ...td, textAlign: "right", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" };
const teamText = p => (p.teams && p.teams.length ? p.teams.map(t => t.name).join(", ") : p.team || "–");

export function Flags({ p }) {
  const nach = p.nachgetragen || (p.nachtraege || 0) > 0, late = (p.late_entries || 0) > 0;
  if (!nach && !late) return <span style={{ color: "var(--text3)" }}>–</span>;
  return (
    <span style={{ display: "inline-flex", gap: 4, flexWrap: "wrap" }}>
      {nach && <span className="zp-pill changed" title="Nach Monatsschluss nachgetragen">nachgetragen{p.nachtraege > 0 ? ` (${p.nachtraege})` : ""}</span>}
      {late && <span className="zp-pill changed" title="Spät erfasst (nach der Frist)">spät erfasst ({p.late_entries})</span>}
    </span>);
}

/* extra: optionale Spalte { head, cell(p) } */
export function PeopleTable({ people, onOpen, extra }) {
  return (
    <div style={{ overflowX: "auto" }}>
      <table style={{ width: "100%", borderCollapse: "collapse", minWidth: 860 }}>
        <thead><tr>
          <th style={th}>Name</th><th style={th}>Team</th><th style={th}>Status</th>
          <th style={thr} title="Soll bis heute (laufender Monat) bzw. Monats-Soll">Soll bis heute</th><th style={thr}>Ist</th><th style={thr}>Saldo</th><th style={thr}>Konto</th>
          <th style={thr} title="Offene Prüfhinweise">Hinweise</th><th style={th}>Kennzeichen</th>{extra && <th style={th}>{extra.head}</th>}
        </tr></thead>
        <tbody>
          {people.map(p => (
            <tr key={p.id} onClick={() => onOpen(p)} style={{ cursor: "pointer" }} onMouseEnter={e => e.currentTarget.style.background = "var(--fill)"} onMouseLeave={e => e.currentTarget.style.background = ""}>
              <td style={{ ...td, fontWeight: 600 }}><button onClick={e => { e.stopPropagation(); onOpen(p); }} style={{ all: "unset", cursor: "pointer", fontWeight: 600 }}>{p.name}</button>{p.self && <span style={{ color: "var(--text2)", fontWeight: 400 }}> (du)</span>}</td>
              <td style={{ ...td, color: "var(--text2)" }}>{teamText(p)}</td>
              <td style={td}><StatusBadge status={p.status} raw={p.status_raw} /></td>
              <td style={tdr}>{fmtH(p.soll_bis_heute_h)}</td><td style={tdr}>{fmtH(p.ist_h)}</td>
              <td style={{ ...tdr, color: num(p.saldo_h) < 0 ? "var(--stop)" : "var(--text)" }}>{fmtSigned(p.saldo_h)}</td>
              <td style={{ ...tdr, color: num(p.konto_h) < 0 ? "var(--stop)" : "var(--text)" }}>{fmtSigned(p.konto_h)}</td>
              <td style={tdr}>{(p.checks_open || 0) > 0 ? <span className="zp-pill changed">{p.checks_open}</span> : <span style={{ color: "var(--text3)" }}>0</span>}</td>
              <td style={td}><Flags p={p} /></td>
              {extra && <td style={td} onClick={e => e.stopPropagation()}>{extra.cell(p)}</td>}
            </tr>))}
        </tbody>
      </table>
    </div>);
}

/* ── Personen-Detail ───────────────────────────────────────────────
   Daten der Zielperson über die vorhandenen Lese-Routen mit ?user=<id>.
   renderActions({ status, reload }) : optionale Schaltflächen (nur Personal). */
const isoWeekKey = date => {
  const d = new Date(date + "T12:00:00Z"); const day = (d.getUTCDay() + 6) % 7;   // Mo = 0
  d.setUTCDate(d.getUTCDate() - day + 3);
  const y = d.getUTCFullYear(), j1 = new Date(Date.UTC(y, 0, 4));
  return { y, w: 1 + Math.round(((d - j1) / 864e5 - 3 + ((j1.getUTCDay() + 6) % 7)) / 7) };
};
const entryHours = e => (e.duration_seconds != null ? e.duration_seconds / 3600 : (Date.parse(e.end_time) - Date.parse(e.start_time)) / 36e5) || 0;
const entryWhat = e => [e.jira_ticket || e.ticket, e.task_title, e.project_name].filter(Boolean).join(" · ") || "(ohne Zuordnung)";

export function PersonDetail({ person, month, onMonth, onBack, renderActions, onChanged }) {
  const [d, setD] = useState(null);
  const [busy, setBusy] = useState(false);
  const { from, to } = useMemo(() => monthRange(month), [month]);
  const q = `user=${encodeURIComponent(person.id)}`;

  const load = useCallback(async () => {
    setBusy(true);
    const [st, ent, ov, ab] = await Promise.all([
      call("GET", `/approval/status?month=${month}&${q}`),
      call("GET", `/time-entries?from=${from}&to=${to}&${q}`),
      call("GET", `/overview?from=${from}&to=${to}&${q}`),
      call("GET", `/absences?from=${from}&to=${to}&${q}`),
    ]);
    setD({ st, ent, ov, ab });
    setBusy(false);
  }, [month, from, to, q]);
  useEffect(() => { setD(null); load(); }, [load]);
  const reload = async () => { await load(); onChanged && onChanged(); };

  const denied = d && [d.st, d.ent, d.ov].every(x => x.error) && /nicht gefunden|HTTP 404/i.test(d.st.error);
  const status = d && !d.st.error ? d.st.status : null;
  const entries = d && !d.ent.error ? (Array.isArray(d.ent) ? d.ent : d.ent.items || []) : null;

  // Tagesliste
  const days = useMemo(() => {
    if (!entries) return null;
    const m = new Map();
    entries.forEach(e => { const k = (e.date || e.start_time || "").slice(0, 10); if (!k) return; if (!m.has(k)) m.set(k, []); m.get(k).push(e); });
    return [...m.entries()].sort((a, b) => a[0] < b[0] ? -1 : 1).map(([date, list]) => ({ date, list: list.sort((a, b) => (a.start_time || "") < (b.start_time || "") ? -1 : 1) }));
  }, [entries]);
  // Soll/Ist je Woche
  const weeks = useMemo(() => {
    if (!d || d.ov.error || !Array.isArray(d.ov.days)) return null;
    const m = new Map();
    d.ov.days.forEach(x => {
      if (x.date < from || x.date > to) return;
      const k = isoWeekKey(x.date), key = `${k.y}-${k.w}`;
      const w = m.get(key) || { label: `KW ${k.w}`, first: x.date, last: x.date, soll: 0, ist: 0 };
      w.last = x.date; w.soll += x.targetH || 0; w.ist += (x.bookedH || 0) + (x.absenceH || 0); m.set(key, w);
    });
    return [...m.values()];
  }, [d, from, to]);
  const absences = d && !d.ab.error && Array.isArray(d.ab) ? d.ab : null;

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 12, flexWrap: "wrap" }}>
        <button style={btn(false)} onClick={onBack}><ArrowLeft size={14} /> Zurück zur Übersicht</button>
        <div style={{ flex: 1 }} />
        <MonthNav month={month} onChange={onMonth} /><RefreshBtn onClick={load} busy={busy} />
      </div>
      <Box title={<>{person.name}{person.self && <span style={{ color: "var(--text2)", fontWeight: 400 }}> (du)</span>}</>} sub={`${teamText(person)} · ${monthLabel(month)}`}
        right={status && <StatusBadge status={status} />}>
        {!d ? <div style={{ color: "var(--text3)", fontSize: 12 }}>Lade…</div> : denied ? <Err>Keine Berechtigung für diese Person.</Err> : (<>
          {d.st.error ? <Err>{friendly(d.st.error)}</Err> : (<>
            <div style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap", fontSize: 12.5, color: "var(--text2)" }}>
              {status === "submitted" && d.st.submitted_at && <span>eingereicht {when(d.st.submitted_at)}</span>}
              {(status === "approved" || status === "rejected") && (d.st.decided_by_name || d.st.decided_at) && <span>{d.st.decided_by_name ? `von ${d.st.decided_by_name} ` : ""}{d.st.decided_at ? `am ${when(d.st.decided_at)}` : ""}</span>}
              <div style={{ flex: 1 }} />
              {renderActions && renderActions({ status, reload })}
            </div>
            {status === "rejected" && d.st.comment && <div style={{ marginTop: 8, padding: "8px 12px", borderRadius: 8, background: "rgba(217,69,59,.08)", fontSize: 13 }}>Abgelehnt: „{d.st.comment}“</div>}
          </>)}
        </>)}
      </Box>

      {d && !denied && (<>
        <Box title="Soll / Ist je Woche" sub="Abwesenheiten zählen zum Ist.">
          {!weeks ? (d.ov.error ? <Err>{friendly(d.ov.error)}</Err> : <Empty>Keine Daten.</Empty>) : (
            <table style={{ width: "100%", borderCollapse: "collapse" }}>
              <thead><tr><th style={th}>Woche</th><th style={th}>Zeitraum</th><th style={thr}>Soll</th><th style={thr}>Ist</th><th style={thr}>Saldo</th></tr></thead>
              <tbody>
                {weeks.map(w => <tr key={w.label + w.first}><td style={td}>{w.label}</td><td style={{ ...td, color: "var(--text2)" }}>{deDay(w.first)}–{deDay(w.last)}</td>
                  <td style={tdr}>{fmtH(w.soll)}</td><td style={tdr}>{fmtH(w.ist)}</td><td style={{ ...tdr, color: w.ist - w.soll < 0 ? "var(--stop)" : "var(--text)" }}>{fmtSigned(w.ist - w.soll)}</td></tr>)}
                <tr><td style={{ ...td, fontWeight: 600 }} colSpan={2}>Monat</td><td style={{ ...tdr, fontWeight: 600 }}>{fmtH(weeks.reduce((a, w) => a + w.soll, 0))}</td>
                  <td style={{ ...tdr, fontWeight: 600 }}>{fmtH(weeks.reduce((a, w) => a + w.ist, 0))}</td>
                  <td style={{ ...tdr, fontWeight: 600 }}>{fmtSigned(weeks.reduce((a, w) => a + w.ist - w.soll, 0))}</td></tr>
              </tbody>
            </table>)}
        </Box>

        <Box title="Buchungen des Monats" sub="Nach Tag gruppiert, nur lesend.">
          {!days ? <Err>{friendly(d.ent.error)}</Err> : !days.length ? <Empty>Keine Buchungen in diesem Monat.</Empty> : days.map(day => (
            <div key={day.date} style={{ marginBottom: 10 }}>
              <div style={{ display: "flex", gap: 10, fontSize: 12.5, fontWeight: 600, borderBottom: "1px solid var(--line)", padding: "3px 0" }}>
                <span>{WD[new Date(day.date + "T12:00").getDay()]}, {deDay(day.date)}</span><span style={{ flex: 1 }} /><span style={{ fontVariantNumeric: "tabular-nums" }}>{fmtH(day.list.reduce((a, e) => a + entryHours(e), 0))}</span>
              </div>
              {day.list.map((e, i) => (
                <div key={e.id || i} style={{ display: "flex", gap: 10, fontSize: 12.5, padding: "3px 0", color: "var(--text2)", alignItems: "baseline", flexWrap: "wrap" }}>
                  <span style={{ minWidth: 92, fontVariantNumeric: "tabular-nums" }}>{hm(e.start_time)}–{hm(e.end_time)}</span>
                  <span style={{ color: "var(--text)", flex: "1 1 220px" }}>{entryWhat(e)}{(e.comment || e.note) ? <span style={{ color: "var(--text2)" }}> – {e.comment || e.note}</span> : null}</span>
                  {e.entered_by_name || e.entered_by ? <span className="zp-pill changed" title="Nachgetragen"><PencilLine size={11} /> von Teamleitung nachgetragen{e.entered_by_name ? ` (${e.entered_by_name})` : ""}</span> : null}
                  <span style={{ minWidth: 64, textAlign: "right", fontVariantNumeric: "tabular-nums" }}>{fmtH(entryHours(e))}</span>
                </div>))}
            </div>))}
        </Box>

        <Box title="Abwesenheiten">
          {!absences ? (d.ab.error ? <Err>{friendly(d.ab.error)}</Err> : <Empty>Keine Daten.</Empty>) : !absences.length ? <Empty>Keine Abwesenheiten in diesem Monat.</Empty> : absences.map((a, i) => (
            <div key={a.id || i} style={{ display: "flex", gap: 10, fontSize: 12.5, padding: "3px 0", borderTop: i ? "1px solid var(--line)" : "none" }}>
              <span style={{ minWidth: 110 }}>{deDay(a.date_from)}{a.date_to !== a.date_from ? `–${deDay(a.date_to)}` : ""}</span><b style={{ minWidth: 90 }}>{ABS[a.type] || a.type}</b><span style={{ color: "var(--text2)" }}>{a.note || ""}</span></div>))}
        </Box>

        <Box title="Statusverlauf">
          {d.st.error ? <Err>{friendly(d.st.error)}</Err> : !(d.st.history || []).length ? <Empty>Noch kein Verlauf.</Empty> : d.st.history.map((h, i) => (
            <div key={i} style={{ display: "flex", gap: 10, fontSize: 12, color: "var(--text2)", borderTop: i ? "1px solid var(--line)" : "none", padding: "4px 0", flexWrap: "wrap" }}>
              <span style={{ minWidth: 104 }}>{when(h.at)}</span><span style={{ minWidth: 100, color: "var(--text)" }}>{ACT[h.action] || h.action}</span>
              <span style={{ minWidth: 90 }}>{h.actor || ""}</span><span style={{ flex: 1 }}>{h.comment ? `„${h.comment}“` : ""}</span></div>))}
        </Box>

        {/* Platz für das Nachtragen nach Monatsschluss (Teamleitung/Personal) – Bildschirm folgt */}
        <div data-slot="nachtragen" className="zp-card" style={{ padding: "14px 18px", display: "flex", gap: 12, alignItems: "flex-start", borderStyle: "dashed" }}>
          <Lock size={18} style={{ flex: "none", color: "var(--text2)", marginTop: 1 }} />
          <div><div style={{ fontWeight: 600, fontSize: 14 }}>Nachtragen – folgt</div>
            <div style={{ color: "var(--text2)", fontSize: 12.5, marginTop: 2 }}>Buchungen nach Monatsschluss nachtragen (mit Begründung und Protokoll) ist hier noch nicht möglich.</div></div>
        </div>
      </>)}
    </div>);
}
