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
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/ingress"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/providers"
	"github.com/pusyc74-prog/mangoman/internal/radar"
	"github.com/pusyc74-prog/mangoman/internal/router"
	"github.com/pusyc74-prog/mangoman/internal/store"
)

// version is set at build time with -ldflags "-X main.version=..."
var version = "0.1.0-dev"

const usage = `MangoMan: free-first local AI router

Usage:
  mangoman setup                guided setup: connect free providers step by step
  mangoman init                 create config and local token, print tool setup
  mangoman serve [--port N]     run the local endpoint on 127.0.0.1
  mangoman keys add <provider>  store a provider key (OS keychain)
  mangoman keys list            show providers and which keys are present
  mangoman keys rm <provider>   remove a stored key
  mangoman dashboard            open the dashboard in your browser
  mangoman list [add|rm|up|new]  My list: models tried first; new free models
  mangoman status               show the running router's providers and quota
  mangoman models               list the free model catalogue with data policies
  mangoman test [prompt]        send a test request through the running router
  mangoman doctor [flags]       live-check every connected provider and model
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
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "setup":
		err = cmdSetup()
	case "init":
		err = cmdInit()
	case "serve":
		err = cmdServe(os.Args[2:])
	case "keys":
		err = cmdKeys(os.Args[2:])
	case "dashboard", "ui":
		err = cmdDashboard()
	case "list", "mylist":
		err = cmdList(os.Args[2:])
	case "status":
		err = cmdStatus()
	case "models":
		err = cmdModels()
	case "test":
		err = cmdTest(os.Args[2:])
	case "doctor":
		err = cmdDoctor(os.Args[2:])
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
	store := "OS keychain"
	if !keys.KeychainAvailable() || os.Getenv("MANGOMAN_KEYSTORE") == "file" {
		store = "encrypted file (no OS keychain found)"
	}
	if created {
		fmt.Println("Created", dir)
	} else {
		fmt.Println("Already initialised:", dir)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d/v1", cfg.Port)
	fmt.Printf(`
Keys are stored in: %s
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

  Claude Code: assist mode (MCP) arrives in M4; Anthropic endpoint in M3.
  Codex CLI:   Responses endpoint arrives in M3.
`, store, base, cfg.Token)
	return nil
}

func passphrase() (string, error) {
	if p := os.Getenv("MANGOMAN_PASSPHRASE"); p != "" {
		return p, nil
	}
	return readSecret("Key file passphrase: ")
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
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *port != 0 {
		cfg.Port = *port
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

	// Warm the key cache now, so a passphrase prompt (file store) happens at
	// startup rather than on the first request.
	connected := 0
	for _, p := range cat.AllProviders() {
		if !p.NeedsKey {
			continue
		}
		if k, _ := rt.Keys.Get(p.ID); k != "" {
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

	rd := &radar.Radar{Cat: cat, Keys: rt.Keys, List: rt.Client.ListModels, Excluded: cfg.Excluded,
		Path: dir + string(os.PathSeparator) + "radar.json"}
	if err := rd.Load(); err != nil {
		logger.Printf("radar state ignored: %v", err)
	}
	go rd.Run(ctx, 30*time.Second, 6*time.Hour, logger.Printf)

	srv := &ingress.Server{Router: rt, Cfg: cfg, Version: version, Started: time.Now(),
		UsagePath: dir + string(os.PathSeparator) + "usage.jsonl", Radar: rd}
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
		}
		fmt.Printf("Key store: %s\n\n", st.Name())
		return tw.Flush()

	case "add":
		if len(args) < 2 {
			return errors.New("usage: mangoman keys add <provider> [--stdin] [--no-check]")
		}
		p, ok := cat.Provider(args[1])
		if !ok {
			return fmt.Errorf("unknown provider %q (see `mangoman keys list`)", args[1])
		}
		if !p.NeedsKey {
			return fmt.Errorf("%s needs no key", p.Name)
		}
		fromStdin, check := false, true
		for _, a := range args[2:] {
			switch a {
			case "--stdin":
				fromStdin = true
			case "--no-check":
				check = false
			}
		}
		var key string
		if fromStdin {
			line, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil && err != io.EOF {
				return err
			}
			key = strings.TrimSpace(line)
		} else {
			fmt.Printf("Get a free key at %s\n", p.SignupURL)
			key, err = readSecret(fmt.Sprintf("Paste your %s key: ", p.Name))
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
		if err := st.Set(p.ID, key); err != nil {
			return fmt.Errorf("could not store key: %w", err)
		}
		fmt.Printf("Stored %s key in %s. Restart `mangoman serve` to use it.\n", p.Name, st.Name())
		return nil

	case "rm", "remove":
		if len(args) < 2 {
			return errors.New("usage: mangoman keys rm <provider>")
		}
		st, err := openStore()
		if err != nil {
			return err
		}
		if err := st.Delete(args[1]); err != nil {
			if errors.Is(err, keys.ErrNotFound) {
				return fmt.Errorf("no stored key for %s", args[1])
			}
			return err
		}
		fmt.Println("Removed key for", args[1])
		return nil
	}
	return fmt.Errorf("unknown keys command %q", args[0])
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
	fmt.Fprintln(tw, "PROVIDER\tCONNECTED\tMODELS\tBREAKER OPEN\tDATA POLICY")
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
	if len(st.Quota) > 0 {
		fmt.Println()
		tw = tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "PROVIDER\tMODEL\tREQUESTS (24H)\tTOKENS (24H)\tBLOCKED UNTIL")
		for _, q := range st.Quota {
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
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	prompt := strings.Join(args, " ")
	if prompt == "" {
		prompt = "Reply with one short sentence confirming you are working."
	}
	body, _ := json.Marshal(map[string]any{
		"model":    "free/auto",
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", cfg.Port), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := (&http.Client{Timeout: 3 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("router not reachable (is `mangoman serve` running?): %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	_ = json.Unmarshal(data, &out)
	h := resp.Header
	fmt.Printf("%s/%s, %d attempt(s), %s, class %s\nData policy: %s\n\n",
		h.Get("X-MangoMan-Provider"), h.Get("X-MangoMan-Model"), atoi(h.Get("X-MangoMan-Attempts")),
		time.Since(start).Round(time.Millisecond), h.Get("X-MangoMan-Class"), h.Get("X-MangoMan-Data-Policy"))
	if len(out.Choices) > 0 {
		fmt.Println(strings.TrimSpace(out.Choices[0].Message.Content))
	}
	return nil
}

func atoi(s string) int {
	var n int
	_, _ = fmt.Sscan(s, &n)
	return n
}

// readSecret reads a line without echo where the terminal allows it.
func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fi, _ := os.Stdin.Stat()
	tty := fi != nil && fi.Mode()&os.ModeCharDevice != 0
	echoOff := false
	if tty && runtime.GOOS != "windows" {
		cmd := exec.Command("stty", "-echo")
		cmd.Stdin = os.Stdin
		echoOff = cmd.Run() == nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
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
