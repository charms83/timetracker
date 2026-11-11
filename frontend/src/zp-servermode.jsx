/* ═══════════════════════════════════════════════════════════════════
   Servermodus (Go-Server, Mehrbenutzer): nur dort sichtbar.
   Anmeldung/Abmeldung, Hinweise, Monatsfreigabe, Jira-Instanzen je Person,
   Hinweis „braucht den Mac-Client“. Am Node-System wird nichts davon eingebunden.
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback } from "react";
import { LogOut, ChevronLeft, ChevronRight, Send, Undo2, MonitorSmartphone, Info, KeyRound, Trash2, Check } from "lucide-react";
import { api, call, Box, lab, inpS, btn, loadMe, useServerMode, PlaneWorkspacesPanel } from "./zp-ui.jsx";
export { PlaneWorkspacesPanel };

/* ── Abmelden, Name, Administration (Seitenleiste / Titelleiste) ── */
const ROLE = { lead: "Teamleiter", user: "Mitarbeiter" };
export async function logout() {
  await call("POST", "/auth/logout");
  location.href = "/login";
}
export function ServerUserBox({ compact }) {
  const { serverMode, me } = useServerMode();
  if (!serverMode || !me) return null;
  // Admin/Notfall landen nie hier (isAdminOnly → /admin); Anzeige nur Fachrollen
  const roles = [ROLE[me.role] || "Mitarbeiter", me.is_lead && me.role !== "lead" ? "Teamleiter" : "", me.is_hr ? "Personal" : ""].filter(Boolean).join(" · ");
  if (compact) return (
    <div style={{ display: "flex", alignItems: "center", gap: 6, marginLeft: 8, WebkitAppRegion: "no-drag", fontSize: 12 }}>
      <span title={roles} style={{ color: "var(--text2)", maxWidth: 120, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{me.name}</span>
      <button className="zp-nav" title="Abmelden" onClick={logout}><LogOut size={15} /></button>
    </div>);
  return (
    <div style={{ marginTop: 8, padding: "10px 10px 8px", borderTop: "1px solid var(--line)", fontSize: 12 }}>
      <div style={{ fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={me.email || me.name}>{me.name}</div>
      {roles && <div style={{ color: "var(--text2)", fontSize: 11 }}>{roles}</div>}
      <div style={{ display: "flex", gap: 4, marginTop: 6, flexWrap: "wrap" }}>
        <button className="zp-nav" style={{ display: "inline-flex", alignItems: "center", gap: 5, fontSize: 12, padding: "4px 8px" }} onClick={logout}><LogOut size={14} /> Abmelden</button>
      </div>
    </div>);
}

/* ── Hinweise des Servers (/api/me › notices) als Banner ── */
export function ServerBanner() {
  const { serverMode, me } = useServerMode();
  if (!serverMode || !me?.notices?.length) return null;
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 6, marginBottom: 12 }}>
      {me.notices.map((n, i) => {
        const warn = /NOTFALL|ACHTUNG|abgelehnt/i.test(n);
        return (
          <div key={i} role="status" style={{ display: "flex", gap: 8, alignItems: "flex-start", fontSize: 13, padding: "9px 12px", borderRadius: 9,
            background: warn ? "var(--pause-bg, #fff7e0)" : "var(--accent-soft)", border: `1px solid ${warn ? "var(--pause-line, #e5c46b)" : "transparent"}` }}>
            <Info size={15} style={{ flex: "none", marginTop: 1, color: warn ? "#8a6400" : "var(--accent)" }} /><span>{n}</span>
          </div>);
      })}
    </div>);
}

/* ── Funktion nur mit dem Mac-Client ── */
export function ClientHint({ title = "Braucht den Mac-Client", children }) {
  return (
    <div className="zp-card" style={{ padding: "22px 24px", display: "flex", gap: 14, alignItems: "flex-start" }}>
      <MonitorSmartphone size={22} style={{ flex: "none", color: "var(--text2)", marginTop: 2 }} />
      <div>
        <div style={{ fontWeight: 600, fontSize: 14 }}>{title}</div>
        <div style={{ color: "var(--text2)", fontSize: 12.5, marginTop: 3, lineHeight: 1.5 }}>
          {children || "Diese Funktion erkennt Daten auf deinem Rechner (z. B. Meetings, SSH-Sitzungen, Zeit am Mac) und läuft deshalb nur im Mac-Client. Im Browser steht sie nicht zur Verfügung; alles andere funktioniert hier vollständig."}
        </div>
      </div>
    </div>);
}

/* ── Jira-Modul am Server an? (Administrator kann es abschalten) ── */
export function useJiraModule() {
  const [on, setOn] = useState(null);
  useEffect(() => { api("GET", "/jira/status").then(s => setOn(!(s && s.module === false) && !!s)); }, []);
  return on;
}

/* ═══ Monatsfreigabe (Mitarbeiter): einreichen / zurückziehen ═══ */
const MONTHS = ["Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"];
const monthLabel = m => `${MONTHS[+m.slice(5, 7) - 1]} ${m.slice(0, 4)}`;
const shiftMonth = (m, d) => { const x = new Date(+m.slice(0, 4), +m.slice(5, 7) - 1 + d, 1); return `${x.getFullYear()}-${String(x.getMonth() + 1).padStart(2, "0")}`; };
const curMonth = () => { const x = new Date(); return `${x.getFullYear()}-${String(x.getMonth() + 1).padStart(2, "0")}`; };
const fh = h => (Math.round(h * 4) / 4).toLocaleString("de-DE") + " h";
const ACT = { submit: "eingereicht", withdraw: "zurückgezogen", approve: "freigegeben", reject: "abgelehnt", reopen: "wiedereröffnet", auto_close: "automatisch geschlossen", nachtrag: "nachgetragen" };
const ST = {
  open: ["offen", ""], submitted: ["eingereicht – wartet auf Freigabe", "changed"], approved: ["freigegeben", "synced"],
  rejected: ["abgelehnt", "error"], closed: ["geschlossen", "new"],
};
const when = iso => { try { return new Date(iso).toLocaleString("de-DE", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" }); } catch (_) { return ""; } };

export function MonthApprovalCard() {
  const [month, setMonth] = useState(() => shiftMonth(curMonth(), -1));   // Standard: Vormonat (typischer Einreichzeitpunkt)
  const [st, setSt] = useState(null); const [miss, setMiss] = useState(null);
  const [busy, setBusy] = useState(false); const [msg, setMsg] = useState(null); const [hist, setHist] = useState(false);

  const load = useCallback(async () => {
    setSt(null); setMiss(null);
    const s = await call("GET", `/approval/status?month=${month}`);
    setSt(s);
    // Fehlzeiten: /missing-hours (max. 62 Tage) – ältere Monate über /overview
    const first = new Date(+month.slice(0, 4), +month.slice(5, 7) - 1, 1);
    const last = new Date(+month.slice(0, 4), +month.slice(5, 7), 0);
    const days = Math.ceil((Date.now() - first.getTime()) / 864e5) + 1;
    if (days <= 62) {
      const r = await api("GET", `/missing-hours?days=${Math.max(1, days)}`);
      if (r && !r.error && Array.isArray(r.days)) {
        const d = r.days.filter(x => x.date.slice(0, 7) === month);
        setMiss({ days: d, total: d.reduce((a, x) => a + (x.missing_h || 0), 0), optional: !!r.optional });
      } else setMiss(null);
    } else {
      const from = `${month}-01`, to = `${month}-${String(last.getDate()).padStart(2, "0")}`;
      const ov = await api("GET", `/overview?from=${from}&to=${to}`);
      if (ov && !ov.error && Array.isArray(ov.days)) {
        const d = ov.days.filter(x => {
          if (x.absence || x.isBeforeTracking) return false;
          const target = x.targetH || 0, booked = x.workH || x.bookedH || 0;
          return target > 0 && booked < target - 0.24;
        }).map(x => ({ date: x.date, missing_h: Math.max(0, (x.targetH || 0) - (x.workH || x.bookedH || 0)), empty: !(x.workH || x.bookedH) }));
        setMiss({ days: d, total: d.reduce((a, x) => a + x.missing_h, 0), optional: false });
      } else setMiss(null);
    }
  }, [month]);
  useEffect(() => { load(); }, [load]);
  useEffect(() => { const h = () => load(); window.addEventListener("zp:entry-changed", h); return () => window.removeEventListener("zp:entry-changed", h); }, [load]);

  const act = async (kind) => {
    if (busy) return;
    if (kind === "submit") {
      const warn = miss && miss.days.length ? `\n\nAchtung: Es fehlen noch ${fh(miss.total)} an ${miss.days.length} Tag${miss.days.length === 1 ? "" : "en"}.` : "";
      if (!confirm(`${monthLabel(month)} jetzt einreichen?\n\nDanach ist der Monat gesperrt, bis er freigegeben oder von dir zurückgezogen wird.${warn}`)) return;
    }
    setBusy(true); setMsg(null);
    const r = await call("POST", `/approval/${kind}`, { month });
    setBusy(false);
    if (r.error) { setMsg({ err: true, text: r.error }); return; }
    setMsg({ text: kind === "submit" ? "Monat eingereicht" : "Einreichung zurückgezogen" });
    await load(); loadMe(); window.dispatchEvent(new Event("zp:entry-changed"));
  };

  const status = st && !st.error ? st.status : null;
  const [lbl, cls] = ST[status] || ["…", ""];
  const arrow = (d, dis) => <button className="zp-nav" disabled={dis} onClick={() => { setMonth(m => shiftMonth(m, d)); setMsg(null); }} title={d < 0 ? "Vormonat" : "Folgemonat"}>{d < 0 ? <ChevronLeft size={16} /> : <ChevronRight size={16} />}</button>;
  return (
    <Box title="Monatsfreigabe" sub="Reiche den Monat ein, wenn alles stimmt. Teamleitung oder Personal geben ihn frei; eingereichte und freigegebene Monate sind gesperrt."
      right={<div style={{ display: "flex", alignItems: "center", gap: 2 }}>{arrow(-1, false)}<b style={{ minWidth: 118, textAlign: "center", fontSize: 13 }}>{monthLabel(month)}</b>{arrow(1, month >= curMonth())}</div>}>
      {!st ? <div style={{ color: "var(--text3)", fontSize: 12 }}>Lade…</div> : st.error ? <div style={{ color: "var(--stop)", fontSize: 12 }}>✗ {st.error}</div> : (<>
        <div style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
          <span className={`zp-pill ${cls}`}>{lbl}</span>
          {status === "submitted" && st.submitted_at && <span style={{ fontSize: 12, color: "var(--text2)" }}>seit {when(st.submitted_at)}</span>}
          {status === "approved" && <span style={{ fontSize: 12, color: "var(--text2)" }}>{st.decided_by_name ? `von ${st.decided_by_name} ` : ""}{st.decided_at ? `am ${when(st.decided_at)}` : ""} – Nachträge nur durch Teamleitung/Personal oder nach Wiedereröffnung.</span>}
          {status === "closed" && <span style={{ fontSize: 12, color: "var(--text2)" }}>{st.decided_at ? `seit ${when(st.decided_at)} · ` : ""}Nachtragen nur noch durch Teamleitung oder Personal (mit Begründung).</span>}
          <div style={{ flex: 1 }} />
          {(status === "open" || status === "rejected") && <button style={btn(true)} disabled={busy} onClick={() => act("submit")}><Send size={14} /> {status === "rejected" ? "Erneut einreichen" : "Monat einreichen"}</button>}
          {status === "submitted" && <button style={btn(false)} disabled={busy} onClick={() => act("withdraw")}><Undo2 size={14} /> Zurückziehen</button>}
        </div>
        {status === "closed" && (
          <div style={{ marginTop: 10, padding: "9px 12px", borderRadius: 8, background: "var(--fill)", border: "1px solid var(--line)", fontSize: 13 }}>
            <b>Monat geschlossen.</b> Die Nachfrist nach Monatsende ist abgelaufen. Du kannst hier nichts mehr ändern.
            <div style={{ color: "var(--text2)", fontSize: 12, marginTop: 2 }}>Fehlende oder falsche Zeiten: Teamleitung oder Personal tragen sie nach – mit Begründung und Protokoll. Wiedereröffnen kann nur das Personal.</div>
          </div>)}
        {status === "rejected" && (
          <div style={{ marginTop: 10, padding: "9px 12px", borderRadius: 8, background: "rgba(217,69,59,.08)", fontSize: 13 }}>
            <b>Abgelehnt{st.decided_by_name ? ` von ${st.decided_by_name}` : ""}.</b> {st.comment ? <>Kommentar: „{st.comment}“</> : null}
            <div style={{ color: "var(--text2)", fontSize: 12, marginTop: 2 }}>Bitte korrigieren (der Monat ist wieder offen) und danach neu einreichen.</div>
          </div>)}
        {(status === "open" || status === "rejected") && miss && (miss.days.length > 0
          ? <div style={{ marginTop: 10, fontSize: 12.5, padding: "8px 12px", borderRadius: 8, background: "var(--pause-bg, #fff7e0)", border: "1px solid var(--pause-line, #e5c46b)" }}>
              <b>Es fehlen noch {fh(miss.total)}</b> an {miss.days.length} Tag{miss.days.length === 1 ? "" : "en"}{miss.optional ? " (dein Konto steht im Plus – nur ein Hinweis)" : ""}:{" "}
              {miss.days.slice(0, 6).map((x, i) => <span key={x.date}>{i ? ", " : ""}<a href={"#vorschlaege/nachtragen/" + x.date}>{x.date.slice(8, 10)}.{x.date.slice(5, 7)}.</a> ({x.empty ? "leer" : fh(x.missing_h)})</span>)}{miss.days.length > 6 ? ` … +${miss.days.length - 6}` : ""}
              <div style={{ marginTop: 4 }}><a href="#vorschlaege/nachtragen" style={{ fontWeight: 600 }}>Nachtragen öffnen ›</a></div>
            </div>
          : <div style={{ marginTop: 10, fontSize: 12.5, color: "var(--accent)" }}><Check size={13} style={{ verticalAlign: -2 }} /> Keine fehlenden Stunden erkannt.</div>)}
        {msg && <div style={{ fontSize: 12, marginTop: 8, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</div>}
        {(st.history || []).length > 0 && (
          <div style={{ marginTop: 10 }}>
            <button onClick={() => setHist(h => !h)} style={{ border: "none", background: "none", color: "var(--text2)", fontSize: 12, cursor: "pointer", padding: 0 }}>{hist ? "▾" : "▸"} Verlauf ({st.history.length})</button>
            {hist && st.history.map((h, i) => (
              <div key={i} style={{ display: "flex", gap: 10, fontSize: 12, color: "var(--text2)", borderTop: "1px solid var(--line)", padding: "4px 0", marginTop: i ? 0 : 4 }}>
                <span style={{ minWidth: 96 }}>{when(h.at)}</span><span style={{ minWidth: 100, color: "var(--text)" }}>{ACT[h.action] || h.action}</span>
                <span>{h.actor || ""}</span><span style={{ flex: 1 }}>{h.comment ? `„${h.comment}“` : ""}</span></div>))}
          </div>)}
      </>)}
    </Box>);
}

/* ═══ Jira-Instanzen je Person: Token und Tempo-Einstellungen ═══
   mode "tokens": Liste der Instanzen mit Token speichern/entfernen · "tempo": Tempo-Einstellungen je Instanz */
const FIELDS = [
  ["tempo_booking_issue", "Sammel-Buchungsticket", "leer = legt das System je Projekt selbst an", true],
  ["jira_new_project", "Jira-Projekt für neue Tickets", "z. B. ZEIT (leer = Sammelticket)", true],
  ["jira_new_issuetype", "Vorgangstyp", "Task", false],
  ["tempo_account", "Tempo-Account", "z. B. IMPROVEMENTS", false],
  ["tempo_account_attr_id", "Tempo-Account-Attribut (Nummer)", "nur falls nötig", false],
  ["tempo_push_since", "Übertragen ab (abweichend)", "", false],
];
function InstanceTempo({ inst }) {
  const [v, setV] = useState(null); const [msg, setMsg] = useState(null); const [busy, setBusy] = useState(false);
  useEffect(() => { call("GET", `/jira/instances/${inst.id}/settings`).then(r => setV(r.error ? { _err: r.error } : r)); }, [inst.id]);
  if (!v) return <div style={{ fontSize: 12, color: "var(--text3)" }}>Lade…</div>;
  if (v._err) return <div style={{ fontSize: 12, color: "var(--stop)" }}>✗ {v._err}</div>;
  const set = k => e => setV(x => ({ ...x, [k]: e.target.value }));
  const save = async () => {
    setBusy(true); setMsg(null);
    const body = Object.fromEntries(FIELDS.map(([k]) => [k, String(v[k] || "").trim()]));
    body.tempo_booking_issue = body.tempo_booking_issue.toUpperCase(); body.jira_new_project = body.jira_new_project.toUpperCase();
    const r = await call("PUT", `/jira/instances/${inst.id}/settings`, body);
    setBusy(false); setMsg(r.error ? { err: true, text: r.error } : { text: "Gespeichert" });
  };
  return (
    <div>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "0 10px" }}>
        {FIELDS.map(([k, l, ph]) => <div key={k}><label style={lab}>{l}</label>
          <input style={inpS} type={k === "tempo_push_since" ? "date" : "text"} placeholder={ph} value={v[k] || ""} onChange={set(k)} /></div>)}
      </div>
      <div style={{ display: "flex", gap: 8, alignItems: "center", marginTop: 10 }}>
        <button style={btn(true)} disabled={busy} onClick={save}>{busy ? "Speichere…" : "Speichern"}</button>
        {msg && <span style={{ fontSize: 12, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</span>}
      </div>
    </div>);
}
function InstanceToken({ inst, onChanged }) {
  const [val, setVal] = useState(""); const [msg, setMsg] = useState(null); const [busy, setBusy] = useState(false);
  const save = async () => {
    if (!val.trim() || busy) return;
    setBusy(true); setMsg(null);
    const r = await call("PUT", `/profile/secrets/jira_token?instance=${inst.id}`, { value: val.trim() });
    setBusy(false);
    if (r.error) { setMsg({ err: true, text: r.error }); return; }
    setVal(""); setMsg({ text: "Token gespeichert" }); onChanged();
  };
  const del = async () => {
    if (!confirm(`Token für „${inst.name}“ entfernen?`)) return;
    setBusy(true); setMsg(null);
    const r = await call("DELETE", `/profile/secrets/jira_token?instance=${inst.id}`);
    setBusy(false);
    if (r.error) { setMsg({ err: true, text: r.error }); return; }
    setMsg({ text: "Token entfernt" }); onChanged();
  };
  return (
    <div>
      <label style={lab}>Persönliches Zugriffstoken (Jira Personal Access Token – wird verschlüsselt gespeichert, nie wieder angezeigt)</label>
      <div style={{ display: "flex", gap: 8 }}>
        <input style={inpS} type="password" autoComplete="new-password" placeholder={inst.token_set ? "•••• gespeichert – zum Ersetzen neues Token eingeben" : "Token einfügen"} value={val} onChange={e => setVal(e.target.value)} onKeyDown={e => e.key === "Enter" && save()} />
        <button style={btn(true)} disabled={!val.trim() || busy} onClick={save}><KeyRound size={14} /> Speichern</button>
        {inst.token_set && <button style={btn(false)} disabled={busy} onClick={del} title="Token entfernen"><Trash2 size={14} /></button>}
      </div>
      {msg && <div style={{ fontSize: 12, marginTop: 6, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</div>}
    </div>);
}
export function JiraInstancesPanel({ mode = "tokens", status }) {
  const [list, setList] = useState(null);
  const load = useCallback(() => call("GET", "/jira/instances").then(r => setList(r.error ? { error: r.error } : r)), []);
  useEffect(() => { load(); }, [load]);
  if (!list) return <div style={{ color: "var(--text3)", fontSize: 12 }}>Lade…</div>;
  if (list.error) return <div style={{ color: "var(--text2)", fontSize: 12 }}>{list.error}</div>;
  if (!list.length) return <div style={{ color: "var(--text2)", fontSize: 12 }}>Es ist keine Jira-Instanz eingerichtet. Das legt der Administrator fest.</div>;
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      {list.map(i => {
        const st = (status?.instances || []).find(x => x.id === i.id);
        return (
          <div key={i.id} style={{ border: "1px solid var(--line)", borderRadius: 10, padding: "12px 14px" }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
              <b>{i.name}</b>
              <span className="zp-pill">{i.kind === "cloud" ? "Jira Cloud" : "Jira Data Center"}</span>
              {(i.tempo === "dc" || i.tempo === "cloud") && <span className="zp-pill">Tempo</span>}
              {mode === "tokens" && <span className={`zp-pill ${i.token_set ? "synced" : "changed"}`}>{i.token_set ? "Token gespeichert" : "Token fehlt"}</span>}
              {st && <span style={{ fontSize: 12, color: st.ok ? "var(--accent)" : "var(--stop)" }}>{st.ok ? "✓ " : "✗ "}{st.message}</span>}
            </div>
            {mode === "tokens" ? <div style={{ marginTop: 6 }}><InstanceToken inst={i} onChanged={load} /></div> : <div style={{ marginTop: 6 }}><InstanceTempo inst={i} /></div>}
          </div>);
      })}
    </div>);
}


