package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/setup"
)

// openWorkspace runs OpenCode as a local server for the coding screen
// (mangoman code --ui), attaches it to the router and opens the screen in the
// browser. It runs until Ctrl-C or until OpenCode stops. dir is the folder to
// work in ("" = here); shipDir is the Guardian project to ship from.
func openWorkspace(cfg *config.Config, oc string, env []string, dir, shipDir string) error {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	dir, _ = filepath.Abs(dir)
	port, err := freePort()
	if err != nil {
		return err
	}
	// OpenCode's server would otherwise accept anyone on this computer; the
	// password stays between this process and the router.
	password := config.NewToken()
	home, _ := config.Dir()
	logPath := filepath.Join(home, "workspace.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()

	cmd := exec.Command(oc, "serve", "--port", strconv.Itoa(port), "--hostname", "127.0.0.1")
	cmd.Dir, cmd.Env = dir, append(env, "OPENCODE_SERVER_PASSWORD="+password)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitHealthy(base, password, exited); err != nil {
		return fmt.Errorf("%w; see %s", err, logPath)
	}
	body, _ := json.Marshal(map[string]string{"url": base, "password": password, "dir": dir, "ship_dir": shipDir})
	out, err := localSend(cfg, http.MethodPost, "/mangoman/code/attach", body)
	var att struct{ ID string }
	if err != nil || json.Unmarshal(out, &att) != nil {
		return fmt.Errorf("could not connect the coding screen to the router: %v", err)
	}
	defer func() { _, _ = localSend(cfg, http.MethodDelete, "/mangoman/code/attach?id="+att.ID, nil) }()

	page := fmt.Sprintf("http://127.0.0.1:%d/ui/code.html", cfg.Port)
	// The token travels in the URL fragment, which browsers never send to a
	// server or put in logs.
	if err := setup.OpenURL(page + "#token=" + cfg.Token); err != nil {
		fmt.Printf("Open this in your browser:\n  %s#token=%s\n", page, cfg.Token)
	} else {
		fmt.Println("Opened the coding screen:", page)
	}
	fmt.Printf("Working in %s. Press Ctrl-C here to close the workspace.\n", dir)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(stop)
	select {
	case <-stop:
		fmt.Println("\nClosing the workspace.")
		return nil
	case err := <-exited:
		return fmt.Errorf("OpenCode stopped (%v); see %s", err, logPath)
	}
}

// freePort asks the system for a port nobody is using on 127.0.0.1.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// waitHealthy waits up to 30 s for OpenCode's server to answer.
func waitHealthy(base, password string, exited <-chan error) error {
	c := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 60; i++ {
		select {
		case err := <-exited:
			return fmt.Errorf("OpenCode stopped while starting (%v)", err)
		default:
		}
		req, _ := http.NewRequest(http.MethodGet, base+"/global/health", nil)
		req.SetBasicAuth("opencode", password)
		if resp, err := c.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("OpenCode's server did not start")
}

// localSend calls the running router with the local token and returns the
// answer's body.
func localSend(cfg *config.Config, method, path string, body []byte) ([]byte, error) {
	req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", cfg.Port, path), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return out, fmt.Errorf("router answered HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(out))
	}
	return out, nil
}
