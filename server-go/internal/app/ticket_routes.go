package app

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

/* Ticketnummer → lokale Aufgabe (wie ensureTicketTask in backend/todo.js): POST /api/ticket/ensure (nur zuordnen) und
   POST /api/ticket/start (Timer starten). Es wird NIE ein Ticket in Plane/Jira angelegt – nur die lokale Aufgabe zu einem
   bestehenden Ticket (Plane: Work-Item lesen wie ensurePlaneTask; Jira: lokale Aufgabe im verknüpften Projekt).
   Idempotent: je Person wird unter einer Transaktionssperre erneut gesucht und höchstens einmal angelegt (Netz vorher, ohne Sperre);
   doppelte Klicks liefern dieselbe Aufgabe. Jira-Aufgaben neu anlegen nur bei eingeschaltetem Jira/Tempo-Modul. */

func (a *App) routesTicket(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/ticket/ensure", a.auth(a.ticketEnsure))
	mux.HandleFunc("POST /api/ticket/start", a.auth(a.ticketStart))
}

type ticketTask struct {
	ProjectID   int64
	TaskID      int64
	Title       string
	ProjectName any
}

// ensureTicketTask: bekannte Aufgabe nehmen, sonst Plane-Ticket übernehmen oder Jira-Aufgabe im verknüpften Projekt anlegen.
// msg ≠ "" = fachlich abgelehnt (400). Netzaufrufe (Plane) laufen VOR der Sperre; unter der Sperre wird nur noch einmal
// gesucht und höchstens einmal angelegt, und zwar ausschließlich über die Transaktion (keine zweite Pool-Verbindung).
func (a *App) ensureTicketTask(ctx context.Context, uid int64, key, title string) (*ticketTask, string, error) {
	const knownSQL = `SELECT t.id, t.project_id FROM tasks t JOIN projects p ON p.id=t.project_id AND p.user_id=t.user_id
		WHERE t.user_id=$1 AND UPPER(TRIM(t.jira_ticket))=$2 AND COALESCE(p.status,'active')<>'archived' ORDER BY t.id DESC LIMIT 1`
	res := &ticketTask{}
	err := a.DB.QueryRow(ctx, knownSQL, uid, key).Scan(&res.TaskID, &res.ProjectID)
	switch {
	case err == nil: // schon bekannt → direkt nehmen (kein Netz nötig)
		return a.ticketTaskInfo(ctx, uid, res, key, title), "", nil
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, "", err
	}
	sys, err := a.ticketSystems(ctx, uid, []string{key})
	if err != nil {
		return nil, "", err
	}
	var insert func(tx pgx.Tx) error
	if sys.of(key) == "plane" {
		c, err := a.planeCfg(ctx, uid)
		if err != nil {
			return nil, "", err
		}
		wi, ppid, p, err := a.workItemByKey(ctx, uid, c, key) // Netz: ohne Sperre, ohne offene Transaktion
		if err != nil {
			return nil, "", err
		}
		if p == nil {
			return nil, "Das Plane-Projekt ist noch keinem lokalen Projekt zugeordnet (Projekte › Quellen)", nil
		}
		insert = func(tx pgx.Tx) error { // wie ensurePlaneTask (plane_tickets.go)
			res.ProjectID = p.ID
			return tx.QueryRow(ctx, `INSERT INTO tasks(user_id,project_id,title,jira_ticket,status,plane_work_item_id,plane_project_id) VALUES($1,$2,$3,$4,'open',$5,$6) RETURNING id`,
				uid, p.ID, jsOr(asStr(wi["name"]), key), key, asStr(wi["id"]), ppid).Scan(&res.TaskID)
		}
	} else {
		if !a.tempoModuleOn(ctx) {
			return nil, "Jira/Tempo ist auf diesem Server abgeschaltet – " + key + " lässt sich nicht zuordnen", nil
		}
		prefix := strings.SplitN(key, "-", 2)[0]
		pid, _ := a.projectByJiraKey(ctx, uid, prefix).(int64)
		if pid == 0 {
			_ = a.DB.QueryRow(ctx, `SELECT t.project_id FROM tasks t JOIN projects p ON p.id=t.project_id AND p.user_id=t.user_id
				WHERE t.user_id=$1 AND UPPER(TRIM(t.jira_ticket))=$2 AND COALESCE(p.status,'active')<>'archived' ORDER BY t.id LIMIT 1`, uid, key).Scan(&pid)
		}
		if pid == 0 {
			return nil, "Das Jira-Projekt " + prefix + " ist noch mit keinem Projekt verknüpft (Projekte › Neue Projekte)", nil
		}
		insert = func(tx pgx.Tx) error {
			res.ProjectID = pid
			var old string
			err := tx.QueryRow(ctx, `SELECT id, COALESCE(title,'') FROM tasks WHERE user_id=$1 AND project_id=$2 AND UPPER(TRIM(jira_ticket))=$3 ORDER BY id LIMIT 1`, uid, pid, key).Scan(&res.TaskID, &old)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				return tx.QueryRow(ctx, `INSERT INTO tasks(user_id,project_id,title,jira_ticket,status) VALUES($1,$2,$3,$4,'open') RETURNING id`, uid, pid, jsOr(title, key), key).Scan(&res.TaskID)
			case err == nil && title != "" && (old == "" || strings.ToUpper(old) == key):
				_, err = tx.Exec(ctx, `UPDATE tasks SET title=$3 WHERE id=$1 AND user_id=$2`, res.TaskID, uid, title)
			}
			return err
		}
	}
	err = a.inTx(ctx, func(tx pgx.Tx) error {
		// gleiche Sperre wie ensureTaskForIssue/Aufräumen: Aufgaben je Person nie doppelt (auch nicht über verschiedene Routen)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, ticketTaskLock+uid); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, knownSQL, uid, key).Scan(&res.TaskID, &res.ProjectID) // inzwischen angelegt (Doppelklick)?
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return insert(tx)
	})
	if err != nil {
		return nil, "", err
	}
	return a.ticketTaskInfo(ctx, uid, res, key, title), "", nil
}

func (a *App) ticketTaskInfo(ctx context.Context, uid int64, res *ticketTask, key, title string) *ticketTask {
	var t string
	var pn *string
	if a.DB.QueryRow(ctx, `SELECT t.title, p.name FROM tasks t LEFT JOIN projects p ON p.id=t.project_id AND p.user_id=t.user_id WHERE t.id=$1 AND t.user_id=$2`, res.TaskID, uid).Scan(&t, &pn) == nil {
		res.Title = t
		if pn != nil {
			res.ProjectName = *pn
		}
	}
	res.Title = jsOr(res.Title, jsOr(title, key))
	return res
}

func ticketInput(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	var in body
	if !readJSON(w, r, &in) {
		return "", "", false
	}
	key := strings.ToUpper(strings.TrimSpace(asStr(in["key"])))
	if !ticketKeyRE.MatchString(key) || len(key) > 64 {
		badRequest(w, "Keine Ticketnummer")
		return "", "", false
	}
	return key, truncRunes(strings.TrimSpace(asStr(in["title"])), 255), true
}

// ticketEnsure: nur zuordnen (z. B. beim Nachtragen) – startet nichts
func (a *App) ticketEnsure(w http.ResponseWriter, r *http.Request, u User) {
	key, title, ok := ticketInput(w, r)
	if !ok {
		return
	}
	t, msg, err := a.ensureTicketTask(r.Context(), u.ID, key, title)
	switch {
	case err != nil:
		a.planeFail(w, err) // Nicht-Plane-Fehler gehen dort an a.fail
	case msg != "":
		badRequest(w, msg)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": key, "project_id": t.ProjectID, "task_id": t.TaskID, "title": t.Title, "project_name": t.ProjectName})
	}
}

// ticketStart: offenes To-do zum Ticket → dessen Start (wie /todo/{id}/start), sonst Aufgabe sicherstellen und Timer starten
func (a *App) ticketStart(w http.ResponseWriter, r *http.Request, u User) {
	key, title, ok := ticketInput(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var todo int64
	err := a.DB.QueryRow(ctx, `SELECT id FROM plan_tasks WHERE user_id=$1 AND UPPER(jira_ticket)=$2 AND COALESCE(status,'open')='open' ORDER BY id DESC LIMIT 1`, u.ID, key).Scan(&todo)
	if err == nil {
		r2 := r.Clone(ctx)
		r2.SetPathValue("id", strconv.FormatInt(todo, 10))
		a.todoStart(w, r2, u)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		a.fail(w, err)
		return
	}
	t, msg, err := a.ensureTicketTask(ctx, u.ID, key, title)
	switch {
	case err != nil:
		a.planeFail(w, err)
		return
	case msg != "":
		badRequest(w, msg)
		return
	}
	_, ae, err := a.startTimer(ctx, u.ID, t.ProjectID, t.TaskID, nil, nil, nil, "")
	if ae != nil {
		writeJSON(w, ae.Code, ae.Body)
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	a.sysLog(ctx, u.ID, "info", "todo", "Timer auf "+key+" gestartet (aus dem Browser übernommen)")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "project_id": t.ProjectID, "task_id": t.TaskID})
}
