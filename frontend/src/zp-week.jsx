/* ═══════════════════════════════════════════════════════════════════
   Wochenraster (wie Tempo-Timesheet)
   Zeilen = Work-Items / Projekte, Spalten = Mo–So. Stunden direkt eintippen.
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback, useMemo, useRef } from "react";
import { ChevronLeft, ChevronRight, Plus, Copy, X, MoreHorizontal } from "lucide-react";
import { api, localDate, TicketInput, projectFromTicket, resolveTask, useActivities, ImpreciseMark } from "./zp-ui.jsx";

const DOW = ["Mo", "Di", "Mi", "Do", "Fr", "Sa", "So"];
const isoWeek = d => { const x = new Date(d); x.setHours(12); x.setDate(x.getDate() + 3 - ((x.getDay() + 6) % 7)); const f = new Date(x.getFullYear(), 0, 4, 12); return 1 + Math.round(((x - f) / 864e5 - 3 + ((f.getDay() + 6) % 7)) / 7); };
const monday = d => { const x = new Date(d); x.setHours(12, 0, 0, 0); x.setDate(x.getDate() - ((x.getDay() + 6) % 7)); return x; };
const addD = (d, n) => { const x = new Date(d); x.setDate(x.getDate() + n); return x; };

/* „1,5“ „1.5“ „1:30“ „90m“ „2h“ „1h30“ → Sekunden; leer → 0; ungültig → null */
export function parseHours(v) {
  v = String(v || "").trim().toLowerCase().replace(/\s+/g, "");
  if (!v) return 0;
  let m;
  if ((m = /^(\d{1,2}):(\d{1,2})$/.exec(v))) return (+m[1] * 60 + +m[2]) * 60;
  if ((m = /^(\d+)m(in)?$/.exec(v))) return +m[1] * 60;
  if ((m = /^(\d{1,2})h(\d{1,2})?(m(in)?)?$/.exec(v))) return (+m[1] * 60 + +(m[2] || 0)) * 60;
  if ((m = /^(\d+(?:[.,]\d+)?)h?$/.exec(v))) return Math.round(parseFloat(m[1].replace(",", ".")) * 3600);
  return null;
}
/* Anzeige wie Tempo: Stunden:Minuten (0:31, 2:00) */
const hm = s => { const m = Math.round(Math.abs(s || 0) / 60); return `${Math.floor(m / 60)}:${String(m % 60).padStart(2, "0")}`; };
const fmtCell = s => s ? hm(s) : "";
const fmtH = hm;
const clock = iso => { const d = new Date(iso); return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`; };

const SYS = { jira: "#2f6fd6", plane: "#7c5cd6", none: "var(--text3)" };
const sysOf = r => r.system === "jira" ? "jira" : r.system === "plane" ? "plane" : "none";
const SYS_LABEL = { jira: "Jira", plane: "Plane" };
const MINUS = "\u2212";

function Cell({ value, locked, plain, onCommit, rowIdx, col, items, onDetail }) {
  const [txt, setTxt] = useState(fmtCell(value));
  const [bad, setBad] = useState(false);
  const [saving, setSaving] = useState(false);
  useEffect(() => { setTxt(fmtCell(value)); setBad(false); }, [value]);
  if (plain) return <div className="zp-wc"><span className="zp-wplain">{fmtCell(value)}</span></div>;
  const commit = async () => {
    const s = parseHours(txt);
    if (s === null) { setBad(true); return; }
    if (Math.abs(s - (value || 0)) < 30) { setTxt(fmtCell(value)); return; }
    setSaving(true);
    const ok = await onCommit(s);
    setSaving(false);
    if (!ok) setTxt(fmtCell(value));
  };
  return (
    <div className="zp-wc">
      <input data-cell={`${rowIdx}:${col}`} value={txt} placeholder="+" className={"zp-wcell" + (value ? " has" : "") + (bad ? " bad" : "") + (saving ? " saving" : "")}
        onChange={e => { setTxt(e.target.value); setBad(false); }}
        onBlur={commit} onFocus={e => e.target.select()}
        onKeyDown={e => {
          const go = (r, c) => { const el = document.querySelector(`[data-cell="${r}:${c}"]`); if (el) { e.preventDefault(); el.focus(); } };
          if (e.key === "Enter" || e.key === "ArrowDown") go(rowIdx + 1, col);
          else if (e.key === "ArrowUp") go(rowIdx - 1, col);
          else if (e.key === "Tab") return;
          else if (e.key === "Escape") { setTxt(fmtCell(value)); e.target.blur(); }
        }}
        title={locked ? "Enthält Buchungen aus Tempo/Plane – diese lassen sich hier nicht verringern" : "Stunden, z.B. 1:30 · 1,5 · 90m"} />
      {items?.length > 0 && <button className="zp-wdet" title="Einzelne Buchungen ansehen" onMouseDown={e => e.preventDefault()} onClick={onDetail}>{items.length}</button>}
    </div>
  );
}

const WEEK_CSS = `
.zp-wtab{width:100%;border-collapse:collapse;table-layout:fixed;min-width:700px}
.zp-wtab th,.zp-wtab td{border-bottom:1px solid var(--line)}
.zp-wtab tbody tr:last-child td,.zp-wtab tfoot td{border-bottom:none}
.zp-wc{position:relative;display:flex;justify-content:center}
.zp-wcell{width:100%;max-width:84px;height:34px;text-align:center;padding:4px 6px;font-variant-numeric:tabular-nums;border-radius:7px;border:1px solid transparent;background:transparent;font-size:13.5px;font-weight:400;color:var(--text);cursor:text;outline:none}
.zp-wcell.has{font-weight:600}
.zp-wcell::placeholder{color:transparent;font-weight:400}
.zp-wrow:hover .zp-wcell::placeholder{color:color-mix(in srgb,var(--text3) 55%,transparent)}
.zp-wcell:hover{background:color-mix(in srgb,var(--text3) 10%,transparent)}
.zp-wcell:hover::placeholder{color:var(--text2)}
.zp-wcell:focus{background:var(--card);border-color:var(--accent);box-shadow:0 0 0 3px var(--accent-soft)}
.zp-wcell:focus::placeholder{color:transparent}
.zp-wcell.bad{border-color:var(--stop)}
.zp-wcell.saving{background:var(--accent-soft)}
.zp-wplain{height:34px;line-height:34px;font-size:13.5px;font-variant-numeric:tabular-nums;font-weight:600;color:var(--text2)}
.zp-wdet{position:absolute;top:1px;right:calc(50% - 40px);min-width:15px;height:15px;border-radius:8px;border:none;font-size:9.5px;font-weight:700;padding:0 4px;background:color-mix(in srgb,var(--text3) 22%,var(--card));color:var(--text2);cursor:pointer;opacity:0;transition:opacity .1s}
.zp-wc:hover .zp-wdet{opacity:1}
.zp-wrow:hover td{background:color-mix(in srgb,var(--text3) 4%,transparent)}
.zp-wrow.abs{opacity:.55}
.zp-wrow.abs:hover td{background:transparent}
.zp-wbadge{display:inline-block;font-size:10px;font-weight:600;line-height:1;padding:2px 5px;border-radius:4px;margin-right:6px;vertical-align:1px}
.zp-wbar{height:4px;border-radius:2px;background:color-mix(in srgb,var(--text3) 20%,transparent);overflow:hidden}
.zp-wbar>i{display:block;height:100%;border-radius:2px;background:var(--accent)}
.zp-wmenu{position:absolute;right:0;top:34px;z-index:50;min-width:250px;padding:10px;display:flex;flex-direction:column;gap:8px;box-shadow:0 8px 28px rgba(0,0,0,.22)}
.zp-wmenu button.item{display:flex;gap:8px;align-items:center;padding:8px 10px;border-radius:7px;border:none;background:transparent;font-size:12.5px;color:var(--text);text-align:left}
.zp-wmenu button.item:hover{background:var(--line)}
.zp-wleg{display:inline-flex;gap:12px;font-size:11px;color:var(--text2)}
.zp-wleg i{display:inline-block;width:8px;height:8px;border-radius:50%;margin-right:5px}
`;

export function WeekGrid() {
  // Sprung aus „fehlende Stunden“: #woche/2026-10-14
  const [mon, setMon] = useState(() => { const d = (typeof location !== "undefined" ? location.hash.split("/")[1] : "") || ""; return /^\d{4}-\d{2}-\d{2}$/.test(d) ? monday(new Date(d + "T12:00")) : monday(new Date()); });
  useEffect(() => { const h = () => { const d = location.hash.split("/")[1] || ""; if (/^\d{4}-\d{2}-\d{2}$/.test(d)) setMon(monday(new Date(d + "T12:00"))); }; window.addEventListener("hashchange", h); return () => window.removeEventListener("hashchange", h); }, []);
  const days = useMemo(() => [...Array(7)].map((_, i) => localDate(addD(mon, i))), [mon]);
  const from = days[0], to = days[6], today = localDate(new Date());
  const [grid, setGrid] = useState(null);
  const [sheet, setSheet] = useState(null);
  const [extra, setExtra] = useState([]);           // manuell hinzugefügte (noch leere) Zeilen
  const [msg, setMsg] = useState(null);
  const [detail, setDetail] = useState(null);   // {row, date}
  const [adding, setAdding] = useState(false);
  const [ticket, setTicket] = useState("");
  const [picked, setPicked] = useState(null);
  const [addPid, setAddPid] = useState("");
  const [projects, setProjects] = useState([]);
  const [activity, setActivity] = useState(() => { try { return localStorage.getItem("zp.grid.activity") || ""; } catch (_) { return ""; } });
  const activities = useActivities();
  const loadSeq = useRef(0);
  const [menu, setMenu] = useState(false);
  const menuRef = useRef(null);
  useEffect(() => { if (!menu) return; const h = e => { if (menuRef.current && !menuRef.current.contains(e.target)) setMenu(false); }; document.addEventListener("mousedown", h); return () => document.removeEventListener("mousedown", h); }, [menu]);

  const load = useCallback(async () => {
    const my = ++loadSeq.current;
    const [g, t] = await Promise.all([api("GET", `/grid?from=${from}&to=${to}`), api("GET", `/timesheet?from=${from}&to=${to}`)]);
    if (my !== loadSeq.current) return;
    if (!g || g.error) { setMsg({ err: true, text: g?.error || "Server nicht erreichbar" }); return; }
    setGrid(g); setSheet(t && !t.error ? t : null);
  }, [from, to]);
  useEffect(() => { setGrid(null); setExtra([]); load(); }, [load]);
  useEffect(() => { api("GET", "/projects").then(p => setProjects((Array.isArray(p) ? p : []).filter(x => x.status !== "archived" && x.status !== "done"))); }, []);
  useEffect(() => { try { localStorage.setItem("zp.grid.activity", activity); } catch (_) {} }, [activity]);

  const rows = useMemo(() => {
    const r = [...(grid?.rows || [])];
    for (const x of extra) if (!r.some(y => y.key === x.key)) r.push(x);
    const grp = x => x.absence ? 3 : x.system === "jira" ? 0 : x.system === "plane" ? 1 : 2;
    const k = x => (x.ticket || x.title || "").toLowerCase();
    return r.sort((a, b) => grp(a) - grp(b) || k(a).localeCompare(k(b), "de", { numeric: true }));
  }, [grid, extra]);

  const setCell = async (row, date, seconds) => {
    const r = await api("PUT", "/grid/cell", { date, project_id: row.project_id, task_id: row.task_id, seconds, activity: activity || null });
    if (!r || r.error) { setMsg({ err: true, text: r?.error || "Speichern fehlgeschlagen" }); return false; }
    setMsg(null); await load(); return true;
  };

  const dayTarget = d => (sheet?.days || []).find(x => x.date === d);
  const real = r => !r.absence;
  const colSum = d => rows.reduce((s, r) => s + (real(r) ? (r.cells?.[d] || 0) : 0), 0);
  const weekSum = days.reduce((s, d) => s + colSum(d), 0);
  const dayInfo = d => { const t = dayTarget(d); const target = Math.max(0, (t?.target_h || 0) * 3600 - (t?.absence ? (t.absence_h || 0) * 3600 : 0));
    return { t, target, label: t?.absence ? (t.absence.type === "feiertag" ? "Feiertag" : t.absence.type === "urlaub" ? "Urlaub" : t.absence.type === "krankheit" ? "Krank" : t.absence.type) : "" }; };
  const weekNeed = days.reduce((s, d) => s + dayInfo(d).target, 0);
  const upto = days.filter(d => d <= today);
  const toDateNeed = upto.reduce((s, d) => s + dayInfo(d).target, 0);
  const toDateSum = upto.reduce((s, d) => s + colSum(d), 0);
  // Sa/So nur anzeigen, wenn dort etwas steht (oder heute)
  const vis = days.filter((d, i) => i < 5 || d === today || rows.some(r => (r.cells?.[d] || 0) > 0));
  const nCols = vis.length + 2;

  const addRow = async () => {
    const pid = +addPid || picked?.local_project_id || projectFromTicket(projects, ticket)?.id;
    if (!pid) { setMsg({ err: true, text: "Bitte ein Projekt wählen" }); return; }
    const tid = picked?.task_id || (ticket.trim() ? await resolveTask(pid, ticket.trim(), picked?.name) : null);
    const p = projects.find(x => x.id === pid);
    const t = picked?.name || ticket.trim() || null;
    const key = `${pid}|${tid || 0}`;
    setExtra(x => [...x, { key, project_id: pid, task_id: tid, ticket: /^[A-Za-z][A-Za-z0-9_]*-\d+$/.test(ticket.trim()) ? ticket.trim().toUpperCase() : null,
      title: t, project_name: p?.name || "", system: picked?.system || null, cells: {}, total: 0 }]);
    setTicket(""); setPicked(null); setAdding(false);
    setTimeout(() => { const tr = [...document.querySelectorAll("tr[data-rowkey]")].find(e => e.dataset.rowkey === key); const inputs = tr ? [...tr.querySelectorAll("input[data-cell]")] : []; (inputs.find(i => i.dataset.cell.endsWith(":" + Math.max(0, vis.indexOf(today)))) || inputs[0])?.focus(); }, 50);
  };
  const copyLastWeek = async () => {
    setMenu(false);
    const g = await api("GET", `/grid?from=${localDate(addD(mon, -7))}&to=${localDate(addD(mon, -1))}`);
    const prev = (g?.rows || []).filter(r => !r.absence).map(r => ({ ...r, cells: {}, locked: {}, total: 0 }));
    setExtra(x => [...x, ...prev.filter(p => !x.some(y => y.key === p.key))]);
    setMsg({ text: prev.length ? `${prev.length} Zeilen aus der Vorwoche übernommen – jetzt Stunden eintragen` : "Vorwoche hat keine Buchungen" });
  };

  const isCurWeek = localDate(mon) === localDate(monday(new Date()));
  const pct = weekNeed ? Math.min(100, weekSum / weekNeed * 100) : weekSum ? 100 : 0;
  const lineStyle = { whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" };
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <style>{WEEK_CSS}</style>
      {/* Kopf */}
      <div className="zp-card" style={{ padding: "14px 18px", display: "flex", alignItems: "flex-start", gap: 14, flexWrap: "wrap" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
          <button className="zp-nav" onClick={() => setMon(addD(mon, -7))} title="Vorwoche"><ChevronLeft size={17} /></button>
          <div style={{ minWidth: 150 }}>
            <div style={{ fontWeight: 700, fontSize: 17, lineHeight: 1.2 }}>KW {isoWeek(mon)}</div>
            <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 2 }}>{addD(mon, 0).toLocaleDateString("de-DE", { day: "2-digit", month: "2-digit" })}–{addD(mon, 6).toLocaleDateString("de-DE", { day: "2-digit", month: "2-digit", year: "numeric" })}</div>
          </div>
          <button className="zp-nav" onClick={() => setMon(addD(mon, 7))} title="Nächste Woche"><ChevronRight size={17} /></button>
          {!isCurWeek && <button onClick={() => setMon(monday(new Date()))} style={{ background: "var(--accent-soft)", border: "none", borderRadius: 7, padding: "5px 10px", color: "var(--accent)", fontSize: 12, fontWeight: 600, marginLeft: 4 }}>Heute</button>}
        </div>
        <div style={{ flex: 1, alignSelf: "center", minWidth: 0 }}>
          <span className="zp-wleg">
            <span><i style={{ background: SYS.jira }} />Jira</span>
            <span><i style={{ background: SYS.plane }} />Plane</span>
            <span><i style={{ background: SYS.none }} />ohne Ticket</span>
          </span>
        </div>
        <div style={{ display: "flex", alignItems: "flex-start", gap: 8 }}>
          <div style={{ minWidth: 190, textAlign: "right" }}>
            <div style={{ fontVariantNumeric: "tabular-nums", lineHeight: 1.1 }}>
              <span style={{ fontSize: 26, fontWeight: 700 }}>{hm(weekSum)}</span>
              <span style={{ fontSize: 15, color: "var(--text2)", marginLeft: 6 }}>/ {hm(weekNeed)}</span>
            </div>
            <div className="zp-wbar" style={{ marginTop: 8 }}><i style={{ width: `${pct}%` }} /></div>
            {toDateNeed > toDateSum + 600 && <div style={{ fontSize: 11.5, color: "var(--warn)", marginTop: 6 }}>bis heute fehlen {hm(toDateNeed - toDateSum)} · <a href="#assistent/nachtragen" style={{ color: "inherit", fontWeight: 600 }}>Nachtragen ›</a></div>}
          </div>
          <div ref={menuRef} style={{ position: "relative" }}>
            <button className="zp-nav" onClick={() => setMenu(m => !m)} title="Mehr" aria-label="Mehr"><MoreHorizontal size={18} /></button>
            {menu && (
              <div className="zp-card zp-wmenu">
                <label style={{ fontSize: 11, color: "var(--text2)", display: "flex", flexDirection: "column", gap: 4 }}>Tätigkeit für neue Zeiten
                  <select value={activity} onChange={e => setActivity(e.target.value)} style={{ padding: "6px 8px", fontSize: 12.5 }}>
                    <option value="">keine</option>
                    {activities.map(a => <option key={a} value={a}>{a}</option>)}
                  </select>
                </label>
                <button className="item" onClick={copyLastWeek}><Copy size={14} /> Zeilen der Vorwoche übernehmen</button>
              </div>)}
          </div>
        </div>
      </div>
      {msg && <div style={{ fontSize: 12, color: msg.err ? "var(--stop)" : "var(--accent)", padding: "0 4px" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</div>}

      <div className="zp-card" style={{ overflowX: "auto", padding: 0 }}>
        <table className="zp-wtab">
          <colgroup><col style={{ width: 260 }} />{vis.map(d => <col key={d} />)}<col style={{ width: 84 }} /></colgroup>
          <thead>
            <tr>
              <th style={{ textAlign: "left", padding: "10px 16px", fontSize: 11, fontWeight: 600, color: "var(--text3)" }}>Ticket / Projekt</th>
              {vis.map(d => { const { label } = dayInfo(d); const isT = d === today; return (
                <th key={d} style={{ padding: "9px 4px 7px", textAlign: "center", background: isT ? "color-mix(in srgb, var(--accent) 7%, transparent)" : undefined }}>
                  <div style={{ fontSize: 12.5, fontWeight: 650, whiteSpace: "nowrap", color: isT ? "var(--accent)" : "var(--text)" }}>{DOW[days.indexOf(d)]} {new Date(d + "T12:00").getDate()}.</div>
                  <div style={{ fontSize: 10, fontWeight: 500, color: isT ? "var(--accent)" : "var(--text3)", height: 13, marginTop: 1 }}>{isT ? ["heute", label].filter(Boolean).join(" · ") : label}</div>
                </th>); })}
              <th style={{ padding: "8px 16px 8px 4px", textAlign: "right", fontSize: 11, fontWeight: 600, color: "var(--text3)" }}>Summe</th>
            </tr>
          </thead>
          <tbody>
            {grid === null && <tr><td colSpan={nCols} style={{ padding: 24, textAlign: "center", color: "var(--text3)" }}>Lade…</td></tr>}
            {grid && !rows.length && <tr><td colSpan={nCols} style={{ padding: 24, textAlign: "center", color: "var(--text3)" }}>Noch keine Buchungen in dieser Woche – unten ein Ticket hinzufügen oder „Zeilen der Vorwoche“ übernehmen.</td></tr>}
            {rows.map((r, ri) => {
              const total = Object.values(r.cells || {}).reduce((a, b) => a + b, 0);
              const isExtra = !grid?.rows?.some(y => y.key === r.key);
              const sk = sysOf(r), col = SYS[sk];
              return (
                <tr key={r.key} data-rowkey={r.key} className={"zp-wrow" + (r.absence ? " abs" : "")}>
                  <td style={{ padding: "7px 8px 7px 16px", boxShadow: `inset 3px 0 0 ${col}` }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                      <div style={{ flex: 1, minWidth: 0 }} title={`${r.ticket || ""} ${r.title || ""}\n${r.project_name || ""}`}>
                        <div style={{ ...lineStyle, fontSize: 13 }}>
                          {SYS_LABEL[sk] && <span className="zp-wbadge" style={{ color: col, background: `color-mix(in srgb, ${col} 13%, transparent)` }}>{SYS_LABEL[sk]}</span>}
                          {r.ticket && <b style={{ marginRight: 5 }}>{r.ticket}</b>}<span>{r.title && r.title !== r.ticket ? r.title : r.ticket ? "" : "Ohne Ticket"}</span></div>
                        <div style={{ ...lineStyle, fontSize: 11, color: "var(--text3)", marginTop: 1 }}>{r.project_name || "kein Projekt"}{r.absence ? " · Abwesenheit · zählt nicht" : ""}</div>
                      </div>
                      {isExtra && <button className="zp-nav" title="Leere Zeile entfernen" onClick={() => setExtra(x => x.filter(y => y.key !== r.key))}><X size={13} /></button>}
                    </div>
                  </td>
                  {vis.map((d, ci) => (
                    <td key={d} style={{ padding: "4px 4px", background: d === today ? "color-mix(in srgb, var(--accent) 5%, transparent)" : undefined }}>
                      <Cell value={r.cells?.[d] || 0} locked={r.locked?.[d]} plain={!!r.absence} rowIdx={ri} col={ci} items={r.items?.[d]}
                        onDetail={() => setDetail({ row: r, date: d })}
                        onCommit={s => r.project_id ? setCell(r, d, s) : (setMsg({ err: true, text: "Diese Zeile hat kein Projekt" }), false)} />
                    </td>
                  ))}
                  <td style={{ padding: "6px 16px 6px 4px", textAlign: "right", fontWeight: 600, fontVariantNumeric: "tabular-nums", color: r.absence ? "var(--text2)" : "var(--text)" }}>{total ? hm(total) : ""}</td>
                </tr>
              );
            })}
            <tr>
              <td colSpan={nCols} style={{ padding: "8px 16px" }}>
                {!adding ? (
                  <button onClick={() => setAdding(true)} style={{ display: "inline-flex", gap: 6, alignItems: "center", padding: "6px 12px", borderRadius: 7, border: "1px dashed var(--line)", background: "transparent", color: "var(--accent)", fontWeight: 600, fontSize: 12 }}><Plus size={14} /> Ticket hinzufügen</button>
                ) : (
                  <div style={{ display: "grid", gridTemplateColumns: "2fr 1fr auto auto", gap: 8, alignItems: "center", maxWidth: 820 }}>
                    <TicketInput value={ticket} localTasks={[]} projectId={addPid}
                      onChange={v => { setTicket(v); setPicked(null); const p = projectFromTicket(projects, v); if (p) setAddPid(String(p.id)); }}
                      onPick={it => { setPicked(it); if (it.local_project_id) setAddPid(String(it.local_project_id)); else { const p = projectFromTicket(projects, it.key); if (p) setAddPid(String(p.id)); } }}
                      onEnter={addRow} />
                    <select value={addPid} onChange={e => setAddPid(e.target.value)} style={{ padding: "7px 9px" }}>
                      <option value="">Projekt wählen …</option>
                      {projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
                    </select>
                    <button onClick={addRow} className="zp-btn-start" style={{ height: 34 }}>Hinzufügen</button>
                    <button className="zp-nav" onClick={() => setAdding(false)}><X size={15} /></button>
                  </div>
                )}
              </td>
            </tr>
          </tbody>
          <tfoot>
            <tr style={{ background: "color-mix(in srgb, var(--text3) 6%, transparent)", borderTop: "1px solid var(--line)" }}>
              <td style={{ padding: "10px 16px", fontWeight: 650, fontSize: 13, borderTop: "1px solid var(--line)" }}>Gebucht</td>
              {vis.map(d => {
                const sum = colSum(d), { target } = dayInfo(d), isT = d === today, future = d > today;
                const diff = sum - target;
                let sub = null;
                if (!future && (target > 0 || sum > 0)) {
                  if (Math.abs(diff) <= 60) sub = <span style={{ color: "var(--ok)" }}>✓</span>;
                  else if (diff < 0) sub = <span style={{ color: "var(--warn)" }}>{isT ? `noch ${hm(-diff)}` : `${MINUS}${hm(-diff)}`}</span>;
                  else sub = <span style={{ color: "var(--ok)" }}>+{hm(diff)}</span>;
                }
                return (
                  <td key={d} style={{ padding: "8px 4px", textAlign: "center", fontVariantNumeric: "tabular-nums", borderTop: "1px solid var(--line)", background: isT ? "color-mix(in srgb, var(--accent) 5%, transparent)" : undefined }}>
                    <div style={{ fontSize: 13.5, fontWeight: 650 }}>{sum ? hm(sum) : future ? "" : "0:00"}</div>
                    <div style={{ fontSize: 11, height: 15, fontWeight: 500 }}>{sub}</div>
                  </td>);
              })}
              <td style={{ padding: "8px 16px 8px 4px", textAlign: "right", fontVariantNumeric: "tabular-nums", borderTop: "1px solid var(--line)" }}>
                <div style={{ fontSize: 14, fontWeight: 700 }}>{hm(weekSum)}</div>
                {weekNeed > 0 && <div style={{ fontSize: 10.5, color: "var(--text2)", height: 15 }}>von {hm(weekNeed)}</div>}
              </td>
            </tr>
          </tfoot>
        </table>
      </div>
      <div style={{ fontSize: 11, color: "var(--text3)", padding: "0 4px" }}>
        Klicken und tippen: 1:30 · 1,5 · 90m · 2h · Enter/↓ nächste Zeile · Zahl anklicken zeigt die einzelnen Buchungen
      </div>
      {detail && (() => { const it = detail.row.items?.[detail.date] || []; return (
        <div style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.25)", zIndex: 200, display: "flex", alignItems: "flex-start", justifyContent: "center", paddingTop: 90 }} onMouseDown={e => e.target === e.currentTarget && setDetail(null)}>
          <div className="zp-card" style={{ width: "min(480px, calc(100vw - 32px))", padding: "14px 16px" }}>
            <div style={{ display: "flex", alignItems: "flex-start", gap: 8 }}>
              <div style={{ flex: 1 }}><b>{detail.row.ticket ? detail.row.ticket + " " : ""}{detail.row.title || detail.row.project_name}</b>
                <div style={{ fontSize: 12, color: "var(--text2)" }}>{new Date(detail.date + "T12:00").toLocaleDateString("de-DE", { weekday: "long", day: "2-digit", month: "2-digit" })} · {hm(detail.row.cells?.[detail.date])}</div></div>
              <button className="zp-nav" onClick={() => setDetail(null)}><X size={14} /></button>
            </div>
            {it.map(e => (
              <div key={e.id} style={{ display: "flex", gap: 10, borderTop: "1px solid var(--line)", padding: "7px 0", fontSize: 12.5 }}>
                <span style={{ fontVariantNumeric: "tabular-nums", color: "var(--text2)", whiteSpace: "nowrap" }}>{clock(e.start_time)}–{clock(e.end_time)}<ImpreciseMark e={e} /></span>
                <span style={{ fontWeight: 600, fontVariantNumeric: "tabular-nums" }}>{hm(e.duration_seconds)}</span>
                <span style={{ flex: 1, color: e.comment ? "var(--text)" : "var(--text3)" }}>{e.comment || "ohne Text"}{e.account ? <span style={{ color: "var(--text2)" }}> · {e.account}</span> : null}</span>
                <span style={{ fontSize: 11, color: e.tempo ? "var(--accent)" : "var(--text3)", whiteSpace: "nowrap" }}>{e.tempo ? "✓ Tempo" : e.source === "tempo" || e.source === "plane" ? "importiert" : "noch nicht übertragen"}</span>
              </div>))}
            <div style={{ fontSize: 11, color: "var(--text2)", marginTop: 6 }}>Uhrzeiten und Texte änderst du im <a href={`#kalender`} onClick={() => setDetail(null)}>Kalender</a>.</div>
          </div>
        </div>); })()}
    </div>
  );
}
