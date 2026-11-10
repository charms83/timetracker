// Ticket-Erkennung (identisch zur Desktop-App).
var DEFAULT_JIRA_HOST = "jira.example.com";
var DEFAULT_PLANE_HOST = "plane.example.com";
var KEY_RE = /\b([A-Z][A-Z0-9_]{1,15}-\d+)\b/;

// hosts: optional {jira, plane} aus /ext/state (null/leer = Standard)
function parseTicket(url, title, hosts) {
  const JIRA_HOST = (hosts && hosts.jira) || DEFAULT_JIRA_HOST;
  const PLANE_HOST = (hosts && hosts.plane) || DEFAULT_PLANE_HOST;
  let u; try { u = new URL(url); } catch (_) { return null; }
  const sys = u.host === JIRA_HOST ? "jira" : u.host === PLANE_HOST ? "plane" : null; if (!sys) return null;
  let key = (u.pathname.match(/\/browse\/([A-Za-z][A-Za-z0-9_]*-\d+)/) || [])[1] || u.searchParams.get("selectedIssue")
    || (u.pathname.match(/\/(?:issues|work-items)\/([A-Za-z][A-Za-z0-9_]*-\d+)/) || [])[1] || (String(title).match(KEY_RE) || [])[1];
  if (!key && sys === "plane") {
    // Plane ohne Nummer in der Adresse: …/projects/<uuid>/issues/<uuid> oder …?peekId=<uuid> → Nummer holt Zeitprotokoll
    const U = "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}";
    const project = u.searchParams.get("peekProjectId") || (u.pathname.match(new RegExp("/projects/(" + U + ")", "i")) || [])[1] || null;
    // Vorschau (halbe Ansicht): peekId/peekIssue – auch ohne Projekt in der Adresse (z. B. „Meine Work-Items“)
    const item = u.searchParams.get("peekId") || u.searchParams.get("peekIssue") || u.searchParams.get("peekIssueId")
      || (u.pathname.match(new RegExp("/(?:issues|work-items)/(" + U + ")", "i")) || [])[1];
    if (item && new RegExp("^" + U + "$", "i").test(item)) return { key: null, system: "plane", project, item, title: "" };
  }
  if (!key) return null; key = key.toUpperCase();
  const name = String(title || "").replace(/^\[[^\]]+\]\s*/, "").replace(new RegExp("^" + key + "[:\\s–-]*", "i"), "")
    .replace(/\s*[-|–]\s*(Jira|Plane|Beispiel[^-|–]*)\s*$/i, "").replace(/\s*[-|–]\s*(Jira|Plane)\s*$/i, "").trim();
  const titled = new RegExp("\\b" + key + "\\b", "i").test(String(title || "")) || /\/browse\//.test(u.pathname);
  return { key, system: sys, title: titled && name && name.toUpperCase() !== key ? name : "" };
}

if (typeof module !== "undefined" && module.exports) module.exports = { parseTicket };
