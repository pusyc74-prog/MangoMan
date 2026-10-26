package skills

import (
	"fmt"
	"regexp"
	"strings"
)

// Scan reads a skill file someone else wrote (an import, a community skill)
// for what should not be in one, the way ECC's AgentShield checks agent
// files: instructions hidden from the user, sending data out, piping a
// download into a shell, destructive commands, keys and passwords written
// in, and invisible characters. A finding is a reason to read the file
// before using it, not proof of harm.

// Finding is one thing Scan saw, with its line number (1-based).
type Finding struct {
	Line int    `json:"line"`
	What string `json:"what"`
}

var scanRules = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`(?i)\b(ignore|disregard|forget|override)\b.{0,30}\b(previous|prior|above|earlier|all|any|other|system)\b.{0,20}\b(instructions?|rules?|prompts?)`),
		"tells the model to ignore its other instructions"},
	{regexp.MustCompile(`(?i)\b(do not|don't|never)\b.{0,20}\b(tell|inform|show|mention to|let)\b.{0,20}\b(the )?(user|owner)\b|\bwithout (telling|asking|informing) the (user|owner)\b|\bsecretly\b`),
		"tells the model to hide something from the user"},
	{regexp.MustCompile(`(?i)(curl|wget|invoke-webrequest|iwr|invoke-restmethod|irm)\b[^\n]*\|\s*(sudo\s+)?(ba|z|da)?sh\b|\|\s*(iex|invoke-expression)\b|\biex\s*\(|base64\s+(-d|--decode)[^\n]*\|\s*(ba)?sh\b`),
		"runs code downloaded from the internet"},
	{regexp.MustCompile(`(?i)(curl|wget|invoke-webrequest|iwr|invoke-restmethod|irm|requests\.post|fetch\()[^\n]*(\$\{?[A-Z_]*(KEY|TOKEN|SECRET|PASSWORD)|os\.environ|process\.env|MANGOMAN_TOKEN|\.ssh|id_rsa|\.aws|keys\.enc)`),
		"sends keys or private files to the internet"},
	{regexp.MustCompile(`(?i)(cat|type|get-content|open\(|read)\s*[^\n]{0,40}(~/\.ssh|id_rsa|id_ed25519|\.aws/credentials|keys\.enc|\.mangoman/config\.json|login data|cookies\.sqlite)`),
		"reads keys, passwords or browser data"},
	{regexp.MustCompile(`(?i)\brm\s+-[a-z]*r[a-z]*f?[a-z]*\s+(/|~|\$home|\*)(\s|$)|\bdel\s+/[sq].{0,10}[a-z]:\\|\bformat\s+[a-z]:|\bmkfs\b|:\(\)\s*\{\s*:\|:&\s*\};:|\bgit\s+push\s+(-f|--force)\b`),
		"runs a command that can destroy files or history"},
	{regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{30,}|xox[abpr]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{35}|gsk_[A-Za-z0-9]{30,}|nvapi-[A-Za-z0-9_-]{30,})\b|-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		"holds a key or password written into the file"},
	{regexp.MustCompile(`[A-Za-z0-9+/]{300,}={0,2}`),
		"holds a long block of encoded data that cannot be read"},
}

// invisible reports a character that changes what a reader sees: zero-width
// and direction marks, word joiners, the byte-order mark mid-text, and tag
// characters (which can spell hidden text).
func invisible(r rune) bool {
	return (r >= 0x200B && r <= 0x200F) || (r >= 0x202A && r <= 0x202E) || (r >= 0x2060 && r <= 0x2064) ||
		(r >= 0x2066 && r <= 0x2069) || r == 0xFEFF || (r >= 0xE0000 && r <= 0xE007F)
}

// Scan returns what it saw, at most one finding per rule per line.
func Scan(text string) []Finding {
	var out []Finding
	for i, line := range strings.Split(text, "\n") {
		for _, r := range scanRules {
			if r.re.MatchString(line) {
				out = append(out, Finding{i + 1, r.what})
			}
		}
		for j, c := range line {
			if i == 0 && j == 0 && c == 0xFEFF {
				continue // a byte-order mark at the very start is how some editors save
			}
			if invisible(c) {
				out = append(out, Finding{i + 1, fmt.Sprintf("holds an invisible character (U+%04X) that can hide text", c)})
				break
			}
		}
	}
	return out
}
