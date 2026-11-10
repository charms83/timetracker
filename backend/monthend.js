/* ═══════════════════════════════════════════════════════════════════
   Monatsende & Nachtragen (v6.15.0, v6.26: „Lücken füllen“ → „Nachtragen“)
   ───────────────────────────────────────────────────────────────────
   • Hinweis 5 Arbeitstage vor Monatsende: „Zeiten jetzt eintragen“.
     Arbeitstage = Mo–Fr ohne eingetragene Feiertage. Wochenende zählt
     nicht – auch wenn dort manchmal gearbeitet wird.
   • In den ersten 3 Arbeitstagen des neuen Monats: Vormonat noch offen?
   • Lücken: Tage (ab Buchungsbeginn, ohne heute), an denen weniger als
     das Soll gebucht ist (Abwesenheiten abgezogen). „Passt so“ merkt sich
     den Tag (z. B. Gleitzeit-Abbau) und fragt nicht wieder.
   • v6.26: Pro Tag „am Mac von–bis“, gebucht, noch einzutragen. Liegt das
     Arbeitszeitkonto (bis gestern) im Plus und deckt das Fehlende, ist
     Nachtragen nur ein Hinweis (optional) – kein Zwang.
   Nur lokale Abfragen, kein Netz.
═══════════════════════════════════════════════════════════════════ */
module.exports = function registerMonthEnd(app, ctx) {
  const { one, all, run, getSettings, localDateOf, balanceAt } = ctx;
  const DAYKEYS = ["target_sun", "target_mon", "target_tue", "target_wed", "target_thu", "target_fri", "target_sat"];
  const pad = n => String(n).padStart(2, "0");
  const iso = d => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  const dayOf = s => new Date(s + "T12:00:00");
  const MONTHS = ["Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"];

  const absOn = date => all("SELECT type, hours FROM absences WHERE date_from<=? AND date_to>=?", date, date);
  const isHoliday = date => absOn(date).some(a => a.type === "feiertag");
  const isWorkday = date => { const w = dayOf(date).getDay(); return w >= 1 && w <= 5 && !isHoliday(date); };
  const okList = () => { try { return JSON.parse(getSettings().gapfill_ok || "[]"); } catch (_) { return []; } };
  const since = () => { const s = getSettings().tempo_push_since; return /^\d{4}-\d{2}-\d{2}$/.test(s || "") ? s : "2000-01-01"; };

  /* Arbeitstage im Monat ab (inkl.) from */
  function workdaysLeft(from) {
    const d = dayOf(from), m = d.getMonth(), out = [];
    while (d.getMonth() === m) { const s = iso(d); if (isWorkday(s)) out.push(s); d.setDate(d.getDate() + 1); }
    return out;
  }

  /* Am Mac: von–bis und Dauer (aus der Anwesenheit, sonst Arbeitstag) */
  const hm = m => `${pad(Math.floor(m / 60))}:${pad(m % 60)}`;
  function macOf(date) {
    try {
      const ov = ctx.hooks?.presence?.dayOverview(date); if (!ov) return {};
      const p = ov.presence || [];
      return { mac_from: p.length ? hm(p[0].s) : ov.work ? hm(ov.work.s) : null, mac_to: p.length ? hm(p[p.length - 1].e) : ov.work ? hm(ov.work.e) : null,
        mac_h: p.length ? Math.round(ov.presence_minutes / 60 * 100) / 100 : null, open_h: Math.round((ov.gap_minutes || 0) / 60 * 100) / 100 };
    } catch (_) { return {}; }
  }
  /* Tage mit fehlenden Stunden in [from, to] */
  function missingDays(from, to) {
    const s = getSettings(), ok = new Set(okList()), out = [];
    const d = dayOf(from);
    for (; iso(d) <= to; d.setDate(d.getDate() + 1)) {
      const date = iso(d), target = parseFloat(s[DAYKEYS[d.getDay()]]) || 0;
      if (!target || isHoliday(date)) continue;
      const absH = absOn(date).reduce((a, x) => a + (x.hours != null ? x.hours : target), 0);
      const want = Math.max(0, target - absH);
      if (want <= 0) continue;
      const booked = (one("SELECT COALESCE(SUM(duration_seconds),0) AS s FROM time_entries WHERE date=? AND end_time IS NOT NULL AND COALESCE(is_absence,0)=0", date).s || 0) / 3600;
      const miss = Math.round((want - booked) * 100) / 100;
      if (miss >= 0.25) out.push({ date, target_h: want, booked_h: Math.round(booked * 100) / 100, missing_h: miss, ok: ok.has(date), ...macOf(date) });
    }
    return out;
  }

  function status() {
    const today = localDateOf(Date.now()), t = dayOf(today);
    const start = (a, b) => a > b ? a : b;
    const monthStart = today.slice(0, 8) + "01";
    const left = workdaysLeft(today);
    const yesterday = iso(new Date(t.getTime() - 864e5));
    // offen: dieser Monat und die letzten 14 Tage (auch über den Monatswechsel)
    const from14 = iso(new Date(t.getTime() - 14 * 864e5)), openFrom = start(from14 < monthStart ? from14 : monthStart, since());
    const days = yesterday >= openFrom ? missingDays(openFrom, yesterday) : [];
    const res = {
      month: today.slice(0, 7), month_name: MONTHS[t.getMonth()],
      last_workday: left[left.length - 1] || null, workdays_left: left.length,
      remind: left.length >= 1 && left.length <= 5,
      days, open_days: days.filter(x => !x.ok), missing_h: Math.round(days.filter(x => !x.ok).reduce((a, x) => a + x.missing_h, 0) * 100) / 100,
      prev: null,
    };
    // Konto bis gestern: deckt das Plus das Fehlende, ist Nachtragen nur optional
    try { const b = balanceAt ? balanceAt(yesterday) : null; res.balance_h = b; res.optional = b != null && res.missing_h > 0 && b >= res.missing_h; } catch (_) { res.balance_h = null; res.optional = false; }
    // Neuer Monat: in den ersten 3 Arbeitstagen den Vormonat nachfragen
    const passed = [];
    for (const d = dayOf(monthStart); iso(d) <= today; d.setDate(d.getDate() + 1)) if (isWorkday(iso(d))) passed.push(iso(d));
    if (passed.length <= 3) {
      const p = new Date(t.getFullYear(), t.getMonth() - 1, 1), pStart = iso(p), pEnd = iso(new Date(t.getFullYear(), t.getMonth(), 0));
      if (pEnd >= since()) {
        const pd = missingDays(start(pStart, since()), pEnd).filter(x => !x.ok);
        if (pd.length) res.prev = { month: pStart.slice(0, 7), month_name: MONTHS[p.getMonth()], days: pd, missing_h: Math.round(pd.reduce((a, x) => a + x.missing_h, 0) * 100) / 100 };
      }
    }
    return res;
  }

  app.get("/api/month-end", (_, res) => res.json(status()));
  /* Tag als „passt so“ markieren (oder zurücknehmen) */
  app.post("/api/month-end/ok", (req, res) => {
    const date = String(req.body?.date || ""); if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return res.status(400).json({ error: "Datum fehlt" });
    const l = new Set(okList()); req.body?.undo ? l.delete(date) : l.add(date);
    run("INSERT OR REPLACE INTO settings (key,value) VALUES ('gapfill_ok',?)", JSON.stringify([...l].sort().slice(-400)));
    res.json({ ok: true });
  });

  /* ══ Vorschläge zum Nachtragen ══════════════════════════════════════
     Für jede offene Stelle des Tages (Termin, SSH, Lücke am Mac, Rest)
     die wahrscheinlichsten Tickets – aus den eigenen letzten Buchungen:
       Termin gleichen Titels früher gebucht      +6
       SSH auf denselben Server früher gebucht    +6
       direkt davor / danach gebucht (≤ 30 min)   +3 / +2
       sonst an diesem Wochentag um diese Zeit    +2
       viel gebucht in den letzten 14 Tagen       bis +2
     Nichts wird automatisch gebucht – nur vorgeschlagen. */
  const { hooks } = ctx;
  const minOf = iso => { const d = new Date(iso); return d.getHours() * 60 + d.getMinutes(); };
  const hhmm = m => `${pad(Math.floor(m / 60))}:${pad(m % 60)}`;
  const ENTRY = `SELECT e.task_id, e.project_id, e.comment, t.jira_ticket, t.title AS task_title, p.name AS project_name
    FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id LEFT JOIN projects p ON p.id=e.project_id`;
  const keyOf = e => e.task_id ? "t" + e.task_id : "p" + e.project_id;

  /* ── Lernen aus den eigenen Entscheidungen (nur lokal, nur diese Person) ──
     key: „ev:<Termintitel>“, „ssh:<Server>“, „slot:<Wochentag>:<Stunde>“ → gewähltes Ticket/Projekt, Anzahl */
  let fbReady = false;
  function fbEnsure() { if (fbReady) return; run(`CREATE TABLE IF NOT EXISTS suggest_feedback (key TEXT, task_id INTEGER, project_id INTEGER, n INTEGER DEFAULT 0, last TEXT, PRIMARY KEY (key, task_id, project_id))`); fbReady = true; }
  function learn(key, task_id, project_id) {
    fbEnsure(); if (!key || !project_id) return;
    run(`INSERT INTO suggest_feedback (key,task_id,project_id,n,last) VALUES (?,?,?,1,datetime('now')) ON CONFLICT(key,task_id,project_id) DO UPDATE SET n=n+1, last=datetime('now')`, String(key).slice(0, 200), task_id || 0, project_id);
  }
  // Früher gewählt: nur ein leichter Schubs (v6.30) – zählt erst ab 2× gleicher Wahl, nur aus den letzten 90 Tagen
  const learned = key => { fbEnsure(); return all("SELECT task_id, project_id, n FROM suggest_feedback WHERE key=? AND n>=2 AND last>=datetime('now','-90 days') ORDER BY n DESC, last DESC LIMIT 2", String(key).slice(0, 200)); };
  const slotKey = (date, m) => `slot:${dayOf(date).getDay()}:${Math.floor(m / 60)}`;

  function suggest(date) {
    const ov = hooks.presence?.dayOverview(date); if (!ov) return { date, items: [] };
    const from14 = iso(new Date(dayOf(date).getTime() - 14 * 864e5));
    const recent = all(`SELECT e.task_id, e.project_id, SUM(e.duration_seconds) AS secs FROM time_entries e WHERE e.date>=? AND e.date<=? AND e.project_id IS NOT NULL GROUP BY e.task_id, e.project_id ORDER BY secs DESC LIMIT 12`, from14, date);
    const maxSecs = recent[0]?.secs || 1;
    const info = new Map();
    const cand = () => new Map();
    const bump = (m, e, pts, why) => { if (!e || !e.project_id) return; const k = keyOf(e); if (!info.has(k)) info.set(k, e); const c = m.get(k) || { score: 0, why: [] }; c.score += pts; if (why && !c.why.includes(why)) c.why.push(why); m.set(k, c); };
    const entryAt = (where, ...args) => one(`${ENTRY} WHERE ${where} ORDER BY e.date DESC, e.start_time DESC LIMIT 1`, ...args);
    const wd = dayOf(date).getDay();
    const common = (m, s, e) => {
      const before = ov.entries.filter(x => x.e <= s + 2 && s - x.e <= 30).pop(), after = ov.entries.find(x => x.s >= e - 2 && x.s - e <= 30);
      if (before) bump(m, entryAt("e.id=?", before.id), 3, `davor ${hhmm(before.s)}–${hhmm(before.e)}`);
      if (after) bump(m, entryAt("e.id=?", after.id), 2, `danach ab ${hhmm(after.s)}`);
      // gleicher Wochentag, ähnliche Uhrzeit, letzte 5 Wochen
      for (let w = 1; w <= 5; w++) {
        const d2 = iso(new Date(dayOf(date).getTime() - w * 7 * 864e5));
        for (const x of all(`${ENTRY.replace("SELECT ", "SELECT e.start_time, e.end_time, ")} WHERE e.date=? AND e.end_time IS NOT NULL`, d2))
          if (Math.min(minOf(x.end_time), e) - Math.max(minOf(x.start_time), s) >= 15) bump(m, x, 2 / w + 0.5, "sonst " + ["sonntags", "montags", "dienstags", "mittwochs", "donnerstags", "freitags", "samstags"][wd] + " um diese Zeit");
      }
      for (const r of recent) bump(m, one(`${ENTRY} WHERE e.date>=? AND ${r.task_id ? "e.task_id=?" : "e.task_id IS NULL AND e.project_id=?"} ORDER BY e.date DESC LIMIT 1`, from14, r.task_id || r.project_id), 2 * r.secs / maxSecs, "zuletzt oft gebucht");
      learnedInto(m, slotKey(date, s), 1, "früher hier gewählt");
    };
    const learnedInto = (m, key, w, why) => { for (const l of learned(key)) bump(m, one(`${ENTRY} WHERE e.project_id=? AND ${l.task_id ? "e.task_id=?" : "e.task_id IS NULL"} ORDER BY e.date DESC LIMIT 1`, ...(l.task_id ? [l.project_id, l.task_id] : [l.project_id])), w * Math.min(4, l.n) / 4, why); };
    const top = m => [...m.entries()].sort((a, b) => b[1].score - a[1].score).slice(0, 4).map(([k, c]) => { const e = info.get(k);
      return { task_id: e.task_id || null, project_id: e.project_id, jira_ticket: e.jira_ticket || null, label: `${e.jira_ticket ? e.jira_ticket + " " : ""}${e.task_title || e.project_name}`, project_name: e.project_name, last_comment: e.comment || "", why: c.why.slice(0, 2), score: Math.round(c.score * 10) / 10 }; });
    const items = [];
    // Vorschläge überschneiden sich nie: was gebucht, pausiert oder schon vorgeschlagen ist, wird herausgeschnitten
    const taken = [...ov.entries, ...(ov.pauses || [])].map(x => [x.s, x.e]);
    const free = (s0, e0) => { let parts = [[s0, e0]]; for (const [bs, be] of taken) parts = parts.flatMap(([a, b]) => be <= a || bs >= b ? [[a, b]] : [[a, Math.min(b, bs)], [Math.max(a, be), b]].filter(([x, y]) => y - x > 0)); return parts.filter(([a, b]) => b - a >= 10); };
    for (const ev of [...ov.events].sort((a, b) => a.s - b.s)) {
      const m = cand();
      const prev = one(`SELECT ms.entry_id FROM calendar_events c JOIN meeting_state ms ON ms.key IN ('cal:'||c.uid, c.uid) AND ms.status='booked' AND ms.entry_id IS NOT NULL WHERE c.title=? AND c.date<? ORDER BY c.date DESC LIMIT 1`, ev.title, date);
      if (prev) bump(m, entryAt("e.id=?", prev.entry_id), 6, "Termin früher so gebucht");
      learnedInto(m, "ev:" + ev.title, 3, "früher für diesen Termin gewählt");
      const byText = entryAt("e.comment=? AND e.date<? AND e.project_id IS NOT NULL", ev.title, date); if (byText) bump(m, byText, 5, "gleicher Titel früher gebucht");
      for (const [a, b] of free(ev.s, ev.e)) { const m2 = new Map([...m].map(([k, v]) => [k, { score: v.score, why: [...v.why] }])); common(m2, a, b);
        items.push({ kind: "event", ref: ev.uid, key: "ev:" + ev.title, s: a, e: b, title: ev.title, comment: ev.title, candidates: top(m2) }); taken.push([a, b]); }
    }
    for (const x of [...ov.ssh].sort((a, b) => a.s - b.s)) {
      const m = cand();
      const prev = one(`SELECT time_entry_id FROM ssh_sessions WHERE (hostname=? OR target=?) AND time_entry_id IS NOT NULL AND date<? ORDER BY date DESC LIMIT 1`, x.server, x.server, date);
      if (prev) bump(m, entryAt("e.id=?", prev.time_entry_id), 6, `${x.server} früher so gebucht`);
      learnedInto(m, "ssh:" + x.server, 3, `früher für ${x.server} gewählt`);
      for (const [a, b] of free(x.s, x.e)) { const m2 = new Map([...m].map(([k, v]) => [k, { score: v.score, why: [...v.why] }])); common(m2, a, b);
        items.push({ kind: "ssh", ref: x.id, key: "ssh:" + x.server, s: a, e: b, title: `SSH ${x.server}`, comment: `SSH ${x.server}`, candidates: top(m2) }); taken.push([a, b]); }
    }
    const busy = items.map(x => [x.s, x.e]);
    // Lücken ohne die Zeiten, die schon als Termin/SSH vorgeschlagen sind (keine doppelten, überlappenden Vorschläge)
    const pieces = [];
    for (const g of ov.gaps) {
      let parts = [[g.s, g.e]];
      for (const [bs, be] of busy) parts = parts.flatMap(([a, b]) => be <= a || bs >= b ? [[a, b]] : [[a, Math.min(b, bs)], [Math.max(a, be), b]].filter(([x, y]) => y - x > 0));
      for (const [a, b] of parts) if (b - a >= 10) pieces.push({ s: a, e: b });
    }
    for (const g of pieces) {
      const m = cand(); common(m, g.s, g.e);
      items.push({ kind: "gap", key: slotKey(date, g.s), s: g.s, e: g.e, title: ov.presence.length ? "Am Mac, nichts gebucht" : "Zwischen Buchungen, nichts gebucht", comment: "", candidates: top(m) });
    }
    // Rest: fehlt danach noch Zeit (z. B. keine Mac-Daten an alten Tagen) → Block nach der letzten Buchung
    const target = parseFloat(getSettings()[DAYKEYS[wd]]) || 0;
    const absH = absOn(date).reduce((a, x) => a + (x.hours != null ? x.hours : target), 0);
    const covered = ov.booked_minutes + items.reduce((a, i) => a + i.e - i.s, 0);
    const rest = Math.round(Math.max(0, target - absH) * 60 - covered);
    if (rest >= 15) {
      // Keine Mac-Daten/Termine: typischen Tag aus den letzten 6 gleichen Wochentagen vorschlagen
      const pat = !ov.presence.length && !ov.entries.length ? typicalDay(date, Math.round(rest / 15) * 15) : [];
      for (const b0 of pat) for (const [ps, pe] of free(b0.s, b0.e)) { const b = { ...b0, s: ps, e: pe }; taken.push([ps, pe]); const m = cand(); bump(m, b.entry, 4, `sonst ${["sonntags", "montags", "dienstags", "mittwochs", "donnerstags", "freitags", "samstags"][wd]} ${hhmm(b.s)}–${hhmm(b.e)}`); common(m, b.s, b.e); items.push({ kind: "pattern", key: slotKey(date, b.s), s: b.s, e: b.e, title: "Typisch für diesen Wochentag", comment: "", candidates: top(m) }); }
      const left = rest - pat.reduce((a, b) => a + b.e - b.s, 0);
      if (left >= 15) {
        const ends = [...ov.entries.map(x => x.e), ...pat.map(b => b.e), ...items.map(b => b.e)];
        const s = ends.length ? Math.max(...ends) : 9 * 60, e = Math.min(24 * 60 - 1, s + Math.round(left / 15) * 15);
        const m = cand(); common(m, s, e);
        items.push({ kind: "rest", key: slotKey(date, s), s, e, title: "Restzeit bis zum Soll", comment: "", candidates: top(m) });
      }
    }
    return { date, items: items.sort((a, b) => a.s - b.s) };
  }
  /* Typischer Tag: je 15-min-Raster das häufigste Ticket der letzten 6 gleichen Wochentage → Blöcke, auf need Minuten begrenzt */
  function typicalDay(date, need) {
    const slots = new Map();   // slot → Map(key → {n, entry})
    for (let w = 1; w <= 6; w++) {
      const d2 = iso(new Date(dayOf(date).getTime() - w * 7 * 864e5));
      for (const x of all(`${ENTRY.replace("SELECT ", "SELECT e.id, e.start_time, e.end_time, ")} WHERE e.date=? AND e.end_time IS NOT NULL AND e.project_id IS NOT NULL AND COALESCE(e.is_absence,0)=0 AND COALESCE(t.status,'open') NOT IN ('done','closed')`, d2)) {
        for (let q = Math.floor(minOf(x.start_time) / 15); q < Math.ceil(minOf(x.end_time) / 15); q++) {
          const k = keyOf(x), sm = slots.get(q) || new Map(), c = sm.get(k) || { n: 0, entry: x }; c.n++; sm.set(k, c); slots.set(q, sm);
        }
      }
    }
    const best = [...slots.entries()].map(([q, sm]) => { const [k, c] = [...sm.entries()].sort((a, b) => b[1].n - a[1].n)[0]; return { q, k, n: c.n, entry: c.entry }; })
      .filter(x => x.n >= 3).sort((a, b) => a.q - b.q);   // mind. 3 der letzten 6 gleichen Wochentage – keine Einzelfälle
    const blocks = [];
    for (const b of best) { const l = blocks[blocks.length - 1]; if (l && l.k === b.k && l.qe === b.q) l.qe = b.q + 1; else blocks.push({ k: b.k, qs: b.q, qe: b.q + 1, entry: b.entry }); }
    let left = need; const out = [];
    for (const b of blocks) { if (left < 15) break; if ((b.qe - b.qs) * 15 < 30) continue; const len = Math.min((b.qe - b.qs) * 15, left); if (len < 15) continue; out.push({ s: b.qs * 15, e: b.qs * 15 + len, entry: b.entry }); left -= len; }
    return out;
  }

  /* Gebucht aus einem Vorschlag: Termin/SSH als erledigt verknüpfen und die Wahl merken */
  app.post("/api/gap-suggest/learn", (req, res) => {
    const b = req.body || {}, entry = parseInt(b.entry_id) || null;
    try {
      if (b.kind === "ssh" && b.ref && entry) run("UPDATE ssh_sessions SET time_entry_id=? WHERE id=? AND time_entry_id IS NULL", entry, parseInt(b.ref));
      if (b.kind === "event" && b.ref && entry) run("INSERT OR REPLACE INTO meeting_state (key,status,entry_id) VALUES (?,'booked',?)", "cal:" + b.ref, entry);
    } catch (_) {}
    learn(b.key, parseInt(b.task_id) || null, parseInt(b.project_id) || null);
    if (b.slot) learn(b.slot, parseInt(b.task_id) || null, parseInt(b.project_id) || null);
    res.json({ ok: true });
  });

  /* „Jetzt vermutlich“: was gerade wahrscheinlich dran ist – laufender Termin, offene SSH-Sitzung, Gewohnheit */
  function suggestNow() {
    const date = localDateOf(Date.now()), now = new Date(), nm = now.getHours() * 60 + now.getMinutes();
    const ov = hooks.presence?.dayOverview(date); const m = new Map(); info.clear();
    let ctxTitle = null;
    const ev = ov?.events.find(e => e.s <= nm + 5 && e.e > nm);
    if (ev) { ctxTitle = ev.title; learnedIntoNow(m, "ev:" + ev.title, 3, `Termin „${ev.title}“ läuft`);
      const byText = one(`${ENTRY} WHERE e.comment=? AND e.project_id IS NOT NULL ORDER BY e.date DESC LIMIT 1`, ev.title); if (byText) bumpNow(m, byText, 6, `Termin „${ev.title}“ läuft`); }
    const sh = ov?.ssh.find(x => x.s <= nm && x.e >= nm - 2);
    if (sh) { ctxTitle = ctxTitle || `SSH ${sh.server}`; learnedIntoNow(m, "ssh:" + sh.server, 3, `SSH ${sh.server} offen`);
      const prev = one(`SELECT time_entry_id FROM ssh_sessions WHERE (hostname=? OR target=?) AND time_entry_id IS NOT NULL ORDER BY date DESC LIMIT 1`, sh.server, sh.server);
      if (prev) bumpNow(m, one(`${ENTRY} WHERE e.id=?`, prev.time_entry_id), 6, `SSH ${sh.server} offen`); }
    learnedIntoNow(m, slotKey(date, nm), 1, "früher hier gewählt");
    for (let w = 1; w <= 5; w++) {
      const d2 = iso(new Date(dayOf(date).getTime() - w * 7 * 864e5));
      for (const x of all(`${ENTRY.replace("SELECT ", "SELECT e.start_time, e.end_time, ")} WHERE e.date=? AND e.end_time IS NOT NULL AND e.project_id IS NOT NULL`, d2))
        if (minOf(x.start_time) <= nm && minOf(x.end_time) > nm) bumpNow(m, x, 2 / w + 0.5, "sonst um diese Zeit");
    }
    const last = one(`${ENTRY} WHERE e.date=? AND e.project_id IS NOT NULL ORDER BY e.start_time DESC LIMIT 1`, date); if (last) bumpNow(m, last, 1.5, "zuletzt heute");
    const list = [...m.entries()].sort((a, b) => b[1].score - a[1].score).slice(0, 3).map(([k, c]) => { const e = info.get(k);
      return { task_id: e.task_id || null, project_id: e.project_id, jira_ticket: e.jira_ticket || null, label: `${e.jira_ticket ? e.jira_ticket + " " : ""}${e.task_title || e.project_name}`, comment: ctxTitle || e.comment || "", why: c.why.slice(0, 2), score: Math.round(c.score * 10) / 10, key: slotKey(date, nm) }; });
    return { context: ctxTitle, items: list.filter(x => x.score >= 2) };
  }
  const info = new Map();
  const bumpNow = (m, e, pts, why) => { if (!e || !e.project_id) return; const k = keyOf(e); if (!info.has(k)) info.set(k, e); const c = m.get(k) || { score: 0, why: [] }; c.score += pts; if (why && !c.why.includes(why)) c.why.push(why); m.set(k, c); };
  const learnedIntoNow = (m, key, w, why) => { for (const l of learned(key)) bumpNow(m, one(`${ENTRY} WHERE e.project_id=? AND ${l.task_id ? "e.task_id=?" : "e.task_id IS NULL"} ORDER BY e.date DESC LIMIT 1`, ...(l.task_id ? [l.project_id, l.task_id] : [l.project_id])), w * Math.min(4, l.n) / 4, why); };
  app.get("/api/suggest-now", (_, res) => { try { res.json(suggestNow()); } catch (e) { res.status(500).json({ error: e.message }); } });

  app.get("/api/gap-suggest", (req, res) => { const d = String(req.query.date || ""); if (!/^\d{4}-\d{2}-\d{2}$/.test(d)) return res.status(400).json({ error: "Datum fehlt" }); res.json(suggest(d)); });

  return { status, suggest, suggestNow, learn };
};
