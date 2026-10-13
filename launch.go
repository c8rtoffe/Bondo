package main

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
)

const singleInstanceID = "bondo-759cc571-2e68-4be5-a55e-2a260c45c568"

// Only accept the expected protocol arguments. Browser data is never a command.
func magnetFromArgs(args []string) (string, error) {
	var source string
	switch len(args) {
	case 0:
		return "", nil
	case 1:
		source = strings.TrimPrefix(args[0], "--magnet=")
	case 2:
		if args[0] != "--magnet" {
			return "", fmt.Errorf("expected --magnet followed by a magnet link")
		}
		source = args[1]
	default:
		return "", fmt.Errorf("expected one magnet link")
	}
	source = strings.TrimSpace(source)
	u, err := url.Parse(source)
	if err != nil || !strings.EqualFold(u.Scheme, "magnet") || u.RawQuery == "" {
		return "", fmt.Errorf("expected a valid magnet link")
	}
	if _, err := url.ParseQuery(u.RawQuery); err != nil {
		return "", fmt.Errorf("invalid magnet link: %w", err)
	}
	return source, nil
}

// Links arriving during startup wait for the engine. Close prevents stale launches.
type magnetInbox struct {
	mu      sync.Mutex
	pending []string
	handler func(string)
	closed  bool
}

func (inbox *magnetInbox) add(source string) {
	if source == "" {
		return
	}
	inbox.mu.Lock()
	if inbox.closed {
		inbox.mu.Unlock()
		return
	}
	handler := inbox.handler
	if handler == nil {
		inbox.pending = append(inbox.pending, source)
	}
	inbox.mu.Unlock()
	if handler != nil {
		handler(source)
	}
}

func (inbox *magnetInbox) start(handler func(string)) {
	inbox.mu.Lock()
	if inbox.closed {
		inbox.mu.Unlock()
		return
	}
	inbox.handler = handler
	pending := inbox.pending
	inbox.pending = nil
	inbox.mu.Unlock()
	for _, source := range pending {
		handler(source)
	}
}

func (inbox *magnetInbox) close() {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	inbox.closed = true
	inbox.pending = nil
	inbox.handler = nil
}

// Run maintenance before Wails startup, including when another window is open.
func handleUnregisterCommand(args []string, unregister func() error) (bool, error) {
	if len(args) == 0 || args[0] != "--unregister" {
		return false, nil
	}
	if len(args) != 1 {
		return true, fmt.Errorf("usage: bondo.exe --unregister")
	}
	return true, unregister()
}
