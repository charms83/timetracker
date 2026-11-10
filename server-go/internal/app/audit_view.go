package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

/* Änderungsverlauf der eigenen Buchungen (wie backend/audit.js: GET /api/audit, /api/audit/verify).
   Quelle: entry_audit (Trigger aus 008/018, Hash-Kette über alle Personen). Ausgabe immer nur user_id = angemeldete Person;
   vorher/nachher nur mit den Feldern, die Node zeigt (COLS). Die Kette wird global geprüft (sie ist global verkettet),
   gemeldet werden aber nur eigene Zahlen; ein Bruch bei einer fremden Zeile wird ohne Id/Zeitpunkt gemeldet. */

var auditCols = []string{"project_id", "task_id", "start_time", "end_time", "duration_seconds", "comment", "billable", "date", "activity", "source"}

var actorIDRE = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

func (a *App) routesAuditView(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/audit", a.auth(a.auditList))
	mux.HandleFunc("GET /api/audit/verify", a.auth(a.auditVerify))
}

// auditShape: vorher/nachher auf COLS kürzen, Änderungen wie diff() in audit.js
func auditShape(beforeRaw, afterRaw *string) (before, after, changes any) {
	pick := func(raw *string) map[string]any {
		if raw == nil || *raw == "" {
			return nil
		}
		var m map[string]any
		if json.Unmarshal([]byte(*raw), &m) != nil {
			return nil
		}
		out := make(map[string]any, len(auditCols))
		for _, c := range auditCols {
			out[c] = m[c]
		}
		return out
	}
	b, af := pick(beforeRaw), pick(afterRaw)
	if b != nil {
		before = b
	}
	if af != nil {
		after = af
	}
	if b != nil && af != nil {
		ch := map[string]any{}
		for _, c := range auditCols {
			if fmt.Sprint(b[c]) != fmt.Sprint(af[c]) {
				ch[c] = []any{b[c], af[c]}
			}
		}
		changes = ch
	}
	return
}

func (a *App) auditList(w http.ResponseWriter, r *http.Request, u User) {
	q := r.URL.Query()
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit <= 0 {
		limit = 100
	}
	limit = min(limit, 500)
	var entry, before, from any
	if s := q.Get("entry"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			badRequest(w, "entry ungültig")
			return
		}
		entry = n
	}
	if s := q.Get("before"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			badRequest(w, "before ungültig")
			return
		}
		before = n
	}
	if s := q.Get("from"); s != "" {
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			d, err2 := time.ParseInLocation("2006-01-02", s, a.loc())
			if err2 != nil {
				badRequest(w, "from ungültig")
				return
			}
			t = d
		}
		from = t
	}
	action := q.Get("action")
	if action != "" && action != "create" && action != "update" && action != "delete" {
		badRequest(w, "action: create, update oder delete")
		return
	}
	ctx := r.Context()
	rows, err := a.DB.Query(ctx, `SELECT id, ts, row_id, action, before::text, after::text, actor, origin, prev_hash, hash FROM entry_audit
		WHERE user_id=$1 AND ($2::bigint IS NULL OR row_id=$2) AND ($3 = '' OR action=$3) AND ($4::timestamptz IS NULL OR ts >= $4) AND ($5::bigint IS NULL OR id < $5)
		ORDER BY id DESC LIMIT $6`, u.ID, entry, action, from, before, limit)
	if err != nil {
		a.fail(w, err)
		return
	}
	out := []map[string]any{}
	actorIDs := []int64{}
	for rows.Next() {
		var id int64
		var ts time.Time
		var rowID *int64
		var act string
		var b, af, actor, origin, prev, hash *string
		if err := rows.Scan(&id, &ts, &rowID, &act, &b, &af, &actor, &origin, &prev, &hash); err != nil {
			rows.Close()
			a.fail(w, err)
			return
		}
		bo, ao, ch := auditShape(b, af)
		m := map[string]any{"id": id, "ts": ts.UTC().Format("2006-01-02T15:04:05.000Z"), "tbl": "time_entries", "row_id": rowID, "action": act,
			"before": bo, "after": ao, "changes": ch, "actor": actor, "origin": origin, "prev_hash": prev, "hash": hash}
		if actor != nil && actorIDRE.MatchString(*actor) {
			if n, err := strconv.ParseInt(*actor, 10, 64); err == nil {
				actorIDs = append(actorIDs, n)
			}
		}
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		a.fail(w, err)
		return
	}
	// Nachtrag (018): actor ist die Id der Teamleitung/des Personals → Name zeigen
	if len(actorIDs) > 0 {
		names := map[string]string{}
		nr, err := a.DB.Query(ctx, `SELECT id::text, name FROM users WHERE id = ANY($1)`, actorIDs)
		if err != nil {
			a.fail(w, err)
			return
		}
		for nr.Next() {
			var id, name string
			if nr.Scan(&id, &name) == nil {
				names[id] = name
			}
		}
		nr.Close()
		for _, m := range out {
			if s, ok := m["actor"].(*string); ok && s != nil {
				if n, ok := names[*s]; ok {
					m["actor"] = n
				}
			}
		}
	}
	projects := map[string]any{}
	tasks := map[string]any{}
	prow, err := a.DB.Query(ctx, `SELECT id, name FROM projects WHERE user_id=$1`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	for prow.Next() {
		var id int64
		var name string
		if prow.Scan(&id, &name) == nil {
			projects[strconv.FormatInt(id, 10)] = name
		}
	}
	prow.Close()
	trow, err := a.DB.Query(ctx, `SELECT id, COALESCE(jira_ticket,''), title FROM tasks WHERE user_id=$1`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	for trow.Next() {
		var id int64
		var key, title string
		if trow.Scan(&id, &key, &title) == nil {
			if key != "" {
				title = key + " " + title
			}
			tasks[strconv.FormatInt(id, 10)] = title
		}
	}
	trow.Close()
	var total int64
	if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM entry_audit WHERE user_id=$1`, u.ID).Scan(&total); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": out, "projects": projects, "tasks": tasks, "total": total})
}

/* ── Hash-Kette prüfen ──────────────────────────────────────────── */

// Die Kette wird schrittweise geprüft: Stand (letzte geprüfte id/hash) bleibt im Speicher, jeder Lauf prüft nur Neues
// (höchstens chainBatch Zeilen); stündlich beginnt die Prüfung wieder von vorn. Nur ein Lauf gleichzeitig; Wartende brechen
// mit ihrer Anfrage ab. Fehler werden kurz gemerkt, damit ein Fehlerfall nicht jeden Aufruf erneut scannen lässt.
type chainState struct {
	checkedAt time.Time // letzter Lauf
	fullFrom  time.Time // Beginn der aktuellen Prüfung ab id 0
	lastID    int64
	lastHash  *string
	rows      int64
	complete  bool // bis zur letzten Zeile geprüft
	ok        bool
	brokenID  int64
	brokenTS  string
	brokenBy  int64 // Eigentümer der gebrochenen Zeile (nur intern)
	err       error
	errAt     time.Time
}

var (
	chainSem    = make(chan struct{}, 1)
	chainState0 = &chainState{ok: true}
)

const (
	chainCacheFor = time.Minute
	chainFullFor  = time.Hour
	chainBatch    = 500000
)

// chainLink: Hash wie time_entries_audit() (008/018): sha256(prev|ts|uid|rid|action|before|after). rid NULL → Hash NULL (SQL-Verkettung).
func chainLink(prev, ts string, uid int64, rid *int64, action, before, after string) *string {
	if rid == nil {
		return nil
	}
	s := prev + "|" + ts + "|" + strconv.FormatInt(uid, 10) + "|" + strconv.FormatInt(*rid, 10) + "|" + action + "|" + before + "|" + after
	h := sha256.Sum256([]byte(s))
	x := hex.EncodeToString(h[:])
	return &x
}

func strEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// verifyChain liefert eine Kopie des Prüfstands (nach einem Lauf, wenn nötig)
func (a *App) verifyChain(ctx context.Context) (chainState, error) {
	select {
	case chainSem <- struct{}{}:
	case <-ctx.Done():
		return chainState{}, ctx.Err()
	}
	defer func() { <-chainSem }()
	c := chainState0
	now := time.Now()
	if c.err != nil && now.Sub(c.errAt) < 30*time.Second {
		return *c, c.err
	}
	if c.complete && now.Sub(c.checkedAt) < chainCacheFor && now.Sub(c.fullFrom) < chainFullFor {
		return *c, nil
	}
	if !c.ok && now.Sub(c.fullFrom) < chainFullFor { // Bruch bleibt Bruch bis zur nächsten Vollprüfung
		return *c, nil
	}
	if now.Sub(c.fullFrom) >= chainFullFor || c.fullFrom.IsZero() {
		c = &chainState{ok: true, fullFrom: now}
		chainState0 = c
	}
	c.err = nil
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 50*time.Second)
	defer cancel()
	rows, err := a.DB.Query(rctx, `SELECT id, user_id, row_id, action, to_char(ts AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
		COALESCE(before::text,''), COALESCE(after::text,''), prev_hash, hash FROM entry_audit WHERE id > $1 ORDER BY id LIMIT $2`, c.lastID, chainBatch)
	if err != nil {
		c.err, c.errAt = err, now
		return *c, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
		var id, uid int64
		var rid *int64
		var act, ts, b, af string
		var prev, hash *string
		if err := rows.Scan(&id, &uid, &rid, &act, &ts, &b, &af, &prev, &hash); err != nil {
			c.err, c.errAt = err, now
			return *c, err
		}
		p := ""
		if c.lastHash != nil {
			p = *c.lastHash
		}
		if !strEq(prev, c.lastHash) || !strEq(chainLink(p, ts, uid, rid, act, b, af), hash) {
			c.ok, c.brokenID, c.brokenTS, c.brokenBy = false, id, ts, uid
			break
		}
		c.lastID, c.lastHash = id, hash
		c.rows++
	}
	if err := rows.Err(); err != nil && c.ok {
		c.err, c.errAt = err, now
		return *c, err
	}
	c.complete = n < chainBatch || !c.ok
	c.checkedAt = now
	return *c, nil
}

func (a *App) auditVerify(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	c, err := a.verifyChain(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	var own int64
	if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM entry_audit WHERE user_id=$1`, u.ID).Scan(&own); err != nil {
		a.fail(w, err)
		return
	}
	out := map[string]any{"ok": c.ok, "rows": own, "scope": "global", "complete": c.complete, "checked_at": c.checkedAt.UTC().Format(isoMillis), "file": nil, "file_ok": nil, "last_hash": nil}
	if !c.ok {
		out["broken_at"], out["ts"], out["own"] = nil, nil, c.brokenBy == u.ID
		if c.brokenBy == u.ID { // eigene Zeile: Id und Zeitpunkt zeigen, fremde nie
			out["broken_at"], out["ts"] = c.brokenID, c.brokenTS
		}
	}
	writeJSON(w, http.StatusOK, out)
}
