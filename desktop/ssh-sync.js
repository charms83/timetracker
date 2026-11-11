/* SSH-Protokoll → Team-Server (Parität zu backend/server.js importSshLog).
   Liest ~/.zeitprotokoll/ssh_sessions.log und synct neue Zeilen per POST /ssh-sessions/sync. */
const fs = require("fs");
const path = require("path");
const os = require("os");

const LOG = process.env.SSH_LOG || path.join(os.homedir(), ".zeitprotokoll", "ssh_sessions.log");
const FLAG = path.join(path.dirname(LOG), "ssh_tracking_disabled");
const OFFSET = path.join(path.dirname(LOG), "ssh_log_offset_team");

function localDateOf(ms) {
  const d = new Date(ms);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

function readOffset() {
  try { return parseInt(fs.readFileSync(OFFSET, "utf8"), 10) || 0; } catch (_) { return 0; }
}
function writeOffset(n) {
  try { fs.mkdirSync(path.dirname(OFFSET), { recursive: true }); fs.writeFileSync(OFFSET, String(n)); } catch (_) {}
}

function parseChunk(text) {
  const byUid = new Map();
  for (const l of String(text || "").split("\n")) {
    if (!l) continue;
    const f = l.split("\t");
    if (f[0] === "S" && f.length >= 4) {
      const ms = parseInt(f[2], 10) * 1000;
      if (!ms) continue;
      byUid.set(f[1], {
        session_uid: f[1], date: localDateOf(ms), start_time: new Date(ms).toISOString(),
        target: f[3] || "", hostname: f[4] || "", username: f[5] || "",
        port: parseInt(f[6], 10) || null, remote_cmd: f[7] || "",
      });
    } else if (f[0] === "E" && f.length >= 3) {
      const endMs = parseInt(f[2], 10) * 1000;
      const cur = byUid.get(f[1]) || { session_uid: f[1] };
      if (endMs && cur.start_time) {
        cur.end_time = new Date(endMs).toISOString();
        cur.duration_seconds = Math.max(0, Math.round((endMs - Date.parse(cur.start_time)) / 1000));
        cur.exit_code = parseInt(f[3], 10);
        if (!cur.date) cur.date = localDateOf(Date.parse(cur.start_time));
      } else if (endMs) {
        cur.end_time = new Date(endMs).toISOString();
        cur.exit_code = parseInt(f[3], 10);
      }
      byUid.set(f[1], cur);
    }
  }
  return [...byUid.values()].filter(s => s.start_time);
}

function create({ api, log }) {
  let busy = false;
  async function syncTracking() {
    const st = await api("GET", "/ssh-tracking");
    if (!st || st.error) return;
    try {
      fs.mkdirSync(path.dirname(FLAG), { recursive: true });
      if (st.enabled === false) {
        if (!fs.existsSync(FLAG)) fs.writeFileSync(FLAG, `deaktiviert ${Date.now()}\n`);
      } else if (fs.existsSync(FLAG)) fs.unlinkSync(FLAG);
    } catch (e) { log("ssh flag", e.message); }
  }
  async function tick() {
    if (busy) return; busy = true;
    try {
      await syncTracking();
      const exists = fs.existsSync(LOG);
      if (!exists) {
        await api("POST", "/ssh-sessions/sync", { sessions: [], log_exists: false });
        return;
      }
      const size = fs.statSync(LOG).size;
      let offset = readOffset();
      if (offset > size) offset = 0;
      if (offset === size) {
        await api("POST", "/ssh-sessions/sync", { sessions: [], log_exists: true });
        return;
      }
      const fd = fs.openSync(LOG, "r");
      const buf = Buffer.alloc(size - offset);
      fs.readSync(fd, buf, 0, buf.length, offset);
      fs.closeSync(fd);
      let text = buf.toString("utf8");
      const lastNl = text.lastIndexOf("\n");
      if (lastNl < 0) return;
      text = text.slice(0, lastNl);
      const newOffset = offset + Buffer.byteLength(text, "utf8") + 1;
      const sessions = parseChunk(text);
      const r = await api("POST", "/ssh-sessions/sync", { sessions, log_exists: true });
      if (r && !r.error) writeOffset(newOffset);
      if (sessions.length) log("ssh sync", sessions.length, "sessions");
    } catch (e) { log("ssh-sync", e.message); }
    finally { busy = false; }
  }
  function start() {
    setTimeout(() => tick().catch(() => {}), 20000);
    setInterval(() => tick().catch(() => {}), 60 * 1000);
  }
  return { start, tick, parseChunk };
}

module.exports = { create, parseChunk, LOG };
