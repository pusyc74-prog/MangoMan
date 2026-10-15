package sheets

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Every number a model writes must come from numbers code computed: the
// query results or the dashboard's facts, as written or rounded, or worked
// out from values that belong together (one result column, one row, one
// fact): their total, a sum or gap of two, and, in small groups only, a
// share or change of two. Ratios of any two values would let almost any
// percentage pass.

var (
	numRe   = regexp.MustCompile(`(?i)(\d[\d,]*(?:\.\d+)?)(?:\s*(lakhs?|lacs?|crores?|cr|l|k|thousand|million|mn|m|bn|b|billion)\b)?`)
	listRe  = regexp.MustCompile(`(?m)^\s*\d{1,2}[.)]\s`)
	grouped = regexp.MustCompile(`^\d{1,3}(,\d{3})+(\.\d+)?$|^\d{1,2}(,\d{2})*,\d{3}(\.\d+)?$|^\d+(\.\d+)?$`)
)

var scales = map[string]float64{"lakh": 1e5, "lakhs": 1e5, "lac": 1e5, "lacs": 1e5, "l": 1e5, "crore": 1e7, "crores": 1e7, "cr": 1e7,
	"k": 1e3, "thousand": 1e3, "million": 1e6, "mn": 1e6, "m": 1e6, "bn": 1e9, "b": 1e9, "billion": 1e9}

type written struct {
	text  string
	value float64
	tol   float64 // half of the last written digit, times the scale
}

// numbersIn finds the numbers in text, with how precisely each was written.
func numbersIn(text string) []written {
	text = listRe.ReplaceAllString(text, " ")
	var out []written
	for _, m := range numRe.FindAllStringSubmatch(text, -1) {
		raw := strings.TrimRight(m[1], ",")
		if !grouped.MatchString(raw) {
			continue // "2,3" is a list, not a number
		}
		v, err := strconv.ParseFloat(strings.ReplaceAll(raw, ",", ""), 64)
		if err != nil {
			continue
		}
		dec := 0
		if i := strings.IndexByte(raw, '.'); i >= 0 {
			dec = len(raw) - i - 1
		}
		scale := 1.0
		if s := scales[strings.ToLower(m[2])]; s > 0 {
			scale = s
		}
		out = append(out, written{text: strings.TrimSpace(m[0]), value: v * scale, tol: 0.5 * math.Pow(10, -float64(dec)) * scale})
	}
	return out
}

// known collects the numbers in values: numbers as they are, and the
// numbers inside words and dates ("2026-09-01" gives 2026, 9 and 1).
func known(values []any) []float64 {
	seen := map[float64]bool{}
	var out []float64
	add := func(v float64) {
		if !seen[v] && !math.IsNaN(v) && !math.IsInf(v, 0) {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, v := range values {
		switch x := v.(type) {
		case float64:
			add(x)
			add(math.Abs(x))
		case int:
			add(float64(x))
		case string:
			for _, w := range numbersIn(strings.ReplaceAll(x, "-", " ")) {
				add(w.value)
			}
		}
	}
	return out
}

// Unchecked returns the numbers in text that do not trace to the groups of
// known values. Small whole numbers (up to 12: "top 3", "6 months") always pass.
func Unchecked(text string, groups [][]any) []string {
	var derived []float64
	for _, g := range groups {
		vals := known(g)
		derived = append(derived, vals...)
		if len(vals) > 60 {
			continue
		}
		sum := 0.0
		for _, v := range g {
			if f, ok := v.(float64); ok {
				sum += f
			}
		}
		derived = append(derived, sum)
		for i, a := range vals {
			for _, b := range vals[i+1:] {
				derived = append(derived, a+b, math.Abs(a-b))
				if len(vals) > 12 || a == 0 || b == 0 {
					continue
				}
				derived = append(derived, a/b*100, b/a*100, math.Abs(a-b)/math.Abs(b)*100, math.Abs(a-b)/math.Abs(a)*100)
			}
		}
	}
	var bad []string
	for _, w := range numbersIn(text) {
		if w.value <= 12 && w.value == math.Trunc(w.value) {
			continue
		}
		ok := false
		for _, k := range derived {
			if math.Abs(math.Abs(k)-w.value) <= w.tol+1e-9*math.Abs(k) {
				ok = true
				break
			}
		}
		if !ok {
			bad = append(bad, w.text)
		}
	}
	return bad
}
