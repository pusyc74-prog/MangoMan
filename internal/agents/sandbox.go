package agents

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

//go:embed guard/sitecustomize.py
var guardPy []byte

// safeEnv is the only environment an agent gets from the user's: anything
// else (keys, tokens, database addresses, proxies) stays out.
var safeEnv = regexp.MustCompile(`(?i)^(PATH|HOME|USERPROFILE|USER|USERNAME|LOGNAME|LANG|LANGUAGE|LC_[A-Z]+|TERM|TZ|SYSTEMROOT|WINDIR|COMSPEC|PATHEXT|PROGRAMFILES|PROGRAMFILES\(X86\)|LOCALAPPDATA|APPDATA|HOMEDRIVE|HOMEPATH|XDG_CACHE_HOME|PLAYWRIGHT_BROWSERS_PATH)$`)

// Command builds the sandboxed command that runs one of an installed agent's
// Python scripts: in workdir, with the guard loaded, with only safe
// environment variables, and (on Linux, for agents with no network) with no
// network at all.
func Command(agentsDir, name, workdir, script string, args []string) (*exec.Cmd, error) {
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("no agent named %q", name)
	}
	dir := filepath.Join(agentsDir, name)
	m, err := Load(dir)
	if err != nil {
		return nil, err
	}
	scripts := filepath.Join(dir, "scripts")
	path := filepath.Join(scripts, script)
	if !strings.HasSuffix(script, ".py") || strings.ContainsAny(script, `/\:`) || strings.HasPrefix(script, ".") {
		return nil, fmt.Errorf("%s is not one of %s's scripts", script, name)
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("%s has no script %s", name, script)
	}
	guard := filepath.Join(agentsDir, ".guard")
	if err := os.MkdirAll(guard, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(guard, "sitecustomize.py"), guardPy, 0o644); err != nil {
		return nil, err
	}
	work, err := filepath.Abs(workdir)
	if err != nil {
		return nil, err
	}
	tmp := filepath.Join(agentsDir, ".tmp", name)
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, err
	}
	env := []string{
		"PYTHONPATH=" + guard + string(os.PathListSeparator) + filepath.Join(dir, "scripts"),
		"PYTHONDONTWRITEBYTECODE=1",
		"PYTHONUTF8=1", // Windows: print ₹ (see pyenv.Use)
		"MANGOMAN_WORKDIR=" + work,
		"TMPDIR=" + tmp, "TEMP=" + tmp, "TMP=" + tmp,
		"MANGOMAN_AGENT_DIR=" + dir,
		"MANGOMAN_ALLOW_HOSTS=" + strings.Join(m.Permissions.Network, ","),
		"MANGOMAN_ALLOW_CMDS=" + strings.Join(m.Permissions.Commands, ","),
	}
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); safeEnv.MatchString(k) {
			env = append(env, kv)
		}
	}
	argv := append([]string{"python3", path}, args...)
	if len(m.Permissions.Network) == 0 && noNetwork() {
		argv = append([]string{"unshare", "--map-root-user", "--net", "--"}, argv...)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = work, env
	return cmd, nil
}

// noNetwork reports whether this Linux machine can start a process in a
// namespace without network (user namespaces are sometimes turned off).
func noNetwork() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	return exec.Command("unshare", "--map-root-user", "--net", "--", "true").Run() == nil
}
