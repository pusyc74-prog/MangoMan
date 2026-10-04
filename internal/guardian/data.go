package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Data copies production data to dev every night with personal details
// masked, so bugs can be reproduced on realistic data without exposing
// customers. Export prints an SQL dump of production (it runs with the full
// environment, in the project folder); Import reads the masked dump on its
// standard input into dev's database (dev's environment, in dev's folder).
type Data struct {
	Export string `json:"export,omitempty"` // e.g. pg_dump --data-only --column-inserts "$PROD_DATABASE_URL"
	Import string `json:"import,omitempty"` // e.g. psql "$DATABASE_URL"
	At     string `json:"at,omitempty"`     // daily time, default 02:00
}

var (
	personalCol = regexp.MustCompile(`(?i)name|email|e_mail|phone|mobile|address|street|city|pin_?code|zip|aadhaa?r|pan_?(no|number)?$|dob|birth|ip_?addr|passport|gst`)
	emailRe     = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	phoneRe     = regexp.MustCompile(`(\+?91[\s-]?)?\b[6-9]\d{9}\b`)
	aadhaarRe   = regexp.MustCompile(`\b\d{4}\s?\d{4}\s?\d{4}\b`)
	createRe    = regexp.MustCompile(`(?is)^CREATE TABLE (?:IF NOT EXISTS )?["\x60]?(?:\w+\.)?(\w+)["\x60]?\s*\((.*)\)`)
	insertRe    = regexp.MustCompile(`(?is)^INSERT INTO ["\x60]?(?:\w+\.)?(\w+)["\x60]?\s*(\(([^)]*)\))?\s*VALUES\s*(.*)$`)
)

// Mask hides personal details in an SQL dump: every value in a column whose
// name looks personal (name, email, phone, address, Aadhaar, PAN, date of
// birth...), and any email, Indian mobile number or Aadhaar-like number
// anywhere. Column names come from INSERT column lists or CREATE TABLE.
func Mask(dump string) string {
	cols := map[string][]string{}
	n := 0
	var out strings.Builder
	for _, stmt := range strings.SplitAfter(dump, ";\n") {
		// Inside text values only: numbers outside quotes are ids and amounts.
		stmt = textRe.ReplaceAllStringFunc(stmt, func(v string) string {
			v = emailRe.ReplaceAllString(v, "user@example.com")
			v = phoneRe.ReplaceAllString(v, "9000000000")
			return aadhaarRe.ReplaceAllString(v, "000000000000")
		})
		trimmed := strings.TrimSpace(stmt)
		if m := createRe.FindStringSubmatch(trimmed); m != nil {
			for _, part := range splitTop(m[2], ',') {
				f := strings.Fields(strings.Trim(strings.TrimSpace(part), "\"`"))
				if len(f) > 0 && !notColumn[strings.ToUpper(f[0])] {
					cols[strings.ToLower(m[1])] = append(cols[strings.ToLower(m[1])], strings.Trim(f[0], "\"`"))
				}
			}
		}
		if m := insertRe.FindStringSubmatch(trimmed); m != nil {
			names := cols[strings.ToLower(m[1])]
			if m[3] != "" {
				names = nil
				for _, c := range strings.Split(m[3], ",") {
					names = append(names, strings.Trim(strings.TrimSpace(c), "\"`"))
				}
			}
			head := trimmed[:len(trimmed)-len(m[4])]
			var rows []string
			for _, row := range splitTop(strings.TrimSuffix(strings.TrimSpace(m[4]), ";"), ',') {
				row = strings.TrimSpace(row)
				vals := splitTop(strings.TrimSuffix(strings.TrimPrefix(row, "("), ")"), ',')
				n++
				for i, v := range vals {
					if i < len(names) && personalCol.MatchString(names[i]) && strings.TrimSpace(v) != "NULL" {
						vals[i] = v[:len(v)-len(strings.TrimLeft(v, " "))] + fake(names[i], n)
					}
				}
				rows = append(rows, "("+strings.Join(vals, ",")+")")
			}
			stmt = head + strings.Join(rows, ",") + ";\n"
		}
		out.WriteString(stmt)
	}
	return out.String()
}

var (
	textRe    = regexp.MustCompile(`'(?:[^'\\]|''|\\.)*'`)
	notColumn = map[string]bool{"PRIMARY": true, "UNIQUE": true, "CONSTRAINT": true, "FOREIGN": true, "CHECK": true, "KEY": true, "INDEX": true}
)

func fake(col string, n int) string {
	c := strings.ToLower(col)
	switch {
	case strings.Contains(c, "email"):
		return fmt.Sprintf("'user%d@example.com'", n)
	case strings.Contains(c, "phone") || strings.Contains(c, "mobile"):
		return "'9000000000'"
	case strings.Contains(c, "dob") || strings.Contains(c, "birth"):
		return "'2000-01-01'"
	case strings.Contains(c, "name"):
		return fmt.Sprintf("'Customer %d'", n)
	}
	return "'masked'"
}

// splitTop splits s at sep outside quotes and brackets.
func splitTop(s string, sep byte) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				if i+1 < len(s) && s[i+1] == quote {
					i++ // '' inside a string
				} else {
					quote = 0
				}
			} else if c == '\\' {
				i++
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == sep && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// copyData exports production, masks it and imports it into dev.
func (g *Guardian) copyData(ctx context.Context) error {
	d := g.Cfg.Data
	dump, err := shell(ctx, g.Cfg.Repo, d.Export)
	if err != nil {
		return fmt.Errorf("export failed: %s", cut(strings.TrimSpace(dump), 300))
	}
	cmd := shellCmd(ctx, d.Import)
	cmd.Dir, cmd.Env, cmd.Stdin = g.devDir(), devEnv(g.Cfg.Dev.Env), strings.NewReader(Mask(dump))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("import into dev failed: %s", cut(strings.TrimSpace(string(out)), 300))
	}
	return nil
}

// dueData copies the data once a day after its time.
func (g *Guardian) dueData(ctx context.Context, now time.Time) {
	d := g.Cfg.Data
	if d.Export == "" || d.Import == "" || !g.copying.TryLock() {
		return
	}
	defer g.copying.Unlock()
	if _, err := os.Stat(g.devDir()); err != nil {
		return
	}
	at := d.At
	if at == "" {
		at = "02:00"
	}
	t, err := time.ParseInLocation("15:04", at, now.Location())
	if err != nil {
		return
	}
	when := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	path := filepath.Join(g.Dir, "data-copied.json")
	var last time.Time
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &last)
	}
	if now.Before(when) || !last.Before(when) {
		return
	}
	b, _ := json.Marshal(now)
	_ = os.WriteFile(path, b, 0o600) // once a day even if it fails; the report says so
	if err := g.copyData(ctx); err != nil {
		g.notify(fmt.Sprintf("%s: the nightly data copy to dev failed: %v", g.Cfg.App, err))
		return
	}
	g.logf("production data copied to dev, personal details masked")
}
