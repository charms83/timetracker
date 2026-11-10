/* Urlaubsantrag (Servermodus): stellen / stornieren; Teamleitung entscheidet, Personal als Vertretung. */
import { useState, useEffect, useCallback } from "react";
import { Umbrella, Check, X, Send, Undo2 } from "lucide-react";
import { call, Box, lab, inpS, btn, useServerMode } from "./zp-ui.jsx";

const ST = { pending: ["offen", "changed"], approved: ["genehmigt", "synced"], rejected: ["abgelehnt", "error"], cancelled: ["storniert", ""] };
const de = s => s ? new Date(s.length > 10 ? s : s + "T12:00").toLocaleDateString("de-DE", { day: "2-digit", month: "2-digit", year: "numeric" }) : "–";

export function LeaveRequestCard() {
  const { serverMode } = useServerMode();
  const [list, setList] = useState(null);
  const [from, setFrom] = useState(""); const [to, setTo] = useState(""); const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false); const [msg, setMsg] = useState(null);
  const load = useCallback(() => call("GET", "/leave").then(r => setList(Array.isArray(r) ? r : r?.error ? { error: r.error } : [])), []);
  useEffect(() => { if (serverMode) load(); }, [serverMode, load]);
  if (!serverMode) return null;

  const submit = async () => {
    if (!from || busy) return;
    setBusy(true); setMsg(null);
    const r = await call("POST", "/leave", { date_from: from, date_to: to || from, note });
    setBusy(false);
    if (r?.error) { setMsg({ err: true, text: r.error }); return; }
    setFrom(""); setTo(""); setNote(""); setMsg({ text: "Antrag gestellt – wartet auf Teamleitung" }); load();
  };
  const cancel = async id => {
    if (!confirm("Antrag stornieren?") || busy) return;
    setBusy(true);
    const r = await call("POST", `/leave/${id}/cancel`);
    setBusy(false);
    if (r?.error) setMsg({ err: true, text: r.error }); else { setMsg({ text: "Storniert" }); load(); }
  };

  return (
    <Box title="Urlaubsantrag" sub="Teamleitung genehmigt; Vertretung: Personal. Genehmigt → Abwesenheit Urlaub.">
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "0 10px" }}>
        <div><label style={lab}>Von</label><input style={inpS} type="date" value={from} onChange={e => setFrom(e.target.value)} /></div>
        <div><label style={lab}>Bis</label><input style={inpS} type="date" value={to} onChange={e => setTo(e.target.value)} /></div>
      </div>
      <div style={{ marginTop: 6 }}><label style={lab}>Notiz (optional)</label>
        <input style={inpS} value={note} onChange={e => setNote(e.target.value)} placeholder="z. B. Sommerurlaub" onKeyDown={e => e.key === "Enter" && submit()} /></div>
      <div style={{ display: "flex", gap: 8, alignItems: "center", marginTop: 10 }}>
        <button style={btn(true)} disabled={!from || busy} onClick={submit}><Send size={14} /> {busy ? "…" : "Antrag stellen"}</button>
        {msg && <span style={{ fontSize: 12, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</span>}
      </div>
      {!list ? <div style={{ color: "var(--text3)", fontSize: 12, marginTop: 12 }}>Lade…</div>
        : list.error ? <div style={{ color: "var(--stop)", fontSize: 12, marginTop: 12 }}>✗ {list.error}</div>
        : !list.length ? <div style={{ color: "var(--text2)", fontSize: 12.5, marginTop: 12 }}>Noch keine Anträge.</div>
        : <div style={{ marginTop: 12, display: "flex", flexDirection: "column", gap: 6 }}>
            {list.slice(0, 12).map(l => {
              const [lbl, cls] = ST[l.status] || [l.status, ""];
              return (
                <div key={l.id} style={{ display: "flex", gap: 10, alignItems: "center", fontSize: 13, borderTop: "1px solid var(--line)", paddingTop: 6 }}>
                  <Umbrella size={14} style={{ color: "var(--text2)" }} />
                  <span style={{ minWidth: 150 }}>{de(l.date_from)}{l.date_to !== l.date_from ? ` – ${de(l.date_to)}` : ""}</span>
                  <span style={{ color: "var(--text2)" }}>{l.days != null ? `${Number(l.days).toLocaleString("de-DE")} Tage` : ""}</span>
                  <span className={`zp-pill ${cls}`}>{lbl}</span>
                  <span style={{ flex: 1, color: "var(--text2)", fontSize: 12 }}>{l.note || l.decision_note || ""}</span>
                  {l.status === "pending" && <button style={btn(false)} onClick={() => cancel(l.id)} title="Stornieren"><Undo2 size={13} /></button>}
                </div>);
            })}
          </div>}
    </Box>);
}

export function LeavePendingPanel() {
  const { serverMode, me } = useServerMode();
  const [list, setList] = useState(null);
  const [busy, setBusy] = useState(false); const [msg, setMsg] = useState(null);
  const load = useCallback(() => call("GET", "/leave/pending").then(r => setList(Array.isArray(r) ? r : r?.error ? { error: r.error } : [])), []);
  useEffect(() => { if (serverMode && (me?.is_lead || me?.is_hr)) load(); }, [serverMode, me, load]);
  if (!serverMode || !(me?.is_lead || me?.is_hr)) return null;

  const decide = async (id, decision) => {
    if (busy) return;
    let note = "";
    if (decision === "rejected") {
      note = prompt("Kommentar zur Ablehnung (optional):") ?? "";
      if (note === null) return;
    } else if (!confirm("Urlaub genehmigen? Es wird eine Abwesenheit „Urlaub“ angelegt.")) return;
    setBusy(true); setMsg(null);
    const r = await call("POST", `/leave/${id}/decide`, { decision, note });
    setBusy(false);
    if (r?.error) setMsg({ err: true, text: r.error }); else { setMsg({ text: decision === "approved" ? "Genehmigt" : "Abgelehnt" }); load(); }
  };

  return (
    <Box title="Urlaubsanträge" sub={me?.is_hr && !me?.is_lead ? "Vertretung Personal – alle offenen Anträge." : "Offene Anträge deines Teams (Personal sieht alle)."}>
      {msg && <div style={{ fontSize: 12, marginBottom: 8, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</div>}
      {!list ? <div style={{ color: "var(--text3)", fontSize: 12 }}>Lade…</div>
        : list.error ? <div style={{ color: "var(--stop)", fontSize: 12 }}>✗ {list.error}</div>
        : !list.length ? <div style={{ color: "var(--text2)", fontSize: 12.5 }}>Keine offenen Anträge.</div>
        : list.map(l => (
          <div key={l.id} style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap", fontSize: 13, borderTop: "1px solid var(--line)", padding: "8px 0" }}>
            <b style={{ minWidth: 120 }}>{l.user_name || l.user_email}</b>
            <span>{de(l.date_from)}{l.date_to !== l.date_from ? ` – ${de(l.date_to)}` : ""}</span>
            <span style={{ color: "var(--text2)" }}>{l.days != null ? `${Number(l.days).toLocaleString("de-DE")} Tage` : ""}</span>
            <span style={{ flex: 1, color: "var(--text2)", fontSize: 12 }}>{l.note || ""}</span>
            <button style={{ ...btn(true), padding: "4px 10px", fontSize: 12 }} disabled={busy} onClick={() => decide(l.id, "approved")}><Check size={13} /> Genehmigen</button>
            <button style={{ ...btn(false), padding: "4px 10px", fontSize: 12 }} disabled={busy} onClick={() => decide(l.id, "rejected")}><X size={13} /> Ablehnen</button>
          </div>))}
    </Box>);
}
