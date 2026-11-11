/* ═══════════════════════════════════════════════════════════════════
   Bereich „Personal“ (nur Servermodus): alle Personen je Monat mit Filtern und Seitenwechsel,
   offene Freigaben (Freigeben / Ablehnen / Wiedereröffnen), Personen-Detail.
   Abgefragt wird nur beim Öffnen, bei Filter-/Monatswechsel und über „Aktualisieren“.
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback } from "react";
import { UserCog, Check, X, RotateCcw, Search, Download } from "lucide-react";
import { call, Box, inpS, btn, useServerMode } from "./zp-ui.jsx";
import { MonthNav, RefreshBtn, PeopleTable, PersonDetail, ReasonDialog, Empty, Err, STATUS, prevMonth, monthLabel, friendly, when, peopleOverviewCsv } from "./zp-people.jsx";
import { LeavePendingPanel } from "./zp-leave.jsx";

const LIMIT = 50;
const SELF_MSG = "Nicht möglich: Eigene Monate gibt das Personal nicht selbst frei (oder keine Berechtigung). Bitte eine andere Person aus dem Personal bitten.";
const sBtn = { ...btn(false), padding: "4px 10px", fontSize: 12 };

/* Schaltflächen je Person und Monat. status: Monatsstatus; onDone({text, err}) meldet das Ergebnis. */
function ApprovalButtons({ userId, name, month, status, onDone, compact }) {
  const { me } = useServerMode();
  const [dlg, setDlg] = useState(null);   // "reject" | "reopen" | "self"
  const [busy, setBusy] = useState(false);
  const self = me && Number(me.id) === Number(userId);
  const post = async (path, body) => {
    const r = await call("POST", `/approval/${path}`, { user_id: userId, month, ...body });
    if (r.error) return /nicht gefunden|HTTP 404/i.test(r.error) ? SELF_MSG : /^HTTP 409$/.test(r.error) ? "Konflikt (409): Der Monat hat inzwischen einen anderen Status. Bitte aktualisieren." : r.error;
    return "";
  };
  const approve = async () => {
    if (busy) return;
    if (self) { setDlg("self"); return; } // einzige Person im Personal: Pflichtkommentar ≥ 10
    if (!confirm(`${name}: ${monthLabel(month)} jetzt freigeben?\n\nDanach ist der Monat gesperrt; Nachträge sind nur nach Wiedereröffnung möglich.`)) return;
    setBusy(true);
    const e = await post("decide", { decision: "approve", comment: "" });
    setBusy(false);
    onDone(e ? { err: true, text: `${name}: ${e}` } : { text: `${name}: ${monthLabel(month)} freigegeben` });
  };
  const sub = (path, body, okText) => async text => {
    const e = await post(path, { ...body, comment: text });
    onDone(e ? { err: true, text: `${name}: ${e}` } : { text: okText });
    return e;
  };
  const canReopen = ["submitted", "approved", "closed"].includes(status);
  return (
    <span style={{ display: "inline-flex", gap: 6, flexWrap: "wrap" }}>
      {status === "submitted" && <>
        <button style={{ ...btn(true), padding: "4px 10px", fontSize: 12 }} disabled={busy} onClick={approve}><Check size={13} /> Freigeben</button>
        {!self && <button style={sBtn} disabled={busy} onClick={() => setDlg("reject")}><X size={13} /> Ablehnen</button>}</>}
      {canReopen && !self && <button style={sBtn} disabled={busy} onClick={() => setDlg("reopen")} title="Monat wieder öffnen"><RotateCcw size={13} /> {compact ? "Wiedereröffnen" : "Wiedereröffnen"}</button>}
      {dlg === "self" && <ReasonDialog title={`${monthLabel(month)} selbst freigeben`} text="Du bist die einzige Person im Personal. Die Begründung wird im Verlauf als Selbstfreigabe protokolliert; der Monat ist danach gesperrt." label="Begründung" confirmLabel="Freigeben" min={10}
        onSubmit={sub("decide", { decision: "approve" }, `${name}: ${monthLabel(month)} freigegeben`)} onClose={() => setDlg(null)} />}
      {dlg === "reject" && <ReasonDialog title={`${name}: ${monthLabel(month)} ablehnen`} text="Die Person sieht den Kommentar, korrigiert den Monat und reicht ihn neu ein." label="Kommentar" confirmLabel="Ablehnen" danger
        onSubmit={sub("decide", { decision: "reject" }, `${name}: ${monthLabel(month)} abgelehnt`)} onClose={() => setDlg(null)} />}
      {dlg === "reopen" && <ReasonDialog title={`${name}: ${monthLabel(month)} wiedereröffnen`} text="Der Monat wird wieder bearbeitbar; die Begründung wird im Verlauf protokolliert." label="Begründung" confirmLabel="Wiedereröffnen"
        onSubmit={sub("reopen", {}, `${name}: ${monthLabel(month)} wiedereröffnet`)} onClose={() => setDlg(null)} />}
    </span>);
}

export default function PersonalView() {
  const { me } = useServerMode();
  const [month, setMonth] = useState(prevMonth);
  const [team, setTeam] = useState(""); const [status, setStatus] = useState("");
  const [qIn, setQIn] = useState(""); const [q, setQ] = useState("");
  const [offset, setOffset] = useState(0);
  const [data, setData] = useState(null);
  const [pending, setPending] = useState(null);
  const [teams, setTeams] = useState([]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState(null);
  const [sel, setSel] = useState(null);

  const loadList = useCallback(async () => {
    setBusy(true);
    const p = new URLSearchParams({ month, limit: LIMIT, offset });
    if (team) p.set("team", team); if (status) p.set("status", status); if (q) p.set("q", q);
    setData(await call("GET", `/personal/overview?${p}`)); setBusy(false);
  }, [month, team, status, q, offset]);
  const loadPending = useCallback(async () => { setPending(await call("GET", "/approval/pending")); }, []);
  useEffect(() => { setData(null); loadList(); }, [loadList]);
  useEffect(() => { loadPending(); }, [loadPending]);
  useEffect(() => {   // Teamliste einmalig aus der Personenliste ableiten
    call("GET", "/personal/people").then(r => {
      const t = []; ((r && r.items) || []).forEach(p => (p.teams || []).forEach(x => { if (!t.some(y => y.id === x.id)) t.push({ id: x.id, name: x.name }); }));
      setTeams(t.sort((a, b) => a.name.localeCompare(b.name, "de")));
    });
  }, []);

  if (me && !me.is_hr) return <Err>Keine Berechtigung für diesen Bereich.</Err>;
  const done = m => { setMsg(m); loadList(); loadPending(); };
  const refresh = () => { setMsg(null); loadList(); loadPending(); };
  const filt = fn => v => { fn(v); setOffset(0); };

  if (sel) return <PersonDetail person={sel} month={month} onMonth={setMonth} onBack={() => setSel(null)} onChanged={() => { loadList(); loadPending(); }}
    renderActions={({ status: st, reload }) => <ApprovalButtons userId={sel.id} name={sel.name} month={month} status={st} onDone={m => { setMsg(m); reload(); loadPending(); }} />} />;

  const items = pending && !pending.error ? pending.items || [] : null;
  const total = data && !data.error ? data.total || 0 : 0, people = data && !data.error ? data.people || [] : null;
  const open = p => setSel({ id: p.id, name: p.name, teams: p.teams, team: p.team, self: p.self });

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 12 }}>
        <UserCog size={20} /><h2 style={{ margin: 0, fontSize: 18, flex: 1 }}>Personal</h2>
        {items && <span className={`zp-pill ${items.length ? "changed" : "synced"}`} style={{ fontSize: 12 }}>{items.length} offene Freigabe{items.length === 1 ? "" : "n"}</span>}
      </div>
      {msg && <div role="status" style={{ fontSize: 13, marginBottom: 12, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</div>}

      <Box title="Offene Freigaben" sub="Eingereichte Monate. Eigene Monate nur, wenn du die einzige Person im Personal bist (Begründung Pflicht)."
        right={<RefreshBtn onClick={refresh} busy={busy} />}>
        {!items ? (pending && pending.error ? <Err>{friendly(pending.error)}</Err> : <div style={{ color: "var(--text3)", fontSize: 12 }}>Lade…</div>)
          : !items.length ? <Empty>Keine offenen Freigaben.</Empty>
          : items.map((it, i) => (
            <div key={`${it.user_id}-${it.month}`} style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap", padding: "7px 0", borderTop: i ? "1px solid var(--line)" : "none" }}>
              <button onClick={() => { setMonth(it.month); setSel({ id: it.user_id, name: it.name }); }} style={{ all: "unset", cursor: "pointer", fontWeight: 600, fontSize: 13, minWidth: 160 }} title="Details öffnen">{it.name}</button>
              <span style={{ fontSize: 12.5, minWidth: 110 }}>{monthLabel(it.month)}</span>
              <span style={{ fontSize: 12, color: "var(--text2)", flex: 1 }}>eingereicht {when(it.submitted_at)}</span>
              <ApprovalButtons userId={it.user_id} name={it.name} month={it.month} status="submitted" onDone={done} />
            </div>))}
      </Box>

      <div style={{ margin: "12px 0" }}><LeavePendingPanel /></div>

      <Box title="Alle Personen" sub="Monatsübersicht mit Status, Salden und Hinweisen; Klick auf eine Zeile öffnet die Details. CSV = aktuelle Auswahl (kein Lohn-Export)."
        right={<div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <button style={sBtn} disabled={!people?.length} onClick={() => peopleOverviewCsv(people, month, "personal")} title="Übersicht als CSV"><Download size={13} /> CSV</button>
          <MonthNav month={month} onChange={filt(setMonth)} /></div>}>
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap", marginBottom: 10 }}>
          <select style={{ ...inpS, width: "auto" }} value={team} onChange={e => filt(setTeam)(e.target.value)} aria-label="Team">
            <option value="">Alle Teams</option><option value="none">Ohne Team</option>{teams.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}</select>
          <select style={{ ...inpS, width: "auto" }} value={status} onChange={e => filt(setStatus)(e.target.value)} aria-label="Status">
            <option value="">Alle Status</option>{["open", "submitted", "approved", "rejected", "closed"].map(s => <option key={s} value={s}>{STATUS[s][0]}</option>)}</select>
          <form style={{ display: "flex", gap: 6 }} onSubmit={e => { e.preventDefault(); setQ(qIn.trim()); setOffset(0); }}>
            <input style={{ ...inpS, width: 200 }} placeholder="Name suchen" value={qIn} onChange={e => setQIn(e.target.value)} aria-label="Namenssuche" />
            <button type="submit" style={sBtn} title="Suchen"><Search size={13} /></button></form>
        </div>
        {!data ? <div style={{ color: "var(--text3)", fontSize: 12 }}>Lade…</div>
          : data.error ? <Err>{friendly(data.error)}</Err>
          : !people.length ? <Empty>Keine Personen für diese Auswahl.</Empty>
          : <>
              <PeopleTable people={people} onOpen={open} extra={{ head: "Aktion", cell: p => <ApprovalButtons userId={p.id} name={p.name} month={month} status={p.status} onDone={done} compact /> }} />
              <div style={{ display: "flex", alignItems: "center", gap: 10, marginTop: 10, fontSize: 12, color: "var(--text2)" }}>
                <span>{offset + 1}–{offset + people.length} von {total}</span><div style={{ flex: 1 }} />
                <button style={sBtn} disabled={offset <= 0 || busy} onClick={() => setOffset(Math.max(0, offset - LIMIT))}>Zurück</button>
                <button style={sBtn} disabled={offset + LIMIT >= total || busy} onClick={() => setOffset(offset + LIMIT)}>Weiter</button>
              </div>
            </>}
      </Box>
    </div>);
}
