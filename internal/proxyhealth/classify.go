package proxyhealth

import (
	"context"
	"errors"
	"net"
	"strings"
)

// IsProxyConnectivityError returns true if the error indicates the proxy
// itself is unreachable (as opposed to an HTTP error from the upstream).
func IsProxyConnectivityError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Op == "dial" || opErr.Op == "read" || opErr.Op == "write" {
			return true
		}
	}

	msg := strings.ToLower(err.Error())
	indicators := []string{
		"connection refused",
		"i/o timeout",
		"no route to host",
		"network is unreachable",
		"socks",
		"proxy",
		"dial tcp",
		"connect: connection timed out",
	}
	for _, ind := range indicators {
		if strings.Contains(msg, ind) {
			return true
		}
	}
	return false
}

// IsProxyConnectivityErrorMsg is a convenience that classifies based on the
// error message string alone (used when only the message is available).
func IsProxyConnectivityErrorMsg(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)
	indicators := []string{
		"connection refused",
		"i/o timeout",
		"no route to host",
		"network is unreachable",
		"socks",
		"proxy",
		"dial tcp",
		"connect: connection timed out",
		"context deadline exceeded",
		"timeout",
	}
	for _, ind := range indicators {
		if strings.Contains(lower, ind) {
			return true
		}
	}
	return false
}
