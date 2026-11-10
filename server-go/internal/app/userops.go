package app

import "net/http"

// routesUserOps: Übersichten Teamleiter/Personal und restliche Nutzer-Routen der bestehenden Oberfläche (Parität Gruppe E)
func (a *App) routesUserOps(mux *http.ServeMux) {
	a.routesTeamView(mux)     // /api/team/overview, /api/team/members, /api/personal/*
	a.routesAuditView(mux)    // /api/audit, /api/audit/verify
	a.routesIntegrity(mux)    // /api/check/integrity
	a.routesProjectTools(mux) // Zusammenführen, Namen angleichen, Doppelte, Lokale Zeiten
	a.routesCleanup(mux)      // /api/cleanup/*
	a.routesTicket(mux)       // /api/ticket/ensure, /api/ticket/start
}
