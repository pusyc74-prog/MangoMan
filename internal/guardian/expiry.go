package guardian

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// How much time must be left before a date counts as a problem.
const (
	certWarn   = 14 * 24 * time.Hour
	domainWarn = 30 * 24 * time.Hour
)

// rdapBase answers "when does this domain end" for most registries (a
// variable for tests).
var rdapBase = "https://rdap.org/domain/"

// expiry checks an https address's certificate and its domain's end date;
// "" when both have time left. A domain whose registry publishes no end date is
// checked on its certificate alone.
func expiry(ctx context.Context, address string, now time.Time) string {
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" {
		return "not an address: " + address
	}
	host := u.Hostname()
	var problems []string
	if u.Scheme == "https" {
		if end, err := certEnd(ctx, host, u.Port()); err != nil {
			problems = append(problems, "security certificate: "+err.Error())
		} else if left := end.Sub(now); left < certWarn {
			problems = append(problems, fmt.Sprintf("the security certificate ends on %s (%s); visitors will see a warning", end.Format("2 Jan 2006"), days(left)))
		}
	}
	if end, name, ok := domainEnd(ctx, host); ok && end.Sub(now) < domainWarn {
		problems = append(problems, fmt.Sprintf("the domain %s ends on %s (%s); renew it with your domain seller", name, end.Format("2 Jan 2006"), days(end.Sub(now))))
	}
	return strings.Join(problems, "; ")
}

func days(d time.Duration) string {
	n := int(d.Hours() / 24)
	switch {
	case d < 0:
		return "already ended"
	case n == 1:
		return "1 day left"
	}
	return fmt.Sprintf("%d days left", n)
}

// certEnd is when the certificate the site shows ends. A certificate that
// does not check out (wrong name, expired, unknown issuer) is an error.
func certEnd(ctx context.Context, host, port string) (time.Time, error) {
	if port == "" {
		port = "443"
	}
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{ServerName: host}}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return time.Time{}, err
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return time.Time{}, fmt.Errorf("none shown")
	}
	return certs[0].NotAfter, nil
}

// domainEnd asks RDAP for the registration's end date, trying the host and
// then shorter names (shop.example.co.uk, example.co.uk, co.uk) until a
// registry answers.
func domainEnd(ctx context.Context, host string) (time.Time, string, bool) {
	if net.ParseIP(host) != nil {
		return time.Time{}, "", false
	}
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	client := &http.Client{Timeout: 15 * time.Second}
	for i := 0; i+2 <= len(labels) && i < 3; i++ {
		name := strings.Join(labels[i:], ".")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rdapBase+name, nil)
		if err != nil {
			return time.Time{}, "", false
		}
		req.Header.Set("Accept", "application/rdap+json")
		resp, err := client.Do(req)
		if err != nil {
			return time.Time{}, "", false
		}
		var r struct {
			Events []struct {
				Action string    `json:"eventAction"`
				Date   time.Time `json:"eventDate"`
			} `json:"events"`
		}
		err = json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&r)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || err != nil {
			continue
		}
		for _, e := range r.Events {
			if e.Action == "expiration" {
				return e.Date, name, true
			}
		}
		return time.Time{}, "", false // the registry answered without an end date
	}
	return time.Time{}, "", false
}
