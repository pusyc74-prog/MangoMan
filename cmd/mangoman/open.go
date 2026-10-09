package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"runtime"
	"strings"

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
// running yet, it runs in this window until the window is closed.
func cmdOpen() error {
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
	fmt.Println("Keep this window open while you use MangoMan; closing it stops MangoMan.")
	return serve(0, open)
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
// message can be read before the window closes.
func waitIfDoubleClicked() {
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
			if p := done * 100 / total; p != pct {
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
