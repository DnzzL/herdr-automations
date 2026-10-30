package host

import (
	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/hc"
)

// Resolve settles auto: herdr-core when it answers, else the TUI's server.
func Resolve(b config.Backend) config.Backend {
	if b != config.BackendAuto {
		return b
	}
	if hc.Available() {
		return config.BackendHC
	}
	return config.BackendHerdr
}

// For returns the Host for a backend, auto resolved.
func For(b config.Backend) Host {
	if Resolve(b) == config.BackendHC {
		return NewHC()
	}
	return New()
}
