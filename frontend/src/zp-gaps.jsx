/* ═══════════════════════════════════════════════════════════════════
   Nachtragen (v6.26.0, vorher „Lücken füllen“)
   Pro Tag eine klare Zeile: am Mac von–bis · gebucht · noch einzutragen.
   Darunter „+ Eintragen“ (von–bis vorbelegt) und Vorschläge aus Terminen,
   SSH-Sitzungen, Zeit am Mac und den letzten Buchungen.
   Steht das Konto im Plus und deckt das Fehlende, ist es nur ein Hinweis.
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback } from "react";
import { Plus, Check, ChevronRight, X, CalendarClock, Pencil, Trash2 } from "lucide-react";
import { api, call, btn, TicketInput, deleteEntry, dupIds, isServerMode } from "./zp-ui.jsx";
import { Terminal, Monitor, Sparkles } from "lucide-react";
import { DayPanel } from "./zp-day.jsx";
import { NewEntry } from "./zp-calendar.jsx";

const fh = h => (Math.round(h * 4) / 4).toLocaleString("de-DE") + " h";
const dd = s => new Date(s + "T12:00:00").toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit" });

function Stat({ label, value, color, sub }) {
  return <div style={{ minWidth: 0 }}><div style={{ fontSize: 11, color: "var(--text2)" }}>{label}</div>
    <div style={{ fontSize: 16, fontWeight: 700, color: color || "var(--text)", fontVariantNumeric: "tabular-nums" }}>{value}</div>
    {sub && <div style={{ fontSize: 11, color: "var(--text2)" }}>{sub}</div>}</div>;
}

export function GapFiller({ tick, onChanged, initialDate }) {
  const [st, setSt] = useState(null); const [projects, setProjects] = useState([]);
  const [sel, setSel] = useState(initialDate || null); const [add, setAdd] = useState(null); const [inner, setInner] = useState(0); const [older, setOlder] = useState(false);
  const load = useCallback(() => api("GET", "/month-end").then(r => r && !r.error && setSt(r)), []);
  useEffect(() => { load(); }, [load, tick]);
  useEffect(() => { api("GET", "/projects").then(p => setProjects((p || []).filter(x => x.status !== "archived"))); }, []);
  if (!st) return null;
  const days = st.prev ? st.prev.days : st.open_days;
  const cur = days.find(d => d.date === sel) || days[days.length - 1];
  const changed = () => { setInner(i => i + 1); load(); onChanged && onChanged(); };
  const next = () => { const i = days.findIndex(d => d.date === cur.date); setSel((days[i - 1] || days[days.length - 1])?.date || null); };
  const recent = days.slice(-3), rest = days.slice(0, -3), showAll = older || (rest.some(d => d.date === cur?.date));
  const shown = showAll ? days : recent;
  const chip = (d, small) => { const on = d.date === cur.date, big = d.missing_h >= 4 && !plus; return (
    <button key={d.date} onClick={() => setSel(d.date)} style={{ fontSize: 12, padding: "4px 10px", borderRadius: 8, cursor: "pointer", textAlign: "left",
      border: `1px solid ${on ? "var(--accent)" : "var(--line)"}`, background: on ? "var(--accent-soft)" : "var(--card)", color: "var(--text)" }}>
      <b>{dd(d.date)}</b>{!small && <span style={{ color: big ? "#b58100" : "var(--text2)" }}> fehlt {fh(d.missing_h)}</span>}</button>); };
  const ok = async () => { await api("POST", "/month-end/ok", { date: cur.date }); next(); changed(); };
  const toMinS = t => t ? toMin(t) : 9 * 60;
  const plus = st.optional && !st.prev;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      {(st.remind || st.prev) && (
        <div style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 12.5, padding: "8px 10px", borderRadius: 8, background: "color-mix(in srgb, #b58100 9%, transparent)" }}>
          <CalendarClock size={15} style={{ color: "#b58100", flex: "none" }} />
          {st.prev ? <span><b>{st.prev.month_name}</b> ist vorbei – bitte die fehlenden Tage nachtragen, danach wird der Monat in Tempo gesperrt.</span>
            : <span>Noch <b>{st.workdays_left} Arbeitstag{st.workdays_left === 1 ? "" : "e"}</b> bis Monatsende ({dd(st.last_workday)}). Wochenenden zählen nicht mit.</span>}
        </div>)}
      {plus && <div style={{ fontSize: 12.5, padding: "8px 10px", borderRadius: 8, background: "var(--accent-soft)" }}>
        Dein Konto steht bei <b>+{fh(st.balance_h)}</b> – das deckt die fehlenden {fh(st.missing_h)}. Nachtragen ist hier nur ein Hinweis, kein Muss.</div>}
      {!days.length ? <div style={{ fontSize: 14, color: "var(--accent)", fontWeight: 600 }}><Check size={14} style={{ verticalAlign: -2 }} /> Alles eingetragen – gut gemacht! 🎉</div> : <>
        <div style={{ fontSize: 12, color: "var(--text2)" }}>Noch {days.length} Tag{days.length === 1 ? "" : "e"} offen</div>
        {/* Tage: die letzten 3, Rest hinter „Ältere Tage“ */}
        <div style={{ display: "flex", gap: 6, flexWrap: "wrap", alignItems: "center" }}>
          {shown.map(d => chip(d, showAll && rest.includes(d)))}
          {rest.length > 0 && <button onClick={() => setOlder(o => !o)} style={{ fontSize: 12, padding: "4px 8px", border: "none", background: "transparent", color: "var(--text2)", cursor: "pointer", textDecoration: "underline" }}>{showAll ? "Weniger anzeigen" : `Ältere Tage (${rest.length})`}</button>}
        </div>
        {/* Der Tag in einer Zeile */}
        <div className="zp-card" style={{ padding: "10px 12px" }}>
          <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8 }}>{new Date(cur.date + "T12:00").toLocaleDateString("de-DE", { weekday: "long", day: "2-digit", month: "long" })}</div>
          <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(120px, 1fr))", gap: 10 }}>
            <Stat
              label={cur.mac_h != null ? "Am Mac" : (isServerMode() ? "Fenster" : "Arbeitstag")}
              value={cur.mac_from ? `${cur.mac_from}–${cur.mac_to}` : "–"}
              sub={cur.mac_h != null ? `${fh(cur.mac_h)} anwesend`
                : cur.mac_from ? (isServerMode() ? "aus vorhandenen Buchungen" : "aus Buchungen (keine Mac-Daten)")
                : (isServerMode() ? "kein Signal – einfach eintragen" : "keine Mac-Daten")} />
            <Stat label="Gebucht" value={fh(cur.booked_h)} sub={`Soll ${fh(cur.target_h)}`} />
            <Stat label="Noch einzutragen" value={fh(cur.missing_h)} color={plus ? "var(--text)" : "#b58100"} sub={cur.open_h ? `davon ${fh(cur.open_h)} ohne Buchung` : null} />
          </div>
          <div style={{ height: 6, borderRadius: 3, background: "var(--line)", marginTop: 10, overflow: "hidden" }}>
            <div style={{ height: "100%", width: `${Math.min(100, cur.booked_h / (cur.target_h || 1) * 100)}%`, background: "var(--accent)" }} /></div>
          <div style={{ display: "flex", gap: 6, flexWrap: "wrap", marginTop: 10 }}>
            <button style={btn(true)} onClick={() => setAdd({ s: toMinS(cur.mac_from), e: toMinS(cur.mac_from) + Math.round(cur.missing_h * 4) * 15 })}><Plus size={14} /> Eintragen</button>
            <button style={btn(false)} onClick={ok} title="Tag wird nicht mehr gemeldet (z. B. Gleitzeit, früher gegangen)"><Check size={14} /> Passt so</button>
            {days.length > 1 && <button style={btn(false)} onClick={next}>Anderer Tag <ChevronRight size={14} /></button>}
          </div>
        </div>
        <Suggestions key={"s" + cur.date} date={cur.date} tick={inner} projects={projects} onChanged={changed} />
        <details><summary style={{ fontSize: 12, color: "var(--text2)", cursor: "pointer" }}>Zeitleiste des Tages</summary>
        <div style={{ maxWidth: 520, marginTop: 6 }}><DayPanel key={cur.date} initialDate={cur.date} projects={projects} tick={inner} onChanged={changed} /></div></details>
      </>}
      {add && cur && (
        <div style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.25)", zIndex: 200, display: "flex", alignItems: "flex-start", justifyContent: "center", paddingTop: 90 }} onMouseDown={e => e.target === e.currentTarget && setAdd(null)}>
          <div className="zp-card" style={{ width: "min(440px, calc(100vw - 32px))", padding: "14px 16px" }}>
            <div style={{ display: "flex", alignItems: "center" }}><b style={{ flex: 1 }}>Zeit eintragen – {dd(cur.date)}</b><button className="zp-nav" onClick={() => setAdd(null)}><X size={14} /></button></div>
            <NewEntry date={cur.date} startMin={add.s} endMin={Math.min(24 * 60 - 1, add.e)} projects={projects} onDone={() => { setAdd(null); changed(); }} />
          </div>
        </div>)}
    </div>);
}

/* Vorschläge: offene Termine, SSH, Zeit am Mac ohne Buchung, Restzeit – je mit den wahrscheinlichsten Tickets */
const hhmm = m => `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;
const toMin = t => { const [h, m] = t.split(":").map(Number); return h * 60 + m; };
const ICON = { event: CalendarClock, ssh: Terminal, gap: Monitor, rest: Plus, pattern: Sparkles };
function Suggestions({ date, tick, projects, onChanged }) {
  const [list, setList] = useState(null); const [msg, setMsg] = useState(null);
  const [entries, setEntries] = useState(null);
  const load = useCallback(() => {
    api("GET", `/day-overview?date=${date}`).then(r => setEntries(Array.isArray(r?.entries) ? r.entries : []));
    return api("GET", `/gap-suggest?date=${date}`).then(r => setList(Array.isArray(r?.items) ? r.items.map(i => ({ ...i, pick: i.candidates[0] ? 0 : -1, from: hhmm(i.s), to: hhmm(i.e), text: i.comment || i.candidates[0]?.last_comment || "" })) : []));
  }, [date]);
  useEffect(() => { load(); const h = () => load(); window.addEventListener("zp:entry-changed", h); return () => window.removeEventListener("zp:entry-changed", h); }, [load, tick]);
  if (!list || !entries) return <div style={{ fontSize: 12, color: "var(--text2)" }}>Lade den Tag …</div>;
  const upd = (i, p) => setList(l => l.map((x, j) => j === i ? { ...x, ...p } : x));
  // Selbst gesuchtes Ticket (Plane oder Jira) → Aufgabe im richtigen Projekt anlegen/finden und als Wahl setzen
  const pickTicket = async (i, tk) => {
    setMsg(null);
    let c = null;
    if (tk?.created && tk.task_id) c = { task_id: tk.task_id, project_id: tk.local_project_id, jira_ticket: tk.key, label: `${tk.key} ${tk.name || ""}`.trim(), why: ["selbst gewählt"] };
    else {
      const key = String(tk?.key || "").trim().toUpperCase();
      if (!/^[A-Z][A-Z0-9_]*-\d+$/.test(key)) { setMsg("Bitte ein Ticket aus der Liste wählen oder die Ticketnummer eingeben (z. B. INTERNAL-4430)"); return; }
      const r = await call("POST", "/ticket/ensure", { key, title: tk.name || "" });
      if (r.error) { setMsg(r.error); return; }
      c = { task_id: r.task_id, project_id: r.project_id, jira_ticket: key, label: `${key} ${r.title && r.title.toUpperCase() !== key ? r.title : ""}`.trim(), project_name: r.project_name, why: ["selbst gewählt"] };
    }
    setList(l => l.map((x, j) => { if (j !== i) return x; const cands = [c, ...x.candidates.filter(y => !(y.task_id === c.task_id && y.project_id === c.project_id))]; return { ...x, candidates: cands, pick: 0, search: false, q: "" }; }));
  };
  const book = async it => {
    const c = it.candidates[it.pick]; if (!c) return false;
    const s = new Date(`${date}T${it.from}:00`), e = new Date(`${date}T${it.to}:00`); if (!(e > s)) { setMsg("Ende muss nach dem Beginn liegen"); return false; }
    const r = await call("POST", "/time-entries", { project_id: c.project_id, task_id: c.task_id, start_time: s.toISOString(), end_time: e.toISOString(), duration_seconds: Math.round((e - s) / 1000), comment: it.text || null, date });
    if (r?.error) { setMsg(r.error); return false; }
    // Termin/SSH als erledigt verknüpfen und die Wahl merken – so lernt der Assistent
    api("POST", "/gap-suggest/learn", { kind: it.kind, ref: it.ref || null, key: it.key || null, entry_id: r.id, task_id: c.task_id, project_id: c.project_id });
    return true;
  };
  const one = async it => { setMsg(null); if (await book(it)) { load(); onChanged && onChanged(); } };
  const ready = list.filter(i => i.pick >= 0);
  const allBook = async () => { setMsg(null); let n = 0; for (const it of ready) if (await book(it)) n++; setMsg(`${n} gebucht`); load(); onChanged && onChanged(); };
  // v6.35: eine Tagesliste wie unter „Übertragen“ – Buchungen und Vorschläge in zeitlicher Reihenfolge
  const dup = dupIds(entries);
  const rows = [...entries.map(e => ({ entry: e, _s: e.s })), ...list.map((it, i) => ({ it, i, _s: toMin(it.from) }))].sort((a, b) => a._s - b._s);
  const SYS = { jira: "#2f6fd6", plane: "#7c5cd6" };
  const editor = (it, i, c) => (
    <div style={{ display: "grid", gap: 5, padding: "6px 0 2px" }}>
      <div style={{ display: "flex", gap: 6, alignItems: "center", fontSize: 12.5, flexWrap: "wrap" }}>
        <input type="time" value={it.from} onChange={e => upd(i, { from: e.target.value })} style={{ padding: "2px 4px", fontSize: 12 }} />–<input type="time" value={it.to} onChange={e => upd(i, { to: e.target.value })} style={{ padding: "2px 4px", fontSize: 12 }} />
        <span style={{ color: "var(--text2)" }}>{toMin(it.to) - toMin(it.from)} min</span>
      </div>
      <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
        {it.search ? (
          <div style={{ flex: "1 1 260px", display: "flex", gap: 4, alignItems: "center" }}>
            <div className="zp-fill" style={{ flex: 1 }}><TicketInput value={it.q || ""} jira onChange={v => upd(i, { q: v })} onPick={tk => pickTicket(i, tk)} onEnter={() => pickTicket(i, { key: it.q })} hint="Ticket suchen, z. B. INTERNAL-4430 oder Stichwort" /></div>
            <button className="zp-nav" title="Zurück zu den Vorschlägen" onClick={() => upd(i, { search: false })}><X size={14} /></button>
          </div>
        ) : (
        <select value={it.pick} onChange={e => { const p = Number(e.target.value); if (p === -2) return upd(i, { search: true, q: "" }); upd(i, { pick: p, text: it.text || (it.candidates[p]?.last_comment || "") }); }} style={{ flex: "1 1 220px", padding: "5px 7px", fontSize: 12.5 }}>
          {it.candidates.length ? null : <option value={-1}>kein Vorschlag – Ticket suchen</option>}
          {it.candidates.map((c, k) => <option key={k} value={k}>{c.label}{c.jira_ticket ? "" : " (Projekt)"}</option>)}
          <option value={-2}>🔍 Anderes Ticket suchen …</option>
        </select>)}
        <input value={it.text} placeholder="Was hast du gemacht?" onChange={e => upd(i, { text: e.target.value })} onKeyDown={e => e.key === "Enter" && one(it)} style={{ flex: "2 1 220px", padding: "5px 7px", fontSize: 12.5 }} />
        <button style={{ ...btn(true), padding: "5px 12px" }} disabled={!c} onClick={() => one(it)}><Check size={13} /> Buchen</button>
      </div>
      {c?.why?.length > 0 && <div style={{ fontSize: 11, color: "var(--text2)" }}>Warum: {c.why.join(" · ")}</div>}
    </div>);
  return (
    <div className="zp-card" style={{ padding: 0, overflow: "hidden" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "9px 14px", borderBottom: "1px solid var(--line)" }}>
        <b style={{ fontSize: 13, flex: 1 }}>Der Tag</b>
        <span style={{ fontSize: 11.5, color: "var(--text2)" }}>{entries.length} gebucht · {list.length} Vorschl{list.length === 1 ? "ag" : "äge"}</span>
        {ready.length > 1 && <button style={{ ...btn(true), padding: "4px 12px", fontSize: 12 }} onClick={allBook} title="Alle Vorschläge mit ihrem ersten Ticket buchen">Alle {ready.length} übernehmen</button>}
      </div>
      {!rows.length && <div style={{ padding: "10px 14px", fontSize: 12.5, color: "var(--text2)" }}>Noch nichts gebucht und keine Vorschläge – „Eintragen“ oder „Passt so“.</div>}
      {rows.map(r => {
        if (r.entry) { const e = r.entry; const sys = e.plane ? "plane" : e.jira_ticket ? "jira" : null; const mirror = ["tempo", "tempo_csv"].includes(e.source); return (
          <div key={"e" + e.id} style={{ display: "flex", alignItems: "center", gap: 10, padding: "7px 14px", borderTop: "1px solid var(--line)", fontSize: 13 }}>
            <span style={{ color: "var(--text2)", fontSize: 12, width: 88, flex: "none", fontVariantNumeric: "tabular-nums" }}>{hhmm(e.s)}–{hhmm(e.e)}</span>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ display: "flex", gap: 6, alignItems: "baseline", minWidth: 0 }}>
                {e.jira_ticket ? <span style={{ flex: "none", fontSize: 11, fontWeight: 700, padding: "1px 6px", borderRadius: 5, color: "#fff", background: SYS[sys] || "var(--text3)" }}>{e.jira_ticket}</span>
                  : <span style={{ flex: "none", fontSize: 11, padding: "1px 6px", borderRadius: 5, border: "1px dashed var(--line)", color: "var(--text2)" }}>ohne Ticket</span>}
                <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontWeight: 600 }}>{e.task_title && e.task_title.toUpperCase() !== String(e.jira_ticket || "").toUpperCase() ? e.task_title : (e.comment || e.project_name)}</span>
              </div>
              <div style={{ fontSize: 11.5, color: "var(--text2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{e.comment && e.comment !== e.task_title ? e.comment : e.project_name}</div>
            </div>
            <span style={{ color: "var(--text2)", fontSize: 12, whiteSpace: "nowrap" }}>{(m => m >= 60 ? `${Math.floor(m / 60)} h ${String(m % 60).padStart(2, "0")} min` : `${m} min`)(e.e - e.s)}</span>
            {dup.has(e.id) && <span className="zp-pill changed" title="Gleiche Aufgabe zur gleichen Zeit – vermutlich doppelt">doppelt?</span>}
            {mirror ? <span className="zp-pill" title="Aus Tempo übernommen – dort ändern">aus Tempo</span> : <>
              <button className="zp-nav" title="Bearbeiten" onClick={() => window.dispatchEvent(new CustomEvent("zp:edit-entry", { detail: { id: e.id, date } }))}><Pencil size={14} /></button>
              <button className="zp-nav" title="Löschen" onClick={async () => { if (await deleteEntry(e)) { load(); onChanged && onChanged(); } }}><Trash2 size={14} /></button></>}
          </div>); }
        const { it, i } = r; const I = ICON[it.kind] || Plus; const c = it.candidates[it.pick];
        return (
          <div key={it.kind + it.s} style={{ padding: "6px 14px", borderTop: "1px dashed var(--line)", background: "color-mix(in srgb, var(--accent) 4%, transparent)", fontSize: 12.5 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
              <span style={{ color: "var(--text2)", fontSize: 12, width: 88, flex: "none", fontVariantNumeric: "tabular-nums" }}>{it.from}–{it.to}</span>
              <I size={13} style={{ color: "var(--text2)", flex: "none" }} />
              <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: "var(--text2)" }} title={c?.why?.join(" · ")}>
                {it.title}{c ? <> · Vorschlag: <b style={{ color: "var(--text)" }}>{c.label}</b></> : " · kein Vorschlag"}</span>
              {c && !it.open && <button style={{ ...btn(true), padding: "2px 10px", fontSize: 12 }} onClick={() => one(it)}>übernehmen</button>}
              <button style={{ ...btn(false), padding: "2px 10px", fontSize: 12 }} onClick={() => upd(i, { open: !it.open })}>{it.open ? "zu" : c ? "anpassen" : "eintragen"}</button>
            </div>
            {it.open && editor(it, i, c)}
          </div>); })}
      {msg && <div style={{ fontSize: 12, color: "var(--stop)" }}>{msg}</div>}
    </div>);
}
