package agents

import (
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

//go:embed guard/sitecustomize.py
var guardPy []byte

// secretEnv matches environment variables that must not reach an agent.
var secretEnv = regexp.MustCompile(`(?i)(KEY|TOKEN|SECRET|PASSWORD|PASSPHRASE|CREDENTIAL|AUTH)`)

// Command builds the sandboxed command for one of an installed agent's
// programs: it runs in workdir with the guard loaded, without secrets in its
// environment, and (on Linux, for agents with no network) with no network.
func Command(agentsDir, name, workdir string, argv []string) (*exec.Cmd, error) {
	dir := filepath.Join(agentsDir, name)
	m, err := Load(dir)
	if err != nil {
		return nil, err
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
		"MANGOMAN_WORKDIR=" + work,
		"TMPDIR=" + tmp, "TEMP=" + tmp, "TMP=" + tmp,
		"MANGOMAN_AGENT_DIR=" + dir,
		"MANGOMAN_ALLOW_HOSTS=" + strings.Join(m.Permissions.Network, ","),
		"MANGOMAN_ALLOW_CMDS=" + strings.Join(m.Permissions.Commands, ","),
	}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if secretEnv.MatchString(k) || strings.HasSuffix(strings.ToUpper(k), "_PROXY") || strings.HasPrefix(k, "PYTHON") || strings.HasPrefix(k, "MANGOMAN_") || k == "TMPDIR" || k == "TEMP" || k == "TMP" {
			continue
		}
		env = append(env, kv)
	}
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
