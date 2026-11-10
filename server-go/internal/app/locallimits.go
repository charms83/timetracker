package app

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Queryer: Pool oder Transaktion
type Queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ErrLocalLimits: Verstoß gegen die Regeln des Einzelbetriebs (siehe CheckLocalLimits)
var ErrLocalLimits = errors.New("Einzelbetrieb: Solange authentik/LDAP nicht eingerichtet ist, darf nur eine Person produktiv arbeiten (und ein lokales Testkonto „Administrator“ daneben). Weitere Mitarbeitende brauchen die Anmeldung über authentik oder LDAP/AD.")

// CheckLocalLimits prüft die Regeln des Einzelbetriebs über ALLE aktiven lokalen Konten (ohne Notfallzugang):
// höchstens eine produktive Person und höchstens ein Testkonto, und das Testkonto muss Administrator sein.
// Ohne diese Grenze ließen sich mit „nicht produktiv“ beliebig viele buchende Konten anlegen.
// Aufrufer: Anlegen, Entsperren, Rolle/produktiv ändern, Einrichtung, Wechsel der Anmeldeart – jeweils in der Transaktion der Änderung.
func CheckLocalLimits(ctx context.Context, q Queryer) error {
	var prod, test, testNonAdmin int
	err := q.QueryRow(ctx, `SELECT
		COUNT(*) FILTER (WHERE productive),
		COUNT(*) FILTER (WHERE NOT productive),
		COUNT(*) FILTER (WHERE NOT productive AND role <> 'admin')
		FROM users WHERE kind='local' AND role <> 'breakglass' AND NOT disabled`).Scan(&prod, &test, &testNonAdmin)
	if err != nil {
		return err // im Zweifel ablehnen
	}
	if prod > 1 || test > 1 || testNonAdmin > 0 {
		return ErrLocalLimits
	}
	return nil
}
