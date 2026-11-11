package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // Zeitzonen im Programm (schlankes Container-Image ohne zoneinfo)

	"github.com/jackc/pgx/v5/pgxpool"

	"example/zeitprotokoll-server/internal/app"
	"example/zeitprotokoll-server/internal/config"
	"example/zeitprotokoll-server/internal/crypto"
	"example/zeitprotokoll-server/internal/db"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		os.Exit(runCLI(os.Args[1:]))
	}
	if err := run(log); err != nil {
		log.Error("Start fehlgeschlagen", "err", err)
		os.Exit(1)
	}
}

// openDB wartet beim Start auf die Datenbank (auf OpenShift kommt sie oft erst kurz nach dem Server hoch).
func openDB(ctx context.Context, log *slog.Logger, url string, wait time.Duration) (*pgxpool.Pool, error) {
	deadline := time.Now().Add(wait)
	for {
		pool, err := db.Open(ctx, url)
		if err == nil {
			return pool, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, err
		}
		log.Warn("Datenbank noch nicht erreichbar – neuer Versuch in 5 s", "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// newApp baut die Anwendung (Schlüssel, Zeitzone) auf einer offenen Datenbank; gemeinsam für Server und Befehle.
func newApp(log *slog.Logger, cfg config.Config, pool *pgxpool.Pool, setupToken string, readOnlyKey bool) (*app.App, error) {
	load := crypto.LoadKey
	if readOnlyKey { // Befehle legen nie einen neuen Schlüssel an
		load = crypto.ReadKey
	}
	key, fromFile, err := load(cfg.SecretKey, cfg.DataDir)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(cfg.TZ)
	if err != nil {
		return nil, err
	}
	return &app.App{DB: pool, Key: key, KeyFromFile: fromFile, Log: log, SetupToken: setupToken, Loc: loc, AllowHTTP: cfg.AllowHTTP, Started: time.Now(),
		Production: cfg.Production, SetupEnabled: cfg.SetupEnabled, TrustProxy: cfg.TrustProxy, TrustedProxies: cfg.TrustedProxies, PublicURL: cfg.PublicURL,
		ExtraCAFile: cfg.ExtraCAFile}, nil
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := openDB(ctx, log, cfg.DatabaseURL, 60*time.Second)
	if err != nil {
		return err
	}
	defer pool.Close()

	applied, err := db.Migrate(ctx, pool)
	if err != nil {
		return err
	}
	for _, n := range applied {
		log.Info("Migration angewendet", "name", n)
	}

	setupToken := cfg.SetupToken
	if setupToken == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		setupToken = hex.EncodeToString(b)
	}
	a, err := newApp(log, cfg, pool, setupToken, false)
	if err != nil {
		return err
	}
	if cfg.Production {
		// Im Betrieb: Schlüssel nur aus dem Secret (nie aus einer Datei, die je Pod neu entstünde), stark genug, keine Testbetriebsarten
		if a.KeyFromFile {
			return errors.New("Betrieb: ZP_SECRET_KEY ist Pflicht (Schlüssel aus dem OpenShift-Secret)")
		}
		if len(cfg.SecretKey) < 32 {
			return errors.New("Betrieb: ZP_SECRET_KEY muss mindestens 32 Zeichen (zufällig) haben")
		}
		if cfg.PublicURL == "" || !strings.HasPrefix(cfg.PublicURL, "https://") {
			return errors.New("Betrieb: ZP_PUBLIC_URL mit https:// ist Pflicht (sichere Client-Verbindung / Host-Prüfung / HSTS)")
		}
		if !cfg.TrustProxy {
			log.Warn("ZP_TRUST_PROXY nicht gesetzt – hinter dem Router teilen sich alle Anfragen eine Adresse (Begrenzung wirkt für alle gemeinsam)")
		}
	} else {
		if a.KeyFromFile {
			log.Warn("ZP_SECRET_KEY nicht gesetzt – Schlüsseldatei im Datenordner, nur für Test", "dir", cfg.DataDir)
		} else if len(cfg.SecretKey) < 32 {
			log.Warn("ZP_SECRET_KEY ist kürzer als 32 Zeichen – im Betrieb nicht zulässig")
		}
	}
	if err := a.ApplyEnvDefaults(ctx, cfg.AuthMode); err != nil {
		return err
	}
	a.StartSelfCheck(ctx) // Selbsttest beim Start (mit sicheren Reparaturen), danach alle 6 Stunden

	mux := http.NewServeMux()
	a.Routes(mux)
	handler := a.Protect(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		c, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(c); err != nil {
			http.Error(w, "db nicht erreichbar", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	if cfg.StaticDir != "" {
		mux.Handle("/", http.FileServer(safeFS{http.Dir(cfg.StaticDir)}))
	} else { // ohne Weboberfläche: die Startseite führt zur Administration (die leitet bei Bedarf zur Anmeldung/Einrichtung weiter)
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/admin", http.StatusSeeOther) })
	}

	srv := &http.Server{
		Addr: cfg.Addr, Handler: handler, MaxHeaderBytes: 64 << 10,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 150 * time.Second, // die Ticketliste darf bis zu 90 s warten
		IdleTimeout:  120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("Server läuft", "addr", cfg.Addr)
	if cfg.SetupToken == "" && a.SetupOpen(ctx) { // nur solange die Einrichtung offen ist
		log.Info("Einrichtung", "url", "http://localhost"+cfg.Addr+"/setup?token="+setupToken)
	}
	log.Info("Administration", "url", "http://localhost"+cfg.Addr+"/admin")

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("Beende …")
		sc, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(sc)
	}
	return nil
}

// safeFS liefert keine Verzeichnislisten und keine versteckten Dateien aus
type safeFS struct{ fs http.FileSystem }

func (s safeFS) Open(name string) (http.File, error) {
	if strings.Contains(name, "/.") {
		return nil, os.ErrNotExist
	}
	f, err := s.fs.Open(name)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.IsDir() { // Verzeichnis nur, wenn es eine index.html hat
		idx, err := s.fs.Open(strings.TrimRight(name, "/") + "/index.html")
		if err != nil {
			f.Close()
			return nil, os.ErrNotExist
		}
		idx.Close()
	}
	return f, nil
}
