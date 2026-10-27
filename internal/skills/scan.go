package skills

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/pusyc74-prog/mangoman/internal/review"
)

// Scan reads a skill file someone else wrote (an import, a community skill)
// for what should not be in one, the way ECC's AgentShield checks agent
// files: instructions hidden from the user, sending data out, running a
// download, destructive commands, keys and passwords written in, encoded
// blocks, invisible characters and look-alike letters. A finding is a reason
// to read the file before using it, not proof of harm; skills that warn
// about these things quote them too.

// Finding is one thing Scan saw, with its line number (1-based) and the
// start of that line.
type Finding struct {
	Line int    `json:"line"`
	What string `json:"what"`
	Text string `json:"text"`
}

const sensitive = `~/\.ssh|\.ssh/|id_rsa|id_ed25519|\.aws/credentials|keys\.enc|\.mangoman/config\.json|login data|cookies\.sqlite`

// prose rules also run over each paragraph with its lines joined, since
// markdown is often wrapped mid-sentence.
var (
	ignoreRule = regexp.MustCompile(`(?i)\b(ignore|disregard|forget|override)\b.{0,40}\b(previous|prior|above|earlier|all|any|other|system|everything)\b.{0,30}\b(instructions?|rules?|prompts?|told)`)
	hideRule   = regexp.MustCompile(`(?i)\b(do not|don't|never|must not|mustn't)\b.{0,40}\b(tell|inform|show|mention|reveal|disclose|let)\b.{0,30}\b(the )?(user|owner|anyone)\b|\bwithout (telling|asking|informing) the (user|owner)\b|\bhid(e|den) (it |this )?from the (user|owner)\b|\bsecretly\b`)
	lineRules  = []struct {
		re   *regexp.Regexp
		what string
	}{
		{regexp.MustCompile(`(?i)(curl|wget|invoke-webrequest|iwr|invoke-restmethod|irm)\b[^\n]*\|\s*(sudo(\s+-\S+)*\s+)?((ba|z|da)?sh|python[0-9.]*|node|perl|ruby)\b|[<$]\(\s*(curl|wget)\b|\|\s*(iex|invoke-expression)\b|\biex\s*\(|base64\s+(-d|--decode)[^\n]*\|\s*(ba)?sh\b|exec\(.*urlopen`),
			"runs code downloaded from the internet"},
		{regexp.MustCompile(`(?i)(curl|wget|invoke-webrequest|iwr|invoke-restmethod|irm|requests\.\w+|urlopen|httpx|fetch\(|\bnc\b)[^\n]*(\$\{?[A-Z_]*(KEY|TOKEN|SECRET|PASSWORD)|os\.environ|process\.env|MANGOMAN_TOKEN|` + sensitive + `)`),
			"sends keys or private files to the internet"},
		{regexp.MustCompile(`(?i)\b(send|upload|post|email|paste)\b.{0,60}(` + sensitive + `).{0,80}https?://|https?://.{0,80}(` + sensitive + `)`),
			"sends keys or private files to the internet"},
		{regexp.MustCompile(`(?i)(cat|type|get-content|open\(|read)\s*[^\n]{0,40}(` + sensitive + `)`),
			"reads keys, passwords or browser data"},
		{regexp.MustCompile(`(?i)\brm\s+(-\S+\s+)*-[a-z]*r[a-z]*\s+(-\S+\s+)*(/\*?|~/?|\$home/?|\*)(\s|$)|\bdel\s+/[sq].{0,10}[a-z]:\\|\bformat\s+[a-z]:|\bmkfs\b|:\(\)\s*\{\s*:\|:&\s*\};:|\bgit\s+push\s+(-f|--force)(\s|$)`),
			"runs a command that can destroy files or history"},
		// Keys code review lets through (a Google browser key is public in a
		// web page) but a skill file has no reason to hold.
		{regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{20,}|AIza[0-9A-Za-z_-]{35})`),
			"holds a key or password written into the file (API key)"},
		{regexp.MustCompile(`[A-Za-z0-9+/]{300,}={0,2}`),
			"holds a long block of encoded data that cannot be read"},
	}
	wrapped64 = regexp.MustCompile(`(?m)(^[A-Za-z0-9+/=]{60,}\n){5,}`)
)

// hiding reports a character that hides or reorders text: zero-width
// spaces, direction overrides and isolates, word joiners, a byte-order mark
// mid-text, and tag characters (which can spell hidden words). The joiners
// emoji and many scripts need (U+200C, U+200D) and plain direction marks
// (U+200E, U+200F) are normal text.
func hiding(r rune) bool {
	return r == 0x200B || (r >= 0x202A && r <= 0x202E) || (r >= 0x2060 && r <= 0x2064) ||
		(r >= 0x2066 && r <= 0x2069) || r == 0xFEFF || (r >= 0xE0000 && r <= 0xE007F)
}

// mixedScript reports a word written in Latin letters with Cyrillic ones
// mixed in, a way to slip a word like "ignore" past a check.
func mixedScript(line string) bool {
	for _, w := range strings.FieldsFunc(line, func(r rune) bool { return !unicode.IsLetter(r) }) {
		latin, other := false, false
		for _, r := range w {
			latin = latin || unicode.In(r, unicode.Latin)
			other = other || unicode.In(r, unicode.Cyrillic)
		}
		if latin && other {
			return true
		}
	}
	return false
}

// Scan returns what it saw, at most one finding of a kind per line.
func Scan(text string) []Finding {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	var out []Finding
	seen := map[string]bool{}
	add := func(i int, what string) {
		if k := fmt.Sprint(i, what); !seen[k] {
			seen[k] = true
			s := strings.TrimSpace(lines[i])
			if r := []rune(s); len(r) > 60 {
				s = string(r[:60]) + "..."
			}
			out = append(out, Finding{i + 1, what, s})
		}
	}
	const ignoreWhat, hideWhat = "tells the model to ignore its other instructions", "tells the model to hide something from the user"
	start := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			start = i + 1
			continue
		}
		// The paragraph's last few lines joined catch a sentence wrapped over
		// lines; the finding goes on the line where the sentence ends.
		from := max(start, i-4)
		para, before := strings.Join(lines[from:i+1], " "), strings.Join(lines[from:i], " ")
		if ignoreRule.MatchString(line) || ignoreRule.MatchString(para) && !ignoreRule.MatchString(before) {
			add(i, ignoreWhat)
		}
		if hideRule.MatchString(line) || hideRule.MatchString(para) && !hideRule.MatchString(before) {
			add(i, hideWhat)
		}
		for _, r := range lineRules {
			if r.re.MatchString(line) {
				add(i, r.what)
			}
		}
		if d := review.Secret(line); d != "" {
			add(i, "holds a key or password written into the file ("+d+")")
		}
		if mixedScript(line) {
			add(i, "mixes look-alike letters from other alphabets into a word")
		}
		for j, c := range line {
			if i == 0 && j == 0 && c == 0xFEFF {
				continue // a byte-order mark at the very start is how some editors save
			}
			if hiding(c) {
				add(i, fmt.Sprintf("holds an invisible character (U+%04X) that can hide text", c))
				break
			}
		}
	}
	if loc := wrapped64.FindStringIndex(text + "\n"); loc != nil {
		add(strings.Count(text[:loc[0]], "\n"), "holds a long block of encoded data that cannot be read")
	}
	return out
}
