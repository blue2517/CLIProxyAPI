package proxyhealth

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
)

// Status represents the proxy health state.
type Status int

const (
	StatusHealthy  Status = iota
	StatusProbing  Status = iota
	StatusDisabled Status = iota
)

func (s Status) String() string {
	switch s {
	case StatusHealthy:
		return "healthy"
	case StatusProbing:
		return "probing"
	case StatusDisabled:
		return "disabled"
	default:
		return "unknown"
	}
}

// State is a snapshot of the monitor's current status for display purposes.
type State struct {
	Status       Status
	ProxyURL     string
	BackoffLevel int
	DisabledAt   time.Time
	NextProbeAt  time.Time
	LastError    string
	LastProbeAt  time.Time
}

var backoffDurations = []time.Duration{
	1 * time.Minute,
	10 * time.Minute,
	1 * time.Hour,
}

const (
	probeTimeout = 5 * time.Second
	probeTarget  = "api.anthropic.com:443"
)

// Monitor tracks the health of the global proxy URL.
type Monitor struct {
	mu           sync.Mutex
	proxyURL     string
	status       Status
	backoffLevel int
	disabledAt   time.Time
	nextProbeAt  time.Time
	lastError    string
	lastProbeAt  time.Time
	timer        *time.Timer
	stopCh       chan struct{}
	stopped      bool
	noop         bool
}

// New creates a proxy health monitor. If proxyURL is empty, "direct", or "none",
// the monitor is a no-op (always reports healthy).
func New(proxyURL string) *Monitor {
	trimmed := strings.TrimSpace(proxyURL)
	m := &Monitor{
		proxyURL: trimmed,
		status:   StatusHealthy,
		stopCh:   make(chan struct{}),
	}
	if trimmed == "" || strings.EqualFold(trimmed, "direct") || strings.EqualFold(trimmed, "none") {
		m.noop = true
	}
	return m
}

// ShouldBypass returns true when the proxy is disabled and requests should go direct.
func (m *Monitor) ShouldBypass() bool {
	if m == nil || m.noop {
		return false
	}
	m.mu.Lock()
	bypassed := m.status == StatusDisabled
	m.mu.Unlock()
	return bypassed
}

// ReportNetworkError is called when a request fails with a network-level error.
// It triggers a health probe if the monitor is currently healthy (first failure detection).
func (m *Monitor) ReportNetworkError(errMsg string) {
	if m == nil || m.noop {
		return
	}
	if !IsProxyConnectivityErrorMsg(errMsg) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	if m.status != StatusHealthy {
		return
	}
	m.status = StatusProbing
	log.Infof("proxy health: network error detected (%s), probing proxy connectivity...", truncate(errMsg, 100))
	go m.probe()
}

// GetState returns a snapshot of the current health state.
func (m *Monitor) GetState() State {
	if m == nil {
		return State{Status: StatusHealthy}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return State{
		Status:       m.status,
		ProxyURL:     proxyutil.Redact(m.proxyURL),
		BackoffLevel: m.backoffLevel,
		DisabledAt:   m.disabledAt,
		NextProbeAt:  m.nextProbeAt,
		LastError:    m.lastError,
		LastProbeAt:  m.lastProbeAt,
	}
}

// UpdateProxyURL updates the monitored proxy URL. Resets state if the URL changed.
func (m *Monitor) UpdateProxyURL(proxyURL string) {
	if m == nil {
		return
	}
	trimmed := strings.TrimSpace(proxyURL)
	m.mu.Lock()
	defer m.mu.Unlock()
	if trimmed == m.proxyURL {
		return
	}
	m.proxyURL = trimmed
	m.noop = trimmed == "" || strings.EqualFold(trimmed, "direct") || strings.EqualFold(trimmed, "none")
	m.status = StatusHealthy
	m.backoffLevel = 0
	m.disabledAt = time.Time{}
	m.nextProbeAt = time.Time{}
	m.lastError = ""
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	log.Infof("proxy health: proxy URL updated, state reset to healthy")
}

// Stop shuts down the monitor and cancels pending probes.
func (m *Monitor) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	close(m.stopCh)
}

func (m *Monitor) probe() {
	m.mu.Lock()
	url := m.proxyURL
	m.mu.Unlock()

	err := dialThroughProxy(url, probeTarget, probeTimeout)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	m.lastProbeAt = time.Now()

	if err == nil {
		m.status = StatusHealthy
		m.backoffLevel = 0
		m.disabledAt = time.Time{}
		m.nextProbeAt = time.Time{}
		m.lastError = ""
		log.Infof("proxy health: probe succeeded, proxy is healthy")
		return
	}

	m.lastError = err.Error()
	m.status = StatusDisabled
	m.disabledAt = time.Now()

	level := m.backoffLevel
	if level >= len(backoffDurations) {
		level = len(backoffDurations) - 1
	}
	wait := backoffDurations[level]
	m.nextProbeAt = time.Now().Add(wait)

	if m.backoffLevel < len(backoffDurations)-1 {
		m.backoffLevel++
	}

	log.Warnf("proxy health: probe failed (%s), disabling proxy for %v (level %d)", truncate(err.Error(), 80), wait, level)
	m.scheduleReprobe(wait)
}

func (m *Monitor) scheduleReprobe(wait time.Duration) {
	if m.timer != nil {
		m.timer.Stop()
	}
	m.timer = time.AfterFunc(wait, func() {
		m.mu.Lock()
		if m.stopped {
			m.mu.Unlock()
			return
		}
		m.status = StatusProbing
		m.mu.Unlock()
		log.Infof("proxy health: re-probing proxy after backoff...")
		m.probe()
	})
}

func dialThroughProxy(proxyURL, target string, timeout time.Duration) error {
	dialer, _, err := proxyutil.BuildDialer(proxyURL)
	if err != nil {
		return err
	}
	if dialer == nil {
		conn, errDial := net.DialTimeout("tcp", target, timeout)
		if errDial != nil {
			return errDial
		}
		conn.Close()
		return nil
	}

	type dialResult struct {
		conn net.Conn
		err  error
	}
	ch := make(chan dialResult, 1)
	go func() {
		conn, errDial := dialer.Dial("tcp", target)
		ch <- dialResult{conn, errDial}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-ch:
		if res.err != nil {
			return res.err
		}
		res.conn.Close()
		return nil
	case <-timer.C:
		return net.ErrClosed
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
