package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestProgramFromArchives(t *testing.T) {
	var z bytes.Buffer
	zw := zip.NewWriter(&z)
	f, _ := zw.Create("uvx.exe")
	f.Write([]byte("other"))
	f, _ = zw.Create("uv.exe")
	f.Write([]byte("uv"))
	zw.Close()
	if b, err := Program("x.zip", z.Bytes(), "uv", 1<<20); err != nil || string(b) != "uv" {
		t.Fatalf("zip: %q %v", b, err)
	}
	var tg bytes.Buffer
	gz := gzip.NewWriter(&tg)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "uv-x86_64-unknown-linux-gnu/uv", Mode: 0o755, Size: 2, Typeflag: tar.TypeReg})
	tw.Write([]byte("uv"))
	tw.Close()
	gz.Close()
	if b, err := Program("x.tar.gz", tg.Bytes(), "uv", 1<<20); err != nil || string(b) != "uv" {
		t.Fatalf("tar: %q %v", b, err)
	}
	if _, err := Program("x.tar.gz", []byte("not gzip"), "uv", 1<<20); err == nil {
		t.Error("garbage should fail")
	}
	if _, err := Program("x.zip", z.Bytes(), "opencode", 1<<20); err == nil {
		t.Error("an archive without the program should fail")
	}
}

func TestFetchChecksAndCounts(t *testing.T) {
	body := []byte("the release")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	sum := sha256.Sum256(body)
	var done, total int64
	data, err := Fetch(srv.URL, hex.EncodeToString(sum[:]), 1<<20, func(d, t int64) { done, total = d, t })
	if err != nil || string(data) != "the release" || done != int64(len(body)) || total != int64(len(body)) {
		t.Fatalf("%q %v, progress %d of %d", data, err, done, total)
	}
	if _, err := Fetch(srv.URL, "bad", 1<<20, nil); err == nil {
		t.Fatal("a download that does not match must be refused")
	}
	dst := filepath.Join(t.TempDir(), "tools", "uv")
	if err := Save(dst, data); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "the release" {
		t.Fatalf("saved %q", b)
	}
}
