package ui

import (
	"os"
	"strings"
	"testing"
)

func TestWriteRedirect(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	link := "http://127.0.0.1:4242/#abc_DEF-123"
	path, remove, err := WriteRedirect(link)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("redirect file mode %v, want 0600", fi.Mode().Perm())
	}
	if di, _ := os.Stat(strings.TrimSuffix(path, "/open.html")); di.Mode().Perm() != 0o700 {
		t.Errorf("its folder's mode %v, want 0700", di.Mode().Perm())
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `content="0;url=`+link+`"`) || !strings.Contains(string(b), `href="`+link+`"`) {
		t.Errorf("redirect file:\n%s", b)
	}
	remove()
	if _, err := os.Stat(path); err == nil {
		t.Error("remove left the file")
	}
}
