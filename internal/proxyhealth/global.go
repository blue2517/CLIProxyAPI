package proxyhealth

import (
	"sync/atomic"
)

var globalMonitor atomic.Pointer[Monitor]

// SetGlobal sets the package-level proxy health monitor.
func SetGlobal(m *Monitor) {
	globalMonitor.Store(m)
}

// Global returns the package-level proxy health monitor, or nil.
func Global() *Monitor {
	return globalMonitor.Load()
}

// GlobalShouldBypass returns true if the global proxy is currently disabled.
func GlobalShouldBypass() bool {
	m := globalMonitor.Load()
	return m != nil && m.ShouldBypass()
}
