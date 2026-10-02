package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/brain"
	"github.com/pusyc74-prog/mangoman/internal/config"
)

const brainUsage = `Usage:
  mangoman brain                        show the decision brain and its recent decisions
  mangoman brain on | off               turn it on or off
  mangoman brain set <model>            change the engine (default free/fast)
  mangoman brain test "<question>" [answer...]   ask it one question (default answers: yes no)

The brain makes small, fast decisions where the router's rules are unsure:
which kind of task a request is, and whether a short reply is a refusal that
another model should retry. Engines: free/fast (default), a local model such as
strict/ollama/qwen3:4b, or Jev once added from the new-models list.
`

func cmdBrain(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	var st brain.Stats
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "", "show", "status":
		err = localDo(cfg, http.MethodGet, "/mangoman/brain", nil, &st)
	case "on", "off":
		err = localDo(cfg, http.MethodPut, "/mangoman/brain", map[string]bool{"enabled": sub == "on"}, &st)
	case "set":
		if len(args) != 1 {
			return errors.New("usage: mangoman brain set <model>")
		}
		err = localDo(cfg, http.MethodPut, "/mangoman/brain", map[string]string{"model": args[0]}, &st)
	case "test":
		if len(args) == 0 {
			return errors.New(`usage: mangoman brain test "<question>" [answer...]`)
		}
		var res struct {
			Answer     string  `json:"answer"`
			Confidence float64 `json:"confidence"`
			Decided    bool    `json:"decided"`
			LatencyMS  int64   `json:"latency_ms"`
			Model      string  `json:"model"`
			Fallback   string  `json:"fallback"`
		}
		if err := localDo(cfg, http.MethodPost, "/mangoman/brain/test",
			map[string]any{"question": args[0], "options": args[1:]}, &res); err != nil {
			return err
		}
		if !res.Decided {
			fmt.Printf("No decision from %s after %d ms (%s).\n", res.Model, res.LatencyMS, res.Fallback)
			return nil
		}
		fmt.Printf("%s (confidence %.0f%%) from %s in %d ms\n", res.Answer, res.Confidence*100, res.Model, res.LatencyMS)
		return nil
	case "help", "-h", "--help":
		fmt.Print(brainUsage)
		return nil
	default:
		fmt.Print(brainUsage)
		return fmt.Errorf("unknown brain command %q", sub)
	}
	if err != nil {
		return err
	}
	state := "on"
	if !st.Enabled {
		state = "off"
	}
	fmt.Printf("Decision brain: %s, engine %s\n", state, st.Model)
	fmt.Printf("%d decisions asked: %d decided, %d fell back to rules, %d answered from memory; average %d ms\n",
		st.Calls, st.Decided, st.Fallbacks, st.CacheHits, st.AvgMS)
	if len(st.Recent) == 0 {
		return nil
	}
	fmt.Println()
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tQUESTION\tANSWER\tCONFIDENCE\tTIME TAKEN")
	for _, d := range st.Recent {
		ans := d.Answer
		if ans == "" {
			ans = "rules kept (" + d.Fallback + ")"
		}
		took := fmt.Sprintf("%d ms", d.LatencyMS)
		if d.Cached {
			took = "from memory"
		}
		kind := map[string]string{"task": "task type", "non_answer": "is it a non-answer?"}[d.Kind]
		if kind == "" {
			kind = d.Kind
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%.0f%%\t%s\n", d.Time.Local().Format(time.Kitchen), kind, strings.TrimSpace(ans), d.Confidence*100, took)
	}
	return tw.Flush()
}
