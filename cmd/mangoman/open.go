package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/pyenv"
	"github.com/pusyc74-prog/mangoman/internal/setup"
)

// ensureConfig creates the config and local token on the first run.
func ensureConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if errors.Is(err, config.ErrNotInitialised) {
		cfg = &config.Config{Port: config.DefaultPort, Token: config.NewToken(), MaxAttempts: 6}
		err = config.Save(cfg)
	}
	return cfg, err
}

// cmdOpen is what a double-click does: it opens the setup page until
// MangoMan is set up, the dashboard after that. If the router is not
// running yet, it runs in this window until the window is closed, or, for
// the Mac app (app), in the background until Stop MangoMan on Settings.
func cmdOpen(app bool) error {
	cfg, err := ensureConfig()
	if err != nil {
		return err
	}
	home, err := config.Dir()
	if err != nil {
		return err
	}
	fmt.Println("Opening MangoMan in your browser. For commands: mangoman help.")
	open := func(connected int) {
		page := "/ui/"
		if connected == 0 || setup.NeedsReady(home) {
			page = "/ui/setup.html"
		}
		url := fmt.Sprintf("http://127.0.0.1:%d%s", cfg.Port, page)
		// The token travels in the URL fragment, which browsers never send
		// to a server or put in logs.
		if err := setup.OpenURL(url + "#token=" + cfg.Token); err != nil {
			fmt.Printf("Open this in your browser:\n  %s#token=%s\n", url, cfg.Token)
		}
	}
	if routerUp(cfg) {
		open(connectedNow(cfg))
		return nil
	}
	if app {
		return serveInBackground(cfg, home, open)
	}
	fmt.Println("Keep this window open while you use MangoMan; closing it stops MangoMan.")
	return serve(0, open)
}

// serveInBackground starts the router as its own process, logging to
// serve.log, waits until it answers, then opens the page and returns, so a
// click on the app opens no window and a second click only opens the page.
func serveInBackground(cfg *config.Config, home string, open func(int)) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(home, "serve.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	c := exec.Command(self, "serve")
	c.Stdout, c.Stderr = logf, logf
	background(c)
	if err := c.Start(); err != nil {
		return fmt.Errorf("could not start MangoMan: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- c.Wait() }()
	for i := 0; i < 300; i++ { // 30 s: the first start unlocks the key store
		select {
		case <-exited:
			return fmt.Errorf("MangoMan stopped while starting; see %s", logPath)
		case <-time.After(100 * time.Millisecond):
		}
		if routerUp(cfg) {
			open(connectedNow(cfg))
			return nil
		}
	}
	return fmt.Errorf("MangoMan did not start within 30 seconds; see %s", logPath)
}

// connectedNow asks the running router how many cloud providers it has.
func connectedNow(cfg *config.Config) int {
	var st struct {
		Providers []struct {
			Connected bool `json:"connected"`
		} `json:"providers"`
	}
	resp, err := localGet(cfg, "/mangoman/status")
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if json.NewDecoder(resp.Body).Decode(&st) != nil {
		return 0
	}
	n := 0
	for _, p := range st.Providers {
		if p.Connected {
			n++
		}
	}
	return n
}

// waitIfDoubleClicked keeps a Windows console open after an error, so the
// message can be read before the window closes. The Mac app has no window,
// so the error shows in a dialog instead.
func waitIfDoubleClicked(app bool, msg string) {
	if app && runtime.GOOS == "darwin" {
		_ = exec.Command("osascript", "-e", "on run argv", "-e", "display alert \"MangoMan could not start\" message (item 1 of argv)", "-e", "end run", msg).Run()
		return
	}
	if runtime.GOOS == "windows" && stdinIsTerminal() {
		fmt.Print("Press Enter to close this window.")
		_, _ = stdin.ReadString('\n')
	}
}

// cmdReady downloads what MangoMan needs on this computer, showing the
// sizes first.
func cmdReady(args []string) error {
	fs := flag.NewFlagSet("ready", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "download without asking")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := ensureConfig(); err != nil {
		return err
	}
	home, err := config.Dir()
	if err != nil {
		return err
	}
	parts := setup.ReadyParts(home)
	if len(parts) == 0 {
		if err := pyenv.Check(home); err != nil {
			return err
		}
		fmt.Println("MangoMan is ready on this computer.")
		return nil
	}
	var total int64
	fmt.Println("To get ready, MangoMan downloads:")
	for _, p := range parts {
		total += p.Bytes
		note := ""
		if p.Note != "" {
			note = " (" + p.Note + ")"
		}
		fmt.Printf("  %-32s %8s%s\n", p.Name, setup.MB(p.Bytes), note)
	}
	fmt.Printf("  %-32s %8s\n", "In all", setup.MB(total))
	if !*yes {
		if !stdinIsTerminal() {
			return errors.New("run mangoman ready --yes to download without being asked")
		}
		fmt.Print("Download now? On mobile data, this uses about that much of your data. [Y/n] ")
		line, _ := stdin.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "" && a != "y" && a != "yes" {
			return errors.New("nothing downloaded")
		}
	}
	// On a screen the percentage updates in place; in a log, every 10%.
	fi, _ := os.Stdout.Stat()
	step := int64(10)
	if fi != nil && fi.Mode()&os.ModeCharDevice != 0 {
		step = 1
	}
	last, pct := "", int64(-1)
	err = setup.Ready(home, func(what string, done, total int64) {
		if what != last {
			if last != "" && pct >= 0 {
				fmt.Println()
			}
			last, pct = what, -1
			if total == 0 {
				fmt.Println(what + "...")
			}
		}
		if total > 0 {
			if p := done * 100 / total / step * step; p != pct {
				pct = p
				fmt.Printf("\r%s: %d%% of %s", what, p, setup.MB(total))
			}
		}
	})
	if err != nil {
		return err
	}
	fmt.Println("MangoMan is ready on this computer.")
	return nil
}
