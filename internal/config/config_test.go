package config

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentSavesLeaveValidFile(t *testing.T) {
	t.Setenv("MANGOMAN_HOME", t.TempDir())
	small := &Config{Port: 1, Token: "t"}
	big := &Config{Port: 1, Token: "t"}
	for i := 0; i < 200; i++ {
		big.Favorites = append(big.Favorites, fmt.Sprintf("model-%d", i))
	}
	for round := 0; round < 50; round++ {
		var wg sync.WaitGroup
		for _, c := range []*Config{big, small} {
			wg.Add(1)
			go func(c *Config) { defer wg.Done(); _ = Save(c) }(c)
		}
		wg.Wait()
		if _, err := Load(); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
}

// The engine: the switch unless "opencode" is set, in the config or (for
// tests) in MANGOMAN_ENGINE; anything else is the switch.
func TestEngine(t *testing.T) {
	t.Setenv("MANGOMAN_ENGINE", "")
	for set, want := range map[string]string{"": EngineSwitch, "switch": EngineSwitch, "opencode": EngineOpenCode, " OpenCode ": EngineOpenCode, "opencod": EngineSwitch} {
		if got := (&Config{Engine: set}).GetEngine(); got != want {
			t.Errorf("%q: %s, want %s", set, got, want)
		}
	}
	t.Setenv("MANGOMAN_ENGINE", "opencode")
	if (&Config{}).GetEngine() != EngineOpenCode {
		t.Fatal("MANGOMAN_ENGINE is not read")
	}
}
