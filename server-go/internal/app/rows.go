package app

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* ── Zeilen als JSON-Objekte (gleiche Feldnamen wie das Node-System) ─────────── */

// maps führt eine Abfrage aus und liefert die Zeilen als Objekte. user_id und legacy_id bleiben intern.
func (a *App) maps(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	return mapsQ(ctx, a.DB, sql, args...)
}

// rowQuerier: Pool oder Transaktion (für maps in einer Transaktion)
type rowQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// mapsQ wie maps, aber auf einer beliebigen Verbindung/Transaktion
func mapsQ(ctx context.Context, q rowQuerier, sql string, args ...any) ([]map[string]any, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fds := rows.FieldDescriptions()
	out := []map[string]any{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, len(fds))
		for i, fd := range fds {
			if fd.Name == "user_id" || fd.Name == "legacy_id" {
				continue
			}
			if t, ok := vals[i].(time.Time); ok {
				m[fd.Name] = t.UTC().Format("2006-01-02 15:04:05")
			} else {
				m[fd.Name] = vals[i]
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (a *App) one(ctx context.Context, sql string, args ...any) (map[string]any, error) {
	rs, err := a.maps(ctx, sql, args...)
	if err != nil || len(rs) == 0 {
		return nil, err
	}
	return rs[0], nil
}

// ownedID prüft, dass die Zeile in table dem Benutzer gehört. nil-Id ist erlaubt (keine Verknüpfung).
func (a *App) owned(ctx context.Context, table string, id any, uid int64) bool {
	if id == nil {
		return true
	}
	var ok bool
	if err := a.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+table+" WHERE id=$1 AND user_id=$2)", id, uid).Scan(&ok); err != nil {
		a.Log.Error("Besitzprüfung fehlgeschlagen", "table", table, "err", err)
		return false
	}
	return ok
}

// invalidID: Wert ist angegeben, aber keine gültige Id (leer, null und 0 gelten als „keine Verknüpfung“).
func invalidID(v any) bool {
	if v == nil {
		return false
	}
	if s := asStr(v); s != "" && s != "0" {
		return asID(v) == nil
	}
	return false
}

/* ── Eingaben (JSON-Zahlen kommen als float64, Ids teils als Text) ──────────── */

type body map[string]any

func asStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return ""
}

// nz entspricht `x || null` aus dem Node-Code.
func nz(v any) any {
	if s := asStr(v); s != "" {
		return s
	}
	return nil
}

func asFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	}
	return 0
}

// asID liefert die Id oder nil (fehlend, 0, leer).
func asID(v any) any {
	n := int64(asFloat(v))
	if n == 0 {
		return nil
	}
	return n
}

func pathID(r *http.Request) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return n, err == nil
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "Nicht gefunden"})
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg})
}

/* ── Zeit ───────────────────────────────────────────────────────────────────── */

var timeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"}

func (a *App) parseTime(s string) (time.Time, bool) {
	for _, l := range timeLayouts {
		if t, err := time.ParseInLocation(l, s, a.Loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// dateOf = lokales Datum (Zeitzone des Servers/ZP_TZ) eines ISO-Zeitpunkts, wie dateOfIso im Node-System.
func (a *App) dateOf(iso string) string {
	if t, ok := a.parseTime(iso); ok {
		return t.In(a.Loc).Format("2006-01-02")
	}
	if len(iso) >= 10 {
		return iso[:10]
	}
	return iso
}
