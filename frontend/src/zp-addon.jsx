/* ═══════════════════════════════════════════════════════════════════
   Browser-Add-on (v6.20.0): Kopplung erlauben, Browser erkennen, verbundene Add-ons
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback } from "react";
import { ShieldCheck, Check, X, Puzzle } from "lucide-react";
import { api, call, btn } from "./zp-ui.jsx";

const dt = s => s ? new Date(s).toLocaleString("de-DE", { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit" }) : "noch nie";

/* Erscheint, wenn das Add-on „Mit Zeitprotokoll verbinden“ öffnet: #koppeln/<nonce> */
export function PairApprove({ nonce, onDone }) {
  const [info, setInfo] = useState(null); const [err, setErr] = useState(null); const [done, setDone] = useState(null);
  useEffect(() => { api("GET", `/pair/info?nonce=${encodeURIComponent(nonce)}`).then(r => r?.error ? setErr(r.error) : setInfo(r)); }, [nonce]);
  const answer = async allow => { const r = await call("POST", "/pair/approve", { nonce, allow }); if (r?.error) return setErr(r.error); setDone(allow ? "Verbunden ✓ – das Add-on ist jetzt bereit. Diesen Tab kannst du schließen." : "Abgelehnt."); };
  return (
    <div style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.35)", zIndex: 300, display: "flex", alignItems: "flex-start", justifyContent: "center", paddingTop: 90 }}>
      <div className="zp-card" style={{ width: "min(460px, calc(100vw - 32px))", padding: "16px 18px", fontSize: 13 }}>
        <div style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 8 }}><Puzzle size={18} style={{ color: "var(--accent)" }} /><b style={{ fontSize: 15, flex: 1 }}>Browser-Add-on verbinden?</b>
          <button className="zp-nav" onClick={onDone}><X size={14} /></button></div>
        {err && <div style={{ color: "var(--stop)" }}>{err}</div>}
        {done && <div style={{ color: "var(--accent)", fontWeight: 600 }}>{done}</div>}
        {info && !done && <>
          <div><b>{info.client}</b> {info.ext_version && <span style={{ color: "var(--text2)" }}>· Add-on {info.ext_version}</span>}</div>
          <div style={{ fontSize: 11, color: "var(--text2)", wordBreak: "break-all" }}>{info.origin}</div>
          <div style={{ marginTop: 10 }}>Das Add-on darf danach <b>nur</b>:</div>
          <ul style={{ margin: "4px 0 0 18px", color: "var(--text2)" }}>
            <li>den Timer auf einem Ticket starten, pausieren, stoppen</li><li>ein To-do anlegen</li><li>sehen, ob und worauf der Timer läuft</li></ul>
          <div style={{ marginTop: 6, color: "var(--text2)" }}>Buchungen, Einstellungen und Zugangsdaten sieht es nicht. Trennen geht jederzeit unter Einstellungen › Erkennung.</div>
          <div style={{ display: "flex", gap: 8, marginTop: 14 }}>
            <button style={btn(true)} onClick={() => answer(true)}><Check size={14} /> Erlauben</button>
            <button style={btn(false)} onClick={() => answer(false)}>Ablehnen</button>
          </div>
          <div style={{ fontSize: 11, color: "var(--text2)", marginTop: 8 }}>Nur erlauben, wenn du gerade selbst im Add-on auf „verbinden“ geklickt hast.</div>
        </>}
      </div>
    </div>);
}

/* Einstellungen › Erkennung */
export function AddonSettings() {
  const [browsers, setBrowsers] = useState(null); const [clients, setClients] = useState([]); const [msg, setMsg] = useState({});
  const desk = typeof window !== "undefined" && window.zpDesktop?.browsers;
  const load = useCallback(() => api("GET", "/pair/clients").then(r => setClients(Array.isArray(r) ? r : [])), []);
  useEffect(() => { load(); if (!desk) return; const f = () => window.zpDesktop.browsers().then(setBrowsers).catch(() => setBrowsers([])); f();
    window.addEventListener("focus", f); return () => window.removeEventListener("focus", f); }, [load, desk]);   // Status neu, wenn du aus dem Browser zurückkommst
  const install = async name => { const r = await window.zpDesktop.installAddon(name); setMsg(m => ({ ...m, [name]: r?.text || r?.error || "" })); };
  return (
    <div className="zp-card" style={{ padding: "12px 16px", marginBottom: 12, fontSize: 13 }}>
      <div style={{ display: "flex", gap: 8, alignItems: "center" }}><Puzzle size={16} style={{ color: "var(--accent)" }} /><b>Browser-Add-on</b></div>
      <div style={{ fontSize: 12, color: "var(--text2)", margin: "4px 0 8px" }}>Jira- oder Plane-Ticket im Browser offen → im Add-on „▶ Tracken“. Der Timer läuft dann hier ganz normal, bis du ihn stoppst. Das Add-on spricht mit der lokalen App oder dem Team-Server (Adresse und Geräte-Token im Add-on-Popup).</div>
      {desk ? (browsers === null ? <div style={{ color: "var(--text2)" }}>Suche Browser …</div> : <>
        <div style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 4 }}>
          <span style={{ flex: 1, fontSize: 12, color: "var(--text2)" }}>Auf diesem Mac gefunden: {browsers.map(b => b.name).join(", ") || "keine"}</span>
          {browsers.some(b => b.kind !== "safari" && !b.connected) && <button style={{ ...btn(true), padding: "4px 12px" }} onClick={async () => {
            const r = await window.zpDesktop.installAll(); setMsg(Object.fromEntries((r || []).map(x => [x.name, x.text || x.error || ""])));
            setTimeout(() => window.zpDesktop.browsers().then(setBrowsers), 4000); }}>Alle einrichten</button>}
        </div>
        {browsers.map(b => (
        <div key={b.name} style={{ borderTop: "1px solid var(--line)", padding: "7px 0" }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span style={{ flex: 1 }}>{b.name}</span>
            {b.kind === "safari" ? <span style={{ fontSize: 11.5, color: "var(--text2)" }}>kein Add-on – Zeitprotokoll erkennt das Ticket selbst (Tracker öffnen)</span>
              : b.connected ? <span style={{ fontSize: 12, color: "var(--accent)", fontWeight: 600 }}><ShieldCheck size={12} style={{ verticalAlign: -2 }} /> Add-on verbunden</span>
              : <button style={{ ...btn(false), padding: "3px 10px" }} onClick={() => install(b.name)}>Einrichten</button>}
          </div>
          {msg[b.name] && <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 4 }}>{msg[b.name]}</div>}
        </div>))}</>) : <div style={{ fontSize: 12, color: "var(--text2)" }}>Einrichten: Zeitprotokoll-Symbol oben in der Menüleiste › <b>„Browser-Add-ons einrichten …“</b> – die Mac-App findet deine Browser und richtet alle auf einmal ein.</div>}
      <div style={{ marginTop: 10 }}><b style={{ fontSize: 12.5 }}>Verbundene Add-ons</b></div>
      {!clients.length && <div style={{ fontSize: 12, color: "var(--text2)" }}>keine</div>}
      {clients.map(c => (
        <div key={c.id} style={{ display: "flex", gap: 8, alignItems: "center", padding: "5px 0", borderTop: "1px solid var(--line)", fontSize: 12.5 }}>
          <ShieldCheck size={13} style={{ color: "var(--accent)" }} />
          <span style={{ flex: 1 }}>{c.name} <span style={{ color: "var(--text2)" }}>· verbunden {dt(c.created)} · zuletzt {dt(c.last_used)}</span></span>
          <button style={{ ...btn(false), padding: "2px 9px", color: "var(--stop)" }} onClick={async () => { if (!confirm(`${c.name} trennen? Das Add-on muss danach neu verbunden werden.`)) return; await call("DELETE", `/pair/clients/${c.id}`); load(); }}>Trennen</button>
        </div>))}
    </div>);
}

