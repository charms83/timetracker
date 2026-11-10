package app

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

/* Begrenzung gegen Missbrauch: Anfragen je Adresse (Token-Eimer) und Fehlversuche beim Einrichtungs-Token. */

type bucket struct {
	tokens float64
	last   time.Time
}

type rateLimiter struct {
	mu          sync.Mutex
	m           map[string]*bucket
	rate, burst float64 // Anfragen pro Sekunde, Spitze
}

func newRateLimiter(rate, burst float64) *rateLimiter {
	return &rateLimiter{m: map[string]*bucket{}, rate: rate, burst: burst}
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.m[key]
	if b == nil {
		if len(l.m) >= 20000 { // Speicher hart begrenzen: erst Altes verwerfen, dann notfalls alles
			for k, v := range l.m {
				if now.Sub(v.last) > time.Minute {
					delete(l.m, k)
				}
			}
			if len(l.m) >= 20000 { // immer noch voll: zufällig ein Zehntel verwerfen (nie alles)
				drop := len(l.m) / 10
				for k := range l.m {
					if drop == 0 {
						break
					}
					delete(l.m, k)
					drop--
				}
			}
		}
		b = &bucket{tokens: l.burst, last: now}
		l.m[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type failLimiter struct {
	mu     sync.Mutex
	m      map[string][]time.Time
	max    int
	window time.Duration
}

func newFailLimiter(max int, window time.Duration) *failLimiter {
	return &failLimiter{m: map[string][]time.Time{}, max: max, window: window}
}

func (f *failLimiter) prune(key string, now time.Time) []time.Time {
	var keep []time.Time
	for _, t := range f.m[key] {
		if now.Sub(t) < f.window {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(f.m, key)
	} else {
		f.m[key] = keep
	}
	return keep
}

func (f *failLimiter) blocked(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prune(key, time.Now())) >= f.max
}

func (f *failLimiter) reset(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, key)
}

func (f *failLimiter) fail(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	f.prune(key, now)
	f.evictIfFull(now)
	f.m[key] = append(f.m[key], now)
}

// reserve: Versuch VOR der (langsamen) Prüfung atomar eintragen. false = Grenze erreicht. Bei Erfolg release() aufrufen.
// So kommen parallele Anfragen nicht alle durch die Sperre, bevor der erste Fehlversuch gezählt ist.
func (f *failLimiter) reserve(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if len(f.prune(key, now)) >= f.max {
		return false
	}
	f.evictIfFull(now)
	f.m[key] = append(f.m[key], now)
	return true
}

func (f *failLimiter) release(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l := f.m[key]; len(l) > 1 {
		f.m[key] = l[:len(l)-1]
	} else {
		delete(f.m, key)
	}
}

// evictIfFull: bei Überlauf erst Abgelaufenes, dann ein Zehntel zufällig verwerfen (nie alles)
func (f *failLimiter) evictIfFull(now time.Time) {
	if len(f.m) < 20000 {
		return
	}
	for k := range f.m {
		f.prune(k, now)
	}
	drop := len(f.m) / 10
	if len(f.m) < 20000 {
		return
	}
	for k := range f.m {
		if drop == 0 {
			break
		}
		delete(f.m, k)
		drop--
	}
}

// clientIP: Adresse des Aufrufers. Hinter dem OpenShift-Router nur mit ZP_TRUST_PROXY=1 aus X-Forwarded-For
// (sonst ließe sich die Begrenzung mit einem gefälschten Kopf umgehen).
func (a *App) clientIP(r *http.Request) string {
	if a.TrustProxy {
		// Der Router hängt die echte Adresse HINTEN an; vorangestellte Einträge setzt der Client selbst (fälschbar).
		// Gezählt wird von rechts: bei einem vertrauten Proxy der letzte Eintrag.
		if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
			parts := strings.Split(xf, ",")
			n := a.TrustedProxies
			if n < 1 {
				n = 1
			}
			idx := len(parts) - n
			if idx < 0 {
				idx = 0
			}
			if ip := strings.TrimSpace(parts[idx]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// hostLoopback: Host-Kopf nennt nur diesen Rechner (gegen DNS-Rebinding im Testmodus)
func hostLoopback(h string) bool {
	host := h
	if hh, _, err := net.SplitHostPort(h); err == nil {
		host = hh
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost")
}
