// Package classify tags a request with a task class. Phase 1 is rules only;
// a small local model as tie-break comes later.
package classify

import (
	"regexp"
	"strings"

	"github.com/pusyc74-prog/mangoman/internal/core"
)

// Task classes.
const (
	Code        = "code"
	Reasoning   = "reasoning"
	Writing     = "writing"
	Extraction  = "extraction"
	Vision      = "vision"
	LongContext = "long-context"
	Fast        = "fast"
)

// Virtual model names map to a class. "free/auto" lets the classifier decide.
var Virtual = map[string]string{
	"free/auto":   "",
	"free/coder":  Code,
	"free/writer": Writing,
	"free/fast":   Fast,
	"free/long":   LongContext,
}

// LongContextTokens is the estimated input size treated as long-context.
const LongContextTokens = 24000

var (
	codeHints   = regexp.MustCompile("(?i)```|\\bfunc\\b|\\bdef\\b|\\bclass\\b|stack trace|traceback|exception|\\bbug\\b|refactor|compile|unit test|\\bregex\\b|typescript|python|golang|\\bsql\\b|\\bapi\\b")
	reasonHints = regexp.MustCompile(`(?i)\bprove\b|step by step|\bwhy\b|\bcalculate\b|\bsolve\b|\bplan\b|trade-?off|\banaly[sz]e\b|\bcompare\b`)
)

// Classify returns the class for a request. A virtual model wins over rules.
func Classify(r *core.Request) string {
	c, _ := ClassifySure(r)
	return c
}

// ClassifySure also reports whether the rules are confident. Unsure results
// (only weak hints, or nothing matched) are where the decision brain helps.
func ClassifySure(r *core.Request) (string, bool) {
	if c, ok := Virtual[strings.ToLower(r.Model)]; ok && c != "" {
		return c, true
	}
	switch {
	case r.HasImages:
		return Vision, true
	case r.EstTokens > LongContextTokens:
		return LongContext, true
	case r.WantsJSON:
		return Extraction, true
	}
	text := r.LastUserText()
	switch {
	case r.HasTools() || codeHints.MatchString(text):
		return Code, true
	case reasonHints.MatchString(text):
		return Reasoning, false
	}
	return Writing, false
}

// BrainClasses are the classes the decision brain may choose between.
var BrainClasses = []string{Code, Reasoning, Writing, Extraction, Fast}
