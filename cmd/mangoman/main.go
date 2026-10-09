// Command mangoman is the local, free-first AI router.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/ingress"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/providers"
	"github.com/pusyc74-prog/mangoman/internal/pyenv"
	"github.com/pusyc74-prog/mangoman/internal/radar"
	"github.com/pusyc74-prog/mangoman/internal/router"
	"github.com/pusyc74-prog/mangoman/internal/setup"
	"github.com/pusyc74-prog/mangoman/internal/store"
)

// version is set at build time with -ldflags "-X main.version=..."
var version = "0.1.0-dev"

const usage = `MangoMan: free-first local AI router

Usage:
  mangoman                      open MangoMan in your browser (setup on the first run)
  mangoman ready [--yes]        download what MangoMan needs here: the coding helper and its own Python
  mangoman setup                guided setup in the terminal: connect free providers step by step
  mangoman init                 create config and local token, print tool setup
  mangoman serve [--port N]     run the local endpoint on 127.0.0.1
  mangoman keys add <provider>  store a provider key on this computer
  mangoman keys list            show providers and which keys are present
  mangoman keys rm <provider>   remove a stored key
  mangoman people [add|rm NAME] one local token per person on a shared machine, for usage per person
  mangoman dashboard            open the dashboard in your browser
  mangoman list [add|rm|up|new]  My list: models tried first; new free models
  mangoman code [--model M] [--ui]  open OpenCode on free models; --ui opens the coding screen in the browser
  mangoman mcp                  assist mode: MCP server for Claude Code and Codex
  mangoman skills [install|remove]  list or install the skill packs
  mangoman agents [install|remove|exec|eval|new|pack]  advanced agents
  mangoman brain [on|off|set|test]  decision brain: smarter task detection, refusal checks
  mangoman group [set|rm]       model groups: "group/<name>" never leaves the group
  mangoman status               show the running router's providers and quota
  mangoman models               list the free model catalogue with data policies
  mangoman test [prompt]        send a test request through the running router
  mangoman doctor [flags]       live-check every connected provider and model
  mangoman new NAME             start an app with a development environment from day 1
  mangoman review [DIR]         check a change: secrets, risky code, new dependencies, bugs
  mangoman tests [FILE...]      the QA agent writes tests, then runs them
  mangoman changelog            release notes from recent commits, into CHANGELOG.md
  mangoman qa [DIR] [--url URL] test a project: its tests, and its web app in a browser
  mangoman guardian [init|setup|run|change|approve]  dev next to production; watch, fix, test, ask, deploy
  mangoman usage [--days N]     summarise requests, failovers and tokens
  mangoman version

Environment:
  MANGOMAN_HOME        config directory (default: OS config dir/mangoman)
  MANGOMAN_KEYSTORE    set to "file" to use the encrypted file instead of the keychain
  MANGOMAN_PASSPHRASE  passphrase for the encrypted key file (else prompted)
  GROQ_API_KEY, CEREBRAS_API_KEY, OPENROUTER_API_KEY, NVIDIA_API_KEY  key overrides
`

func main() {
	providers.Version = version
	if home, err := config.Dir(); err == nil {
		pyenv.Use(home)
	}
	if len(os.Args) < 2 {
		// A double-click: no terminal knowledge needed.
		if err := cmdOpen(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			waitIfDoubleClicked()
			os.Exit(1)
		}
		return
	}
	var err error
	switch os.Args[1] {
	case "ready":
		err = cmdReady(os.Args[2:])
	case "setup":
		err = cmdSetup()
	case "init":
		err = cmdInit()
	case "serve":
		err = cmdServe(os.Args[2:])
	case "keys":
		err = cmdKeys(os.Args[2:])
	case "people":
		err = cmdPeople(os.Args[2:])
	case "dashboard", "ui":
		err = cmdDashboard()
	case "list", "mylist":
		err = cmdList(os.Args[2:])
	case "group", "groups":
		err = cmdGroup(os.Args[2:])
	case "code":
		err = cmdCode(os.Args[2:])
	case "brain":
		err = cmdBrain(os.Args[2:])
	case "agents", "agent":
		err = cmdAgents(os.Args[2:])
	case "skills", "skill":
		err = cmdSkills(os.Args[2:])
	case "mcp":
		err = cmdMCP()
	case "status":
		err = cmdStatus()
	case "models":
		err = cmdModels()
	case "test":
		err = cmdTest(os.Args[2:])
	case "doctor":
		err = cmdDoctor(os.Args[2:])
	case "qa":
		err = cmdQA(os.Args[2:])
	case "new":
		err = cmdNew(os.Args[2:])
	case "review":
		err = cmdCodeReview(os.Args[2:])
	case "tests":
		err = cmdTests(os.Args[2:])
	case "changelog":
		err = cmdChangelog(os.Args[2:])
	case "guardian", "watch":
		err = cmdGuardian(os.Args[2:])
	case "usage":
		err = cmdUsage(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("mangoman", version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func cmdInit() error {
	cfg, err := config.Load()
	created := false
	if errors.Is(err, config.ErrNotInitialised) {
		cfg = &config.Config{Port: config.DefaultPort, Token: config.NewToken(), MaxAttempts: 6}
		if err := config.Save(cfg); err != nil {
			return err
		}
		created = true
	} else if err != nil {
		return err
	}
	dir, _ := config.Dir()
	st, err := openStore()
	if err != nil {
		return err
	}
	if created {
		fmt.Println("Created", dir)
	} else {
		fmt.Println("Already initialised:", dir)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d/v1", cfg.Port)
	fmt.Printf(`
Keys are stored in %s.
Every free provider is on by default; each model shows its data policy (mangoman models).

Next:
  1. mangoman setup        connect free providers step by step
  2. mangoman serve        start the router
  3. mangoman dashboard    see providers, models and usage
  4. Point a tool at the router:

  OpenAI-compatible tools (Cursor, Cline, Continue, n8n, own code)
    Base URL:  %s
    API key:   %s
    Model:     free/auto   (or free/coder, free/writer, free/fast, free/long)

  Claude Code (Anthropic Messages API)
    export ANTHROPIC_BASE_URL=http://127.0.0.1:%d
    export ANTHROPIC_AUTH_TOKEN=%s
    claude
    Claude model names map to free models (haiku -> free/fast, others -> free/coder).
    Anthropic does not support Claude Code on other models; assist mode (MCP) follows.

  Codex CLI (Responses API): add to ~/.codex/config.toml
    model = "free/coder"
    model_provider = "mangoman"

    [model_providers.mangoman]
    name = "MangoMan"
    base_url = "%s"
    env_key = "MANGOMAN_TOKEN"
    wire_api = "responses"

    then: export MANGOMAN_TOKEN=%s

  OpenCode: add to opencode.json (project) or ~/.config/opencode/opencode.json
    {
      "$schema": "https://opencode.ai/config.json",
      "provider": {
        "mangoman": {
          "npm": "@ai-sdk/openai-compatible",
          "name": "MangoMan (free)",
          "options": { "baseURL": "%s", "apiKey": "{env:MANGOMAN_TOKEN}" },
          "models": {
            "free/coder": { "name": "Free coder", "limit": { "context": 65536, "output": 8192 } },
            "free/fast":  { "name": "Free fast",  "limit": { "context": 32768, "output": 4096 } }
          }
        }
      },
      "model": "mangoman/free/coder"
    }

  Assist mode (keep Claude or GPT as the main model, offload routine work):
    Claude Code:  claude mcp add --scope user mangoman -- mangoman mcp
    Codex:        add to ~/.codex/config.toml
                    [mcp_servers.mangoman]
                    command = "mangoman"
                    args = ["mcp"]
    Tools: free_ask (tests, docs, summaries), free_review (git changes), free_status.

  Or just run: mangoman code   (OpenCode, already wired to MangoMan)

  Same model every time (workflows): use "strict/<model>", or create a group
  with "mangoman group set <name> <model>..." and use "group/<name>".
`, keys.Where(st), base, cfg.Token, cfg.Port, cfg.Token, base, cfg.Token, base)
	return nil
}

func passphrase() (string, error) {
	if p := os.Getenv("MANGOMAN_PASSPHRASE"); p != "" {
		return p, nil
	}
	p, err := readSecret("Key file passphrase: ")
	if err == nil && p == "" {
		// Also what a run with no terminal reads: say what to do.
		return "", errors.New("no key file passphrase given: type it, or set MANGOMAN_PASSPHRASE")
	}
	return p, err
}

func openStore() (keys.Store, error) {
	p, err := config.Path("keys.enc")
	if err != nil {
		return nil, err
	}
	return keys.Open(p, passphrase)
}

func envMap(cat *catalogue.Catalogue) map[string]string {
	m := map[string]string{}
	for _, p := range cat.AllProviders() {
		if p.KeyEnv != "" {
			m[p.ID] = p.KeyEnv
		}
	}
	return m
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", 0, "port to listen on (127.0.0.1 only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return serve(*port, nil)
}

// serve runs the router until it is stopped. opened, if set, is called once
// it listens, with the number of cloud providers connected.
func serve(port int, opened func(connected int)) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cat, err := catalogue.Seed()
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	loadCustomModels(cat, cfg)
	rt := router.New(cat, keys.NewResolver(st, envMap(cat)), cfg)
	logger := log.New(os.Stderr, "mangoman ", log.LstdFlags)
	rt.Logf = logger.Printf

	dir, _ := config.Dir()
	quotaPath := dir + string(os.PathSeparator) + "quota.json"
	if err := rt.Quota.Load(quotaPath); err != nil {
		logger.Printf("quota state ignored: %v", err)
	}
	if ul, err := store.OpenLog(dir + string(os.PathSeparator) + "usage.jsonl"); err == nil {
		rt.Log = ul
		defer ul.Close()
	}

	// Unlock the key store and warm the key cache now, so a passphrase
	// prompt (file store) happens at startup, and a store that cannot be
	// read stops serve instead of leaving it running with no keys.
	connected := 0
	for _, p := range cat.AllProviders() {
		if !p.NeedsKey {
			continue
		}
		if _, err := st.Get(p.ID); err != nil && !errors.Is(err, keys.ErrNotFound) {
			return fmt.Errorf("cannot read keys (%s): %w", st.Name(), err)
		}
		if rt.HasKey(p) {
			connected++
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	discover := func() {
		if p, ok := cat.Provider("ollama"); ok && !cfg.Excluded("ollama") {
			ms, err := rt.Client.DiscoverOllama(ctx, p)
			if err == nil {
				cat.ReplaceProviderModels("ollama", ms)
			}
		}
	}
	discover()

	rd := &radar.Radar{Cat: cat, Keys: rt.Keys, Team: teamOf(cat, cfg), List: rt.Client.ListModels, Excluded: cfg.Excluded,
		Path: dir + string(os.PathSeparator) + "radar.json"}
	if err := rd.Load(); err != nil {
		logger.Printf("radar state ignored: %v", err)
	}
	go rd.Run(ctx, 30*time.Second, 6*time.Hour, logger.Printf)

	br := ingress.BrainFromConfig(cfg, rt.InternalCall)
	rt.Brain = br

	srv := &ingress.Server{Router: rt, Cfg: cfg, Port: port, Version: version, Started: time.Now(),
		UsagePath: dir + string(os.PathSeparator) + "usage.jsonl", Radar: rd, Brain: br}
	ln, err := net.Listen("tcp", srv.Addr())
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", srv.Addr(), err)
	}
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}

	go func() {
		t := time.NewTicker(30 * time.Second)
		d := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		defer d.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = rt.Quota.Save(quotaPath)
			case <-d.C:
				discover()
			}
		}
	}()

	logger.Printf("listening on http://%s/v1  (%d cloud providers connected, catalogue %s)", srv.Addr(), connected, cat.Version)
	logger.Printf("dashboard: run `mangoman dashboard` in another terminal")
	if connected == 0 {
		logger.Printf("no provider keys yet: run `mangoman keys add groq`")
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	if opened != nil {
		opened(connected)
	}
	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = hs.Shutdown(shutCtx)
	_ = rt.Quota.Save(quotaPath)
	logger.Printf("stopped")
	return nil
}

func cmdKeys(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mangoman keys add|list|rm <provider>")
	}
	cat, err := catalogue.Seed()
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		st, err := openStore()
		if err != nil {
			return err
		}
		res := keys.NewResolver(st, envMap(cat))
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "PROVIDER\tKEY\tDATA POLICY\tSIGN UP")
		for _, p := range cat.AllProviders() {
			state := "not needed (local)"
			if p.NeedsKey {
				state = "missing"
				if k, src := res.Get(p.ID); k != "" {
					state = "present (" + string(src) + ")"
				}
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.ID, state, p.Policy.Label(), p.SignupURL)
			for _, n := range teamNames(p.ID) {
				state := "missing"
				if k, _ := res.Get(keys.Name(p.ID, n)); k != "" {
					state = "present"
				}
				fmt.Fprintf(tw, "  team: %s\t%s\t\t\n", n, state)
			}
		}
		fmt.Printf("Key store: %s\n\n", st.Name())
		return tw.Flush()

	case "add":
		if len(args) < 2 {
			return errors.New("usage: mangoman keys add <provider> [--team NAME] [--stdin] [--no-check]")
		}
		p, ok := cat.Provider(args[1])
		if !ok {
			return fmt.Errorf("unknown provider %q (see `mangoman keys list`)", args[1])
		}
		if !p.NeedsKey {
			return fmt.Errorf("%s needs no key", p.Name)
		}
		fromStdin, check := false, true
		team, err := teamFlag(args[2:])
		if err != nil {
			return err
		}
		for _, a := range args[2:] {
			switch a {
			case "--stdin":
				fromStdin = true
			case "--no-check":
				check = false
			}
		}
		if team != "" {
			if p.NoTeamKeys {
				return fmt.Errorf("team keys are turned off for %s", p.Name)
			}
			fmt.Println(setup.TeamKeyNotice)
		}
		var key string
		if fromStdin {
			line, err := stdin.ReadString('\n')
			if err != nil && err != io.EOF {
				return err
			}
			key = strings.TrimSpace(line)
		} else {
			fmt.Printf("Get a free key at %s\n", p.SignupURL)
			who := "your"
			if team != "" {
				who = team + "'s"
			}
			key, err = readSecret(fmt.Sprintf("Paste %s %s key: ", who, p.Name))
			if err != nil {
				return err
			}
		}
		if key == "" {
			return errors.New("empty key")
		}
		if check {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			fmt.Print("Checking key with ", p.Name, "... ")
			if err := providers.NewClient().ValidateKey(ctx, p, key); err != nil {
				fmt.Println("failed")
				return err
			}
			fmt.Println("ok")
		}
		st, err := openStore()
		if err != nil {
			return err
		}
		if err := st.Set(keys.Name(p.ID, team), key); err != nil {
			return fmt.Errorf("could not store key: %w", err)
		}
		if team != "" {
			if err := setTeamKey(p.ID, team, true); err != nil {
				_ = st.Delete(keys.Name(p.ID, team)) // a key nothing lists would never be used
				return err
			}
			fmt.Printf("Stored %s's %s key in %s. Requests now take turns across the %s keys.\n", team, p.Name, keys.Where(st), p.Name)
			return nil
		}
		fmt.Printf("Stored %s key in %s. Restart `mangoman serve` to use it.\n", p.Name, keys.Where(st))
		return nil

	case "rm", "remove":
		if len(args) < 2 {
			return errors.New("usage: mangoman keys rm <provider> [--team NAME]")
		}
		team, err := teamFlag(args[2:])
		if err != nil {
			return err
		}
		st, err := openStore()
		if err != nil {
			return err
		}
		if err := st.Delete(keys.Name(args[1], team)); err != nil && (team == "" || !errors.Is(err, keys.ErrNotFound)) {
			if errors.Is(err, keys.ErrNotFound) {
				return fmt.Errorf("no stored key for %s", args[1])
			}
			return err
		}
		if team != "" {
			if err := setTeamKey(args[1], team, false); err != nil {
				return err
			}
			fmt.Printf("Removed %s's key for %s\n", team, args[1])
			return nil
		}
		fmt.Println("Removed key for", args[1])
		return nil
	}
	return fmt.Errorf("unknown keys command %q", args[0])
}

// cmdPeople manages one local token per person sharing this machine, so
// mangoman usage can show requests per person.
func cmdPeople(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		names := cfg.PersonNames()
		if len(names) == 0 {
			fmt.Println("No people yet. Add one: mangoman people add NAME")
		}
		for _, n := range names {
			fmt.Println(n)
		}
		return nil
	}
	if len(args) < 2 {
		return errors.New("usage: mangoman people [add|rm NAME]")
	}
	name, err := keys.TeamName(args[1])
	if err != nil {
		return err
	}
	switch args[0] {
	case "add":
		tok := config.NewToken()
		if routerUp(cfg) {
			// The running router keeps its own copy of the settings; change it
			// there, or its next save would drop this person.
			out, err := localSend(cfg, http.MethodPost, "/mangoman/people/"+name, nil)
			if err != nil {
				return err
			}
			var p struct{ Token string }
			if err := json.Unmarshal(out, &p); err != nil || p.Token == "" {
				return fmt.Errorf("the router did not return a token")
			}
			tok = p.Token
		} else {
			cfg.SetPerson(name, tok)
			if err := config.Save(cfg); err != nil {
				return err
			}
		}
		fmt.Printf("Added %s. Use this token in %s's tools instead of the main one (or run mangoman code --as %s):\n%s\n", name, name, name, tok)
		return nil
	case "rm", "remove":
		if routerUp(cfg) {
			if _, err := localSend(cfg, http.MethodDelete, "/mangoman/people/"+name, nil); err != nil {
				return err
			}
		} else {
			cfg.SetPerson(name, "")
			if err := config.Save(cfg); err != nil {
				return err
			}
		}
		fmt.Println("Removed", name)
		return nil
	}
	return fmt.Errorf("unknown people command %q", args[0])
}

// teamFlag reads "--team NAME" from args; "" when absent.
func teamFlag(args []string) (string, error) {
	for i, a := range args {
		if a == "--team" {
			if i+1 >= len(args) {
				return "", errors.New("--team needs a name, for example --team ravi")
			}
			return keys.TeamName(args[i+1])
		}
	}
	return "", nil
}

// teamNames lists a provider's team keys from the config; none if there is
// no config yet.
func teamNames(provider string) []string {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	return cfg.GetTeamKeys(provider)
}

// setTeamKey records (on) or forgets a team key's name. When the router is
// running the change goes through it: it keeps its own copy of the settings,
// and its next save would otherwise undo this one.
func setTeamKey(provider, name string, on bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if routerUp(cfg) {
		method := http.MethodPost
		if !on {
			method = http.MethodDelete
		}
		_, err := localSend(cfg, method, "/mangoman/keys/"+provider+"/team/"+name, nil)
		return err
	}
	cfg.SetTeamKey(provider, name, on)
	return config.Save(cfg)
}

func localGet(cfg *config.Config, path string) (*http.Response, error) {
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", cfg.Port, path), nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	return (&http.Client{Timeout: 5 * time.Second}).Do(req)
}

func cmdStatus() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := localGet(cfg, "/mangoman/status")
	if err != nil {
		fmt.Printf("Router is not running on 127.0.0.1:%d. Start it with `mangoman serve`.\n", cfg.Port)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("router answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var st struct {
		Version   string                   `json:"version"`
		Uptime    int                      `json:"uptime_s"`
		Catalogue string                   `json:"catalogue"`
		Providers []ingress.ProviderStatus `json:"providers"`
		Quota     []struct {
			Key struct {
				Provider, Model string
			} `json:"key"`
			ReqToday     int       `json:"requests_today"`
			TokToday     int       `json:"tokens_today"`
			BlockedUntil time.Time `json:"blocked_until"`
		} `json:"quota"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return err
	}
	fmt.Printf("MangoMan %s, up %s, catalogue %s\n\n", st.Version, time.Duration(st.Uptime)*time.Second, st.Catalogue)
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\tCONNECTED\tMODELS\tCOOLING DOWN\tDATA POLICY")
	for _, p := range st.Providers {
		c := "no"
		if p.Excluded {
			c = "excluded"
		} else if p.Connected {
			c = "yes"
			if p.KeySource != "" {
				c += " (" + p.KeySource + ")"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n", p.ID, c, p.Models, p.OpenModels, p.Policy)
	}
	_ = tw.Flush()
	connected := map[string]bool{}
	for _, p := range st.Providers {
		connected[p.ID] = p.Connected && !p.Excluded
	}
	quota := st.Quota[:0]
	for _, q := range st.Quota {
		if connected[q.Key.Provider] {
			quota = append(quota, q)
		}
	}
	sort.Slice(quota, func(i, j int) bool {
		a, b := quota[i].Key, quota[j].Key
		return a.Provider < b.Provider || a.Provider == b.Provider && a.Model < b.Model
	})
	if len(quota) > 0 {
		fmt.Println()
		tw = tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "PROVIDER\tMODEL\tREQUESTS (24H)\tTOKENS (24H)\tBLOCKED UNTIL")
		for _, q := range quota {
			b := ""
			if !q.BlockedUntil.IsZero() {
				b = q.BlockedUntil.Local().Format("15:04:05")
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n", q.Key.Provider, q.Key.Model, q.ReqToday, q.TokToday, b)
		}
		_ = tw.Flush()
	}
	return nil
}

func cmdModels() error {
	cat, err := catalogue.Seed()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tPROVIDER\tCONTEXT\tRPM\tRPD\tCAPS\tDATA POLICY")
	for _, m := range cat.AllModels() {
		lim := func(n int) string {
			if n == 0 {
				return "-"
			}
			return fmt.Sprint(n)
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\n", m.Canonical, m.Provider, m.Context,
			lim(m.Limits.RPM), lim(m.Limits.RPD), strings.Join(m.Caps, ","), cat.PolicyFor(m).Label())
	}
	fmt.Printf("Catalogue %s. Local Ollama models are discovered when `serve` starts.\n\n", cat.Version)
	return tw.Flush()
}

func cmdTest(args []string) error {
	prompt := strings.Join(args, " ")
	if prompt == "" {
		prompt = "Reply with one short sentence confirming you are working."
	}
	start := time.Now()
	answer, h, err := askRouter(prompt)
	if err != nil {
		return err
	}
	fmt.Printf("%s/%s, %d attempt(s), %s, class %s\nData policy: %s\n\n",
		h.Get("X-MangoMan-Provider"), h.Get("X-MangoMan-Model"), atoi(h.Get("X-MangoMan-Attempts")),
		time.Since(start).Round(time.Millisecond), h.Get("X-MangoMan-Class"), h.Get("X-MangoMan-Data-Policy"))
	fmt.Println(answer)
	return nil
}

// askRouter sends one question through the running router and returns the
// answer and the response headers (which model answered).
func askRouter(prompt string) (string, http.Header, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", nil, err
	}
	body, _ := json.Marshal(map[string]any{
		"model":    "free/auto",
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", cfg.Port), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 3 * time.Minute}).Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("router not reachable (is `mangoman serve` running?): %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	_ = json.Unmarshal(data, &out)
	if len(out.Choices) == 0 {
		return "", resp.Header, errors.New("the router returned no answer")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), resp.Header, nil
}

func atoi(s string) int {
	var n int
	_, _ = fmt.Sscan(s, &n)
	return n
}

// stdin is the one buffered reader on standard input. A second reader
// would lose lines the first one buffered (key, then passphrase, piped).
var stdin = bufio.NewReader(os.Stdin)

func stdinIsTerminal() bool {
	fi, _ := os.Stdin.Stat()
	return fi != nil && fi.Mode()&os.ModeCharDevice != 0
}

// readSecret reads a line without echo where the terminal allows it.
func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	echoOff := false
	if stdinIsTerminal() && runtime.GOOS != "windows" {
		cmd := exec.Command("stty", "-echo")
		cmd.Stdin = os.Stdin
		echoOff = cmd.Run() == nil
	}
	line, err := stdin.ReadString('\n')
	if echoOff {
		cmd := exec.Command("stty", "echo")
		cmd.Stdin = os.Stdin
		_ = cmd.Run()
		fmt.Fprintln(os.Stderr)
	}
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// loadCustomModels adds models the user picked from the new-model radar.
func loadCustomModels(cat *catalogue.Catalogue, cfg *config.Config) {
	for _, m := range cfg.GetCustomModels() {
		if _, ok := cat.Provider(m.Provider); ok {
			cat.AddModel(cat.DiscoveredModel(m.Provider, m.Upstream))
		}
	}
}

// teamOf lists the teammates' keys for a provider, none when the provider
// does not allow team keys.
func teamOf(cat *catalogue.Catalogue, cfg *config.Config) func(string) []string {
	return func(id string) []string {
		if p, ok := cat.Provider(id); !ok || p.NoTeamKeys {
			return nil
		}
		return cfg.GetTeamKeys(id)
	}
}
