package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bulkhead-labs/tailsuite/internal/project"
)

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func startTestUI(t *testing.T, holder, browser, noRedirect bool, stdin string) (*uiRun, *lockedBuf, []string) {
	t.Helper()
	dir := initProject(t)
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var opened []string
	var mu sync.Mutex
	old := openBrowser
	openBrowser = func(_ context.Context, target string) error {
		mu.Lock()
		opened = append(opened, target)
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() { openBrowser = old })
	out := &lockedBuf{}
	var in io.Reader // nil: no terminal
	if stdin != "" {
		in = strings.NewReader(stdin)
	}
	u, err := startUI(context.Background(), dir, p, holder, browser, noRedirect, in, out)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.close)
	mu.Lock()
	defer mu.Unlock()
	return u, out, opened
}

func TestUIForAHolder(t *testing.T) {
	u, out, opened := startTestUI(t, true, true, false, "")
	if u.redirect == "" || len(opened) != 0 || !uiLink.MatchString(out.String()) {
		t.Errorf("holder: redirect %q, opened %q, out %q", u.redirect, opened, out.String())
	}
	path := u.redirect
	u.close()
	if _, err := os.Stat(path); err == nil {
		t.Error("close left the redirect file")
	}
	for _, c := range []struct{ browser, noRedirect bool }{{false, false}, {true, true}} {
		if u, _, _ := startTestUI(t, true, c.browser, c.noRedirect, ""); u.redirect != "" {
			t.Errorf("browser %v noRedirect %v: a holder wrote a redirect file nothing will open", c.browser, c.noRedirect)
		}
	}
}

func TestUIWithoutABrowserPrintsTheLink(t *testing.T) {
	_, out, opened := startTestUI(t, false, false, false, "")
	if len(opened) != 0 || !uiLink.MatchString(out.String()) {
		t.Errorf("opened %q, out %q", opened, out.String())
	}
}

func TestUIPrintsTheLinkWhenTheBrowserDoesntTradeIt(t *testing.T) {
	old := linkGrace
	linkGrace = 50 * time.Millisecond
	t.Cleanup(func() { linkGrace = old })
	_, out, opened := startTestUI(t, false, true, false, "")
	if len(opened) != 1 || !strings.HasSuffix(opened[0], "open.html") {
		t.Fatalf("opened %q, want the redirect file", opened)
	}
	if uiLink.MatchString(out.String()) {
		t.Error("the link was printed before the grace ran out")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "hasn't opened the UI yet") {
		if time.Now().After(deadline) {
			t.Fatalf("no link after the grace: %q", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !uiLink.MatchString(out.String()) || !strings.Contains(out.String(), "--no-redirect-file") {
		t.Errorf("out %q", out.String())
	}
	_, _, opened2 := startTestUI(t, false, true, true, "")
	if len(opened2) != 1 || !uiLink.MatchString(opened2[0]) {
		t.Errorf("--no-redirect-file opened %q, want the link", opened2)
	}
}

func TestUIEnterPrintsAFreshLink(t *testing.T) {
	_, out, _ := startTestUI(t, false, false, false, "\n\n")
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(out.String(), "A fresh one-time link") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("out %q", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if links := uiLink.FindAllString(out.String(), -1); len(links) != 3 || links[0] == links[1] || links[1] == links[2] {
		t.Errorf("links %q: want three different ones", links)
	}
}

func TestBrowserAllowed(t *testing.T) {
	t.Setenv("CI", "")
	if !browserAllowed(false) || browserAllowed(true) {
		t.Error("outside CI")
	}
	t.Setenv("CI", "true")
	if browserAllowed(false) {
		t.Error("in CI")
	}
}
