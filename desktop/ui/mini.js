"use strict";
/* Zeitprotokoll – Schnellfenster */
window.addEventListener("error", e => console.error("JS-Fehler:", e.message, e.filename + ":" + e.lineno));
const API = "http://127.0.0.1:3747/api";
const $ = id => document.getElementById(id);
const bridge = window.zp || { quit(){}, hide(){}, openMain(){ window.open("http://127.0.0.1:3747/"); }, changed(){}, onReminder(){}, onRefresh(){} };

const api = (m, p, b) => fetch(API + p, { method: m, headers: { "Content-Type": "application/json" }, body: b ? JSON.stringify(b) : undefined })
  .then(async r => { const j = await r.json().catch(() => null); return r.ok ? j : (j && j.error ? { error: j.error } : null); }).catch(() => null);

// ── Helfer ──────────────────────────────────────────────────────
const pad = n => String(n).padStart(2, "0");
const localDate = d => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
const addDays = (s, n) => { const d = new Date(s + "T12:00"); d.setDate(d.getDate() + n); return localDate(d); };
const hm = iso => { if (!iso) return "…"; const d = new Date(iso); return `${pad(d.getHours())}:${pad(d.getMinutes())}`; };
const dur = s => { s = Math.max(0, Math.round(s)); const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60); return h ? `${h} h ${m} min` : `${m} min`; };
const clock = s => { s = Math.max(0, Math.floor(s)); return `${Math.floor(s / 3600)}:${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`; };
const esc = s => String(s ?? "").replace(/[&<>"]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
const color = id => { if (!id) return "#8e8e93"; const hues = [220, 4, 140, 32, 280, 190, 330, 95, 250, 15]; return `hsl(${hues[id % hues.length]} 62% 52%)`; };
const store = { get(k, d) { try { return JSON.parse(localStorage.getItem(k)) ?? d; } catch (_) { return d; } }, set(k, v) { try { localStorage.setItem(k, JSON.stringify(v)); } catch (_) {} } };

// ── Zustand ─────────────────────────────────────────────────────
let S = { day: localDate(new Date()), projects: [], tasks: {}, timer: { active: false }, pause: { active: false }, entries: [], pauses: [], ssh: [], recent: [] };

async function loadTasks(pid) {
  if (!pid) return [];
  const r = await api("GET", `/tasks?projectId=${pid}`);
  S.tasks[pid] = Array.isArray(r) ? r : [];   // immer frisch laden, nie zwischenspeichern
  return S.tasks[pid];
}

let loadSeq = 0, lastToday = localDate(new Date());
async function load() {
  // Mitternacht: wenn „heute“ angezeigt wurde, auf den neuen Tag springen
  const tNow = localDate(new Date());
  if (tNow !== lastToday) { if (S.day === lastToday) S.day = tNow; lastToday = tNow; }
  const seq = ++loadSeq;
  const d = S.day, from = addDays(d, -1), to = addDays(d, 1);
  const [timer, pause, projects, entries, wd0, wd1, ssh, recent] = await Promise.all([
    api("GET", "/timer/status"), api("GET", "/pause/status"),
    S.projects.length && Date.now() - (S.projAt || 0) < 300e3 ? Promise.resolve(S.projects) : api("GET", "/projects").then(r => { S.projAt = Date.now(); return r; }),
    api("GET", `/time-entries?from=${from}&to=${to}`),
    api("GET", `/workday?date=${d}`), api("GET", `/workday?date=${to}`),
    api("GET", `/ssh-sessions?from=${d}&to=${d}`),
    api("GET", `/time-entries?from=${addDays(localDate(new Date()), -21)}`),
  ]);
  if (seq !== loadSeq) return;            // überholt von neuerem load()
  if (!timer || timer.error) { renderOffline(); return; }
  S.timer = timer; S.pause = pause || { active: false };
  S.projects = (Array.isArray(projects) ? projects : []).filter(p => p.status !== "archived" && p.status !== "done");
  const isDay = iso => iso && localDate(new Date(iso)) === d;
  S.entries = (Array.isArray(entries) ? entries : []).filter(e => isDay(e.start_time));
  S.pauses = [...(wd0?.pauses || []), ...(wd1?.pauses || [])].filter(p => isDay(p.start_time));
  S.ssh = (ssh?.sessions || []).filter(s => !s.time_entry_id && s.assign_source !== "timer");
  if (!S.timer.active && !S.pause.active) store.set("beforePause", null);
  // letzte Kombinationen für die Schnellauswahl
  const seen = new Set(); S.recent = [];
  for (const e of Array.isArray(recent) ? recent : []) {
    if (!e.project_id) continue;
    const k = e.task_id ? `${e.project_id}|${e.task_id}` : `${e.project_id}||${(e.comment || "").trim()}`;   // je Ticket nur einmal
    if (seen.has(k)) continue; seen.add(k);
    S.recent.push(e); if (S.recent.length >= 6) break;
  }
  render();
  loadActivities();
}

// ── Darstellung ─────────────────────────────────────────────────
function renderOffline() {
  $("app").style.display = "none";
  $("list").innerHTML = `<div class="offline"><b>Server nicht erreichbar.</b><br>Das Programm versucht ihn zu starten …<br><br><button class="chip" onclick="load()">Erneut versuchen</button></div>`;
  $("bPause").disabled = $("bStop").disabled = true;
}

// Ticket als Hauptsache: „PRJA-581 Beispiel Evidence Portal“
function tLabel(key, title) { if (!key) return title || ""; return title && title.toUpperCase() !== key.toUpperCase() ? `${key} ${title}` : key; }
function projLabel(p) { return p ? `${p.jira_key ? p.jira_key + " · " : ""}${p.name}` : "–"; }

function render() {
  $("app").style.display = "";
  // Läuft-Karte
  const t = S.timer, pz = S.pause, now = $("now");
  now.className = "now" + (t.active ? " running" : pz.active ? " paused" : "");
  if (t.active) {
    now.style.setProperty("--bar", color(t.project?.id));
    now.innerHTML = `<div class="info"><div class="p">${t.task ? esc(tLabel(t.task.jira_ticket, t.task.title)) + (t.project ? ` <span class="tag" style="--c:${color(t.project?.id)}">${esc(t.project.name)}</span>` : "") : esc(t.project?.name || "Ohne Projekt")}</div>
      <div class="s">${esc([t.activity, t.comment || "läuft seit " + hm(t.start_time)].filter(Boolean).join(" · "))}</div></div><div class="clock" id="clock"></div>`;
  } else if (pz.active) {
    now.innerHTML = `<div class="info"><div class="p">Pause</div><div class="s">seit ${hm(pz.start_time)}</div></div><div class="clock" id="clock"></div>`;
  } else {
    now.innerHTML = `<div class="info"><div class="p" style="color:var(--text2)">Kein Timer aktiv</div><div class="s">Work-Item wählen und Start drücken</div></div>`;
  }
  tick();

  // Projekt-Auswahl
  const opts = `<option value="">Projekt wählen …</option>` + S.projects.map(p => `<option value="${p.id}">${esc(projLabel(p))}</option>`).join("") + `<option value="__new__">+ Neues Projekt …</option>`;
  for (const id of ["qProject", "mProject"]) { const el = $(id), v = el.value; el.innerHTML = opts; el.value = v; }
  if (!$("qProject").value) $("qProject").value = store.get("lastProject", "") || "";
  updateTicketList($("qProject").value);

  // Chips
  $("chips").innerHTML = S.recent.map((e, i) => `<button class="chip" data-i="${i}" style="--c:${color(e.project_id)}" title="Direkt starten"><i></i>${e.jira_ticket || e.task_title ? esc(tLabel(e.jira_ticket, e.task_title)) : esc(e.project_name || "") + (e.comment ? " – " + esc(e.comment) : "")}</button>`).join("");

  // Tag
  const today = localDate(new Date());
  const dd = new Date(S.day + "T12:00");
  $("dayLabel").textContent = (S.day === today ? "Heute, " : S.day === addDays(today, -1) ? "Gestern, " : "") +
    dd.toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit", year: "numeric" });
  $("nextDay").style.visibility = S.day >= today ? "hidden" : "visible";

  // Liste
  const items = [
    ...S.entries.map(e => ({ kind: "entry", start: e.start_time, end: e.end_time, sec: e.duration_seconds || 0, e })),
    ...S.pauses.map(p => ({ kind: "pause", start: p.start_time, end: p.end_time, sec: p.end_time ? (Date.parse(p.end_time) - Date.parse(p.start_time)) / 1000 : 0 })),
    ...S.ssh.map(s => ({ kind: "ssh", start: s.start_time, end: s.end_time, sec: s.effective_seconds, s })),
  ];
  if (S.day === today && t.active) items.push({ kind: "running", start: t.start_time, end: null, sec: (Date.now() - Date.parse(t.start_time)) / 1000 });
  items.sort((a, b) => (a.start < b.start ? 1 : -1));

  const work = items.filter(i => i.kind === "entry" || i.kind === "running").reduce((s, i) => s + i.sec, 0);
  const brk = items.filter(i => i.kind === "pause").reduce((s, i) => s + i.sec, 0);
  $("daySum").innerHTML = `<b>${dur(work)}</b><br>${brk ? "Pause " + dur(brk) : "&nbsp;"}`;

  // Zeitleiste 6–20 Uhr
  const t0 = new Date(S.day + "T06:00").getTime(), t1 = new Date(S.day + "T20:00").getTime();
  $("timeline").innerHTML = items.filter(i => i.kind !== "ssh").map(i => {
    const a = Math.max(t0, Date.parse(i.start)), b = Math.min(t1, i.end ? Date.parse(i.end) : Date.now());
    if (b <= a) return "";
    const c = i.kind === "pause" ? "var(--pause)" : color(i.kind === "running" ? t.project?.id : i.e.project_id);
    return `<span style="left:${(a - t0) / (t1 - t0) * 100}%;width:${Math.max(.6, (b - a) / (t1 - t0) * 100)}%;background:${c}"></span>`;
  }).join("");

  $("list").innerHTML = items.length ? items.map((i, idx) => {
    if (i.kind === "pause") return `<div class="entry pause"><div class="info"><div class="t">Pause</div><div class="m">${hm(i.start)} – ${hm(i.end)}</div></div><div class="dur">${dur(i.sec)}</div></div>`;
    if (i.kind === "ssh") return `<div class="entry ssh"><div class="info"><div class="t">SSH ${esc(i.s.server)}</div><div class="m">${hm(i.start)} – ${hm(i.end)}${i.s.project_name ? " · " + esc(i.s.project_name) : " · nicht gebucht"}</div></div><div class="dur">${dur(i.sec)}</div></div>`;
    if (i.kind === "running") return `<div class="entry" style="--c:${color(t.project?.id)};background:var(--sel)"><div class="info"><div class="t">${t.task ? esc(tLabel(t.task.jira_ticket, t.task.title)) + ` <span class="tag" style="--c:${color(t.project?.id)}">${esc(t.project?.name || "")}</span>` : esc(t.project?.name || "–")}</div><div class="m">${hm(i.start)} – läuft${t.comment ? " · " + esc(t.comment) : ""}</div></div><div class="dur" id="runDur">${dur(i.sec)}</div></div>`;
    const e = i.e;
    return `<div class="entry" style="--c:${color(e.project_id)}"><div class="info"><div class="t">${e.jira_ticket || e.task_title ? esc(tLabel(e.jira_ticket, e.task_title)) + (e.project_name ? ` <span class="tag" style="--c:${color(e.project_id)}">${esc(e.project_name)}</span>` : "") : esc(e.project_name || "Ohne Projekt")}</div><div class="m">${hm(i.start)} – ${hm(i.end)}${e.activity ? " · " + esc(e.activity) : ""}${e.comment ? " · " + esc(e.comment) : ""}${(e.source === "plane" || (!e.plane_skip && e.plane_sum_key && e.plane_sum_key === (e.jira_ticket || "").trim().toUpperCase() && e.plane_sum_secs === e.duration_seconds)) ? " · Plane ✓" : ""}</div></div><div class="dur">${dur(i.sec)}</div><button class="again edit" data-e="${idx}" title="Bearbeiten"><svg viewBox="0 0 24 24"><path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/></svg></button><button class="again" data-e="${idx}" title="Erneut starten"><svg viewBox="0 0 24 24"><path d="M7 5l12 7-12 7z"/></svg></button></div>`;
  }).join("") : `<div class="empty">Noch keine Einträge an diesem Tag.</div>`;
  $("list").querySelectorAll("button.again:not(.edit)").forEach(b => b.onclick = () => { const e = items[+b.dataset.e].e; startWith({ project_id: e.project_id, task_id: e.task_id, comment: e.comment, activity: e.activity || null }); });
  $("list").querySelectorAll("button.edit").forEach(b => b.onclick = () => { const e = items[+b.dataset.e].e; openModal(e); });

  // Knöpfe
  $("bPause").disabled = !(t.active || pz.active);
  $("bPause").querySelector("span").textContent = pz.active ? "Weiter" : "Pause";
  $("bStop").disabled = !(t.active || pz.active);
}

function tick() {
  const c = $("clock");
  if (c) c.textContent = clock((Date.now() - Date.parse(S.timer.active ? S.timer.start_time : S.pause.start_time)) / 1000);
  const r = $("runDur");
  if (r && S.timer.active) r.textContent = dur((Date.now() - Date.parse(S.timer.start_time)) / 1000);
}

const planeNames = {};          // Ticket-Schlüssel → Titel/Projekt aus Plane
let planeOn = null, localItems = [], myItems = [], myAt = 0;
async function ensurePlaneOn() {
  if (planeOn === null) { const s = await api("GET", "/settings"); planeOn = !!(s && s.plane_base_url && s.plane_api_key && s.plane_workspace); }
  return planeOn;
}
async function updateTicketList(pid) {
  const tasks = await loadTasks(pid);
  localItems = tasks.filter(t => t.status !== "done").map(t => ({ key: t.jira_ticket || t.title, name: t.title, local: true }));
}
async function loadMyItems() {
  if (!(await ensurePlaneOn())) return [];
  if (myItems.length && Date.now() - myAt < 120e3) return myItems;
  const r = await api("GET", "/integrations/plane/my-items");
  if (Array.isArray(r)) { myItems = r; myAt = Date.now(); r.forEach(i => { planeNames[i.key.toUpperCase()] = { name: i.name, pid: i.local_project_id }; }); }
  return myItems;
}

// Eigene Vorschlagsliste unter dem Ticketfeld (statt der macOS-Liste)
function suggest(inputId, listId, onPicked, getLocal) {
  const inp = $(inputId), box = $(listId);
  let navigated = false;
  let items = [], hi = 0, timer = null, seq = 0;
  const merge = (a, b) => { const k = new Set(a.map(i => (i.key || "").toUpperCase())); return [...a, ...b.filter(i => !k.has((i.key || "").toUpperCase()))]; };
  let base = [];
  const draw = (header) => {
    const q = inp.value.trim();
    items = base.slice(0, 25);
    if (planeOn && q.length >= 3 && !/^[A-Za-z][A-Za-z0-9_]*-\d+$/.test(q)) items.push({ create: true, key: "+ Neu", name: q });
    if (!items.length || document.activeElement !== inp) { box.style.display = "none"; return; }
    box.innerHTML = (header ? `<div class="h">${header}</div>` : "") + items.map((it, i) => it.create
      ? `<button type="button" data-i="${i}" class="new ${i === hi ? "on" : ""}"><span class="k"><b>+ Neues Work-Item</b><small>in Plane anlegen</small></span><span class="n">„${esc(it.name)}“</span></button>`
      : `<button type="button" data-i="${i}" class="${i === hi ? "on" : ""}"><span class="k"><b>${esc(it.key)}</b><small>${esc(it.mine ? (it.state || "mir zugewiesen") : it.local ? "lokal" : "Plane")}</small></span><span class="n">${esc(it.name || "")}</span></button>`).join("");
    box.style.display = "block";
  };
  const refresh = async () => {
    const q = inp.value.trim(), my = ++seq;
    navigated = false;
    const loc = (getLocal ? getLocal() : localItems).filter(t => !q || `${t.key} ${t.name}`.toLowerCase().includes(q.toLowerCase()));
    hi = 0;
    await ensurePlaneOn();
    if (!q) { base = loc; draw(); const mine = await loadMyItems(); if (my !== seq) return; base = merge(mine, loc); draw(mine.length ? "Meine offenen Work-Items" : ""); return; }
    base = loc; draw();
    if (q.length < 2 || !(await ensurePlaneOn())) return;
    clearTimeout(timer);
    timer = setTimeout(async () => {
      const r = await api("GET", `/integrations/plane/search?q=${encodeURIComponent(q)}`);
      if (my !== seq || !Array.isArray(r)) return;
      r.forEach(i => { planeNames[i.key.toUpperCase()] = { name: i.name, pid: i.local_project_id }; });
      base = merge(r, loc); draw();
    }, 250);
  };
  const pick = it => {
    box.style.display = "none";
    if (it.create) { openCreate(it.name, inputId); return; }
    inp.value = it.key; onPicked(it);
  };
  inp.addEventListener("focus", refresh);
  inp.addEventListener("input", refresh);
  inp.addEventListener("blur", () => setTimeout(() => (box.style.display = "none"), 150));
  inp.addEventListener("keydown", e => {
    const open = box.style.display === "block";
    if (e.key === "ArrowDown" && open) { navigated = true; hi = Math.min(hi + 1, items.length - 1); draw(box.querySelector(".h")?.textContent); e.preventDefault(); }
    else if (e.key === "ArrowUp" && open) { hi = Math.max(hi - 1, 0); draw(box.querySelector(".h")?.textContent); e.preventDefault(); }
    else if (e.key === "Enter" && open && items[hi] && inp.value.trim() && items[hi].key !== inp.value.trim()
             && (navigated || (!items[hi].create && !/^[A-Za-z][A-Za-z0-9_]*-\d+$/.test(inp.value.trim())))) { pick(items[hi]); e.preventDefault(); e.stopImmediatePropagation(); }
    else if (e.key === "Escape" && open) { box.style.display = "none"; e.stopImmediatePropagation(); }
  }, true);   // capture: läuft vor dem Enter=Start-Handler
  box.addEventListener("mousedown", e => { const b = e.target.closest("button"); if (!b) return; e.preventDefault(); pick(items[+b.dataset.i]); });
}

// Ticket "ISMS-12" → passendes Projekt vorwählen
function projectFromTicket(ticket) {
  const m = /^([A-Za-z][A-Za-z0-9_]*)-\d+/.exec((ticket || "").trim());
  if (!m) return null;
  const k = m[1].toUpperCase(), pl = planeNames[(ticket || "").trim().toUpperCase()];
  return (pl?.pid && S.projects.find(p => p.id === pl.pid))
    || S.projects.find(p => (p.plane_identifier || "").toUpperCase() === k)
    || S.projects.find(p => (p.jira_key || "").toUpperCase() === k) || null;
}

async function resolveTask(pid, ticket) {
  ticket = (ticket || "").trim();
  if (!pid || !ticket) return null;
  const norm = s => (s || "").trim().replace(/\s+/g, " ").toUpperCase();
  const tasks = await loadTasks(pid);
  const hit = tasks.find(t => norm(t.jira_ticket) === norm(ticket) || norm(t.title) === norm(ticket));
  if (hit) return hit.id;
  const isKey = /^[A-Za-z][A-Za-z0-9_]*-\d+$/.test(ticket);
  const title = planeNames[ticket.toUpperCase()]?.name || ticket;
  const t = await api("POST", "/tasks", { project_id: pid, title, jira_ticket: isKey ? ticket.toUpperCase() : null });
  delete S.tasks[pid];
  return t?.id || null;
}

// ── Tätigkeitsarten ─────────────────────────────────────────────
let activities = [], actAt = 0;
async function loadActivities() {
  if (activities.length && Date.now() - actAt < 300e3) return;
  const r = await api("GET", "/activities");
  if (!Array.isArray(r)) return;
  activities = r; actAt = Date.now();
  for (const id of ["qActivity", "mActivity"]) {
    const el = $(id), v = el.value;
    el.innerHTML = `<option value="">${id === "qActivity" ? "Tätigkeit" : "– keine –"}</option>` + r.map(a => `<option value="${esc(a)}">${esc(a)}</option>`).join("");
    el.value = v;
  }
  if (!$("qActivity").value) $("qActivity").value = store.get("lastActivity", "") || "";
}

// ── Aktionen ────────────────────────────────────────────────────
async function startWith({ project_id, task_id, ticket, comment, activity, start_time }) {
  if (activity === undefined) activity = $("qActivity").value || null;
  if (!project_id) { $("qProject").focus(); return; }
  if (!task_id && ticket) task_id = await resolveTask(project_id, ticket);
  if (S.pause.active) await api("POST", "/pause/stop");
  const r = await api("POST", "/timer/start", { project_id, task_id: task_id || null, comment: comment || null, activity: activity || null, start_time: start_time || undefined });
  if (!r || r.error) { alert(r?.error || "Start fehlgeschlagen – läuft der Zeitprotokoll-Server?"); return; }
  store.set("lastActivity", activity || "");
  store.set("lastProject", String(project_id));
  $("qTicket").value = ""; $("qComment").value = "";
  hideReminder(); bridge.changed(); await load();
}

async function doPause() {
  if (S.pause.active) {                       // Weiter
    const last = store.get("beforePause", null);
    // nur fortsetzen, wenn diese Pause aus genau diesem Timer heraus gestartet wurde
    if (last?.project_id && last.ts && last.ts >= Date.parse(S.pause.start_time) - 5000) { store.set("beforePause", null); return startWith(last); }
    await api("POST", "/pause/stop");
  } else if (S.timer.active) {
    store.set("beforePause", { project_id: S.timer.project?.id, task_id: S.timer.task?.id, comment: S.timer.comment, activity: S.timer.activity || null, ts: Date.now() });
    await api("POST", "/pause/start");
  }
  hideReminder(); bridge.changed(); load();
}

async function doStop() {
  if (S.timer.active) await api("POST", "/timer/stop");
  else if (S.pause.active) await api("POST", "/pause/stop");
  store.set("beforePause", null);
  hideReminder(); bridge.changed(); load();
}

// ── Erinnerung ──────────────────────────────────────────────────
function hideReminder() { $("reminder").style.display = "none"; }
function showReminder(r) {
  $("remText").textContent = r.text;
  const btn = (label, fn, primary) => { const b = document.createElement("button"); b.textContent = label; if (primary) b.className = "primary"; b.onclick = fn; return b; };
  const row = $("remBtns"); row.innerHTML = "";
  if (r.kind === "running") {
    row.append(btn("Ja, weiter", () => { hideReminder(); bridge.hide(); }, true), btn("Wechseln", () => { hideReminder(); $("qProject").focus(); }), btn("Pause", doPause), btn("Stopp", doStop));
  } else if (r.kind === "meeting") {
    const later = () => { hideReminder(); bridge.hide(); };
    if (r.rule?.project_id) {
      const begun = r.start_time && Date.parse(r.start_time) < Date.now() - 60e3;
      const hhmm = r.start_time ? new Date(r.start_time).toTimeString().slice(0, 5) : "";
      if (begun) row.append(btn(`Ab ${hhmm}: ${r.rule.key || r.rule.project_name || "Projekt"}`, async () => {
        await startWith({ project_id: r.rule.project_id, task_id: r.rule.task_id, comment: r.title || `${r.app}-Meeting`, activity: r.rule.activity ?? undefined, start_time: r.start_time });
        bridge.meetingTimerStarted && bridge.meetingTimerStarted(); }, true));
      row.append(btn(`${begun ? "Jetzt" : "Starten"}: ${r.rule.key || r.rule.project_name || "Projekt"}`, async () => {
        await startWith({ project_id: r.rule.project_id, task_id: r.rule.task_id, comment: r.title || `${r.app}-Meeting`, activity: r.rule.activity ?? undefined });
        bridge.meetingTimerStarted && bridge.meetingTimerStarted(); }, !begun));
      row.append(btn("Anderes Projekt", () => { hideReminder(); $("qComment").value = r.title || `${r.app}-Meeting`; $("qProject").focus(); bridge.meetingTimerStarted && bridge.meetingTimerStarted(); }));
    } else {
      row.append(btn("Projekt wählen & starten", () => { hideReminder(); $("qComment").value = r.title || `${r.app}-Meeting`; $("qProject").focus(); bridge.meetingTimerStarted && bridge.meetingTimerStarted(); }, true));
    }
    row.append(btn(r.timerActive ? "Timer läuft schon richtig" : "Nicht jetzt", later));
  } else if (r.kind === "meeting-end") {
    row.append(btn("Stoppen", doStop, true), btn("Weiterlaufen lassen", () => { hideReminder(); bridge.hide(); }));
  } else if (r.kind === "missing") {
    row.append(btn("Mit dem Assistenten nachtragen", () => { hideReminder(); bridge.openMain("assistent/nachtragen/" + (r.first || "")); }, true), btn("Später", () => { hideReminder(); bridge.hide(); }));
  } else if (r.kind === "pause") {
    row.append(btn("Weiter", doPause, true), btn("Später", () => { hideReminder(); bridge.hide(); }));
  } else {
    row.append(btn("Auswählen", () => { hideReminder(); $("qProject").focus(); }, true), btn("Später", () => { hideReminder(); bridge.hide(); }));
  }
  $("reminder").style.display = "block";
  load();
}

// ── Nachtragen ──────────────────────────────────────────────────
let editingId = null;
function openModal(entry) {
  editingId = entry?.id || null;
  $("modalTitle").textContent = entry ? "Zeiteintrag bearbeiten" : "Zeit nachtragen";
  $("mDelete").style.display = entry ? "" : "none";
  $("mErr").textContent = "";
  if (entry) {
    $("mProject").value = entry.project_id || "";
    $("mTicket").value = entry.jira_ticket || entry.task_title || "";
    $("mComment").value = entry.comment || "";
    if (entry.activity && !activities.includes(entry.activity)) $("mActivity").insertAdjacentHTML("beforeend", `<option value="${esc(entry.activity)}">${esc(entry.activity)}</option>`);
    $("mActivity").value = entry.activity || "";
    $("mDate").value = localDate(new Date(entry.start_time));
    const s = new Date(entry.start_time), e = entry.end_time ? new Date(entry.end_time) : new Date();
    $("mFrom").value = `${pad(s.getHours())}:${pad(s.getMinutes())}`;
    $("mTo").value = `${pad(e.getHours())}:${pad(e.getMinutes())}`;
  } else {
    const now = new Date();
    $("mProject").value = $("qProject").value || "";
    $("mTicket").value = ""; $("mComment").value = "";
    $("mActivity").value = $("qActivity").value || "";
    $("mDate").value = S.day;
    const from = new Date(now.getTime() - 60 * 60 * 1000);
    $("mFrom").value = `${pad(from.getHours())}:${pad(from.getMinutes())}`;
    $("mTo").value = `${pad(now.getHours())}:${pad(now.getMinutes())}`;
  }
  $("modal").style.display = "flex";
  if (+$("mProject").value) loadTasks(+$("mProject").value);
}
let savingModal = false;
async function saveModal() {
  if (savingModal) return;
  const pid = +$("mProject").value || projectFromTicket($("mTicket").value)?.id;
  const d = $("mDate").value, f = $("mFrom").value, t = $("mTo").value;
  if (!pid) return ($("mErr").textContent = "Bitte ein Projekt wählen.");
  if (!d || !f || !t) return ($("mErr").textContent = "Datum, Von und Bis ausfüllen.");
  const s = new Date(`${d}T${f}`), e = new Date(`${d}T${t}`);
  if (e <= s) return ($("mErr").textContent = "Bis muss nach Von liegen.");
  savingModal = true;
  const task_id = await resolveTask(pid, $("mTicket").value);
  const body = { project_id: pid, task_id, date: d, activity: $("mActivity").value || null, start_time: s.toISOString(), end_time: e.toISOString(), duration_seconds: Math.round((e - s) / 1000), comment: $("mComment").value || null };
  const r = editingId ? await api("PUT", `/time-entries/${editingId}`, body) : await api("POST", "/time-entries", body);
  savingModal = false;
  if (!r || r.error) return ($("mErr").textContent = r?.error || "Speichern fehlgeschlagen.");
  $("modal").style.display = "none"; editingId = null; load();
}
async function deleteModal() {
  if (!editingId) return;
  if (!confirm("Diesen Zeiteintrag löschen?")) return;
  await api("DELETE", `/time-entries/${editingId}`);
  $("modal").style.display = "none"; editingId = null; load();
}

// ── Neues Projekt (aus Mini-Fenster) ───────────────────────────────
let pmodalTarget = null;
function openNewProjectModal(targetId) {
  pmodalTarget = targetId;
  $("pName").value = ""; $("pKey").value = ""; $("pErr").textContent = "";
  $("pmodal").style.display = "flex";
  setTimeout(() => $("pName").focus(), 30);
}
async function saveNewProject() {
  const name = $("pName").value.trim();
  if (!name) return ($("pErr").textContent = "Bitte einen Namen eingeben.");
  const jira_key = $("pKey").value.trim().toUpperCase() || null;
  const r = await api("POST", "/projects", { name, jira_key });
  if (!r || r.error) return ($("pErr").textContent = r?.error || "Anlegen fehlgeschlagen.");
  $("pmodal").style.display = "none";
  S.projects = []; S.tasks = {};
  await load();
  if (pmodalTarget) { $(pmodalTarget).value = r.id; if (pmodalTarget === "qProject") updateTicketList(r.id); store.set("lastProject", String(r.id)); }
  pmodalTarget = null;
  bridge.changed();
}

// ── Ereignisse ──────────────────────────────────────────────────
$("qStart").onclick = () => {
  const pid = +$("qProject").value || projectFromTicket($("qTicket").value)?.id;
  startWith({ project_id: pid, ticket: $("qTicket").value, comment: $("qComment").value });
};
$("qComment").addEventListener("keydown", e => { if (e.key === "Enter") $("qStart").click(); });
$("qTicket").addEventListener("keydown", e => { if (e.key === "Enter") $("qStart").click(); });
$("qTicket").addEventListener("input", () => { const p = projectFromTicket($("qTicket").value); if (p && $("qProject").value != p.id) { $("qProject").value = p.id; updateTicketList(p.id); } });
$("qProject").onchange = () => {
  if ($("qProject").value === "__new__") { $("qProject").value = ""; openNewProjectModal("qProject"); return; }
  updateTicketList($("qProject").value);
};
$("mProject").onchange = () => {
  if ($("mProject").value === "__new__") { $("mProject").value = ""; openNewProjectModal("mProject"); }
};
$("chips").onclick = e => { const b = e.target.closest(".chip"); if (!b) return; const r = S.recent[+b.dataset.i]; startWith({ project_id: r.project_id, task_id: r.task_id, comment: r.comment, activity: r.activity || null }); };
$("bPause").onclick = doPause;
$("bStop").onclick = doStop;
$("bAdd").onclick = () => openModal();
$("mDelete").onclick = deleteModal;
$("bMain").onclick = () => bridge.openMain();
$("bSheet").onclick = () => bridge.openMain("stundenzettel");
$("bRefresh").onclick = async () => {
  const el = $("bRefresh");
  el.style.transition = "transform .5s ease"; el.style.transform = "rotate(360deg)";
  S.projects = []; S.tasks = {};
  await load();
  setTimeout(() => { el.style.transition = ""; el.style.transform = ""; }, 550);
};
$("bQuit").onclick = () => { if (confirm("Zeitprotokoll beenden? Ein laufender Timer läuft im Hintergrund weiter.")) (bridge.quit || bridge.hide)(); };
$("prevDay").onclick = () => { S.day = addDays(S.day, -1); load(); };
$("nextDay").onclick = () => { S.day = addDays(S.day, 1); load(); };
$("mCancel").onclick = () => ($("modal").style.display = "none");
$("mSave").onclick = saveModal;
$("pCancel").onclick = () => { $("pmodal").style.display = "none"; pmodalTarget = null; };
$("pSave").onclick = saveNewProject;
$("pName").addEventListener("keydown", e => { if (e.key === "Enter") saveNewProject(); });
$("pKey").addEventListener("keydown", e => { if (e.key === "Enter") saveNewProject(); });
$("mTicket").addEventListener("input", () => { const p = projectFromTicket($("mTicket").value); if (p) $("mProject").value = p.id; });
document.addEventListener("keydown", e => { if (e.key === "Escape") { if ($("cmodal").style.display === "flex") { $("cmodal").style.display = "none"; return; } if ($("pmodal").style.display === "flex") { $("pmodal").style.display = "none"; pmodalTarget = null; } else if ($("modal").style.display === "flex") $("modal").style.display = "none"; else bridge.hide(); } });

// ── Neues Work-Item in Plane anlegen ─────────────────────────────
let createFor = null;
async function openCreate(title, inputId) {
  createFor = inputId;
  const fresh = await api("GET", "/projects");
  if (Array.isArray(fresh)) S.projects = fresh.filter(p => p.status !== "archived" && p.status !== "done");
  $("cName").value = title || ""; $("cDesc").value = ""; $("cErr").textContent = "";
  const linked = S.projects.filter(p => p.plane_project_id);
  $("cProject").innerHTML = linked.length ? linked.map(p => `<option value="${p.id}">${esc((p.plane_identifier || p.jira_key) ? (p.plane_identifier || p.jira_key) + " · " : "")}${esc(p.name)}</option>`).join("")
    : `<option value="">– kein Projekt mit Plane verknüpft –</option>`;
  const cur = $(inputId === "mTicket" ? "mProject" : "qProject").value;
  if (linked.some(p => String(p.id) === String(cur))) $("cProject").value = cur;
  $("cStart").style.display = inputId === "qTicket" ? "" : "none";
  $("cSave").className = inputId === "qTicket" ? "" : "primary";
  $("cmodal").style.display = "flex";
  setTimeout(() => $("cName").focus(), 30);
}
async function saveCreate(start) {
  if ($("cSave").disabled) return;
  const name = $("cName").value.trim(), pid = +$("cProject").value;
  if (!name) return ($("cErr").textContent = "Bitte einen Titel eingeben.");
  if (!pid) return ($("cErr").textContent = "Erst ein Plane-Projekt verknüpfen (Hauptfenster → Einstellungen → Plane).");
  $("cSave").disabled = $("cStart").disabled = true;
  const r = await api("POST", "/integrations/plane/work-items", { local_project_id: pid, name, description: $("cDesc").value });
  $("cSave").disabled = $("cStart").disabled = false;
  if (!r || r.error) return ($("cErr").textContent = r?.error || "Anlegen fehlgeschlagen.");
  $("cmodal").style.display = "none";
  myItems = []; S.tasks = {};
  planeNames[(r.key || "").toUpperCase()] = { name: r.name, pid: r.local_project_id };
  if (createFor === "mTicket") { $("mTicket").value = r.key; $("mProject").value = r.local_project_id; $("mComment").focus(); return; }
  $("qProject").value = r.local_project_id; $("qTicket").value = r.key; updateTicketList(r.local_project_id);
  if (start) startWith({ project_id: r.local_project_id, task_id: r.task_id, comment: $("qComment").value });
  else $("qComment").focus();
}
$("cCancel").onclick = () => ($("cmodal").style.display = "none");
$("cSave").onclick = () => saveCreate(false);
$("cStart").onclick = () => saveCreate(true);
$("cName").addEventListener("keydown", e => { if (e.key === "Enter") saveCreate(createFor === "qTicket"); });

// Vorschlagslisten: Auswahl setzt das Projekt automatisch
suggest("qTicket", "qSugg", it => {
  const p = (it.local_project_id && S.projects.find(x => x.id === it.local_project_id)) || projectFromTicket(it.key);
  if (p && $("qProject").value != p.id) { $("qProject").value = p.id; updateTicketList(p.id); }
  $("qComment").focus();
});
const tasksAsItems = pid => (S.tasks[pid] || []).filter(t => t.status !== "done").map(t => ({ key: t.jira_ticket || t.title, name: t.title, local: true }));
$("mProject").addEventListener("change", () => { if (+$("mProject").value) loadTasks(+$("mProject").value); });
suggest("mTicket", "mSugg", it => {
  const p = (it.local_project_id && S.projects.find(x => x.id === it.local_project_id)) || projectFromTicket(it.key);
  if (p) $("mProject").value = p.id;
  $("mComment").focus();
}, () => tasksAsItems(+$("mProject").value));

// ── Ticket aus dem Browser übernehmen (v6.19) ─────────────────────
const btDismissed = new Set();
async function checkBrowser() {
  const box = $("bt"); if (!bridge.browserTickets) return;
  const list = (await bridge.browserTickets().catch(() => [])) || [];
  const running = String(S.timer?.task?.jira_ticket || S.timer?.ticket || "").toUpperCase();
  const t = list.find(x => !btDismissed.has(x.key) && x.key !== running);
  if (!t) { box.style.display = "none"; return; }
  box.innerHTML = "";
  const el = (tag, cls, txt) => { const e = document.createElement(tag); if (cls) e.className = cls; if (txt != null) e.textContent = txt; return e; };
  box.append(el("div", "lbl", `Gerade in ${t.browser} offen (${t.system === "plane" ? "Plane" : "Jira"})`));
  const title = el("div", "t"); title.append(el("b", null, t.key), document.createTextNode(t.title || "")); title.title = `${t.key} ${t.title || ""}`; box.append(title);
  const row = el("div", "row");
  const b1 = el("button", "primary", "▶ Tracken"); b1.onclick = async () => {
    b1.disabled = true;
    if (S.pause.active) await api("POST", "/pause/stop");
    const r = await api("POST", "/ticket/start", { key: t.key, title: t.title });
    if (!r || r.error) { alert(r?.error || "Start fehlgeschlagen"); b1.disabled = false; return; }
    box.style.display = "none"; hideReminder(); bridge.changed(); await load();
  };
  const b2 = el("button", null, "+ To-do heute"); b2.onclick = async () => {
    const r = await api("POST", "/todo", { title: t.title || t.key, jira_ticket: t.key, ticket_title: t.title || null, when: "today" });
    if (r?.error) alert(r.error); btDismissed.add(t.key); box.style.display = "none";
  };
  const bx = el("button", "x", "✕"); bx.title = "Nicht mehr anbieten (bis zum Neustart)"; bx.onclick = () => { btDismissed.add(t.key); box.style.display = "none"; };
  row.append(b1, b2, bx); box.append(row);
  box.style.display = "block";
}

bridge.onReminder(showReminder);
bridge.onRefresh(load);
window.addEventListener("focus", () => { load(); checkBrowser(); });
setTimeout(checkBrowser, 800);
setInterval(tick, 1000);
setInterval(load, 30000);
load();
