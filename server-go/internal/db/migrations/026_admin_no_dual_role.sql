-- Technischer Admin ist kein Mischkonto: keine Zeiterfassung (wie breakglass für Zeiten).
-- LDAP/OIDC-Admin-Gruppe → getrenntes Admin-Konto; Mitarbeiter bleiben in user/lead/hr-Gruppen.
UPDATE users SET productive = false WHERE role = 'admin' AND productive = true;
