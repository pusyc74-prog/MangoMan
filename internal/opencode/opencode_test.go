package opencode

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func archive(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if filepath.Ext(name) == ".zip" {
		w := zip.NewWriter(&buf)
		f, _ := w.Create(exeName())
		f.Write([]byte("program"))
		w.Close()
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "opencode", Mode: 0o755, Size: 7, Typeflag: tar.TypeReg})
	tw.Write([]byte("program"))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestInstallThenFind(t *testing.T) {
	name, err := asset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+name {
			http.NotFound(w, r)
			return
		}
		w.Write(archive(t, name))
	}))
	defer srv.Close()
	old := Releases
	Releases = srv.URL + "/"
	defer func() { Releases = old }()

	dir := t.TempDir()
	t.Setenv("PATH", t.TempDir()) // no OpenCode on the PATH
	if _, err := Path(dir); err == nil {
		t.Fatal("nothing installed yet")
	}
	p, err := Install(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "program" {
		t.Fatalf("got %q", b)
	}
	if got, err := Path(dir); err != nil || got != p {
		t.Fatalf("Path = %q, %v", got, err)
	}
}

func TestAssetNames(t *testing.T) {
	for _, c := range []struct{ os, arch, want string }{
		{"linux", "amd64", "opencode-linux-x64.tar.gz"},
		{"darwin", "arm64", "opencode-darwin-arm64.zip"},
		{"windows", "amd64", "opencode-windows-x64.zip"},
	} {
		if got, err := asset(c.os, c.arch); err != nil || got != c.want {
			t.Errorf("%s/%s: %q %v", c.os, c.arch, got, err)
		}
	}
	if _, err := asset("freebsd", "amd64"); err == nil {
		t.Error("freebsd has no download")
	}
}

func TestBadDownloads(t *testing.T) {
	if _, err := extract("x.tar.gz", []byte("not gzip")); err == nil {
		t.Error("garbage should fail")
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("readme.txt")
	f.Write([]byte("x"))
	w.Close()
	if _, err := extract("x.zip", buf.Bytes()); err == nil {
		t.Error("an archive without the program should fail")
	}
}
