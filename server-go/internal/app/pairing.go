package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

/* Browser-Add-on-Kopplung (wie backend/pairing.js), Team-Server:
   Pairing erzeugt ein Geräte-Token (kind=addon). Das Add-on nutzt X-ZP-Token.
   Genehmigung nur mit Sitzung; Poll liefert den Klartext-Token einmal. */

type pairPending struct {
	Client, ExtVer, Origin string
	Created                time.Time
	Token                  string // nach Freigabe, einmalig an Poll
	Denied                 bool
	UserID                 int64
}

var (
	pairMu         sync.Mutex
	pairPendingMap = map[string]*pairPending{}
	extOriginRE    = regexp.MustCompile(`(?i)^(moz-extension|chrome-extension|safari-web-extension)://[a-z0-9-]+$`)
)

func isBrowserExtensionOrigin(o string) bool {
	return extOriginRE.MatchString(o)
}

// addonCSRFExempt: Add-on-Pfade, bei denen chrome-extension://… Origin erlaubt ist
// (Protect würde sonst cross-site / Host-Mismatch ablehnen).
func addonCSRFExempt(path string) bool {
	switch path {
	case "/api/pair/start", "/api/pair/poll",
		"/api/ext/state", "/api/plane/resolve",
		"/api/ticket/start", "/api/timer/stop", "/api/timer/status",
		"/api/pause/start", "/api/pause/stop", "/api/pause/status",
		"/api/todo":
		return true
	default:
		return false
	}
}

func (a *App) routesPairing(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/pair/start", a.pairStart)
	mux.HandleFunc("GET /api/pair/poll", a.pairPoll)
	mux.HandleFunc("GET /api/pair/info", a.auth(a.pairInfo))
	mux.HandleFunc("POST /api/pair/approve", a.auth(a.pairApprove))
	mux.HandleFunc("GET /api/pair/clients", a.auth(a.pairClients))
	mux.HandleFunc("DELETE /api/pair/clients/{id}", a.auth(a.pairClientDelete))
	mux.HandleFunc("GET /api/ext/state", a.authSessionOrDevice(a.extState))
}

func pairSweep() {
	cut := time.Now().Add(-5 * time.Minute)
	pairMu.Lock()
	defer pairMu.Unlock()
	for k, v := range pairPendingMap {
		if v.Created.Before(cut) {
			delete(pairPendingMap, k)
		}
	}
}

func (a *App) pairStart(w http.ResponseWriter, r *http.Request) {
	pairSweep()
	var in body
	_ = jsonDecodeSoft(r, &in)
	if in == nil {
		in = body{}
	}
	pairMu.Lock()
	if len(pairPendingMap) > 20 {
		pairMu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Zu viele offene Anfragen"})
		return
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	nonce := hex.EncodeToString(b)
	client := truncRunes(jsOr(asStr(in["client"]), "Browser"), 40)
	ver := truncRunes(asStr(in["ext_version"]), 20)
	pairPendingMap[nonce] = &pairPending{
		Client: client, ExtVer: ver, Origin: r.Header.Get("Origin"), Created: time.Now(),
	}
	pairMu.Unlock()
	base := strings.TrimRight(a.PublicURL, "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	writeJSON(w, http.StatusOK, map[string]any{"nonce": nonce, "approve_url": base + "/#koppeln/" + nonce})
}

func (a *App) pairPoll(w http.ResponseWriter, r *http.Request) {
	pairSweep()
	nonce := r.URL.Query().Get("nonce")
	pairMu.Lock()
	p := pairPendingMap[nonce]
	if p == nil || p.Denied {
		if p != nil {
			delete(pairPendingMap, nonce)
		}
		pairMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "abgelaufen oder abgelehnt"})
		return
	}
	if p.Token == "" {
		pairMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"pending": true})
		return
	}
	tok := p.Token
	delete(pairPendingMap, nonce)
	pairMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"token": tok})
}

func (a *App) pairInfo(w http.ResponseWriter, r *http.Request, u User) {
	_ = u
	pairSweep()
	nonce := r.URL.Query().Get("nonce")
	pairMu.Lock()
	p := pairPendingMap[nonce]
	pairMu.Unlock()
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Anfrage abgelaufen – im Add-on erneut „verbinden“ klicken"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": p.Client, "ext_version": p.ExtVer, "origin": p.Origin})
}

func (a *App) pairApprove(w http.ResponseWriter, r *http.Request, u User) {
	if !u.timeAccount() {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Nur Zeiterfassungs-Konten können Add-ons koppeln"})
		return
	}
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	pairSweep()
	nonce := asStr(in["nonce"])
	pairMu.Lock()
	p := pairPendingMap[nonce]
	if p == nil {
		pairMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Anfrage abgelaufen"})
		return
	}
	if !truthy(in["allow"]) {
		p.Denied = true
		client := p.Client
		pairMu.Unlock()
		a.sysLog(r.Context(), u.ID, "warn", "zugang", "Add-on-Verbindung abgelehnt ("+client+")")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	client, origin := p.Client, p.Origin
	pairMu.Unlock()

	raw, id, err := a.createDeviceToken(r.Context(), u.ID, truncRunes(client, 80), "addon")
	if err != nil {
		a.fail(w, err)
		return
	}
	pairMu.Lock()
	if p2 := pairPendingMap[nonce]; p2 != nil {
		p2.Token = raw
		p2.UserID = u.ID
	}
	pairMu.Unlock()
	a.sysLog(r.Context(), u.ID, "info", "zugang", fmt.Sprintf("Browser-Add-on verbunden: %s (%s) #%d", client, origin, id))
	a.audit(r.Context(), u.ID, "device.pair", fmt.Sprintf("addon #%d %s", id, client))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) pairClients(w http.ResponseWriter, r *http.Request, u User) {
	rs, err := a.maps(r.Context(), `SELECT id, label AS name, kind, created_at AS created, last_used
		FROM device_tokens WHERE user_id=$1 AND kind='addon' AND revoked_at IS NULL ORDER BY created_at DESC`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *App) pairClientDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	tag, err := a.DB.Exec(r.Context(), `UPDATE device_tokens SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND kind='addon' AND revoked_at IS NULL`, id, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		notFound(w)
		return
	}
	a.audit(r.Context(), u.ID, "device.revoke", "Add-on #"+fmt.Sprint(id))
	a.sysLog(r.Context(), u.ID, "warn", "zugang", "Browser-Add-on getrennt #"+fmt.Sprint(id))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) extState(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	timer := map[string]any{"active": false, "start_time": nil, "ticket": nil, "title": nil, "project_name": nil}
	s, err := a.one(ctx, `SELECT project_id,task_id,plan_task_id,comment,start_time,activity FROM timer_state WHERE user_id=$1`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if s != nil && s["start_time"] != nil && s["start_time"] != "" {
		get := func(table string, id any) map[string]any {
			if id == nil {
				return nil
			}
			m, _ := a.one(ctx, "SELECT * FROM "+table+" WHERE id=$1 AND user_id=$2", id, u.ID)
			return m
		}
		task, plan, proj := get("tasks", s["task_id"]), get("plan_tasks", s["plan_task_id"]), get("projects", s["project_id"])
		ticket, title := any(nil), any(nil)
		if task != nil {
			if t := task["jira_ticket"]; t != nil && asStr(t) != "" {
				ticket = t
			}
			if t := task["title"]; t != nil && asStr(t) != "" {
				title = t
			}
		}
		if title == nil && plan != nil {
			if t := plan["title"]; t != nil && asStr(t) != "" {
				title = t
			}
		}
		if title == nil && asStr(s["comment"]) != "" {
			title = s["comment"]
		}
		projName := any(nil)
		if proj != nil {
			projName = proj["name"]
		}
		timer = map[string]any{
			"active": true, "start_time": s["start_time"], "ticket": ticket, "title": title, "project_name": projName,
		}
	}
	pause := map[string]any{"active": false, "start_time": nil}
	var pstart *string
	if err := a.DB.QueryRow(ctx, `SELECT start_time FROM pause_state WHERE user_id=$1`, u.ID).Scan(&pstart); err == nil && pstart != nil && *pstart != "" {
		pause = map[string]any{"active": true, "start_time": *pstart}
	}

	st, _ := a.userSettings(ctx, u.ID)
	hostOf := func(raw string) any {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil
		}
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return nil
		}
		return u.Host
	}
	jiraHost := hostOf(st["jira_base_url"])
	if jiraHost == nil {
		if list, err := a.JiraInstances(ctx); err == nil {
			for _, ji := range list {
				if h := hostOf(ji.BaseURL); h != nil {
					jiraHost = h
					break
				}
			}
		}
	}
	planeHost := hostOf(st["plane_base_url"])
	if planeHost == nil {
		if w0, ok := a.primaryPlaneWorkspace(ctx); ok {
			planeHost = hostOf(w0.BaseURL)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"timer": timer, "pause": pause,
		"hosts": map[string]any{"jira": jiraHost, "plane": planeHost},
	})
}
