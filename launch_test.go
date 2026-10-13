package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestMagnetLaunchArguments(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:" + strings.Repeat("1", 40) + "&dn=Sample%20file&tr=http%3A%2F%2F127.0.0.1%3A8080%2Fannounce"
	for _, args := range [][]string{{magnet}, {"--magnet", magnet}, {"--magnet=" + magnet}, {"--magnet", "  " + magnet + "  "}} {
		got, err := magnetFromArgs(args)
		if err != nil || got != magnet {
			t.Fatalf("%q: %q, %v", args, got, err)
		}
	}
	if got, err := magnetFromArgs(nil); err != nil || got != "" {
		t.Fatalf("normal launch: %q, %v", got, err)
	}
	for _, args := range [][]string{
		{"--magnet"}, {"--magnet", ""}, {"https://example.com/file"},
		{"--magnet", magnet, "--register-magnet"}, {"--register-magnet", magnet},
		{"magnet:?bad=%zz"}, {"--magnet", "file:///C:/unknown"},
	} {
		if _, err := magnetFromArgs(args); err == nil {
			t.Fatalf("accepted unexpected protocol arguments %q", args)
		}
	}
}

func TestMagnetInboxQueuesStartupAndForwardsConcurrentLinks(t *testing.T) {
	var inbox magnetInbox
	var mu sync.Mutex
	received := make(map[string]int)
	handler := func(source string) { mu.Lock(); received[source]++; mu.Unlock() }
	inbox.add("initial")
	inbox.add("during-startup")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); inbox.add(fmt.Sprintf("link-%d", i)) }(i)
	}
	inbox.start(handler)
	wg.Wait()
	inbox.add("already-running")
	inbox.close()
	inbox.add("after-shutdown")
	inbox.start(handler)
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 103 {
		t.Fatalf("lost or accepted an extra launch: %d", len(received))
	}
	for source, count := range received {
		if count != 1 {
			t.Fatalf("%s delivered %d times", source, count)
		}
	}
}

func TestMagnetInboxCloseBeforeStartup(t *testing.T) {
	var inbox magnetInbox
	inbox.add("queued")
	inbox.close()
	inbox.start(func(string) { t.Fatal("delivered after close") })
	inbox.add("new")
}

func TestUnregisterCommandRunsOnlyWithExactArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"--magnet", "magnet:?xt=urn:btih:example"}, {"--unregister-magnet"}} {
		called := false
		handled, err := handleUnregisterCommand(args, func() error { called = true; return nil })
		if handled || called || err != nil {
			t.Fatalf("unexpected maintenance call for %q", args)
		}
	}
	called := false
	handled, err := handleUnregisterCommand([]string{"--unregister", "extra"}, func() error { called = true; return nil })
	if !handled || err == nil || called {
		t.Fatal("invalid arguments executed cleanup")
	}
	failure := errors.New("registry cleanup failed")
	handled, err = handleUnregisterCommand([]string{"--unregister"}, func() error { called = true; return failure })
	if !handled || !called || !errors.Is(err, failure) {
		t.Fatal("cleanup error was hidden")
	}
	handled, err = handleUnregisterCommand([]string{"--unregister"}, func() error { return nil })
	if !handled || err != nil {
		t.Fatalf("valid unregister failed: %v", err)
	}
}
