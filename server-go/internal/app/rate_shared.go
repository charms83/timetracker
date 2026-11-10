package app

import (
	"context"
	"fmt"
	"time"
)

/* Gemeinsame Zähler (Migration 022): Anmeldesperre und Plane-/Jira-Bremse über PostgreSQL,
   damit mehrere Pods dieselbe Grenze sehen. Ohne DB (Unit-Tests) bleibt der Speicher-Limiter. */

func (a *App) rateCount(ctx context.Context, kind, key string, window time.Duration) (int, error) {
	var n int
	err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM rate_events WHERE kind=$1 AND key=$2 AND at > now() - $3::interval`,
		kind, key, fmt.Sprintf("%f seconds", window.Seconds())).Scan(&n)
	return n, err
}

func (a *App) rateAdd(ctx context.Context, kind, key string) error {
	_, err := a.DB.Exec(ctx, `INSERT INTO rate_events(kind,key) VALUES($1,$2)`, kind, key)
	return err
}

func (a *App) rateRemoveLast(ctx context.Context, kind, key string) {
	_, _ = a.DB.Exec(ctx, `DELETE FROM rate_events WHERE ctid = (
		SELECT ctid FROM rate_events WHERE kind=$1 AND key=$2 ORDER BY at DESC LIMIT 1)`, kind, key)
}

// rateReserve: Ereignis eintragen; false wenn schon ≥ max im Fenster (inkl. dieses Versuchs).
func (a *App) rateReserve(ctx context.Context, kind, key string, max int, window time.Duration) bool {
	if a == nil || a.DB == nil || key == "" {
		return true
	}
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return true // bei DB-Fehler nicht die Anmeldung hart sperren – Speicher-Limiter greift noch
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var n int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM rate_events WHERE kind=$1 AND key=$2 AND at > now() - $3::interval`,
		kind, key, fmt.Sprintf("%f seconds", window.Seconds())).Scan(&n); err != nil {
		return true
	}
	if n >= max {
		return false
	}
	if _, err := tx.Exec(ctx, `INSERT INTO rate_events(kind,key) VALUES($1,$2)`, kind, key); err != nil {
		return true
	}
	return tx.Commit(ctx) == nil
}

func (a *App) rateBlocked(ctx context.Context, kind, key string, max int, window time.Duration) bool {
	if a == nil || a.DB == nil {
		return false
	}
	n, err := a.rateCount(ctx, kind, key, window)
	return err == nil && n >= max
}

func (a *App) rateFail(ctx context.Context, kind, key string) {
	if a == nil || a.DB == nil || key == "" {
		return
	}
	_ = a.rateAdd(ctx, kind, key)
}

func (a *App) rateRelease(ctx context.Context, kind, key string) {
	if a == nil || a.DB == nil {
		return
	}
	a.rateRemoveLast(ctx, kind, key)
}

func (a *App) rateSetBlock(ctx context.Context, kind, key string, until time.Time) {
	if a == nil || a.DB == nil || key == "" || until.IsZero() {
		return
	}
	_, _ = a.DB.Exec(ctx, `INSERT INTO rate_blocks(kind,key,until_at) VALUES($1,$2,$3)
		ON CONFLICT (kind,key) DO UPDATE SET until_at=GREATEST(rate_blocks.until_at, EXCLUDED.until_at)`, kind, key, until.UTC())
}

func (a *App) rateBlockUntil(ctx context.Context, kind, key string) time.Time {
	if a == nil || a.DB == nil {
		return time.Time{}
	}
	var until time.Time
	err := a.DB.QueryRow(ctx, `SELECT until_at FROM rate_blocks WHERE kind=$1 AND key=$2 AND until_at > now()`, kind, key).Scan(&until)
	if err != nil {
		return time.Time{}
	}
	return until
}

// ratePrune: alte Ereignisse/Sperren wegräumen (Selbsttest / Hintergrund).
func (a *App) ratePrune(ctx context.Context) {
	if a == nil || a.DB == nil {
		return
	}
	_, _ = a.DB.Exec(ctx, `DELETE FROM rate_events WHERE at < now() - interval '2 hours'`)
	_, _ = a.DB.Exec(ctx, `DELETE FROM rate_blocks WHERE until_at < now()`)
}

/* ── Anmeldung: Speicher + gemeinsame DB ─────────────────────────── */

func (a *App) loginReserve(kind, key string, lim *failLimiter) bool {
	if lim == nil || !lim.reserve(key) {
		return false
	}
	if a.DB == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !a.rateReserve(ctx, kind, key, lim.max, lim.window) {
		lim.release(key)
		return false
	}
	return true
}

func (a *App) loginRelease(kind, key string, lim *failLimiter) {
	if lim != nil {
		lim.release(key)
	}
	if a.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a.rateRelease(ctx, kind, key)
}

func (a *App) loginFailRec(kind, key string, lim *failLimiter) {
	if lim != nil {
		lim.fail(key)
	}
	if a.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a.rateFail(ctx, kind, key)
}

func (a *App) loginBlocked(kind, key string, lim *failLimiter) bool {
	if lim != nil && lim.blocked(key) {
		return true
	}
	if a.DB == nil || lim == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return a.rateBlocked(ctx, kind, key, lim.max, lim.window)
}
