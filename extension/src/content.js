// Zeitprotokoll – Pille auf Jira-/Plane-Seiten. Kein Netzwerk, nur Nachrichten an den Hintergrund.
(() => {
  const api_ = globalThis.browser ?? globalThis.chrome;
  if (window.top !== window) return;

  let hosts = null, st = null, paired = false, current = null, lastHref = "";
  let host, pill;

  function fmt(startIso) {
    const s = Math.max(0, Math.floor((Date.now() - new Date(startIso).getTime()) / 1000));
    const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
    const p = (n) => String(n).padStart(2, "0");
    return h ? h + ":" + p(m) + ":" + p(sec) : m + ":" + p(sec);
  }

  function domTitle(key) {
    try {
      const h = document.querySelector("h1");
      const t = h && (h.innerText || h.textContent || "").trim().replace(/\s+/g, " ");
      if (t && t.length < 250 && t.toUpperCase() !== key) return t.replace(new RegExp("^" + key + "[:\\s–-]*", "i"), "");
    } catch (_) {}
    return "";
  }

  const resolved = {};   // Plane-uuid → {key,title}
  function detect() {
    const t = parseTicket(location.href, document.title, hosts);
    if (!t) return null;
    if (!t.key && t.item) {
      const c = resolved[t.item];
      if (c === undefined) { resolved[t.item] = null; send({ type: "resolve", project: t.project, item: t.item }).then(r => { resolved[t.item] = r && r.key ? r : false; lastHref = ""; check(); }); return null; }
      if (!c) return null;
      return { key: c.key, system: "plane", title: c.title || "" };
    }
    // DOM-Titel nur auf echten Ticketseiten (nicht Board-Overlay)
    if (!t.title && /\/(browse|issues|work-items)\//.test(location.pathname)) t.title = domTitle(t.key);
    return t;
  }

  function ensure() {
    if (host) return;
    host = document.createElement("div");
    host.id = "zp-pill-host";
    host.style.cssText = "all:initial;position:fixed;right:16px;bottom:16px;z-index:2147483647;";
    const root = host.attachShadow({ mode: "open" });
    const style = document.createElement("style");
    style.textContent = `
      button{font:600 12px/1 system-ui,-apple-system,"Segoe UI",sans-serif;border:0;border-radius:999px;padding:8px 12px;cursor:pointer;
        background:#0F752F;color:#fff;box-shadow:0 2px 8px rgba(0,0,0,.3);opacity:.92;white-space:nowrap}
      button:hover{opacity:1}
      button.run{background:#fff;color:#0F752F;border:1.5px solid #0F752F}
      button.run:hover{background:#b3261e;color:#fff;border-color:#b3261e}
      @media (prefers-color-scheme:dark){button{background:#B4D229;color:#10200a}
        button.run{background:#1b1f14;color:#B4D229;border:1.5px solid #B4D229}
        button.run:hover{background:#b3261e;color:#fff;border-color:#b3261e}}
    `;
    pill = document.createElement("button");
    pill.type = "button";
    root.append(style, pill);
    pill.addEventListener("click", onClick);
    pill.addEventListener("mouseenter", () => { hover = true; render(); });
    pill.addEventListener("mouseleave", () => { hover = false; render(); });
    document.documentElement.appendChild(host);
  }
  let hover = false, busy = false;

  function running() {
    return !!(current && st && st.timer && st.timer.active && st.timer.ticket === current.key);
  }

  function render() {
    if (!paired || !current || !st) { if (host) host.style.display = "none"; return; }
    ensure();
    host.style.display = "";
    if (running()) {
      pill.className = "run";
      pill.textContent = hover ? "■ " + current.key + " stoppen" : "● " + current.key + " läuft " + fmt(st.timer.start_time);
    } else {
      pill.className = "";
      pill.textContent = busy ? "…" : "⏱ " + current.key + " tracken";
    }
  }

  function apply(r) {
    paired = !!(r && r.paired);
    st = r && r.state || null;
    if (st && st.hosts) hosts = st.hosts;
    render();
  }

  async function send(m) {
    try { return await api_.runtime.sendMessage(m); } catch (_) { return null; }
  }
  async function pull() { apply(await send({ type: "state" })); }

  async function onClick() {
    if (busy || !current) return;
    busy = true; render();
    const r = running() ? await send({ type: "stop" }) : await send({ type: "start", key: current.key, title: current.title });
    busy = false; hover = false;
    apply(r);
  }

  function check() {
    if (location.href !== lastHref) {
      lastHref = location.href;
      current = detect();
      render();
      if (current) pull();
    } else if (current && !current.title) {
      // Titel kann nachgeladen werden (SPA)
      const t = detect();
      if (t && t.key === current.key && t.title) current = t;
    }
  }

  api_.runtime.onMessage.addListener((m, _s, send) => {
    if (m && m.type === "getTicket") { current = detect(); send(current ? { key: current.key, title: current.title, system: current.system } : null); }
  });

  setInterval(check, 1000);
  setInterval(() => { if (current && !document.hidden) pull(); }, 30000);
  setInterval(() => { if (running() && !document.hidden) render(); }, 1000);
  check();
})();
