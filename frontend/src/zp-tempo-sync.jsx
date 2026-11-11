/* ═══════════════════════════════════════════════════════════════════
   Tempo abgleichen (v6.17.0) – alle eigenen Tempo-Zeiten ins System,
   nur lesen, nichts wird gebucht. Erst Vorschau, dann übernehmen.
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback } from "react";
import { RefreshCw, Check, ShieldCheck } from "lucide-react";
import { api, call, btn, isServerMode } from "./zp-ui.jsx";

const fh = h => (Math.round((h || 0) * 10) / 10).toLocaleString("de-DE") + " h";
const dt = s => s ? new Date(s.length > 10 ? s : s + "T12:00").toLocaleDateString("de-DE", { day: "2-digit", month: "2-digit", year: "numeric" }) : "–";

export function TempoSyncCard({ onChanged }) {
  const sm = isServerMode();
  const [st, setSt] = useState(null); const [from, setFrom] = useState("");
  const [prev, setPrev] = useState(null); const [busy, setBusy] = useState(false); const [res, setRes] = useState(null); const [err, setErr] = useState(null);
  const [replace, setReplace] = useState(true);
  const load = useCallback(() => api("GET", "/tempo-sync").then(r => { if (r && !r.error) { setSt(r); setFrom(f => f || r.from || r.tracking_start || `${new Date().getFullYear() - 1}-01-01`); } }), []);
  useEffect(() => { load(); }, [load]);
  // Fortschritt anzeigen, solange der Server abgleicht (lange Läufe)
  useEffect(() => {
    if (!busy && !st?.running) return;
    const t = setInterval(() => load(), 1500);
    return () => clearInterval(t);
  }, [busy, st?.running, load]);
  if (!st) return null;
  if (!st.connected) return <div style={{ fontSize: 13, color: "var(--text2)" }}>Erst Jira & Tempo verbinden (Einstellungen › Verbindungen › Tempo).</div>;
  const run = async dry => {
    setBusy(true); setErr(null); if (!dry) setRes(null);
    const r = await call("POST", "/tempo-sync", { from, dry, replace_old: replace });
    setBusy(false);
    if (r?.error) return setErr(r.error);
    if (dry) setPrev(r); else { setRes(r); setPrev(null); load(); onChanged && onChanged(); }
  };
  const runProg = st.running && st.running.total ? ` · ${st.running.done || 0}/${st.running.total} Monate` : "";
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10, fontSize: 13 }}>
      <div style={{ color: "var(--text2)", fontSize: 12.5 }}>
        Holt <b>alle deine eigenen</b> Zeiten aus Tempo ins System – damit Arbeitszeitkonto, Auswertung und „Nachtragen“ stimmen. <b>Es wird nichts gebucht</b>, weder nach Tempo noch nach Plane. Was das System selbst nach Tempo übertragen hat, wird erkannt und nicht doppelt geholt.
        {sm && <> Ersatz für den früheren Tempo-CSV-Import: Abgleich läuft über die API, ohne Datei.</>}
      </div>
      {st.entries > 0 && <div style={{ fontSize: 12.5 }}><Check size={13} style={{ color: "var(--accent)", verticalAlign: -2 }} /> {st.entries} Tempo-Buchungen ({fh(st.hours)}) von {dt(st.first)} bis {dt(st.latest)} · zuletzt abgeglichen {dt(st.last)} · danach automatisch alle 12 Stunden</div>}
      <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
        <label style={{ fontSize: 12.5 }}>Ab wann (z. B. Eintrittsdatum): <input type="date" value={from} onChange={e => { setFrom(e.target.value); setPrev(null); }} style={{ padding: "4px 6px" }} /></label>
        <button style={btn(!prev)} disabled={busy || !from} onClick={() => run(true)}><RefreshCw size={13} className={busy ? "zp-spin" : ""} /> {busy && !prev ? `Lese Tempo …${runProg}` : "Vorschau"}</button>
        {(busy || st.running) && <span style={{ fontSize: 12, color: "var(--text2)" }}>Abgleich läuft{runProg} – du kannst warten, nichts geht verloren.</span>}
      </div>
      {prev && (
        <div className="zp-card" style={{ padding: "10px 12px" }}>
          <b>Vorschau {dt(prev.from)} – {dt(prev.to)}</b>
          <div style={{ marginTop: 4 }}>{prev.new} neue Tempo-Buchungen · zusammen {fh(prev.hours)}{prev.own_skipped ? ` · ${prev.own_skipped} hat das System selbst übertragen (bleiben, wie sie sind)` : ""}</div>
          <div style={{ fontSize: 12, color: "var(--text2)" }}>{Object.entries(prev.years || {}).map(([y, h]) => `${y}: ${fh(h)}`).join(" · ")}</div>
          {(prev.projects_new || []).length > 0 && <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 4 }}>Neu als archivierte Projekte (Name wie in Jira): {prev.projects_new.join(", ")}</div>}
          {prev.old_local > 0 && <label style={{ display: "flex", gap: 8, alignItems: "flex-start", marginTop: 8, fontSize: 12.5 }}>
            <input type="checkbox" checked={replace} onChange={e => setReplace(e.target.checked)} style={{ marginTop: 3 }} />
            <span><b>Grundimport:</b> {prev.old_local} alte lokale Buchungen vor {dt(prev.since || st.since)} ({fh(prev.old_local_h)}) durch die Tempo-Zeiten ersetzen.
              {sm ? " Vorher prüft der Server die Monatssperren." : " Vorher wird eine Sicherung angelegt."}</span></label>}
          {(prev.errors || []).length > 0 && <div style={{ fontSize: 12, color: "var(--stop)" }}>Nicht lesbar: {prev.errors.join(" · ")}</div>}
          <div style={{ display: "flex", gap: 8, marginTop: 10 }}>
            <button style={btn(true)} disabled={busy} onClick={() => run(false)}><Check size={14} /> {busy ? "Übernehme …" : "Übernehmen"}</button>
            <button style={btn(false)} onClick={() => setPrev(null)}>Abbrechen</button>
          </div>
        </div>)}
      {res && <div style={{ color: "var(--accent)" }}>✓ {res.new} neu, {res.changed} aktualisiert, {res.removed} entfernt{res.holidays ? `, ${res.holidays} Feiertage ergänzt` : ""}. Das Arbeitszeitkonto rechnet jetzt ab {dt(from)}.</div>}
      {err && <div style={{ color: "var(--stop)" }}>✗ {err}</div>}
      <div style={{ display: "flex", gap: 6, fontSize: 11, color: "var(--text2)" }}><ShieldCheck size={13} style={{ flex: "none" }} />
        <span>Nur deine eigenen Worklogs (dein Jira-Benutzer), nur lesend.
          {sm ? " Die Daten bleiben auf dem Team-Server – sie gehen an keinen anderen Dienst außer dem verbundenen Tempo/Jira." : " Die Daten bleiben in deiner Datenbank auf diesem Mac – sie gehen an keinen anderen Dienst."}</span></div>
    </div>);
}
