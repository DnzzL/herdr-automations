package host

import (
	"testing"

	"github.com/DnzzL/herdr-automations/internal/config"
)

// Windows has no Herdr TUI (ADR 0013): every backend line, auto included,
// runs on hc, without probing herdr-core first.
func TestOnWindowsEveryBackendResolvesToHC(t *testing.T) {
	for _, b := range []config.Backend{"", config.BackendAuto, config.BackendHerdr, config.BackendHC} {
		if got := Resolve(b); got != config.BackendHC {
			t.Errorf("Resolve(%q) = %q, want hc", b, got)
		}
	}
}
