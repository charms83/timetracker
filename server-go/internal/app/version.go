package app

/* Build-Infos: package.sh setzt -ldflags "-X …Version=… -X …Commit=… -X …Built=…".
   Ohne ldflags bleiben die Defaults (Entwicklung). */

var (
	Version = "dev" // SemVer oder Datumspaket, z. B. 20261110
	Commit  = ""    // kurzer Git-SHA
	Built   = ""    // UTC-Zeitstempel der Paketierung
)

// ServerVersion: Anzeige für Health/Admin (präfix go- für Erkennung).
func ServerVersion() string {
	if Version == "" || Version == "dev" {
		return "go-dev"
	}
	if len(Version) >= 3 && Version[:3] == "go-" {
		return Version
	}
	return "go-" + Version
}
