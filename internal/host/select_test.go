package host

import (
	"runtime"
	"testing"

	"github.com/DnzzL/herdr-automations/internal/config"
)

func TestForPicksTheNamedBackend(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("every backend is hc on Windows; see TestOnWindowsEveryBackendResolvesToHC")
	}
	if _, ok := For(config.BackendHC).(*hcHost); !ok {
		t.Fatal("backend: hc did not get the hc host")
	}
	if _, ok := For(config.BackendHerdr).(*live); !ok {
		t.Fatal("backend: herdr did not get the herdr host")
	}
}
