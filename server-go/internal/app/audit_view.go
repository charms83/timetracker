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
	"sync"
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

type chainResult struct {
	at       time.Time
	ok       bool
	rows     int64
	brokenID int64
	brokenTS string
	brokenBy int64 // Eigentümer der gebrochenen Zeile (nur intern)
	err      error
}

var (
	chainMu    sync.Mutex
	chainCache *chainResult
)

const chainCacheFor = time.Minute

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

// verifyChain läuft über die ganze Kette (gestreamt); Ergebnis eine Minute zwischengespeichert, gleichzeitige Aufrufe warten auf denselben Lauf
func (a *App) verifyChain(ctx context.Context) *chainResult {
	chainMu.Lock()
	defer chainMu.Unlock()
	if c := chainCache; c != nil && c.err == nil && time.Since(c.at) < chainCacheFor {
		return c
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 55*time.Second)
	defer cancel()
	res := &chainResult{at: time.Now(), ok: true}
	rows, err := a.DB.Query(ctx, `SELECT id, user_id, row_id, action, to_char(ts AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
		COALESCE(before::text,''), COALESCE(after::text,''), prev_hash, hash FROM entry_audit ORDER BY id`)
	if err != nil {
		res.err = err
		return res
	}
	defer rows.Close()
	var last *string
	for rows.Next() {
		var id, uid int64
		var rid *int64
		var act, ts, b, af string
		var prev, hash *string
		if err := rows.Scan(&id, &uid, &rid, &act, &ts, &b, &af, &prev, &hash); err != nil {
			res.err = err
			return res
		}
		p := ""
		if last != nil {
			p = *last
		}
		if !strEq(prev, last) || !strEq(chainLink(p, ts, uid, rid, act, b, af), hash) {
			res.ok, res.brokenID, res.brokenTS, res.brokenBy = false, id, ts, uid
			break
		}
		last = hash
		res.rows++
	}
	if err := rows.Err(); err != nil && res.ok {
		res.err = err
		return res
	}
	chainCache = res
	return res
}

func (a *App) auditVerify(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	c := a.verifyChain(ctx)
	if c.err != nil {
		a.fail(w, c.err)
		return
	}
	var own int64
	if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM entry_audit WHERE user_id=$1`, u.ID).Scan(&own); err != nil {
		a.fail(w, err)
		return
	}
	out := map[string]any{"ok": c.ok, "rows": own, "scope": "global", "checked_at": c.at.UTC().Format(isoMillis), "file": nil, "file_ok": nil, "last_hash": nil}
	if !c.ok {
		out["broken_at"], out["ts"], out["own"] = nil, nil, c.brokenBy == u.ID
		if c.brokenBy == u.ID { // eigene Zeile: Id und Zeitpunkt zeigen, fremde nie
			out["broken_at"], out["ts"] = c.brokenID, c.brokenTS
		}
	}
	writeJSON(w, http.StatusOK, out)
}
