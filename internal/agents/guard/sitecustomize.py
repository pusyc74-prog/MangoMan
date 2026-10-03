"""MangoMan agent guard: loaded into every Python process an agent starts.

It enforces what the agent's agent.json declares, through Python audit hooks
(which code cannot remove once added):
- network: only the declared hosts ("localhost" must be declared too);
- programs: only python and the declared commands (and Playwright's driver);
- files: writes only inside the work folder and the temp folder; secret
  folders (SSH, cloud and MangoMan keys, browser profiles) cannot be read.
On Linux, an agent with no network also runs in a namespace with no network
at all. This guard is one layer; the marketplace review is the other.
"""
import os
import socket
import sys
import tempfile


def _real(p):
    try:
        return os.path.realpath(os.fsdecode(p))
    except (TypeError, ValueError):
        return ""


def _under(p, roots):
    return any(p == r or p.startswith(r.rstrip(os.sep) + os.sep) for r in roots)


HOSTS = [h for h in os.environ.get("MANGOMAN_ALLOW_HOSTS", "").split(",") if h]
CMDS = {c for c in os.environ.get("MANGOMAN_ALLOW_CMDS", "").split(",") if c} | {"python", "python3", os.path.basename(sys.executable)}
WRITE_OK = [_real(os.environ.get("MANGOMAN_WORKDIR", os.getcwd())), _real(tempfile.gettempdir()), "/dev/null"]
_home = os.path.expanduser("~")
SECRET = [_real(os.path.join(_home, p)) for p in (
    ".ssh", ".aws", ".gnupg", ".netrc", ".docker", ".kube", ".azure", ".config/gcloud", ".config/mangoman",
    ".config/gh", ".git-credentials", ".local/share/keyrings", "Library/Keychains", "Library/Application Support/mangoman",
    ".config/google-chrome", ".config/chromium", ".mozilla", "Library/Application Support/Google/Chrome",
    "AppData/Roaming/mangoman", "AppData/Local/Google/Chrome")]
_ips = {"127.0.0.1", "::1"} if "localhost" in HOSTS else set()


def _host_ok(h):
    h = (h.decode() if isinstance(h, bytes) else str(h or "")).lower().rstrip(".")
    if h in ("127.0.0.1", "::1"):
        h = "localhost"
    return any(h == a or (a.startswith("*.") and h.endswith(a[1:])) for a in HOSTS)


_getaddrinfo = socket.getaddrinfo


def _recording_getaddrinfo(host, *a, **k):
    out = _getaddrinfo(host, *a, **k)
    if _host_ok(host):
        _ips.update(r[4][0] for r in out)
    return out


socket.getaddrinfo = _recording_getaddrinfo


def _program_ok(exe):
    if exe is None:
        return False
    exe = os.fsdecode(exe if not isinstance(exe, (list, tuple)) else exe[0])
    name = os.path.basename(exe)
    if os.name == "nt":
        name = name.lower().removesuffix(".exe")
    return name in CMDS or "playwright" + os.sep + "driver" in _real(exe)


def _deny(what):
    raise PermissionError("this agent is not allowed to %s (not declared in its agent.json)" % what)


def _hook(event, args):
    if event == "open":
        path, mode, flags = args
        if path is None or isinstance(path, int):
            return
        p = _real(path)
        if _under(p, SECRET):
            _deny("read " + p)
        writing = (isinstance(mode, str) and any(c in mode for c in "wax+")) or \
            (isinstance(flags, int) and flags & (os.O_WRONLY | os.O_RDWR | os.O_CREAT | os.O_APPEND | os.O_TRUNC))
        if writing and not _under(p, WRITE_OK):
            _deny("write " + p)
    elif event in ("os.remove", "os.rmdir", "os.mkdir", "os.rename", "os.truncate", "shutil.rmtree", "os.chmod"):
        for a in args[:2]:
            if isinstance(a, (str, bytes, os.PathLike)) and not _under(_real(a), WRITE_OK):
                _deny("change " + _real(a))
    elif event == "socket.getaddrinfo":
        if not _host_ok(args[0]):
            _deny("reach %s" % args[0])
    elif event == "socket.connect":
        addr = args[1]
        if isinstance(addr, tuple) and addr[0] not in _ips:
            _deny("connect to %s" % addr[0])
    elif event == "subprocess.Popen":
        exe, argv = args[0], args[1]
        if exe is None:
            exe = argv if isinstance(argv, (str, bytes)) else (list(argv) or [None])[0]
        if not _program_ok(exe):
            _deny("run %s" % exe)
    elif event in ("os.system", "os.exec", "os.spawn", "os.posix_spawn", "pty.spawn", "os.startfile"):
        cmd = args[0]
        if event == "os.system" or not _program_ok(cmd):
            _deny("run " + os.fsdecode(cmd))


sys.addaudithook(_hook)
