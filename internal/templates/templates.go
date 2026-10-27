// Package templates holds the projects `tailsuite init` writes, embedded
// in the binary so nobody clones anything (decision 0014).
package templates

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// all: keeps the dotfiles, such as .gitignore.
//
//go:embed all:startup
var files embed.FS

// Names lists the templates.
func Names() []string {
	entries, _ := fs.ReadDir(files, ".")
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// Files returns a template's files by their path in the project.
func Files(name string) (map[string][]byte, error) {
	if !slices.Contains(Names(), name) {
		return nil, fmt.Errorf("no template %q (there's %s)", name, strings.Join(Names(), ", "))
	}
	out := map[string][]byte{}
	err := fs.WalkDir(files, name, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := files.ReadFile(p)
		out[strings.TrimPrefix(p, name+"/")] = b
		return err
	})
	return out, err
}

// ErrExists means a file Write would write is already there.
var ErrExists = errors.New("already exists")

// gitignore is merged rather than written: a new repo often has one.
const gitignore = ".gitignore"

// Write writes files into dir, creating it. It writes nothing if any of
// them exists already: a project is the user's, and init never overwrites
// it. An existing .gitignore gets the template's lines it lacks.
func Write(dir string, files map[string][]byte) error {
	var there []string
	for p := range files {
		if p == gitignore {
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, p)); err == nil {
			there = append(there, p)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if len(there) > 0 {
		slices.Sort(there)
		return fmt.Errorf("%s: %s %w; init writes nothing over a project", dir, strings.Join(there, ", "), ErrExists)
	}
	if b, ok := files[gitignore]; ok {
		merged, err := mergeLines(filepath.Join(dir, gitignore), b)
		if err != nil {
			return err
		}
		files[gitignore] = merged
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil { //nolint:gosec // G301: a project folder the user owns and commits
			return err
		}
		if err := os.WriteFile(full, files[p], 0o644); err != nil { //nolint:gosec // G306: project files are meant to be read and committed
			return err
		}
	}
	return nil
}

// mergeLines is an existing file with each non-comment line of add that it
// lacks appended, or add itself when there's no file.
func mergeLines(path string, add []byte) ([]byte, error) {
	have, err := os.ReadFile(path) //nolint:gosec // G304: the user's own project
	if errors.Is(err, fs.ErrNotExist) {
		return add, nil
	}
	if err != nil {
		return nil, err
	}
	out := string(have)
	lines := strings.Split(out, "\n")
	for _, l := range strings.Split(string(add), "\n") {
		if l == "" || strings.HasPrefix(l, "#") || slices.Contains(lines, l) {
			continue
		}
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += l + "\n"
	}
	return []byte(out), nil
}
