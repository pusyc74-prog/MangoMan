// Package opencode finds the OpenCode coding agent, or downloads it into
// MangoMan's own folder, so a user who never heard of OpenCode can still
// run "mangoman code".
package opencode

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/download"
)

// Version is the OpenCode release MangoMan has tested. Moving it forward
// means testing the new release first, then updating Version and sums.
const Version = "1.18.34"

// Releases is where OpenCode publishes its builds (MIT licence).
var Releases = "https://github.com/anomalyco/opencode/releases/download/v" + Version + "/"

// sums are the SHA-256 of each download for Version.
var sums = map[string]string{
	"opencode-linux-x64.tar.gz":   "0f22479647226d1d2dd99595d20082ee7bda3870b62dc6a90b41efc1a71d7e9a",
	"opencode-linux-arm64.tar.gz": "bbdb3f00c2c51e42e315525233151309724226a8776da8e9145e3b0fa3d5310f",
	"opencode-darwin-x64.zip":     "66bf0638cffad3b65bd6648cc3947619e1dd71f4bfeee0a81e087ac036bb1088",
	"opencode-darwin-arm64.zip":   "8522b70f545184b3a8d97c5ca4f814093b2476d72aebfda8c48bcd072ec31d1b",
	"opencode-windows-x64.zip":    "8ec42ed1ad8db108052394b83ab69d0331398f761fbfff5fe50f91d65bdd3548",
}

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

// Outdated reports whether MangoMan's own copy (never one the user installed
// themselves) is not the tested Version.
func Outdated(dir string) bool {
	if _, err := exec.LookPath("opencode"); err == nil {
		return false
	}
	own := filepath.Join(dir, "tools", exeName())
	if _, err := os.Stat(own); err != nil {
		return false
	}
	v, _ := os.ReadFile(filepath.Join(dir, "tools", "opencode.version"))
	return strings.TrimSpace(string(v)) != Version
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
// progress may be nil.
func Install(dir string, progress func(done, total int64)) (string, error) {
	name, err := asset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	data, err := download.Fetch(Releases+name, sums[name], maxSize, progress)
	if err != nil {
		return "", err
	}
	bin, err := download.Program(name, data, "opencode", maxSize)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "tools", exeName())
	if err := download.Save(dst, bin); err != nil {
		return "", err
	}
	return dst, os.WriteFile(filepath.Join(dir, "tools", "opencode.version"), []byte(Version), 0o600)
}
