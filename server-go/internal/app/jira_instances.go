package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"example/zeitprotokoll-server/internal/crypto"
)

/* Jira-Instanzen (mehrere Jira-Server), vom Administrator verwaltet (Migration 014). Jede Person trägt je Instanz
   genau EIN Jira-Token (PAT) ein; Tempo nutzt dasselbe Token. Das Chiffrat ist an Person, Art, Instanz-Id und
   Schema+Host der Instanz gebunden – ein Blob lässt sich weder für eine andere Person noch Instanz noch Adresse öffnen.
   Stufe jetzt: nur kind 'dc' mit tempo 'dc'/'none' ist einschaltbar; Cloud lässt sich anlegen, bleibt aber aus („folgt“). */

type jiraInstance struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	BaseURL   string `json:"base_url"`
	Tempo     string `json:"tempo"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
}

// usable: in dieser Stufe einschaltbar
func instanceUsable(kind, tempo string) bool {
	return kind == "dc" && (tempo == "dc" || tempo == "none")
}

func instanceNote(kind, tempo string) string {
	if instanceUsable(kind, tempo) {
		return ""
	}
	return "Jira Cloud / Tempo Cloud folgt – noch nicht einschaltbar"
}

// JiraInstanceInput: nil = unverändert (PUT) bzw. Standard (POST)
type JiraInstanceInput struct {
	Name, Kind, BaseURL, Tempo *string
	Enabled                    *bool
	// SameSystem: neue Adresse für DASSELBE Jira (Umzug/Umbenennung) – nötig, wenn Buchungen auf die Instanz verweisen
	SameSystem bool
}

var errInstanceNotFound = errors.New("Jira-Instanz nicht gefunden")

// jiraTokenAAD bindet das Token an Person, Instanz und Schema+Host der Instanz
func jiraTokenAAD(uid, instID int64, baseURL string) []byte {
	return []byte(fmt.Sprintf("u%d|jira_token|i%d|%s", uid, instID, planeScope(baseURL)))
}

const instSettingPrefix = "i" // Schlüssel je Person und Instanz: „i<Id>:<Name>“ (über /api/settings nicht erreichbar)

func instKey(id int64, key string) string {
	return instSettingPrefix + strconv.FormatInt(id, 10) + ":" + key
}

// vom Server gepflegte Werte je Instanz – werden bei Adresswechsel verworfen
var instDerivedKeys = []string{"jira_username", "jira_userkey", "jira_project_keys", "jira_project_keys_at"}

// von der Person je Instanz änderbare Werte
var instUserKeys = []string{"tempo_account", "tempo_account_attr_id", "tempo_booking_issue", "jira_new_project", "jira_new_issuetype", "tempo_push_since"}

/* ── Prüfen ─────────────────────────────────────────────────────── */

func (a *App) checkInstance(in jiraInstance) (jiraInstance, error) {
	in.Name = strings.TrimSpace(in.Name)
	if n := len([]rune(in.Name)); n < 1 || n > 60 {
		return in, errUser("Name: 1 bis 60 Zeichen")
	}
	if in.Kind != "dc" && in.Kind != "cloud" {
		return in, errUser("Art: dc oder cloud")
	}
	if in.Tempo != "dc" && in.Tempo != "cloud" && in.Tempo != "none" {
		return in, errUser("Tempo: dc, cloud oder none")
	}
	u, err := a.validateJiraURL(in.BaseURL)
	if err != nil {
		return in, errUser(err.Error())
	}
	if u == "" {
		return in, errUser("Adresse fehlt")
	}
	in.BaseURL = u
	if in.Enabled && !instanceUsable(in.Kind, in.Tempo) {
		return in, errUser(instanceNote(in.Kind, in.Tempo))
	}
	return in, nil
}

func applyInput(cur jiraInstance, in JiraInstanceInput) jiraInstance {
	if in.Name != nil {
		cur.Name = *in.Name
	}
	if in.Kind != nil {
		cur.Kind = strings.ToLower(strings.TrimSpace(*in.Kind))
	}
	if in.BaseURL != nil {
		cur.BaseURL = *in.BaseURL
	}
	if in.Tempo != nil {
		cur.Tempo = strings.ToLower(strings.TrimSpace(*in.Tempo))
	}
	if in.Enabled != nil {
		cur.Enabled = *in.Enabled
	}
	return cur
}

func uniqueErr(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return errUser("Name schon vergeben")
	}
	if errors.As(err, &pe) && pe.Code == "23514" {
		return errUser("Angaben ungültig (Cloud ist noch nicht einschaltbar)")
	}
	return err
}

/* ── Lesen ──────────────────────────────────────────────────────── */

const instCols = `id, name, kind, base_url, tempo, enabled, to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS')`

func scanInstance(row pgx.Row) (jiraInstance, error) {
	var i jiraInstance
	err := row.Scan(&i.ID, &i.Name, &i.Kind, &i.BaseURL, &i.Tempo, &i.Enabled, &i.CreatedAt)
	return i, err
}

// JiraInstances: alle Instanzen (auch ausgeschaltete), nach Name
func (a *App) JiraInstances(ctx context.Context) ([]jiraInstance, error) {
	rows, err := a.DB.Query(ctx, `SELECT `+instCols+` FROM jira_instances ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []jiraInstance{}
	for rows.Next() {
		i, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (a *App) jiraInstance(ctx context.Context, q Queryer, id int64, lock bool) (jiraInstance, error) {
	sql := `SELECT ` + instCols + ` FROM jira_instances WHERE id=$1`
	if lock {
		sql += " FOR UPDATE"
	}
	i, err := scanInstance(q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return i, errInstanceNotFound
	}
	return i, err
}

// JiraInstanceFind: Id oder Name (Befehlszeile)
func (a *App) JiraInstanceFind(ctx context.Context, idOrName string) (jiraInstance, error) {
	if id, err := strconv.ParseInt(idOrName, 10, 64); err == nil {
		return a.jiraInstance(ctx, a.DB, id, false)
	}
	i, err := scanInstance(a.DB.QueryRow(ctx, `SELECT `+instCols+` FROM jira_instances WHERE lower(btrim(name))=lower(btrim($1))`, idOrName))
	if errors.Is(err, pgx.ErrNoRows) {
		return i, errInstanceNotFound
	}
	return i, err
}

// instanceRefs: Buchungen, Löschaufträge und Übergangs-Tickets, die auf die Instanz verweisen
func (a *App) instanceRefs(ctx context.Context, q Queryer, id int64) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM time_entries WHERE tempo_push_instance=$1 OR tempo_target_instance=$1)
		+ (SELECT COUNT(*) FROM tempo_push_deletions WHERE instance_id=$1)
		+ (SELECT COUNT(*) FROM tempo_month_tickets WHERE instance_id=$1)`, id).Scan(&n)
	return n, err
}

/* ── Ändern (Admin-API und Befehlszeile) ────────────────────────── */

func (a *App) JiraInstanceCreate(ctx context.Context, in JiraInstanceInput) (jiraInstance, error) {
	cur := applyInput(jiraInstance{Kind: "dc", Tempo: "dc"}, in)
	cur, err := a.checkInstance(cur)
	if err != nil {
		return cur, err
	}
	if err := a.hostTaken(ctx, cur.BaseURL, 0); err != nil {
		return cur, err
	}
	out, err := scanInstance(a.DB.QueryRow(ctx, `INSERT INTO jira_instances(name,kind,base_url,tempo,enabled) VALUES($1,$2,$3,$4,$5) RETURNING `+instCols,
		cur.Name, cur.Kind, cur.BaseURL, cur.Tempo, cur.Enabled))
	return out, uniqueErr(err)
}

// hostTaken: zwei Instanzen mit demselben Schema+Host machten jedes Ticket mehrdeutig
func (a *App) hostTaken(ctx context.Context, base string, self int64) error {
	list, err := a.JiraInstances(ctx)
	if err != nil {
		return err
	}
	for _, i := range list {
		if i.ID != self && planeScope(i.BaseURL) == planeScope(base) {
			return errUser("Diese Adresse hat schon die Jira-Instanz „" + i.Name + "“")
		}
	}
	return nil
}

// JiraInstanceUpdate ändert eine Instanz. Wechselt Schema oder Host, werden NUR die Tokens dieser Instanz und die vom
// Server gepflegten Werte je Person (Benutzer, Projektkürzel) sowie ihre Monatssperren gelöscht.
func (a *App) JiraInstanceUpdate(ctx context.Context, id int64, in JiraInstanceInput) (jiraInstance, int64, error) {
	var out jiraInstance
	var cleared int64
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		cur, err := a.jiraInstance(ctx, tx, id, true)
		if err != nil {
			return err
		}
		next, err := a.checkInstance(applyInput(cur, in))
		if err != nil {
			return err
		}
		if next.Kind != cur.Kind {
			if n, err := a.instanceRefs(ctx, tx, id); err != nil {
				return err
			} else if n > 0 {
				return errUser("Art ist nicht änderbar, solange Buchungen auf die Instanz verweisen")
			}
		}
		if planeScope(next.BaseURL) != planeScope(cur.BaseURL) {
			if err := a.hostTaken(ctx, next.BaseURL, id); err != nil {
				return err
			}
			// vorhandene Worklog-Ids gehören zum alten System: auf ein anderes System nie weiterverwenden
			if n, err := a.instanceRefs(ctx, tx, id); err != nil {
				return err
			} else if n > 0 && !in.SameSystem {
				return errUser(fmt.Sprintf("%d Buchungen/Löschaufträge verweisen auf diese Instanz. Anderes Jira → neue Instanz anlegen und diese ausschalten; "+
					"nur wenn es DASSELBE Jira unter neuer Adresse ist, mit same_system bestätigen", n))
			}
			if cleared, err = a.dropInstanceTokens(ctx, tx, id); err != nil {
				return err
			}
		}
		out, err = scanInstance(tx.QueryRow(ctx, `UPDATE jira_instances SET name=$2, kind=$3, base_url=$4, tempo=$5, enabled=$6 WHERE id=$1 RETURNING `+instCols,
			id, next.Name, next.Kind, next.BaseURL, next.Tempo, next.Enabled))
		return uniqueErr(err)
	})
	return out, cleared, err
}

func (a *App) dropInstanceTokens(ctx context.Context, tx pgx.Tx, id int64) (int64, error) {
	tag, err := tx.Exec(ctx, `DELETE FROM user_secrets WHERE kind='jira_token' AND scope=$1`, id)
	if err != nil {
		return 0, err
	}
	keys := make([]string, 0, len(instDerivedKeys))
	for _, k := range instDerivedKeys {
		keys = append(keys, instKey(id, k))
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_settings WHERE key = ANY($1)`, keys); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM tempo_months WHERE instance_id=$1`, id); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

type errInUse struct{ Refs int64 }

func (e errInUse) Error() string {
	return fmt.Sprintf("Noch %d Verweise (übertragene Buchungen, Löschaufträge, Übergangs-Tickets) – nur ausschalten oder ausdrücklich mit force löschen", e.Refs)
}

// JiraInstanceDelete löscht eine Instanz samt Tokens und Einstellungen je Person. Verweisen noch Daten darauf, nur mit force.
func (a *App) JiraInstanceDelete(ctx context.Context, id int64, force bool) (jiraInstance, int64, error) {
	var cur jiraInstance
	var tokens int64
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		if cur, err = a.jiraInstance(ctx, tx, id, true); err != nil {
			return err
		}
		n, err := a.instanceRefs(ctx, tx, id)
		if err != nil {
			return err
		}
		if n > 0 && !force {
			return errInUse{n}
		}
		if tokens, err = a.dropInstanceTokens(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_settings WHERE key LIKE $1`, instSettingPrefix+strconv.FormatInt(id, 10)+":%"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM tempo_month_tickets WHERE instance_id=$1`, id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM jira_instances WHERE id=$1`, id) // Buchungen behalten Worklog-Id, Instanz → NULL (nie neu angelegt)
		return err
	})
	return cur, tokens, err
}

/* ── Token je Person und Instanz ────────────────────────────────── */

// hasJiraToken: Zeile vorhanden (auch wenn nicht lesbar)
func (a *App) hasJiraToken(ctx context.Context, uid, instID int64) bool {
	var ok bool
	_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_secrets WHERE user_id=$1 AND kind='jira_token' AND scope=$2)`, uid, instID).Scan(&ok)
	return ok
}

// jiraToken: entschlüsseltes Token der Person für die Instanz; "" wenn keins
func (a *App) jiraToken(ctx context.Context, uid int64, inst jiraInstance) (string, error) {
	var blob []byte
	if err := a.DB.QueryRow(ctx, `SELECT ciphertext FROM user_secrets WHERE user_id=$1 AND kind='jira_token' AND scope=$2`, uid, inst.ID).Scan(&blob); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	b, err := openJiraToken(a.Key, blob, uid, inst)
	return string(b), err
}

// openJiraToken: nur mit voller Bindung (alte Blobs versiegelt der Selbsttest beim Start neu)
func openJiraToken(key, blob []byte, uid int64, inst jiraInstance) ([]byte, error) {
	if crypto.IsLegacy(blob) {
		return nil, errors.New("Token im alten Format – Selbsttest abwarten oder neu eintragen")
	}
	return crypto.OpenAAD(key, blob, jiraTokenAAD(uid, inst.ID, inst.BaseURL))
}

func (a *App) setJiraToken(ctx context.Context, uid int64, inst jiraInstance, value string) error {
	blob, err := crypto.SealAAD(a.Key, []byte(value), jiraTokenAAD(uid, inst.ID, inst.BaseURL))
	if err != nil {
		return err
	}
	if _, err := a.DB.Exec(ctx, `INSERT INTO user_secrets(user_id,kind,scope,ciphertext) VALUES($1,'jira_token',$2,$3)
		ON CONFLICT (user_id,kind,scope) DO UPDATE SET ciphertext=EXCLUDED.ciphertext, updated_at=now()`, uid, inst.ID, blob); err != nil {
		return err
	}
	// neues Token kann zu einem anderen Jira-Benutzer gehören
	_, err = a.DB.Exec(ctx, `DELETE FROM user_settings WHERE user_id=$1 AND key = ANY($2)`, uid, []string{instKey(inst.ID, "jira_username"), instKey(inst.ID, "jira_userkey")})
	return err
}

func (a *App) deleteJiraToken(ctx context.Context, uid, instID int64) error {
	_, err := a.DB.Exec(ctx, `DELETE FROM user_secrets WHERE user_id=$1 AND kind='jira_token' AND scope=$2`, uid, instID)
	return err
}

// tokenInstance: Instanz für Token-Aufrufe ohne Angabe – nur eindeutig, wenn genau eine eingeschaltet ist
func (a *App) tokenInstance(ctx context.Context, raw string, needEnabled bool) (jiraInstance, error) {
	if raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return jiraInstance{}, errUser("instance: Id der Jira-Instanz")
		}
		i, err := a.jiraInstance(ctx, a.DB, id, false)
		if err != nil {
			return i, err
		}
		if needEnabled && !i.Enabled {
			return i, errUser("Jira-Instanz „" + i.Name + "“ ist ausgeschaltet")
		}
		return i, nil
	}
	list, err := a.JiraInstances(ctx)
	if err != nil {
		return jiraInstance{}, err
	}
	var on []jiraInstance
	for _, i := range list {
		if i.Enabled {
			on = append(on, i)
		}
	}
	switch len(on) {
	case 1:
		return on[0], nil
	case 0:
		return jiraInstance{}, errUser("Keine Jira-Instanz eingeschaltet (Administrator)")
	}
	return jiraInstance{}, errUser("Mehrere Jira-Instanzen – bitte instance=<Id> angeben (GET /api/jira/instances)")
}

/* ── Routen ─────────────────────────────────────────────────────── */

func (a *App) routesJiraInstances(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/jira-instances", a.strictAdmin(a.adminJiraList))
	mux.HandleFunc("POST /api/admin/jira-instances", a.strictAdmin(a.adminJiraCreate))
	mux.HandleFunc("PUT /api/admin/jira-instances/{id}", a.strictAdmin(a.adminJiraUpdate))
	mux.HandleFunc("DELETE /api/admin/jira-instances/{id}", a.strictAdmin(a.adminJiraDelete))
	mux.HandleFunc("GET /api/jira/instances", a.tempoGuard(a.jiraInstancesMine))
	mux.HandleFunc("GET /api/jira/instances/{id}/settings", a.tempoGuard(a.jiraInstanceSettingsGet))
	mux.HandleFunc("PUT /api/jira/instances/{id}/settings", a.tempoGuard(a.jiraInstanceSettingsPut))
}

func (a *App) instFail(w http.ResponseWriter, err error) {
	var ue errUser
	var iu errInUse
	switch {
	case errors.As(err, &ue):
		badRequest(w, string(ue))
	case errors.As(err, &iu):
		writeJSON(w, http.StatusConflict, map[string]any{"error": iu.Error(), "refs": iu.Refs})
	case errors.Is(err, errInstanceNotFound):
		notFound(w)
	default:
		a.fail(w, err)
	}
}

func readInstanceInput(w http.ResponseWriter, r *http.Request) (JiraInstanceInput, bool) {
	var in body
	if !readJSON(w, r, &in) {
		return JiraInstanceInput{}, false
	}
	var out JiraInstanceInput
	str := func(k string) *string {
		if v, ok := in[k]; ok {
			s := asStr(v)
			return &s
		}
		return nil
	}
	out.Name, out.Kind, out.BaseURL, out.Tempo = str("name"), str("kind"), str("base_url"), str("tempo")
	if v, ok := in["enabled"]; ok {
		b := truthy(v)
		out.Enabled = &b
	}
	out.SameSystem = truthy(in["same_system"])
	return out, true
}

func (a *App) adminJiraList(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	list, err := a.JiraInstances(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, i := range list {
		var tokens int64
		_ = a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM user_secrets WHERE kind='jira_token' AND scope=$1`, i.ID).Scan(&tokens)
		refs, _ := a.instanceRefs(ctx, a.DB, i.ID)
		out = append(out, map[string]any{"id": i.ID, "name": i.Name, "kind": i.Kind, "base_url": i.BaseURL, "tempo": i.Tempo, "enabled": i.Enabled,
			"created_at": i.CreatedAt, "usable": instanceUsable(i.Kind, i.Tempo), "note": nz(instanceNote(i.Kind, i.Tempo)), "tokens": tokens, "refs": refs})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) adminJiraCreate(w http.ResponseWriter, r *http.Request, u User) {
	in, ok := readInstanceInput(w, r)
	if !ok {
		return
	}
	i, err := a.JiraInstanceCreate(r.Context(), in)
	if err != nil {
		a.instFail(w, err)
		return
	}
	a.audit(r.Context(), u.ID, "admin.jira_instance", fmt.Sprintf("angelegt #%d „%s“ (%s, %s)", i.ID, i.Name, planeScope(i.BaseURL), onOff(i.Enabled)))
	writeJSON(w, http.StatusOK, i)
}

func onOff(b bool) string {
	if b {
		return "ein"
	}
	return "aus"
}

func (a *App) adminJiraUpdate(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	in, ok := readInstanceInput(w, r)
	if !ok {
		return
	}
	i, cleared, err := a.JiraInstanceUpdate(r.Context(), id, in)
	if err != nil {
		a.instFail(w, err)
		return
	}
	detail := fmt.Sprintf("geändert #%d „%s“ (%s, %s)", i.ID, i.Name, planeScope(i.BaseURL), onOff(i.Enabled))
	if cleared > 0 {
		detail += fmt.Sprintf(", Adresse gewechselt: %d Tokens gelöscht", cleared)
		a.Log.Warn("Jira-Instanz: Adresse geändert, Tokens gelöscht", "instanz", i.ID, "anzahl", cleared)
	}
	a.audit(r.Context(), u.ID, "admin.jira_instance", detail)
	writeJSON(w, http.StatusOK, map[string]any{"instance": i, "tokens_cleared": cleared})
}

func (a *App) adminJiraDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	force := r.URL.Query().Get("force") == "1"
	i, tokens, err := a.JiraInstanceDelete(r.Context(), id, force)
	if err != nil {
		a.instFail(w, err)
		return
	}
	a.audit(r.Context(), u.ID, "admin.jira_instance", fmt.Sprintf("gelöscht #%d „%s“ (%d Tokens%s)", i.ID, i.Name, tokens, map[bool]string{true: ", force", false: ""}[force]))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tokens_cleared": tokens})
}

// jiraInstancesMine: eingeschaltete Instanzen und ob die Person dort ein Token hat (nie das Token)
func (a *App) jiraInstancesMine(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	list, err := a.JiraInstances(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	have := map[int64]bool{}
	if rows, err := a.DB.Query(ctx, `SELECT scope FROM user_secrets WHERE user_id=$1 AND kind='jira_token'`, u.ID); err == nil {
		for rows.Next() {
			var s int64
			if rows.Scan(&s) == nil {
				have[s] = true
			}
		}
		rows.Close()
	}
	out := []map[string]any{}
	for _, i := range list {
		if i.Enabled {
			out = append(out, map[string]any{"id": i.ID, "name": i.Name, "kind": i.Kind, "tempo": i.Tempo, "token_set": have[i.ID]})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) jiraInstanceSettingsGet(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	i, err := a.jiraInstance(r.Context(), a.DB, id, false)
	if err != nil || !i.Enabled {
		notFound(w)
		return
	}
	st, err := a.userSettings(r.Context(), u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	out := map[string]any{"id": i.ID, "name": i.Name}
	for _, k := range append(append([]string{}, instUserKeys...), "jira_username", "jira_userkey") {
		out[k] = st[instKey(i.ID, k)]
	}
	writeJSON(w, http.StatusOK, out)
}

// jiraInstanceSettingsPut: nur die von der Person änderbaren Werte (Benutzer/Kürzel pflegt der Server)
func (a *App) jiraInstanceSettingsPut(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	i, err := a.jiraInstance(r.Context(), a.DB, id, false)
	if err != nil || !i.Enabled {
		notFound(w)
		return
	}
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	for k, v := range in {
		sv := strings.TrimSpace(asStr(v))
		switch {
		case !contains(instUserKeys, k):
			badRequest(w, "Nicht änderbar: "+k)
			return
		case len(sv) > 200:
			badRequest(w, k+": zu lang")
			return
		case k == "tempo_push_since" && sv != "" && !isoDateRE.MatchString(sv):
			badRequest(w, "tempo_push_since: Datum JJJJ-MM-TT")
			return
		case k == "tempo_account_attr_id" && sv != "" && leadingInt(sv) <= 0:
			badRequest(w, "tempo_account_attr_id: Zahl")
			return
		}
	}
	for k, v := range in {
		sv := strings.TrimSpace(asStr(v))
		var err error
		if sv == "" {
			_, err = a.DB.Exec(r.Context(), `DELETE FROM user_settings WHERE user_id=$1 AND key=$2`, u.ID, instKey(i.ID, k))
		} else {
			_, err = a.DB.Exec(r.Context(), `INSERT INTO user_settings(user_id,key,value) VALUES($1,$2,$3) ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value`, u.ID, instKey(i.ID, k), sv)
		}
		if err != nil {
			a.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// instKeysStale: Projektkürzel der Instanz älter als 6 h (oder unbekannt)?
func instKeysStale(st map[string]string, id int64) bool {
	t, err := time.Parse(tempoStampFmt, st[instKey(id, "jira_project_keys_at")])
	return err != nil || time.Since(t) > 6*time.Hour || st[instKey(id, "jira_project_keys")] == ""
}
