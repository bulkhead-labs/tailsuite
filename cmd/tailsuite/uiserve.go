package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bulkhead-labs/tailsuite/internal/project"
	"github.com/bulkhead-labs/tailsuite/internal/ui"
)

// linkGrace is how long up waits for the browser to trade the link before
// printing it (0017).
var linkGrace = 5 * time.Second

// browserAllowed is 0017's rule: never in CI, or with --no-browser.
func browserAllowed(noBrowser bool) bool { return !noBrowser && os.Getenv("CI") == "" }

// openBrowser opens the UI; tests replace it.
var openBrowser = ui.OpenBrowser

// isTerminal says whether f is a terminal, where Enter can be pressed.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// uiRun is the local UI a holding up serves (#11).
type uiRun struct {
	srv      *ui.Server
	redirect string // a redirect file for the browser, if one was written
	remove   func()
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex // serialises writes to out
	out      io.Writer
}

// startUI serves the UI for a lab that's up, and gets a browser to it:
//   - a detached holder logs the link and, when the up that started it
//     will open a browser on one, writes a redirect file for it;
//   - a foreground up opens the browser, on a redirect file unless
//     noRedirect, and prints the link if the page hasn't traded it within
//     linkGrace; with no browser, it prints the link at once. Enter, on a
//     terminal, prints a fresh one-time link.
func startUI(ctx context.Context, dir string, p *project.Project, holder, browser, noRedirect bool, stdin io.Reader, out io.Writer) (*uiRun, error) {
	srv, err := ui.Start(ui.Options{View: ui.NewView(p), Results: filepath.Join(dir, stateDir, "results.json")})
	if err != nil {
		return nil, err
	}
	u := &uiRun{srv: srv, remove: func() {}, done: make(chan struct{}), out: out}
	link, traded, err := srv.NewLink()
	if err != nil {
		u.close()
		return nil, err
	}
	switch {
	case holder:
		if browser && !noRedirect {
			path, remove, err := ui.WriteRedirect(link)
			if err != nil {
				u.close()
				return nil, err
			}
			u.redirect, u.remove = path, remove
		}
		u.printf("The local UI: %s (a one-time link)\n", link)
	case browser:
		target := link
		if !noRedirect {
			path, remove, err := ui.WriteRedirect(link)
			if err != nil {
				u.close()
				return nil, err
			}
			target, u.remove = path, remove
		}
		if err := openBrowser(ctx, target); err != nil {
			u.printf("The local UI: %s (a one-time link; there's no browser to open: %v)\n", link, err)
			break
		}
		u.printf("Opening the local UI in your browser.\n")
		grace := linkGrace
		go func() {
			select {
			case <-traded:
			case <-u.done:
			case <-time.After(grace):
				hint := "; it may not be able to read the redirect file (--no-redirect-file opens the link directly)"
				if noRedirect {
					hint = ""
				}
				u.printf("The browser hasn't opened the UI yet%s. Open this one-time link:\n  %s\n", hint, link)
			}
		}()
	default:
		u.printf("The local UI: %s (a one-time link)\n", link)
	}
	if !holder && stdin != nil {
		u.printf("Press Enter here for a fresh one-time link.\n")
		go u.freshLinks(stdin)
	}
	return u, nil
}

// freshLinks prints a fresh one-time link for every line read.
func (u *uiRun) freshLinks(stdin io.Reader) {
	sc := bufio.NewScanner(stdin)
	for sc.Scan() {
		select {
		case <-u.done:
			return
		default:
		}
		if link, _, err := u.srv.NewLink(); err == nil {
			u.printf("A fresh one-time link: %s\n", link)
		}
	}
}

func (u *uiRun) printf(format string, a ...any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	select {
	case <-u.done:
	default:
		_, _ = fmt.Fprintf(u.out, format, a...)
	}
}

// close stops the UI and removes the redirect file. It's idempotent.
func (u *uiRun) close() {
	u.once.Do(func() {
		u.mu.Lock()
		close(u.done)
		u.mu.Unlock()
		_ = u.srv.Close()
		u.remove()
	})
}
