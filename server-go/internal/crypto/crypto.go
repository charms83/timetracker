// Package crypto verschlüsselt Zugangsdaten (AES-256-GCM) für die Ablage in der Datenbank.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// LoadKey nimmt ZP_SECRET_KEY (beliebiger Text, wird gehasht). Fehlt er, wird für Test und
// lokalen Betrieb ein Schlüssel in <dataDir>/secret.key erzeugt (fromFile=true, nicht für Produktion).
func LoadKey(secret, dataDir string) (key []byte, fromFile bool, err error) {
	if secret != "" {
		h := sha256.Sum256([]byte(secret))
		return h[:], false, nil
	}
	if dataDir == "" {
		dataDir = "./data"
	}
	path := filepath.Join(dataDir, "secret.key")
	if b, err := os.ReadFile(path); err == nil {
		k, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(k) != 32 {
			return nil, true, errors.New(path + ": ungültiger Schlüssel")
		}
		return k, true, nil
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, true, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, true, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(k)), 0o600); err != nil {
		return nil, true, err
	}
	return k, true, nil
}

// ReadKey wie LoadKey, legt aber NIE eine Schlüsseldatei an (für Befehle am Server): fehlt der Schlüssel, ist das ein Fehler.
func ReadKey(secret, dataDir string) (key []byte, fromFile bool, err error) {
	if secret != "" {
		h := sha256.Sum256([]byte(secret))
		return h[:], false, nil
	}
	if dataDir == "" {
		dataDir = "./data"
	}
	path := filepath.Join(dataDir, "secret.key")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, true, errors.New("kein Schlüssel: ZP_SECRET_KEY setzen (oder im Datenordner des Servers ausführen: " + path + ")")
	}
	k, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(k) != 32 {
		return nil, true, errors.New(path + ": ungültiger Schlüssel")
	}
	return k, true, nil
}

func Seal(key, plain []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

var magicV2 = []byte("ZP2:")

// SealAAD verschlüsselt mit zusätzlich geprüften Daten (aad, z. B. „u7|plane_key“): Ein Chiffrat lässt sich so nicht
// in die Zeile einer anderen Person oder anderer Art kopieren. Format: "ZP2:" + Nonce + Chiffrat.
func SealAAD(key, plain, aad []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append(append([]byte{}, magicV2...), nonce...)
	return gcm.Seal(out, nonce, plain, aad), nil
}

// OpenAAD liest neue Chiffrate (mit aad) und, zur Übernahme, alte ohne Bindung.
func OpenAAD(key, blob, aad []byte) ([]byte, error) {
	if len(blob) > len(magicV2) && string(blob[:len(magicV2)]) == string(magicV2) {
		gcm, err := newGCM(key)
		if err != nil {
			return nil, err
		}
		body := blob[len(magicV2):]
		if len(body) >= gcm.NonceSize() {
			if p, err := gcm.Open(nil, body[:gcm.NonceSize()], body[gcm.NonceSize():], aad); err == nil {
				return p, nil
			}
		}
	}
	return Open(key, blob) // alte Chiffrate ohne Bindung
}

// IsLegacy: Chiffrat im alten Format (ohne Bindung an Person/Art)
func IsLegacy(blob []byte) bool {
	return !(len(blob) > len(magicV2) && string(blob[:len(magicV2)]) == string(magicV2))
}

func Open(key, blob []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, errors.New("Chiffrat zu kurz")
	}
	return gcm.Open(nil, blob[:gcm.NonceSize()], blob[gcm.NonceSize():], nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
