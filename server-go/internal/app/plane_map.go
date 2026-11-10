package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Zuordnung Jira-Ticket (Tempo-Altdaten) → Plane-Work-Item (wie backend/plane.js map/suggest, map/apply).
   Danach gehen die Tempo-Importe des Tickets mit der normalen Plane-Übertragung in die Summe des Work-Items.
   Ein neues Work-Item entsteht nur mit Bestätigung (create + confirm) und höchstens einmal je Jira-Ticket. */

func (a *App) routesPlaneMap(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/plane/map/suggest", a.auth(a.planeMapSuggest))
	mux.HandleFunc("POST /api/integrations/plane/map/apply", a.auth(a.planeMapApply))
}

var nodeWordRE = regexp.MustCompile(`[^a-z0-9äöüß ]+`)

// nodeWords/nodeSim = Node words/similarity (Jaccard über Wörter ab 3 Zeichen)
func nodeWords(t string) []string {
	var out []string
	for _, w := range strings.Fields(nodeWordRE.ReplaceAllString(strings.ToLower(t), " ")) {
		if len([]rune(w)) >= 3 {
			out = append(out, w)
		}
	}
	return out
}

func nodeSim(a, b string) float64 {
	A, B := setOf(nodeWords(a)), setOf(nodeWords(b))
	if len(A) == 0 || len(B) == 0 {
		return 0
	}
	inter := 0
	for w := range A {
		if B[w] {
			inter++
		}
	}
	return float64(inter) / float64(len(A)+len(B)-inter)
}

type mapHit struct {
	ID    string  `json:"id"`
	Key   string  `json:"key"`
	Name  string  `json:"name"`
	Via   string  `json:"via"`
	Score float64 `json:"score"`
}

// mapScore = Node: Basis + 0,6·Ähnlichkeit + 0,4 wenn der Jira-Key im Namen steht (höchstens 1, 2 Stellen)
func mapScore(key, title, name string, base float64) float64 {
	sim := 0.0
	if title != "" {
		sim = nodeSim(title, name)
	}
	kin := 0.0
	if name != "" && strings.Contains(strings.ToUpper(name), key) {
		kin = 0.4
	}
	return math.Round(math.Min(1, base+sim*0.6+kin)*100) / 100
}

func (a *App) planeMapSuggest(w http.ResponseWriter, r *http.Request, u User) {
	key := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("key")))
	if !jiraKeyRE.MatchString(key) {
		badRequest(w, "Ungültiger Ticket-Key")
		return
	}
	title := strings.TrimSpace(r.URL.Query().Get("title"))
	ctx := r.Context()
	cfgs, err := a.planeCfgsForQuery(ctx, u.ID, r.URL.Query().Get("workspace"))
	if err != nil {
		a.fail(w, err)
		return
	}
	found := map[string]mapHit{}
	add := func(id, k, name, via string, base float64) {
		if id == "" || k == "" {
			return
		}
		h := mapHit{ID: id, Key: k, Name: name, Via: via, Score: mapScore(key, title, name, base)}
		if cur, ok := found[id]; !ok || h.Score > cur.Score {
			found[id] = h
		}
	}
	queries := []string{key}
	if ws := nodeWords(title); len(ws) > 0 {
		queries = append(queries, strings.Join(firstN(ws, 6), " "))
	}
	for _, c := range cfgs {
		if !c.ok() {
			continue
		}
		if wi, err := a.planeDo(ctx, u.ID, c, "GET", c.wsPath()+"/work-items/"+url.PathEscape(key)+"/", nil, planeOpts{Interactive: true}); err == nil {
			add(asStr(jm(wi)["id"]), key, asStr(jm(wi)["name"]), "gleicher Key in Plane", 0.2)
		} else {
			var pe *planeErr
			if (!errors.As(err, &pe) || pe.Status != 404) && len(cfgs) == 1 {
				a.planeFail(w, err)
				return
			}
		}
		for _, q := range queries {
			res, err := a.planeDo(ctx, u.ID, c, "GET", c.wsPath()+"/work-items/search/?search="+url.QueryEscape(q)+"&limit=6&workspace_search=true", nil, planeOpts{Interactive: true})
			if err != nil {
				if len(cfgs) == 1 {
					a.planeFail(w, err)
					return
				}
				continue
			}
			for _, i := range listOf(res) {
				via := "ähnlicher Titel"
				if q == key {
					via = "Key im Text"
				}
				add(asStr(i["id"]), strings.ToUpper(asStr(i["project__identifier"]))+"-"+asStr(i["sequence_id"]), asStr(i["name"]), via, 0)
			}
		}
	}
	out := make([]mapHit, 0, len(found))
	for _, h := range found {
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > 5 {
		out = out[:5]
	}
	writeJSON(w, http.StatusOK, out)
}

// planeMapApply = Node POST …/map/apply {jira_key, plane_key | create+local_project_id+confirm | skip | undo}
func (a *App) planeMapApply(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	jiraKey := strings.ToUpper(strings.TrimSpace(asStr(in["jira_key"])))
	if !jiraKeyRE.MatchString(jiraKey) {
		badRequest(w, "Ungültiger Jira-Key")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancel()
	switch {
	case truthy(in["undo"]):
		err := a.inTx(ctx, func(tx pgx.Tx) error {
			if err := a.taskLockCheck(ctx, tx, u.ID, `jira_origin=$2`, jiraKey); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE time_entries SET plane_include=0, plane_sum_key=NULL WHERE user_id=$1 AND source IN ('tempo','tempo_csv')
				AND task_id IN (SELECT id FROM tasks WHERE user_id=$1 AND jira_origin=$2)`, u.ID, jiraKey); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE tasks SET jira_ticket=jira_origin, jira_origin=NULL, plane_work_item_id=NULL, plane_project_id=NULL WHERE user_id=$1 AND jira_origin=$2`, u.ID, jiraKey); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `DELETE FROM ticket_map WHERE user_id=$1 AND jira_key=$2`, u.ID, jiraKey)
			return err
		})
		if err != nil {
			a.setupFail(w, err) // Sperre/ZP001 → 409
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	case truthy(in["skip"]):
		if _, err := a.DB.Exec(ctx, `INSERT INTO ticket_map(user_id,jira_key,status) VALUES($1,$2,'skipped')
			ON CONFLICT (user_id,jira_key) DO UPDATE SET status='skipped', plane_key=NULL, plane_work_item_id=NULL, plane_project_id=NULL
			WHERE ticket_map.status NOT IN ('creating','created')`, u.ID, jiraKey); err != nil { // angelegtes Work-Item nicht vergessen
			a.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	var c planeCfg
	var err error
	if lid := asID(in["local_project_id"]); lid != nil {
		c, err = a.planeCfgForProject(ctx, u.ID, lid.(int64))
	} else if ws := asStr(in["workspace_id"]); ws != "" {
		c, err = a.planeCfgSelect(ctx, u.ID, ws)
	} else {
		c, err = a.planeCfg(ctx, u.ID)
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	planeKey := strings.ToUpper(strings.TrimSpace(asStr(in["plane_key"])))
	if truthy(in["create"]) {
		if in["confirm"] != true {
			badRequest(w, "Neues Work-Item nur mit Bestätigung (confirm: true)")
			return
		}
		if planeKey, err = a.mapCreateOnce(ctx, u.ID, c, jiraKey, asID(in["local_project_id"])); err != nil {
			var ue errUser
			var ce errConflict
			switch {
			case errors.As(err, &ue):
				badRequest(w, string(ue))
			case errors.As(err, &ce):
				writeJSON(w, http.StatusConflict, map[string]any{"error": string(ce)})
			default:
				a.planeFail(w, err)
			}
			return
		}
	}
	if !jiraKeyRE.MatchString(planeKey) {
		badRequest(w, "Plane-Key im Format ABC-123 angeben")
		return
	}
	out, err := a.applyMap(ctx, u.ID, c, jiraKey, planeKey)
	if err != nil {
		var ce errConflict
		if errors.As(err, &ce) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": string(ce), "code": "month_locked"})
			return
		}
		var ue errUser
		if errors.As(err, &ue) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": string(ue)})
			return
		}
		var pe *planeErr
		if errors.As(err, &pe) {
			a.planeFail(w, err)
			return
		}
		a.fail(w, err)
		return
	}
	a.sysLog(ctx, u.ID, "success", "plane", fmt.Sprintf("Zuordnung: %s → %s", jiraKey, planeKey))
	writeJSON(w, http.StatusOK, out)
}

// mapTitle = Node: Titel der Aufgabe, sonst „KEY (aus Jira übernommen)“
func mapTitle(jiraKey, title string) string {
	if title != "" && strings.ToUpper(title) != jiraKey {
		return title
	}
	return jiraKey + " (aus Jira übernommen)"
}

// mapCreateOnce: neues Work-Item für das Jira-Ticket – höchstens eins. Anspruch über ticket_map (status creating);
// nach dem Anlegen sofort als „created“ mit Key gemerkt (Wiederholung nimmt dasselbe); bei unklarem Ergebnis bleibt der Anspruch.
func (a *App) mapCreateOnce(ctx context.Context, uid int64, c planeCfg, jiraKey string, pid any) (string, error) {
	var st, have string
	if err := a.DB.QueryRow(ctx, `SELECT status, COALESCE(plane_key,'') FROM ticket_map WHERE user_id=$1 AND jira_key=$2`, uid, jiraKey).Scan(&st, &have); err == nil && st == "created" && have != "" {
		return have, nil // schon angelegt, nur das Zuordnen fehlte
	}
	if pid == nil {
		return "", errUser("Projekt für das neue Work-Item wählen (mit Plane verknüpft)")
	}
	p, err := a.planeProjectOf(ctx, uid, pid.(int64))
	if err != nil {
		return "", errUser("Projekt für das neue Work-Item wählen (mit Plane verknüpft)")
	}
	if err := a.mapPrecheck(ctx, uid, jiraKey); err != nil { // nie ein Work-Item anlegen, das nicht zugeordnet werden kann
		return "", err
	}
	var title string
	_ = a.DB.QueryRow(ctx, `SELECT COALESCE(MAX(title),'') FROM tasks WHERE user_id=$1 AND UPPER(TRIM(jira_ticket))=$2`, uid, jiraKey).Scan(&title)
	title = mapTitle(jiraKey, title)
	var stale string
	err = a.DB.QueryRow(ctx, `INSERT INTO ticket_map(user_id,jira_key,status,title,mapped_at) VALUES($1,$2,'creating',$3,now())
		ON CONFLICT (user_id,jira_key) DO UPDATE SET status='creating', mapped_at=now()
		WHERE ticket_map.status NOT IN ('creating','created','mapped') OR (ticket_map.status='creating' AND ticket_map.mapped_at < now() - interval '10 minutes')
		RETURNING CASE WHEN xmax = 0 THEN 'neu' ELSE 'übernommen' END`, uid, jiraKey, title).Scan(&stale)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errConflict("Für " + jiraKey + " wird gerade ein Work-Item angelegt oder es ist schon zugeordnet")
	}
	if err != nil {
		return "", err
	}
	release := func() { // nur, wenn Plane sicher nichts angelegt hat
		_, _ = a.DB.Exec(ctx, `DELETE FROM ticket_map WHERE user_id=$1 AND jira_key=$2 AND status='creating'`, uid, jiraKey)
	}
	// vielleicht schon angelegt (abgebrochener Versuch) → erst suchen; ohne gelungene Suche nicht anlegen
	res, err := a.planeDo(ctx, uid, c, "GET", c.wsPath()+"/work-items/search/?search="+url.QueryEscape(jiraKey)+"&limit=10&workspace_search=true", nil, planeOpts{})
	if err != nil {
		if planeRejected(err) {
			release()
		}
		return "", err
	}
	for _, i := range listOf(res) {
		if strings.TrimSpace(asStr(i["name"])) == title && asStr(i["project_id"]) == p.PlaneProject {
			key := strings.ToUpper(asStr(i["project__identifier"])) + "-" + asStr(i["sequence_id"])
			a.markCreated(ctx, uid, jiraKey, key, asStr(i["id"]), p.PlaneProject)
			return key, nil
		}
	}
	it, err := a.createWorkItem(ctx, uid, c, p, title, "Übernommen aus Jira: "+jiraKey)
	if err != nil {
		if planeRejected(err) {
			release()
		}
		return "", err
	}
	if it.Key == "" {
		return "", errors.New("Plane hat keinen Key geliefert – Work-Item " + it.ID + " bitte von Hand zuordnen")
	}
	a.markCreated(ctx, uid, jiraKey, it.Key, it.ID, p.PlaneProject)
	return it.Key, nil
}

func (a *App) markCreated(ctx context.Context, uid int64, jiraKey, key, id, project string) {
	if _, err := a.DB.Exec(ctx, `UPDATE ticket_map SET status='created', plane_key=$3, plane_work_item_id=$4, plane_project_id=$5 WHERE user_id=$1 AND jira_key=$2`,
		uid, jiraKey, key, id, project); err != nil {
		a.sysLog(ctx, uid, "error", "plane", fmt.Sprintf("Work-Item %s für %s angelegt, nicht gemerkt (%v)", key, jiraKey, err))
	}
}

// planeRejected: Plane hat sicher nichts angelegt (4xx außer 429-Wiederholung)
func planeRejected(err error) bool {
	var pe *planeErr
	return errors.As(err, &pe) && pe.Status >= 400 && pe.Status < 500
}

// applyMap = Node applyMap: Aufgaben des Jira-Tickets aufs Work-Item hängen, Tempo-Importe in die Plane-Summe nehmen
func (a *App) applyMap(ctx context.Context, uid int64, c planeCfg, jiraKey, planeKey string) (map[string]any, error) {
	wi, err := a.planeDo(ctx, uid, c, "GET", c.wsPath()+"/work-items/"+url.PathEscape(planeKey)+"/", nil, planeOpts{})
	if err != nil {
		var pe *planeErr
		if errors.As(err, &pe) && pe.Status == 404 {
			return nil, errUser("Work-Item " + planeKey + " gibt es in Plane nicht")
		}
		return nil, err
	}
	m := jm(wi)
	id, project, name := asStr(m["id"]), jsOr(asStr(m["project"]), asStr(m["project_id"])), asStr(m["name"])
	if id == "" || project == "" {
		return nil, errUser("Work-Item " + planeKey + " nicht gefunden")
	}
	err = a.inTx(ctx, func(tx pgx.Tx) error {
		if err := a.taskLockCheck(ctx, tx, uid, `UPPER(TRIM(jira_ticket))=$2`, jiraKey); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE tasks SET jira_origin=COALESCE(jira_origin, jira_ticket), jira_ticket=$3, plane_work_item_id=$4, plane_project_id=$5,
			title=CASE WHEN title IS NULL OR UPPER(TRIM(title))=$2 THEN $6 ELSE title END
			WHERE user_id=$1 AND UPPER(TRIM(jira_ticket))=$2`, uid, jiraKey, planeKey, id, project, jsOr(name, planeKey))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errUser("Keine Aufgabe zu " + jiraKey + " gefunden")
		}
		if _, err := tx.Exec(ctx, `UPDATE time_entries SET plane_include=1, plane_sum_key=NULL WHERE user_id=$1 AND source IN ('tempo','tempo_csv')
			AND task_id IN (SELECT id FROM tasks WHERE user_id=$1 AND jira_origin=$2)`, uid, jiraKey); err != nil {
			return err // ZP001: Monat eingereicht/freigegeben
		}
		_, err = tx.Exec(ctx, `INSERT INTO ticket_map(user_id,jira_key,status,plane_key,plane_work_item_id,plane_project_id,title,mapped_at) VALUES($1,$2,'mapped',$3,$4,$5,$6,now())
			ON CONFLICT (user_id,jira_key) DO UPDATE SET status='mapped', plane_key=EXCLUDED.plane_key, plane_work_item_id=EXCLUDED.plane_work_item_id,
			plane_project_id=EXCLUDED.plane_project_id, title=EXCLUDED.title, mapped_at=now()`, uid, jiraKey, planeKey, id, project, nz(name))
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"jira_key": jiraKey, "plane_key": planeKey, "name": name}, nil
}

// taskLockCheck: Aufgaben (Bedingung mit $1 = Person, $2 = Wert) umzuhängen, nur wenn keine Buchung eines eingereichten/
// freigegebenen Monats dadurch anders gebucht würde (gemeinsame Sperre wie zp_month_locked)
func (a *App) taskLockCheck(ctx context.Context, tx pgx.Tx, uid int64, where, val string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, int64(7270030000000000)+uid); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM tasks WHERE user_id=$1 AND `+where, uid, val)
	if err != nil {
		return err
	}
	var list []ticketEntry
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			list = append(list, ticketEntry{TaskID: id})
		}
	}
	rows.Close()
	if len(list) == 0 {
		return nil
	}
	return a.lockedConflict(ctx, tx, uid, "task", list, "")
}

// mapPrecheck: vor dem Anlegen eines Work-Items – gibt es Aufgaben und ist kein gesperrter Monat betroffen?
func (a *App) mapPrecheck(ctx context.Context, uid int64, jiraKey string) error {
	return a.inTx(ctx, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM tasks WHERE user_id=$1 AND UPPER(TRIM(jira_ticket))=$2`, uid, jiraKey).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return errUser("Keine Aufgabe zu " + jiraKey + " gefunden")
		}
		return a.taskLockCheck(ctx, tx, uid, `UPPER(TRIM(jira_ticket))=$2`, jiraKey)
	})
}
