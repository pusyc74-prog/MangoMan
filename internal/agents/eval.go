package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Result is one test case's score for each contender (0 to 100).
type Result struct {
	Case   string             `json:"case"`
	Scores map[string]float64 `json:"scores"`
	Notes  map[string]string  `json:"notes"`
}

// Eval runs every case in casesDir/cases for each contender and scores the
// output with casesDir/score.py. A case is a folder with prompt.txt and any
// input files; run does the work in a fresh copy of it. score.py prints
// {"score": 0-100, "notes": "..."} for a finished folder.
func Eval(casesDir string, contenders []string, workRoot string, run func(contender, dir, prompt string) error) ([]Result, error) {
	entries, err := os.ReadDir(filepath.Join(casesDir, "cases"))
	if err != nil {
		return nil, fmt.Errorf("no test cases in %s: %w", casesDir, err)
	}
	var out []Result
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		src := filepath.Join(casesDir, "cases", e.Name())
		prompt, err := os.ReadFile(filepath.Join(src, "prompt.txt"))
		if err != nil {
			return nil, fmt.Errorf("case %s: %w", e.Name(), err)
		}
		r := Result{Case: e.Name(), Scores: map[string]float64{}, Notes: map[string]string{}}
		for _, c := range contenders {
			dir := filepath.Join(workRoot, c, e.Name())
			if err := os.RemoveAll(dir); err != nil {
				return nil, err
			}
			if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
				return nil, err
			}
			p := fmt.Sprintf("Use the %s skill. %s", c, strings.TrimSpace(string(prompt)))
			if err := run(c, dir, p); err != nil {
				r.Notes[c] = "run failed: " + err.Error()
				continue
			}
			r.Scores[c], r.Notes[c] = score(filepath.Join(casesDir, "score.py"), dir)
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Case < out[j].Case })
	return out, nil
}

func score(script, dir string) (float64, string) {
	out, err := exec.Command("python3", script, dir).Output()
	var s struct {
		Score float64 `json:"score"`
		Notes string  `json:"notes"`
	}
	if err != nil || json.Unmarshal(out, &s) != nil {
		return 0, "could not score: " + strings.TrimSpace(string(out))
	}
	return s.Score, s.Notes
}

// Verdict compares an agent with its free pack: it beats the pack when its
// average is higher and it scored on every case.
func Verdict(results []Result, pack, agent string) (packAvg, agentAvg float64, beats bool) {
	beats = len(results) > 0
	for _, r := range results {
		packAvg += r.Scores[pack]
		agentAvg += r.Scores[agent]
		if r.Scores[agent] == 0 {
			beats = false
		}
	}
	if n := float64(len(results)); n > 0 {
		packAvg, agentAvg = packAvg/n, agentAvg/n
	}
	return packAvg, agentAvg, beats && agentAvg > packAvg
}
