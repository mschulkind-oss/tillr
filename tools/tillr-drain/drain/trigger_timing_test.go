package drain

import (
	"testing"
	"time"
)

func TestStorePollIntervalDefaultAndOverride(t *testing.T) {
	_, l := passFixture(t)
	if l.cfg.StorePollInterval != DefaultFallbackPollInterval {
		t.Fatalf("default store poll interval = %s, want %s", l.cfg.StorePollInterval, DefaultFallbackPollInterval)
	}
	if DefaultFallbackPollInterval != 15*time.Second {
		t.Fatalf("DefaultFallbackPollInterval = %s, want 15s", DefaultFallbackPollInterval)
	}
	cfg := l.cfg
	cfg.StorePollInterval = time.Millisecond
	short, err := NewLoop(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if short.cfg.StorePollInterval != time.Millisecond {
		t.Fatalf("injected interval = %s, want 1ms", short.cfg.StorePollInterval)
	}
	cfg.StorePollInterval = -time.Millisecond
	if _, err := NewLoop(cfg); err == nil {
		t.Fatal("negative store poll interval accepted")
	}
}
