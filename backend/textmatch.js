/* Text-Ähnlichkeit für Vorschläge (Tickets, Termine, Buchungen) – ohne externe Dienste */
const STOP = new Set("und oder der die das den dem des ein eine einer eines mit von für fuer auf aus bei im in zu zum zur nach über ueber vom am an als auch noch nicht ist sind wird wurde the and for with from to of on in at by is are be ticket aufgabe arbeit".split(" "));
const norm = t => String(t || "").toLowerCase().replace(/ä/g, "ae").replace(/ö/g, "oe").replace(/ü/g, "ue").replace(/ß/g, "ss");
const toks = t => norm(t).replace(/[^a-z0-9 ]+/g, " ").split(/\s+/).filter(w => w.length >= 3 && !STOP.has(w) && !/^\d+$/.test(w));
const grams = t => { const g = new Set(); for (const w of toks(t)) { const x = ` ${w} `; for (let i = 0; i < x.length - 2; i++) g.add(x.slice(i, i + 3)); } return g; };
function textSim(a, b) {
const A = new Set(toks(a)), B = new Set(toks(b)); if (!A.size || !B.size) return 0;
  let inter = 0; for (const w of A) if (B.has(w)) inter++;
const jac = inter / Math.min(A.size, B.size);                              // Anteil der kürzeren Seite, der wiederkommt
const GA = grams(a), GB = grams(b); let gi = 0; for (const g of GA) if (GB.has(g)) gi++;
const dice = GA.size && GB.size ? 2 * gi / (GA.size + GB.size) : 0;      // fängt „Firewall-Regeln“ ↔ „Firewallregeln“
  return Math.min(1, Math.max(jac, dice * 0.9));
}

/* Projektnamen: gemeinsame Firmenwörter (z. B. „Beispiel“, in vielen Namen) und „Projekt“ zählen nicht;
   Wörter gelten als gleich, wenn sie gleich sind oder mit denselben 5 Buchstaben beginnen („Internal“ ↔ „Interne“). */
const PSTOP = new Set(["projekt", "projekte", "project", "projects", "gmbh", "allgemein"]);
function nameSim(a, b, common = new Set()) {
  const t = x => toks(x).filter(w => !PSTOP.has(w) && !common.has(w));
  const A = t(a), B = t(b); if (!A.length || !B.length) return 0;
  const same = (x, y) => x === y || (x.length >= 5 && y.length >= 5 && x.slice(0, 5) === y.slice(0, 5));
  const hit = A.filter(x => B.some(y => same(x, y))).length;
  return hit / Math.min(A.length, B.length);
}

module.exports = { toks, textSim, norm, nameSim };
