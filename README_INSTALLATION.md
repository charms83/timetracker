# Zeitprotokoll v4.7.0 / Frontend v3.6.0 – Installationsanleitung

Dieses Paket enthält den **kompletten Projektordner** (nicht nur einzelne geänderte
Dateien wie beim letzten Mal – genau das hatte zum Build-Fehler geführt, weil dabei
`index.html`, `vite.config.js` & Co. verloren gingen). Diesmal kannst du den ganzen
`zeitprotokoll`-Ordner gefahrlos ersetzen.

## Voraussetzung: Node.js ≥ 22

Der einzige echte Vorbehalt für einen komplett anderen Mac: Er braucht **Node.js 22 oder
neuer** (wegen `node:sqlite`). Falls dort noch kein Node oder eine ältere Version installiert
ist, bricht `install.sh` das jetzt sauber mit einer klaren Fehlermeldung ab, statt später
kryptisch beim Serverstart zu crashen. Node installieren/aktualisieren:
```bash
brew install node@22   # falls Homebrew vorhanden
# oder direkt von https://nodejs.org (LTS oder aktueller)
```
Xcode Command Line Tools sind für dieses Projekt **nicht** erforderlich – der Installer
prüft das nur noch informativ und blockiert nicht mehr darauf.

---

## Wichtig zuerst: Deine Daten sind sicher

Die Datenbank liegt **nicht** im Projektordner, sondern unter:
```
~/Documents/Zeitprotokoll/zeitprotokoll.db
```
Das Ersetzen von `~/zeitprotokoll` (dem Code-Ordner) hat darauf keinerlei Einfluss.

---

## Schritt-für-Schritt-Installation

### 1. LaunchAgents stoppen

```bash
launchctl unload ~/Library/LaunchAgents/com.user.zeitprotokoll.server.plist
launchctl unload ~/Library/LaunchAgents/com.user.zeitprotokoll.hourly.plist
launchctl unload ~/Library/LaunchAgents/com.user.zeitprotokoll.backup.plist
```
(Falls eine Zeile eine Fehlermeldung wirft, weil der Agent gar nicht läuft – einfach ignorieren.)

### 2. Alten Projektordner beiseiteschieben (nicht löschen, nur zur Sicherheit)

```bash
mv ~/zeitprotokoll ~/zeitprotokoll_alt_$(date +%Y%m%d)
```

### 3. Neues Paket entpacken

Das Zip so entpacken, dass am Ende genau `~/zeitprotokoll/` mit diesem Inhalt entsteht:

```bash
cd ~
unzip ~/Downloads/zeitprotokoll-v4.1-komplett.zip
# ergibt: ~/zeitprotokoll/backend, ~/zeitprotokoll/frontend, ~/zeitprotokoll/scripts, ~/zeitprotokoll/install.sh
```

### 4. Installer laufen lassen

```bash
cd ~/zeitprotokoll
chmod +x install.sh
./install.sh
```

Der Installer:
- installiert Backend- und Frontend-Abhängigkeiten frisch (`npm install`)
- baut das Frontend (`npm run build`)
- legt `~/Documents/Zeitprotokoll/backup/` an, falls nicht vorhanden
- erstellt `.env` mit `MODE=standalone`, falls nicht vorhanden
- richtet alle drei LaunchAgents neu ein und startet sie

**Wichtig:** Das Installer-Skript wurde diesmal auch selbst gefixt – vorher hat es bei
einem fehlgeschlagenen Build trotzdem fälschlich "✓ Frontend gebaut" angezeigt. Jetzt
bricht es bei einem echten Fehler sofort ab und zeigt die tatsächliche Fehlermeldung.

### 5. Ergebnis prüfen

```bash
curl -s http://localhost:3747/api/health
# erwartet: {"ok":true,"version":"4.1.0"}
```

Im Browser `http://localhost:3747` öffnen (ggf. Hard-Reload: Cmd+Shift+R) und
prüfen, ob der neue Button **„☕ Pause erfassen"** sichtbar ist.

---

## Zur npm-Warnung "allow-scripts" / fsevents

Falls beim `npm install` wieder folgende Meldung auftaucht:
```
npm warn allow-scripts fsevents@2.3.3 (install: (install scripts present))
```
→ **Das ist harmlos und war nicht die Ursache des letzten Fehlers.** `fsevents` wird nur
im Dev-Modus (`npm run dev`, nicht `npm run build`) für schnelleres Datei-Watching auf
macOS genutzt. Für den produktiven Build/Betrieb hat es keine Auswirkung. Du kannst es
ignorieren oder bei Gelegenheit einmal freigeben mit:
```bash
npm approve-scripts --allow-scripts-pending
```

Der eigentliche Fehler letztes Mal war **"Could not resolve entry module index.html"** –
das lag daran, dass beim vorherigen (unvollständigen) Update-Paket `index.html` und
weitere Grunddateien fehlten. Dieses Paket enthält jetzt alle nötigen Dateien.

---

## Was ist neu in v4.7.0 (Backend) / v3.6.0 (Frontend)

- **Doppelbuchung von Pausen wird jetzt verhindert**: Überschneidet sich eine neue Pause
  zeitlich mit einer bereits bestehenden (egal ob manuell oder live erfasst), wird das beim
  Anlegen blockiert – mit klarer Fehlermeldung, welche Pause im Weg ist (z.B. „Überschneidet
  sich mit bestehender Pause 12:00–13:00 Uhr").
- **Nachträgliches Bearbeiten bleibt möglich**: Der gleiche Check greift auch beim Bearbeiten
  einer bestehenden Pause (z.B. über den Stift-Button in der Tagesansicht) – verhindert eine
  neue Überschneidung, blockiert aber nicht das Ändern an sich. Verkleinert man z.B. eine
  Pause so, dass sie sich nicht mehr überschneidet, klappt das ganz normal.
- Getestet: Anlegen mit Überschneidung → 400 mit Fehlermeldung; Bearbeiten in überschneidungs-
  freien Zeitraum → funktioniert; Bearbeiten in neue Überschneidung → ebenfalls blockiert.

## Was ist neu in v4.6.0 (Backend) / v3.5.0 (Frontend)

- **Kritischer Bugfix: Pausen wurden nie angezeigt.** Die Tagesansicht (`GET /api/workday`)
  hat Pausen bisher nur geladen, wenn ein "Arbeitstag" existierte – den gibt's seit dem
  Feierabend-Umbau aber gar nicht mehr. Dadurch waren alle neu erfassten Pausen (egal ob
  manuell oder live getrackt) für die App faktisch unsichtbar, obwohl sie korrekt in der
  Datenbank standen. Jetzt werden Pausen direkt über das Datum abgefragt – funktioniert für
  alte (Arbeitstag-gebundene) und neue (freie) Pausen gleichermaßen.
- **Bugfix: Heutige Ist-Stunden zogen keine Pausen ab.** Ein Überbleibsel aus dem
  Feierabend-Umbau – die Anzeige für "heute" (Saldo-Widget, Tages-Header) hat Pausenzeit
  nirgends berücksichtigt. Jetzt korrekt: heutige Stunden abzüglich aller abgeschlossenen
  Pausen des Tages.
- **Bugfix: Tagesansicht aktualisierte sich nicht zuverlässig.** Nach dem Erfassen einer
  Pause/eines Eintrags an einem bereits geöffneten, nicht-heutigen Tag blieb die Ansicht
  leer, bis man sie zu- und wieder aufklappte. Jetzt wird gezielt neu geladen.
- **Neu: Live-Pause-Tracker.** Button „☕ Pause" direkt in der Timer-Leiste – funktioniert
  wie der Projekt-Timer (Start → Live-Anzeige → Stopp). Startet automatisch den laufenden
  Projekt-Timer neu bzw. stoppt ihn (Eintrag wird gespeichert), je nachdem was gerade läuft.
  Ergänzt die bereits vorhandene manuelle Pause-Erfassung (jederzeit nachtragbar, beliebig
  viele pro Tag).
- Alles ausführlich getestet: Live-Pause + manuell nachgetragene Pause am selben Tag,
  beide korrekt in der Tagesansicht sichtbar und von den Ist-Stunden abgezogen.

## Was ist neu in v4.5.1 (Backend) / v3.4.0 (Frontend)

- **Saldo-Korrekturen**: Unter „Abwesenheiten" → „Arbeitszeitmodell" gibt es jetzt eine neue
  Karte „Saldo-Korrekturen" – du kannst jederzeit einen Stichtag setzen (z.B. „am 01.09.
  Saldo auf +5h"), ab dem Folgetag läuft die normale Berechnung von diesem Wert aus weiter.
  Beliebig viele Korrekturen möglich, auch nachträglich löschbar. Getestet: Korrektur
  überschreibt den kumulativen Saldo exakt am gewählten Tag, alte Tage bleiben unangetastet,
  Folgetage bauen korrekt darauf auf.
- **Mehrere Pausen pro Tag bestätigt funktionsfähig**: „☕ Pause erfassen" erlaubt beliebig
  viele Pausen an einem Tag (z.B. kurze Vormittagspause + Mittagspause) – werden alle korrekt
  von den Ist-Stunden abgezogen. Getestet: 9h Eintrag − 15min − 1h = 7,75h.
- **Bugfix (Backend)**: Ein nicht existierender `/api/*`-Pfad (z.B. ein Tippfehler oder eine
  entfernte Route) ließ die Verbindung bisher endlos hängen, statt einen sauberen 404 zurück-
  zugeben. Betraf zwei Stellen im Code (Duplikat), beide gefixt.

## Was ist neu (Frontend v3.3.0)

- **Start-Saldo selbst einstellen**: Unter „Abwesenheiten" → „Arbeitszeitmodell" gibt es
  jetzt eine neue Karte „Arbeitszeitkonto – Startwerte" mit zwei Feldern:
  - **Startdatum der Zeiterfassung** – ab wann gerechnet werden soll (leer = automatisch ab
    dem ersten Zeiteintrag)
  - **Start-Saldo (Stunden)** – dein Über-/Unterstunden-Stand aus der Zeit vor dem System,
    positiv oder negativ, fließt direkt ins laufende Arbeitszeitkonto ein
  - Getestet: Start-Saldo +12h korrekt als Basis der kumulativen Saldo-Berechnung bestätigt.
- **Aufräumen**: Diese zwei Felder gab es technisch schon vorher (Backend hat sie schon
  lange genutzt), standen aber nur versteckt unter „Einstellungen" → „System" – dort raus,
  jetzt an der Stelle, wo man sie auch vermutet (bei den anderen Arbeitszeit-Einstellungen).

## Was ist neu in v4.4.0

- **Pause-Erfassung wieder da** – nach kurzer Rückfrage: Du wolltest sie doch behalten,
  nur ohne das Arbeitstag/Feierabend-Konzept. „☕ Pause erfassen" ist wieder im Timer-Bereich
  und in der Tagesansicht verfügbar. Funktioniert genau wie zuvor: Pause wird automatisch
  von den gebuchten Stunden des Tages abgezogen (z.B. 8h durchgehender Eintrag − 1h Pause
  = 7h Ist-Zeit). Das Backend dafür war unverändert, nur die Frontend-Bedienelemente kamen
  zurück.
- Integritätsprüfung erkennt weiterhin echte Pause-Überschneidungen, ohne bei der gewollten
  Nutzung (Pause innerhalb eines durchgehenden Eintrags) fälschlich anzuschlagen.

## Was ist neu in v4.3.0

- **Kein Arbeitstag/Feierabend-Konzept mehr**: Der Timer lässt sich jetzt jederzeit direkt
  starten, ohne vorher einen "Arbeitstag" zu beginnen. Feierabend-Button, Live-Pause,
  manuelle Pause-Erfassung und die zugehörigen Korrektur-Dialoge sind komplett raus –
  passend zum modularen Arbeiten mit vielen einzelnen Timer-Sessions über den Tag verteilt.
- **Stunden/Saldo bleiben vollständig erhalten**: Tages-Saldo ("Noch X bis Soll" /
  "Überstunden"), Wochen-Saldo und das Arbeitszeitkonto funktionieren weiterhin genau wie
  vorher – laufen jetzt aber durchgängig auf Basis der Summe deiner Zeitbuchungen statt
  eines Arbeitstag-Fensters. Lücken zwischen zwei Timer-Sessions zählen automatisch nicht mit.
- **Stündliche Erinnerung entfernt**: Der automatische Popup ("Was hast du die letzte Stunde
  gemacht?") läuft nicht mehr. `install.sh` deaktiviert eine eventuell noch laufende alte
  Instanz beim Einspielen automatisch sauber.
- Bereits bestehende, historische Arbeitstage/Pausen in der Datenbank bleiben unangetastet
  und werden weiterhin korrekt angezeigt/berechnet – die Änderung betrifft nur, dass ab
  jetzt keine neuen mehr angelegt werden.

## Was ist neu in v4.2.0

- **Timer + Planung verbunden**: Der „▶ Timer starten"-Dialog zeigt jetzt oben
  „📋 Heute geplant" – deine offenen Planungsaufgaben aus dem Planung-Tab. Direkt
  anklicken startet den Timer mit Projekt **und** verknüpfter Planungsaufgabe.
  Ist keine passende Aufgabe dabei, kannst du direkt im selben Dialog eine neue
  Planungsaufgabe für heute anlegen – genau wie beim Projekt.
- **Fortschritt sichtbar**: Planungsaufgaben zeigen jetzt „Getrackt: X von Y Min"
  – sowohl im Timer-Picker als auch über die API (`tracked_seconds`).
- **Kundenverwaltung entfernt**: Der „Kunden"-Tab, das Anlegen/Bearbeiten/Löschen
  von Kunden ist aus der Oberfläche raus. **Die Daten bleiben aber vollständig
  erhalten** – die `customers`-Tabelle und alle bestehenden Kunden-Zuordnungen an
  Projekten werden nicht angetastet, nur die Verwaltungs-UI ist weg. Bestehende
  Projekt-Kunde-Zuordnungen werden weiterhin angezeigt (z.B. Gruppierung in der
  Projektliste), lassen sich über die Oberfläche aber nicht mehr ändern.
- **Automatisches Backup vor jedem Update**: `install.sh` sichert jetzt automatisch
  die bestehende Datenbank nach `~/Documents/Zeitprotokoll/backup/` (Zeitstempel im
  Dateinamen), bevor irgendetwas installiert oder gebaut wird.
- **Health- & Datenkonsistenz-Check nach der Installation**: Der Installer prüft
  am Ende automatisch `/api/health` und `/api/check/integrity` und zeigt dir direkt
  im Terminal, ob der Server läuft und ob Auffälligkeiten in den Daten bestehen.

## Was war schon in v4.1.0


- **Timer-Start ohne Projekt-Zwang**: Beim „▶ Timer starten"/„⇄ Projekt wechseln"-Dialog
  kannst du jetzt einen neuen Projektnamen eintippen und über „➕ Neues Projekt „…" anlegen"
  direkt loslegen – kein Umweg mehr über „Projekte" nötig. Das neue Projekt landet ganz
  normal in der Projektliste (ohne Kunde, kann später wie gewohnt bearbeitet werden).
- **Pausen manuell erfassen**: neuer Button „☕ Pause erfassen", wirkt sich auf reinen
  Buchungstagen (ohne Arbeitstag-Start/Ende) auch echt auf die Ist-Stunden aus
  (Beispiel: 8h durchgehender Eintrag − 1h erfasste Pause = 7h Ist-Zeit)
- Integritätsprüfung erkennt jetzt Pause-Überschneidungen
- Zwei Bugs gefixt, die nur bei einer Neuinstallation mit leerer DB auftraten
  (Migrations-Reihenfolge, fehlende `month_closings`-Tabelle)
- `install.sh` bricht jetzt bei echten Fehlern korrekt ab, statt sie zu verschlucken
- `install.sh` prüft jetzt korrekt auf Node ≥ 22 (statt ≥ 18) und blockiert nicht mehr
  unnötig auf Xcode Command Line Tools

## Getestet (in dieser Umgebung)

- Kompletter Durchlauf aus genau diesem Paket heraus: `npm install` (Backend + Frontend)
  → `npm run build` → Serverstart mit frischer, leerer Datenbank → alle Kern-Endpoints
  (Health, Projects, Overview, Integrity, Month-Closings, Timer, Plan-Tasks) → 200 OK
- Neuer Pause-Button im ausgelieferten JS-Bundle verifiziert vorhanden
- Saldo-Rechnung mit manueller Pause verifiziert (8h − 1h = 7h)

Nicht auf deinem echten Mac getestet (diese Sandbox läuft unter Linux) – bitte nach
der Installation Schritt 5 (Health-Check + Browser) einmal durchgehen und kurz Bescheid
geben, falls doch etwas hakt.
