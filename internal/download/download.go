// Package download fetches a pinned file, keeps it only if its SHA-256
// matches the tested release, and takes one program out of a .zip or
// .tar.gz. OpenCode and uv are installed this way.
package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// client gives up on a site that does not answer; a slow download is fine
// as long as it keeps moving (see stall).
var client = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 20 * time.Second}).DialContext,
	TLSHandshakeTimeout:   20 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
}}

// stall is how long a download may receive nothing before it is given up.
var stall = time.Minute

// Fetch downloads url, at most max bytes, telling progress how far it got,
// and returns the data only if its SHA-256 is sum. progress may be nil.
func Fetch(url, sum string, max int64, progress func(done, total int64)) ([]byte, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: %s", resp.Status)
	}
	if progress == nil {
		progress = func(int64, int64) {}
	}
	timer := time.AfterFunc(stall, cancel)
	defer timer.Stop()
	data, err := io.ReadAll(&counter{r: io.LimitReader(resp.Body, max), total: resp.ContentLength,
		f: func(d, t int64) { timer.Reset(stall); progress(d, t) }})
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("the download stopped moving; check the internet connection and try again")
		}
		return nil, err
	}
	if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != sum {
		return nil, errors.New("the download does not match the tested release; not installing it")
	}
	return data, nil
}

type counter struct {
	r           io.Reader
	done, total int64
	f           func(done, total int64)
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.done += int64(n)
		c.f(c.done, c.total)
	}
	return n, err
}

// Program returns the program called name (or name.exe) from a downloaded
// .zip or .tar.gz archive, at most max bytes.
func Program(archive string, data []byte, name string, max int64) ([]byte, error) {
	want := func(path string) bool { b := filepath.Base(path); return b == name || b == name+".exe" }
	missing := fmt.Errorf("the download has no %s program", name)
	if strings.HasSuffix(archive, ".zip") {
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range z.File {
			if !f.FileInfo().IsDir() && want(f.Name) {
				r, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer r.Close()
				return io.ReadAll(io.LimitReader(r, max))
			}
		}
		return nil, missing
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, missing
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && want(h.Name) {
			return io.ReadAll(io.LimitReader(tr, max))
		}
	}
}

// Save writes a program to dst in one step, so a cut-off write never leaves
// half a program behind.
func Save(dst string, program []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".part"
	if err := os.WriteFile(tmp, program, 0o700); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
