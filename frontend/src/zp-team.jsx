/* ═══════════════════════════════════════════════════════════════════
   Bereich „Team“ (Teamleitung, nur Servermodus): Übersicht der Teammitglieder je Monat,
   Personen-Detail. Teamleitung gibt NICHT frei (nur Personal) – nur lesend.
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useCallback } from "react";
import { Users, ShieldCheck } from "lucide-react";
import { call, Box, inpS, useServerMode } from "./zp-ui.jsx";
import { MonthNav, RefreshBtn, PeopleTable, PersonDetail, Note, Empty, Err, prevMonth, friendly } from "./zp-people.jsx";

export default function TeamView() {
  const { me } = useServerMode();
  const [month, setMonth] = useState(prevMonth);
  const [team, setTeam] = useState("");
  const [members, setMembers] = useState(null);   // { items } | { error }
  const [data, setData] = useState(null);
  const [busy, setBusy] = useState(false);
  const [sel, setSel] = useState(null);

  const loadMembers = useCallback(async () => { setMembers(await call("GET", "/team/members")); }, []);
  useEffect(() => { loadMembers(); }, [loadMembers]);

  const load = useCallback(async () => {
    setBusy(true);
    const r = await call("GET", `/team/overview?month=${month}${team ? `&team=${team}` : ""}`);
    setData(r); setBusy(false);
  }, [month, team]);
  useEffect(() => { setData(null); load(); }, [load]);

  if (me && !me.is_lead) return <Err>Keine Berechtigung für diesen Bereich.</Err>;
  const refresh = () => { loadMembers(); load(); };

  if (sel) return <PersonDetail person={sel} month={month} onMonth={setMonth} onBack={() => setSel(null)} onChanged={load}
    renderActions={() => <span style={{ display: "inline-flex", alignItems: "center", gap: 5 }}><ShieldCheck size={14} /> Freigabe durch Personal</span>} />;

  const teams = [];
  ((members && members.items) || []).forEach(m => { if (m.team_id && !teams.some(t => t.id === m.team_id)) teams.push({ id: m.team_id, name: m.team }); });
  const people = data && !data.error ? data.people || [] : null;
  const noTeam = members && !members.error && !(members.items || []).length;

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 12 }}>
        <Users size={20} /><h2 style={{ margin: 0, fontSize: 18, flex: 1 }}>Team</h2>
      </div>
      <Note icon={<ShieldCheck size={15} />}>Freigabe durch Personal – Teamleitung sieht Monate, Salden und Buchungen, gibt aber nicht frei.</Note>
      <Box title="Teammitglieder" sub="Soll bis heute, Ist, Saldo und Konto aus der Zeiterfassung der Personen; Klick auf eine Zeile öffnet die Details."
        right={<div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          {teams.length > 1 && <select style={{ ...inpS, width: "auto" }} value={team} onChange={e => setTeam(e.target.value)} aria-label="Team filtern">
            <option value="">Alle Teams</option>{teams.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}</select>}
          <MonthNav month={month} onChange={setMonth} /><RefreshBtn onClick={refresh} busy={busy} /></div>}>
        {noTeam ? <Empty>Dir ist noch kein Team zugeordnet.</Empty>
          : !data ? <div style={{ color: "var(--text3)", fontSize: 12 }}>Lade…</div>
          : data.error ? <Err>{friendly(data.error)}</Err>
          : !people.length ? <Empty>Keine Teammitglieder gefunden.</Empty>
          : <>
              <PeopleTable people={people} onOpen={setSel} />
              {data.truncated && <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 8 }}>Angezeigt werden höchstens 200 Personen; bitte nach Team filtern.</div>}
            </>}
      </Box>
    </div>);
}
