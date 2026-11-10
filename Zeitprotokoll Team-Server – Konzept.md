Zeitprotokoll Team-Server – Konzept
22. Okt. 2026 · @Autor
Ziel und Ausgangslage
Das Zeitprotokoll soll vom Einzel-Tracker auf einem Mac zu einem Team-System für Beispiel werden, das Tempo bei Jira ersetzt und an Plane angebunden ist. Jede Person erfasst weiter lokal; ein zentraler Server führt die Daten zusammen, steuert die Freigabe und hält Plane aktuell.
Heute vorhanden (Version 5.6, Einzelplatz):
• Desktop-App für macOS mit Lasche und Schnellfenster, großes Fenster im Browser, lokale SQLite-Datenbank
• Timer, Nachtragen, Wochenraster, Tätigkeitsarten, Pausen, Abwesenheiten, Arbeitszeitkonto, Monatsabschluss
• Buchen auf Plane-Work-Items, Work-Items anlegen, eine Summe pro Ticket und Person nach Plane, Import von direkt in Plane gebuchten Zeiten
• Auswertung nach Ticket, Projekt, Tätigkeit und Tag; Erinnerung an fehlende Stunden; Schätzung gegen Ist
Was das Team-System zusätzlich leisten soll:
• Mehrere Personen, Teams und Rollen
• Monatlicher Freigabe-Workflow mit Sperre
• Team-Auswertungen, Auslastung, Kunden-Abrechnung
• Plane-Anbindung zentral statt pro Mac, später als Plane-App
• Desktop-App auch für Windows
Leitlinie: lokal zuerst. Die App arbeitet auch offline weiter und gleicht ab, sobald der Server erreichbar ist. Plane wird nie direkt von den Rechnern beschrieben, sondern nur vom Server.
Architektur
Jede Desktop-App erfasst lokal und gleicht mit einem zentralen Server ab; nur der Server schreibt nach Plane.
Die Mac- und Windows-App bleiben offline nutzbar. Teamleitung und Buchhaltung arbeiten im Browser direkt gegen den Server. Plane bekommt wie heute eine Summe pro Work-Item und Person, jetzt aber zentral über ein Dienst-Konto statt über einen API-Schlüssel auf jedem Rechner.
Aufgabenteilung Server ↔ Agent. Der Server ist die Stelle für Buchungen, Projekte und Rechte; der Agent auf jedem Rechner ist mehr als ein Anzeiger – er erkennt, schlägt vor und arbeitet auch ohne Netz.
Aufgabe
Server
Agent (lokal)
Buchungen
führende Ablage, Änderungsprotokoll, Monatssperre
erfassen (Timer, Nachtragen), puffern ohne Netz, dann abgleichen
Projektsysteme
Verbindung zu Plane, Jira, Tempo; Tickets, Übergangs-Tickets, Übertragung
Ticket suchen und anlegen über den Server
Erkennung
–
Sensoren: Terminal/SSH, Meetings, Kalender, Anwesenheit; Rohdaten bleiben lokal
Vorschläge
lernt Zuordnungen aus allen Buchungen der Person
zeigt Vorschläge, Lücken, Doppelungen; erinnert, fragt nach
Verwaltung
Personen, Rollen und Rechte (OIDC/LDAP), Projekte und Quellen, Freigaben
eigene Einstellungen, Sensoren an/aus
Ansicht
Browser-Oberfläche für alles, Auswertung, Teamsicht
Schnellfenster, Tray, Tagesleiste
Grundsatz: Was den Rechner über die Person verrät (welche Server, wann am Rechner, welche Meetings), bleibt im Agent; zum Server geht nur, was gebucht wurde.
Rollen und Rechte
Vier Rollen reichen für Beispiel; jede Person sieht standardmäßig nur die eigenen Zeiten, Teamleitungen nur ihr Team. Anmeldung per OIDC über authentik (an FreeIPA angebunden); Keycloak oder ein anderer OIDC-Anbieter lässt sich alternativ eintragen. Zusätzlich direkt per LDAP an FreeIPA oder Windows Active Directory (LDAPS, Gruppen über memberOf) – für Kunden ohne OIDC. Rollen kommen in beiden Fällen aus Gruppen.
Rolle
Sieht
Darf
Gruppe (Vorschlag)
Mitarbeiter
eigene Buchungen, Saldo, Urlaub
buchen, Monat einreichen, Plane-Summen auslösen
zp-users
Teamleitung
Buchungen und Salden des eigenen Teams
Monate freigeben oder ablehnen, Team-Auswertung
zp-leads
Buchhaltung
freigegebene Monate aller Personen, Kunden-Auswertung
Exporte für Abrechnung und Lohn, keine Änderungen
zp-finance
Admin
alles
Personen, Teams, Arbeitszeitmodelle, Plane-Anbindung, Sperren aufheben
zp-admins
Offen: ob Teamleitungen Einzelbuchungen (Kommentare, Uhrzeiten) sehen dürfen oder nur Tagessummen – das berührt den Betriebsrat (siehe Datenschutz).
Freigabe-Workflow
Jede Person reicht ihren Monat ein, die Teamleitung gibt frei oder lehnt mit Kommentar ab; erst freigegebene Monate gehen in Lohn- und Kunden-Exporte.
• Einreichen: in der App unter Stundenzettel, sobald der Monat vollständig ist; die Erinnerung an fehlende Stunden warnt vorher.
• Prüfen: die Teamleitung sieht Soll/Ist, Abwesenheiten und Buchungen je Ticket; Ablehnen braucht einen Kommentar.
• Sperren: nach Freigabe sind Buchungen des Monats schreibgeschützt; Plane-Summen laufen unverändert weiter.
• Fristen (Vorschlag): Einreichen bis zum 3. Werktag, Freigabe bis zum 5. Werktag des Folgemonats.
Datenmodell und Synchronisation
Der Server ist die Wahrheit für eingereichte und freigegebene Zeiten; die lokale App ist die Wahrheit für alles, was noch offen ist. Jede Buchung bekommt eine weltweit eindeutige ID (UUID), damit App und Server ohne Konflikte abgleichen.
Tabelle (Server)
Inhalt
Neu gegenüber heute
users, teams, team_members
Personen aus authentik (OIDC) oder LDAP (FreeIPA, AD), Team-Zuordnung, Rolle
neu
work_models
Soll-Stunden je Wochentag, gültig ab Datum
heute in den Einstellungen pro Mac
entries
Buchung: Person, Work-Item, Projekt, Tätigkeit, Start, Ende, Dauer, Kommentar, Version
heute time_entries, dazu user_id, uuid, version, updated_at
absences, holidays
Urlaub, Krankheit, Feiertage je Bundesland
heute lokal
periods
Monat je Person: offen, eingereicht, freigegeben, abgelehnt + Kommentar
neu
plane_ticket_sums
Summe je Work-Item und Person, Plane-Worklog-ID
heute plane_ticket_sync
audit_log
wer hat was wann geändert
neu
Abgleich App ↔ Server:
1. Die App schickt geänderte Buchungen seit dem letzten Abgleich (updated_at), der Server antwortet mit seinen Änderungen.
2. Gleiche Buchung auf beiden Seiten geändert: die höhere version gewinnt, die andere landet als Konflikt zur Klärung in der App.
3. Buchungen in einem eingereichten oder freigegebenen Monat nimmt der Server nur nach Wiedereröffnung an.
4. Ohne Netz läuft alles lokal weiter; der Abgleich holt nach.
Abgleich Server ↔ Plane: wie heute eine Summe pro Work-Item und Person, nur läuft es zentral mit einem Dienst-Konto bzw. später als Plane-App (OAuth, Webhooks). Damit entfällt der API-Schlüssel auf jedem Mac.
Datenschutz, Sicherheit, ISO 27001
Zeiterfassungsdaten sind Beschäftigtendaten; ein zentrales System braucht deshalb vor dem Start eine Betriebsvereinbarung bzw. Mitarbeiterinformation und einen Eintrag im Verarbeitungsverzeichnis.
Datenschutz
• Zweck festlegen: Arbeitszeitnachweis, Projekt-/Kundenabrechnung, Plane-Zeitsummen – keine Leistungskontrolle
• Datensparsam: Plane bekommt nur Summen pro Work-Item, keine Uhrzeiten oder Kommentare
• SSH-Sitzungs-Erfassung bleibt lokal und freiwillig; sie geht nicht an den Server
• Löschfristen: Buchungen nach Aufbewahrungspflicht (z. B. 2 Jahre Arbeitszeit, länger für abgerechnete Kundenzeiten), danach anonymisieren
• Jede Person kann ihre Daten exportieren (Auskunft nach Art. 15 DSGVO)
Sicherheit
• Anmeldung nur über authentik/OIDC (Keycloak als Alternative) oder LDAPS gegen FreeIPA/AD, kein eigenes Passwort im Zeitprotokoll
• TLS überall, Server nur im internen Netz bzw. per VPN
• Rechte serverseitig prüfen; die App bekommt nur, was die Rolle sehen darf
• Plane-Zugang über ein Dienst-Konto auf dem Server statt API-Schlüssel auf jedem Mac
• Audit-Log für Änderungen an fremden oder freigegebenen Buchungen
Bezug zu ISO 27001 (Annex A, 2022)
Control
Umsetzung
A.5.15 Zugangssteuerung
Rollen aus authentik- bzw. LDAP-Gruppen
A.8.15 Protokollierung
Audit-Log, Server-Logs an Wazuh
A.8.13 Datensicherung
tägliche Datenbank-Sicherung auf PBS
A.5.34 Schutz personenbezogener Daten
Zweckbindung, Löschfristen, Betriebsvereinbarung
A.8.5 Sichere Authentisierung
Agent meldet sich per OIDC an; je Gerät ein eigenes, widerrufbares Zertifikat/Token (mTLS), kurzlebige Zugriffstokens
A.8.24 Kryptographie
TLS 1.3 mit Zertifikatsprüfung/Pinning; lokale Datenbank verschlüsselt, Schlüssel und Tokens im Schlüsselbund des Betriebssystems
A.8.1 Endgeräte
Agent signiert und notarisiert, nur signierte Updates; lokale Schnittstelle nur 127.0.0.1 mit Token und Host-/Origin-Prüfung (kein Zugriff aus Webseiten)
A.8.12 Verhinderung von Datenabfluss
Sensor-Rohdaten bleiben lokal, Aufbewahrung begrenzt (120 Tage); zum Server nur Buchungen
A.8.32 Änderungssteuerung
Versionen über Git mit Review, Freigabe vor Rollout, Rückweg über Sicherung
Lücken in der heutigen Mac-App (vor dem Rollout schließen): Die lokale Schnittstelle auf 127.0.0.1:3747 prüft weder Token noch Host/Origin – andere Programme auf dem Rechner und im Grenzfall Webseiten (DNS-Rebinding) könnten Daten lesen. Jira- und Plane-Token liegen unverschlüsselt in der lokalen Datenbank statt im Schlüsselbund.
Betrieb und Rollout
Der Server läuft als eine VM im eigenen Rechenzentrum. Aufgebaut und aktualisiert wird er nicht über Semaphore/Ansible, sondern mit einem eigenen Installationsskript mit Auswahlfenstern (Textoberfläche im Terminal): Es fragt Schritt für Schritt Adresse, Zertifikat, Anmeldung (OIDC), Datenbank, Jira/Tempo- und Plane-Anbindung und Sicherung ab, prüft jede Angabe sofort und kann jederzeit erneut gestartet werden, um Einstellungen zu ändern oder ein Update einzuspielen.
Baustein
Vorschlag
Server
Debian-VM, Node.js 22 LTS, PostgreSQL 16 (statt SQLite, wegen mehrerer Nutzer)
Erreichbarkeit
zeit.example.com hinter dem vorhandenen Reverse-Proxy, nur intern/VPN
Anmeldung
authentik-Anwendung (OIDC-Provider) oder LDAP-Dienstkonto (nur lesend) zeitprotokoll, Gruppen wie in „Rollen“
Bereitstellung
Installationsskript zp-setup mit Auswahlfenstern: Erstinstallation, Einstellungen ändern, Update, Sicherung/Wiederherstellung, Selbsttest – ohne Semaphore/Ansible
Sicherung
nächtlicher pg_dump + VM-Backup auf PBS
Überwachung
Icinga2-Check auf /api/health, Logs an Wazuh
Desktop-App Mac
signiertes Paket, Verteilung über Nextcloud-Link oder MDM, Auto-Update vom Server
Desktop-App Windows
Electron-Build mit Installer, Autostart per Aufgabenplanung, Symbol im Infobereich
Windows-Client – was angepasst wird:
• Autostart: Aufgabenplanung statt launchctl
• Lasche und Schnellfenster: gleiche Oberfläche, Infobereich statt Menüleiste
• SSH-Erfassung: PowerShell-Profil statt zsh
• Installation: ein .exe-Installer statt Shell-Skripte
Updates: Server über zp-setup → „Update“ (lädt die neue Version, legt vorher eine Sicherung an, prüft danach selbst und kann zurückrollen); die Desktop-App lädt ihre Oberfläche vom Server bzw. prüft beim Start auf eine neue Version, damit niemand mehr Pakete von Hand einspielt.
Festgehaltene Entscheidungen
Stand 28.10.2026: Der Team-Server hält alle Zeitdaten und alle Anbindungen; die Clients sind schlanke Oberflächen.
Thema
Entscheidung
Backend
Go – eine Programmdatei, kleine Container für OpenShift, Red Hat, Debian
Datenhaltung
Alle Zeitdaten zentral auf dem Server; der Client hält lokal nur die aktuelle Woche vor
Anbindungen
Plane, Jira/Tempo und Kalender (Exchange/CalDAV) laufen über den Server, nicht über den Client
Schlüssel
Jeder hinterlegt in seiner Ansicht eigene Tokens; der Server speichert sie verschlüsselt je Person
Oberfläche
Browser-first; Mac- und Windows-App nur als Zusatz (Tray, Erkennung)
Erweiterbarkeit
Offene REST-API mit Token und Plugins; Module wie Plane, Jira, Meetings einzeln abschaltbar
Jira/Tempo
Nur Übergang, bis alles über Plane läuft; Zeitprotokoll ersetzt Tempo für die Abrechnung
Aviation
Als möglicher Andockpunkt notiert (Plane-SSO, Worklogs mit Vergangenheitsdatum), noch nicht angebunden
Projekte
Projekte gibt es nur, wenn sie in Plane oder Jira existieren; lokale Projekte nur optional. Verknüpft wird an genau einer Stelle (Projekte › Quellen), Namen wie in Plane.
Plane-Zeiten in Tempo
Ein Übergangs-Ticket je Person und Monat („Übergang Zeiten user01 2026-10“), beim ersten Buchen angelegt, nach der Monatssperre geschlossen; Kunde über den Tempo-Account des Worklogs.
Altdaten
Nach Tempo zählt nur ab 01.10.2026; die Zeit davor kommt später per sauberem Grundimport aus Tempo.
Vorschläge
Termine, Meetings und SSH-Sitzungen sind keine Buchungen; erst „Buchen“ macht Zeit daraus.
Anwesenheit
Optional: je Minute nur „jemand am Mac“ (kein Inhalt), für Arbeitstag von–bis und Lücken; 120 Tage aufbewahrt.
Bedienung
Einfache Ansicht als Standard, „Mehr Ansichten“ optional; abschaltbarer Einrichtungs-Assistent; Löschen immer mit Namen in der Rückfrage.
Idee, noch nicht bauen: ein eingebauter Assistent, der beim Einrichten und sauberen Buchen hilft – Projekte und Tickets zuordnen, fehlende Zeiten vorschlagen, Unstimmigkeiten erklären, Einrichtung Schritt für Schritt.
Lernen der Vorschläge
Anlass: Der Assistent schlug für den Server app-host-03 das Ticket „Datenschutz“ vor, obwohl dort viele Tickets bearbeitet werden. Ursache: Ein einziger früherer SSH-Eintrag gab +6 Punkte, das stärkste Signal überhaupt, ohne Häufigkeit und ohne Lernen aus Ablehnung.
Regeln (umgesetzt in Version 6.37.0, gelten unverändert im Team-Server, weiter nur je Person):
• Häufigkeit statt Einzelfall: Für Termin-Titel und SSH-Server zählt der Anteil je Ticket an den letzten 20 Buchungen (höchstens 180 Tage). Ein Einzelfall bringt höchstens ein Drittel der Punkte; die Begründung nennt die Zahl („3 von 5×“).
• Ablehnung zählt: Wird ein anderes Ticket als vorgeschlagen gebucht, wird die abgelehnte Zuordnung abgewertet (eine Ablehnung zieht zwei Wahlen ab). Erneutes Wählen vergibt schrittweise.
• SSH-Historie aus der Zeit (Version 6.38.2): Zu einem Server zählen auch Buchungen, die zeitgleich zu früheren Sitzungen liefen (mindestens 5 Minuten Überlappung). Grund: Nur 2 von 228 Sitzungen waren mit einer Buchung verknüpft, die Historie war praktisch leer.
• Zerfall: Das Gewicht einer gelernten Wahl halbiert sich alle 60 Tage; wirksam erst ab 1,5 frischen Wahlen.
• „Alle übernehmen“ lernt nichts, damit ein falscher Vorschlag sich nicht selbst bestätigt.
• Gelernt wird nur lokal und nur für die eigene Person; nichts wird über Personen hinweg ausgewertet.
Offen: Ansicht „Gelernte Zuordnungen“ mit Löschen je Eintrag; Regel „Server X nie für Ticket Y vorschlagen“ per Klick; Uhrzeit und Nachbartermine als zusätzliches Signal.
Umgesetzt als Prototyp in Version 6.38.0 (Schalter Einstellungen › Erkennung, standardmäßig aus): lokale Apple-KI (Foundation-Models-Framework, macOS 26, Apple Silicon) als zweites Signal. Sie ändert die Punkte um höchstens ±3, die Statistik bleibt die Grundlage. Sie bekommt als Beleg mit, was früher für diesen Termin oder Server gebucht wurde (Version 6.38.1); ohne Historie gibt sie 5. Probelauf: Für SSH auf app-host-03 bewertet sie „Datenschutz-Konzept“ mit 2 und „Server-Wartung“ mit 7 von 10. Im Alltag (30.10.2026): Für app-host-01 steht nach der Änderung PRJA-708 mit „früher so gebucht: 2 von 4×“ und KI 10/10 oben. Sie vergleicht Termin-Titel, SSH-Befehle und Ticket-Titel inhaltlich, statt nur zu zählen; das Lernen selbst bleibt in der eigenen Logik. Sie sieht nicht, was am Mac passiert, sondern nur, was die Sensoren melden. Läuft offline, Daten bleiben auf dem Mac. Nicht für Windows verfügbar, dort gilt die Statistik allein. Bildschirminhalte werden nicht ausgewertet (Betriebsrat). Offen: Wirkung im Alltag beobachten, Nutzung bei „Jetzt vermutlich“, Satz-Embeddings (NaturalLanguage) als schlankere Alternative.
Idee, noch nicht entschieden: Sensoren als eigene kleine Programme – lokal auf dem Rechner. SSH-Erkennung, Meeting-Erkennung, Kalender und Anwesenheit werden aus der Mac-App gelöst und zu eigenständigen „Sensoren“, die auf dem eigenen Rechner laufen und nur an das lokale Zeitprotokoll melden – nicht an den Server. Sie liefern Vorschläge („SSH auf app-host-04 08:00–10:20“, „Meeting in Teams 09:30–10:15“), gebucht wird nie automatisch. Rohdaten der Sensoren bleiben lokal; zum Server geht erst die fertige Buchung.
Sensor
Läuft lokal als
Meldet an das lokale System
Terminal
Shell-Plugin für zsh/bash (Mac, Linux), PowerShell (Windows)
SSH-Sitzung: Server, von–bis, aktive Minuten
Meeting
kleiner Hintergrunddienst je Betriebssystem
Meeting-Programm/Browser-Meeting läuft, von–bis (kein Mikrofon)
Kalender
Leser für den Kalender des Rechners (macOS-Kalender, Outlook)
Termine
Anwesenheit
Hintergrunddienst
Minuten am Rechner (kein Inhalt)
Vorteile: jeder Sensor einzeln installier- und abschaltbar, auch unter Windows und Linux; sensible Rohdaten (welche Server, wann am Rechner) verlassen den Rechner nicht; andere können eigene Sensoren schreiben (gleiche lokale Schnittstelle wie heute die Mac-App, 127.0.0.1). Offen: wie viel davon in die erste Version gehört.
Idee, später: Zeitprotokoll als Werkzeug für Claude (MCP-Schnittstelle) – „starte den Timer auf PRJA-581“, „was fehlt mir diese Woche?“, „plane morgen“, für Jira- und Plane-Tickets gleichermaßen. Läuft über das lokale Programm bzw. den Server mit denselben Rechten wie das Add-on (eigene Kopplung, nur freigegebene Aktionen, alles im Protokoll).
Idee, später: Handy. Mit dem Team-Server wird die Oberfläche als installierbare Web-App (PWA) fürs Handy angeboten – Timer starten/stoppen, To-dos, Woche, Freigabe; Anmeldung über OIDC, Erinnerungen per Push. Kein Mac nötig; Erkennung (SSH, Meetings, Browser) bleibt Sache des Agenten auf dem Rechner. Eine eigene App aus dem Store nur, falls die Web-App nicht reicht.
Idee, später: nur im Browser, mit Client-Stufen. Das System soll vollständig ohne installierten Client nutzbar sein – nur im Browser. Was dort nicht geht, wird nicht versteckt, sondern mit einem Hinweis markiert: „braucht den Minimal-Client“ oder „braucht den Voll-Client“, mit Installation direkt aus dem Browser.
• Nur Browser: buchen, Woche, Kalender, To-dos, Auswertung, Freigabe, Tempo/Plane über den Server. Timer läuft serverseitig weiter, auch wenn der Tab zu ist.
• Browser + Add-on (Firefox, Chrome, Edge, Brave, Arc): offenes Jira-/Plane-Ticket mit einem Klick tracken, Timer im Add-on sichtbar. Safari ohne Add-on.
• Minimal-Client (Windows, Mac, Linux): kleiner Hintergrunddienst ohne eigenes Fenster – Anwesenheit am Rechner (von–bis), SSH-Sitzungen, Leerlauf, Offline-Puffer; spricht nur mit dem eigenen Server, gekoppelt wie das Add-on.
• Voll-Client (Mac-App, später Windows/Linux): zusätzlich Tracker-Fenster, Termin- und Meeting-Erkennung, Safari-Ticket-Erkennung, Tray.
Grundsätze: jede Funktion kennt ihre Mindeststufe; die Oberfläche fragt den Server, welche Clients der Person gerade verbunden sind, und zeigt nur dann den Hinweis. Daten fließen nur an den eigenen Server, Kopplung mit Erlaubnis und widerrufbar (ISO 27001). Das wird sauber durchdacht, wenn der Server steht.
Geplant: Installer für einen neuen Mac. Eine Datei „Zeitprotokoll installieren“, ohne Admin-Rechte: lädt Node in den Programmordner, legt die Mac-App an, richtet Autostart, SSH-Erfassung und Add-on ein. Umzug alter Zeiten über Sicherung exportieren → auf dem neuen Mac importieren, oder Tempo-Abgleich ab Eintrittsdatum. Zugangsschlüssel (Jira, Plane) künftig im Schlüsselbund statt in der Datenbank.
Leitlinie: für Menschen mit ADHS gebaut (seit 29.10.2026). Wenig auf einmal, ein klarer nächster Schritt, kein Schuld-Ton, so wenig Tippen wie möglich, Zeit spürbar machen, Erfolge sichtbar machen. Umgesetzt in 6.31 (siehe Versionen). Noch offen, nach Wirkung sortiert:
• Zeit spürbar machen: Fortschrittsring im Tracker; sanfter Check-in nach 90/150 min („weiter, Pause oder wechseln?“); nach Rückkehr aus Leerlauf „Du warst 25 min weg – abziehen?“.
• Ein nächster Schritt: Heute-Ansicht und Tracker zeigen genau eine Sache („Als Nächstes: … ▶“), alles andere hinter einem Zähler.
• Unterbrechung mit einem Klick: „Unterbrechung“ startet Nebenzeit und kehrt danach automatisch zum vorherigen Ticket zurück; „Zurück zu …“.
• Timebox: To-do mit Dauer startet einen Timer mit Ziel („noch 12 min“).
• Erfolge: Tagesring, „4 Tage in Folge getrackt“, ruhiger Wochenabschluss.
• Erinnerungen: Später mit 10/30/60 min; Abstand passt sich an; Hinweis beim Ticket-Wechsel im Browser.
• Einfacher Modus: 4 Menüpunkte (Heute, To-dos, Woche, Nachtragen), Rest unter „Mehr“; Einrichtung in 3 Schritten, Rest später.
Projekte, Tickets und Systeme sauber trennen
Ein Projekt in Zeitprotokoll ist das echte Projekt (z. B. „Beispiel Marketing“); Plane- und Jira-Projekte sind nur Quellen für Tickets, die daran hängen. Wer bucht, sieht eine Ansicht – woher ein Ticket kommt und wohin die Zeit geht, regelt das System im Hintergrund.
Jedes Plane- oder Jira-Projekt ist genau einem Zeitprotokoll-Projekt zugeordnet; ein Projekt kann mehrere Quellen haben. Ein Ticket gibt es nur einmal, auch wenn es in Plane und Jira steht – dann trägt es beide Verweise. Die Regel am Projekt entscheidet, wohin die Zeit geht.
Betriebsarten – dasselbe System, nur andere Regel:
Betriebsart
Tickets kommen aus
Zeit geht nach
Für wen
Nur Plane
Plane
Plane (Summe je Ticket)
Teams ohne Jira
Tempo-Ersatz
Jira
bleibt im System, Abrechnung hier
Jira-Teams, die Tempo ablösen
Jira mit Tempo
Jira
Tempo (Worklog je Buchung)
wer Tempo weiter braucht
Brücke (Beispiel heute)
Plane, Jira
Plane und Tempo
Übergang, bis Jira wegfällt
Was automatisch im Hintergrund läuft
• Quellen einlesen: Plane- und Jira-Projekte einmal im Monat; neue landen auf der Liste „Neue Projekte gefunden“, Projektleitung/Admin übernimmt sie mit einem Klick (gleiches Kürzel in Jira und Plane wird als ein Projekt vorgeschlagen).
• Tickets abgleichen: eigene Tickets alle 10 Minuten; Plane- und Jira-Gegenstücke werden verknüpft (Zuordnung, Titelähnlichkeit, später Aviation).
• Ausgänge: nach der Regel des Projekts automatisch oder nach Freigabe.
• Eingang „Zu klären“: nur Fälle mit Entscheidung – Quelle ohne Projekt, Buchung ohne Ticket, Monat gesperrt, mögliche Doppelbuchung.
Was sich am heutigen System ändert: Die beiden Bildschirme „Projekte verknüpfen“ (Plane, Jira) werden einer unter Projekte; Ausgänge werden je Projekt statt global eingestellt; eine Ticket-Ansicht über alle Quellen ersetzt die getrennten Reiter.
Konnektoren statt Sonderwege: Jedes Ticket-System hängt über einen Konnektor mit denselben Fähigkeiten an (Projekte lesen, Tickets suchen und anlegen, kommentieren, Status ändern, Zeit übergeben). Plane ist das Hauptsystem; Jira Data Center und Jira Cloud (mit Tempo Cloud) kommen als weitere Konnektoren, damit Kunden auf Jira Cloud das System als besseres Tempo nutzen können. Weitere Systeme folgen nach demselben Muster.
Umgesetzt in Version 6.5.0 (Mac): Liste „Neue Projekte gefunden“ mit monatlichem Einlesen aus Plane und Jira, Regel je Projekt (Plane + Tempo, nur Plane, nur Tempo, nur hier), Nachfolge-Projekt beim Abschluss.
Geklärt am 27.10.2026
Frage
Festlegung
Woher kommen Projekte?
Aus Plane und Jira. Zeitprotokoll legt dort keine Projekte an; es übernimmt sie nach Bestätigung. Eigene Projekte nur, wenn ohne Plane/Jira gearbeitet wird – später verknüpfbar.
Kunde
1 Kunde = 1 Plane-Projekt; dazu interne Dauerprojekte (Beispiel Intern). Zeitprotokoll braucht keine eigene Kundenliste, solange Plane/Jira da sind.
Jira und Plane gleich?
Je nach Projekt – gleiche Kürzel werden vorgeschlagen, sonst Zuordnung von Hand.
Abrechnung
Kundenprojekte: Zeit auf Ticket, die Beschreibung der Buchung ist der Rechnungstext. Stundensätze und Rechnung macht ein Abrechnungsprogramm (noch offen) – Zeitprotokoll liefert eine Export-Schnittstelle. Intern: Auswertung für Teamleitung und GL im erlaubten Rahmen.
Tickets Pflicht?
Ja, gebucht wird immer auf ein Ticket – selbst Abwesenheiten haben Tickets.
Abwesenheiten
Vorerst getrennt (Arbeitszeitkonto). Später: Abwesenheit legt/bucht automatisch das passende Ticket im Projekt.
Projekt geschlossen
Bleibt in Zeitprotokoll mit allen Zeiten und Plus/Minus. Ein Nachfolge-Projekt wird erst nach dem Abschluss festgelegt, sobald es bekannt ist; bis dahin ist das Projekt nicht mehr buchbar.
Wer pflegt Projekte und Regeln
Projektleitung/Admin; alle anderen buchen und wählen Tickets.
Standard-Regel
Jetzt Brücke (Plane und Tempo). Ohne Jira/Tempo: Zeit in Zeitprotokoll und Plane, weiter an das Abrechnungsprogramm.
Stand der Mac-App (v6.36.0)
Die Mac-App steht am 29.10.2026 auf v6.20.0; die Bausteine dienen als Vorlage für den Team-Server. Neue Funktionen werden hier laufend nachgetragen.
Version
Was dazukam
6.36.0
„Arbeitstag starten“ ist optional: Ist = gebuchte Zeit (wie Tempo); das Arbeitstag-Fenster zählt nur, wenn eingeschaltet. Die Anmelde-Abfrage „Guten Morgen – Arbeitstag starten?“ ist standardmäßig aus; der Installer entfernt den alten Autostart. Beides unter Einstellungen › Arbeitszeit › Startwerte
6.35.0
Löschen vor dem Übertragen: Mülleimer bei jeder Buchung in Übertragen und Nachtragen (mit Rückfrage; schon in Tempo → wird dort beim nächsten Übertragen mit entfernt). „doppelt?“-Markierung bei gleicher Aufgabe zur gleichen Zeit. Nachtragen zeigt den Tag jetzt als Liste wie Übertragen: Buchungen (Ticket-Kürzel, Bearbeiten, Löschen) und Vorschläge in zeitlicher Reihenfolge, Vorschlag mit „übernehmen“ oder „anpassen“ (Zeit, Ticket suchen, Text)
6.34.0
Übertragen › Nach Tempo als Tagesliste zum Prüfen und Nachtragen: vorne das eigene Ticket als farbiges Kürzel (Jira blau, Plane lila) mit Titel, darunter der Text, das Tempo-Ziel nur klein („→ Übergangs-Ticket …“); Account-Zeile nur noch bei Abweichung. Lücken zwischen Buchungen erscheinen direkt im Tag („11:45–14:00 · nichts gebucht“) mit dem Vorschlag des Assistenten („übernehmen“) oder „+ eintragen“
6.33.0
Buchung überall bearbeiten: Stift in Übertragen › Nach Tempo und „✎ Bearbeiten“ im Kalender-Seitenfenster öffnen denselben Dialog (Projekt, Ticket, Datum, Zeit, Text, Tätigkeit); danach aktualisieren sich die Listen selbst. Aus Tempo gespiegelte Buchungen bleiben nur lesbar
6.32.0
Langsamkeit behoben: Plane-Anfragen werden vor dem Limit gedrosselt (Hintergrund max. 45/min), die Suche beim Tippen wartet nie mehr 40 s; „Meine Work-Items“ nur einmal gleichzeitig und sofort aus dem Zwischenspeicher. Jede Anfrage der Oberfläche bricht nach 30 s ab (sonst blockieren hängende Anfragen den Browser). Offene Browser-Tabs laden nach einem Update selbst neu. 6.32.1: Messung im Server (langsame Anfragen > 0,7 s und blockierter Server ins Protokoll, Kategorie „leistung“); Plane-Work-Items zuerst lokal erkennen, „nicht gefunden“ 10 min merken, „Meine Work-Items“ überstehen Neustarts. 6.32.2: eigentliche Ursache behoben – das Ticket-Suchfeld (To-do-Schnelleingabe, Nachtragen, Woche) renderte sich endlos neu, legte den Browser lahm und fragte dabei ständig Plane an
6.31.0
ADHS-freundlicher: Prüf-Dialog „Kurz prüfen“ mit anklickbaren Gründen statt Pflicht-Tippen (Begründung landet weiter im Protokoll); Nachtragen zeigt nur die 3 jüngsten Tage, Rest unter „Ältere Tage“, Erfolgsmeldung wenn alles eingetragen ist; To-do „Zeiten nachtragen (N Tage)“ kurz; Rot/Minus nur, wenn das Konto im Minus ist, sonst grau „noch X h offen“; ruhige Erinnerungstexte
6.30.0
Nachtragen: eigenes Ticket suchen (Plane und Jira) statt nur Vorschläge; Vorschläge überschneiden sich nicht mehr (SSH/Termine werden an Buchungen und aneinander zugeschnitten). „Früher gewählt“ zählt nur noch leicht (ab 2× gleicher Wahl, letzte 90 Tage); „typischer Tag“ nur aus Tickets, die an mind. 3 der letzten 6 gleichen Wochentage gebucht waren, Blöcke ab 30 min, ohne erledigte Tickets. Große Ansicht öffnet jetzt den Standard-Browser (umschaltbar: Menüleiste › „Großes Fenster statt Browser“); Add-ons einrichten über Menüleiste › „Browser-Add-ons einrichten …“
6.29.0
Überschneidung beim Speichern: „Zeiten anpassen“ im Prüf-Dialog – beide Buchungen nebeneinander mit Balken, von–bis direkt änderbar, Schnellwahl „Diese Buchung kürzen“ / „Bestehende kürzen“; „Angepasst speichern“ ändert beide zusammen, ohne Begründung. „Trotzdem speichern“ mit Begründung bleibt
6.28.0
Regel: In Jira gibt es keine Buchung ohne Ticket. Aufs Sammel-/Übergangs-Ticket geht nur, was über Plane kommt. Jira-Buchungen ohne Ticket werden nicht übertragen, sondern stehen unter Übertragen › Nach Tempo als „Ticket wählen“ mit Vorschlägen; der Assistent meldet sie als „Buchungen ohne Jira-Ticket“
6.27.0
Assistent hilft beim Buchen: eigene Seite „Nachtragen“ (auch vom Hinweis „In den letzten zwei Wochen fehlen …“, aus To-do und Woche), letzte 14 Tage auch über den Monatswechsel. Lernt aus den eigenen Entscheidungen (je Termin-Titel, SSH-Server und Wochentag/Stunde, nur lokal, nur diese Person). Ohne Mac-Daten schlägt er einen typischen Tag aus den letzten 6 gleichen Wochentagen vor; „Alle übernehmen“ bucht alles mit einem Klick, Termine und SSH-Sitzungen werden dabei als erledigt verknüpft. Beim Tracken: „Jetzt vermutlich“ – laufender Termin, offene SSH-Sitzung, Gewohnheit zu dieser Uhrzeit. 6.27.1: Zeit auf einem bestehenden Jira-Ticket geht immer auf genau dieses Ticket – auch in Projekten, die mit Plane verknüpft sind; aufs Übergangs-/Sammelticket gehen nur Plane-Work-Items und Buchungen ohne Ticket
6.26.0
„Lücken füllen“ ersetzt durch „Nachtragen“: je Tag eine klare Zeile – am Mac von–bis, gebucht, noch einzutragen – dazu „Eintragen“ mit vorbelegtem von–bis und Vorschlägen aus Terminen, SSH-Sitzungen, Zeit am Mac und eigener Historie. Deckt das Plus auf dem Arbeitszeitkonto das Fehlende, ist es nur ein Hinweis (grau statt gelb), kein Zwang. 6.26.1: Kalender-Abo der Abwesenheiten über kopierte http-Adresse und „Ablage › Neues Kalenderabonnement“, Ort „Auf meinem Mac“ (webcal:// versucht der Mac-Kalender per https, das geht lokal nicht)
6.25.0
Urlaub: fürs Konto zählt nur, was im Programm steht (von Hand oder aus dem Kalender übernommen); Buchungen auf dem Tempo-Abwesenheitsprojekt (HAP) zählen nicht als Arbeitszeit und werden nur vorgeschlagen. Neue Wochenansicht: KW mit Fortschritt, Legende Jira/Plane/ohne Ticket, Abwesenheit grau, keine leeren Kästen
6.22–6.24
Abwesenheiten aus Tempo und Kalender erkennen, Urlaubszähler zählt Arbeitstage; vergessene Tage als To-do „Zeiten nachtragen“; Vorschläge per Ziehen in To-do-Spalten; To-dos mit Datum/Zeitblock im Kalender
6.20.x–6.21.x
Tracker schwebt nur über anderen Apps, nicht über dem Hauptfenster; Browser-Erkennung nur für installierte und laufende Browser (kein „Wo befindet sich …“-Dialog); Plane auch in der Halb-/Peek-Ansicht; Datenkonsistenz-Check mit denselben Regeln wie der Assistent
6.20.0
Browser-Add-on (Firefox, Chrome, Edge, Brave, Arc): offenes Jira-/Plane-Ticket mit einem Klick tracken; Kopplung mit Erlaubnis in Zeitprotokoll; lokale Schnittstelle abgesichert (Host-Prüfung, App-Schlüssel, gekoppelte Add-ons mit Rechte-Liste); Mac-App erkennt installierte Browser und bietet das Add-on an
6.19.x
Ticket aus Safari übernehmen (ohne Add-on, abschaltbar); Stabilität: Indizes, kurze Wartezeit statt „Datenbank gesperrt“, Sicherung als Momentaufnahme im laufenden Betrieb, Fehler in Hintergrund-Jobs beenden den Server nicht
6.18.0
To-dos & Planung: Heute/Morgen/Woche/Später/Eingang, Ticket suchen oder in Plane/Jira anlegen, Fälligkeit und Schätzung, Kapazität je Tag, Vorschläge, wiederkehrende Aufgaben, Abgleich mit Plane/Jira/Tempo-Plan (abschaltbar); Saldo zählt keine Zukunft mehr, dazu „noch offen“
6.17.x
Tempo-Abgleich (nur lesen) ab Eintrittsdatum, Grundimport, Feiertage für alle Jahre; Tracker-Fenster nicht mehr dauerhaft im Vordergrund, „Im Browser öffnen“
6.16.0
Wochenansicht wie Tempo: Zeiten als h:mm, jedes Feld sichtbar, gebucht/Soll je Tag mit Balken, Einzelbuchungen je Feld
6.15.0
Monatsende-Hinweis 5 Arbeitstage vorher (Wochenende zählt nicht); Lücken füllen mit Ticket-Vorschlägen aus Terminen, SSH, Zeit am Mac und eigener Historie
6.14.x
Assistent mit Pflege-Aufgaben, die nur erscheinen, wenn es etwas zu tun gibt; Tempo-Konto je Buchung
6.13.0
Plausibilitätsprüfung in drei Stufen; der Timer wird nie blockiert, nur markiert
6.12.0
Heute-Ansicht mit Zeitleiste (Arbeitstag, Buchungen, offene Termine, ungebuchte SSH-Sitzungen, Lücken); optionale Anwesenheit am Mac
6.11.x
Neues Ticket in Plane oder Jira je nach Projekt; Tempo bucht mit Benutzer-Schlüssel und Pflicht-Account; Aufräumen lokaler Projekte mit Sicherung
6.10.x
Menü einfach/erweitert, „Vorschläge“ und „Übertragen“; doppelte Projekte erkennen und zusammenführen
6.9.0
Übergangs-Ticket je Monat für Tempo, „Automatisch einrichten“, Namen wie in Plane, Abwesenheiten im Kalender und als Kalender-Abo
6.8.0
Einrichtungs-Assistent; Projekt-Verknüpfung an einer Stelle; Plane-Buchung nur für Projekte, die in Plane existieren
Offen für die nächsten Schritte: Firefox-Add-on bei Mozilla signieren (Beispiel-Konto), Add-on mit Adressen aus den Einstellungen statt fest eingebaut, Zeitblöcke der To-dos im Kalender, ein Dialog „Zeit buchen“ statt mehrerer Wege, eine gemeinsame Freigabeliste für Plane und Tempo, Abwesenheiten aus Tempo (HAP) als Urlaub/Krank erkennen und danach das Entfernen von Altteilen (CSV-Import, alter Tempo-Import, Tempo-Altdaten-Zuordnung).
Tempo-Abgleich und Regeln für die Server-Version
Für die Zeit vor der Umstellung ist Tempo die Quelle. Das System holt diese Zeiten nur lesend, damit Arbeitszeitkonto, Auswertung und Lücken von Anfang an stimmen. Die folgenden Regeln gelten heute in der Mac-App und unverändert im Team-Server:
• Eine führende Quelle je Buchung. Jede Buchung trägt ihre Herkunft (App, Tempo, Plane) und die Kennung im Fremdsystem. In der App erfasst: die App führt und überträgt. Aus Tempo gespiegelt: Tempo führt, hier nur lesbar, wird nie zurückgeschrieben.
• Idempotent über Kennungen. Der Abgleich ist beliebig wiederholbar. Geänderte Worklogs werden nachgezogen, in Tempo gelöschte nur im vollständig gelesenen Zeitraum entfernt. Selbst übertragene Worklogs werden an ihrer Übertragungs-Kennung erkannt und nicht doppelt geholt.
• Nur eigene Daten. Abgefragt wird mit dem eigenen Jira-Benutzerschlüssel, beim Speichern noch einmal geprüft. Die Daten gehen an keinen Dienst außer Jira/Tempo; auf dem Server sieht sie nur, wer laut Rollen darf.
• Arbeitszeitkonto. Soll ab Eintrittsdatum abzüglich Feiertagen und Abwesenheiten; Ist ist die Summe der Buchungen (seit v6.36 immer; das Arbeitstag-Fenster nur, wenn ausdrücklich eingeschaltet). Ein Startsaldo wird als Saldo-Korrektur eingetragen. Abwesenheiten (seit v6.25): Fürs Konto zählt nur, was im Programm eingetragen ist – von Hand oder aus Kalender/Tempo übernommen –, ganze Tage nach dem eigenen Tages-Soll; der Urlaubszähler zählt Arbeitstage. Urlaub in Tempo ist ein Ticket: Zeit auf dem Abwesenheitsprojekt (HAP) zählt nicht als Arbeitszeit, der Zeitraum aus dem Tickettitel („Vacation(…) 20.07.-07.08.26“) wird nur als „übernehmen?“ vorgeschlagen. Ganztägige Kalendertermine wie „Urlaub“, „Krank“, „Abwesend“ werden nur vorgeschlagen (geteilte Kalender). Urlaub, der nur hier steht, meldet der Assistent als „fehlt in Tempo“. Nachtragen fehlender Tage ist ein Hinweis, kein Zwang: deckt das Plus das Fehlende, erscheint es nur grau.
• Lernen nur je Person. Ticket-Vorschläge nutzen die eigene Historie (auch die aus Tempo). Auf dem Server bleibt das je Person; nichts wird über Personen hinweg ausgewertet ohne Freigabe.
• Umzug Mac → Server. Der Server übernimmt die Mac-Datenbank je Person über dieselben Kennungen, ohne Doppelte, und führt den Tempo-Abgleich selbst weiter. Die Mac-App wird danach zum Agenten (Erkennung, Offline-Puffer).
Plausibilitätsprüfung (Schutz vor Fehl- und Doppelbuchungen)
Jede Buchung wird beim Speichern geprüft – in der Mac-App heute, auf dem Server später mit denselben Regeln. Ziel: Betrug und Versehen erschweren, ohne normale Arbeit zu behindern. Darum drei Stufen:
Stufe
Wann
Was passiert
Blockieren
Beginn in der Zukunft, Ende vor Beginn, Einzelbuchung über 16 h, Tag über 16 h
Wird nicht gespeichert
Bestätigen
Einzelbuchung über 10 h, Tag über 10 h (ArbZG §3), Überschneidung mit einer anderen Buchung ab 3 min, Buchung an Urlaubs-/Krank-/Feiertag, Monat in Tempo schon gesperrt
Speichern nur mit kurzer Begründung; Begründung steht im Protokoll
Hinweis
Ruhezeit unter 11 h (§5), Pause fehlt nach 6 h (30 min) oder 9 h (45 min) (§4)
Nur angezeigt
• Der Timer wird nie blockiert. Läuft er zu lang oder überschneidet sich, wird die Buchung als „Bitte prüfen“ markiert und geht erst nach Bestätigung nach Tempo.
• Überschneidungen bietet der Assistent zum Lösen an: zusammenführen (gleicher Termin doppelt), Überlappung abschneiden (zwei Tickets) oder „Ist richtig so“ – danach wird das Paar nicht mehr gemeldet.
• Tempo-Spiegel (aus Tempo geholte Zeiten) werden nicht geprüft, weil Tempo oft nur geschätzte Uhrzeiten liefert.
• Server: dieselbe Prüfung serverseitig (der Client kann sie nicht umgehen); Bestätigungen und Begründungen sieht die freigebende Person im Monatsabschluss.
To-dos und Planung
Planen gehört zur Zeiterfassung dazu: Wer morgen oder die Woche plant, bucht am Ende sauberer. Ein To-do ist optional an ein Ticket gebunden (Plane oder Jira, je nach Projekt) und hat einen geplanten Tag, eine Fälligkeit, eine Schätzung und auf Wunsch eine Uhrzeit.
• Spalten: Überfällig, Heute, Morgen (nächster Arbeitstag), Diese Woche, Später, Eingang. Karten ziehen = umplanen; ▶ startet den normalen Timer auf dem Ticket.
• Kapazität: je Tag Soll minus Termine minus schon Gebuchtes gegenüber dem Verplanten.
• Vorschläge: zugewiesene Tickets (fällig/dringend zuerst), Termine ohne Vorbereitung, Liegengebliebenes, Wiederkehrendes. Der Assistent erinnert nachmittags an „Morgen planen“ und montags/freitags an die Woche.
• Abgleich, einzeln abschaltbar: Fälligkeit ins Ticket (Plane Zieldatum, Jira Fälligkeit), geplante Zeit als Tempo-Plan (Jira), geplanter Tag als Startdatum in Plane. Gebucht wird dadurch nichts; Fehler stehen am To-do und blockieren nie.
• Saldo: nur abgeschlossene Tage plus heutiges Plus; die Zukunft zählt nicht ins Minus. Daneben „noch offen“ für Woche und Monat.
• Im Kalender (seit v6.24): To-dos mit Uhrzeit erscheinen als gestrichelter Zeitblock, ohne Uhrzeit oben in der Zeile „ganzt.“, Fälligkeiten mit Fahne. Ein To-do auf eine Uhrzeit ziehen = fest einplanen; Vorschläge lassen sich direkt in eine Spalte ziehen.
• Vergessene Tage (seit v6.23): ein einziges To-do „Zeiten nachtragen“ mit allen Tagen unter Soll; aktualisiert sich selbst und hakt sich ab, sobald alles nachgetragen ist.
• Server: To-dos gehören der Person; Leitungen sehen nur Summen (verplant/frei), keine Inhalte, sofern nicht anders freigegeben.
Browser-Add-on und Zugang zur lokalen Schnittstelle
Ist ein Jira- oder Plane-Ticket im Browser offen, startet ein Klick im Add-on den ganz normalen Tracker auf diesem Ticket. Der Timer läuft dann in Zeitprotokoll, bis man ihn stoppt – es wird nie gemessen, wie lange eine Seite offen ist.
• Grundregel: Das Add-on spricht nie direkt mit Jira, Plane oder Tempo, sondern nur mit dem lokalen Programm (heute die Mac-App auf 127.0.0.1, später der Agent). Kommt der Team-Server, leitet der Agent weiter; das Add-on bleibt unverändert.
• Minimale Rechte: sieht nur die Jira- und Plane-Adresse und 127.0.0.1; darf nur Timer starten/pausieren/stoppen, To-do anlegen und den Timer-Zustand lesen. Buchungen, Einstellungen, Zugangsdaten sieht es nicht. Keine Datenweitergabe an Dritte.
• Kopplung: einmalig im Add-on „verbinden“, in Zeitprotokoll „Erlauben“. Das Add-on bekommt einen eigenen Schlüssel (gespeichert nur als SHA-256), jederzeit trennbar; jede Kopplung steht im Protokoll.
• Zugangsschutz der lokalen Schnittstelle: nur eigene Oberfläche, Mac-App mit App-Schlüssel (Datei nur für den Benutzer lesbar) und gekoppelte Add-ons. Fremde Webseiten werden abgewiesen, ebenso Anfragen mit falschem Host (Schutz vor DNS-Rebinding).
• Installation: Die Mac-App erkennt selbst, welche Browser installiert sind, und zeigt je Browser, ob das Add-on schon verbunden ist; „Alle einrichten“ richtet alle fehlenden auf einmal ein. Firefox braucht ein von Mozilla signiertes Paket (ohne öffentlichen Eintrag), Chrome/Edge/Brave/Arc laden es über den Entwicklermodus oder einen nicht öffentlichen Store-Eintrag. Safari bleibt beim Erkennen über den Tracker (abschaltbar); Browser mit verbundenem Add-on werden dabei gar nicht mehr abgefragt, und die macOS-Erlaubnisfrage kommt je Browser nur einmal. Das Tracker-Fenster bleibt über anderen Programmen sichtbar, aber nicht über dem eigenen Hauptfenster.
• Adressen: heute fest für Beispiel im Add-on eingetragen (vom Browser beim Installieren angezeigt). Für andere Teams/Kunden: Add-on ohne Seitenzugriff, das nach dem Koppeln genau die eingestellten Jira-/Plane-Adressen beim Browser anfragt.
• Plane-Adressen ohne Ticketnummer (Detailseite mit interner Kennung oder Vorschau peekId) löst Zeitprotokoll über die Plane-Schnittstelle in die Nummer auf (nur lesen, zwischengespeichert).
• Safari (Entscheidung 29.10.2026): kein Add-on und kein Knopf im Browser. Ein Safari-Add-on bräuchte ein mit Apple-Entwicklerkonto signiertes Mac-Programm (Xcode), das Beispiel nicht hat; ein Lesezeichen mit Zusatzfenster war zu umständlich. In Safari erkennt Zeitprotokoll das offene Ticket selbst, sobald man den Tracker öffnet („▶ Tracken“ dort). Alle anderen Browser bekommen das Add-on.
• Server: dieselbe Kopplung zwischen Add-on und Agent; zwischen Agent und Server gilt die Anmeldung der Person (OIDC).
Fahrplan und offene Entscheidungen
Nächster Schritt ist der Server mit Anmeldung und Abgleich; Team-Funktionen starten erst nach einem Pilot und der Betriebsvereinbarung.
Zu entscheiden, bevor Phase 2 beginnt:
[ ] Sehen Teamleitungen Einzelbuchungen oder nur Tagessummen?
[ ] PostgreSQL auf eigener VM oder auf dem vorhandenen Datenbank-Server?
[ ] Plane-Anbindung zunächst per Dienst-Konto oder gleich als Plane-App (OAuth)?
[ ] Wer pilotiert (Vorschlag: 3 Personen aus Betrieb und Entwicklung, 1 Monat)?
[ ] Aufbewahrungsfristen für Arbeitszeit- und Kundendaten (mit Buchhaltung und Datenschutz)
[ ] Windows-App schon im Pilot oder erst in Phase 4?
