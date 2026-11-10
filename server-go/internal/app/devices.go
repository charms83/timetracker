package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

/* Geräte-Token (Mac, Add-on, curl) + Annahme-Schnittstelle für Client-Hinweise.
   Klartext nur einmal; DB nur HMAC. Nie Admin. Abrechnung: eigene Integrations-Token (integrations.go). */

const deviceTokenPrefix = "zpdev_"
const deviceTokenHMACPrefix = "v1:" // Kennzeichnung gepfefferter Hashes (Migration von reinem SHA-256)

func (a *App) routesDevices(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/devices", a.auth(a.devicesList))
	mux.HandleFunc("POST /api/devices", a.auth(a.devicesCreate))
	mux.HandleFunc("DELETE /api/devices/{id}", a.auth(a.devicesRevoke))
	mux.HandleFunc("GET /api/client/hints", a.auth(a.clientHintsList))
	mux.HandleFunc("POST /api/client/hints/{id}/consume", a.auth(a.clientHintsConsume))
	mux.HandleFunc("POST /api/client/hints", a.deviceOrSession(a.clientHintsIngest))
}

// deviceTokenHashLegacy: alter SHA-256 ohne Server-Schlüssel (nur Abwärtskompatibilität).
func deviceTokenHashLegacy(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// deviceTokenHash: HMAC-SHA256 mit Server-Schlüssel – DB-Dump allein reicht nicht zur Verifikation.
func (a *App) deviceTokenHash(raw string) string {
	key := a.Key
	if len(key) == 0 {
		key = make([]byte, 32) // Tests ohne Key: deterministisch, nicht für Betrieb
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("device_token|v1|"))
	_, _ = mac.Write([]byte(raw))
	return deviceTokenHMACPrefix + hex.EncodeToString(mac.Sum(nil))
}

func (a *App) devicesList(w http.ResponseWriter, r *http.Request, u User) {
	rs, err := a.maps(r.Context(), `SELECT id, label, kind, created_at, last_used, revoked_at,
		(revoked_at IS NULL) AS active FROM device_tokens WHERE user_id=$1 ORDER BY created_at DESC`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *App) devicesCreate(w http.ResponseWriter, r *http.Request, u User) {
	if !u.timeAccount() {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Geräte-Token nur für Konten mit Zeiterfassung", "code": "admin_only_account"})
		return
	}
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	kind := strings.ToLower(strings.TrimSpace(asStr(in["kind"])))
	if kind == "" {
		kind = "mac"
	}
	switch kind {
	case "mac", "addon", "minimal", "api", "other": // api = Altbestand; Abrechnung nutzt zpint_…
	default:
		badRequest(w, "kind: mac, addon, minimal, api oder other")
		return
	}
	label := truncRunes(strings.TrimSpace(asStr(in["label"])), 80)
	if label == "" {
		label = kind
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		a.fail(w, err)
		return
	}
	raw := deviceTokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	ctx := r.Context()
	var id int64
	err := a.DB.QueryRow(ctx, `INSERT INTO device_tokens(user_id,token_hash,label,kind) VALUES($1,$2,$3,$4) RETURNING id`,
		u.ID, a.deviceTokenHash(raw), label, kind).Scan(&id)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit(ctx, u.ID, "device.create", fmt.Sprintf("#%d %s (%s)", id, label, kind))
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "label": label, "kind": kind, "token": raw,
		"hint": "Token nur einmal sichtbar – für Client/Add-on. Abrechnung: /admin › Abrechnung-API. Server speichert nur Hash.",
	})
}

func (a *App) devicesRevoke(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	ctx := r.Context()
	tag, err := a.DB.Exec(ctx, `UPDATE device_tokens SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		notFound(w)
		return
	}
	a.audit(ctx, u.ID, "device.revoke", "Gerät #"+fmt.Sprint(id))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// deviceUser: Benutzer aus Geräte-Token (nie Admin-Routen).
func (a *App) deviceUser(r *http.Request) (User, int64, bool) {
	raw := r.Header.Get("X-ZP-Device-Token")
	if raw == "" {
		if ah := r.Header.Get("Authorization"); strings.HasPrefix(ah, "Bearer ") {
			raw = strings.TrimSpace(strings.TrimPrefix(ah, "Bearer "))
		}
	}
	if raw == "" || !strings.HasPrefix(raw, deviceTokenPrefix) || len(raw) > 200 {
		return User{}, 0, false
	}
	ctx := r.Context()
	u, did, ok := a.lookupDeviceUser(ctx, a.deviceTokenHash(raw))
	if !ok {
		// Abwärtskompatibilität: alter SHA-256-Hash → einmalig auf HMAC umstellen
		u, did, ok = a.lookupDeviceUser(ctx, deviceTokenHashLegacy(raw))
		if !ok {
			return User{}, 0, false
		}
		_, _ = a.DB.Exec(ctx, `UPDATE device_tokens SET token_hash=$1 WHERE id=$2 AND revoked_at IS NULL`, a.deviceTokenHash(raw), did)
	}
	if !u.timeAccount() {
		return User{}, 0, false
	}
	_, _ = a.DB.Exec(ctx, `UPDATE device_tokens SET last_used=now() WHERE id=$1 AND (last_used IS NULL OR last_used < now() - interval '1 minute')`, did)
	return u, did, true
}

func (a *App) lookupDeviceUser(ctx context.Context, hash string) (User, int64, bool) {
	var u User
	var did int64
	err := a.DB.QueryRow(ctx, `SELECT d.id, u.id, u.email, u.name, u.role, u.kind, u.productive, u.must_change_password, u.is_hr,
		EXISTS(SELECT 1 FROM team_members m WHERE m.user_id=u.id AND m.is_lead)
		FROM device_tokens d JOIN users u ON u.id=d.user_id
		WHERE d.token_hash=$1 AND d.revoked_at IS NULL AND NOT u.disabled AND u.role<>'breakglass' AND u.role<>'admin'`, hash).
		Scan(&did, &u.ID, &u.Email, &u.Name, &u.Role, &u.Kind, &u.Productive, &u.MustChange, &u.IsHR, &u.IsLead)
	if err != nil {
		return User{}, 0, false
	}
	return u, did, true
}

// deviceOrSession: Sitzung ODER Geräte-Token (für Client-Ingest).
func (a *App) deviceOrSession(h func(http.ResponseWriter, *http.Request, User, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u, did, ok := a.deviceUser(r); ok {
			h(w, r, u, did)
			return
		}
		a.auth(func(w http.ResponseWriter, r *http.Request, u User) {
			h(w, r, u, 0)
		})(w, r)
	}
}

func (a *App) clientHintsList(w http.ResponseWriter, r *http.Request, u User) {
	q := r.URL.Query()
	sql := `SELECT * FROM client_hints WHERE user_id=$1`
	args := []any{u.ID}
	if q.Get("open") != "0" {
		sql += " AND consumed_at IS NULL"
	}
	if d := q.Get("from"); d != "" {
		args = append(args, d)
		sql += fmt.Sprintf(" AND day>=$%d", len(args))
	}
	if d := q.Get("to"); d != "" {
		args = append(args, d)
		sql += fmt.Sprintf(" AND day<=$%d", len(args))
	}
	rs, err := a.maps(r.Context(), sql+" ORDER BY day DESC, id DESC LIMIT 500", args...)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *App) clientHintsConsume(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	tag, err := a.DB.Exec(r.Context(), `UPDATE client_hints SET consumed_at=now() WHERE id=$1 AND user_id=$2 AND consumed_at IS NULL`, id, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// clientHintsIngest: Vorschläge vom Client (keine Buchungen). Body: {hints:[{kind,day,start_time?,end_time?,title?,payload?,dedupe_key?}]}
func (a *App) clientHintsIngest(w http.ResponseWriter, r *http.Request, u User, deviceID int64) {
	ctx := r.Context()
	// Geräte-Token: eng begrenzen (Missbrauch / Flooding); Sitzung bleibt großzügiger über Protect.
	if deviceID > 0 {
		key := fmt.Sprintf("d:%d", deviceID)
		if !a.rateReserve(ctx, "client_hints", key, 60, time.Minute) {
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Zu viele Hinweise – bitte später erneut", "code": "rate_limited"})
			return
		}
	}
	var in struct {
		Hints []body `json:"hints"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Hints) == 0 {
		badRequest(w, "hints erforderlich")
		return
	}
	maxHints := 50
	if deviceID == 0 {
		maxHints = 200
	}
	if len(in.Hints) > maxHints {
		badRequest(w, fmt.Sprintf("höchstens %d Hinweise je Aufruf", maxHints))
		return
	}
	var deviceRef any
	if deviceID > 0 {
		deviceRef = deviceID
	}
	accepted, skipped := 0, 0
	for _, h := range in.Hints {
		kind := strings.ToLower(asStr(h["kind"]))
		switch kind {
		case "ssh", "meeting", "calendar", "presence", "other":
		default:
			skipped++
			continue
		}
		day := asStr(h["day"])
		if !isoDateRE.MatchString(day) {
			skipped++
			continue
		}
		title := truncRunes(asStr(h["title"]), 200)
		dedupe := truncRunes(asStr(h["dedupe_key"]), 200)
		var start, end any
		if s := asStr(h["start_time"]); s != "" {
			if t, ok := parseTimeIn(s, a.Loc); ok {
				start = t.UTC()
			}
		}
		if s := asStr(h["end_time"]); s != "" {
			if t, ok := parseTimeIn(s, a.Loc); ok {
				end = t.UTC()
			}
		}
		payload, _ := json.Marshal(h["payload"])
		if payload == nil || string(payload) == "null" {
			payload = []byte("{}")
		}
		if len(payload) > 4096 { // Payload-Cap gegen aufgeblähte Client-Daten
			skipped++
			continue
		}
		tag, err := a.DB.Exec(ctx, `INSERT INTO client_hints(user_id,device_id,kind,day,start_time,end_time,title,payload,dedupe_key)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9)
			ON CONFLICT (user_id, kind, dedupe_key) WHERE (dedupe_key <> '') DO NOTHING`,
			u.ID, deviceRef, kind, day, start, end, title, string(payload), dedupe)
		if err != nil {
			a.fail(w, err)
			return
		}
		if tag.RowsAffected() > 0 {
			accepted++
		} else {
			skipped++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": accepted, "skipped": skipped})
}
