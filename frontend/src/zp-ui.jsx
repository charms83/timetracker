/* ═══════════════════════════════════════════════════════════════════
   Bausteine im Stil des Schnellfensters + Plane-Anbindung (UI)
═══════════════════════════════════════════════════════════════════ */
import { useState, useEffect, useRef, useCallback } from "react";
import { Play, Pause, Square, Plus, Coffee, RefreshCw, Check, Upload, Download, EyeOff, Eye, Trash2, Link2, AlertTriangle, X, Clock, KeyRound } from "lucide-react";

/* Plausibilitätsprüfung: Server antwortet 409 + needs_confirm → Nachfrage mit Begründung, dann erneut senden.
   Bei einer Überschneidung (v6.29): „Zeiten anpassen“ – beide Buchungen zusammen ändern, mit Vorschlägen
   (neue kürzen / bestehende kürzen). Ergebnis: { reason } | { adjust: { start, end, other: {id,start,end}|null } } | null */
export function askConfirm(issues, body) {
  return new Promise(resolve => {
    const ov = document.createElement("div");
    ov.style.cssText = "position:fixed;inset:0;background:rgba(0,0,0,.3);z-index:9999;display:flex;align-items:flex-start;justify-content:center;padding-top:90px;font:13px -apple-system,Avenir,sans-serif";
    const esc = t => String(t).replace(/[&<>"]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
    const p2 = n => String(n).padStart(2, "0");
    const toHM = iso => { const d = new Date(iso); return `${p2(d.getHours())}:${p2(d.getMinutes())}`; };
    const conf = issues.filter(i => i.level === "confirm");
    const o = conf.find(i => i.code === "overlap" && i.other)?.other;
    const canAdjust = !!(o && body?.start_time && body?.end_time);
    const inp = (k, v) => `<input type="time" data-t="${k}" value="${v}" style="padding:4px 6px;font:inherit;border:1px solid #ccc;border-radius:6px;background:transparent;color:inherit">`;
    const adjustHtml = !canAdjust ? "" : `
      <div data-adj style="border:1px solid var(--line,#ddd);border-radius:9px;padding:10px 12px;margin:0 0 10px">
        <div style="font-weight:600;margin-bottom:6px">Zeiten anpassen</div>
        <div data-bar style="position:relative;height:30px;margin-bottom:8px"></div>
        <div style="display:grid;grid-template-columns:110px auto auto auto;gap:6px 8px;align-items:center">
          <span>Diese Buchung</span>${inp("ns", toHM(body.start_time))}<span>–</span>${inp("ne", toHM(body.end_time))}
          <span title="${esc(o.comment)}" style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(o.comment || "Bestehende")}</span>${inp("os", toHM(o.start_time))}<span>–</span>${inp("oe", toHM(o.end_time))}
        </div>
        <div style="display:flex;gap:6px;flex-wrap:wrap;margin-top:8px">
          <button data-q="new" style="font-size:12px;padding:3px 10px;border-radius:12px;border:1px solid #ccc;background:transparent;color:inherit;cursor:pointer">Diese Buchung kürzen</button>
          <button data-q="old" style="font-size:12px;padding:3px 10px;border-radius:12px;border:1px solid #ccc;background:transparent;color:inherit;cursor:pointer">Bestehende kürzen</button>
          <button data-q="reset" style="font-size:12px;padding:3px 10px;border-radius:12px;border:1px dashed #ccc;background:transparent;color:inherit;opacity:.7;cursor:pointer">zurücksetzen</button>
        </div>
        <div data-msg style="font-size:12px;margin-top:6px;opacity:.8"></div>
        <div style="display:flex;justify-content:flex-end;margin-top:8px"><button data-k="adj" style="padding:7px 14px;border-radius:7px;border:none;background:#0F752F;color:#fff;font-weight:600;cursor:pointer">Angepasst speichern</button></div>
      </div>
      <div style="font-size:12px;opacity:.75;margin:0 0 4px">… oder so lassen:</div>`;
    const codes = conf.map(i => i.code);
    const chips = [...(codes.includes("overlap") ? ["Termin lief parallel"] : []), ...(codes.includes("absence") ? ["Trotz Abwesenheit gearbeitet"] : []),
      ...(codes.some(c => c === "long10" || c === "day10") ? ["Bereitschaft/Einsatz", "Langer Tag war so"] : codes.includes("overlap") ? ["Bereitschaft/Einsatz"] : []),
      ...(codes.includes("locked") ? ["Bewusst nachgetragen"] : []), "Timer vergessen – passt trotzdem", "Passt so"].filter((c, i, a) => a.indexOf(c) === i);
    ov.innerHTML = `<div style="background:var(--card,#fff);color:var(--text,#000);border-radius:12px;padding:16px 18px;width:min(500px,calc(100vw - 32px));box-shadow:0 10px 40px rgba(0,0,0,.3)">
      <div style="font-weight:600;font-size:15px;margin-bottom:6px">Kurz prüfen</div>
      <ul style="margin:0 0 10px 18px;padding:0">${conf.map(i => `<li style="margin:3px 0">${esc(i.text)}</li>`).join("")}</ul>
      ${adjustHtml}
      <div style="font-size:12px;opacity:.75;margin-bottom:6px">So lassen? Wähle kurz warum (landet im Protokoll):</div>
      <div data-chips style="display:flex;gap:6px;flex-wrap:wrap;margin-bottom:7px">${chips.map(c => `<button type="button" data-chip="${esc(c)}" style="font-size:12px;padding:4px 10px;border-radius:14px;border:1px solid #ccc;background:transparent;color:inherit;cursor:pointer">${esc(c)}</button>`).join("")}</div>
      <textarea rows="2" style="width:100%;padding:7px 9px;font:inherit;border:1px solid #ccc;border-radius:7px" placeholder="oder eigene Notiz (optional) …"></textarea>
      <div style="display:flex;gap:8px;justify-content:flex-end;margin-top:10px">
        <button data-k="no" style="padding:7px 14px;border-radius:7px;border:1px solid #ccc;background:transparent;color:inherit;font-weight:600;cursor:pointer">Abbrechen</button>
        <button data-k="yes" style="padding:7px 14px;border-radius:7px;border:1px solid #0F752F;background:transparent;color:inherit;font-weight:600;cursor:pointer">Trotzdem speichern</button></div></div>`;
    document.body.appendChild(ov);
    const ta = ov.querySelector("textarea");
    const done = v => { ov.remove(); resolve(v); };
    ov.querySelector('[data-k="no"]').onclick = () => done(null);
    ov.querySelectorAll("[data-chip]").forEach(b => b.onclick = () => {
      ta.value = b.dataset.chip; ov.querySelectorAll("[data-chip]").forEach(x => { const on = x === b; x.style.borderColor = on ? "#0F752F" : "#ccc"; x.style.background = on ? "rgba(15,117,47,.12)" : "transparent"; });
    });
    ov.querySelector('[data-k="yes"]').onclick = () => done({ reason: ta.value.trim().length >= 3 ? ta.value.trim() : "Passt so (ohne Angabe)" });
    if (!canAdjust) return;
    const $ = k => ov.querySelector(`[data-t="${k}"]`);
    const m = v => { const [h, mi] = String(v).split(":").map(Number); return h * 60 + mi; };
    const init = { ns: toHM(body.start_time), ne: toHM(body.end_time), os: toHM(o.start_time), oe: toHM(o.end_time) };
    const hmOf = x => `${p2(Math.floor(x / 60))}:${p2(x % 60)}`;
    const draw = () => {
      const ns = m($("ns").value), ne = m($("ne").value), os = m($("os").value), oe = m($("oe").value);
      const lo = Math.min(ns, os), hi = Math.max(ne, oe, lo + 1), w = x => ((x - lo) / (hi - lo) * 100).toFixed(2) + "%";
      const ovl = Math.min(ne, oe) - Math.max(ns, os);
      ov.querySelector("[data-bar]").innerHTML =
        `<div style="position:absolute;top:0;height:12px;left:${w(ns)};width:calc(${w(ne)} - ${w(ns)});background:#0F752F;border-radius:3px;opacity:.85"></div>
         <div style="position:absolute;top:16px;height:12px;left:${w(os)};width:calc(${w(oe)} - ${w(os)});background:#7c5cd6;border-radius:3px;opacity:.85"></div>
         ${ovl > 0 ? `<div style="position:absolute;top:0;height:28px;left:${w(Math.max(ns, os))};width:calc(${w(Math.min(ne, oe))} - ${w(Math.max(ns, os))});background:repeating-linear-gradient(45deg,rgba(196,39,27,.35) 0 4px,transparent 4px 8px)"></div>` : ""}`;
      const msg = ov.querySelector("[data-msg]");
      msg.textContent = ne <= ns || oe <= os ? "Ende muss nach dem Beginn liegen" : ovl > 0 ? `überschneidet sich noch ${ovl} min` : "keine Überschneidung mehr ✓";
      msg.style.color = ovl > 0 || ne <= ns || oe <= os ? "#C4271B" : "#0F752F";
    };
    ["ns", "ne", "os", "oe"].forEach(k => $(k).addEventListener("input", draw));
    ov.querySelector('[data-q="new"]').onclick = () => { const ns = m($("ns").value), ne = m($("ne").value), os = m($("os").value), oe = m($("oe").value);
      if (ns < os) $("ne").value = hmOf(Math.min(ne, os)); else $("ns").value = hmOf(Math.max(ns, oe)); draw(); };
    ov.querySelector('[data-q="old"]').onclick = () => { const ns = m($("ns").value), ne = m($("ne").value), os = m($("os").value), oe = m($("oe").value);
      if (os < ns) $("oe").value = hmOf(Math.min(oe, ns)); else $("os").value = hmOf(Math.max(os, ne)); draw(); };
    ov.querySelector('[data-q="reset"]').onclick = () => { for (const k in init) $(k).value = init[k]; draw(); };
    ov.querySelector('[data-k="adj"]').onclick = () => {
      const v = { ns: m($("ns").value), ne: m($("ne").value), os: m($("os").value), oe: m($("oe").value) };
      if (v.ne <= v.ns || v.oe <= v.os) return draw();
      const at = (iso, mins) => { const d = new Date(iso); d.setHours(Math.floor(mins / 60), mins % 60, 0, 0); return d.toISOString(); };
      const changedOther = $("os").value !== init.os || $("oe").value !== init.oe;
      done({ adjust: { start: at(body.start_time, v.ns), end: at(body.start_time, v.ne), other: changedOther ? { id: o.id, start: at(o.start_time, v.os), end: at(o.start_time, v.oe) } : null } });
    };
    draw();
  });
}
/* ═══ Servermodus (Go-Server, Mehrbenutzer) ═══════════════════════════════════════
   Dieselbe Oberfläche läuft am Node-System (Mac-App, localhost:3747) und am Go-Server. Erkennung einmal beim Start:
   GET /api/auth/state antwortet nur am Go-Server mit 200 + JSON, das Node-System mit 404. Alles Neue hängt an isServerMode(). */
const SERVER = { mode: false, me: null, boot: null, subs: new Set() };
export const isServerMode = () => SERVER.mode;
export const getMe = () => SERVER.me;
const meSet = m => { SERVER.me = m; SERVER.subs.forEach(f => f(m)); };
// reines Administrationskonto (lokales Testkonto, nicht produktiv) oder Notfallzugang: kein Zeiterfassungs-Bereich
export const isAdminOnly = me => !!me && ((me.kind === "local" && !me.productive) || me.role === "breakglass");
export async function loadMe() {
  try {
    const r = await fetch("/api/me", { headers: { Accept: "application/json" } });
    const j = await r.json().catch(() => null);
    if ((r.status === 401 && j?.error === "login_required") || (r.status === 403 && j?.error === "password_change_required")) { location.replace("/login?next=/"); return null; }
    if (!r.ok || !j?.user) return SERVER.me;
    meSet({ ...j.user, auth_mode: j.auth_mode, secrets: j.secrets || {}, notices: Array.isArray(j.notices) ? j.notices : [] });
  } catch (_) {}
  return SERVER.me;
}
// vor dem ersten Rendern aufrufen; true = Seite wird gerade weitergeleitet (nicht rendern)
export function bootServerMode() {
  if (SERVER.boot) return SERVER.boot;
  return SERVER.boot = (async () => {
    try {
      const ac = new AbortController(); const t = setTimeout(() => ac.abort(), 5000);
      const r = await fetch("/api/auth/state", { signal: ac.signal, headers: { Accept: "application/json" } }).finally(() => clearTimeout(t));
      const j = r.ok && /json/i.test(r.headers.get("content-type") || "") ? await r.json().catch(() => null) : null;
      SERVER.mode = !!(j && typeof j === "object" && "mode" in j);
    } catch (_) { SERVER.mode = false; }
    if (!SERVER.mode) return false;
    const me = await loadMe();
    if (isAdminOnly(me)) { location.replace("/admin"); return true; }
    return false;
  })();
}
export function useServerMode() {
  const [me, setMe] = useState(SERVER.me);
  useEffect(() => { SERVER.subs.add(setMe); return () => SERVER.subs.delete(setMe); }, []);
  return { serverMode: SERVER.mode, me };
}
const CLIENT_ONLY_RE = /^\/(meetings\/|ssh-|pair\/|security\/(tls|trust-|untrust-)|ext\/)/;   // lokale Schnittstellen des Mac-Clients
const MSG_CLIENT = "Diese Funktion braucht den Mac-Client.";
const MSG_PENDING = "Diese Funktion kommt in Kürze auf dem Server.";
const MSG_LOCKED = "Dieser Monat ist eingereicht, freigegeben oder geschlossen und gesperrt. Du selbst kannst nichts mehr ändern – Nachtragen macht die Teamleitung oder das Personal (mit Begründung). Wiedereröffnen kann nur das Personal.";
const MISSING = new Set();   // am Server (noch) nicht vorhandene Pfade: nach dem ersten 404 keine weiteren Aufrufe
export const newRequestKey = () => (globalThis.crypto?.randomUUID?.() || `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`);
// Antworten des Go-Servers vereinheitlichen (nur im Servermodus)
function serverMap(res, method, path) {
  const { r, j } = res;
  if (r.pending || r.clientOnly) return res;
  if (r.status === 404 && j == null) { MISSING.add(method + " " + path.split("?")[0]); return { r: { ok: false, status: 404, pending: true }, j: null }; }   // Pfad fehlt am Server (Antwort ohne JSON)
  if (r.status === 409 && j?.code === "month_locked") return { r, j: { ...j, error: MSG_LOCKED, month_locked: true } };
  if (r.status === 403 && j?.code === "admin_only_account") { location.replace("/admin"); }
  return res;
}
async function send(method, path, body, depth = 0) {
  if (SERVER.mode) {
    const p = path.split("?")[0];
    if (CLIENT_ONLY_RE.test(p)) return { r: { ok: false, status: 404, clientOnly: true }, j: null };   // gar nicht erst anfragen
    if (MISSING.has(method + " " + p)) return { r: { ok: false, status: 404, pending: true }, j: null };
    return serverMap(await sendRaw(method, path, body, depth), method, path);
  }
  return sendRaw(method, path, body, depth);
}
async function sendRaw(method, path, body, depth = 0) {
  // nach 30 s abbrechen – hängende Anfragen blockieren sonst den Browser (max. 6 Verbindungen je Adresse)
  const go = b => { const ac = typeof AbortController !== "undefined" ? new AbortController() : null; const t = ac && setTimeout(() => ac.abort(), 30000);
    return fetch(`/api${path}`, { method, headers: { "Content-Type": "application/json" }, body: b ? JSON.stringify(b) : undefined, signal: ac?.signal }).finally(() => t && clearTimeout(t)); };
  let r = await go(body), j = await r.json().catch(() => null);
  // Go-Server (Mehrbenutzer): nicht angemeldet bzw. Passwortwechsel nötig → zur Anmeldeseite (das alte Node-System antwortet nie so)
  if ((r.status === 401 && j?.error === "login_required") || (r.status === 403 && j?.error === "password_change_required")) { location.href = "/login?next=/"; return { r, j }; }
  if (r.status === 409 && j?.needs_confirm) {
    const ans = await askConfirm(j.issues || [], body);
    if (!ans) return { r: { ok: false, status: 409 }, j: { error: "Nicht gespeichert", cancelled: true } };
    if (ans.adjust && depth < 3) {
      const a = ans.adjust;
      if (a.other) {   // erst die bestehende Buchung verschieben (fragt selbst nach, falls nötig)
        const o = await send("PUT", `/time-entries/${a.other.id}`, { start_time: a.other.start, end_time: a.other.end }, depth + 1);
        if (!o.r.ok) return o;
      }
      const nb = { ...(body || {}), start_time: a.start, end_time: a.end };
      if (body && "duration_seconds" in body) nb.duration_seconds = Math.round((Date.parse(a.end) - Date.parse(a.start)) / 1000);
      return send(method, path, nb, depth + 1);
    }
    r = await go({ ...(body || {}), confirm_checks: true, confirm_reason: ans.reason }); j = await r.json().catch(() => null);
  }
  return { r, j };
}
/* Buchung löschen – mit kurzer Rückfrage; schon in Tempo/Plane → wird dort beim nächsten Übertragen mit entfernt */
export async function deleteEntry(e) {
  const t = iso => { const d = new Date(iso); return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`; };
  const what = [e.jira_ticket || e.ticket, e.task_title, e.comment || e.note].filter(Boolean)[0] || e.project_name || "";
  const extra = e.tempo_push_id || e.tempo_id ? "\n\nSie ist schon in Tempo – dort wird sie beim nächsten Übertragen ebenfalls entfernt." : "";
  if (!confirm(`Buchung ${t(e.start_time)}–${t(e.end_time)} ${what} löschen?${extra}`)) return false;
  const r = await call("DELETE", `/time-entries/${e.id}`);
  if (r?.error) { alert(r.error); return false; }
  window.dispatchEvent(new Event("zp:entry-changed")); return true;
}
/* doppelt? – gleiche Aufgabe/gleiches Projekt und mind. 80 % zeitgleich */
export function dupIds(list) {
  const out = new Set(), ms = x => [Date.parse(x.start_time), Date.parse(x.end_time)];
  for (let i = 0; i < list.length; i++) for (let j = i + 1; j < list.length; j++) {
    const a = list[i], b = list[j]; if ((a.task_id || 0) !== (b.task_id || 0) || a.project_id !== b.project_id) continue;
    const [as, ae] = ms(a), [bs, be] = ms(b), ov = Math.min(ae, be) - Math.max(as, bs);
    if (ov > 0 && ov >= 0.8 * Math.min(ae - as, be - bs)) { out.add(a.id); out.add(b.id); }
  }
  return out;
}
export const api = (method, path, body) => send(method, path, body)
  .then(({ r, j }) => r.ok ? j : (j && j.error ? { error: j.error } : null)).catch(() => null);
// Variante, die Fehler mitliefert (für Formulare)
export const call = (m, p, b) => send(m, p, b)
  .then(({ r, j }) => r.ok ? (j || {}) : { error: j?.error || (r.clientOnly ? MSG_CLIENT : r.pending ? MSG_PENDING : `HTTP ${r.status}`) }).catch(e => ({ error: e.message }));

const pad = n => String(n).padStart(2, "0");
export const localDate = d => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
export const hm = iso => { if (!iso) return "…"; const d = new Date(iso); return `${pad(d.getHours())}:${pad(d.getMinutes())}`; };
export const durTxt = s => { s = Math.max(0, Math.round(s || 0)); const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60); return h ? `${h} h ${m} min` : `${m} min`; };
const clock = s => { s = Math.max(0, Math.floor(s)); return `${Math.floor(s / 3600)}:${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`; };
// gleiche Projektfarben wie im Schnellfenster
export const projColor = id => { if (!id) return "#8e8e93"; const hues = [220, 4, 140, 32, 280, 190, 330, 95, 250, 15]; return `hsl(${hues[id % hues.length]} 62% 52%)`; };
const store = { get(k, d) { try { return JSON.parse(localStorage.getItem(k)) ?? d; } catch (_) { return d; } }, set(k, v) { try { localStorage.setItem(k, JSON.stringify(v)); } catch (_) {} } };
const KEY_RE = /^([A-Za-z][A-Za-z0-9_]*)-\d+$/;

export function projectFromTicket(projects, ticket) {
  const m = /^([A-Za-z][A-Za-z0-9_]*)-\d+/.exec((ticket || "").trim());
  if (!m) return null;
  const k = m[1].toUpperCase();
  return projects.find(p => (p.plane_identifier || "").toUpperCase() === k) || projects.find(p => (p.jira_key || "").toUpperCase() === k) || null;
}

export async function resolveTask(pid, ticket, name) {
  ticket = (ticket || "").trim();
  if (!pid || !ticket) return null;
  const norm = s => (s || "").trim().replace(/\s+/g, " ").toUpperCase();
  const tasks = (await api("GET", `/tasks?projectId=${pid}`)) || [];
  const hit = Array.isArray(tasks) && tasks.find(t => norm(t.jira_ticket) === norm(ticket) || norm(t.title) === norm(ticket));
  if (hit) return hit.id;
  const isKey = KEY_RE.test(ticket);
  const t = await api("POST", "/tasks", { project_id: pid, title: name || ticket, jira_ticket: isKey ? ticket.toUpperCase() : null, local_only: !isKey });   // Freitext = Entwurf, Ticket entsteht erst beim Freigeben
  return t?.id || null;
}

/* Ticket als Hauptsache: „PRJA-581 Beispiel Evidence Portal“ */
export function ticketLabel(key, title) {
  if (!key) return title || "";
  return title && title.toUpperCase() !== key.toUpperCase() ? `${key} ${title}` : key;
}

/* Meine offenen Work-Items aus Plane (Server cached 5 Min.; Cache-Key inkl. Workspace) */
let myItemsCache = null, myItemsAt = 0, myItemsKey = "";
async function loadMyItems(force, workspaceId) {
  const ck = String(workspaceId || "");
  if (!force && myItemsCache && myItemsKey === ck && Date.now() - myItemsAt < 120e3) return myItemsCache;
  // Servermodus ohne Filter: alle Workspaces mit Schlüssel (wie Jira über mehrere Instanzen)
  if (isServerMode() && !workspaceId) {
    const list = planeWsCache !== null ? planeWsCache : await reloadPlaneWorkspaces();
    const withKey = (list || []).filter(w => w.key_set);
    if (withKey.length > 1) {
      const parts = await Promise.all(withKey.map(async w => {
        const q = ["workspace=" + w.id];
        if (force) q.unshift("refresh=1");
        const r = await api("GET", `/integrations/plane/my-items?${q.join("&")}`);
        return Array.isArray(r) ? r.map(i => ({ ...i, workspace_id: i.workspace_id || w.id, workspace: i.workspace || w.name })) : [];
      }));
      const seen = new Set(), merged = [];
      for (const row of parts.flat()) {
        const k = (row.key || "").toUpperCase() + ":" + (row.workspace_id || "");
        if (!row.key || seen.has(k)) continue;
        seen.add(k); merged.push(row);
      }
      myItemsCache = merged; myItemsAt = Date.now(); myItemsKey = ck;
      return merged;
    }
  }
  const q = [];
  if (force) q.push("refresh=1");
  if (isServerMode() && workspaceId) q.push("workspace=" + workspaceId);
  const r = await api("GET", `/integrations/plane/my-items${q.length ? "?" + q.join("&") : ""}`);
  if (Array.isArray(r)) { myItemsCache = r; myItemsAt = Date.now(); myItemsKey = ck; }
  return myItemsCache || [];
}

/* Tätigkeitsarten (Liste aus den Einstellungen, gecacht) */
let actCache = null; const actSubs = new Set();
export function reloadActivities() { return api("GET", "/activities").then(r => { if (Array.isArray(r)) { actCache = r; actSubs.forEach(f => f(r)); } return actCache || []; }); }
export function useActivities() {
  const [list, setList] = useState(actCache || []);
  useEffect(() => { actSubs.add(setList); if (!actCache) reloadActivities(); return () => actSubs.delete(setList); }, []);
  return list;
}

/* ── Plane-Workspaces / „eingeschaltet?“ (Server: Schlüssel je Host) ── */
export const wsQ = (ws, first = true) => (isServerMode() && ws ? `${first ? "?" : "&"}workspace=${ws}` : "");
let planeCfgCache = null;
let planeWsCache = null;
const planeWsSubs = new Set();
export function usePlaneWorkspaces() {
  const [list, setList] = useState(planeWsCache || []);
  useEffect(() => {
    if (!isServerMode()) return;
    planeWsSubs.add(setList);
    if (planeWsCache === null) {
      api("GET", "/plane/workspaces").then(r => {
        planeWsCache = Array.isArray(r) ? r : [];
        planeWsSubs.forEach(f => f(planeWsCache));
      });
    }
    return () => planeWsSubs.delete(setList);
  }, []);
  return isServerMode() ? (list || []) : [];
}
export function reloadPlaneWorkspaces() {
  if (!isServerMode()) return Promise.resolve([]);
  return api("GET", "/plane/workspaces").then(r => {
    planeWsCache = Array.isArray(r) ? r : [];
    planeWsSubs.forEach(f => f(planeWsCache));
    return planeWsCache;
  });
}
export function usePlaneOn() {
  const [on, setOn] = useState(planeCfgCache);
  useEffect(() => {
    if (planeCfgCache !== null) { setOn(planeCfgCache); return; }
    if (isServerMode()) {
      api("GET", "/plane/workspaces").then(r => {
        const list = Array.isArray(r) ? r : [];
        planeWsCache = list;
        planeWsSubs.forEach(f => f(list));
        planeCfgCache = list.some(w => w.key_set);
        setOn(planeCfgCache);
      });
      return;
    }
    api("GET", "/settings").then(s => { planeCfgCache = !!(s?.plane_base_url && s?.plane_api_key && s?.plane_workspace); setOn(planeCfgCache); });
  }, []);
  return !!on;
}
const resetPlaneCfg = () => { planeCfgCache = null; myItemsCache = null; myItemsAt = 0; };

/* ── Neues Work-Item in Plane anlegen ─────────────────────────────── */
/* Neues Ticket anlegen – das System wählt Plane oder Jira je nach Projekt:
   Projekt ist in Plane → Work-Item in Plane, sonst → Vorgang im verbundenen Jira-Projekt (dir zugewiesen). */
export function CreateTicketDialog({ title, projectId, canStart, onDone, onClose }) {
  const [projects, setProjects] = useState([]);
  const [pid, setPid] = useState(projectId ? String(projectId) : "");
  const [name, setName] = useState(title || "");
  const [desc, setDesc] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [jiraOn, setJiraOn] = useState(true);
  const sm = isServerMode();
  const [insts, setInsts] = useState([]); const [instId, setInstId] = useState("");   // Servermodus: Jira-Instanzen zur Auswahl
  const rk = useRef({ sig: "", key: newRequestKey() });   // je Dialog-Öffnung eine Auftrags-Kennung; bei Wiederholung derselbe, bei geänderter Eingabe neu
  useEffect(() => {
    if (sm) api("GET", "/jira/instances").then(l => { if (Array.isArray(l)) { setInsts(l); if (l.length === 1) setInstId(String(l[0].id)); } });
    api("GET", "/settings").then(st => setJiraOn(st?.jira_enabled !== "0"));
    api("GET", "/sources").then(r => {
      const list = (r?.projects || []).filter(p => !p.closed && !p.muted && (p.in_plane || p.sources.some(x => x.system === "jira" && !x.gone)));
      setProjects(list);
      if (!list.some(p => String(p.id) === pid)) setPid(list[0] ? String(list[0].id) : "");
    });
  }, []);
  const p = projects.find(x => String(x.id) === pid);
  const jsrc = p?.sources.find(x => x.system === "jira" && !x.gone);
  const target = !p ? null : p.in_plane ? { sys: "plane", key: p.sources.find(x => x.system === "plane")?.key } : jsrc && jiraOn ? { sys: "jira", key: jsrc.key } : null;
  const save = async (start) => {
    if (!name.trim() || !target || busy) return;
    if (sm && target.sys === "jira" && insts.length > 1 && !instId) { setErr("Bitte die Jira-Instanz wählen"); return; }
    setBusy(true); setErr("");
    let extra = {};
    if (sm) {   // Go-Server: nur nach ausdrücklichem Klick (confirm), idempotent über request_key
      const sig = JSON.stringify([target.sys, pid, name.trim(), desc, instId]);
      if (rk.current.sig !== sig) rk.current = { sig, key: newRequestKey() };
      extra = { confirm: true, request_key: rk.current.key, ...(target.sys === "jira" && instId ? { instance_id: +instId } : {}) };
    }
    const r = target.sys === "plane"
      ? await call("POST", "/integrations/plane/work-items", { local_project_id: +pid, name: name.trim(), description: desc, ...extra })
      : await call("POST", "/jira/issues", { project: target.key, summary: name.trim(), description: desc, ...extra });
    setBusy(false);
    if (r.error) { setErr(r.error); return; }
    onDone({ key: r.key, name: r.name || name.trim(), task_id: r.task_id, local_project_id: r.local_project_id || r.project_id, system: target.sys }, start);
  };
  const L = { display: "block", fontSize: 11, color: "var(--text2)", margin: "10px 0 4px" };
  return (
    <div style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.25)", zIndex: 200, display: "flex", alignItems: "flex-start", justifyContent: "center", paddingTop: 80 }}
      onMouseDown={e => e.target === e.currentTarget && onClose()}>
      <div className="zp-card" style={{ width: "min(520px, calc(100vw - 32px))", padding: "16px 18px", boxShadow: "0 10px 40px rgba(0,0,0,.25)", animation: "pop .2s ease" }}>
        <div style={{ fontWeight: 600, fontSize: 15 }}>Neues Ticket</div>
        <div style={{ color: "var(--text2)", fontSize: 12, marginTop: 2 }}>Wird sofort angelegt und dir zugewiesen – in Plane, wenn das Projekt dort ist, sonst in Jira.{sm ? " Mit „Anlegen“ bestätigst du das neue Ticket." : ""}</div>
        <label style={L}>Titel</label>
        <input autoFocus style={{ width: "100%", padding: "7px 9px" }} value={name} onChange={e => setName(e.target.value)} onKeyDown={e => e.key === "Enter" && save(!!canStart)} maxLength={250} />
        <label style={L}>Projekt</label>
        <select style={{ width: "100%", padding: "7px 9px" }} value={pid} onChange={e => setPid(e.target.value)}>
          {!projects.length && <option value="">– kein Projekt mit Plane/Jira verbunden –</option>}
          {projects.map(x => <option key={x.id} value={x.id}>{x.name}</option>)}
        </select>
        {p && <div style={{ fontSize: 12, marginTop: 6, color: target ? "var(--accent)" : "var(--stop)" }}>
          {target ? `→ wird angelegt in ${target.sys === "plane" ? "Plane" : "Jira"} ${target.key || ""}` : "Für dieses Projekt ist kein System verbunden (Jira ist ausgeschaltet)"}</div>}
        {sm && target?.sys === "jira" && insts.length > 1 && (<>
          <label style={L}>Jira-Instanz</label>
          <select style={{ width: "100%", padding: "7px 9px" }} value={instId} onChange={e => setInstId(e.target.value)}>
            <option value="">– bitte wählen –</option>{insts.map(i => <option key={i.id} value={i.id}>{i.name}</option>)}
          </select></>)}
        <label style={L}>Beschreibung (optional)</label>
        <textarea rows={4} style={{ width: "100%", padding: "7px 9px", resize: "vertical", fontFamily: "inherit" }} value={desc} onChange={e => setDesc(e.target.value)} placeholder="Worum geht es?" />
        {err && <div style={{ color: "var(--stop)", fontSize: 12, marginTop: 8 }}>{err}</div>}
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", marginTop: 14 }}>
          <button style={btn(false)} onClick={onClose}>Abbrechen</button>
          <button style={btn(!canStart)} onClick={() => save(false)} disabled={busy || !name.trim() || !target}>{busy ? "Lege an…" : "Anlegen"}</button>
          {canStart && <button style={btn(true)} onClick={() => save(true)} disabled={busy || !name.trim() || !target}><Play size={14} /> Anlegen & starten</button>}
        </div>
      </div>
    </div>
  );
}
export const CreateWorkItemDialog = CreateTicketDialog;

/* ── Ticket-Feld mit Plane-Suche ──────────────────────────────────── */
const NO_TASKS = [];   // fester Standardwert – ein neues [] bei jedem Rendern löste eine Endlosschleife aus (v6.32.2)
export function TicketInput({ value, onChange, onPick, onEnter, placeholder = "Ticket, z.B. PRJB-123", localTasks = NO_TASKS, projectId, onCreatedStart, jira = false, hint }) {
  const locSig = (localTasks || NO_TASKS).map(t => `${t.id}:${t.status}`).join(",");
  const planeOn = usePlaneOn();
  const workspaces = usePlaneWorkspaces();
  const wsName = id => workspaces.find(w => String(w.id) === String(id))?.name;
  const [creating, setCreating] = useState(null);
  const [items, setItems] = useState([]);
  const [open, setOpen] = useState(false);
  const [hi, setHi] = useState(0);
  const tRef = useRef(null);
  const navRef = useRef(false);   // wurde mit Pfeiltasten gewählt?
  useEffect(() => {
    clearTimeout(tRef.current);
    let dead = false;
    navRef.current = false;
    const q = (value || "").trim();
    const loc = localTasks.filter(t => t.status !== "done" && (!q || `${t.jira_ticket || ""} ${t.title}`.toLowerCase().includes(q.toLowerCase())))
      .slice(0, 6).map(t => ({ key: t.jira_ticket || t.title, name: t.title, local: true }));
    if (planeOn && !q) {
      setItems(loc);
      loadMyItems().then(mine => {
        if (dead) return;
        const keys = new Set(mine.map(i => i.key.toUpperCase()));
        setItems([...mine, ...loc.filter(l => !keys.has((l.key || "").toUpperCase()))]);
      });
      return () => { dead = true; };
    }
    if ((!planeOn && !jira) || q.length < 2) { setItems(loc); return; }
    tRef.current = setTimeout(async () => {
      // Plane und (falls gewünscht) Jira gleichzeitig durchsuchen (Plane: alle Workspaces, wie Jira über Instanzen)
      const [r, j] = await Promise.all([planeOn ? api("GET", `/integrations/plane/search?q=${encodeURIComponent(q)}`) : [], jira ? api("GET", `/jira/search?q=${encodeURIComponent(q)}&open=1`).catch(() => null) : null]);
      if (dead) return;
      const jr = (j?.issues || []).slice(0, 10).map(i => ({ key: i.key, name: i.summary, jira: true, due: i.due, state: i.status, instance_id: i.instance_id, instance: i.instance }));
      const pr = (Array.isArray(r) ? r : []).map(i => ({ ...i, workspace: i.workspace || wsName(i.workspace_id) }));
      const pk = new Set(pr.map(i => i.key.toUpperCase()));
      const remote = [...pr, ...jr.filter(i => !pk.has(i.key.toUpperCase()))];
      const keys = new Set(remote.map(i => i.key.toUpperCase()));
      setItems([...remote, ...loc.filter(l => !keys.has((l.key || "").toUpperCase()))]);
    }, 250);
    return () => { dead = true; clearTimeout(tRef.current); };
  }, [value, planeOn, locSig, workspaces]);   // eslint-disable-line react-hooks/exhaustive-deps
  // „+ Neues Work-Item …“ anbieten, wenn Text eingegeben wurde, der keine Ticketnummer ist
  const q = (value || "").trim();
  const shown = q.length >= 3 && !KEY_RE.test(q) ? [...items, { create: true, key: "+ Neu", name: q }] : items;
  const pick = it => {
    if (it.create) { setOpen(false); setCreating(it.name); return; }
    onChange(it.key); onPick?.(it); setOpen(false);
  };
  const created = (r, start) => {
    setCreating(null);
    myItemsCache = null;
    const it = { key: r.key, name: r.name, local_project_id: r.local_project_id, task_id: r.task_id, created: true };
    onChange(r.key); onPick?.(it);
    if (start) onCreatedStart?.(it);
  };
  const planeLbl = it => {
    const n = it.workspace || wsName(it.workspace_id);
    if (it.mine) return n ? `${n} · ${it.state || "mir zugewiesen"}` : (it.state || "mir zugewiesen");
    return n ? `Plane · ${n}` : "Plane";
  };
  return (
    <div className="zp-suggest">
      <input value={value} placeholder={hint || (planeOn ? "Work-Item: klicken oder suchen, z.B. PRJA-581" : placeholder)} autoComplete="off"
        onFocus={() => setOpen(true)} onBlur={() => setTimeout(() => setOpen(false), 150)}
        onChange={e => { onChange(e.target.value); setOpen(true); setHi(0); }}
        onKeyDown={e => {
          if (e.key === "ArrowDown") { navRef.current = true; setHi(h => Math.min(h + 1, shown.length - 1)); e.preventDefault(); }
          else if (e.key === "ArrowUp") { setHi(h => Math.max(h - 1, 0)); e.preventDefault(); }
          else if (e.key === "Enter") {
            const it = shown[hi], typed = (value || "").trim();
            const auto = it && !it.create && !KEY_RE.test(typed);          // Freitext → besten Treffer übernehmen
            if (open && it && it.key !== typed && (navRef.current || auto)) { pick(it); e.preventDefault(); }
            else onEnter?.();
          }
          else if (e.key === "Escape") setOpen(false);
        }} />
      {open && shown.length > 0 && (
        <div className="zp-suggest-list">
          {!value && items.some(i => i.mine) && <div style={{ padding: "6px 10px 4px", fontSize: 10, fontWeight: 600, color: "var(--text2)", textTransform: "uppercase", letterSpacing: ".04em" }}>Meine offenen Work-Items</div>}
          {shown.map((it, i) => it.create ? (
            <button key="create" className={i === hi ? "on" : ""} onMouseDown={e => { e.preventDefault(); pick(it); }} style={{ borderTop: "1px solid var(--line)" }}>
              <b><Plus size={11} style={{ verticalAlign: -1 }} /> Neu</b><span>Ticket „{it.name}“ anlegen (Plane oder Jira – je nach Projekt)</span>
            </button>
          ) : (
            <button key={(it.id || it.key) + ":" + (it.workspace_id || "") + i} className={i === hi ? "on" : ""} onMouseDown={e => { e.preventDefault(); pick(it); }}>
              <b>{it.key}</b><span>{it.name}</span><small>{it.local ? "lokal" : it.jira ? (it.instance ? `Jira · ${it.instance}` : "Jira") : planeLbl(it)}</small>
            </button>
          ))}
        </div>
      )}
      {creating !== null && <CreateTicketDialog title={creating} projectId={projectId} canStart={!!onCreatedStart}
        onDone={created} onClose={() => setCreating(null)} />}
    </div>
  );
}

/* ── Hinweis: fehlende Stunden der letzten 2 Wochen ──────────────── */
export function MissingBanner() {
  const [d, setD] = useState(null);
  const [hidden, setHidden] = useState(() => store.get("missingHidden", "") === localDate(new Date()));
  useEffect(() => { api("GET", "/missing-hours?days=14").then(r => r && !r.error && setD(r)); }, []);
  if (hidden || !d || !d.days?.length) return null;
  const DOW = ["So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"];
  const fmt = h => h.toLocaleString("de-DE", { maximumFractionDigits: 1 });
  const first = d.days[0].date;
  return (
    <div className="zp-card" style={{ padding: "12px 14px", ...(d.optional ? {} : { background: "var(--pause-bg)", borderColor: "var(--pause-line)" }), display: "flex", gap: 12, alignItems: "center", flexWrap: "wrap" }}>
      <div style={{ flex: 1, minWidth: 240 }}>
        {d.optional ? <div style={{ fontWeight: 600 }}>Ein paar Tage sind noch offen<span style={{ fontWeight: 400, color: "var(--text2)" }}> – kein Druck, Konto +{fmt(d.balance_h)} h</span></div> : <>
        <div style={{ fontWeight: 600 }}>In den letzten zwei Wochen fehlen {fmt(d.total_missing_h)} h</div>
        <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 2 }}>
          {d.days.slice(0, 3).map(x => `${DOW[x.dow]} ${x.date.slice(8, 10)}.${x.date.slice(5, 7)}.: ${x.empty ? "leer" : "noch " + fmt(x.missing_h) + " h"}`).join(" · ")}{d.days.length > 3 ? ` · +${d.days.length - 3} weitere` : ""}
        </div></>}
      </div>
      <button style={btn(true)} onClick={() => { location.hash = "assistent/nachtragen/" + first; }} title="Der Assistent zeigt je Tag die Zeit am Mac und schlägt Tickets vor">Mit dem Assistenten nachtragen</button>
      <a href={"#woche/" + first} style={{ fontSize: 12, color: "var(--text2)" }}>Wochenraster</a>
      <button style={btn(false)} onClick={() => { store.set("missingHidden", localDate(new Date())); setHidden(true); }}>Heute nicht mehr</button>
    </div>
  );
}

/* ── Fortschritt Schätzung (Ist / Soll) ────────────────────────────── */
export function EstimateBar({ secs, estimate_h, width = 90 }) {
  if (!estimate_h) return null;
  const pct = secs / 3600 / estimate_h;
  const c = pct > 1 ? "var(--stop)" : pct > 0.8 ? "#c98a00" : "var(--accent)";
  return (
    <span title={`${(secs / 3600).toLocaleString("de-DE", { maximumFractionDigits: 1 })} von ${estimate_h.toLocaleString("de-DE")} h geschätzt`} style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
      <span style={{ width, height: 5, borderRadius: 3, background: "var(--line)", overflow: "hidden", display: "inline-block" }}>
        <span style={{ display: "block", width: `${Math.min(100, pct * 100)}%`, height: "100%", background: c }} />
      </span>
      <span style={{ fontSize: 11, color: c, fontWeight: 600, fontVariantNumeric: "tabular-nums" }}>{Math.round(pct * 100)} %</span>
    </span>
  );
}

/* ── Läuft-Karte (wie Mini-Fenster) ───────────────────────────────── */
export function NowCard({ timer, pauseState }) {
  const [, tick] = useState(0);
  useEffect(() => { const t = setInterval(() => tick(x => x + 1), 1000); return () => clearInterval(t); }, []);
  if (pauseState?.active) return (
    <div className="zp-now paused">
      <div className="info"><div className="p">☕ Pause</div><div className="s">seit {hm(pauseState.start_time)} Uhr</div></div>
      <div className="clock">{clock((Date.now() - Date.parse(pauseState.start_time)) / 1000)}</div>
    </div>
  );
  if (timer?.active) {
    const p = timer.project || {};
    const tk = timer.task?.jira_ticket, tt = timer.task?.title;
    const main = tk || tt ? ticketLabel(tk, tt) : (timer.planTask?.title || p.name || "–");
    return (
      <div className="zp-now running" style={{ "--bar": projColor(p.id) }}>
        <div className="info">
          <div className="p">{main}{(tk || tt) && p.name && <span className="zp-tag" title={p.name} style={{ "--c": projColor(p.id) }}>{(p.plane_identifier || p.jira_key || p.name).toString().toUpperCase().slice(0, 18)}</span>}</div>
          <div className="s">{[timer.activity, timer.comment, `seit ${hm(timer.start_time)} Uhr`].filter(Boolean).join(" · ")}</div>
        </div>
        <div className="clock">{clock((Date.now() - Date.parse(timer.start_time)) / 1000)}</div>
      </div>
    );
  }
  return (
    <div className="zp-now">
      <div className="info"><div className="p">Kein Timer aktiv</div><div className="s">Work-Item wählen und Start drücken</div></div>
    </div>
  );
}

/* ── Schnellstart (wie Mini-Fenster) ──────────────────────────────── */
export function QuickStart({ projects, onChanged }) {
  const active = (projects || []).filter(p => p.status !== "archived" && p.status !== "done");
  const [pid, setPid] = useState(() => store.get("lastProject", ""));
  const [ticket, setTicket] = useState("");
  const [picked, setPicked] = useState(null);
  const [comment, setComment] = useState("");
  const [activity, setActivity] = useState(() => store.get("lastActivity", ""));
  const activities = useActivities();
  const [tasks, setTasks] = useState([]);
  const [recent, setRecent] = useState([]);
  const [busy, setBusy] = useState(false);
  const [hint, setHint] = useState("");

  useEffect(() => {
    if (!pid) { setTasks([]); return; }
    api("GET", `/tasks?projectId=${pid}`).then(t => setTasks(Array.isArray(t) ? t : []));
  }, [pid]);
  const loadRecent = useCallback(async () => {
    const d = new Date(); d.setDate(d.getDate() - 21);
    const r = await api("GET", `/time-entries?from=${localDate(d)}`);
    const seen = new Set(), out = [];
    for (const e of Array.isArray(r) ? r : []) {
      if (!e.project_id) continue;
      const k = e.task_id ? `${e.project_id}|${e.task_id}` : `${e.project_id}||${(e.comment || "").trim()}`;   // je Ticket nur einmal
      if (seen.has(k)) continue; seen.add(k); out.push(e); if (out.length >= 6) break;
    }
    setRecent(out);
  }, []);
  useEffect(() => { loadRecent(); }, [loadRecent]);
  const [now, setNow] = useState(null);
  useEffect(() => { const l = () => api("GET", "/suggest-now").then(r => r && !r.error && setNow(r)); l(); const t = setInterval(l, 5 * 60e3); return () => clearInterval(t); }, []);
  useEffect(() => { if (active.length && !active.some(p => String(p.id) === String(pid))) setPid(String(active[0].id)); }, [active.length, pid]);

  const onTicket = v => {
    setTicket(v); setPicked(null); setHint("");
    const p = projectFromTicket(active, v);
    if (p && String(p.id) !== String(pid)) setPid(String(p.id));
  };
  const onPick = it => {
    setPicked(it); setHint("");
    if (it.created && it.task_id) { setPid(String(it.local_project_id)); return; }
    if (it.local_project_id) setPid(String(it.local_project_id));
    else if (!it.local) {
      const p = projectFromTicket(active, it.key);
      if (p) setPid(String(p.id));
      else setHint(`Plane-Projekt von ${it.key} ist noch nicht verknüpft – Einstellungen → Plane. Buchung läuft vorerst auf das gewählte Projekt.`);
    }
  };

  const start = async ({ project_id, task_id, comment: c, ticket: tk, name } = {}) => {
    project_id = project_id || +pid;
    if (!project_id || busy) return;
    setBusy(true);
    if (!task_id && (tk ?? ticket)) task_id = await resolveTask(project_id, tk ?? ticket, name ?? picked?.name);
    // Kein Ticket, aber „Was machst du?“ ausgefüllt → lokale Aufgabe (Entwurf) anlegen; das echte Ticket entsteht erst beim Freigeben
    if (!task_id && !(tk ?? ticket) && ((c ?? comment) || "").trim()) {
      const t = await api("POST", "/tasks", { project_id, title: (c ?? comment).trim().slice(0, 200), local_only: true });
      if (t && !t.error && t.id) task_id = t.id;
    }
    const r = await api("POST", "/timer/start", { project_id, task_id: task_id || null, comment: (c ?? comment) || null, activity: activity || null });
    store.set("lastActivity", activity || "");
    if (!r || r.error) { setHint(r?.error || "Start fehlgeschlagen – läuft der Zeitprotokoll-Server?"); setBusy(false); return; }
    store.set("lastProject", String(project_id));
    setTicket(""); setComment(""); setPicked(null); setHint(""); setBusy(false);
    onChanged?.(); loadRecent();
  };

  return (
    <div className="zp-quick">
      <div className="grid" style={{ gridTemplateColumns: "1.6fr 1fr" }}>
        <TicketInput value={ticket} onChange={onTicket} onPick={onPick} onEnter={() => start()} localTasks={tasks} projectId={pid}
          onCreatedStart={it => start({ project_id: it.local_project_id, task_id: it.task_id, ticket: "" })} />
        <select value={pid} onChange={e => setPid(e.target.value)} title="Projekt (wird aus dem Work-Item automatisch gesetzt)">
          {!active.length && <option value="">– kein Projekt –</option>}
          {active.map(p => <option key={p.id} value={p.id}>{p.name}{p.customer_name ? ` · ${p.customer_name}` : ""}</option>)}
        </select>
      </div>
      <div className="go">
        <input placeholder="Was machst du?" value={comment} onChange={e => setComment(e.target.value)} onKeyDown={e => e.key === "Enter" && start()} />
        {activities.length > 0 && (
          <select value={activity} onChange={e => setActivity(e.target.value)} title="Tätigkeit" style={{ width: 150, flex: "0 0 auto", color: activity ? "var(--text)" : "var(--text3)" }}>
            <option value="">Tätigkeit …</option>
            {activities.map(a => <option key={a} value={a}>{a}</option>)}
          </select>
        )}
        <button className="zp-btn-start" disabled={!pid || busy} onClick={() => start()}>Start</button>
      </div>
      {hint && <div style={{ color: "#8a6400", fontSize: 11, marginTop: 6 }}>{hint}</div>}
      {now?.items?.length > 0 && !ticket && (
        <div className="zp-chips" style={{ marginBottom: 2 }}>
          <span style={{ fontSize: 11, color: "var(--text2)", alignSelf: "center" }}>✨ Jetzt vermutlich{now.context ? ` (${now.context})` : ""}:</span>
          {now.items.map(e => (
            <button key={"n" + e.project_id + "-" + e.task_id} className="zp-chip" style={{ "--c": projColor(e.project_id), fontWeight: 600 }} title={"Warum: " + e.why.join(" · ")}
              onClick={() => { api("POST", "/gap-suggest/learn", { key: e.key, task_id: e.task_id, project_id: e.project_id }); start({ project_id: e.project_id, task_id: e.task_id, comment: comment || e.comment || "", ticket: "" }); }}>
              <i />{e.label}
            </button>))}
        </div>)}
      {recent.length > 0 && (
        <div className="zp-chips">
          {recent.map(e => (
            <button key={e.id} className="zp-chip" style={{ "--c": projColor(e.project_id) }}
              title={[ticketLabel(e.jira_ticket, e.task_title), e.project_name, e.comment].filter(Boolean).join(" – ")}
              onClick={() => start({ project_id: e.project_id, task_id: e.task_id, comment: e.comment || "", ticket: "" })}>
              <i />{e.jira_ticket || e.task_title ? ticketLabel(e.jira_ticket, e.task_title) : [e.project_name, e.comment].filter(Boolean).join(" – ")}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

/* ── Pause / Stopp (wie Fußleiste im Mini-Fenster) ────────────────── */
export function TimerActions({ timer, pauseState, onChanged, onManual, onPauseEntry }) {
  const doPause = async () => {
    if (pauseState?.active) {
      const last = store.get("beforePause", null);
      await api("POST", "/pause/stop");
      if (last?.project_id && last.ts && last.ts >= Date.parse(pauseState.start_time) - 5000)
        await api("POST", "/timer/start", { project_id: last.project_id, task_id: last.task_id || null, comment: last.comment || null, activity: last.activity || null });
      store.set("beforePause", null);
    } else if (timer?.active) {
      store.set("beforePause", { project_id: timer.project?.id, task_id: timer.task?.id, comment: timer.comment, activity: timer.activity, ts: Date.now() });
      await api("POST", "/pause/start");
    } else {
      await api("POST", "/pause/start");
    }
    onChanged?.();
  };
  const doStop = async () => {
    if (timer?.active) await api("POST", "/timer/stop");
    else if (pauseState?.active) await api("POST", "/pause/stop");
    store.set("beforePause", null);
    onChanged?.();
  };
  return (
    <div className="zp-actions">
      <button className="zp-b-pause" onClick={doPause}>
        {pauseState?.active ? <><Play size={16} /> Weiter</> : <><Pause size={16} /> Pause</>}
      </button>
      <button className="zp-b-stop" onClick={doStop} disabled={!timer?.active && !pauseState?.active}><Square size={15} /> Stopp</button>
      <button className="zp-b-soft" onClick={onManual} style={{ flex: "0 0 auto", padding: "0 14px" }}><Plus size={15} /> Nachtragen</button>
      <button className="zp-b-soft" onClick={onPauseEntry} style={{ flex: "0 0 auto", padding: "0 14px" }}><Coffee size={15} /> Pause erfassen</button>
    </div>
  );
}

/* ── Tages-Zeitleiste (wie Mini-Fenster) ─────────────────────────── */
export function DayTimeline({ date, entries = [], pauses = [] }) {
  const t0 = new Date(date + "T06:00").getTime(), t1 = new Date(date + "T20:00").getTime(), span = t1 - t0;
  const seg = (s, e, c, k) => {
    const a = Math.max(t0, Date.parse(s)), b = Math.min(t1, e ? Date.parse(e) : Date.now());
    if (b <= a) return null;
    return <span key={k} style={{ left: `${(a - t0) / span * 100}%`, width: `${Math.max(.4, (b - a) / span * 100)}%`, background: c }} />;
  };
  return (
    <div className="zp-timeline">
      {entries.map(e => seg(e.start_time, e.end_time, projColor(e.project_id), "e" + e.id))}
      {pauses.map(p => seg(p.start_time, p.end_time, "var(--pause)", "p" + p.id))}
    </div>
  );
}

/* ═══ Box-Hülle für Einstellungs-Abschnitte ═══ */
export const Box = ({ title, sub, children, right }) => (
  <div className="zp-card" style={{ padding: "16px 18px", marginBottom: 12 }}>
    <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: sub || children ? 10 : 0 }}>
      <div style={{ flex: 1 }}>
        <div style={{ fontWeight: 600, fontSize: 14 }}>{title}</div>
        {sub && <div style={{ color: "var(--text2)", fontSize: 12, marginTop: 2 }}>{sub}</div>}
      </div>
      {right}
    </div>
    {children}
  </div>
);
export const lab = { display: "block", fontSize: 11, color: "var(--text2)", margin: "10px 0 4px" };
export const inpS = { width: "100%", padding: "7px 9px" };
export const btn = (primary) => ({ display: "inline-flex", alignItems: "center", gap: 6, padding: "7px 14px", borderRadius: 7, fontWeight: 600,
  border: primary ? "none" : "1px solid var(--line)", background: primary ? "var(--accent-fill)" : "var(--card)", color: primary ? "#fff" : "var(--text)" });

/* Plane-Workspaces (Server): wie Jira-Instanzen – Schlüssel je Host */
function WorkspaceKey({ ws, onChanged }) {
  const [val, setVal] = useState(""); const [msg, setMsg] = useState(null); const [busy, setBusy] = useState(false);
  const save = async () => {
    if (!val.trim() || busy) return;
    setBusy(true); setMsg(null);
    const r = await call("PUT", `/profile/secrets/plane_key?workspace=${ws.id}`, { value: val.trim() });
    setBusy(false);
    if (r.error) { setMsg({ err: true, text: r.error }); return; }
    setVal(""); setMsg({ text: "Schlüssel gespeichert" }); onChanged();
  };
  const del = async () => {
    if (!confirm(`Plane-Schlüssel für „${ws.name}“ entfernen?`)) return;
    setBusy(true); setMsg(null);
    const r = await call("DELETE", `/profile/secrets/plane_key?workspace=${ws.id}`);
    setBusy(false);
    if (r.error) { setMsg({ err: true, text: r.error }); return; }
    setMsg({ text: "Schlüssel entfernt" }); onChanged();
  };
  return (
    <div>
      <label style={lab}>API-Schlüssel (Plane › Profil › API Tokens – verschlüsselt, nie wieder angezeigt)</label>
      <div style={{ display: "flex", gap: 8 }}>
        <input style={inpS} type="password" autoComplete="new-password" placeholder={ws.key_set ? "•••• gespeichert – zum Ersetzen neuen Schlüssel eingeben" : "Schlüssel einfügen"} value={val} onChange={e => setVal(e.target.value)} onKeyDown={e => e.key === "Enter" && save()} />
        <button style={btn(true)} disabled={!val.trim() || busy} onClick={save}><KeyRound size={14} /> Speichern</button>
        {ws.key_set && <button style={btn(false)} disabled={busy} onClick={del} title="Schlüssel entfernen"><Trash2 size={14} /></button>}
      </div>
      {msg && <div style={{ fontSize: 12, marginTop: 6, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</div>}
    </div>);
}
export function PlaneWorkspacesPanel({ status, onChanged }) {
  const [list, setList] = useState(null);
  const load = useCallback(() => call("GET", "/plane/workspaces").then(r => {
    const arr = Array.isArray(r) ? r : (r?.error ? null : []);
    if (r?.error) setList({ error: r.error });
    else { setList(arr); onChanged?.(arr); }
  }), [onChanged]);
  useEffect(() => { load(); }, [load]);
  if (!list) return <div style={{ color: "var(--text3)", fontSize: 12 }}>Lade…</div>;
  if (list.error) return <div style={{ color: "var(--text2)", fontSize: 12 }}>{list.error}</div>;
  if (!list.length) return <div style={{ color: "var(--text2)", fontSize: 12 }}>Es ist kein Plane-Workspace eingerichtet. Das legt der Administrator unter /admin fest.</div>;
  const byHost = {};
  for (const w of list) {
    const h = w.host || w.base_url || "";
    if (!byHost[h]) byHost[h] = { host: h, key_set: false, items: [] };
    byHost[h].key_set = byHost[h].key_set || !!w.key_set;
    byHost[h].items.push(w);
  }
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <div style={{ fontSize: 12, color: "var(--text2)" }}>Der Schlüssel gilt je Plane-Adresse; Workspaces auf demselben Host teilen ihn.</div>
      {Object.values(byHost).map(g => {
        const rep = g.items[0];
        const st = (status || []).find(x => g.items.some(i => i.id === x.id));
        return (
          <div key={g.host || rep.id} style={{ border: "1px solid var(--line)", borderRadius: 10, padding: "12px 14px" }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
              <b>{g.host || rep.name}</b>
              <span className={`zp-pill ${g.key_set ? "synced" : "changed"}`}>{g.key_set ? "Schlüssel gespeichert" : "Schlüssel fehlt"}</span>
              {st && <span style={{ fontSize: 12, color: st.ok ? "var(--accent)" : "var(--stop)" }}>{st.ok ? "✓ " : "✗ "}{st.message}</span>}
            </div>
            <div style={{ fontSize: 12, color: "var(--text2)", margin: "4px 0 8px" }}>{g.items.map(i => `${i.name} (${i.workspace})`).join(" · ")}</div>
            <WorkspaceKey ws={{ ...rep, key_set: g.key_set, name: g.items.map(i => i.name).join(", ") }} onChanged={load} />
          </div>);
      })}
    </div>);
}

/* ═══ Einstellungen → Plane ═══ */
export function PlaneSettingsTab() {
  const sm = isServerMode();
  const [f, setF] = useState({ plane_base_url: "", plane_workspace: "", plane_api_key: "", plane_sync_mode: "manual", plane_insecure_tls: "0" });
  const [msg, setMsg] = useState(null);
  const [busy, setBusy] = useState(false);
  const [wsStatus, setWsStatus] = useState([]);

  useEffect(() => {
    api("GET", "/settings").then(s => s && setF(v => ({ ...v,
      plane_base_url: s.plane_base_url || "", plane_workspace: s.plane_workspace || "", plane_api_key: s.plane_api_key || "",
      plane_sync_mode: s.plane_sync_mode || "manual", plane_insecure_tls: s.plane_insecure_tls || "0" })));
  }, []);
  const set = k => e => setF(v => ({ ...v, [k]: e.target.value }));

  const saveLocal = async () => {
    setBusy(true); setMsg(null);
    const body = { ...f, plane_base_url: f.plane_base_url.trim().replace(/\/+$/, "").replace(/\/api(\/v1)?$/, ""), plane_workspace: f.plane_workspace.trim() };
    const put = await call("PUT", "/settings", body);
    if (put.error) { setMsg({ err: true, text: put.error }); setBusy(false); return; }
    resetPlaneCfg();
    const st = await call("GET", "/integrations/plane/status");
    setMsg(st.error ? { err: true, text: st.error } : { text: st.message || "Gespeichert" });
    setBusy(false);
  };
  const saveMode = async () => {
    setBusy(true); setMsg(null);
    const put = await call("PUT", "/settings", { plane_sync_mode: f.plane_sync_mode });
    resetPlaneCfg();
    setMsg(put.error ? { err: true, text: put.error } : { text: "Übertragungsmodus gespeichert" });
    setBusy(false);
  };
  const testAll = async () => {
    setBusy(true); setMsg(null);
    const list = await reloadPlaneWorkspaces();
    const st = [];
    for (const w of list) {
      const r = await call("GET", `/integrations/plane/status?workspace=${w.id}`);
      st.push({ id: w.id, ok: !!(r.configured && r.connected), message: r.message || r.error || (r.configured ? "ok" : "nicht eingerichtet") });
    }
    setWsStatus(st);
    resetPlaneCfg();
    const ok = st.filter(x => x.ok).length;
    setMsg(ok ? { text: `${ok} von ${st.length} Workspace${st.length === 1 ? "" : "s"} verbunden` } : { err: true, text: st[0]?.message || "Keine Verbindung" });
    setBusy(false);
  };

  return (
    <div>
      <Box title="Plane-Zugang" sub={sm ? "Adresse und Workspace legt der Administrator fest. Du trägst je Plane-Adresse deinen API-Schlüssel ein (verschlüsselt, nie wieder angezeigt)." : "Die Zugangsdaten bleiben lokal auf diesem Mac (SQLite). Der API-Key wird danach nur noch maskiert angezeigt."}>
        {sm ? (<>
          <div style={{ marginBottom: 10 }}>
            <PlaneWorkspacesPanel status={wsStatus} onChanged={() => { resetPlaneCfg(); reloadPlaneWorkspaces(); }} />
          </div>
          <label style={lab}>Übertragung nach Plane</label>
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
            {[["manual", "Manuell freigeben (empfohlen)", "Buchungen bleiben lokal, bis du sie im Reiter „Plane“ freigibst."],
              ["auto", "Automatisch", "Abgeschlossene Buchungen mit Ticket werden nach kurzer Zeit selbst übertragen."]].map(([k, l, d]) => (
              <button key={k} onClick={() => setF(v => ({ ...v, plane_sync_mode: k }))}
                style={{ flex: 1, minWidth: 220, textAlign: "left", padding: "10px 12px", borderRadius: 8,
                  border: `1px solid ${f.plane_sync_mode === k ? "var(--accent)" : "var(--line)"}`,
                  background: f.plane_sync_mode === k ? "var(--accent-soft)" : "var(--card)" }}>
                <div style={{ fontWeight: 600 }}>{f.plane_sync_mode === k ? "● " : "○ "}{l}</div>
                <div style={{ color: "var(--text2)", fontSize: 11, marginTop: 2 }}>{d}</div>
              </button>
            ))}
          </div>
          <div style={{ display: "flex", gap: 8, alignItems: "center", marginTop: 14 }}>
            <button style={btn(true)} onClick={saveMode} disabled={busy}><Check size={14} /> Modus speichern</button>
            <button style={btn(false)} onClick={testAll} disabled={busy}>{busy ? "Prüfe…" : "Verbindungen testen"}</button>
            {msg && <span style={{ fontSize: 12, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</span>}
          </div>
        </>) : (<>
        <label style={lab}>Plane-Adresse</label>
        <input style={inpS} placeholder="https://plane.example.com" value={f.plane_base_url} onChange={set("plane_base_url")} />
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <div><label style={lab}>Workspace (aus der URL, z.B. „example“)</label>
            <input style={inpS} placeholder="example" value={f.plane_workspace} onChange={set("plane_workspace")} /></div>
          <div><label style={lab}>API-Key (Plane → Profil → Personal Access Tokens)</label>
            <input style={inpS} type="password" placeholder="plane_api_…" value={f.plane_api_key} onChange={set("plane_api_key")} /></div>
        </div>
        <label style={lab}>Übertragung nach Plane</label>
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
          {[["manual", "Manuell freigeben (empfohlen)", "Buchungen bleiben lokal, bis du sie im Reiter „Plane“ freigibst."],
            ["auto", "Automatisch", "Abgeschlossene Buchungen mit Ticket werden nach kurzer Zeit selbst übertragen."]].map(([k, l, d]) => (
            <button key={k} onClick={() => setF(v => ({ ...v, plane_sync_mode: k }))}
              style={{ flex: 1, minWidth: 220, textAlign: "left", padding: "10px 12px", borderRadius: 8,
                border: `1px solid ${f.plane_sync_mode === k ? "var(--accent)" : "var(--line)"}`,
                background: f.plane_sync_mode === k ? "var(--accent-soft)" : "var(--card)" }}>
              <div style={{ fontWeight: 600 }}>{f.plane_sync_mode === k ? "● " : "○ "}{l}</div>
              <div style={{ color: "var(--text2)", fontSize: 11, marginTop: 2 }}>{d}</div>
            </button>
          ))}
        </div>
        <label style={{ ...lab, display: "flex", alignItems: "center", gap: 6, cursor: "pointer" }}>
          <input type="checkbox" checked={f.plane_insecure_tls === "1"} onChange={e => setF(v => ({ ...v, plane_insecure_tls: e.target.checked ? "1" : "0" }))} />
          Selbstsigniertes Zertifikat erlauben (nur für interne Instanzen ohne gültiges Zertifikat)
        </label>
        <div style={{ display: "flex", gap: 8, alignItems: "center", marginTop: 14 }}>
          <button style={btn(true)} onClick={saveLocal} disabled={busy}><Check size={14} /> {busy ? "Prüfe…" : "Speichern & Verbindung testen"}</button>
          {msg && <span style={{ fontSize: 12, color: msg.err ? "var(--stop)" : "var(--accent)" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</span>}
        </div>
        </>)}
      </Box>

      <Box title="Projekte verknüpfen" sub="Verknüpft wird jetzt an genau einer Stelle: Projekte › Quellen. Dort siehst du Plane- und Jira-Projekte nebeneinander, übernimmst neue, schaltest ungenutzte stumm und legst fest, wohin die Zeit geht.">
        <button style={btn(true)} onClick={() => { location.hash = "projects"; }}>Zu Projekte › Quellen</button>
      </Box>
      <Box title="Wie es funktioniert">
        <ul style={{ color: "var(--text2)", fontSize: 12, paddingLeft: 18, lineHeight: 1.7 }}>
          <li>Jede Buchung wird zuerst <b>lokal</b> gespeichert. Plane wird nie direkt beschrieben.</li>
          <li>Pro Ticket (z.B. <b>PRJB-123</b>) steht in Plane genau <b>ein Eintrag mit deiner Gesamtzeit</b>. Die App hält ihn aktuell, so wie Tempo bei Jira.</li>
          <li>Damit ist egal, wann übertragen wird: Nachträge für letzte Woche ändern einfach die Summe.</li>
          <li><b>Wer wann wie lange</b> gearbeitet hat, steht nur im Zeitprotokoll (Stundenzettel, Auswertung).</li>
          <li>„Aus Plane holen“ übernimmt Zeiten, die du direkt in Plane gebucht hast. Sie bleiben in Plane eigene Einträge und zählen nicht doppelt.</li>
        </ul>
      </Box>
    </div>
  );
}

/* ═══ Reiter „Plane“: Summen pro Ticket freigeben & abgleichen ═══ */
const ST = { new: "neu", changed: "geändert", synced: "aktuell", error: "Fehler" };
const hmin = m => `${Math.floor((m || 0) / 60)}:${String((m || 0) % 60).padStart(2, "0")} h`;
const deD = d => d ? new Date(d + "T12:00").toLocaleDateString("de-DE", { day: "2-digit", month: "2-digit" }) : "";

function TicketEntries({ ticket, onChanged }) {
  const [rows, setRows] = useState(null);
  const load = useCallback(() => api("GET", `/integrations/plane/outbox/${encodeURIComponent(ticket)}`).then(r => setRows(Array.isArray(r) ? r : [])), [ticket]);
  useEffect(() => { load(); }, [load]);
  if (!rows) return <div style={{ color: "var(--text3)", padding: "6px 14px 10px 44px", fontSize: 12 }}>Lade…</div>;
  const skip = async (id, skipIt) => { await call("POST", "/integrations/plane/skip", { ids: [id], skip: skipIt }); load(); onChanged(); };
  const SRC = { plane: "direkt in Plane", tempo: "Tempo", tempo_csv: "Tempo" };
  return (
    <div style={{ padding: "0 10px 8px 36px" }}>
      {rows.map(e => (
        <div key={e.id} style={{ display: "flex", alignItems: "center", gap: 10, padding: "5px 4px", borderTop: "1px solid var(--line)", fontSize: 12, opacity: e.counted ? 1 : .55 }}>
          <span style={{ color: "var(--text2)", minWidth: 150 }}>{new Date(e.date + "T12:00").toLocaleDateString("de-DE", { weekday: "short", day: "2-digit", month: "2-digit" })} · {hm(e.start_time)}–{hm(e.end_time)}</span>
          <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{e.comment || "–"}</span>
          {SRC[e.source] && <span className="zp-pill">{SRC[e.source]}</span>}
          {e.counted && e.in_plane && <span className="zp-pill synced">in Plane</span>}
          <span style={{ fontVariantNumeric: "tabular-nums", color: "var(--text2)", minWidth: 52, textAlign: "right" }}>{hmin(e.minutes)}</span>
          {!SRC[e.source] && (e.skipped
            ? <button className="zp-nav" title="Wieder mitzählen" onClick={() => skip(e.id, false)}><Eye size={14} /></button>
            : <button className="zp-nav" title="Nicht nach Plane übertragen" onClick={() => skip(e.id, true)}><EyeOff size={14} /></button>)}
        </div>
      ))}
    </div>
  );
}

/* ── Ungenaue Uhrzeit (Servermodus): aus Plane importierte Einträge kennen nur die Dauer ── */
export const isImprecise = e => !!e && isServerMode() && (e.time_precise === false || e.time_precise === 0 || e.source === "plane_import");
export function ImpreciseMark({ e, style }) {
  if (!isImprecise(e)) return null;
  return <span title="aus Plane importiert – Uhrzeit ungenau" aria-label="aus Plane importiert – Uhrzeit ungenau" style={{ display: "inline-flex", color: "var(--text3)", verticalAlign: -2, marginLeft: 4, ...style }}><Clock size={12} /></span>;
}

/* ── Zeiten aus Plane importieren (Servermodus): erst Vorschau (dry_run), dann Bestätigung ── */
function PlaneImportPanel({ onDone, onClose }) {
  const today = localDate(new Date());
  const [from, setFrom] = useState(today.slice(0, 8) + "01"); const [to, setTo] = useState(today);
  const [res, setRes] = useState(null); const [busy, setBusy] = useState(""); const [err, setErr] = useState("");
  const run = async dry => {
    if (busy) return;
    setBusy(dry ? "prev" : "do"); setErr("");
    const r = await call("POST", "/integrations/plane/import", { from, to, dry_run: dry });
    setBusy("");
    if (r.error) { setErr(r.error); if (dry) setRes(null); return; }
    setRes(r);
    if (!dry) onDone?.(r);
  };
  const skipped = res ? Object.entries(res.skipped || {}) : [];
  const changed = () => setRes(null);
  return (
    <div className="zp-card" style={{ padding: "14px 16px" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <div style={{ fontWeight: 600, fontSize: 14, flex: 1 }}>Zeiten aus Plane importieren</div>
        <button className="zp-nav" onClick={onClose} title="Schließen"><X size={15} /></button>
      </div>
      <div style={{ color: "var(--text2)", fontSize: 12, marginTop: 2 }}>Holt Zeiten, die du direkt in Plane gebucht hast. Plane kennt nur die Dauer: Die Uhrzeit ist ein Platzhalter (ab 08:00 gestapelt) und wird als ungenau markiert. Vorhandene Buchungen werden nie überschrieben, eingereichte Monate übersprungen.</div>
      <div style={{ display: "flex", gap: 10, alignItems: "flex-end", flexWrap: "wrap", marginTop: 8 }}>
        <div><label style={lab}>Von</label><input type="date" style={{ padding: "6px 8px" }} value={from} max={to} onChange={e => { setFrom(e.target.value); changed(); }} /></div>
        <div><label style={lab}>Bis</label><input type="date" style={{ padding: "6px 8px" }} value={to} min={from} onChange={e => { setTo(e.target.value); changed(); }} /></div>
        <button style={btn(false)} disabled={!!busy || !from || !to} onClick={() => run(true)}>{busy === "prev" ? "Prüfe…" : "Vorschau"}</button>
        <button style={btn(true)} disabled={!!busy || !res || !res.dry_run || !res.imported} onClick={() => { if (confirm(`${res.imported} Eintrag${res.imported === 1 ? "" : "e"} jetzt aus Plane importieren?`)) run(false); }}>
          <Download size={14} /> {busy === "do" ? "Importiere…" : res?.dry_run && res.imported ? `${res.imported} importieren` : "Importieren"}</button>
      </div>
      {err && <div style={{ color: "var(--stop)", fontSize: 12, marginTop: 8 }}>✗ {err}</div>}
      {res && (
        <div style={{ marginTop: 10, fontSize: 12 }}>
          <div style={{ fontWeight: 600, color: res.imported ? "var(--accent)" : "var(--text2)" }}>
            {res.dry_run ? `Vorschau ${res.from} bis ${res.to}: ${res.imported} Eintrag${res.imported === 1 ? "" : "e"} würden importiert` : `${res.imported} Eintrag${res.imported === 1 ? "" : "e"} importiert`}
            {skipped.length ? `, ${skipped.reduce((a, [, n]) => a + n, 0)} übersprungen` : ""}</div>
          {skipped.length > 0 && <ul style={{ margin: "4px 0 0 18px", padding: 0, color: "var(--text2)" }}>{skipped.map(([why, n]) => <li key={why}>{n}× {why || "übersprungen"}</li>)}</ul>}
          {(res.errors || []).length > 0 && <div style={{ color: "var(--stop)", marginTop: 4 }}>{res.errors.length} Fehler: {res.errors.slice(0, 3).map(x => typeof x === "string" ? x : x.error || x.message || JSON.stringify(x)).join(" · ")}</div>}
          {(res.items || []).filter(i => i.action === "import").slice(0, 12).map(i => (
            <div key={i.worklog_id} style={{ display: "flex", gap: 10, borderTop: "1px solid var(--line)", padding: "3px 0", color: "var(--text2)" }}>
              <span style={{ minWidth: 70 }}>{deD(i.date)}</span><b style={{ color: "var(--accent)", minWidth: 80 }}>{i.ticket || "–"}</b><span style={{ flex: 1 }} /><span>{hmin(i.minutes)}</span></div>))}
          {res.note && <div style={{ color: "var(--text3)", marginTop: 6 }}>{res.note}</div>}
        </div>)}
    </div>);
}

export function PlaneSyncView({ onOpenSettings }) {
  const sm = isServerMode();
  const workspaces = usePlaneWorkspaces();
  const [wsId, setWsId] = useState("");
  const [importOpen, setImportOpen] = useState(false);
  const [data, setData] = useState(null);
  const [est, setEst] = useState({});
  const [editEst, setEditEst] = useState(null);
  const loadEst = useCallback(() => api("GET", "/ticket-estimates").then(r => r && !r.error && setEst(r)), []);
  useEffect(() => { loadEst(); }, [loadEst]);
  useEffect(() => {
    if (sm && workspaces.length === 1 && !wsId) setWsId(String(workspaces[0].id));
  }, [sm, workspaces, wsId]);
  const saveEst = async (ticket, val) => {
    const r = await call("PUT", `/ticket-estimates/${encodeURIComponent(ticket)}`, { hours: val });
    setEditEst(null);
    if (r.error) setMsg({ err: true, text: r.error }); else loadEst();
  };
  const [localOnly, setLocalOnly] = useState([]);
  const loadLocal = useCallback(() => api("GET", "/integrations/plane/local-only").then(r => Array.isArray(r) && setLocalOnly(r)), []);
  useEffect(() => { loadLocal(); }, [loadLocal]);
  const pushLocal = async (t) => {
    setBusy("loc" + t.id);
    const r = await call("POST", `/integrations/plane/local-only/${t.id}/push`);
    setBusy("");
    setMsg(r.error ? { err: true, text: r.error } : { text: `„${t.title}“ in Plane angelegt: ${r.jira_ticket || ""}` });
    loadLocal(); load();
  };
  const [filter, setFilter] = useState("open");
  const [open, setOpen] = useState(null);
  const [busy, setBusy] = useState("");
  const [msg, setMsg] = useState(null);
  const load = useCallback(async () => {
    const r = await api("GET", `/integrations/plane/outbox${wsQ(wsId)}`);
    setData(r && !r.error ? r : { failed: true, error: r?.error || "Server nicht erreichbar" });
  }, [wsId]);
  useEffect(() => { load(); }, [load]);

  if (!data) return <div style={{ color: "var(--text3)", padding: 30, textAlign: "center" }}>Lade…</div>;
  if (data.failed) return (
    <div className="zp-card" style={{ padding: 24, textAlign: "center" }}>
      <div style={{ color: "var(--stop)", marginBottom: 10 }}>✗ {data.error}</div>
      <button style={btn(false)} onClick={load}><RefreshCw size={13} /> Erneut versuchen</button>
    </div>
  );
  if (!data.configured) return (
    <div className="zp-card" style={{ padding: 30, textAlign: "center" }}>
      <div style={{ fontWeight: 600, fontSize: 15, marginBottom: 6 }}>Plane ist noch nicht eingerichtet</div>
      <div style={{ color: "var(--text2)", marginBottom: 14 }}>{sm ? "API-Schlüssel unter Einstellungen → Plane je Workspace eintragen." : "Trage Adresse, Workspace und API-Key unter Einstellungen → Plane ein."}</div>
      <button style={btn(true)} onClick={onOpenSettings}>Zu den Einstellungen</button>
    </div>
  );
  const wsName = workspaces.find(w => String(w.id) === String(wsId))?.name;

  const c = data.counts || {};
  const pending = (c.new || 0) + (c.changed || 0) + (c.error || 0);
  const isOpen = t => t.status !== "synced";
  const shown = data.tickets.filter(t => filter === "all" ? true : filter === "open" ? isOpen(t) : t.status === filter);
  const totalLocal = data.tickets.reduce((s, t) => s + t.local_minutes, 0);

  const push = async (tickets) => {
    setBusy("push"); setMsg(null);
    const r = await call("POST", "/integrations/plane/push", tickets ? { tickets } : {});
    setBusy("");
    if (r.busy) { setMsg({ text: "Eine Übertragung läuft gerade – wird danach automatisch fortgesetzt." }); load(); return; }
    setMsg(r.error ? { err: true, text: r.error } : { err: r.errors > 0,
      text: `${r.ok} Ticket${r.ok === 1 ? "" : "s"} aktualisiert${r.errors ? `, ${r.errors} Fehler` : ""}${r.deletions?.length ? `, ${r.deletions.filter(d => d.status === "deleted").length} gelöscht` : ""}` });
    load();
  };
  const pull = async () => {
    setBusy("pull"); setMsg(null);
    const r = await call("POST", "/integrations/plane/pull", {});
    setBusy("");
    setMsg(r.error ? { err: true, text: r.error } : { text: `${r.imported} direkt in Plane gebuchte Einträge übernommen, ${r.linked} Summen wiedererkannt${r.errors?.length ? ` – ${r.errors.length} Fehler` : ""}` });
    load();
  };
  const skipTicket = async (t, skipIt) => {
    if (skipIt && !confirm(`${t.ticket} komplett von der Übertragung ausnehmen?${t.plane_minutes ? " Die Summe in Plane wird mit der nächsten Freigabe entfernt." : ""}`)) return;
    await call("POST", "/integrations/plane/skip", { tickets: [t.ticket], skip: skipIt }); load();
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <div className="zp-card" style={{ padding: "14px 16px", display: "flex", alignItems: "center", gap: 14, flexWrap: "wrap" }}>
        <div style={{ flex: 1, minWidth: 260 }}>
          <div style={{ fontWeight: 600, fontSize: 14 }}>Plane-Abgleich · Summe pro Ticket{wsName ? ` · ${wsName}` : ""}</div>
          <div style={{ color: "var(--text2)", fontSize: 12 }}>
            In Plane steht je Ticket genau ein Eintrag mit deiner Gesamtzeit. Wer wann wie lange gearbeitet hat, bleibt hier im Zeitprotokoll.
          </div>
          <div style={{ color: "var(--text2)", fontSize: 12, marginTop: 4 }}>
            {data.mode === "auto" ? "Automatische Übertragung ist aktiv · " : ""}{pending} Ticket{pending === 1 ? "" : "s"} offen
            {data.deletions.length ? ` · ${data.deletions.length} Löschung(en) vorgemerkt` : ""}
          </div>
        </div>
        {sm && workspaces.length > 1 && (
          <select value={wsId} onChange={e => setWsId(e.target.value)} style={{ ...inpS, width: "auto", minWidth: 160 }} title="Plane-Workspace">
            <option value="">Primär-Workspace</option>
            {workspaces.map(w => <option key={w.id} value={w.id}>{w.name}{w.key_set ? "" : " (Schlüssel fehlt)"}</option>)}
          </select>
        )}
        {sm
          ? <button style={btn(false)} onClick={() => setImportOpen(o => !o)} disabled={!!busy}><Download size={14} /> Zeiten aus Plane importieren</button>
          : <button style={btn(false)} onClick={pull} disabled={!!busy}><Download size={14} /> {busy === "pull" ? "Hole…" : "Aus Plane holen"}</button>}
        <button style={btn(true)} onClick={() => { if (confirm(`${pending} Buchung(en)${data.deletions.length ? ` und ${data.deletions.length} Löschung(en)` : ""} jetzt nach Plane übertragen?`)) push(null); }} disabled={!!busy || (!pending && !data.deletions.length)}>
          <Upload size={14} /> {busy === "push" ? "Übertrage…" : `Alle freigeben (${pending + data.deletions.length})`}
        </button>
      </div>
      {msg && <div style={{ fontSize: 12, color: msg.err ? "var(--stop)" : "var(--accent)", padding: "0 4px" }}>{msg.err ? "✗ " : "✓ "}{msg.text}</div>}
      {sm && importOpen && <PlaneImportPanel onClose={() => setImportOpen(false)} onDone={r => { setMsg({ text: `${r.imported} Zeit${r.imported === 1 ? "" : "en"} aus Plane importiert` }); load(); window.dispatchEvent(new Event("zp:entry-changed")); }} />}
      {localOnly.length > 0 && (
        <div className="zp-card" style={{ padding: "12px 16px" }}>
          <div style={{ fontWeight: 600, fontSize: 13 }}>Nur lokal angelegt – noch nicht in Plane</div>
          <div style={{ color: "var(--text2)", fontSize: 12, margin: "2px 0 8px" }}>Diese Aufgaben haben kein Ticket. Mit einem Klick wird daraus ein Work-Item in Plane (dir zugewiesen).</div>
          {localOnly.map(t => (
            <div key={t.id} style={{ display: "flex", alignItems: "center", gap: 10, padding: "5px 0", borderTop: "1px solid var(--line)" }}>
              <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{t.title} <small style={{ color: "var(--text3)" }}>· {t.project_name}</small></span>
              <button style={btn(false)} disabled={!!busy} onClick={() => pushLocal(t)}>{busy === "loc" + t.id ? "Lege an…" : "In Plane anlegen"}</button>
            </div>
          ))}
        </div>
      )}

      <div style={{ display: "flex", gap: 6, flexWrap: "wrap", alignItems: "center" }}>
        {[["open", `Offen (${pending})`], ["synced", `Aktuell (${c.synced || 0})`], ["all", `Alle (${data.tickets.length})`]].map(([k, l]) => (
          <button key={k} onClick={() => setFilter(k)}
            style={{ padding: "4px 12px", borderRadius: 20, fontSize: 12, border: "1px solid " + (filter === k ? "var(--accent)" : "var(--line)"),
              background: filter === k ? "var(--accent-soft)" : "var(--card)", color: filter === k ? "var(--accent)" : "var(--text2)" }}>{l}</button>
        ))}
        <div style={{ flex: 1 }} />
        <span style={{ color: "var(--text2)", fontSize: 12 }}>Gesamt lokal: <b style={{ color: "var(--text)" }}>{hmin(totalLocal)}</b></span>
      </div>

      {data.deletions.length > 0 && (
        <div className="zp-card" style={{ padding: "6px 0" }}>
          {data.deletions.map(dl => (
            <div key={dl.id} className="zp-entry" style={{ "--c": "var(--stop)", margin: "4px 10px" }}>
              <Trash2 size={14} color="var(--stop)" />
              <div className="info"><div className="t">Löschung in Plane vorgemerkt</div><div className="m">{dl.label}{dl.error ? ` · ${dl.error}` : ""}</div></div>
              <button className="zp-nav" title="Löschung verwerfen (Worklog bleibt in Plane)" onClick={async () => { await api("DELETE", `/integrations/plane/deletions/${dl.id}`); load(); }}>✕</button>
            </div>
          ))}
        </div>
      )}

      <div className="zp-card" style={{ padding: "8px 0 4px" }}>
        {shown.length === 0 && <div style={{ color: "var(--text3)", textAlign: "center", padding: 24 }}>{filter === "open" ? "Alles aktuell – Plane hat dieselben Summen wie das Zeitprotokoll." : "Keine Tickets."}</div>}
        {shown.map(t => {
          const diff = t.local_minutes - t.plane_minutes;
          return (
            <div key={t.ticket} style={{ margin: "0 10px 4px" }}>
              <div className="zp-entry" style={{ margin: 0, "--c": t.status === "error" ? "var(--stop)" : t.status === "synced" ? "var(--accent)" : "#4a6cf7", cursor: "pointer" }}
                onClick={() => setOpen(o => o === t.ticket ? null : t.ticket)}>
                <span style={{ color: "var(--text3)", width: 14 }}>{open === t.ticket ? "▾" : "▸"}</span>
                <div className="info">
                  <div className="t"><b style={{ color: "var(--accent)", marginRight: 6 }}>{t.ticket}</b>{t.title || ""}</div>
                  <div className="m">
                    {t.entries} {t.entries === 1 ? "Eintrag" : "Einträge"}{t.first ? ` · ${deD(t.first)}${t.last !== t.first ? "–" + deD(t.last) : ""}` : ""}{t.projects ? ` · ${t.projects}` : ""}
                    {t.legacy ? ` · ${t.legacy} alte Einzel-Einträge werden zusammengefasst` : ""}
                    {t.error && <span style={{ color: "var(--stop)" }}> · <AlertTriangle size={10} /> {t.error}</span>}
                  </div>
                </div>
                <div style={{ minWidth: 150, textAlign: "right" }} onClick={e => e.stopPropagation()}>
                  {editEst === t.ticket ? (
                    <input autoFocus defaultValue={est[t.ticket]?.estimate_h || ""} placeholder="Std." style={{ width: 70, padding: "3px 6px", textAlign: "right" }}
                      onBlur={e => saveEst(t.ticket, e.target.value)} onKeyDown={e => { if (e.key === "Enter") e.target.blur(); if (e.key === "Escape") setEditEst(null); }} />
                  ) : est[t.ticket]?.estimate_h ? (
                    <span style={{ cursor: "pointer" }} onClick={() => setEditEst(t.ticket)} title="Schätzung ändern">
                      <EstimateBar secs={est[t.ticket].total_secs} estimate_h={est[t.ticket].estimate_h} width={60} />
                      <div style={{ fontSize: 10, color: "var(--text3)" }}>von {String(est[t.ticket].estimate_h).replace(".", ",")} h</div>
                    </span>
                  ) : (
                    <button onClick={() => setEditEst(t.ticket)} style={{ fontSize: 11, color: "var(--text3)", background: "none", border: "1px dashed var(--line)", borderRadius: 6, padding: "2px 8px" }}>+ Schätzung</button>
                  )}
                </div>
                <div style={{ textAlign: "right", fontSize: 12, lineHeight: 1.3, minWidth: 120 }}>
                  <div><span style={{ color: "var(--text2)" }}>lokal </span><b style={{ fontVariantNumeric: "tabular-nums" }}>{hmin(t.local_minutes)}</b></div>
                  <div style={{ color: "var(--text2)" }}>Plane <span style={{ fontVariantNumeric: "tabular-nums" }}>{hmin(t.plane_minutes)}</span>
                    {diff !== 0 && <b style={{ marginLeft: 4, color: diff > 0 ? "#3651c9" : "var(--stop)" }}>{diff > 0 ? "+" : "−"}{hmin(Math.abs(diff))}</b>}</div>
                </div>
                <span className={`zp-pill ${t.status}`}>{ST[t.status] || t.status}</span>
                <div style={{ display: "flex", gap: 2 }} onClick={e => e.stopPropagation()}>
                  {t.status !== "synced" && <button className="zp-nav" title="Dieses Ticket jetzt übertragen" onClick={() => push([t.ticket])} disabled={!!busy}><Upload size={14} /></button>}
                  {t.local_minutes > 0 && <button className="zp-nav" title="Ticket nicht nach Plane übertragen" onClick={() => skipTicket(t, true)}><EyeOff size={14} /></button>}
                </div>
              </div>
              {open === t.ticket && <TicketEntries ticket={t.ticket} onChanged={load} />}
            </div>
          );
        })}
      </div>

      {(data.without_ticket.count > 0 || data.skipped.length > 0) && (
        <div className="zp-card" style={{ padding: "12px 16px", fontSize: 12, color: "var(--text2)" }}>
          {data.without_ticket.count > 0 && <div>{data.without_ticket.count} Buchungen ({hmin(data.without_ticket.minutes)}) seit {deD(data.without_ticket.since)} haben <b>kein Ticket</b> und bleiben nur im Zeitprotokoll. Ticket nachtragen über den Stift in der Tagesansicht.</div>}
          {data.skipped.length > 0 && (
            <div style={{ marginTop: data.without_ticket.count ? 10 : 0 }}>
              <div style={{ marginBottom: 4 }}><b>Nicht übertragen</b> (bewusst ausgenommen):</div>
              {data.skipped.map(e => (
                <div key={e.id} style={{ display: "flex", gap: 10, alignItems: "center", padding: "3px 0" }}>
                  <span style={{ minWidth: 70 }}>{deD(e.date)}</span><b style={{ color: "var(--accent)" }}>{e.ticket || "–"}</b>
                  <span style={{ flex: 1 }}>{e.comment || ""}</span><span>{hmin(e.minutes)}</span>
                  <button className="zp-nav" title="Wieder mitzählen" onClick={async () => { await call("POST", "/integrations/plane/skip", { ids: [e.id], skip: false }); load(); }}><Eye size={14} /></button>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/* ── Ticket aus dem Browser übernehmen (nur in der Mac-App) ─────────
   Zeigt das Jira-/Plane-Ticket aus dem aktiven Browser-Tab an: ▶ Tracken oder als To-do. */
const btDismissed = new Set();
export function BrowserTicketBar({ timer, onChanged }) {
  const [list, setList] = useState([]); const [err, setErr] = useState(null); const [busy, setBusy] = useState(false);
  const has = typeof window !== "undefined" && window.zpDesktop?.browserTickets;
  const check = useCallback(() => { if (has) window.zpDesktop.browserTickets().then(r => setList(Array.isArray(r) ? r : [])).catch(() => {}); }, [has]);
  useEffect(() => { check(); window.addEventListener("focus", check); return () => window.removeEventListener("focus", check); }, [check]);
  const running = String(timer?.task?.jira_ticket || "").toUpperCase();
  const t = list.find(x => !btDismissed.has(x.key) && x.key !== running);
  if (!has || !t) return null;
  const start = async () => { setBusy(true); setErr(null); const r = await call("POST", "/ticket/start", { key: t.key, title: t.title }); setBusy(false); if (r?.error) return setErr(r.error); btDismissed.add(t.key); setList(l => [...l]); onChanged && onChanged(); };
  const todo = async () => { const r = await call("POST", "/todo", { title: t.title || t.key, jira_ticket: t.key, ticket_title: t.title || null, when: "today" }); if (r?.error) return setErr(r.error); btDismissed.add(t.key); setList(l => [...l]); onChanged && onChanged(); };
  return (
    <div className="zp-card" style={{ padding: "10px 14px", border: "1px solid var(--accent)", boxShadow: "0 0 0 3px var(--accent-soft)", display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
      <div style={{ flex: "1 1 260px", minWidth: 0 }}>
        <div style={{ fontSize: 11, color: "var(--text2)" }}>Gerade in {t.browser} offen ({t.system === "plane" ? "Plane" : "Jira"})</div>
        <div style={{ fontWeight: 600, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}><span style={{ color: "var(--accent)", marginRight: 6 }}>{t.key}</span>{t.title}</div>
        {err && <div style={{ fontSize: 12, color: "var(--stop)" }}>{err}</div>}
      </div>
      <button style={btn(true)} disabled={busy} onClick={start}><Play size={14} /> Tracken</button>
      <button style={btn(false)} onClick={todo}><Plus size={14} /> To-do heute</button>
      <button className="zp-nav" title="Nicht mehr anbieten" onClick={() => { btDismissed.add(t.key); setList(l => [...l]); }}><X size={14} /></button>
    </div>);
}
