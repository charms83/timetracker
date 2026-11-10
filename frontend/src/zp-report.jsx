/* ═══════════════════════════════════════════════════════════════════
   Auswertung – pro Ticket (Work-Item), Plane-Projekt oder Tag
   Die Zahlen kommen ausschließlich aus dem Zeitprotokoll (lokal).
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useMemo } from "react";
import { Download, ChevronDown, ChevronRight, Search } from "lucide-react";
import { api, localDate, hm, projColor, ticketLabel, EstimateBar } from "./zp-ui.jsx";

const pad = n => String(n).padStart(2, "0");
const h2 = s => ((s || 0) / 3600).toLocaleString("de-DE", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const hmin = s => { const m = Math.round((s || 0) / 60); return `${Math.floor(m / 60)}:${pad(m % 60)} h`; };
const euro = n => n.toLocaleString("de-DE", { style: "currency", currency: "EUR" });
const deDate = d => new Date(d + "T12:00").toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit", year: "numeric" });
const KEY_RE = /^[A-Za-z][A-Za-z0-9_]*-\d+$/;

function rangeOf(preset) {
  const now = new Date(); now.setHours(12, 0, 0, 0);
  const mon = d => { const x = new Date(d); x.setDate(x.getDate() - ((x.getDay() + 6) % 7)); return x; };
  const add = (d, n) => { const x = new Date(d); x.setDate(x.getDate() + n); return x; };
  switch (preset) {
    case "week": { const m = mon(now); return [m, add(m, 6)]; }
    case "lastweek": { const m = add(mon(now), -7); return [m, add(m, 6)]; }
    case "month": return [new Date(now.getFullYear(), now.getMonth(), 1, 12), new Date(now.getFullYear(), now.getMonth() + 1, 0, 12)];
    case "lastmonth": return [new Date(now.getFullYear(), now.getMonth() - 1, 1, 12), new Date(now.getFullYear(), now.getMonth(), 0, 12)];
    case "year": return [new Date(now.getFullYear(), 0, 1, 12), new Date(now.getFullYear(), 11, 31, 12)];
    default: return null;
  }
}
const PRESETS = [["week", "Diese Woche"], ["lastweek", "Letzte Woche"], ["month", "Dieser Monat"], ["lastmonth", "Letzter Monat"], ["year", "Dieses Jahr"], ["custom", "Zeitraum"]];
const GROUPS = [["ticket", "Ticket"], ["project", "Projekt"], ["activity", "Tätigkeit"], ["day", "Tag"]];

function inPlane(e) {
  if (e.source === "plane") return true;
  const k = (e.jira_ticket || "").trim().toUpperCase();
  return !!k && !e.plane_skip && e.plane_sum_key === k && e.plane_sum_secs === e.duration_seconds;
}

function downloadCsv(name, rows) {
  const csv = rows.map(r => r.map(c => `"${String(c ?? "").replace(/"/g, '""')}"`).join(";")).join("\r\n");
  Object.assign(document.createElement("a"), { href: "data:text/csv;charset=utf-8,﻿" + encodeURIComponent(csv), download: name }).click();
}

const chip = on => ({ padding: "5px 12px", borderRadius: 20, fontSize: 12, fontWeight: on ? 600 : 500, border: "1px solid " + (on ? "var(--accent)" : "var(--line)"),
  background: on ? "var(--accent-soft)" : "var(--card)", color: on ? "var(--accent)" : "var(--text2)" });
const btnS = { display: "inline-flex", alignItems: "center", gap: 6, padding: "6px 12px", borderRadius: 7, border: "1px solid var(--line)", background: "var(--card)", color: "var(--text)", fontWeight: 500, fontSize: 12 };

export function ReportView() {
  const [preset, setPreset] = useState(() => { try { return localStorage.getItem("zp.report.preset") || "month"; } catch (_) { return "month"; } });
  const [group, setGroup] = useState(() => { try { return localStorage.getItem("zp.report.group") || "ticket"; } catch (_) { return "ticket"; } });
  const [custom, setCustom] = useState(() => { const r = rangeOf("month"); return [localDate(r[0]), localDate(r[1])]; });
  const [data, setData] = useState(null);
  const [err, setErr] = useState("");
  const [open, setOpen] = useState({});
  const [q, setQ] = useState("");
  const [proj, setProj] = useState("");
  const [est, setEst] = useState({});
  useEffect(() => { api("GET", "/ticket-estimates").then(r => r && !r.error && setEst(r)); }, []);

  const [from, to] = preset === "custom" ? custom : rangeOf(preset).map(localDate);
  useEffect(() => { try { localStorage.setItem("zp.report.preset", preset); localStorage.setItem("zp.report.group", group); } catch (_) {} }, [preset, group]);
  useEffect(() => {
    if (!from || !to || from > to) return;
    let dead = false;
    setData(null); setErr("");
    api("GET", `/report?from=${from}&to=${to}`).then(r => {
      if (dead) return;
      if (!r || r.error) { setErr(r?.error || "Server nicht erreichbar"); return; }
      setData(r); setOpen({});
    });
    return () => { dead = true; };
  }, [from, to]);

  const entries = useMemo(() => (data?.entries || []).filter(e => e.end_time && e.duration_seconds > 0)
    .filter(e => !proj || String(e.project_id) === proj)
    .filter(e => !q.trim() || [e.jira_ticket, e.task_title, e.project_name, e.comment, e.customer_name, e.activity].some(v => (v || "").toLowerCase().includes(q.trim().toLowerCase()))), [data, q, proj]);

  const projects = useMemo(() => {
    const m = new Map(); (data?.entries || []).forEach(e => e.project_id && m.set(e.project_id, e.project_name)); return [...m].sort((a, b) => String(a[1]).localeCompare(String(b[1])));
  }, [data]);

  const kpi = useMemo(() => {
    let total = 0, ticket = 0, plane = 0, bill = 0, rev = 0; const days = new Set();
    for (const e of entries) {
      const s = e.duration_seconds; total += s; days.add(e.date);
      if (KEY_RE.test((e.jira_ticket || "").trim())) { ticket += s; if (inPlane(e)) plane += s; }
      if (e.billable) { bill += s; rev += s / 3600 * (e.hourly_rate || 0); }
    }
    return { total, ticket, noTicket: total - ticket, plane, bill, rev, days: days.size };
  }, [entries]);

  const groups = useMemo(() => {
    const m = new Map();
    for (const e of entries) {
      let k, label, sub, color;
      const key = (e.jira_ticket || "").trim().toUpperCase();
      if (group === "ticket") {
        k = key || (e.task_id ? "t" + e.task_id : "p" + (e.project_id || 0));
        label = key || e.task_title ? ticketLabel(key, e.task_title) : "Ohne Ticket";
        sub = e.project_name || "kein Projekt"; color = projColor(e.project_id);
      } else if (group === "activity") {
        k = "a" + (e.activity || ""); label = e.activity || "Ohne Tätigkeit"; sub = ""; color = e.activity ? "#4a6cf7" : "#c9a227";
      } else if (group === "project") {
        k = "p" + (e.project_id || 0); label = e.project_name || "Ohne Projekt"; sub = e.customer_name || ""; color = projColor(e.project_id);
      } else { k = e.date; label = deDate(e.date); sub = ""; color = "var(--accent)"; }
      if (!m.has(k)) m.set(k, { k, label, sub, color, secs: 0, plane: 0, n: 0, list: [], noTicket: group === "ticket" && !key && !e.task_title, ticket: group === "ticket" ? key : null });
      const g = m.get(k); g.secs += e.duration_seconds; g.n++; g.list.push(e); if (inPlane(e)) g.plane += e.duration_seconds;
    }
    const arr = [...m.values()];
    arr.forEach(g => g.list.sort((a, b) => a.start_time.localeCompare(b.start_time)));
    return group === "day" ? arr.sort((a, b) => a.k.localeCompare(b.k)) : arr.sort((a, b) => b.secs - a.secs);
  }, [entries, group]);
  const max = Math.max(1, ...groups.map(g => g.secs));

  const exportEntries = () => downloadCsv(`zeitprotokoll-buchungen-${from}-${to}.csv`, [
    ["Datum", "Start", "Ende", "Stunden", "Ticket", "Titel", "Projekt", "Kunde", "Tätigkeit", "Kommentar", "Abrechenbar", "In Plane"],
    ...entries.slice().sort((a, b) => a.start_time.localeCompare(b.start_time)).map(e => [e.date, hm(e.start_time), hm(e.end_time), h2(e.duration_seconds),
      e.jira_ticket || "", e.task_title || "", e.project_name || "", e.customer_name || "", e.activity || "", e.comment || "", e.billable ? "ja" : "nein", inPlane(e) ? "ja" : "nein"])]);
  const exportGroups = () => downloadCsv(`zeitprotokoll-${group}-${from}-${to}.csv`, [
    [GROUPS.find(g => g[0] === group)[1], "Zusatz", "Buchungen", "Stunden", "davon in Plane"],
    ...groups.map(g => [g.label, g.sub, g.n, h2(g.secs), h2(g.plane)])]);

  const Kpi = ({ l, v, s, c }) => (
    <div style={{ flex: "1 1 140px", padding: "10px 14px", borderRight: "1px solid var(--line)" }}>
      <div style={{ fontSize: 11, color: "var(--text2)" }}>{l}</div>
      <div style={{ fontSize: 18, fontWeight: 600, fontVariantNumeric: "tabular-nums", color: c || "var(--text)" }}>{v}</div>
      {s && <div style={{ fontSize: 11, color: "var(--text3)" }}>{s}</div>}
    </div>
  );
  const pct = (a, b) => b ? Math.round(a / b * 100) + " %" : "–";

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <div className="zp-card" style={{ padding: "12px 14px", display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center" }}>
        {PRESETS.map(([k, l]) => <button key={k} style={chip(preset === k)} onClick={() => { if (k === "custom") setCustom([from, to]); setPreset(k); }}>{l}</button>)}
        {preset === "custom" && <>
          <input type="date" value={custom[0]} onChange={e => setCustom([e.target.value, custom[1]])} style={{ padding: "4px 8px" }} />
          <span style={{ color: "var(--text3)" }}>–</span>
          <input type="date" value={custom[1]} onChange={e => setCustom([custom[0], e.target.value])} style={{ padding: "4px 8px" }} />
        </>}
        <div style={{ flex: 1 }} />
        <span style={{ color: "var(--text2)", fontSize: 12 }}>{deDate(from)} – {deDate(to)}</span>
      </div>

      {err && <div className="zp-card" style={{ padding: 16, color: "var(--stop)" }}>✗ {err}</div>}
      {!data && !err && <div style={{ color: "var(--text3)", padding: 30, textAlign: "center" }}>Lade…</div>}

      {data && <>
        <div className="zp-card" style={{ display: "flex", flexWrap: "wrap", overflow: "hidden" }}>
          <Kpi l="Gesamt" v={hmin(kpi.total)} s={`${kpi.days} Arbeitstage · ${entries.length} Buchungen`} />
          <Kpi l="Auf Tickets" v={hmin(kpi.ticket)} s={pct(kpi.ticket, kpi.total) + " der Zeit"} c="var(--accent)" />
          <Kpi l="Ohne Ticket" v={hmin(kpi.noTicket)} s={kpi.noTicket ? "nur im Zeitprotokoll" : "alles zugeordnet"} c={kpi.noTicket ? "#8a6400" : undefined} />
          <Kpi l="In Plane übertragen" v={pct(kpi.plane, kpi.ticket)} s={`${hmin(kpi.plane)} von ${hmin(kpi.ticket)}`} />
          {kpi.rev > 0 && <Kpi l="Abrechenbar" v={euro(kpi.rev)} s={hmin(kpi.bill)} />}
        </div>

        <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center" }}>
          <span style={{ fontSize: 12, color: "var(--text2)" }}>Gruppieren nach</span>
          {GROUPS.map(([k, l]) => <button key={k} style={chip(group === k)} onClick={() => { setGroup(k); setOpen({}); }}>{l}</button>)}
          <div style={{ position: "relative", marginLeft: 8 }}>
            <Search size={13} style={{ position: "absolute", left: 8, top: 8, color: "var(--text3)" }} />
            <input value={q} onChange={e => setQ(e.target.value)} placeholder="Ticket, Titel, Kommentar…" style={{ padding: "5px 8px 5px 26px", width: 220 }} />
          </div>
          <select value={proj} onChange={e => setProj(e.target.value)} style={{ padding: "5px 8px" }}>
            <option value="">Alle Projekte</option>
            {projects.map(([id, n]) => <option key={id} value={id}>{n}</option>)}
          </select>
          <div style={{ flex: 1 }} />
          <button style={btnS} onClick={exportGroups} disabled={!groups.length}><Download size={13} /> Übersicht (CSV)</button>
          <button style={btnS} onClick={exportEntries} disabled={!entries.length}><Download size={13} /> Alle Buchungen (CSV)</button>
        </div>

        <div className="zp-card" style={{ padding: "8px 0 4px" }}>
          {!groups.length && <div style={{ color: "var(--text3)", textAlign: "center", padding: 28 }}>Keine Buchungen in diesem Zeitraum.</div>}
          {groups.map(g => (
            <div key={g.k} style={{ margin: "0 10px 4px" }}>
              <div className="zp-entry" style={{ margin: 0, "--c": g.noTicket ? "#c9a227" : g.color, cursor: "pointer" }} onClick={() => setOpen(o => ({ ...o, [g.k]: !o[g.k] }))}>
                <span style={{ color: "var(--text3)", display: "flex" }}>{open[g.k] ? <ChevronDown size={14} /> : <ChevronRight size={14} />}</span>
                <div className="info">
                  <div className="t">{g.label}</div>
                  <div className="m">{[g.sub, `${g.n} ${g.n === 1 ? "Buchung" : "Buchungen"}`, group !== "day" && g.plane ? `${pct(g.plane, g.secs)} in Plane` : ""].filter(Boolean).join(" · ")}</div>
                </div>
                {g.ticket && est[g.ticket]?.estimate_h > 0 && (
                  <span style={{ textAlign: "right", flexShrink: 0 }} title="Gesamtzeit (alle Zeiträume) gegen Schätzung">
                    <EstimateBar secs={est[g.ticket].total_secs} estimate_h={est[g.ticket].estimate_h} width={50} />
                    <div style={{ fontSize: 10, color: "var(--text3)" }}>gesamt von {String(est[g.ticket].estimate_h).replace(".", ",")} h</div>
                  </span>
                )}
                <div style={{ width: 160, height: 6, borderRadius: 3, background: "var(--line)", overflow: "hidden", flexShrink: 0 }}>
                  <div style={{ width: `${g.secs / max * 100}%`, height: "100%", background: g.noTicket ? "#c9a227" : g.color }} />
                </div>
                <span style={{ fontWeight: 600, fontVariantNumeric: "tabular-nums", minWidth: 70, textAlign: "right" }}>{hmin(g.secs)}</span>
              </div>
              {open[g.k] && (
                <div style={{ padding: "2px 10px 8px 36px" }}>
                  {g.list.map(e => (
                    <div key={e.id} style={{ display: "flex", gap: 10, alignItems: "center", padding: "5px 4px", borderTop: "1px solid var(--line)", fontSize: 12 }}>
                      <span style={{ color: "var(--text2)", minWidth: 160 }}>{group === "day" ? "" : new Date(e.date + "T12:00").toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit" }) + " · "}{hm(e.start_time)}–{hm(e.end_time)}</span>
                      {group !== "ticket" && <b style={{ color: "var(--accent)", whiteSpace: "nowrap" }}>{e.jira_ticket || ""}</b>}
                      <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                        {[group === "ticket" ? null : group === "day" ? e.task_title || e.project_name : e.task_title, group !== "activity" ? e.activity : null, e.comment].filter(Boolean).join(" · ") || "–"}
                      </span>
                      {inPlane(e) && <span className="zp-pill synced">Plane ✓</span>}
                      <span style={{ fontVariantNumeric: "tabular-nums", color: "var(--text2)", minWidth: 56, textAlign: "right" }}>{hmin(e.duration_seconds)}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      </>}
    </div>
  );
}
