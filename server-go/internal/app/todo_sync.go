package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Abgleich To-do → Plane/Jira/Tempo (wie backend/todo.js syncOut).
   Läuft nach Create/Update asynchron; Fehler landen in plan_tasks.sync_error und blockieren nie. */

type todoSyncWant struct {
	Due   any    `json:"due"`
	Day   any    `json:"day"`
	Secs  int    `json:"secs"`
	Start string `json:"start"`
}

func todoSyncParseHash(raw string) todoSyncWant {
	var w todoSyncWant
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &w)
	}
	if w.Start == "" {
		w.Start = "09:00"
	}
	return w
}

func todoSyncEqStr(a, b any) bool { return asStr(a) == asStr(b) }

// todoSyncLater: Abgleich im Hintergrund (Request-Kontext nicht verwenden)
func (a *App) todoSyncLater(uid, id int64) {
	go a.safely("todo-sync", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if err := a.todoSyncOut(ctx, uid, id); err != nil {
			a.sysLogSafe(ctx, uid, "warn", "todo", fmt.Sprintf("Abgleich #%d: %s", id, err.Error()))
		}
	})
}

func (a *App) todoSyncOut(ctx context.Context, uid, id int64) error {
	var title, source, status string
	var ticket, due, sched, blockStart, syncHash, tempoPlan *string
	var est *int
	err := a.DB.QueryRow(ctx, `SELECT title, COALESCE(source,''), jira_ticket, due_date, scheduled_date, block_start,
		COALESCE(status,'open'), sync_hash, tempo_plan_id, estimated_minutes
		FROM plan_tasks WHERE id=$1 AND user_id=$2`, id, uid).
		Scan(&title, &source, &ticket, &due, &sched, &blockStart, &status, &syncHash, &tempoPlan, &est)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	key := strings.ToUpper(strings.TrimSpace(deref(ticket)))
	if key == "" {
		_ = a.DB.QueryRow(ctx, `SELECT UPPER(t.jira_ticket) FROM plan_tasks p JOIN tasks t ON t.id=p.task_id AND t.user_id=p.user_id
			WHERE p.id=$1 AND p.user_id=$2 AND t.jira_ticket IS NOT NULL`, id, uid).Scan(&key)
		key = strings.ToUpper(strings.TrimSpace(key))
	}
	if key == "" {
		return nil
	}
	sysMap, err := a.ticketSystems(ctx, uid, []string{key})
	if err != nil {
		return err
	}
	sys, _ := sysMap.of(key).(string)
	if sys == "" {
		return nil
	}
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return err
	}
	on := func(k string) bool { return st[k] != "0" }
	done := status == "done"
	want := todoSyncWant{
		Due:   nz(deref(due)),
		Day:   nil,
		Secs:  0,
		Start: jsOr(deref(blockStart), "09:00"),
	}
	if !done {
		want.Day = nz(deref(sched))
		mins := 0
		if est != nil {
			mins = *est
		}
		want.Secs = mins * 60
	}
	prev := todoSyncParseHash(deref(syncHash))
	var errs []string
	planID := deref(tempoPlan)

	if on("todo_sync_due") && !todoSyncEqStr(want.Due, prev.Due) && source != "ticket-due" {
		if err := a.todoSyncDue(ctx, uid, key, sys, asStr(want.Due)); err != nil {
			errs = append(errs, "Fälligkeit: "+err.Error())
		} else {
			a.sysLogSafe(ctx, uid, "info", "todo", fmt.Sprintf("Fälligkeit %s → %s (%s)", key, jsOr(asStr(want.Due), "keine"), sys))
		}
	}

	if sys == "plane" && on("todo_sync_plane_start") && !todoSyncEqStr(want.Day, prev.Day) {
		if err := a.todoSyncPlaneStart(ctx, uid, key, asStr(want.Day)); err != nil {
			errs = append(errs, "Planung: "+err.Error())
		}
	}

	if sys == "jira" && on("todo_sync_tempo_plan") &&
		(!todoSyncEqStr(want.Day, prev.Day) || want.Secs != prev.Secs || want.Start != prev.Start) {
		newID, err := a.todoSyncTempoPlan(ctx, uid, id, key, planID, title, want)
		if err != nil {
			errs = append(errs, "Planung: "+err.Error())
		} else {
			planID = newID
		}
	}

	hash := want
	if len(errs) > 0 {
		hash = prev
	}
	b, _ := json.Marshal(hash)
	var syncErr any
	if len(errs) > 0 {
		msg := truncRunes(strings.Join(errs, " · "), 300)
		syncErr = msg
		a.sysLogSafe(ctx, uid, "warn", "todo", key+": "+msg)
	}
	_, err = a.DB.Exec(ctx, `UPDATE plan_tasks SET sync_hash=$3, sync_error=$4, tempo_plan_id=NULLIF($5,'') WHERE id=$1 AND user_id=$2`,
		id, uid, string(b), syncErr, planID)
	return err
}

func (a *App) todoSyncDue(ctx context.Context, uid int64, key, sys, due string) error {
	var dueVal any
	if due != "" {
		dueVal = due
	}
	if sys == "plane" {
		c, wi, pid, _, err := a.planeCfgForKey(ctx, uid, key, "")
		if err != nil {
			return err
		}
		_, err = a.planeDo(ctx, uid, c, "PATCH",
			c.wsPath()+"/projects/"+url.PathEscape(pid)+"/work-items/"+url.PathEscape(asStr(wi["id"]))+"/",
			map[string]any{"target_date": dueVal}, planeOpts{})
		return err
	}
	ti, err := a.todoJiraInst(ctx, uid, key)
	if err != nil {
		return err
	}
	_, err = a.jiraDo(ctx, uid, ti.C.jira(), "PUT", "/rest/api/2/issue/"+url.PathEscape(key),
		map[string]any{"fields": map[string]any{"duedate": dueVal}})
	return err
}

func (a *App) todoSyncPlaneStart(ctx context.Context, uid int64, key, day string) error {
	var start any
	if day != "" {
		start = day
	}
	c, wi, pid, _, err := a.planeCfgForKey(ctx, uid, key, "")
	if err != nil {
		return err
	}
	_, err = a.planeDo(ctx, uid, c, "PATCH",
		c.wsPath()+"/projects/"+url.PathEscape(pid)+"/work-items/"+url.PathEscape(asStr(wi["id"]))+"/",
		map[string]any{"start_date": start}, planeOpts{})
	return err
}

func (a *App) todoSyncTempoPlan(ctx context.Context, uid, planTaskID int64, key, oldPlanID, title string, want todoSyncWant) (string, error) {
	ti, err := a.todoJiraInst(ctx, uid, key)
	if err != nil {
		return oldPlanID, err
	}
	jc := ti.C.jira()
	if oldPlanID != "" {
		if _, err := a.jiraDo(ctx, uid, jc, "DELETE", "/rest/tempo-planning/1/plan/"+url.PathEscape(oldPlanID), nil); err != nil && jiraStatus(err) != 404 {
			return oldPlanID, err
		}
		_, _ = a.DB.Exec(ctx, `UPDATE plan_tasks SET tempo_plan_id=NULL WHERE id=$1 AND user_id=$2`, planTaskID, uid)
		oldPlanID = ""
	}
	day := asStr(want.Day)
	if day == "" || want.Secs <= 0 {
		return "", nil
	}
	issue, err := a.jiraDo(ctx, uid, jc, "GET", "/rest/api/2/issue/"+url.PathEscape(key)+"?fields=summary", nil)
	if err != nil {
		return "", err
	}
	issueID := asStr(jm(issue)["id"])
	if issueID == "" {
		return "", errors.New("Jira-Ticket ohne ID")
	}
	assignee := jsOr(ti.C.WorkerKey, ti.C.Worker)
	body := map[string]any{
		"planItemType": "ISSUE", "planItemId": issueID, "assigneeKey": assignee, "assigneeType": "USER",
		"start": day, "end": day, "secondsPerDay": want.Secs, "startTime": want.Start,
		"includeNonWorkingDays": false, "description": truncRunes(title, 250),
	}
	p, err := a.jiraDo(ctx, uid, jc, "POST", "/rest/tempo-planning/1/plan", body)
	if err != nil && jiraStatus(err) == 400 && ti.C.Worker != "" && assignee != ti.C.Worker {
		body["assigneeKey"] = ti.C.Worker
		p, err = a.jiraDo(ctx, uid, jc, "POST", "/rest/tempo-planning/1/plan", body)
	}
	if err != nil {
		return "", err
	}
	pid := tempoPlanIDOf(p)
	if pid != "" {
		a.sysLogSafe(ctx, uid, "info", "todo", fmt.Sprintf("Tempo-Plan %s am %s: %d min", key, day, want.Secs/60))
	}
	return pid, nil
}

func tempoPlanIDOf(p any) string {
	if m := jm(p); m != nil {
		if id := asStr(m["id"]); id != "" {
			return id
		}
		if id := asStr(m["planId"]); id != "" {
			return id
		}
	}
	if list, ok := p.([]any); ok && len(list) > 0 {
		return tempoPlanIDOf(list[0])
	}
	return ""
}

func (a *App) todoJiraInst(ctx context.Context, uid int64, key string) (*tempoInst, error) {
	s, _, err := a.tempoSetup(ctx, uid)
	if err != nil {
		return nil, err
	}
	ti, err := pickInstance(s, nil, key)
	if err != nil {
		return nil, err
	}
	if ti.C.Token == "" {
		return nil, errors.New("Jira-Token fehlt")
	}
	return ti, nil
}

// todoDeleteTempoPlan: bei To-do-Löschung Tempo-Plan entfernen (wie Node DELETE)
func (a *App) todoDeleteTempoPlan(ctx context.Context, uid int64, key, planID string) {
	if planID == "" || !jiraKeyRE.MatchString(strings.ToUpper(key)) {
		return
	}
	ti, err := a.todoJiraInst(ctx, uid, strings.ToUpper(key))
	if err != nil {
		return
	}
	_, _ = a.jiraDo(ctx, uid, ti.C.jira(), "DELETE", "/rest/tempo-planning/1/plan/"+url.PathEscape(planID), nil)
}
