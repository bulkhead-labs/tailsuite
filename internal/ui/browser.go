package ui

import (
	"context"
	"errors"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// redirect is the file the browser opens instead of the link, so the
// link's secret never appears on a command line (0017), as Jupyter
// Server's redirect file does.
var redirect = template.Must(template.New("redirect").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta http-equiv="refresh" content="0;url={{.}}">
<title>tailsuite</title></head>
<body><p>Opening the tailsuite lab. If nothing happens, <a href="{{.}}">open it here</a>.</p></body></html>
`))

// WriteRedirect writes a redirect file for a link into a new folder of
// its own, readable only by this user, in the runtime directory. remove
// deletes it.
func WriteRedirect(link string) (path string, remove func(), err error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "tailsuite-ui-") // mode 0700
	if err != nil {
		return "", nil, err
	}
	remove = func() { _ = os.RemoveAll(dir) } //nolint:gosec // G703: the folder MkdirTemp just made
	var b strings.Builder
	if err := redirect.Execute(&b, template.URL(link)); err != nil { //nolint:gosec // G203: the link is the server's own, http://127.0.0.1:<port>/#<secret>
		remove()
		return "", nil, err
	}
	path = filepath.Join(dir, "open.html")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil { //nolint:gosec // G703: a file in the folder MkdirTemp just made
		remove()
		return "", nil, err
	}
	return path, remove, nil
}

// ErrNoBrowser means there's nothing to open a browser with.
var ErrNoBrowser = errors.New("no browser to open")

// OpenBrowser opens target, a file path or a URL, in the user's browser,
// without waiting for it. The opener runs in a session of its own and
// outlives ctx, so the Ctrl-C that ends a lab doesn't close the browser
// it opened.
func OpenBrowser(_ context.Context, target string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		name = "xdg-open"
	}
	if _, err := exec.LookPath(name); err != nil {
		return ErrNoBrowser
	}
	cmd := exec.Command(name, append(args, target)...) //nolint:gosec,noctx // G204: the system's opener, with our own file or link; it must outlive up
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
