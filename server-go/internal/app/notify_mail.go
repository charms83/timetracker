package app

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"example/zeitprotokoll-server/internal/crypto"
)

/* E-Mail über notifyApproval (Spezifikation §6 / §16): abschaltbar, SMTP (STARTTLS/TLS/plain),
   minimaler Inhalt ohne Zeiten. Exchange-OAuth/Graph folgt später. Entprellung in notify_sent
   (Migration 020). Der Hook blockiert nie den Aufrufer (Hintergrund, kurzes Zeitlimit). */

const (
	mailEnabledKey = "mail_enabled"
	mailHostKey    = "mail_smtp_host"
	mailPortKey    = "mail_smtp_port"
	mailUserKey    = "mail_smtp_user"
	mailPassKey    = "mail_smtp_password"
	mailTLSKey     = "mail_smtp_tls" // starttls | tls | none
	mailFromKey    = "mail_from"
	mailFromNameKey = "mail_from_name"
)

var mailConfigKeys = []string{mailEnabledKey, mailHostKey, mailPortKey, mailUserKey, mailPassKey, mailTLSKey, mailFromKey, mailFromNameKey}

func mailSecretAAD() []byte { return []byte("server|mail_smtp_password") }

// notifyApproval: In-App bleibt in approvalNotices/closeNotices; hier nur E-Mail (wenn eingeschaltet).
func (a *App) notifyApproval(ctx context.Context, event string, userID int64, month string) {
	if a == nil || a.DB == nil || userID <= 0 || !monthRE.MatchString(month) {
		return
	}
	switch event {
	case evClosingSoon, evClosed, evRejected, evNachtrag, evPending:
	default:
		return
	}
	// eigener Kontext: Aufrufer (HTTP/Hintergrundlauf) darf nicht auf SMTP warten
	go a.sendMailNotify(context.WithoutCancel(ctx), event, userID, month)
}

func (a *App) sendMailNotify(ctx context.Context, event string, userID int64, month string) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if a.conf(ctx, mailEnabledKey) != "1" {
		return
	}
	cfg, err := a.mailSMTP(ctx)
	if err != nil || cfg.host == "" || cfg.from == "" {
		return // absichtlich still: Admin hat E-Mail an, aber unvollständig → kein Spam im Log je Ereignis
	}
	msgs, err := a.mailMessages(ctx, event, userID, month)
	if err != nil || len(msgs) == 0 {
		return
	}
	for _, m := range msgs {
		if !a.mailClaim(ctx, event, userID, month, m.to, event == evNachtrag) {
			continue
		}
		if err := mailSend(cfg, m); err != nil {
			a.sysLog(ctx, userID, "warn", "mail", fmt.Sprintf("%s %s an %s: %v", event, month, m.to, err))
			a.mailUnclaim(ctx, event, userID, month, m.to) // erneuter Versuch beim nächsten Ereignis/Lauf
			continue
		}
		a.sysLog(ctx, userID, "success", "mail", fmt.Sprintf("%s %s an %s", event, month, m.to))
	}
}

type mailMsg struct {
	to, subject, body string
}

type mailSMTPCfg struct {
	host, port, user, pass, tlsMode, from, fromName string
}

func (a *App) mailSMTP(ctx context.Context) (mailSMTPCfg, error) {
	c := mailSMTPCfg{
		host:     strings.TrimSpace(a.conf(ctx, mailHostKey)),
		port:     strings.TrimSpace(a.conf(ctx, mailPortKey)),
		user:     strings.TrimSpace(a.conf(ctx, mailUserKey)),
		tlsMode:  strings.ToLower(strings.TrimSpace(a.conf(ctx, mailTLSKey))),
		from:     strings.TrimSpace(a.conf(ctx, mailFromKey)),
		fromName: strings.TrimSpace(a.conf(ctx, mailFromNameKey)),
	}
	if c.port == "" {
		c.port = "587"
	}
	if c.tlsMode == "" {
		c.tlsMode = "starttls"
	}
	if c.fromName == "" {
		c.fromName = "Zeitprotokoll"
	}
	if blob := a.conf(ctx, mailPassKey); blob != "" {
		raw, err := base64.StdEncoding.DecodeString(blob)
		if err != nil {
			return c, err
		}
		plain, err := crypto.OpenAAD(a.Key, raw, mailSecretAAD())
		if err != nil {
			return c, err
		}
		c.pass = string(plain)
	}
	return c, nil
}

// mailMessages: Empfänger und Text je Ereignis – ohne Zeiten, ohne Ticketnummern.
func (a *App) mailMessages(ctx context.Context, event string, userID int64, month string) ([]mailMsg, error) {
	var name, email string
	err := a.DB.QueryRow(ctx, `SELECT name, email FROM users WHERE id=$1 AND role<>'breakglass' AND NOT disabled`, userID).Scan(&name, &email)
	if err != nil {
		return nil, err
	}
	email = strings.TrimSpace(email)
	link := a.mailLink()
	label := monthLabelDE(month)
	switch event {
	case evClosingSoon:
		if email == "" || !looksLikeMail(email) {
			return nil, nil
		}
		return []mailMsg{{to: email, subject: "Monat " + label + " schließt bald",
			body: mailBody("Hallo "+name+",",
				"Der Monat "+label+" schließt in Kürze. Bitte prüfe deine Einträge und reiche den Monat ein, wenn alles stimmt.",
				"Nach dem Schließen kannst du selbst nichts mehr ändern – Nachtragen macht dann die Teamleitung oder das Personal.",
				link)}}, nil
	case evClosed:
		if email == "" || !looksLikeMail(email) {
			return nil, nil
		}
		return []mailMsg{{to: email, subject: "Monat " + label + " ist geschlossen",
			body: mailBody("Hallo "+name+",",
				"Der Monat "+label+" wurde automatisch geschlossen.",
				"Nachträge sind nur noch durch Teamleitung oder Personal mit Begründung möglich. Wiedereröffnen kann nur das Personal.",
				link)}}, nil
	case evRejected:
		if email == "" || !looksLikeMail(email) {
			return nil, nil
		}
		return []mailMsg{{to: email, subject: "Monat " + label + " abgelehnt",
			body: mailBody("Hallo "+name+",",
				"Dein eingereichter Monat "+label+" wurde abgelehnt. Der Monat ist wieder offen zur Korrektur.",
				"Details und Kommentar siehst du in der Monatsfreigabe.",
				link)}}, nil
	case evNachtrag:
		if email == "" || !looksLikeMail(email) {
			return nil, nil
		}
		return []mailMsg{{to: email, subject: "Nachtrag in Monat " + label,
			body: mailBody("Hallo "+name+",",
				"In deinem Monat "+label+" wurde durch Teamleitung oder Personal nachgetragen.",
				"Den Verlauf siehst du in der Monatsfreigabe bzw. im Statusverlauf.",
				link)}}, nil
	case evPending:
		// an das Personal (Freigabe-Modus hr/either); nie Zeiten
		rows, err := a.DB.Query(ctx, `SELECT DISTINCT u.email, u.name FROM users u
			WHERE u.is_hr AND NOT u.disabled AND u.role<>'breakglass' AND u.email <> '' AND u.id <> $1`, userID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []mailMsg
		for rows.Next() {
			var to, hrName string
			if rows.Scan(&to, &hrName) != nil {
				continue
			}
			to = strings.TrimSpace(to)
			if !looksLikeMail(to) {
				continue
			}
			out = append(out, mailMsg{to: to, subject: "Freigabe offen: " + name + " · " + label,
				body: mailBody("Hallo "+hrName+",",
					name+" hat den Monat "+label+" eingereicht.",
					"Bitte prüfe und gib frei oder lehne ab – in der Personal-Übersicht.",
					link)})
		}
		return out, rows.Err()
	}
	return nil, nil
}

func (a *App) mailLink() string {
	if a.PublicURL != "" {
		return a.PublicURL + "/"
	}
	return ""
}

func mailBody(greeting, line1, line2, link string) string {
	var b strings.Builder
	b.WriteString(greeting)
	b.WriteString("\n\n")
	b.WriteString(line1)
	b.WriteString("\n\n")
	b.WriteString(line2)
	if link != "" {
		b.WriteString("\n\n")
		b.WriteString(link)
	}
	b.WriteString("\n\n— Zeitprotokoll\n")
	return b.String()
}

func monthLabelDE(month string) string {
	if len(month) < 7 {
		return month
	}
	names := []string{"", "Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"}
	var y, m int
	if _, err := fmt.Sscanf(month, "%d-%d", &y, &m); err != nil || m < 1 || m > 12 {
		return month
	}
	return fmt.Sprintf("%s %d", names[m], y)
}

func looksLikeMail(s string) bool {
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address != "" && strings.Contains(addr.Address, "@") && !strings.HasSuffix(strings.ToLower(addr.Address), "@lokal")
}

// mailClaim: Entprellung. once=true → für immer je Empfänger; once=false (Nachtrag) → höchstens alle 60 Minuten.
func (a *App) mailClaim(ctx context.Context, event string, userID int64, month, recipient string, rateLimit bool) bool {
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	if rateLimit {
		var recent bool
		_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notify_sent WHERE event=$1 AND user_id=$2 AND month=$3 AND recipient=$4 AND sent_at > now() - interval '60 minutes')`,
			event, userID, month, recipient).Scan(&recent)
		if recent {
			return false
		}
		_, err := a.DB.Exec(ctx, `INSERT INTO notify_sent(event,user_id,month,recipient) VALUES($1,$2,$3,$4)
			ON CONFLICT (event,user_id,month,recipient) DO UPDATE SET sent_at=now()`, event, userID, month, recipient)
		return err == nil
	}
	tag, err := a.DB.Exec(ctx, `INSERT INTO notify_sent(event,user_id,month,recipient) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
		event, userID, month, recipient)
	return err == nil && tag.RowsAffected() == 1
}

func (a *App) mailUnclaim(ctx context.Context, event string, userID int64, month, recipient string) {
	_, _ = a.DB.Exec(ctx, `DELETE FROM notify_sent WHERE event=$1 AND user_id=$2 AND month=$3 AND recipient=$4`,
		event, userID, month, strings.ToLower(strings.TrimSpace(recipient)))
}

/* ── SMTP-Versand ────────────────────────────────────────────────── */

func mailSend(cfg mailSMTPCfg, m mailMsg) error {
	if cfg.host == "" || m.to == "" {
		return errors.New("SMTP unvollständig")
	}
	from := cfg.from
	if cfg.fromName != "" {
		from = (&mail.Address{Name: cfg.fromName, Address: cfg.from}).String()
	}
	msg := []byte("From: " + from + "\r\n" +
		"To: " + m.to + "\r\n" +
		"Subject: " + mailHeader(m.subject) + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n\r\n" + m.body)
	addr := net.JoinHostPort(cfg.host, cfg.port)
	switch cfg.tlsMode {
	case "none":
		return mailSMTPPlain(addr, cfg, m.to, msg)
	case "tls":
		return mailSMTPTLS(addr, cfg, m.to, msg)
	default: // starttls
		return mailSMTPStartTLS(addr, cfg, m.to, msg)
	}
}

func mailHeader(s string) string {
	// einfache Betreffzeile ohne Zeilenumbruch; UTF-8 bleibt 8bit im Body, Betreff ASCII-sicher halten
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func mailAuth(cfg mailSMTPCfg) smtp.Auth {
	if cfg.user == "" {
		return nil
	}
	return smtp.PlainAuth("", cfg.user, cfg.pass, cfg.host)
}

func mailSMTPPlain(addr string, cfg mailSMTPCfg, to string, msg []byte) error {
	return smtp.SendMail(addr, mailAuth(cfg), cfg.from, []string{to}, msg)
}

func mailSMTPTLS(addr string, cfg mailSMTPCfg, to string, msg []byte) error {
	tlsCfg := &tls.Config{ServerName: cfg.host, MinVersion: tls.VersionTLS12}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, tlsCfg)
	if err != nil {
		return err
	}
	defer conn.Close()
	c, err := smtp.NewClient(conn, cfg.host)
	if err != nil {
		return err
	}
	defer c.Close()
	return mailSMTPData(c, cfg, to, msg)
}

func mailSMTPStartTLS(addr string, cfg mailSMTPCfg, to string, msg []byte) error {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, cfg.host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: cfg.host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	return mailSMTPData(c, cfg, to, msg)
}

func mailSMTPData(c *smtp.Client, cfg mailSMTPCfg, to string, msg []byte) error {
	if auth := mailAuth(cfg); auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(cfg.from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

/* ── Admin-Prüfung / Speichern für mail_* ────────────────────────── */

func (a *App) checkMailConfig(key, raw string) (string, error) {
	val := strings.TrimSpace(raw)
	switch key {
	case mailEnabledKey:
		if val != "0" && val != "1" {
			return "", errUser(mailEnabledKey + ": 0 (aus) oder 1 (ein)")
		}
		return val, nil
	case mailPortKey:
		if val == "" {
			return "587", nil
		}
		var n int
		if _, err := fmt.Sscan(val, &n); err != nil || n < 1 || n > 65535 || fmt.Sprint(n) != val {
			return "", errUser(mailPortKey + ": Port 1–65535")
		}
		return val, nil
	case mailTLSKey:
		val = strings.ToLower(val)
		if val == "" {
			return "starttls", nil
		}
		if val != "starttls" && val != "tls" && val != "none" {
			return "", errUser(mailTLSKey + ": starttls, tls oder none")
		}
		return val, nil
	case mailFromKey:
		if val != "" && !looksLikeMail(val) {
			return "", errUser(mailFromKey + ": gültige Absender-Adresse")
		}
		return val, nil
	case mailFromNameKey, mailHostKey, mailUserKey:
		if len(val) > 200 {
			return "", errUser(key + ": höchstens 200 Zeichen")
		}
		return val, nil
	case mailPassKey:
		if len(val) > 500 {
			return "", errUser(mailPassKey + ": zu lang")
		}
		return val, nil // Verschlüsselung in applyMailConfig
	}
	return "", errUser("unbekannt: " + key)
}

func (a *App) applyMailConfig(ctx context.Context, key, val string) error {
	if key == mailPassKey {
		if val == "" {
			_, err := a.DB.Exec(ctx, `DELETE FROM server_config WHERE key=$1`, key)
			return err
		}
		blob, err := crypto.SealAAD(a.Key, []byte(val), mailSecretAAD())
		if err != nil {
			return err
		}
		return a.setConf(ctx, key, base64.StdEncoding.EncodeToString(blob))
	}
	if val == "" && (key == mailHostKey || key == mailFromKey || key == mailUserKey || key == mailFromNameKey) {
		_, err := a.DB.Exec(ctx, `DELETE FROM server_config WHERE key=$1`, key)
		return err
	}
	return a.setConf(ctx, key, val)
}
