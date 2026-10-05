// Package review checks a code change before it ships: leaked secrets
// block it; risky code and new dependencies are flagged for a person. A
// model can add a review of the change for bugs; the checks here are rules,
// so they never depend on a model.
package review

import (
	"fmt"
	"regexp"
	"strings"
)

// Finding is one thing the scan found in an added line.
type Finding struct {
	File   string `json:"file"`
	Line   int    `json:"line"` // line number in the new file
	Kind   string `json:"kind"` // secret, risky, dependency
	Detail string `json:"detail"`
	Block  bool   `json:"block"` // must be fixed before the change ships
}

type rule struct {
	kind, detail string
	re           *regexp.Regexp
	block        bool
}

var rules = []rule{
	{"secret", "private key", regexp.MustCompile(`-----BEGIN (RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`), true},
	{"secret", "AWS access key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), true},
	{"secret", "GitHub token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`), true},
	{"secret", "live payment key (Stripe or Razorpay)", regexp.MustCompile(`\b(sk_live_[A-Za-z0-9]{16,}|rzp_live_[A-Za-z0-9]{10,})`), true},
	{"secret", "AI provider key", regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{32,}|gsk_[A-Za-z0-9]{32,}|nvapi-[A-Za-z0-9_-]{32,})`), true},
	{"secret", "Slack token", regexp.MustCompile(`\bxox[abpr]-[A-Za-z0-9-]{10,}`), true},
	{"secret", "password or key written in the code", regexp.MustCompile(`(?i)\b(password|passwd|secret|api_?key|access_?token)\b\s*[:=]\s*["'][^"'\s]{8,}["']`), true},
	{"risky", "runs text as code (eval/exec)", regexp.MustCompile(`\b(eval|exec)\s*\(`), false},
	{"risky", "shell command built from text", regexp.MustCompile(`shell\s*=\s*True|child_process\.exec\(|os\.system\(`), false},
	{"risky", "SQL built by joining text (SQL injection)", regexp.MustCompile(`(?i)["'](select|insert|update|delete)\b[^"']*["']\s*(\+|%|\.format\()|f["'](select|insert|update|delete)\b[^"']*\{`), false},
	{"risky", "HTML set from text (cross-site scripting)", regexp.MustCompile(`\.innerHTML\s*=|dangerouslySetInnerHTML|v-html=`), false},
	{"risky", "weak hashing (md5/sha1)", regexp.MustCompile(`(?i)\b(md5|sha1)\s*\(|hashlib\.(md5|sha1)|createHash\(["'](md5|sha1)`), false},
	{"risky", "TLS checks turned off", regexp.MustCompile(`verify\s*=\s*False|InsecureSkipVerify:\s*true|rejectUnauthorized:\s*false`), false},
}

var depFiles = map[string]bool{"package.json": true, "go.mod": true, "requirements.txt": true, "pyproject.toml": true, "Cargo.toml": true, "Gemfile": true, "composer.json": true}

// Scan checks the added lines of a unified diff (git diff output).
func Scan(diff string) []Finding {
	var out []Finding
	file, line := "", 0
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
		case strings.HasPrefix(l, "@@"):
			// @@ -a,b +c,d @@: new lines start at c
			if _, after, ok := strings.Cut(l, "+"); ok {
				fmt.Sscanf(after, "%d", &line)
				line--
			}
		case strings.HasPrefix(l, "+"):
			line++
			added := l[1:]
			for _, r := range rules {
				if r.re.MatchString(added) {
					out = append(out, Finding{File: file, Line: line, Kind: r.kind, Detail: r.detail, Block: r.block})
				}
			}
			base := file[strings.LastIndex(file, "/")+1:]
			if depFiles[base] && strings.TrimSpace(added) != "" {
				out = append(out, Finding{File: file, Line: line, Kind: "dependency", Detail: "new or changed dependency: " + strings.TrimSpace(added)})
			}
		case !strings.HasPrefix(l, "-"):
			line++
		}
	}
	return out
}

// Blocking reports whether any finding must be fixed first.
func Blocking(fs []Finding) bool {
	for _, f := range fs {
		if f.Block {
			return true
		}
	}
	return false
}

// Report is the findings as markdown. opinion is a model's review, or "".
func Report(fs []Finding, opinion string) string {
	var b strings.Builder
	b.WriteString("# Code review\n\n")
	if len(fs) == 0 {
		b.WriteString("No secrets, risky code or new dependencies found.\n")
	}
	for _, f := range fs {
		mark := "Check"
		if f.Block {
			mark = "MUST FIX"
		}
		fmt.Fprintf(&b, "- %s: %s, %s line %d\n", mark, f.Detail, f.File, f.Line)
	}
	if opinion != "" {
		fmt.Fprintf(&b, "\n## Review (from a model; check before acting)\n\n%s\n", strings.TrimSpace(opinion))
	}
	return b.String()
}
