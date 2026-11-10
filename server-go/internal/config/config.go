// Package config liest die Konfiguration ausschließlich aus Umgebungsvariablen.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr           string // ZP_ADDR, Standard ":8080"
	DatabaseURL    string // DATABASE_URL (Pflicht)
	StaticDir      string // ZP_STATIC_DIR: Verzeichnis der gebauten Weboberfläche
	SecretKey      string // ZP_SECRET_KEY: Schlüssel für Token-Verschlüsselung; leer = Schlüsseldatei in DataDir (nur Test)
	DataDir        string // ZP_DATA_DIR, Standard "./data"
	SetupToken     string // ZP_SETUP_TOKEN: Einrichtungs-Token; leer = bei jedem Start zufällig, steht im Log
	TZ             string // ZP_TZ, Standard "Europe/Berlin": Zeitzone für lokale Datumswerte
	AllowHTTP      bool   // ZP_ALLOW_HTTP=1: Plane-Adresse darf http:// sein
	Production     bool   // ZP_PRODUCTION=1 oder auf OpenShift/Kubernetes: Testmodus ohne Anmeldung verboten, Schlüssel Pflicht
	SetupEnabled   bool   // ZP_SETUP_ENABLED=1: /setup bleibt nach der Einrichtung offen (sonst gesperrt)
	TrustProxy     bool   // ZP_TRUST_PROXY=1: Aufrufer-Adresse aus X-Forwarded-For (hinter dem Router)
	TrustedProxies int    // ZP_TRUSTED_PROXIES (Standard 1): Anzahl vorgeschalteter Proxys für X-Forwarded-For
	PublicURL      string // ZP_PUBLIC_URL: öffentliche Adresse; andere Host-Köpfe werden abgelehnt
	AuthMode       string // ZP_AUTH_MODE=none richtet den Testmodus ohne Assistenten ein
	ExtraCAFile    string // ZP_EXTRA_CA_FILE: PEM-Datei mit zusätzlichen Zertifikaten (Firmen-CA) für Jira/Tempo
}

func Load() (Config, error) {
	c := Config{
		Addr:           env("ZP_ADDR", ":8080"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		StaticDir:      env("ZP_STATIC_DIR", ""),
		SecretKey:      os.Getenv("ZP_SECRET_KEY"),
		DataDir:        env("ZP_DATA_DIR", "./data"),
		SetupToken:     os.Getenv("ZP_SETUP_TOKEN"),
		TZ:             env("ZP_TZ", "Europe/Berlin"),
		AllowHTTP:      os.Getenv("ZP_ALLOW_HTTP") == "1",
		Production:     os.Getenv("ZP_PRODUCTION") == "1" || os.Getenv("KUBERNETES_SERVICE_HOST") != "",
		SetupEnabled:   os.Getenv("ZP_SETUP_ENABLED") == "1",
		TrustProxy:     os.Getenv("ZP_TRUST_PROXY") == "1",
		TrustedProxies: envInt("ZP_TRUSTED_PROXIES", 1),
		PublicURL:      strings.TrimRight(os.Getenv("ZP_PUBLIC_URL"), "/"),
		AuthMode:       os.Getenv("ZP_AUTH_MODE"),
		ExtraCAFile:    os.Getenv("ZP_EXTRA_CA_FILE"),
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL fehlt")
	}
	return c, nil
}

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil && n > 0 {
		return n
	}
	return def
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
