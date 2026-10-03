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
