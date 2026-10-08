package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/mcp"
	"github.com/pusyc74-prog/mangoman/internal/opencode"
	"github.com/pusyc74-prog/mangoman/internal/skills"
)

// cmdMCP runs the assist-mode MCP server on stdin/stdout. Coding tools start
// it themselves (claude mcp add, Codex mcp_servers); nothing else may write
// to stdout here, because stdout is the protocol channel.
func cmdMCP() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	root, _ := os.Getwd()
	a := &mcp.Assist{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", cfg.Port), Token: cfg.Token, Root: root}
	s := &mcp.Server{Name: "mangoman", Version: version, Instructions: mcp.Instructions, Tools: a.Tools()}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return s.Serve(ctx, os.Stdin, os.Stdout)
}

func routerUp(cfg *config.Config) bool {
	c := &http.Client{Timeout: time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.Port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// How mangoman code shows OpenCode.
const (
	modeTUI = iota // OpenCode's own terminal screen
	modeRun        // unattended: mangoman code run ...
	modeUI         // the coding screen in the browser: mangoman code --ui
)

// uiAllowed are commands the coding screen runs without asking: they only
// read. Everything else is shown with Allow and Deny first.
var uiAllowed = []string{"ls*", "pwd", "git status*", "git diff*", "git log*"}

// openCodeConfig is OpenCode's inline config that points it at MangoMan;
// mode is how OpenCode is shown. Measured on 8 Oct: adding a "use the skill
// first" rule and switching off tools nobody used saved almost no tokens per
// request and lowered ad copy scores, so OpenCode runs with its own defaults.
func openCodeConfig(cfg *config.Config, model string, mode int) string {
	models := map[string]any{
		"free/coder": map[string]any{"name": "Free coder", "limit": map[string]int{"context": 65536, "output": 8192}},
		"free/auto":  map[string]any{"name": "Free auto", "limit": map[string]int{"context": 65536, "output": 8192}},
		"free/fast":  map[string]any{"name": "Free fast", "limit": map[string]int{"context": 32768, "output": 4096}},
		"free/long":  map[string]any{"name": "Free long context", "limit": map[string]int{"context": 131072, "output": 8192}},
	}
	for _, g := range cfg.GroupNames() {
		models["group/"+g] = map[string]any{"name": "Group " + g, "limit": map[string]int{"context": 65536, "output": 8192}}
	}
	if _, ok := models[model]; !ok {
		models[model] = map[string]any{"name": model, "limit": map[string]int{"context": 65536, "output": 8192}}
	}
	c := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{"mangoman": map[string]any{
			"npm":  "@ai-sdk/openai-compatible",
			"name": "MangoMan (free models)",
			"options": map[string]any{
				"baseURL": fmt.Sprintf("http://127.0.0.1:%d/v1", cfg.Port),
				"apiKey":  "{env:MANGOMAN_TOKEN}",
			},
			"models": models,
		}},
		"model":       "mangoman/" + model,
		"small_model": "mangoman/free/fast",
	}
	bash := map[string]string{"*": "allow"}
	if mode == modeUI {
		// The coding screen asks before any command that could change
		// something (installs, deletes, scripts); the user can allow a kind
		// of command for the rest of the session.
		bash["*"] = "ask"
		for _, p := range uiAllowed {
			bash[p] = "allow"
		}
	}
	if ad, err := agentsDir(); err == nil {
		// Agent scripts run only through mangoman agents exec (the sandbox).
		// Best effort: the patterns match the command's text.
		bash["*"+ad+"*"], bash["*mangoman/agents/*"], bash["*agents/*/scripts/*"] = "deny", "deny", "deny"
	}
	c["permission"] = map[string]any{"bash": bash}
	b, _ := json.Marshal(c)
	return string(b)
}

// serveEnv is the environment for a router started in the background. It has
// no terminal, so a key file passphrase is asked for here and passed on.
func serveEnv() ([]string, error) {
	env := os.Environ()
	st, err := openStore()
	if err != nil {
		return nil, err
	}
	path, err := config.Path("keys.enc")
	if err != nil {
		return nil, err
	}
	if _, isFile := st.(*keys.FileStore); !isFile || os.Getenv("MANGOMAN_PASSPHRASE") != "" {
		return env, nil
	}
	if _, err := os.Stat(path); err != nil {
		return env, nil // no key file yet: nothing to unlock
	}
	pass, err := readSecret("Key file passphrase: ")
	if err != nil {
		return nil, err
	}
	check := keys.NewFileStore(path, func() (string, error) { return pass, nil })
	if _, err := check.Get(""); err != nil && !errors.Is(err, keys.ErrNotFound) {
		return nil, err
	}
	return append(env, "MANGOMAN_PASSPHRASE="+pass), nil
}

const openCodeInstall = `To install OpenCode yourself, use one of:
  curl -fsSL https://opencode.ai/install | bash
  npm install -g opencode-ai
then run "mangoman code" again.`

// offerOpenCode asks before downloading OpenCode into MangoMan's own folder.
func offerOpenCode(home, why string) (string, error) {
	if !stdinIsTerminal() {
		return "", errors.New("opencode not found")
	}
	size := "about 60 MB"
	if n := opencode.Size(); n > 0 {
		size = fmt.Sprintf("%d MB", n>>20)
	}
	fmt.Printf("MangoMan's coding helper (OpenCode, free and open source) %s.\nDownload it now from its official GitHub page (%s)? [Y/n] ", why, size)
	line, _ := stdin.ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "" && a != "y" && a != "yes" {
		return "", errors.New("opencode not installed")
	}
	fmt.Println("Downloading...")
	p, err := opencode.Install(home)
	if err == nil {
		fmt.Println("Installed.")
	}
	return p, err
}

// cmdCode opens OpenCode wired to MangoMan, starting the router for the
// session if it is not already running.
func cmdCode(args []string) error {
	fs := flag.NewFlagSet("code", flag.ContinueOnError)
	model := fs.String("model", "free/coder", "model: free/coder, free/auto, strict/<model> or group/<name>")
	noWeb := fs.Bool("no-web", false, "turn off OpenCode's web search")
	as := fs.String("as", "", "use this person's local token (mangoman people), so usage shows per person")
	ui := fs.Bool("ui", false, "open the coding screen in the browser instead of the terminal")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Checked before anything starts, so a wrong name costs nothing.
	token := cfg.Token
	if *as != "" {
		name, err := keys.TeamName(*as)
		if token = ""; err == nil {
			token = cfg.People[name]
		}
		if token == "" {
			return fmt.Errorf("no person %q: add them with mangoman people add %s", *as, *as)
		}
	}
	home, err := config.Dir()
	if err != nil {
		return err
	}
	oc, err := opencode.Path(home)
	if err != nil {
		if oc, err = offerOpenCode(home, "is not installed"); err != nil {
			fmt.Println(openCodeInstall)
			return err
		}
	} else if opencode.Outdated(home) {
		if p, err := offerOpenCode(home, "has a newer tested version"); err == nil {
			oc = p
		}
	}

	var router *exec.Cmd
	if !routerUp(cfg) {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		dir, _ := config.Dir()
		logPath := filepath.Join(dir, "serve.log")
		logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer logf.Close()
		env, err := serveEnv()
		if err != nil {
			return err
		}
		router = exec.Command(self, "serve")
		router.Stdout, router.Stderr, router.Env = logf, logf, env
		detach(router)
		if err := router.Start(); err != nil {
			return fmt.Errorf("could not start the router: %w", err)
		}
		defer func() {
			_ = router.Process.Signal(os.Interrupt)
			done := make(chan struct{})
			go func() { _ = router.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = router.Process.Kill()
			}
		}()
		ok := false
		for i := 0; i < 50 && !ok; i++ {
			time.Sleep(100 * time.Millisecond)
			ok = routerUp(cfg)
		}
		if !ok {
			return fmt.Errorf("the router did not start; see %s", logPath)
		}
		fmt.Printf("Started MangoMan for this session (log: %s).\n", logPath)
	}

	env := append(os.Environ(), "MANGOMAN_TOKEN="+token)
	if os.Getenv("OPENCODE_CONFIG_DIR") == "" {
		if oc, err := codeSkillsDir(); err == nil {
			env = append(env, "OPENCODE_CONFIG_DIR="+oc)
		} else {
			fmt.Println("Skill packs not loaded:", err)
		}
	} else {
		fmt.Println("OPENCODE_CONFIG_DIR is set; MangoMan skill packs not added there (mangoman skills install --dir to add them).")
	}
	mode := modeTUI
	switch {
	case *ui:
		mode = modeUI
	case fs.Arg(0) == "run":
		mode = modeRun
	}
	env = append(env, "OPENCODE_CONFIG_CONTENT="+openCodeConfig(cfg, *model, mode))
	if !*noWeb {
		// OpenCode's web search (via Exa) lets it research while it codes.
		env = append(env, "OPENCODE_ENABLE_EXA=1")
	}
	packs := "none"
	if ps, err := skills.List(); err == nil && len(ps) > 0 {
		names := make([]string, len(ps))
		for i, p := range ps {
			names[i] = strings.ReplaceAll(strings.TrimPrefix(p.Name, "mangoman-"), "-", " ")
		}
		packs = strings.Join(names, ", ")
	}
	fmt.Printf("Opening OpenCode on MangoMan free models (%s). Web search %s. Skill packs: %s.\n", *model, map[bool]string{true: "off", false: "on"}[*noWeb], packs)

	// In a project Guardian looks after, your coding happens in a copy made
	// from dev, never in production's code; ship it when it is ready.
	workDir, shipDir := "", ""
	if _, err := os.Stat("guardian.json"); err == nil && mode != modeRun {
		g, err := loadGuardian("guardian.json")
		if err != nil {
			return err
		}
		dir, branch, err := g.Workspace(context.Background())
		if err != nil {
			return fmt.Errorf("could not make your workspace from dev: %w", err)
		}
		workDir = dir
		shipDir, _ = os.Getwd()
		fmt.Printf("Working in a copy of dev on branch %s.\nWhen it is ready: mangoman guardian ship (QA tests it in dev, then you approve it for production).\n", branch)
	}
	if mode == modeUI {
		return openWorkspace(cfg, oc, env, workDir, shipDir)
	}
	cmd := exec.Command(oc, fs.Args()...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = os.Stdin, os.Stdout, os.Stderr, env
	cmd.Dir = workDir
	// Ctrl-C belongs to OpenCode while it runs. A terminate or hang-up
	// (closing the terminal) is passed on, so OpenCode ends and the router
	// started above is stopped by the deferred cleanup instead of lingering.
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(stop)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		if sig, ok := <-stop; ok {
			_ = cmd.Process.Signal(sig)
		}
	}()
	err = cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return nil // OpenCode reported its own error
	}
	return err
}
