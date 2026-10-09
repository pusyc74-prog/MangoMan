"""MangoMan agent guard: loaded into every Python process an agent starts.

It enforces what the agent's agent.json declares, through Python audit hooks
(which code cannot remove once added):
- network: only the declared hosts ("localhost" must be declared too);
- programs: only python (still guarded: no -S, -I or -E, and the guard's
  environment kept) and the declared commands (and Playwright's driver);
- files: writes only inside the work folder and the temp folder; secret
  folders (SSH, cloud and MangoMan keys, browser profiles) cannot be read.
On Linux, an agent with no network also runs in a namespace with no network
at all. This guard is one layer; the marketplace review is the other.
"""
import os
import shutil
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


GUARD_DIR = os.path.realpath(os.path.dirname(os.path.abspath(__file__)))
PYTHONS = {"python", "python3", os.path.basename(sys.executable).lower().removesuffix(".exe")}


def _program_ok(exe, argv=None, env=None):
    """Declared programs only; a Python child must keep the guard."""
    if exe is None:
        return False
    exe = os.fsdecode(exe)
    name = os.path.basename(exe)
    if os.name == "nt":
        name = name.lower().removesuffix(".exe")
    real = os.path.basename(os.path.realpath(shutil.which(exe) or exe)).lower()
    if name.startswith(("python", "pypy")) or real.startswith(("python", "pypy")):
        flags = [os.fsdecode(a) for a in list(argv or [])[1:] if os.fsdecode(a).startswith("-")]
        if name not in PYTHONS or any(f in ("-S", "-I", "-E") or (len(f) > 1 and f[1] != "-" and set(f[1:]) & set("SIE")) for f in flags):
            return False
        if env is None:
            return True
        pp = os.fsdecode(env.get("PYTHONPATH", env.get(b"PYTHONPATH", b"")))
        return bool(pp) and os.path.realpath(pp.split(os.pathsep)[0]) == GUARD_DIR
    return name in CMDS or "playwright" + os.sep + "driver" in _real(exe)


def _split(line):
    """Split a Windows command line: the program (maybe quoted), then words."""
    line = line.strip()
    if line.startswith('"'):
        prog, _, rest = line[1:].partition('"')
    else:
        prog, _, rest = line.partition(" ")
    return [prog] + rest.split()


def _deny(what):
    raise PermissionError("this agent is not allowed to %s (not declared in its agent.json)" % what)


def _own_pair():
    """Python's socket pair on Windows (asyncio, so Playwright) is a socket
    connected to a listener it just made on 127.0.0.1: it never leaves the
    process. Only that code may connect to loopback undeclared."""
    f = sys._getframe(2)
    while f is not None:
        # Older Pythons do it inside socketpair itself.
        if f.f_code.co_name in ("_fallback_socketpair", "socketpair") and f.f_globals.get("__name__") == "socket":
            return True
        f = f.f_back
    return False


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
    elif event in ("os.remove", "os.rmdir", "os.mkdir", "os.rename", "os.truncate", "shutil.rmtree", "os.chmod", "os.link", "os.symlink"):
        for a in args[:2]:
            if not isinstance(a, (str, bytes, os.PathLike)):
                continue
            p = _real(a)
            if _under(p, SECRET):
                _deny("touch " + p)
            if not _under(p, WRITE_OK) and not (event == "os.symlink" and a is args[0]):
                _deny("change " + p)
    elif event in ("socket.getaddrinfo", "socket.gethostbyname", "socket.gethostbyname_ex", "socket.gethostbyaddr"):
        if not _host_ok(args[0]):
            _deny("reach %s" % args[0])
    elif event == "socket.getnameinfo":
        if not _host_ok(args[0][0]):
            _deny("reach %s" % args[0][0])
    elif event == "socket.connect":
        addr = args[1]
        if isinstance(addr, tuple) and addr[0] not in _ips and not (addr[0] in ("127.0.0.1", "::1") and _own_pair()):
            _deny("connect to %s" % addr[0])
        if isinstance(addr, (str, bytes)):
            _deny("connect to the local socket %s" % os.fsdecode(addr))
    elif event == "subprocess.Popen":
        exe, argv, env = args[0], args[1], args[3]
        if isinstance(argv, (str, bytes)):
            # Windows passes one command line; elsewhere a string is a program name.
            argv = _split(os.fsdecode(argv)) if os.name == "nt" else [argv]
        if exe is None:
            exe = (list(argv) or [None])[0]
        if not _program_ok(exe, argv, env):
            _deny("run %s" % exe)
    elif event in ("os.exec", "os.posix_spawn"):
        if not _program_ok(args[0], args[1], args[2]):
            _deny("run " + os.fsdecode(args[0]))
    elif event in ("os.system", "os.spawn", "pty.spawn", "os.startfile"):
        _deny("run " + os.fsdecode(args[0] if not isinstance(args[0], int) else args[1]))


sys.addaudithook(_hook)
