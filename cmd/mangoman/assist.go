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

// openCodeConfig is the inline OpenCode config that points it at MangoMan.
func openCodeConfig(cfg *config.Config, model string) string {
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
	if ad, err := agentsDir(); err == nil {
		// Agent scripts run only through mangoman agents exec (the sandbox).
		// Best effort: the patterns match the command's text.
		c["permission"] = map[string]any{"bash": map[string]string{"*": "allow", "*" + ad + "*": "deny",
			"*mangoman/agents/*": "deny", "*agents/*/scripts/*": "deny"}}
	}
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
func offerOpenCode(home string) (string, error) {
	if !stdinIsTerminal() {
		return "", errors.New("opencode not found")
	}
	size := "about 60 MB"
	if n := opencode.Size(); n > 0 {
		size = fmt.Sprintf("%d MB", n>>20)
	}
	fmt.Printf("MangoMan's coding helper (OpenCode, free and open source) is not installed.\nDownload it now from its official GitHub page (%s)? [Y/n] ", size)
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	home, err := config.Dir()
	if err != nil {
		return err
	}
	oc, err := opencode.Path(home)
	if err != nil {
		if oc, err = offerOpenCode(home); err != nil {
			fmt.Println(openCodeInstall)
			return err
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

	env := append(os.Environ(),
		"MANGOMAN_TOKEN="+cfg.Token,
		"OPENCODE_CONFIG_CONTENT="+openCodeConfig(cfg, *model),
	)
	if os.Getenv("OPENCODE_CONFIG_DIR") == "" {
		if oc, err := codeSkillsDir(); err == nil {
			env = append(env, "OPENCODE_CONFIG_DIR="+oc)
		} else {
			fmt.Println("Skill packs not loaded:", err)
		}
	} else {
		fmt.Println("OPENCODE_CONFIG_DIR is set; MangoMan skill packs not added there (mangoman skills install --dir to add them).")
	}
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

	cmd := exec.Command(oc, fs.Args()...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = os.Stdin, os.Stdout, os.Stderr, env
	// In a project Guardian looks after, your coding happens in a copy made
	// from dev, never in production's code; ship it when it is ready.
	if _, err := os.Stat("guardian.json"); err == nil && fs.Arg(0) != "run" {
		g, err := loadGuardian("guardian.json")
		if err != nil {
			return err
		}
		dir, branch, err := g.Workspace(context.Background())
		if err != nil {
			return fmt.Errorf("could not make your workspace from dev: %w", err)
		}
		cmd.Dir = dir
		fmt.Printf("Working in a copy of dev on branch %s.\nWhen it is ready: mangoman guardian ship (QA tests it in dev, then you approve it for production).\n", branch)
	}
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
