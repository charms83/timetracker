/* ═══════════════════════════════════════════════════════════════════
   Projekte und Quellen: neue Plane-/Jira-Projekte übernehmen,
   Regel (wohin die Zeit geht), Nachfolge-Projekt, Abschluss
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback } from "react";
import { RefreshCw, Check, X, Link2, ArrowRight, Archive, RotateCcw, BellOff, Bell, Merge } from "lucide-react";
import { api, call, btn, durTxt } from "./zp-ui.jsx";

const RULES = [["default", "Standard"], ["bridge", "Plane + Tempo"], ["plane", "nur Plane"], ["tempo", "nur Tempo"], ["none", "nur hier (Abrechnung)"]];
const RULE_TXT = Object.fromEntries(RULES);
const SYS = { plane: ["Plane", "#3f76ff"], jira: ["Jira", "#0052cc"] };
const Src = ({ s }) => { const [n, c] = SYS[s.system] || [s.system, "#888"]; return (
  <span title={`${n}: ${s.name}${s.gone ? " (archiviert/verschwunden)" : ""}`} style={{ display: "inline-flex", gap: 4, alignItems: "center", fontSize: 11, fontWeight: 600, padding: "1px 7px", borderRadius: 10,
    border: `1px solid color-mix(in srgb, ${c} 40%, transparent)`, color: c, background: `color-mix(in srgb, ${c} 8%, var(--card))`, textDecoration: s.gone ? "line-through" : "none" }}>{n} {s.key || s.name}</span>); };
const ago = iso => iso ? new Date(iso).toLocaleDateString("de-DE", { day: "2-digit", month: "2-digit", year: "numeric" }) : "noch nie";

export function ProjectSourcesPanel({ onChanged }) {
  const [d, setD] = useState(null); const [busy, setBusy] = useState(""); const [msg, setMsg] = useState(null); const [showAll, setShowAll] = useState(false); const [showMuted, setShowMuted] = useState(false);
  const load = useCallback(() => api("GET", "/sources").then(setD), []);
  useEffect(() => { load(); }, [load]);
  const done = r => { if (r?.error) setMsg({ err: 1, text: r.error }); load(); onChanged && onChanged(); };
  const scan = async () => { setBusy("scan"); const r = await call("POST", "/sources/scan"); setBusy(""); setMsg(r.error ? { err: 1, text: r.error } : { text: `Eingelesen: Plane ${r.plane ?? "–"}, Jira ${r.jira ?? "–"} Projekte · ${r.new} neu zu prüfen${r.errors?.length ? " · " + r.errors.join("; ") : ""}` }); load(); };
  if (!d) return null;
  const active = d.projects.filter(p => !p.closed && !p.muted), closed = d.projects.filter(p => p.closed);
  // abgeschlossene Projekte erscheinen nur mit „abgeschlossene zeigen“ – dort wird der Nachfolger festgelegt
  const list = d.projects.filter(p => (showAll || !p.closed) && (showMuted || !p.muted || p.closed && showAll));
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 12, marginBottom: 16 }}>
      {/* Neue Projekte */}
      <div className="zp-card" style={{ padding: "14px 16px" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <div style={{ flex: 1 }}>
            <div style={{ fontWeight: 600, fontSize: 14 }}>Neue Projekte gefunden {d.pending.length > 0 && <span style={{ background: "#C4271B", color: "#fff", borderRadius: 10, padding: "0 7px", fontSize: 11, marginLeft: 4 }}>{d.pending.length}</span>}</div>
            <div style={{ fontSize: 12, color: "var(--text2)" }}>Projekte entstehen in Plane bzw. Jira. Einmal im Monat liest Zeitprotokoll sie ein – du entscheidest, was übernommen wird. Zuletzt: {ago(d.last_scan)}</div>
          </div>
          <button style={btn(false)} disabled={busy === "scan"} onClick={scan}><RefreshCw size={13} /> {busy === "scan" ? "Lese ein…" : "Jetzt einlesen"}</button>
        </div>
        {msg && <div style={{ fontSize: 12, marginTop: 6, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.text}</div>}
        {d.pending.length === 0 ? <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 8 }}>Nichts offen ✓</div> : <>
          {d.pending.map(s => <Pending key={s.id} s={s} projects={active} onDone={done} />)}
          {d.pending.length > 1 && <div style={{ display: "flex", justifyContent: "flex-end", paddingTop: 8, borderTop: "1px solid var(--line)" }}>
            <button style={btn(false)} title="Du hast Zugriff auf viele Projekte, nutzt aber erst ein paar – die übrigen bleiben stumm, bis du sie wieder aufnimmst"
              onClick={async () => { if (confirm(`Alle ${d.pending.length} offenen Projekte stumm schalten? Du kannst jedes später unter „Ignoriert“ wieder aufnehmen.`)) done(await call("POST", "/sources/ignore-all")); }}><BellOff size={13} /> Alle übrigen stumm schalten</button></div>}
        </>}
        {d.ignored.length > 0 && <div style={{ fontSize: 11, color: "var(--text2)", marginTop: 8 }}>Stumm ({d.ignored.length}): {d.ignored.map(s => (
          <span key={s.id} style={{ marginRight: 8 }}>{SYS[s.system]?.[0]} {s.key || s.name} <a href="#" onClick={async e => { e.preventDefault(); done(await call("POST", `/sources/${s.id}/ignore`, { undo: true })); }}>wieder aufnehmen</a></span>))}</div>}
      </div>

      {/* Vermutlich doppelt angelegte Projekte */}
      {(d.duplicates || []).length > 0 && (
        <div className="zp-card" style={{ padding: "12px 16px", borderLeft: "3px solid #b58100" }}>
          <div style={{ fontWeight: 600, fontSize: 14 }}>Sieht doppelt aus</div>
          <div style={{ fontSize: 12, color: "var(--text2)", marginBottom: 6 }}>Diese Projekte meinen vermutlich dasselbe. Zusammenführen: Buchungen, Aufgaben und Quellen landen in einem Projekt, das andere bleibt als Verweis (im Änderungsprotokoll nachvollziehbar).</div>
          {d.duplicates.map(x => <DupRow key={x.from.id + "-" + x.into.id} x={x} onDone={done} />)}
        </div>)}

      {/* In Plane/Jira abgeschlossen → hier entscheiden (nur dann wird ein Nachfolger gebraucht) */}
      {d.projects.filter(p => p.all_gone && !p.closed && !p.gone_ack).map(p => <GoneCard key={p.id} p={p} onDone={done} />)}

      {/* Projekte mit Quellen, Regel */}
      <div className="zp-card" style={{ padding: "14px 16px" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 6 }}>
          <div style={{ flex: 1 }}>
            <div style={{ fontWeight: 600, fontSize: 14 }}>Projekte, Quellen und Regeln</div>
            <div style={{ fontSize: 12, color: "var(--text2)" }}>Je Projekt: aus welchen Systemen die Tickets kommen und wohin die Zeit geht. Standard für neue Projekte:
              <select value={d.default_rule} onChange={async e => done(await call("PUT", "/sources/default-rule", { rule: e.target.value }))} style={{ marginLeft: 6, padding: "1px 4px", fontSize: 12 }}>
                {RULES.slice(1).map(([k, l]) => <option key={k} value={k}>{l}</option>)}</select></div>
          </div>
          <label style={{ fontSize: 12, color: "var(--text2)", display: "flex", gap: 5, alignItems: "center", marginRight: 10 }}><input type="checkbox" checked={showMuted} onChange={e => setShowMuted(e.target.checked)} /> stumme zeigen ({d.projects.filter(p => p.muted && !p.closed).length})</label>
          {closed.length > 0 && <label style={{ fontSize: 12, color: "var(--text2)", display: "flex", gap: 5, alignItems: "center" }}><input type="checkbox" checked={showAll} onChange={e => setShowAll(e.target.checked)} /> abgeschlossene zeigen ({closed.length})</label>}
        </div>
        <div style={{ display: "grid", gridTemplateColumns: "minmax(180px,1.6fr) minmax(140px,1fr) 170px 100px", gap: "0 10px", fontSize: 11, color: "var(--text2)", padding: "6px 0", borderBottom: "1px solid var(--line)" }}>
          <span>Projekt</span><span>Quellen</span><span>Zeit geht nach</span><span></span></div>
        {list.map(p => <ProjectRow key={p.id} p={p} all={d.projects} defaultRule={d.default_rule} onDone={done} />)}
      </div>
    </div>);
}

function DupRow({ x, onDone }) {
  const [flip, setFlip] = useState(false);
  const from = flip ? x.into : x.from, into = flip ? x.from : x.into;
  // Beide haben eine Quelle im selben System (z. B. Jira IU und Jira ISMS): welche gehört zum Projekt?
  const [keep, setKeep] = useState({});
  const conf = (x.conflict || []).map(sys => ({ sys, a: from.sources.find(s => s.system === sys), b: into.sources.find(s => s.system === sys) }));
  const info = p => `${p.sources.map(s => `${SYS[s.system]?.[0]} ${s.key || ""}`).join(", ") || "ohne Quelle"} · ${p.entries ? `${durTxt(p.seconds)} gebucht` : "keine Buchungen"}`;
  return (
    <div style={{ display: "flex", gap: 10, alignItems: "center", padding: "8px 0", borderTop: "1px solid var(--line)", flexWrap: "wrap" }}>
      <div style={{ flex: "1 1 340px", fontSize: 13 }}>
        <b>{from.name}</b> <span style={{ color: "var(--text2)", fontSize: 12 }}>({info(from)})</span>
        <span style={{ margin: "0 6px", color: "var(--text2)" }}>→</span>
        <b>{into.name}</b> <span style={{ color: "var(--text2)", fontSize: 12 }}>({info(into)})</span>
        <div style={{ fontSize: 11, color: "var(--text2)" }}>{x.reason} · {x.score} %</div>
        {conf.map(c => (
          <div key={c.sys} style={{ fontSize: 12, marginTop: 4, color: "#8a6200" }}>Beide haben eine {SYS[c.sys]?.[0]}-Quelle – welche gehört dazu?{" "}
            {[["b", c.b], ["a", c.a]].map(([k, src]) => src && (
              <label key={k} style={{ marginLeft: 8, cursor: "pointer" }}><input type="radio" name={`k${x.from.id}-${c.sys}`} checked={(keep[c.sys] || "b") === k} onChange={() => setKeep({ ...keep, [c.sys]: k })} /> {SYS[c.sys]?.[0]} {src.key}</label>))}
            <span style={{ color: "var(--text2)" }}> (die andere kommt wieder unter „Neue Projekte“)</span></div>))}
      </div>
      <button style={btn(true)} onClick={async () => {
        if (!confirm(`„${from.name}“ in „${into.name}“ zusammenführen?\n\n${from.entries ? `${from.entries} Buchungen wandern mit. ` : ""}Quellen (${from.sources.map(s => s.key).join(", ") || "keine"}) hängen danach an „${into.name}“.`)) return;
        onDone(await call("POST", `/projects/${from.id}/merge`, { into: into.id, keep_from: conf.filter(c => keep[c.sys] === "a").map(c => c.sys) })); }}><Merge size={13} /> Zusammenführen</button>
      <button style={btn(false)} title="Richtung tauschen" onClick={() => setFlip(!flip)}>⇄</button>
      <button style={btn(false)} onClick={async () => onDone(await call("POST", "/sources/duplicates/ignore", { a: x.from.id, b: x.into.id }))}>Sind verschieden</button>
    </div>);
}

/* Für den Assistenten: nur die Teile, die gerade etwas zu tun haben */
export function DuplicatesPanel({ onChanged }) {
  const [d, setD] = useState(null);
  const load = useCallback(() => api("GET", "/sources").then(setD), []);
  useEffect(() => { load(); }, [load]);
  if (!d) return null;
  const done = () => { load(); onChanged && onChanged(); };
  if (!(d.duplicates || []).length) return <div style={{ fontSize: 13, color: "var(--accent)" }}>✓ Keine doppelten Projekte</div>;
  return <div>{d.duplicates.map(x => <DupRow key={x.from.id + "-" + x.into.id} x={x} onDone={done} />)}</div>;
}
export function PendingPanel({ onChanged }) {
  const [d, setD] = useState(null);
  const load = useCallback(() => api("GET", "/sources").then(setD), []);
  useEffect(() => { load(); }, [load]);
  if (!d) return null;
  const done = r => { if (r?.error) alert(r.error); load(); onChanged && onChanged(); };
  const active = d.projects.filter(p => !p.closed && !p.muted);
  if (!d.pending.length) return <div style={{ fontSize: 13, color: "var(--accent)" }}>✓ Nichts offen</div>;
  return (<div>
    {d.pending.map(s => <Pending key={s.id} s={s} projects={active} onDone={done} />)}
    {d.pending.length > 1 && <div style={{ display: "flex", justifyContent: "flex-end", paddingTop: 8 }}>
      <button style={btn(false)} onClick={async () => { if (confirm(`Alle ${d.pending.length} offenen Projekte stumm schalten?`)) done(await call("POST", "/sources/ignore-all")); }}><BellOff size={13} /> Alle übrigen stumm schalten</button></div>}
  </div>);
}

function GoneCard({ p, onDone }) {
  const set = async b => onDone(await call("PUT", `/projects/${p.id}/setup`, b));
  return (
    <div className="zp-card" style={{ padding: "12px 16px", borderLeft: "3px solid var(--warn)" }}>
      <div style={{ fontWeight: 600 }}>„{p.name}“ ist in {p.sources.map(s => SYS[s.system]?.[0]).join(" und ")} abgeschlossen</div>
      <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 2 }}>Hier auch abschließen? Die Zeiten bleiben erhalten. Ein Nachfolge-Projekt kannst du danach festlegen, sobald es bekannt ist.</div>
      <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap", marginTop: 8 }}>
        <button style={btn(true)} onClick={() => set({ closed: true })}><Archive size={13} /> Abschließen</button>
        <button style={btn(false)} onClick={() => set({ gone_ack: true })}>Weiter nutzen</button>
      </div>
    </div>);
}

function Pending({ s, projects, onDone }) {
  const best = s.suggestions?.[0];
  const [mode, setMode] = useState(best ? String(best.project_id) : "new");
  const [name, setName] = useState(s.name || s.key || "");
  const [sys, color] = SYS[s.system] || [s.system, "#888"];
  return (
    <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap", padding: "8px 0", borderTop: "1px solid var(--line)" }}>
      <span style={{ fontSize: 10, fontWeight: 700, background: color, color: "#fff", borderRadius: 4, padding: "2px 6px" }}>{sys} {s.key}</span>
      <div style={{ flex: 1, minWidth: 160 }}><div style={{ fontWeight: 600 }}>{s.name}</div>
        {best && <div style={{ fontSize: 11, color: "var(--text2)" }}>Vorschlag: zu „{best.name}“ ({best.reason})</div>}</div>
      <select value={mode} onChange={e => setMode(e.target.value)} style={{ padding: "5px 7px", minWidth: 200 }}>
        <option value="new">als neues Projekt übernehmen</option>
        {s.suggestions?.length > 0 && <optgroup label="Vorschläge">{s.suggestions.map(x => <option key={"s" + x.project_id} value={x.project_id}>zu „{x.name}“ ({x.score} %)</option>)}</optgroup>}
        <optgroup label="Zu bestehendem Projekt">{projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</optgroup>
      </select>
      {mode === "new" && <input value={name} onChange={e => setName(e.target.value)} placeholder="Projektname" style={{ padding: "5px 7px", width: 180 }} />}
      <button style={btn(true)} onClick={async () => onDone(await call("POST", `/sources/${s.id}/link`, mode === "new" ? { create: { name } } : { project_id: Number(mode) }))}><Check size={13} /> Übernehmen</button>
      <button style={btn(false)} title="Nicht übernehmen (z. B. fremdes Team)" onClick={async () => onDone(await call("POST", `/sources/${s.id}/ignore`))}><X size={13} /></button>
    </div>);
}

function ProjectRow({ p, all, defaultRule, onDone }) {
  const set = async b => onDone(await call("PUT", `/projects/${p.id}/setup`, b));
  return (
    <div style={{ display: "grid", gridTemplateColumns: "minmax(180px,1.6fr) minmax(140px,1fr) 170px 100px", gap: "0 10px", alignItems: "center", padding: "7px 0", borderBottom: "1px solid var(--line)", opacity: p.closed ? .6 : 1 }}>
      <div style={{ minWidth: 0 }}>
        <div style={{ fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{p.name}{p.closed && <span style={{ fontSize: 10, marginLeft: 6, color: "var(--text2)" }}>{p.merged_into ? "zusammengeführt" : "abgeschlossen"}</span>}{p.muted && !p.closed && <span style={{ fontSize: 10, marginLeft: 6, color: "var(--text2)" }}>stumm</span>}</div>
        <div style={{ fontSize: 11, color: "var(--text2)" }}>{p.entries ? `${durTxt(p.seconds)} gebucht · zuletzt ${new Date(p.last + "T12:00").toLocaleDateString("de-DE")}` : "noch keine Buchungen"}</div>
        {p.closed && !p.merged_into && <div style={{ fontSize: 11, color: "var(--text2)", marginTop: 3, display: "flex", gap: 6, alignItems: "center" }}>Neue Buchungen gehen auf:
          <select value={p.successor_id || ""} onChange={e => set({ successor_id: e.target.value || null })} style={{ padding: "1px 4px", fontSize: 11 }}>
            <option value="">– nicht mehr buchbar –</option>{all.filter(x => x.id !== p.id && !x.closed).map(x => <option key={x.id} value={x.id}>{x.name}</option>)}</select></div>}
      </div>
      <div style={{ display: "flex", gap: 4, flexWrap: "wrap" }}>{p.sources.length ? p.sources.map(s => <Src key={s.id} s={s} />) : <span style={{ fontSize: 11, color: "var(--text2)" }}>nur hier</span>}</div>
      <div>
        <select value={p.output_rule} onChange={e => set({ output_rule: e.target.value })} style={{ padding: "4px 6px", fontSize: 12, width: "100%" }}>
          {RULES.map(([k, l]) => <option key={k} value={k}>{k === "default" ? `Standard (${RULE_TXT[defaultRule]})` : l}</option>)}</select>
        <div style={{ fontSize: 10.5, color: "var(--text2)", marginTop: 2 }} title={!p.in_plane ? "Projekt ist (noch) nicht in Plane – Zeiten gehen nicht nach Plane, bis es dort angelegt und verknüpft ist" : ""}>
          tatsächlich: {[p.effective?.plane && "Plane", p.effective?.tempo && "Tempo"].filter(Boolean).join(" + ") || "nur hier"}{!p.in_plane && ["bridge", "plane"].includes(p.output_rule === "default" ? defaultRule : p.output_rule) ? " · noch nicht in Plane" : ""}</div>
      </div>
      <div style={{ textAlign: "right", display: "flex", gap: 2, justifyContent: "flex-end" }}>
        {!p.closed && <button className="zp-nav" aria-label={p.muted ? "Wieder nutzen" : "Stumm schalten"} title={p.muted ? "Wieder nutzen (in Auswahllisten zeigen)" : "Stumm schalten – nicht in Auswahllisten, kein Abgleich"} onClick={() => set({ muted: !p.muted })}>{p.muted ? <Bell size={14} /> : <BellOff size={14} />}</button>}
        {!p.closed && <button className="zp-nav" aria-label="Zusammenführen" title="In ein anderes Projekt zusammenführen" onClick={async () => {
          const opts = all.filter(x => x.id !== p.id && !x.closed);
          const name = prompt(`„${p.name}“ zusammenführen in welches Projekt?\n\n${opts.map((x, i) => `${i + 1}  ${x.name}`).join("\n")}\n\nNummer eingeben:`);
          const target = opts[Number(name) - 1]; if (!target) return;
          if (!confirm(`„${p.name}“ in „${target.name}“ zusammenführen?\n\nAlle Buchungen, Aufgaben und Quellen wandern zu „${target.name}“. „${p.name}“ bleibt als abgeschlossener Verweis erhalten. Jede verschobene Buchung steht im Änderungsprotokoll.`)) return;
          onDone(await call("POST", `/projects/${p.id}/merge`, { into: target.id })); }}><Merge size={14} /></button>}
        {p.closed
          ? <button className="zp-nav" title="Wieder öffnen" onClick={() => set({ closed: false })}><RotateCcw size={14} /></button>
          : <button className="zp-nav" title="Abschließen (Zeiten bleiben erhalten)" onClick={() => { if (confirm(`„${p.name}“ abschließen? Die Zeiten bleiben erhalten. Ein Nachfolge-Projekt kannst du danach festlegen, sobald es bekannt ist.`)) set({ closed: true }); }}><Archive size={14} /></button>}
      </div>
    </div>);
}
