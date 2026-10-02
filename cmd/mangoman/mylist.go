package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/ingress"
)

const listUsage = `Usage:
  mangoman list                      show My list (tried first, in this order)
  mangoman list add <model>...       add models, e.g. groq/gpt-oss-120b or kimi-k3
  mangoman list rm <model>           remove a model
  mangoman list up <model>           move a model one place up
  mangoman list new [--scan]         new free models not in the catalogue yet
  mangoman list clear                empty My list (normal routing for everything)

<model> is a catalogue name (any provider) or provider/name (that provider only).
A model from "list new" is added with its provider and id, e.g.
  mangoman list add openrouter/acme/fresh-2:free
The router must be running (mangoman serve).
`

func localDo(cfg *config.Config, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", cfg.Port, path), rd)
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("router not reachable on 127.0.0.1:%d (start it with `mangoman serve`)", cfg.Port)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
			return errors.New(e.Error.Message)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

type favList struct {
	Models []string `json:"models"`
}

func cmdList(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	var cur favList
	if err := localDo(cfg, http.MethodGet, "/mangoman/favorites", nil, &cur); err != nil {
		return err
	}
	put := func(models []string) error {
		if models == nil {
			models = []string{}
		}
		return localDo(cfg, http.MethodPut, "/mangoman/favorites", favList{models}, &cur)
	}
	index := func(name string) int {
		for i, m := range cur.Models {
			if strings.EqualFold(m, name) {
				return i
			}
		}
		return -1
	}
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "", "show":
	case "add":
		if len(args) == 0 {
			return errors.New("name a model: mangoman list add groq/gpt-oss-120b")
		}
		for _, name := range args {
			if index(name) >= 0 {
				continue
			}
			err := put(append(append([]string(nil), cur.Models...), name))
			if err == nil {
				continue
			}
			// Not in the catalogue: maybe a new model from the radar.
			prov, up, ok := strings.Cut(name, "/")
			if !ok {
				return err
			}
			var res favList
			if err2 := localDo(cfg, http.MethodPost, "/mangoman/radar/add",
				map[string]string{"provider": prov, "upstream": up}, &res); err2 != nil {
				return fmt.Errorf("%s: %v", name, err)
			}
			cur = res
		}
	case "rm", "remove":
		if len(args) != 1 {
			return errors.New("usage: mangoman list rm <model>")
		}
		i := index(args[0])
		if i < 0 {
			return fmt.Errorf("%s is not in My list", args[0])
		}
		if err := put(append(append([]string(nil), cur.Models[:i]...), cur.Models[i+1:]...)); err != nil {
			return err
		}
	case "up":
		if len(args) != 1 {
			return errors.New("usage: mangoman list up <model>")
		}
		i := index(args[0])
		if i < 0 {
			return fmt.Errorf("%s is not in My list", args[0])
		}
		if i > 0 {
			m := append([]string(nil), cur.Models...)
			m[i-1], m[i] = m[i], m[i-1]
			if err := put(m); err != nil {
				return err
			}
		}
	case "clear":
		if err := put(nil); err != nil {
			return err
		}
	case "new":
		path, method := "/mangoman/radar", http.MethodGet
		if len(args) > 0 && args[0] == "--scan" {
			path, method = "/mangoman/radar/scan", http.MethodPost
			fmt.Println("Checking connected providers for new models...")
		}
		var rv ingress.RadarView
		if err := localDo(cfg, method, path, nil, &rv); err != nil {
			return err
		}
		return printRadar(rv)
	case "help", "-h", "--help":
		fmt.Print(listUsage)
		return nil
	default:
		fmt.Print(listUsage)
		return fmt.Errorf("unknown list command %q", sub)
	}
	if len(cur.Models) == 0 {
		fmt.Println("My list is empty: the router picks the best free model for each request.")
		fmt.Println("Add models with `mangoman list add <model>`; see `mangoman models` and `mangoman list new`.")
		return nil
	}
	fmt.Println("My list (tried first, in this order; when all are used up, the router falls back to the rest):")
	for i, m := range cur.Models {
		fmt.Printf("  %d. %s\n", i+1, m)
	}
	return nil
}

func printRadar(rv ingress.RadarView) error {
	if !rv.Enabled {
		fmt.Println("The new-model radar is off.")
		return nil
	}
	if rv.LastScan == nil {
		fmt.Println("No scan yet: the first runs 30 seconds after `serve` starts. Try `mangoman list new --scan`.")
		return nil
	}
	if len(rv.Items) == 0 {
		fmt.Printf("No free models outside the catalogue (checked %s).\n", rv.LastScan.Local().Format("Jan 2 15:04"))
		return nil
	}
	fmt.Printf("%d free models your providers serve that are not in the catalogue (%d new), checked %s:\n\n",
		len(rv.Items), rv.NewCount, rv.LastScan.Local().Format("Jan 2 15:04"))
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "\tADD WITH\tFIRST SEEN\tDATA POLICY")
	for _, it := range rv.Items {
		badge := ""
		if it.New {
			badge = "NEW"
		}
		fmt.Fprintf(tw, "%s\t%s/%s\t%s\t%s\n", badge, it.Provider, it.Upstream, it.FirstSeen.Local().Format("Jan 2"), it.Policy)
	}
	_ = tw.Flush()
	fmt.Println("\nAdd one with: mangoman list add <provider/id>")
	return nil
}

const groupUsage = `Usage:
  mangoman group                         list groups
  mangoman group set <name> <model>...   create or replace a group (order = try order)
  mangoman group rm <name>               delete a group

Use a group as the model "group/<name>": only its models are used, in order,
and the router never switches to anything else. For one model only, use
"strict/<model>" (no group needed). When all are used up, requests get a 429
with the time capacity returns.
`

func cmdGroup(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	var res struct {
		Groups map[string][]string `json:"groups"`
	}
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "", "list", "ls":
		err = localDo(cfg, http.MethodGet, "/mangoman/groups", nil, &res)
	case "set":
		if len(args) < 2 {
			return errors.New("usage: mangoman group set <name> <model>...")
		}
		err = localDo(cfg, http.MethodPut, "/mangoman/groups/"+args[0], favList{args[1:]}, &res)
	case "rm", "remove", "delete":
		if len(args) != 1 {
			return errors.New("usage: mangoman group rm <name>")
		}
		err = localDo(cfg, http.MethodPut, "/mangoman/groups/"+args[0], favList{[]string{}}, &res)
	case "help", "-h", "--help":
		fmt.Print(groupUsage)
		return nil
	default:
		fmt.Print(groupUsage)
		return fmt.Errorf("unknown group command %q", sub)
	}
	if err != nil {
		return err
	}
	if len(res.Groups) == 0 {
		fmt.Println("No groups yet. Create one with `mangoman group set coding kimi-k3 groq/gpt-oss-120b`.")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL NAME TO USE\tMODELS (TRIED IN THIS ORDER, NOTHING ELSE)")
	for _, n := range sortedKeys(res.Groups) {
		fmt.Fprintf(tw, "group/%s\t%s\n", n, strings.Join(res.Groups[n], ", "))
	}
	return tw.Flush()
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
