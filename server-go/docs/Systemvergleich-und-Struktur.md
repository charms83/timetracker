# Systemvergleich und Strukturvorschlag (Stand 10.11.2026)

Zweck: Zeiterfassungssysteme vergleichen, Lücken im Konzept des Zeitprotokoll-Team-Servers finden, Struktur je Oberfläche vorschlagen. Grundlage intern: Gesamt-Spezifikation (05.–10.11.), Konzept Team-Server, Parität Oberfläche, Navigation `frontend/src/App.jsx`, `server-go/internal/app/admin.html`. Web-Quellen: Abruf am 10.11.2026; nur Belegtes, Herstellerangaben nur als solche übernommen. Nicht belegbar = „offen“.

## 1. Vergleichstabelle

| System | Rollen / Oberflächen | Freigabe / Sperre | Auswertungen / Exporte | Stärken | Passt für Beispiel? | Quelle (Abruf 10.11.2026) |
|---|---|---|---|---|---|---|
| **Tempo Timesheets** (Jira Cloud/DC) | Rechte statt feste Rollen: Team Lead (je Team), „Approve Timesheets“, Account Lead, Account Administrator, Tempo-Admin. Eine Oberfläche in Jira mit rechteabhängigen Ansichten (My Work, Teams/Approvals, Reports, Accounts). Teams sind Voraussetzung für Freigabe | Freigabeperioden vom Admin (meist Abrechnungszeitraum); Person reicht ein und wählt einen Prüfer (Team Lead oder Inhaber des Rechts); Status Open, Ready to Submit, Waiting for Approval, Approved, Rejected; nach Einreichen keine Buchung/Änderung in der Periode; Team Lead kann stellvertretend einreichen; Prüfer darf eigenen Zettel nicht freigeben; Wiedereröffnen durch Team Lead/Prüfer (Status Open); Periode zusätzlich durch Admin schließbar (Period Management); optionale E-Mail-Erinnerungen und Benachrichtigungen (Scheduler). Freigabe nur für ganze Periode | Reports als CSV/Excel (Cloud, auch PDF/Druck), DC: Excel mit Tabs Worklogs/Users, PDF-Export ab 16.0.0 entfernt; Account-Berichte (Zeit und Kosten, PDF/Excel) für Account Lead/Admin; Gruppierung nach Team | Referenz für Ablösung: Teams, Prüferwahl, Perioden, Sperre, Account-/Kundenberichte | Maßstab. Zu übernehmen: Perioden, Perioden-Schließung, Vertretung (Einreichen durch Team Lead), Erinnerungen, Account-Berichte. Nicht: Prüferwahl je Einreichung (bei Beispiel feste Teamleitung einfacher), keine Selbstfreigabe ist zu übernehmen | help.tempo.io/timesheets/latest/timesheet-approvals; help.tempo.io/timesheets-dc/latest/submitting-timesheets-for-approval-tempo-server; Websuche zu Reports/Export (help.tempo.io …/exporting-reports, …/exporting-reports-tempo-server, …/downloading-account-report-pdfs-tempo-server) |
| **Kimai** (Open Source) | Vier Rollen: User, Teamlead, Administrator (Inhalt/Zeiten, keine Benutzerverwaltung), System-Admin (alles inkl. Benutzer, Plugins); Rechte je Rolle änderbar (System > Rollen). Zusätzlich „Teamlead“ je Team (Sichtbarkeit der Mitglieder-Zeiten, Projekte, Kunden); Teams begrenzen Sichtbarkeit von Kunden/Projekten | Kern kennt keine Monatsfreigabe. Freigabe per Plugin (Approval Bundle: Woche einreichen, Teamlead/Admin genehmigen oder ablehnen, danach gesperrt außer Admin; Sperrdatum je Person per Zusatzplugin). Kern: Export-Markierung sperrt exportierte Datensätze für normale Benutzer. Controlling-Plugin: Monate schließen/genehmigen, Entsperren per eigener Berechtigung | Export PDF, XLSX, CSV, HTML; Rechnungen als PDF, HTML, XLSX, ODS, DOCX mit Steuern/Rechnungsnummern; „exportiert“-Status schließt Datensätze aus Folgeexporten aus | Saubere Rollen-/Team-Trennung; Export-Markierung; Open Source | Teilweise: Rollenmodell (Teamlead je Team + Rolle) und „exportiert“-Sperre sind gute Vorlagen. Freigabe nur als Plugin, Rechnungswesen nicht Ziel von Beispiel (Abrechnungsprogramm offen) | kimai.org/documentation/permissions.html; kimai.org/documentation/teams.html; kimai.org/documentation/export.html; Websuche Kimai-Store (katjaglass-approval-bundle, controlling) |
| **Clockify** | Rollen Owner, Admin, Team Manager, Project Manager, Benutzer; Admin überstimmt andere Rollen; Rollen kombinierbar (nur eine davon darf je Einstellung freigeben). Eine Weboberfläche, Bereiche je Recht (Approvals, Reports, Team) | Einstufig: wöchentlich, halbmonatlich oder monatlich (ab Standard-Tarif); Rückweisung nur mit Notiz; Status eingereicht/freigegeben/abgelehnt/zurückgezogen; Freigegebenes ist gesperrt; Zurückziehen mit Notiz; Admin-Freigabe nicht durch Manager überschreibbar; Erinnerungen „zum Einreichen“ und „zum Freigeben“; Mehrstufig (Level 1/2, in Beta laut Seite). Zusätzlich Sperre: Datum oder automatisch (z. B. Vorwoche jeden Mittwoch); Entsperr-Rolle nicht belegt | Berichte (Detailliert etc.): Aussagen im Detail nicht abgerufen = offen | Erinnerungen, Zurückziehen mit Notiz, automatische Sperre, Admin-Override | Teilweise: Erinnerungen, automatische Sperre, Notizpflicht bei Rückweisung übernehmen. Mehrstufig nur falls Personal zweite Stufe wird | clockify.me/help/track-time-and-expenses/approval; …/multi-level-approval; clockify.me/help/users/managers; Websuche (clockify.me/help/?p=4715 u. a.) |
| **Toggl Track** | Org-Admin, Workspace-Admin, Manager, Mitglied, Time Tracker, Gast (Doku „Toggl 2.0“); Berichte rollenabhängig: Admins sehen alle Zeiten, Manager nur eigene außer als Team-/Projektleiter, Mitglieder nur eigene; Kosten/Profitabilität nur Admin/Manager | Genehmigung ist Premium/Enterprise; mehrere Genehmiger oder Teams möglich; Workspace-Admins sehen/bearbeiten Genehmigungen (Details des Ablaufs nicht abgerufen = offen). Sperrdatum nur Org-/Workspace-Admin setzbar; gesperrte Zeiten bearbeiten Admins und Manager | Berichte teilbar, öffentliche Links nur Admins; Export-Formate nicht abgerufen = offen | Rollenabhängige Berichte, Sperrdatum | Gering: Berichts-Freigabelinks unerwünscht (Datenschutz); Rollenabstufung der Berichte als Vorbild | docs.toggl.com/user-roles-in-toggl-2-0; Websuche (docs.toggl.com, support.toggl.com Access rights) |
| **Harvest** | Member, Manager, Administrator, weitere (Project Manager, People Admin, Accounting, Executive Manager laut Suchtreffer; nicht im Original geprüft). Manager erhalten per Administrator zugewiesene Personen („People assignments“) | Modul „Timesheet approval“: Woche einreichen, ein Manager/Admin genügt, Freigabe sperrt die ganze Woche; Entsperren (Freigabe zurückziehen) durch Administrator, bei Sonderrecht auch Manager; Approval-Log im Aktivitätsprotokoll | nicht im Original abgerufen = offen | Zuweisung Personen↔Manager statt Teams; Aktivitätsprotokoll der Freigaben | Teilweise: Personenzuweisung als Alternative zu Teams; Wochenfreigabe passt nicht (Beispiel: Monat) | Websuche (support.getharvest.com/hc/en-us/articles/360048181572, …/4422314817677, …/360048181832 [Abruf 403], getharvest.com/time-tracking/approve-timesheets) |
| **Jira-nativ** (Worklogs) | Projektrechte: Work On Issues, Edit/Delete Own/All Worklogs; Worklog für andere Personen nicht eingebaut (Tempo/Apps liefern das) | keine Freigabe, keine Sperre je Monat | keine eigene Auswertung/Exporte belegt (kein eingebauter Zeitbericht mit eigener Berechtigung gefunden) | Worklog je Ticket, überall vorhanden | Nur als Ziel der Buchung (Brücke) | support.atlassian.com/jira-cloud-administration/docs/time-tracking-permissions/ (Websuche); community.atlassian.com (Nutzerberichte) |
| **Plane-nativ** (Work-Item-Worklogs) | Zeit buchen: Projekt-Admin und Contributor; Commenter/Gäste nicht; Projekt-Admin bearbeitet/löscht fremde Worklogs; je Projekt einzuschalten; Pro-Tarif | keine Freigabe, keine Sperre (Seite erwähnt keine) | Workspace-Einstellungen > Worklogs: Excel/CSV, filterbar nach Person, Projekt, Zeitraum; Berichte über Projekte; API: Worklog anlegen/ändern, Summen je Work Item | Worklog direkt am Work Item, Export vorhanden | Ziel der Buchung; endet bei Beginn/Ende/Pausen (nur Dauer), Freigabe, Teams, Monatsabschluss, Lohn-/Kundenexport | docs.plane.so/core-concepts/issues/time-tracking; developers.plane.so/api-reference/worklogs/overview (Websuche) |

Einschränkung: Preise/Tarife ändern sich; Tarifangaben (Standard, Premium, Pro) nur als Hinweis.

## 2. Gleich, anders, Lücken

**Gleich wie die Vergleichssysteme:** Einreichen → Freigeben/Ablehnen mit Kommentar; Sperre nach Einreichen; Teamleiter je Team; Wiedereröffnen; Rollen kombinierbar (Clockify, Kimai); Hauptoberfläche für alle plus Verwaltungsbereich.

**Anders (Beispiel-Entscheidungen):**
- Zeiten mit Beginn/Ende/Pausen als Quelle der Wahrheit; Plane bekommt nur Summen. Keines der Systeme sitzt „über“ einem anderen Ticketsystem; Tempo ist an Jira gebunden.
- Vier getrennte Oberflächen (Admin ohne Zeitdaten). Üblich ist eine Oberfläche mit rechteabhängigen Bereichen; vollständig getrennte Admin-Sicht ohne Zeiten kennt keines der geprüften Systeme belegt (Kimai-System-Admin und Tempo-Admin sehen Zeiten bzw. können Rechte vergeben). Vorteil Datenschutz/ISMS.
- Wiedereröffnen nur durch Personal mit Begründung; bei Tempo kann der Team Lead wiedereröffnen, bei Clockify/Harvest der Admin. Strenger.
- Lokale Erkennung (SSH, Meetings, Anwesenheit) nur im Client; Ticket-Vorschläge dreifach abschaltbar. Kein Vergleichssystem belegt.
- Mehrere Jira-Instanzen/Plane-Workspaces durch Admin.

**Lücken im Konzept (Priorität: M = muss für Tempo-Ablösung, S = sollte, L = später):**

| Nr | Lücke | Prio | Begründung / Vorbild |
|---|---|---|---|
| 1 | Teams: Pflege (in `/admin`, aus Verzeichnisgruppen oder Tempo-Import), Mitglied in mehreren Teams, Teamleiter-Vertretung | M | Tempo: Teams sind Voraussetzung der Freigabe; Spezifikation nennt „Teams: offen“ |
| 2 | Freigabeperioden und Periodenschluss (Monat; Frist einreichen/freigeben, automatische Sperre nach Frist) | M | Tempo Period Management; Clockify automatische Sperre; Konzept hat nur Vorschlag 3./5. Werktag |
| 3 | Regel „keine Selbstfreigabe“: Teamleiter/Personal reichen eigenen Monat ein, wer gibt frei? (Personal-Mitglied ohne Prüfer) | M | Tempo und Kimai-Praxis; im Konzept nicht geregelt. Offen |
| 4 | Vertretung beim Einreichen (Teamleiter reicht für Mitglied ein, z. B. Urlaub, Krankheit) | S | Tempo (Team Lead submit on behalf) |
| 5 | Vertretung/Delegation beim Freigeben (Teamleiter abwesend) | S | In den geprüften Quellen für Tempo nicht belegt; Clockify/Harvest lösen es über Admin/mehrere Genehmiger. Für Beispiel: Personal kann jede Freigabe übernehmen |
| 6 | Erinnerungen und Benachrichtigungen (Einreichen fällig, Freigabe fällig, abgelehnt/freigegeben) | M | Tempo und Clockify; hängt an E-Mail-Versand (offen). Mindestens In-App-Hinweis |
| 7 | Ablehnen mit Pflicht-Kommentar, Rückweisung markiert einzelne Tage/Einträge | S | Clockify: Notizpflicht; Tempo: nur Gesamtstatus |
| 8 | Freigabehistorie je Monat (Wer, Wann, Status, Kommentar) sichtbar für Person, Teamleiter, Personal | M | Tempo Approval Log (Quelle: Suchzusammenfassung, nicht im Original geprüft); Harvest Aktivitätsprotokoll. Audit-Hash-Kette ist da, Ansicht fehlt |
| 9 | Auswertungen: Team-Bericht (Ist/Soll, Salden, Abwesenheiten), Kunden-/Account-Bericht, Projekt-/Ticketbericht, Vollständigkeit („wer hat nicht eingereicht“) | M | Tempo Reports/Account-Reports; Spezifikation Leitbild nennt Kunden-/Account-Auswertung |
| 10 | Exporte: Lohn (Monat, freigegebene), Kunden (Zeit je Ticket/Kunde mit Beschreibung), Format CSV/XLSX; Markierung „exportiert“ mit Sperre | M | Kimai Export-Marker; Konzept: Exporte nur freigegebene Monate. Formate offen |
| 11 | Grundimport Tempo (Historie) und Plane (Lücken) | M | Spezifikation „offen“ |
| 12 | Sichtbarkeit von Kunden/Projekten je Team | L | Kimai Teams beschränken Kunden/Projekte; bei Beispiel nicht gefordert, offen |
| 13 | Rollenmodell als Merkmale (`is_lead`, `is_hr`, `is_admin`) plus Verzeichnisgruppen | M | Spezifikation Abschnitt 4 |
| 14 | Schreiben für andere (Korrektur fremder Zeiten durch Personal/Teamleiter, mit Pflicht-Begründung und Audit) | S | Jira/Tempo: „Worklog für andere“ nur über Tempo; Clockify Detailbericht |
| 15 | Vertretung Personal (Mehrere Personen mit Recht; „Vier-Augen“ bei Wiedereröffnen?) | L | offen |
| 16 | Abwesenheiten: Genehmigung von Urlaub/Antrag, Resturlaub, Zeitkonto | L | Toggl/Kimai-Plugin kennen Urlaubsverwaltung; Konzept hat Abwesenheiten erfassen, kein Antragswesen. Offen, ob gewünscht |
| 17 | Mobil/PWA: Einreichen und Freigeben von unterwegs | L | Konzept „später“; Tempo Mobile belegt |
| 18 | Datenschutz: Teamleiter sehen Einzelbuchungen; Betriebsvereinbarung; Aufbewahrung | M (vor Pilot) | Spezifikation Abschnitt 11 |
| 19 | Auskunft DSGVO (eigene Daten exportieren) | S | Konzept nennt es |

## 3. Strukturvorschlag je Oberfläche

Grundsatz: Eine Anmeldung, vier Bereiche. Wer mehrere Rollen hat, sieht oben einen Umschalter (Mitarbeiter · Team · Personal; Administration nur für `is_admin`, getrennter Eintritt `/admin`). Jede Seite nennt oben den Monat/Zeitraum und den Freigabestatus.

### 3.1 Mitarbeiter (`/`, bisheriges Zeitprotokoll)
Navigation, Reihenfolge:
1. **Heute** – Timer, Tagesübersicht, Buchen mit Beginn/Ende/Pause.
2. **Woche** – Wochenraster, Nachtragen, Lücken.
3. **Kalender** – Termine, Meetings, Abwesenheiten im Monat (zusammenlegen mit Abwesenheiten, siehe unten).
4. **To-dos** – Planung und Aufgabenliste.
5. **Vorschläge** – Ticket-/Zeitvorschläge; Eintrag nur sichtbar, wenn nicht abgeschaltet.
6. **Übertragen** – Zeiten nach Plane und Jira/Tempo buchen, Fehlendes zuordnen (Status „needs“).
7. **Monat** (bisher „Stundenzettel“) – Soll/Ist, Saldo, Prüfhinweise, Einreichen, Status und Historie.
8. **Auswertung** – eigene Zeiten nach Ticket, Projekt, Tag; Projekte-Ansicht dort als Reiter.
9. **Einstellungen** – nur persönliche Dinge (siehe 3.5).
Bisherige Gruppen „Erfassen / Prüfen / Auswerten / Tickets“ bleiben erhalten; „Plane“ und „Jira · Tempo“ als eigene Menüpunkte entfallen, ihr Inhalt geht in „Übertragen“ (Status/Fehler) und „Einstellungen > Verbindungen“ (persönliche Token). Der Assistent bleibt eigener Punkt, solange gebaut (laut Spezifikation offen). SSH-Sitzungen stehen unter „Vorschläge“/Erkennung (nur mit Client).

### 3.2 Teamleiter (`/team`)
1. **Team heute** – wer ist gebucht/abwesend, Warnungen (fehlende Stunden).
2. **Freigaben** – Monate der Teammitglieder: eingereicht, überfällig, freigegeben; Freigeben/Ablehnen mit Kommentar.
3. **Team-Zeiten** – Einzelbuchungen und Salden je Person (Soll/Ist, Überstunden), Filter Zeitraum/Ticket.
4. **Team-Auswertung** – Summen je Projekt/Ticket/Kunde, Auslastung.
5. **Abwesenheiten** – Urlaub/Krankheit des Teams als Kalender.
6. **Mein Team** – Mitglieder (nur ansehen; Pflege durch Admin, siehe Entscheidung 3).

### 3.3 Personal (`/personal`)
1. **Übersicht** – alle Monate: wer fehlt beim Einreichen/Freigeben, Fristen.
2. **Freigaben** – alle Personen und Teams; Freigeben, Ablehnen, **Wiedereröffnen mit Begründung**.
3. **Personen** – Zeiten, Salden, Korrekturen mit Begründung, Abwesenheiten, Arbeitszeitmodelle (Soll).
4. **Auswertungen** – Team, Kunde/Account, Projekt, Ticket, Vollständigkeit.
5. **Exporte** – Monatsexport Lohn, Kundenexport; Verlauf „exportiert am/von“.
6. **Protokoll** – Freigabe- und Änderungshistorie (Audit-Ansicht, nur fachlich).
7. **Regeln** – Fristen, Feiertage/Kalender, Abwesenheitsarten, Arbeitszeitmodelle (fachliche Einstellungen; technische bleiben Admin).

### 3.4 Administration (`/admin`, keine Zeiten)
Heute: Status, Selbsttest, Logs, Benutzer, Teams, Einstellungen. Vorschlag:
1. **Status** – Zustand, Version, Module.
2. **Selbsttest** – Prüfungen, sichere Reparatur.
3. **Benutzer** – Konten, Rollenmerkmale, Sperren, Sitzungen beenden, Notfallzugang.
4. **Teams** – Teams, Teamleiter, Mitglieder (oder Gruppenzuordnung).
5. **Anmeldung** – LDAP/AD, OIDC (Web-Maske, offen), Verknüpfung.
6. **Verbindungen** – Jira-Instanzen, Plane-Workspaces, Modul-Schalter (Jira/Tempo, Vorschläge, Erkennungsarten, Kalender).
7. **Logs & Audit** – technische Logs, Prüfung der Hash-Kette.
8. **Betrieb** – Sicherung, Update, Datenkonsistenz.
Die Freigabe-Regeln (Fristen, Wer gibt frei?) stehen heute in Admin-Einstellungen; fachlich gehören sie zu Personal („Regeln“), technisch bleibt der Schalter beim Admin – siehe Entscheidung 2.

### 3.5 Was wandert wohin (bestehende Oberfläche)

| Heute | Künftig |
|---|---|
| Einstellungen › Verbindungen › Plane, Jira & Tempo (persönlicher Token, Zuordnung, Tempo-Push-Einrichtung) | Mitarbeiter › Einstellungen › Verbindungen (nur eigene Token/Zuordnung); Instanzen/Adressen: Admin › Verbindungen |
| Einstellungen › Arbeitszeit | Mitarbeiter › Einstellungen (persönliche Arbeitszeit); Soll-Modell/Regeln mit Wirkung auf Konto: Personal › Regeln |
| Einstellungen › Erkennung (Anwesenheit, Add-on, Browser, Meetings) | Mitarbeiter › Einstellungen › Erkennung (persönlich, nur mit Client sichtbar); Freigabe der Arten für alle/Person: Admin |
| Einstellungen › Wartung › Server & Sicherung, Update, Datenkonsistenz, System-Log | Admin › Betrieb / Logs |
| Einstellungen › Wartung › Änderungsprotokoll | Mitarbeiter: eigene Änderungen (Hinweis „Mein Protokoll“); Personal › Protokoll: alle fachlichen; Admin › Logs & Audit: Hash-Kette |
| Einstellungen › Wartung › Allgemein (Assistent-Schalter) | Mitarbeiter › Einstellungen (Schalter); globaler Schalter: Admin |
| Stundenzettel | Mitarbeiter › Monat (Einreichen); Team/Personal sehen dieselbe Ansicht für andere |
| Projekte | Auswertung (Reiter) und Admin › Verbindungen (Verknüpfung Quelle↔Projekt) |
| Abwesenheiten, To-dos | unverändert Mitarbeiter; Teamübersicht in Team/Personal |
| SSH-Sitzungen | Mitarbeiter, nur mit Client |

### 3.6 Abläufe

**Zeit buchen (Mitarbeiter)**
1. Heute öffnen, Timer starten oder Zeitraum nachtragen (Beginn, Ende, Pause, Kommentar; Ticket optional).
2. Server prüft Plausibilität (Überschneidung, Dauer) und zeigt Hinweise.
3. Ohne Ticket: Eintrag bleibt „needs“; Vorschlag annehmen (falls aktiv), manuell zuordnen oder neues Ticket auf Bestätigung.
4. „Übertragen“: Plane bekommt eine Summe je Ticket (`[zp-sum]`), Jira/Tempo einzelne Worklogs.
5. Fehler/fehlendes Token erscheinen in Übertragen, nichts wird still gebucht.

**Monat einreichen**
1. Nach Monatsende zeigt Monat „bereit“ mit Prüfhinweisen (fehlende Stunden, offene „needs“, ungebuchte Vorschläge).
2. Person prüft, ordnet zu, reicht ein (optional mit Kommentar).
3. Server sperrt die Einträge des Monats (Datenbank-Sperre), Status „eingereicht“, Teamleitung wird benachrichtigt (Mittel offen).
4. Nachträge sind erst nach Wiedereröffnen möglich.

**Team-Freigabe**
1. Teamleiter öffnet „Freigaben“, sieht eingereichte Monate (überfällige markiert).
2. Prüft Summen und Einzelbuchungen; „Freigeben“ oder „Ablehnen“ mit Pflicht-Kommentar.
3. Abgelehnt: Monat wird für die Person wieder bearbeitbar, Hinweis mit Kommentar; erneut einreichen.
4. Freigegeben: Status „vom Team freigegeben“; ob danach Personal-Stufe folgt, hängt von Entscheidung 1 ab.
5. Keine Selbstfreigabe: eigener Monat geht an Personal (offen, Lücke 3).

**Personal-Freigabe / Wiedereröffnen**
1. Personal sieht alle Monate und deren Stand; Freigabe der verbleibenden (z. B. Teamleiter-Monate, überfällig).
2. Wiedereröffnen eines freigegebenen Monats: Personal gibt Begründung ein, Server protokolliert (Audit), Sperre fällt, Monatsticket in Jira wird wieder geöffnet.
3. Person/Teamleiter korrigiert, reicht erneut ein, Freigabe läuft erneut; Schließen des Monatstickets erst danach.
4. Export-Markierung eines bereits exportierten Monats: Warnung, bei Neuexport Hinweis „Korrektur“ (Lücke 10).

**Monatsexport / Abrechnung**
1. Voraussetzung: alle Monate des Zeitraums freigegeben; Personal sieht Vollständigkeit (offen/eingereicht/freigegeben).
2. Personal wählt Export (Lohn je Person oder Kunde/Ticket) und Zeitraum.
3. Server erzeugt Datei (CSV/XLSX – Format offen) nur aus freigegebenen Monaten und markiert die Einträge als exportiert (wie Kimai) mit Verlauf.
4. Nachträgliche Änderung nur nach Wiedereröffnen; Folgeexport weist darauf hin.

## 4. Entscheidung für den Nutzer nötig

1. **Ein- oder zweistufig?** Freigabe nur durch Teamleiter (wie Tempo), nur Personal, oder zweistufig (Team, dann Personal) – bestimmt Menü „Freigaben“ und Fristen.
2. **Wo stehen Freigabe-Regeln (Fristen, Wer gibt frei?)?** Im Admin (technisch, wie heute) oder bei Personal („Regeln“), weil es Fachentscheidungen sind.
3. **Teams pflegen:** in `/admin` von Hand, aus Verzeichnisgruppen oder aus Tempo übernehmen – bestimmt, wer Teamleiter und Mitglieder ändert.
4. **Wer gibt den Monat von Teamleitern und Personal-Mitgliedern frei?** Personal gegenseitig, Geschäftsleitung, oder eigene Gruppe (Selbstfreigabe verbieten wie Tempo).
5. **Vertretung:** Genügt „Personal kann jede Freigabe übernehmen“, oder soll der Teamleiter eine Vertretung eintragen können?
6. **Erinnerungen:** nur In-App-Hinweise oder E-Mail (Technik offen: SMTP, Exchange-OAuth, Graph) – ohne E-Mail ist die Frist-Überwachung schwächer.
7. **Exportformate und Abrechnungsziel:** welche Lohn-/Kundenformate (CSV/XLSX; Programm der Buchhaltung offen) und ob „exportiert“-Markierung sperrt.
8. **Abwesenheiten mit Antrag/Genehmigung (Urlaubsverwaltung)** im Zeitprotokoll oder weiter außerhalb; heute nur Erfassung.

## Offen / nicht belegt
- Tempo Cloud: Aufbau der Navigation, Report-Liste (Seite `…/reports` lieferte 404), Teamleiter-Sicht im Detail; Approval-Log nur über Suchzusammenfassung.
- Toggl: Ablauf Genehmigung, Erinnerungen, Export-Formate (Hilfeseite nicht erreichbar).
- Harvest: Originalseiten teils nicht abrufbar (403); Rollen nur aus Suchtreffern.
- Clockify: Berichte/Exporte, Entsperr-Rolle, Rollenrechte Owner/Benutzer.
- Jira-nativ: Berichte; Plane: Freigabe (Seite erwähnt keine – daraus folgt nicht, dass es keine gibt, aber Hinweis fehlt), Worklog-Listen-API.
- Aussagen zu Betriebsrat, Aufbewahrung, Datenschutz sind nicht geprüft.
