package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

/* Plane-Client mit Anfrage-Bremse je Person.
   Plane begrenzt die API auf ca. 60 Anfragen pro Minute und Schlüssel. Jede Person hat ihren eigenen Schlüssel
   und damit ihr eigenes Limit; mehrere Geräte derselben Person teilen es, deshalb bremst der Server hier zentral:
   • Hintergrund (Abgleich) höchstens 45/Minute und wartet selbst,
   • Oberfläche (Suche, Ticket öffnen) höchstens 58/Minute und meldet bei Auslastung sofort „kurz ausgelastet“,
   • bei HTTP 429 wird Retry-After / X-RateLimit-Reset beachtet und mit wachsender Pause wiederholt.
   Adresse und Workspace kommen aus der Servereinrichtung (nicht von Benutzern: sonst könnte jede Person den Server
   auf beliebige Adressen schicken); den Schlüssel trägt jede Person selbst im Profil ein. */

type planeCfg struct {
	Base, Key, WS, Mode, MeID string
	ID                        int64 // plane_workspaces.id (0 = Legacy/Primär ohne Zeile)
}

func (c planeCfg) ok() bool { return c.Base != "" && c.Key != "" && c.WS != "" }

type planeErr struct {
	Status     int
	Msg        string
	Fast       bool
	RetryAfter time.Duration
}

func (e *planeErr) Error() string { return e.Msg }

// validatePlaneURL: https ist Pflicht (der API-Schlüssel geht bei jeder Anfrage mit); http nur für localhost
// oder wenn der Betrieb es mit ZP_ALLOW_HTTP=1 ausdrücklich erlaubt (z. B. internes Testsystem).
func (a *App) validatePlaneURL(raw string) (string, error) {
	v := strings.TrimRight(strings.TrimSpace(raw), "/")
	if v == "" {
		return "", nil
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("Plane-Adresse ungültig (ohne Benutzer:Passwort)")
	}
	if u.Scheme == "http" && !a.AllowHTTP {
		h := u.Hostname()
		if h != "localhost" && h != "127.0.0.1" && h != "::1" && !strings.HasSuffix(h, ".localhost") {
			return "", errors.New("Plane-Adresse muss mit https:// beginnen (http nur für localhost; sonst ZP_ALLOW_HTTP=1)")
		}
	}
	return v, nil
}

func normWS(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(strings.ToLower(v), "http://") || strings.HasPrefix(strings.ToLower(v), "https://") {
		if u, err := url.Parse(v); err == nil {
			v = u.Path
		}
	}
	for _, p := range strings.Split(v, "/") {
		if p != "" {
			return strings.TrimSpace(p)
		}
	}
	return ""
}

func (a *App) planeCfg(ctx context.Context, uid int64) (planeCfg, error) {
	if w, ok := a.primaryPlaneWorkspace(ctx); ok {
		return a.planeCfgFor(ctx, uid, w)
	}
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return planeCfg{}, err
	}
	key, err := a.secretFor(ctx, uid, "plane_key")
	if err != nil {
		return planeCfg{}, err
	}
	mode := "manual"
	if st["plane_sync_mode"] == "auto" {
		mode = "auto"
	}
	return planeCfg{
		Base: strings.TrimRight(strings.TrimSpace(a.conf(ctx, "plane_base_url")), "/"),
		Key:  strings.TrimSpace(key),
		WS:   normWS(a.conf(ctx, "plane_workspace")),
		Mode: mode,
		MeID: st["plane_me_id"],
	}, nil
}

// planeCfgs: von der Person gewählte, eingeschaltete Workspaces mit Schlüssel (für Scan/Sync über mehrere).
func (a *App) planeCfgs(ctx context.Context, uid int64) ([]planeCfg, error) {
	list, err := a.PlaneWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	var out []planeCfg
	for _, w := range list {
		ok, err := a.planeUserSelected(ctx, uid, w)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		c, err := a.planeCfgFor(ctx, uid, w)
		if err != nil {
			return nil, err
		}
		if c.ok() {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		if c, err := a.planeCfg(ctx, uid); err == nil && c.ok() {
			out = append(out, c)
		}
	}
	return out, nil
}

/* ── Bremse ─────────────────────────────────────────────────────── */

type limiter struct {
	mu      sync.Mutex
	sent    []time.Time
	blocked time.Time
	lastLog time.Time
}

func (a *App) limiterFor(uid int64) *limiter {
	v, _ := a.limiters.LoadOrStore(uid, &limiter{})
	return v.(*limiter)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (l *limiter) acquire(ctx context.Context, interactive bool) error {
	return l.acquireShared(ctx, nil, 0, interactive)
}

// acquireShared: Speicher + optional gemeinsame DB (Mehr-Pod).
func (l *limiter) acquireShared(ctx context.Context, a *App, uid int64, interactive bool) error {
	key := strconv.FormatInt(uid, 10)
	for {
		if a != nil && a.DB != nil {
			if until := a.rateBlockUntil(ctx, "plane", key); !until.IsZero() {
				wait := time.Until(until)
				if interactive {
					return &planeErr{Status: 429, Fast: true, Msg: fmt.Sprintf("Plane ist kurz ausgelastet – in %d s nochmal", int(math.Ceil(wait.Seconds())))}
				}
				if err := sleepCtx(ctx, wait); err != nil {
					return err
				}
				continue
			}
		}
		l.mu.Lock()
		now := time.Now()
		for len(l.sent) > 0 && now.Sub(l.sent[0]) > time.Minute {
			l.sent = l.sent[1:]
		}
		if now.Before(l.blocked) {
			wait := l.blocked.Sub(now)
			l.mu.Unlock()
			if interactive {
				return &planeErr{Status: 429, Fast: true, Msg: fmt.Sprintf("Plane ist kurz ausgelastet – in %d s nochmal", int(math.Ceil(wait.Seconds())))}
			}
			if err := sleepCtx(ctx, wait); err != nil {
				return err
			}
			continue
		}
		limit := 45
		if interactive {
			limit = 58
		}
		if a != nil && a.DB != nil {
			if n, err := a.rateCount(ctx, "plane", key, time.Minute); err == nil && n >= limit {
				l.mu.Unlock()
				if interactive {
					return &planeErr{Status: 429, Fast: true, Msg: "Plane ist kurz ausgelastet – bitte gleich nochmal"}
				}
				if err := sleepCtx(ctx, 500*time.Millisecond); err != nil {
					return err
				}
				continue
			}
		}
		if len(l.sent) < limit {
			l.sent = append(l.sent, now)
			l.mu.Unlock()
			if a != nil && a.DB != nil {
				_ = a.rateAdd(ctx, "plane", key)
			}
			return nil
		}
		wait := time.Minute - now.Sub(l.sent[0]) + 50*time.Millisecond
		l.mu.Unlock()
		if interactive {
			return &planeErr{Status: 429, Fast: true, Msg: "Plane ist kurz ausgelastet – bitte gleich nochmal"}
		}
		if wait < 500*time.Millisecond {
			wait = 500 * time.Millisecond
		}
		if err := sleepCtx(ctx, wait); err != nil {
			return err
		}
	}
}

func (l *limiter) block(d time.Duration) (logIt bool) {
	return l.blockShared(nil, 0, d)
}

func (l *limiter) blockShared(a *App, uid int64, d time.Duration) (logIt bool) {
	l.mu.Lock()
	until := time.Now().Add(d)
	if until.After(l.blocked) {
		l.blocked = until
	}
	if time.Since(l.lastLog) > 30*time.Second {
		l.lastLog = time.Now()
		logIt = true
	}
	l.mu.Unlock()
	if a != nil && a.DB != nil {
		a.rateSetBlock(context.Background(), "plane", strconv.FormatInt(uid, 10), until)
	}
	return logIt
}

/* ── Anfragen ───────────────────────────────────────────────────── */

// Umleitungen werden nie gefolgt: Sonst ginge der API-Schlüssel an das Ziel mit, und ein umgeleitetes
// PATCH/POST/DELETE würde zum GET ohne Body und fälschlich als Erfolg zählen.
var metadataIPs = map[string]bool{"fd00:ec2::254": true, "100.100.100.200": true, "169.254.169.254": true, "192.0.0.192": true}

// Verbindungen zu Link-Local-Adressen (u. a. Cloud-Metadaten 169.254.169.254) sind immer gesperrt – auch nach DNS-Auflösung
var planeTransport = &http.Transport{
	Proxy: nil, // kein Proxy: Er würde die Zieladress-Sperre unten umgehen
	DialContext: (&net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || metadataIPs[ip.String()]) {
				return errors.New("Zieladresse nicht erlaubt")
			}
			return nil
		},
	}).DialContext,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 20 * time.Second,
	MaxIdleConnsPerHost:   4,
}

var planeHTTP = &http.Client{
	Transport:     planeTransport,
	Timeout:       20 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type planeOpts struct{ Interactive bool }

// planeDo schickt eine Anfrage mit Bremse und Wiederholung bei 429. Antwort: JSON (map, Liste) oder nil.
func (a *App) planeDo(ctx context.Context, uid int64, c planeCfg, method, path string, body any, o planeOpts) (any, error) {
	if !c.ok() {
		return nil, &planeErr{Status: 400, Msg: "Plane ist nicht eingerichtet (Adresse/Workspace in der Servereinrichtung, API-Schlüssel im Profil)"}
	}
	lim := a.limiterFor(uid)
	for attempt := 0; ; attempt++ {
		if err := lim.acquireShared(ctx, a, uid, o.Interactive); err != nil {
			return nil, err
		}
		res, err := a.planeOnce(ctx, c, method, path, body)
		var pe *planeErr
		if !errors.As(err, &pe) || pe.Status != 429 {
			return res, err
		}
		wait := pe.RetryAfter
		if wait <= 0 {
			wait = time.Duration(attempt+1) * 5 * time.Second
		}
		wait = min(max(wait, 2*time.Second), 65*time.Second)
		if lim.blockShared(a, uid, wait) {
			a.sysLog(ctx, uid, "info", "plane", fmt.Sprintf("Plane-Anfragelimit erreicht – Pause %d s", int(wait.Seconds())))
		}
		if o.Interactive || attempt >= 4 {
			return nil, pe
		}
	}
}

func (a *App) planeOnce(ctx context.Context, c planeCfg, method, path string, body any) (any, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.Key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := planeHTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // URL (und damit nichts Vertrauliches) nicht mit ausgeben
		}
		return nil, fmt.Errorf("Plane nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var out any
	if len(data) > 0 {
		_ = json.Unmarshal(data, &out)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return out, nil
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, &planeErr{Status: 400, Msg: fmt.Sprintf("Plane leitet um (HTTP %d) – Adresse prüfen (https:// statt http://, ohne abschließenden Pfad)", resp.StatusCode)}
	}
	msg := ""
	if m, ok := out.(map[string]any); ok {
		for _, k := range []string{"detail", "error", "message"} {
			if v, has := m[k]; has && v != nil {
				msg = asStr(v)
				if msg == "" {
					b, _ := json.Marshal(v)
					msg = string(b)
				}
				break
			}
		}
	}
	if msg == "" && len(data) > 0 {
		msg = string(data[:min(len(data), 200)])
	}
	hint := ""
	switch resp.StatusCode {
	case 404:
		hint = fmt.Sprintf(" – Adresse „%s“ oder Workspace „%s“ prüfen", c.Base, c.WS)
	case 401, 403:
		hint = " – API-Schlüssel prüfen"
	}
	pe := &planeErr{Status: resp.StatusCode, Msg: fmt.Sprintf("Plane HTTP %d%s%s", resp.StatusCode, map[bool]string{true: ": " + msg, false: ""}[msg != ""], hint)}
	if resp.StatusCode == 429 {
		pe.Msg = "Plane-Anfragelimit erreicht – bitte kurz warten"
		if ra, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); err == nil && ra > 0 {
			pe.RetryAfter = time.Duration(ra * float64(time.Second))
		} else if reset, err := strconv.ParseFloat(resp.Header.Get("X-RateLimit-Reset"), 64); err == nil && reset > 0 {
			if reset > 1e9 {
				pe.RetryAfter = time.Until(time.Unix(int64(reset), 0)) + 500*time.Millisecond
			} else {
				pe.RetryAfter = time.Duration(reset * float64(time.Second))
			}
		}
	}
	return nil, pe
}

/* ── JSON-Hilfen ─────────────────────────────────────────────────── */

func jm(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// listOf: Antwort ist eine Liste oder {results|issues: [...]}
func listOf(v any) []map[string]any {
	var raw []any
	switch x := v.(type) {
	case []any:
		raw = x
	case map[string]any:
		if r, ok := x["results"].([]any); ok {
			raw = r
		} else if r, ok := x["issues"].([]any); ok {
			raw = r
		}
	}
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func (c planeCfg) wsPath() string { return "/api/v1/workspaces/" + url.PathEscape(c.WS) }

func (c planeCfg) wlPath(project, wi, wl string) string {
	p := c.wsPath() + "/projects/" + url.PathEscape(project) + "/work-items/" + url.PathEscape(wi) + "/worklogs/"
	if wl != "" {
		p += url.PathEscape(wl) + "/"
	}
	return p
}

// planeListAll holt alle Seiten (bis 50 × 100)
func (a *App) planeListAll(ctx context.Context, uid int64, c planeCfg, path string, o planeOpts) ([]map[string]any, error) {
	var out []map[string]any
	cursor := ""
	for i := 0; i < 20; i++ {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		p := path + sep + "per_page=100"
		if cursor != "" {
			p += "&cursor=" + url.QueryEscape(cursor)
		}
		r, err := a.planeDo(ctx, uid, c, "GET", p, nil, o)
		if err != nil {
			return nil, err
		}
		out = append(out, listOf(r)...)
		m := jm(r)
		if m == nil || m["next_page_results"] != true || asStr(m["next_cursor"]) == "" {
			break
		}
		cursor = asStr(m["next_cursor"])
	}
	return out, nil
}

// planeMe fragt den angemeldeten Plane-Benutzer ab und merkt sich dessen Id (je Workspace, Legacy zusätzlich global).
func (a *App) planeMe(ctx context.Context, uid int64, c planeCfg, o planeOpts) (map[string]any, error) {
	r, err := a.planeDo(ctx, uid, c, "GET", "/api/v1/users/me/", nil, o)
	if err != nil {
		return nil, err
	}
	m := jm(r)
	if id := asStr(m["id"]); id != "" {
		key := planeMeSettingKey(c.ID)
		_, _ = a.DB.Exec(ctx, `INSERT INTO user_settings(user_id,key,value) VALUES($1,$2,$3) ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value`, uid, key, id)
		if c.ID == 0 || key != "plane_me_id" {
			// Primär/Legacy: auch unter plane_me_id halten, bis alle Leser umgestellt sind
			if c.ID == 0 {
				_, _ = a.DB.Exec(ctx, `INSERT INTO user_settings(user_id,key,value) VALUES($1,'plane_me_id',$2) ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value`, uid, id)
			} else if prim, ok := a.primaryPlaneWorkspace(ctx); ok && prim.ID == c.ID {
				_, _ = a.DB.Exec(ctx, `INSERT INTO user_settings(user_id,key,value) VALUES($1,'plane_me_id',$2) ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value`, uid, id)
			}
		}
	}
	return m, nil
}
