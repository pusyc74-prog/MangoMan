package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/providers"
	"github.com/pusyc74-prog/mangoman/internal/setup"
)

func cmdSetup() error {
	if _, err := config.Load(); errors.Is(err, config.ErrNotInitialised) {
		cfg := &config.Config{Port: config.DefaultPort, Token: config.NewToken(), MaxAttempts: 6}
		if err := config.Save(cfg); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if !stdinIsTerminal() {
		return errors.New("setup is interactive: run it in a terminal (or use `mangoman keys add <provider> --stdin`)")
	}
	cat, err := catalogue.Seed()
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	client := providers.NewClient()
	w := &setup.Wizard{
		Cat: cat, Store: st, Resolver: keys.NewResolver(st, envMap(cat)),
		Validate:   client.ValidateKey,
		In:         stdin,
		Out:        os.Stdout,
		ReadSecret: readSecret,
		OpenURL:    setup.OpenURL,
		Ollama: func(ctx context.Context) (bool, int) {
			p, ok := cat.Provider("ollama")
			if !ok {
				return false, 0
			}
			ms, err := client.DiscoverOllama(ctx, p)
			if err != nil || ms == nil {
				// DiscoverOllama returns nil, nil when Ollama is not running.
				return err == nil && ms != nil, 0
			}
			return true, len(ms)
		},
		Pull: setup.OllamaPull(os.Stdout),
	}
	w.Run(context.Background())
	fmt.Println()
	return nil
}

func cmdDashboard() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := localGet(cfg, "/healthz")
	if err != nil {
		return fmt.Errorf("the router is not running on 127.0.0.1:%d: start it with `mangoman serve`", cfg.Port)
	}
	resp.Body.Close()
	page := fmt.Sprintf("http://127.0.0.1:%d/ui/", cfg.Port)
	// The token travels in the URL fragment, which browsers never send to a
	// server or put in logs; the page moves it to session storage at once.
	if err := setup.OpenURL(page + "#token=" + cfg.Token); err != nil {
		fmt.Printf("Open this in your browser:\n  %s#token=%s\n", page, cfg.Token)
		return nil
	}
	fmt.Println("Opened the dashboard:", page)
	return nil
}
