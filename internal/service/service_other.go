//go:build !windows

package service

// The Run registry key exists only on Windows; service.go never calls these
// elsewhere.

func installRunValue(Spec) (string, error) { panic("unreachable") }
func uninstallRunValue() error             { panic("unreachable") }
func runValueExists() bool                 { panic("unreachable") }
