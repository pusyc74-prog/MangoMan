package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/ingress"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/mcp"
	"github.com/pusyc74-prog/mangoman/internal/opencode"
	"github.com/pusyc74-prog/mangoman/internal/pyenv"
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
// read. Everything else is shown with Allow and Deny first. The Windows ones
// were asked about on the owner's first Windows try (13 Oct).
// Not cat or type: OpenCode checks a command without its redirections, so
// "cat > file" would pass as cat and write without asking.
var uiAllowed = []string{"ls*", "pwd", "git status*", "git diff*", "git log*", "dir*", "Get-ChildItem*", "Get-Content *"}

// projectsAsk are the only commands the coding screen asks about in the
// MangoMan Projects folder, where the agents and templates work for people
// who cannot judge a command (owner, 15 Oct: a data analysis asked about a
// dozen harmless ones, from mkdir to running the skill's own script).
// Everything else runs; these delete, move, download, install, publish or
// change the computer. OpenCode splits a line into its commands and checks
// each, so "cd x; rm y" still asks about rm.
var projectsAsk = []string{
	"rm*", "rmdir*", "del *", "erase *", "rd *", "Remove-Item*", "ri *", "Clear-Content*",
	"mv *", "move *", "Move-Item*", "mi *", "Rename-Item*", "ren *",
	"curl*", "wget*", "Invoke-WebRequest*", "iwr*", "Invoke-RestMethod*", "irm*", "Start-BitsTransfer*",
	"pip *", "pip3 *", "python* -m pip*", "npm *", "npx *", "winget*", "choco*", "brew*", "apt*", "sudo*",
	"git push*", "git reset*", "git clean*", "git checkout*",
	"shutdown*", "Restart-Computer*", "Stop-Computer*", "Stop-Process*", "kill*", "taskkill*",
	"reg *", "Set-ItemProperty*", "Set-ExecutionPolicy*", "chmod*", "chown*", "format*",
}

// codeRules is what every session is told about this computer: where to work
// and how commands are written here, never how to do the task (a "use the
// skill first" rule lowered ad copy scores, 8 Oct). Each rule answers what the
// owner's first Windows try showed (13 Oct): the model changed into a skill's
// folder and wrote there, left every task's files loose in MangoMan Projects,
// used 2>/dev/null in PowerShell, and called a planner page "a social media
// agent". projects is a session in the MangoMan Projects folder; powershell
// is a session whose commands run in PowerShell (see psShell); mode is how
// OpenCode runs (modeRun: nobody is there to answer a question).
func codeRules(projects, powershell bool, mode int) string {
	r := []string{"Run a skill's scripts by their full path. Never change into a skill's folder or write files in it: skill folders belong to MangoMan.",
		// Seen 15 Oct on Windows: python, where python, whereis python, dir C:\Python* before trying python3.
		"Python is python3: MangoMan installs it with every package the skills need. Do not look for another Python or install packages."}
	// The four rules of andrej-karpathy-skills (MIT) in one line, for code
	// only. Not in unattended runs (Guardian, tests, pack runs): there a
	// question ends the run with nothing done.
	if mode != modeRun {
		r = append(r, "When you write or change code: say your assumptions and ask when the request is unclear; write the simplest thing that works; change only what the task needs, in the existing style; run the tests or the app to prove it works before saying it does.")
	}
	// The coding screen already asks before such commands; OpenCode's own
	// terminal screen runs every command without asking.
	if mode == modeTUI {
		r = append(r, "Ask before deleting files you did not make in this task, rewriting git history, deploying, publishing or paying for anything.")
	}
	if projects {
		r = append(r,
			"This is the user's MangoMan Projects folder. For each new task, make a folder named after it in plain words (for example diwali-sale-posts), keep all of that task's files in it, and run that task's commands from inside it. For changes to an earlier task, use its folder.",
			"Nothing made here runs by itself: MangoMan has no way yet to run an agent or bot that posts, replies or watches on its own. If the user asks for one, say so plainly first, then offer what you can make.")
	}
	if powershell {
		r = append(r, `Commands run in Windows PowerShell: join commands with ";" not "&&", send errors away with "2>$null" not "2>/dev/null", and put paths with spaces in quotes.`)
	}
	return "# Working on this computer\n\n- " + strings.Join(r, "\n- ") + "\n"
}

// writeCodeRules saves codeRules for OpenCode's instructions setting and
// returns the file's path. One file per kind of session, named by its rules,
// and written only when it changed: sessions running side by side never
// rewrite each other's file.
func writeCodeRules(home string, projects bool, mode int) (string, error) {
	ps := psShell(runtime.GOOS, os.Getenv("SHELL"))
	name := "rules" + map[bool]string{true: "-projects"}[projects] + map[bool]string{true: "-powershell"}[ps] +
		[]string{"-terminal", "-run", "-screen"}[mode] + ".md"
	path := filepath.Join(home, "opencode", name)
	text := codeRules(projects, ps, mode)
	if old, err := os.ReadFile(path); err == nil && string(old) == text {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(text), 0o644)
}

// psShell reports whether OpenCode runs commands in PowerShell: on Windows
// it uses SHELL when set, else pwsh or Windows PowerShell (OpenCode 1.18.34).
func psShell(goos, shell string) bool {
	s := strings.ToLower(shell)
	return goos == "windows" && (s == "" || strings.Contains(s, "powershell") || strings.Contains(s, "pwsh"))
}

// inProjects reports whether dir is the MangoMan Projects folder.
func inProjects(dir string) bool {
	a, err := os.Stat(dir)
	if err != nil {
		return false
	}
	b, err := os.Stat(ingress.ProjectsDir())
	return err == nil && os.SameFile(a, b)
}

// openCodeConfig is OpenCode's inline config that points it at MangoMan;
// mode is how OpenCode is shown and rules the file from writeCodeRules ("" for
// none). Measured on 8 Oct: switching off tools nobody used saved almost no
// tokens per request, so OpenCode keeps its own tools and prompts. Pruning
// (off in OpenCode by default) clears, from what is sent to the model, the
// output of tool calls older than the last two user turns, once a turn ends
// and only when that frees over 20,000 tokens; the newest 40,000 tokens of
// output and every loaded skill stay. A long session then stops resending
// files it read many steps ago; the coding screen still shows them all.
func openCodeConfig(cfg *config.Config, model string, mode int, rules string, projects bool) string {
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
		"$schema":    "https://opencode.ai/config.json",
		"compaction": map[string]bool{"prune": true},
	}
	if cfg.GetEngine() == config.EngineOpenCode {
		// OpenCode's own models only. OpenCode picks the model (the user's
		// last choice, else its default free one) unless one of its own was
		// asked for, so a free model OpenCode drops never breaks a session.
		c["enabled_providers"] = []string{"opencode"}
		if strings.HasPrefix(model, "opencode/") {
			c["model"] = model
		}
	} else {
		c["provider"] = map[string]any{"mangoman": map[string]any{
			"npm":  "@ai-sdk/openai-compatible",
			"name": "MangoMan (free models)",
			"options": map[string]any{
				"baseURL": fmt.Sprintf("http://127.0.0.1:%d/v1", cfg.Port),
				"apiKey":  "{env:MANGOMAN_TOKEN}",
			},
			"models": models,
		}}
		c["model"] = "mangoman/" + model
		c["small_model"] = "mangoman/free/fast"
	}
	bash := map[string]string{"*": "allow"}
	switch {
	case mode == modeUI && projects:
		for _, p := range projectsAsk {
			bash[p] = "ask"
		}
	case mode == modeUI:
		// In a developer's own folder the coding screen asks before any
		// command that could change something (installs, deletes, scripts);
		// the user can allow a kind of command for the rest of the session.
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
	// Community skills' scripts were written by other people: the user is
	// asked before one runs, and an unattended run never runs one. Best
	// effort, as above (a command that first changes into the folder is not
	// seen). The path is the real skills folder (OpenCode reads \ as /), and
	// "**" sorts these keys before the deny keys above, so a deny still wins.
	idx, ierr := skills.Community()
	cd, cerr := config.Dir()
	if ierr == nil && cerr == nil {
		rule := "ask"
		if mode == modeRun {
			rule = "deny"
		}
		for _, src := range idx.Sources {
			bash["**"+filepath.Join(cd, "opencode", "skills", src.ID+"-")+"*"] = rule
		}
	}
	c["permission"] = map[string]any{"bash": bash}
	if rules != "" {
		c["instructions"] = []string{rules}
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
	p, err := opencode.Install(home, nil)
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
	noBrowser := fs.Bool("no-browser", false, "with --ui: start the workspace but leave opening the screen to the dashboard")
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
	// The screens watch the engine MangoMan runs with; a session on another
	// one would be restarted as stalled.
	if running := runningEngine(cfg); running != cfg.GetEngine() {
		return fmt.Errorf("MangoMan is running on the %s engine and this session asks for %s: restart MangoMan with the same setting", running, cfg.GetEngine())
	}

	// Skill scripts that ask models themselves (the Council) reach the
	// router with the session's own token, or on OpenCode's own models (the
	// engine) the OpenCode program.
	env := append(os.Environ(), "MANGOMAN_TOKEN="+token, fmt.Sprintf("MANGOMAN_URL=http://127.0.0.1:%d/v1", cfg.Port),
		"MANGOMAN_ENGINE="+cfg.GetEngine(), "MANGOMAN_OPENCODE="+oc)
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
	wd, _ := os.Getwd()
	projects := inProjects(wd)
	rules, err := writeCodeRules(home, projects, mode)
	if err != nil {
		fmt.Println("Working rules not saved:", err)
		rules = ""
	}
	env = append(env, "OPENCODE_CONFIG_CONTENT="+openCodeConfig(cfg, *model, mode, rules, projects))
	ocOnly := cfg.GetEngine() == config.EngineOpenCode
	if ocOnly && mode == modeRun {
		// OpenCode waits for hours, silently, when its free messages run out;
		// its error lines are how a run sees that (opencode.FreeLimitLine).
		env = append(env, "OPENCODE_PRINT_LOGS=1", "OPENCODE_LOG_LEVEL=ERROR")
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
	on := "MangoMan free models (" + *model + ")"
	if ocOnly {
		on = "OpenCode's own free models"
	}
	fmt.Printf("Opening OpenCode on %s. Web search %s. Skill packs: %s.\n", on, map[bool]string{true: "off", false: "on"}[*noWeb], packs)
	if packs != "none" {
		// Checked before a pack task can start, so a missing Python is a plain
		// message here instead of a failed step in the middle of a task.
		if err := pyenv.Check(home); err != nil {
			fmt.Println("Skill packs cannot run yet:", err)
		}
	}

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
		return openWorkspace(cfg, oc, env, workDir, shipDir, !*noBrowser)
	}
	// Ctrl-C belongs to OpenCode while it runs. A terminate or hang-up
	// (closing the terminal) is passed on, so OpenCode ends and the router
	// started above is stopped by the deferred cleanup instead of lingering.
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(stop)
	ocArgs := fs.Args()
	for resumes := 0; ; resumes++ {
		var tail tailWriter
		cmd := exec.Command(oc, ocArgs...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = os.Stdin, os.Stdout, os.Stderr, env
		if mode == modeRun {
			// Only a run is watched for a cut-off answer: OpenCode's own
			// screen needs the real terminal.
			cmd.Stdout, cmd.Stderr = io.MultiWriter(os.Stdout, &tail), io.MultiWriter(os.Stderr, &tail)
			// A command OpenCode left running would hold the output open
			// after OpenCode ends; do not wait for it.
			cmd.WaitDelay = 5 * time.Second
		}
		cmd.Dir = workDir
		if err := cmd.Start(); err != nil {
			return err
		}
		done := make(chan struct{})
		go func() {
			select {
			case sig := <-stop:
				_ = cmd.Process.Signal(sig)
			case <-done:
			}
		}()
		var stalled, limited atomic.Bool
		switch {
		case mode == modeRun && ocOnly:
			go watchOpenCode(&tail, cmd.Process, done, &stalled, &limited)
		case mode == modeRun:
			go watchStall(cfg, cmd.Process, done, &stalled)
		}
		err = cmd.Wait()
		close(done)
		if errors.Is(err, exec.ErrWaitDelay) {
			err = nil // OpenCode ended; only something it started still holds the output
		}
		// Also a limit OpenCode logged just before it ended, between checks.
		if limited.Load() || (ocOnly && mode == modeRun && opencode.FreeLimitLine.MatchString(tail.String())) {
			return opencode.FreeLimit(time.Now())
		}
		// Measured on real free models: a model that goes quiet in the middle
		// of an answer is cut off by the router, and OpenCode then ends the
		// whole task; and once OpenCode sat for 17 minutes doing nothing at
		// all. Nobody is watching a run, so pick the task up again in the
		// same session, twice at most.
		if mode == modeRun && stalled.Load() && resumes >= 2 {
			return errors.New("the task stalled three times; the free models may be overloaded, try again in a few minutes")
		}
		if mode == modeRun && resumes < 2 {
			switch {
			case stalled.Load() && ocOnly:
				fmt.Printf("\nNothing happened for %d minutes; continuing the task.\n", int(openCodeStall.Minutes()))
				ocArgs = resumeArgs(fs.Args())
				continue
			case stalled.Load():
				fmt.Println("\nNothing happened for a minute; continuing the task.")
				ocArgs = resumeArgs(fs.Args())
				continue
			case strings.Contains(tail.String(), "upstream_stream_error"):
				fmt.Println("\nThe model's answer was cut off; continuing the task.")
				ocArgs = resumeArgs(fs.Args())
				continue
			}
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil // OpenCode reported its own error
		}
		return err
	}
}

// resumeNote is the message that continues a run that was cut off or stalled.
const resumeNote = "Your last step was interrupted (the connection dropped or the work stalled). Continue the task from where you stopped."

// stallAfter is how long a run may go with nothing happening at all: no
// model request in flight, none begun or ended, no command running. Between
// steps that normally lasts a few seconds.
const stallAfter = time.Minute

// commandCeiling is the longest a running command keeps a run from counting
// as stalled. OpenCode ends its own commands within 10 minutes; past that a
// child process is hung (once seen: a download that never finished).
const commandCeiling = 10 * time.Minute

// watchStall stops OpenCode when nothing has happened for stallAfter, and
// says so in stalled. Slow answers and long commands are not stalls: a
// request in flight or a command running keeps it waiting.
func watchStall(cfg *config.Config, p *os.Process, done <-chan struct{}, stalled *atomic.Bool) {
	started := time.Now()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
		}
		if time.Since(started) < stallAfter {
			continue
		}
		out, err := localSend(cfg, http.MethodGet, "/mangoman/busy", nil)
		var b struct {
			InFlight int64 `json:"in_flight"`
			IdleS    int   `json:"idle_s"`
		}
		if err != nil || json.Unmarshal(out, &b) != nil {
			continue // router restarting, or older than this command
		}
		idle := time.Duration(b.IdleS) * time.Second
		if b.InFlight > 0 || idle < stallAfter || (idle < commandCeiling && commandRunning(p.Pid)) {
			continue
		}
		stalled.Store(true)
		_ = p.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = p.Kill()
		}
		return
	}
}

// runningEngine is the engine the running MangoMan uses: its busy report
// names it only for OpenCode's own models.
func runningEngine(cfg *config.Config) string {
	out, err := localSend(cfg, http.MethodGet, "/mangoman/busy", nil)
	var b struct {
		Engine string `json:"engine"`
	}
	if err != nil || json.Unmarshal(out, &b) != nil || b.Engine != config.EngineOpenCode {
		return config.EngineSwitch
	}
	return config.EngineOpenCode
}

// openCodeStall is how long a run on OpenCode's own models may print
// nothing, with no command running, before it counts as stalled. MangoMan
// sees none of those requests, so OpenCode's output is the only sign of
// work; it prints a step when the step ends, which on a slow free model
// takes minutes.
const openCodeStall = 5 * time.Minute

// watchOpenCode is watchStall for a run on OpenCode's own models: it stops
// OpenCode at once when the free daily limit shows (OpenCode would wait
// for hours), and when nothing has been printed for openCodeStall with no
// command running.
func watchOpenCode(tail *tailWriter, p *os.Process, done <-chan struct{}, stalled, limited *atomic.Bool) {
	started := time.Now()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
		}
		last := tail.Last()
		if last.Before(started) {
			last = started // nothing printed yet: quiet since the run began
		}
		quiet := time.Since(last)
		switch {
		case opencode.FreeLimitLine.MatchString(tail.String()):
			limited.Store(true)
		case quiet >= openCodeStall && (quiet >= commandCeiling || !commandRunning(p.Pid)):
			stalled.Store(true)
		default:
			continue
		}
		_ = p.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = p.Kill()
		}
		return
	}
}

// commandRunning reports whether OpenCode is running a command: its bash
// tool runs each one as a direct child (a shell, or the program itself when
// the shell hands over to it). Language servers OpenCode keeps running for
// code files are children too, but are not commands. Without pgrep
// (Windows) it cannot tell, so it says yes and the watchdog never stops a
// task. A command that hangs is ended by OpenCode's own command time limit.
func commandRunning(pid int) bool {
	// Full command lines: a language server started as "node .../pyright"
	// shows only as node by name. Linux pgrep takes -a; on macOS -a means
	// "include ancestors" and prints bare numbers, so it takes -lf.
	full := "-a"
	if runtime.GOOS == "darwin" {
		full = "-lf"
	}
	out, err := exec.Command("pgrep", full, "-P", strconv.Itoa(pid)).Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false // no children
	}
	if err != nil {
		return true
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if _, cmdline, ok := strings.Cut(line, " "); ok && !languageServer(cmdline) {
			return true
		}
	}
	return false
}

// languageServer reports whether a command line is one of the language
// servers OpenCode starts for code files.
func languageServer(cmdline string) bool {
	for _, s := range []string{"language-server", "languageserver", "langserver", "-lsp", "pyright", "gopls", "rust-analyzer", "clangd"} {
		if strings.Contains(cmdline, s) {
			return true
		}
	}
	return false
}

// resumeArgs turns the arguments of an "opencode run" into ones that
// continue its last session: the same options, the note instead of the task.
func resumeArgs(args []string) []string {
	out := []string{"run", "--continue"}
	valued := map[string]bool{"--dir": true, "-m": true, "--model": true, "--agent": true, "--variant": true}
	for i := 1; i < len(args); i++ {
		switch a := args[i]; {
		case valued[a] && i+1 < len(args):
			out = append(out, a, args[i+1])
			i++
		case a == "--auto":
			out = append(out, a)
		}
	}
	return append(out, resumeNote)
}

// tailWriter keeps the last few kilobytes written to it, from OpenCode's
// output and error streams at once.
type tailWriter struct {
	mu   sync.Mutex
	b    []byte
	last time.Time // when output last came
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = time.Now()
	t.b = append(t.b, p...)
	if len(t.b) > 8<<10 {
		t.b = t.b[len(t.b)-8<<10:]
	}
	return len(p), nil
}

// Last is when output last came (the zero time before any).
func (t *tailWriter) Last() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last
}

func (t *tailWriter) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}
