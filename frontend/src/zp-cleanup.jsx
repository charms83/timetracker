/* ═══════════════════════════════════════════════════════════════════
   Aufräumen: lokale Projekte (ohne Plane/Jira) in die richtigen Projekte
   überführen – die Zeiten bleiben immer erhalten (v6.11.0)
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback } from "react";
import { Search, Check, Wrench } from "lucide-react";
import { api, call, btn, inpS, durTxt } from "./zp-ui.jsx";

const ACTIONS = [
  ["ticket", "ist ein Ticket", "Zeiten kommen in das richtige Projekt, auf dieses Ticket"],
  ["move", "gehört zu Projekt", "Zeiten ins richtige Projekt verschieben, vorhandene Tickets bleiben"],
  ["link", "ist ein Projekt in Plane/Jira", "mit dem Plane- oder Jira-Projekt verbinden"],
  ["remove", "entfernen", "leer – wird nicht mehr gebraucht"],
  ["keep", "lokal behalten", "nur hier, nicht nach Plane/Tempo"],
];
const SYS = { plane: "Plane", jira: "Jira" };

export function useAllowLocal() {
  const [on, setOn] = useState(true);
  useEffect(() => { api("GET", "/settings").then(s => s && setOn(s.allow_local_projects === "1")); }, []);
  return on;
}

export function CleanupPanel({ onChanged }) {
  const [d, setD] = useState(null); const [msg, setMsg] = useState(null); const [since, setSince] = useState(null); const [showOld, setShowOld] = useState(false);
  const load = useCallback(() => api("GET", "/cleanup/local-projects").then(r => r && !r.error && setD(r)), []);
  useEffect(() => { load(); api("GET", "/settings").then(st => { const t = new Date(); setSince(/^\d{4}-\d{2}-\d{2}$/.test(st?.tempo_push_since || "") ? st.tempo_push_since : `${t.getFullYear()}-${String(t.getMonth() + 1).padStart(2, "0")}-01`); }); }, [load]);
  if (!d) return null;
  const done = r => { setMsg(r.error ? { err: 1, text: r.error } : { text: r.text }); load(); onChanged && onChanged(); };
  if (!d.projects.length || !since) return null;
  // Wichtig ist nur, was ab „Übertragen ab“ (Tempo) gebucht ist – ältere Projekte erst mal beiseite
  const now = d.projects.filter(p => !p.entries || (p.last || "") >= since), old = d.projects.filter(p => p.entries && (p.last || "") < since);
  const total = now.reduce((a, p) => a + p.seconds, 0);
  const sinceTxt = since.split("-").reverse().join(".");
  return (
    <div className="zp-card" style={{ padding: "14px 16px", borderLeft: "3px solid #b58100" }}>
      <div style={{ display: "flex", gap: 10, alignItems: "flex-start" }}>
        <Wrench size={18} style={{ color: "#b58100", marginTop: 2 }} />
        <div style={{ flex: 1 }}>
          <div style={{ fontWeight: 600, fontSize: 14 }}>Aufräumen: {now.length} Projekte gibt es nur hier{now.length ? ` (${durTxt(total)} gebucht)` : ""}</div>
          <div style={{ fontSize: 12, marginTop: 2 }}>Wichtig ist nur, was ab <b>{sinceTxt}</b> gebucht ist – das geht nach Tempo („Übertragen ab“). Alles davor wird später sauber aus Tempo importiert – ältere Projekte stehen unten eingeklappt.</div>
          <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 2 }}>Projekte sollte es nur geben, wenn sie in Plane oder Jira existieren. Vieles davon war eigentlich ein <b>Ticket</b> (z. B. ein Jira-Vorgang). Je Zeile wählen, was es ist – die Zeiten werden immer mitgenommen, vorher legt das System eine Sicherung an, jede Änderung steht im Änderungsprotokoll. Schon nach Tempo übertragene Zeiten erscheinen danach unter „Übertragen“ als geändert (gesperrte Monate bleiben unangetastet).</div>
        </div>
      </div>
      {msg && <div style={{ fontSize: 12, margin: "8px 0", color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</div>}
      {now.map(p => <Row key={p.id} p={p} d={d} onDone={done} />)}
      {!now.length && <div style={{ fontSize: 13, color: "var(--accent)", marginTop: 8 }}>✓ Ab {sinceTxt} ist alles sauber zugeordnet.</div>}
      {old.length > 0 && <div style={{ marginTop: 8 }}>
        <button style={{ ...btn(false), fontSize: 12 }} onClick={() => setShowOld(!showOld)}>{showOld ? "▾" : "▸"} {old.length} Projekte nur mit Buchungen vor dem {sinceTxt} – nicht nötig, die Zeit davor kommt später sauber per Import aus Tempo</button>
        {showOld && old.map(p => <Row key={p.id} p={p} d={d} onDone={done} />)}
      </div>}
      <label style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 12, color: "var(--text2)", marginTop: 10 }}>
        <input type="checkbox" checked={d.allow_local} onChange={async e => { await call("PUT", "/cleanup/allow-local", { on: e.target.checked }); load(); }} />
        Lokale Projekte erlauben (Projekte ohne Plane/Jira – z. B. wenn es für etwas keine Anbindung gibt)</label>
    </div>);
}

function Row({ p, d, onDone }) {
  const s = p.suggest || {};
  const [act, setAct] = useState(s.action || (p.entries ? "ticket" : "remove"));
  const [target, setTarget] = useState(s.target ? String(s.target) : "");
  const [ticket, setTicket] = useState(s.ticket || "");
  const [title, setTitle] = useState(s.title || p.name);
  const [src, setSrc] = useState(s.source_id ? String(s.source_id) : "");
  const [q, setQ] = useState(p.name); const [hits, setHits] = useState(null); const [busy, setBusy] = useState(false);
  const search = async () => { setBusy(true); setHits(await api("GET", `/cleanup/search?q=${encodeURIComponent(q)}`)); setBusy(false); };
  const pick = h => { setTicket(h.key); setTitle(h.title || p.name); if (h.project_id) setTarget(String(h.project_id)); setHits(null); };
  const tName = d.targets.find(t => String(t.id) === target)?.name;
  const ok = act === "remove" ? !p.entries : act === "keep" ? d.allow_local : act === "link" ? !!src : act === "move" ? !!target : !!target && /^[A-Z][A-Z0-9_]*-\d+$/i.test(ticket.trim());
  const apply = async () => {
    const what = act === "ticket" ? `${p.entries} Buchungen von „${p.name}“ nach „${tName}“ auf ${ticket.toUpperCase()} legen?`
      : act === "move" ? `${p.entries} Buchungen von „${p.name}“ nach „${tName}“ verschieben?` : act === "link" ? `„${p.name}“ mit dem gewählten Plane/Jira-Projekt verbinden?`
      : act === "remove" ? `„${p.name}“ entfernen? (keine Buchungen)` : `„${p.name}“ als lokales Projekt behalten?`;
    if (!confirm(what)) return;
    const r = await call("POST", "/cleanup/apply", { project_id: p.id, action: act, target_project_id: target ? Number(target) : null, ticket: ticket.trim().toUpperCase(), title, source_id: src ? Number(src) : null });
    onDone(r.error ? r : { text: `„${p.name}“ erledigt${r.moved?.entries ? ` – ${r.moved.entries} Buchungen übernommen` : ""}` });
  };
  return (
    <div style={{ borderTop: "1px solid var(--line)", padding: "10px 0" }}>
      <div style={{ display: "flex", gap: 10, alignItems: "baseline", flexWrap: "wrap" }}>
        <b style={{ fontSize: 14 }}>{p.name}</b>
        <span style={{ fontSize: 12, color: "var(--text2)" }}>{p.entries ? `${durTxt(p.seconds)} in ${p.entries} Buchungen · ${p.first?.split("-").reverse().join(".")} – ${p.last?.split("-").reverse().join(".")}` : "keine Buchungen"}
          {p.tasks.filter(t => t.key).length > 0 && ` · Tickets: ${p.tasks.filter(t => t.key).map(t => t.key).slice(0, 5).join(", ")}`}</span>
        {s.reason && <span style={{ fontSize: 11, color: "var(--accent)" }}>Vorschlag: {s.reason}</span>}
      </div>
      <div style={{ display: "flex", gap: 6, flexWrap: "wrap", margin: "6px 0" }}>
        {ACTIONS.filter(([k]) => k !== "remove" || !p.entries).filter(([k]) => k !== "keep" || d.allow_local).map(([k, l, h]) => (
          <button key={k} title={h} onClick={() => setAct(k)} style={{ border: `1px solid ${act === k ? "var(--accent)" : "var(--line)"}`, background: act === k ? "var(--accent-soft)" : "var(--card)", color: act === k ? "var(--accent)" : "var(--text)", borderRadius: 16, padding: "3px 11px", fontSize: 12, fontWeight: act === k ? 600 : 400, cursor: "pointer" }}>{l}</button>))}
      </div>
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center" }}>
        {act === "ticket" && <>
          <div style={{ display: "flex", gap: 4 }}>
            <input style={{ ...inpS, width: 190 }} value={q} onChange={e => setQ(e.target.value)} onKeyDown={e => e.key === "Enter" && search()} placeholder="Ticket suchen" />
            <button style={btn(false)} onClick={search} disabled={busy}><Search size={13} /> {busy ? "…" : "Suchen"}</button>
          </div>
          <input style={{ ...inpS, width: 120, fontWeight: 600 }} value={ticket} onChange={e => setTicket(e.target.value.toUpperCase())} placeholder="z. B. IU-123" />
        </>}
        {(act === "ticket" || act === "move") && (
          <select style={{ ...inpS, width: 240 }} value={target} onChange={e => setTarget(e.target.value)}>
            <option value="">– Projekt wählen –</option>
            {d.targets.map(t => <option key={t.id} value={t.id}>{t.name} ({t.sources.map(x => `${SYS[x.system]} ${x.key}`).join(", ")})</option>)}
          </select>)}
        {act === "link" && (
          <select style={{ ...inpS, width: 300 }} value={src} onChange={e => setSrc(e.target.value)}>
            <option value="">– Plane-/Jira-Projekt wählen –</option>
            {d.sources.map(x => <option key={x.id} value={x.id}>{SYS[x.system]} {x.key} – {x.name}{x.status === "ignored" ? " (stumm)" : ""}</option>)}
          </select>)}
        <button style={btn(true)} disabled={!ok} onClick={apply}><Check size={13} /> Ausführen</button>
      </div>
      {hits && <div style={{ marginTop: 6, fontSize: 12 }}>
        {!hits.length && <div style={{ color: "var(--text2)" }}>Nichts gefunden – Key direkt eintragen.</div>}
        {hits.map((h, i) => h.error ? <div key={i} style={{ color: "var(--stop)" }}>{h.error}</div> : (
          <div key={h.key} onClick={() => pick(h)} style={{ display: "flex", gap: 8, padding: "3px 6px", borderRadius: 6, cursor: "pointer" }} onMouseEnter={e => e.currentTarget.style.background = "var(--accent-soft)"} onMouseLeave={e => e.currentTarget.style.background = "transparent"}>
            <b style={{ minWidth: 80 }}>{h.key}</b><span style={{ flex: 1 }}>{h.title}</span><span style={{ color: "var(--text2)" }}>{h.project}{h.status ? ` · ${h.status}` : ""} · {h.from}</span>
            {!h.project_id && <span style={{ color: "#b58100" }}>Projekt noch nicht übernommen</span>}</div>))}
      </div>}
    </div>);
}
