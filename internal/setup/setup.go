// Package setup is the guided first run: connect free providers one at a
// time (open the sign-up page, paste a key, check it, store it), detect a
// local Ollama, and finish with a summary. The same ConnectKey step is used
// by the CLI, the wizard and the dashboard.
package setup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/keys"
)

// Validator checks a key with its provider.
type Validator func(ctx context.Context, p catalogue.Provider, key string) error

// ConnectKey validates a key with the provider and stores it locally.
func ConnectKey(ctx context.Context, cat *catalogue.Catalogue, store keys.Store, validate Validator, providerID, key string) (catalogue.Provider, error) {
	p, ok := cat.Provider(providerID)
	if !ok {
		return p, fmt.Errorf("unknown provider %q", providerID)
	}
	if !p.NeedsKey {
		return p, fmt.Errorf("%s needs no key", p.Name)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return p, errors.New("empty key")
	}
	if validate != nil {
		vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := validate(vctx, p, key); err != nil {
			return p, err
		}
	}
	if err := store.Set(p.ID, key); err != nil {
		return p, fmt.Errorf("could not store key: %w", err)
	}
	return p, nil
}

// Order is the recommended connection order: fastest and most generous free
// tiers first. Providers not listed follow in catalogue order.
var Order = []string{"groq", "cerebras", "nvidia", "openrouter", "zen"}

// Wizard runs the interactive setup. Every side effect is a field, so tests
// can script it.
type Wizard struct {
	Cat        *catalogue.Catalogue
	Store      keys.Store
	Resolver   *keys.Resolver
	Validate   Validator
	In         *bufio.Reader
	Out        io.Writer
	ReadSecret func(prompt string) (string, error)
	OpenURL    func(url string) error
	// Ollama reports whether Ollama is running and how many models it has.
	Ollama func(ctx context.Context) (running bool, models int)
	// Pull downloads an Ollama model; nil when the ollama command is missing.
	Pull func(model string) error
}

// SmallModel is the Ollama model offered when none is installed.
const SmallModel = "qwen3:4b"

func (w *Wizard) say(format string, a ...any) { fmt.Fprintf(w.Out, format, a...) }

func (w *Wizard) ask(prompt string) string {
	w.say("%s", prompt)
	line, _ := w.In.ReadString('\n')
	return strings.ToLower(strings.TrimSpace(line))
}

// Result summarises a wizard run.
type Result struct {
	Connected []string // providers with a key at the end
	New       []string // connected during this run
	Ollama    int      // local models available
}

func (w *Wizard) ordered() []catalogue.Provider {
	var out []catalogue.Provider
	seen := map[string]bool{}
	for _, id := range Order {
		if p, ok := w.Cat.Provider(id); ok && p.NeedsKey {
			out = append(out, p)
			seen[id] = true
		}
	}
	for _, p := range w.Cat.AllProviders() {
		if p.NeedsKey && !seen[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

func (w *Wizard) modelCount(provider string) int {
	n := 0
	for _, m := range w.Cat.AllModels() {
		if m.Provider == provider && m.Free {
			n++
		}
	}
	return n
}

// Run walks through every provider. It returns early when the user types q.
func (w *Wizard) Run(ctx context.Context) Result {
	var res Result
	providers := w.ordered()
	w.say("MangoMan setup\n\n")
	w.say("Each provider gives you free AI models with its own free key. One key is\n")
	w.say("enough to start; each extra one adds more free capacity and fallbacks.\n")
	w.say("Keys are checked with the provider and stored in %s.\n", keys.Where(w.Store))
	w.say("They never go anywhere else.\n")

	quit := false
	for i, p := range providers {
		n := w.modelCount(p.ID)
		w.say("\n[%d/%d] %s: %d free %s\n", i+1, len(providers), p.Name, n, plural(n, "model", "models"))
		w.say("      Data policy: %s\n", p.Policy.Label())
		if k, src := w.Resolver.Get(p.ID); k != "" {
			w.say("      Connected (%s).\n", src)
			res.Connected = append(res.Connected, p.ID)
			continue
		}
		if quit {
			w.say("      Not connected.\n")
			continue
		}
		switch w.ask("      Press Enter to open the sign-up page, s to skip, q to finish: ") {
		case "s", "skip":
			continue
		case "q", "quit":
			quit = true
			continue
		}
		if w.OpenURL != nil && p.SignupURL != "" {
			if err := w.OpenURL(p.SignupURL); err != nil {
				w.say("      Open this page: %s\n", p.SignupURL)
			} else {
				w.say("      Opened %s\n", p.SignupURL)
			}
		}
		for attempt := 0; attempt < 3; attempt++ {
			key, err := w.ReadSecret("      Paste the key (or press Enter to skip): ")
			if err != nil || strings.TrimSpace(key) == "" {
				break
			}
			w.say("      Checking with %s... ", p.Name)
			if _, err := ConnectKey(ctx, w.Cat, w.Store, w.Validate, p.ID, key); err != nil {
				w.say("failed: %v\n", err)
				continue
			}
			w.Resolver.Forget(p.ID)
			w.say("ok, connected.\n")
			res.Connected = append(res.Connected, p.ID)
			res.New = append(res.New, p.ID)
			break
		}
	}

	res.Ollama = w.ollamaStep(ctx, quit)

	w.say("\nDone. %d of %d cloud providers connected", len(res.Connected), len(providers))
	if res.Ollama > 0 {
		w.say(", plus %d local Ollama models", res.Ollama)
	}
	w.say(".\n")
	if len(res.Connected) == 0 && res.Ollama == 0 {
		w.say("Nothing is connected yet. Run `mangoman setup` again when you have a key.\n")
		return res
	}
	w.say("\nNext:\n  mangoman serve       start the router\n  mangoman dashboard   see providers, models and usage\n")
	return res
}

func (w *Wizard) ollamaStep(ctx context.Context, quit bool) int {
	if w.Ollama == nil {
		return 0
	}
	w.say("\nLocal models (Ollama): no key, works offline, slower than the cloud.\n")
	running, n := w.Ollama(ctx)
	switch {
	case running && n > 0:
		w.say("      Ollama is running with %d models. They are used as the last-resort backstop.\n", n)
		return n
	case running && n == 0 && w.Pull != nil && !quit:
		if a := w.ask(fmt.Sprintf("      Ollama has no models. Download %s (about 2.5 GB)? [y/N] ", SmallModel)); a == "y" || a == "yes" {
			if err := w.Pull(SmallModel); err != nil {
				w.say("      Download failed: %v\n", err)
				return 0
			}
			w.say("      Downloaded %s.\n", SmallModel)
			return 1
		}
	case running:
		w.say("      Ollama is running but has no models. Run: ollama pull %s\n", SmallModel)
	default:
		w.say("      Not installed or not running. Optional: install from https://ollama.com/download\n")
		w.say("      then run: ollama pull %s\n", SmallModel)
	}
	return 0
}

// OpenURL opens a page in the default browser.
func OpenURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// OllamaPull returns a pull function when the ollama command exists.
func OllamaPull(out io.Writer) func(string) error {
	path, err := exec.LookPath("ollama")
	if err != nil {
		return nil
	}
	return func(model string) error {
		cmd := exec.Command(path, "pull", model)
		cmd.Stdout, cmd.Stderr = out, out
		return cmd.Run()
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
