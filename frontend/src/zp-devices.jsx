/* API- und Geräte-Token (REST Bearer zpdev_…). Mac-Client: normal Login-Sitzung. */
import { useState, useEffect, useCallback } from "react";
import { Smartphone, Plus, Trash2, Copy, Check } from "lucide-react";
import { call, Box, lab, inpS, btn, useServerMode } from "./zp-ui.jsx";

const KINDS = [
  ["api", "API / REST"],
  ["mac", "Mac-Client"],
  ["addon", "Browser-Add-on"],
  ["minimal", "Minimal / curl"],
  ["other", "Sonstiges"],
];
const when = iso => { try { return new Date(iso).toLocaleString("de-DE", { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit" }); } catch (_) { return ""; } };

export function DevicesPanel() {
  const { serverMode } = useServerMode();
  const [list, setList] = useState(null);
  const [label, setLabel] = useState(""); const [kind, setKind] = useState("api");
  const [busy, setBusy] = useState(false); const [msg, setMsg] = useState(null); const [fresh, setFresh] = useState(null);
  const [copied, setCopied] = useState(false);
  const load = useCallback(() => call("GET", "/devices").then(r => setList(Array.isArray(r) ? r : r?.error ? { error: r.error } : [])), []);
  useEffect(() => { if (serverMode) load(); }, [serverMode, load]);
  if (!serverMode) return null;

  const create = async () => {
    if (busy) return;
    setBusy(true); setMsg(null); setFresh(null);
    const r = await call("POST", "/devices", { label: label.trim() || undefined, kind });
    setBusy(false);
    if (r?.error) { setMsg({ err: true, text: r.error }); return; }
    setLabel(""); setFresh(r); setMsg({ text: "Token angelegt – bitte jetzt kopieren" }); load();
  };
  const revoke = async id => {
    if (!confirm("Token widerrufen? Danach kein API-Zugang mehr mit diesem Schlüssel.") || busy) return;
    setBusy(true);
    const r = await call("DELETE", `/devices/${id}`);
    setBusy(false);
    if (r?.error) setMsg({ err: true, text: r.error }); else { setMsg({ text: "Widerrufen" }); if (fresh?.id === id) setFresh(null); load(); }
  };
  const copy = async () => {
    if (!fresh?.token) return;
    try { await navigator.clipboard.writeText(fresh.token); setCopied(true); setTimeout(() => setCopied(false), 1500); } catch (_) { setMsg({ err: true, text: "Kopieren fehlgeschlagen – Token manuell markieren" }); }
  };

  const active = Array.isArray(list) ? list.filter(d => d.active !== false && !d.revoked_at) : [];
  const revoked = Array.isArray(list) ? list.filter(d => d.revoked_at) : [];

  return (
    <Box title="API- und Geräte-Token" sub="REST-Zugang für Skripte: Authorization: Bearer zpdev_…. Gehasht, widerrufbar, nie für /admin. Mac-App braucht normal nur Login.">
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "flex-end" }}>
        <div style={{ flex: "1 1 140px" }}><label style={lab}>Bezeichnung</label>
          <input style={inpS} value={label} onChange={e => setLabel(e.target.value)} placeholder="z. B. Skript Zeiterfassung" /></div>
        <div><label style={lab}>Art</label>
          <select style={inpS} value={kind} onChange={e => setKind(e.target.value)}>{KINDS.map(([k, l]) => <option key={k} value={k}>{l}</option>)}</select></div>
        <button style={btn(true)} disabled={busy} onClick={create}><Plus size={14} /> Token anlegen</button>
      </div>
      {fresh?.token && (
        <div style={{ marginTop: 10, padding: "10px 12px", borderRadius: 8, background: "var(--accent-soft)", border: "1px solid var(--line)", fontSize: 12.5 }}>
          <b>Einmalig sichtbar:</b> Header <code>Authorization: Bearer …</code> – nie für Administration.
          <div style={{ display: "flex", gap: 8, alignItems: "center", marginTop: 6, flexWrap: "wrap" }}>
            <code style={{ flex: 1, wordBreak: "break-all", fontSize: 11 }}>{fresh.token}</code>
            <button style={btn(false)} onClick={copy}>{copied ? <Check size={13} /> : <Copy size={13} />} {copied ? "Kopiert" : "Kopieren"}</button>
          </div>
          <div style={{ marginTop: 8, fontSize: 11, color: "var(--text2)", fontFamily: "ui-monospace, monospace" }}>
            curl -sH &apos;Authorization: Bearer …&apos; https://…/api/me
          </div>
        </div>)}
      {msg && <div style={{ fontSize: 12, marginTop: 8, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</div>}
      {!list ? <div style={{ color: "var(--text3)", fontSize: 12, marginTop: 12 }}>Lade…</div>
        : list.error ? <div style={{ color: "var(--stop)", fontSize: 12, marginTop: 12 }}>✗ {list.error}</div>
        : <>
          <div style={{ marginTop: 14, fontSize: 12, fontWeight: 600 }}>Aktiv ({active.length})</div>
          {!active.length ? <div style={{ color: "var(--text2)", fontSize: 12.5 }}>Noch kein Token – für REST eines mit Art „API / REST“ anlegen.</div>
            : active.map(d => (
              <div key={d.id} style={{ display: "flex", gap: 10, alignItems: "center", fontSize: 13, borderTop: "1px solid var(--line)", padding: "7px 0" }}>
                <Smartphone size={14} style={{ color: "var(--text2)" }} />
                <b>{d.label || "Token"}</b>
                <span className="zp-pill">{KINDS.find(([k]) => k === d.kind)?.[1] || d.kind}</span>
                <span style={{ flex: 1, color: "var(--text2)", fontSize: 12 }}>angelegt {when(d.created_at)}{d.last_used ? ` · zuletzt ${when(d.last_used)}` : ""}</span>
                <button style={btn(false)} onClick={() => revoke(d.id)} title="Widerrufen"><Trash2 size={13} /></button>
              </div>))}
          {revoked.length > 0 && <div style={{ marginTop: 10, fontSize: 11, color: "var(--text3)" }}>{revoked.length} widerrufen</div>}
        </>}
    </Box>);
}
