package app

import (
	"net/http"
	"sort"
	"strings"

	"example/zeitprotokoll-server/internal/ldapauth"
)

/* Web-Maske LDAP/OIDC in /admin (statt nur CLI). Secrets nie in GET; leeres Passwort/Secret = unverändert. */

func (a *App) routesAdminAuth(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/ldap", a.strictAdmin(a.adminLDAPGet))
	mux.HandleFunc("PUT /api/admin/ldap", a.strictAdmin(a.adminLDAPPut))
	mux.HandleFunc("POST /api/admin/ldap/test", a.strictAdmin(a.adminLDAPTest))
	mux.HandleFunc("GET /api/admin/oidc", a.strictAdmin(a.adminOIDCGet))
	mux.HandleFunc("PUT /api/admin/oidc", a.strictAdmin(a.adminOIDCPut))
}

func (a *App) adminLDAPGet(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	out := map[string]any{"auth_mode": a.conf(ctx, "auth_mode")}
	keys := make([]string, 0, len(LDAPKeys))
	for k := range LDAPKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch k {
		case "ldap_bind_password":
			out["ldap_bind_password_set"] = a.conf(ctx, k) != ""
		case "ldap_ca":
			v := a.conf(ctx, k)
			out[k] = v
			out["ldap_ca_set"] = v != ""
			out["ldap_ca_len"] = len(v)
		case "ldap_link_user", "ldap_link_subject":
			// einmalige Verknüpfung: nur CLI, nicht in der Maske editieren
		default:
			out[k] = a.conf(ctx, k)
			if out[k] == "" {
				out[k] = LDAPKeys[k]
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) adminLDAPPut(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	changed := []string{}
	for k, raw := range in {
		key := k
		if !strings.HasPrefix(key, "ldap_") {
			key = "ldap_" + key
		}
		if _, ok := LDAPKeys[key]; !ok {
			continue
		}
		if key == "ldap_link_user" || key == "ldap_link_subject" {
			continue
		}
		v := asStr(raw)
		if key == "ldap_bind_password" && v == "" {
			continue // unverändert
		}
		if err := SetLDAPConfig(ctx, a.DB, a.Key, key, v); err != nil {
			badRequest(w, err.Error())
			return
		}
		changed = append(changed, key)
	}
	sort.Strings(changed)
	a.audit(ctx, u.ID, "admin.ldap", "Einstellungen: "+strings.Join(changed, ", "))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "changed": changed})
}

func (a *App) adminLDAPTest(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	username := strings.TrimSpace(in.Username)
	if username == "" || in.Password == "" {
		badRequest(w, "username und password erforderlich")
		return
	}
	ctx := r.Context()
	cfg, err := LoadLDAPConfig(ctx, a.DB, a.Key)
	if err != nil {
		badRequest(w, "LDAP nicht eingerichtet: "+err.Error())
		return
	}
	if err := cfg.Validate(); err != nil {
		badRequest(w, err.Error())
		return
	}
	res, err := ldapauth.Authenticate(ctx, cfg, username, in.Password)
	if err != nil {
		a.audit(ctx, u.ID, "admin.ldap_test", "fehlgeschlagen für "+trim(username, 64))
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	a.audit(ctx, u.ID, "admin.ldap_test", "ok für "+trim(username, 64))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "name": res.Name, "mail": res.Mail, "subject": res.ID, "role": res.Role,
		"groups": len(res.Groups),
	})
}

func (a *App) adminOIDCGet(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	out := map[string]any{
		"auth_mode":     a.conf(ctx, "auth_mode"),
		"callback_url":  strings.TrimRight(a.PublicURL, "/") + "/api/auth/oidc/callback",
		"public_url_set": a.PublicURL != "",
	}
	keys := make([]string, 0, len(OIDCKeys))
	for k := range OIDCKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "oidc_client_secret" {
			out["oidc_client_secret_set"] = a.conf(ctx, k) != ""
			continue
		}
		out[k] = a.conf(ctx, k)
		if out[k] == "" {
			out[k] = OIDCKeys[k]
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) adminOIDCPut(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	changed := []string{}
	for k, raw := range in {
		key := k
		if !strings.HasPrefix(key, "oidc_") {
			key = "oidc_" + key
		}
		if _, ok := OIDCKeys[key]; !ok {
			continue
		}
		v := asStr(raw)
		if key == "oidc_client_secret" && v == "" {
			continue
		}
		if err := SetOIDCConfig(ctx, a.DB, a.Key, a.AllowHTTP, key, v); err != nil {
			badRequest(w, err.Error())
			return
		}
		changed = append(changed, key)
	}
	sort.Strings(changed)
	a.audit(ctx, u.ID, "admin.oidc", "Einstellungen: "+strings.Join(changed, ", "))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "changed": changed})
}
