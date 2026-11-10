package app

import (
	"regexp"
	"strings"
)

/* Text-Ähnlichkeit für Vorschläge (wie backend/textmatch.js) – ohne externe Dienste. */

var stopWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields("und oder der die das den dem des ein eine einer eines mit von für fuer auf aus bei im in zu zum zur nach über ueber vom am an als auch noch nicht ist sind wird wurde the and for with from to of on in at by is are be ticket aufgabe arbeit") {
		m[w] = true
	}
	return m
}()

var (
	umlauts   = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss")
	nonWord   = regexp.MustCompile(`[^a-z0-9 ]+`)
	allDigits = regexp.MustCompile(`^\d+$`)
	projStop  = map[string]bool{"projekt": true, "projekte": true, "project": true, "projects": true, "gmbh": true, "allgemein": true}
)

func normText(t string) string { return umlauts.Replace(strings.ToLower(t)) }

func toks(t string) []string {
	var out []string
	for _, w := range strings.Fields(nonWord.ReplaceAllString(normText(t), " ")) {
		if len(w) >= 3 && !stopWords[w] && !allDigits.MatchString(w) {
			out = append(out, w)
		}
	}
	return out
}

func gramSet(t string) map[string]bool {
	g := map[string]bool{}
	for _, w := range toks(t) {
		x := " " + w + " "
		for i := 0; i < len(x)-2; i++ {
			g[x[i:i+3]] = true
		}
	}
	return g
}

func setOf(ws []string) map[string]bool {
	m := map[string]bool{}
	for _, w := range ws {
		m[w] = true
	}
	return m
}

// textSim: 0..1, Anteil der kürzeren Wortmenge, die wiederkommt, bzw. Trigramm-Ähnlichkeit (fängt „Firewall-Regeln“ ↔ „Firewallregeln“)
func textSim(a, b string) float64 {
	A, B := setOf(toks(a)), setOf(toks(b))
	if len(A) == 0 || len(B) == 0 {
		return 0
	}
	inter := 0
	for w := range A {
		if B[w] {
			inter++
		}
	}
	jac := float64(inter) / float64(min(len(A), len(B)))
	GA, GB := gramSet(a), gramSet(b)
	gi := 0
	for g := range GA {
		if GB[g] {
			gi++
		}
	}
	dice := 0.0
	if len(GA) > 0 && len(GB) > 0 {
		dice = 2 * float64(gi) / float64(len(GA)+len(GB))
	}
	v := jac
	if dice*0.9 > v {
		v = dice * 0.9
	}
	if v > 1 {
		v = 1
	}
	return v
}

// nameSim für Projektnamen: gemeinsame Firmenwörter und „Projekt“ zählen nicht; Wörter gelten als gleich,
// wenn sie gleich sind oder mit denselben 5 Buchstaben beginnen („Internal“ ↔ „Interne“).
func nameSim(a, b string, common map[string]bool) float64 {
	t := func(x string) []string {
		var out []string
		for _, w := range toks(x) {
			if !projStop[w] && !common[w] {
				out = append(out, w)
			}
		}
		return out
	}
	A, B := t(a), t(b)
	if len(A) == 0 || len(B) == 0 {
		return 0
	}
	same := func(x, y string) bool { return x == y || (len(x) >= 5 && len(y) >= 5 && x[:5] == y[:5]) }
	hit := 0
	for _, x := range A {
		for _, y := range B {
			if same(x, y) {
				hit++
				break
			}
		}
	}
	return float64(hit) / float64(min(len(A), len(B)))
}
