// Package opencode finds the OpenCode coding agent, or downloads it into
// MangoMan's own folder, so a user who never heard of OpenCode can still
// run "mangoman code".
package opencode

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Releases is where OpenCode publishes its builds (MIT licence).
var Releases = "https://github.com/anomalyco/opencode/releases/latest/download/"

const maxSize = 400 << 20 // the unpacked program is about 180 MB

func exeName() string {
	if runtime.GOOS == "windows" {
		return "opencode.exe"
	}
	return "opencode"
}

// Path returns OpenCode from the PATH, or MangoMan's own copy in dir.
func Path(dir string) (string, error) {
	if p, err := exec.LookPath("opencode"); err == nil {
		return p, nil
	}
	own := filepath.Join(dir, "tools", exeName())
	if _, err := os.Stat(own); err != nil {
		return "", errors.New("opencode not found")
	}
	return own, nil
}

// asset names the download for this computer.
func asset(goos, goarch string) (string, error) {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	switch {
	case arch == "":
		return "", fmt.Errorf("no OpenCode download for %s/%s", goos, goarch)
	case goos == "linux":
		return "opencode-linux-" + arch + ".tar.gz", nil
	case goos == "darwin" || goos == "windows":
		return "opencode-" + goos + "-" + arch + ".zip", nil
	}
	return "", fmt.Errorf("no OpenCode download for %s/%s", goos, goarch)
}

// Size asks how big the download is, in bytes (0 when unknown).
func Size() int64 {
	name, err := asset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return 0
	}
	c := http.Client{Timeout: 20 * time.Second}
	resp, err := c.Head(Releases + name)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return max(resp.ContentLength, 0)
}

// Install downloads OpenCode into dir/tools and returns the program's path.
func Install(dir string) (string, error) {
	name, err := asset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	resp, err := http.Get(Releases + name)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
	if err != nil {
		return "", err
	}
	bin, err := extract(name, data)
	if err != nil {
		return "", err
	}
	tools := filepath.Join(dir, "tools")
	if err := os.MkdirAll(tools, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(tools, exeName())
	tmp := dst + ".part"
	if err := os.WriteFile(tmp, bin, 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// extract returns the OpenCode program from a downloaded archive.
func extract(name string, data []byte) ([]byte, error) {
	if filepath.Ext(name) == ".zip" {
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range z.File {
			if b := filepath.Base(f.Name); !f.FileInfo().IsDir() && (b == "opencode.exe" || b == "opencode") {
				r, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer r.Close()
				return io.ReadAll(io.LimitReader(r, maxSize))
			}
		}
		return nil, errors.New("the download has no opencode program")
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("the download has no opencode program")
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(h.Name) == "opencode" && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, maxSize))
		}
	}
}
