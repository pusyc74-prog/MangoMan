# Handoff: read this first

This file lets a new Claude session continue MangoMan exactly where the last
one stopped (8 Oct 2026, late evening India time). Read all of it, then
CLAUDE.md, then the PRD build log (newest rows first). Before building
anything, say back in a short table: what MangoMan is, where it stands, what
the next build is, and what is waiting on the owner. Then wait for the owner's
go-ahead unless they already gave it.

The last session ran for several days and was compressed many times. Near the
end it made avoidable mistakes in what it told the owner (see "Lessons from
8 Oct"). The owner asked for a fresh session for the next build for that
reason. **Check every fact against a file, a log or a command before you say
it.**

---

## 1. The owner and how to work with them

| Topic | What to do |
|---|---|
| Who | Building MangoMan for **non-technical Indian users** (small businesses, agencies, creators, teachers). Plain words, no jargon; Hindi later. |
| Answers | **Crisp, with tables.** Short beats long. No essays, no recaps of steps. |
| "Only discuss", "don't code", "only answer" | Answer and propose. **Do not change any file.** |
| "Build", "yes do it", "let's build", "start" | Build without asking again: test, commit, push, record. |
| Away | Keep building one item after another, test and fix as you go, keep the task list current. |
| Quality | **Output quality comes first**, then cost, then speed. A change that lowers pack scores is removed, even if it saves tokens (this happened on 8 Oct). |
| Bugs | The owner wants "0 bugs": run every check before every commit, end each wave with a review by a separate agent that has not seen the work. |
| Waiting | People will not wait minutes. Say what is happening while they wait; cut waits that serve no one. |
| Money | **Only budget: the Claude subscription.** No paid services. If anything costs money (code signing, minutes, credits), say the cost first and give the free option. |
| Credit cards | No provider or service that needs a card (this is why Cerebras is out). |
| PRD | **Silent.** Every change goes into both PRD copies in the same step (section 9). Never mention PRD updates in replies; share the PRD only when asked. |
| Hard rules | No em dashes anywhere (code, docs, replies). Never mention the former company name. Keep the code small. |
| Honesty | Say when something already exists, when a number is a guess, when a check could not be run, and when you got something wrong. |
| Processes | Kill by PID, never `pkill -f` (it can match and kill your own shell). |

**Why MangoMan exists:** to serve people left out of the AI tooling boom, who
cannot pay for AI or wire up free tiers. Free, private and honest is the
point. **Two goals:** (1) 10,000 people using free AI through MangoMan;
(2) creators earning money from their agents on the marketplace.

---

## 2. Where things stand (8 Oct 2026, end of day)

| Area | State |
|---|---|
| Phase 1 (M1 to M4) | Built: router, dashboard, 17 skill packs, agent kit and marketplace (closed), Guardian, QA agent, team keys, people tokens. |
| M5 (app for everyone) | **Parked** by the owner (7 Oct): teachers and first-time users cannot bring their own keys. Do not start it. |
| M6 (coding screen, `mangoman code --ui`) | **Complete** (8 Oct). Passed a real-model check: 7 of 7 in 133 s. |
| Real-model quality | Last two free/coder runs, one case per pack: **7 of 7 passed** both times. Best: email 100, website copy 100, SEO 97.3. |
| Release | **None yet.** The owner can try it from a CI build (section 12). |
| Repo | **Public, by the owner's choice (9 Oct: "keep it public for now").** Do not ask again until they raise it. CI is already cut down for private minutes. |
| CI | Green on the last commit. No open issues. |
| Next build | Agreed by the owner: (1) consolidate the router's failure handling, (2) easy setup (section 3). |

---

## 3. THE NEXT BUILD (agreed with the owner on 8 Oct)

Order: **first the consolidation (about half a day), then the easy setup
(about 3 to 4 days).** Show the plan in a table first; the owner approved the
direction, not the details.

### 3a. Consolidate the router's failure handling

**Why:** over 7 and 8 Oct, layers were added one at a time, each fixing a real
problem seen in a real run. Each works and is tested, but together they are
hard to reason about, and some overlap. The owner asked whether "we have gone
too far with the code"; the honest answer was "not in size, yes in this area".

**What exists today** (all in `internal/router`; verify in the code):

| Mechanism | Where | What it does |
|---|---|---|
| Circuit breaker | `internal/breaker`, `router.New`: `breaker.New(3, 30*time.Second, 5*time.Minute)` | 3 failures in a row: the model is skipped for 30 s, doubling per trip up to 5 min; then one probe. |
| Quota block | `internal/quota` (`Block`, `Allow`, `RetryAfter`) | A 429 blocks the key and model until the provider's reset (Retry-After, x-ratelimit-reset-*, OpenRouter's x-ratelimit-reset in unix ms). |
| Tried last | `session.go`: `markBusy`, `busyLast`; durations `busyFor` 2 min (overloaded, empty answer), `silentFor` 10 min (no first word in time), `garbledFor` 30 min (leaked control tokens) | Moves the model behind the others; never removes it, so a pinned (strict/) model is still asked. |
| Health ranking | `health.go`: `Rate` (needs 3 samples), `Speed` (needs 2 timed samples) | Feeds the candidate score. |
| Retry after a dropped stream | `router.go` Handle loop: a `stream_error` candidate is appended again, `maxRetries` 2, 1 s pause (`asked` map) | For overloaded providers. |
| Garbled stop | `stream.go`: `controlToken` regexp | Before the answer starts: fail over. After: error event to the client. |
| Empty answer stop | `stream.go`: reasoning only, then the end | Holds back `[DONE]`, sends an error event so an unattended run continues. |
| Time limits | `router.New`: `StreamIdle` 30 s (no first word; set from measurement on 8 Oct), `StreamStall` 180 s (gap after the answer started), `NonStreamTimeout` 180 s | |
| Wait told to clients | `plan.go` (`EarliestReset` now includes breaker `OpenUntil`), `router.go` `activity.waitUntil` (`maxHold` 2 min) | Retry-After counts models only cooling off; watchdogs treat a told-to-wait client as busy. |
| Activity | `router.go`: `activity`, `Busy()`, `Trying()`, `Attempt`, `giveUpWord`; `GET /mangoman/busy` in `internal/ingress/server.go` | For the two watchdogs and the screen's messages. |
| Run watchdog | `cmd/mangoman/assist.go`: `watchStall`, `commandRunning` (pgrep `-a`, then `-lf` on macOS), `languageServer`, `stallAfter` 1 min, `commandCeiling` 10 min, resume with `--continue` at most twice, then an error | `mangoman code run` only. Off on Windows (no pgrep). |
| Screen watchdog | `internal/ingress/ui/code.js`: `watch()`, `restart()`, `settle()` | Restarts a step after a minute with nothing happening, twice at most. |

**Goal:** one clear rule, "how a model earns its place in line", with one
place that decides demotion and its reason and expiry, instead of the breaker
and the "tried last" map both doing it. Keep every behaviour that a real run
proved (each has a test named after the measurement). **Rules:** no behaviour
change without a test; run the whole router test suite; then one real
free/coder pack run (one case per pack) to confirm scores hold (compare with
runs 20261008-152455 and 20261008-161447). If the design is not clearly
simpler, say so and stop: a smaller tidy-up is fine.

### 3b. Easy setup (options A, B and D; the owner said "yes this works")

**The problem (owner, 8 Oct):** "not everyone will have Python 3; isn't this
getting too complicated, like OpenCode's setup is a pain." Today a user must
install Python 3, run pip commands, install a browser engine for PDFs, and use
a terminal. That has to go.

**Target experience:** download, double-click, paste the NVIDIA key, click
"Get ready", wait about 2 minutes. No terminal, no Python install, no pip.

| Part | What to build | Notes and traps (verified 8 Oct) |
|---|---|---|
| **A. MangoMan brings its own Python** | On "Get ready" (and on first pack use if missed), download **uv** (Astral, MIT or Apache-2.0, a single program) pinned by version and SHA-256 per system, the same way `internal/opencode` pins OpenCode. uv installs a private Python (3.12) and a virtual environment inside MangoMan's folder with the packs' packages. | **Packages the packs import** (checked): `pillow` (11 imports), `python-pptx` (8), `python-docx` (6), `pypdf` (3), `playwright` (6, optional: only as a PDF engine), `pandas` (only `mangoman-data-dashboard/scripts/profile.py`). **The agent runs `python3 <skill dir>/scripts/...`** (it is written that way in every SKILL.md), so `mangoman code` must put the private environment's `bin` (Windows: `Scripts`) **first in PATH** for OpenCode. **On Windows a virtual environment has `python.exe`, not `python3.exe`**: add a `python3.exe` copy or shim there, or every pack fails on Windows. Check uv's license, size and download URLs live before pinning; do not trust these notes for numbers. |
| **B. PDFs with the browser the user already has** | 7 packs make PDFs: resume, invoice, proposal, client report, meeting minutes, CEO deck, brand kit (`render.html_to_pdf`). | **Most of B already exists:** `internal/skills/packs/shared/render.py` tries Playwright first, then Chrome, Chromium or Edge by name and in the usual Mac and Windows folders, including `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`. The last session told the owner B meant adding Edge; that was wrong. What is left: add Chrome's per-user folder on Windows (`%LOCALAPPDATA%\Google\Chrome\Application\chrome.exe`) and Edge under `C:\Program Files\Microsoft\Edge\...`; make sure no Playwright never blocks PDFs; a plain message when no browser is found; **test on real Windows and Mac** (CI `other-systems` job or the owner's computer). |
| **D. Setup in the browser** | `mangoman` with no arguments (a double-click) on a first run: create the config, start the router, open a setup page in the browser. Step 1: the NVIDIA key, with a short picture guide and a link to build.nvidia.com, checked with the provider (reuse the dashboard's add-key endpoint and `setup.ConnectKey`). Step 2: **"Get ready"**: shows the download size first, then downloads OpenCode and Python (A) with progress and plain errors. Step 3: done, with buttons for the dashboard and the coding screen (offer a project folder such as "MangoMan Projects"). | Reuse what exists: `internal/setup` (the terminal wizard, `Order` puts NVIDIA first and Cerebras last), `internal/opencode.Install`, the dashboard's styles (`app.css`), the token-in-URL-fragment pattern. On a later run, double-click opens the dashboard. Windows will show a console window; that is fine for now. **Code signing** (removing "unknown developer" warnings) costs money: Apple's developer program is about USD 99 a year; Windows signing also costs. **Ask the owner; do not buy.** Until then the page and README show how to get past the warning. |
| Also | A clear check and message when the private Python is missing or broken, before a pack task starts (open item from 8 Oct). | |

**Download size to show users:** about 60 MB for OpenCode plus about 60 to
80 MB for Python and packages (estimates; measure them). Many users are on
mobile data.

**Testing for 3b:** unit tests in Go; a CI job on Windows and macOS (the
`other-systems` job in `ci.yml` runs on Sundays and on manual runs; a session
cannot start it: see section 7, consider adding a `repository_dispatch` type
for it); the offline stand-in model (section 8); a real pack run; and ask the
owner to try it on their own computer (**Windows**, answered 9 Oct).

---

## 4. Decisions made on 7 and 8 Oct (do not reopen without the owner)

| Decision | Why |
|---|---|
| **Cerebras stays out** | Its free trial asks for a credit card. Setup order is NVIDIA, Groq, OpenRouter, OpenCode Zen, Cerebras last (`internal/setup` `Order`, `app.js` `ORDER`). |
| NVIDIA carries pack work | Groq's free per-minute token limits (6,000 to 8,000) are smaller than one request of a skill task (about 14,500 tokens). OpenRouter allows 50 requests a day per account. |
| GLM 5.3 on NVIDIA removed from the catalogue (`seed-2026-10-01.6`) and from the radar | Its answers came out as noise with chat-template tokens (`<|close|>`). |
| Qwen 3.8 27B on OpenRouter removed (`seed-2026-10-01.7`) | OpenRouter no longer serves it free. |
| Skill tests are not installed with the packs | Models read the scorer instead of doing the task. `mangoman agents eval` takes the test set from the binary. |
| The "skill rule and slimmer tools" setup was **removed** | Same six cases: ad copy 69 against 78 without it, almost no token saving. OpenCode runs with its own tools and instructions. |
| First-word limit 30 s (was 60 s) | 345 measured answers: median 1.1 s, 99% within 10.9 s, slowest 35.1 s; silent models never answered. |
| Silent model tried last for 10 min; overloaded 2 min; empty answer 2 min; garbled 30 min | Measured costs in real runs (section 5). |
| Website copy keeps its normal instructions, not the short-step (ste) version | ste scored 97 and 94 but 0 twice on one case (gurukul-maths stalled). |
| Ad copy pack 1.2 | Over-long lines show their text and how much to cut. |
| CI cut for a private repo (section 6) | 2,000 free minutes a month. |
| Pack runs and the workspace check install the **pinned** OpenCode | They had been testing whatever npm had newest, not what users get. |
| Team keys (7 Oct) | Several people on one computer each add their own key; a notice is shown; the key owner accepts the risk. Not the same as Phase 4 team mode. |
| My list, ask before weaker models, one model per chat | 6 Oct decisions, still in force. |
| M5 parked; Claude Code or Codex alongside OpenCode: later | Owner. |
| Expo Go for phone previews | Owner. |
| Release only after the repo is private, from a separate public repo with only the built app | Owner: the code must not be public. |
| Repo stays public for now (9 Oct) | Owner. Free Actions minutes; no self-hosted runner while public. |
| The owner tests on Windows (9 Oct) | Windows is the first system the easy setup must work on. |

**Discussed but not approved (do not build without asking):** run pack test
cases in parallel to cut a 7-pack run from about 90 to about 15 minutes; batch
fixes before a real run; real runs only when output can change; keep building
while a run goes. The owner asked whether quality and "0 bugs" still hold
(answer: yes, no check is removed), then moved on without a clear yes.

---

## 5. Measured facts (8 Oct; sources on the eval-reports branch)

| Fact | Source |
|---|---|
| Time to first word on NVIDIA: median 1.1 s, 90% 2.4 s, 99% 10.9 s, max 35.1 s (345 answers) | pack run 20261008-152455 |
| With the 30 s limit: 366 answers, max 6.3 s, median 0.8 s; 7 of 7 tasks passed | pack run 20261008-161447 |
| NVIDIA "Service temporarily overloaded" about 1 request in 5 at busy times; 429s while MangoMan sent at most 9 a minute (published limit 40): load throttling, not a daily cap | runs 20261008-080606 and earlier |
| No NVIDIA daily cap found yet | not measured end to end |
| Kimi K3 and GLM 5.3 on NVIDIA garble sometimes (same `<|close|>` token): likely NVIDIA's serving; OpenCode sends no temperature, providers use their defaults | runs 20261008-052629, 20261008-063121, 20261008-122555 |
| Kimi K3 and DeepSeek V4.1 Flash often say nothing past the limit | runs 20261008-144440 (23 times), 20261008-161447 (14 times) |
| Task cost (median of 13 tasks): big task 23 requests and 527,000 tokens; small 12 and 145,000 | `internal/ingress/freeai.go` `taskCost` |
| Costly loops still happen: ad copy 100 requests, website copy 156 (models fix files many times: character limits, JSON syntax) | run 20261008-161447 |
| The 17-minute freeze (7 Oct) was the router telling OpenCode to wait hours: a model only cooling off was not counted in Retry-After. Fixed. | PRD build log, 8 Oct |
| Coding screen on a real model: 7 of 7 checks in 133 s; about 120 s of it waiting on two silent models (before the 30 s limit) | workspace check 20261008-135011 |

---

## 6. The repo, CI and money

- **Repo:** github.com/pusyc74-prog/MangoMan, branch `main`. **Still public**
  On 9 Oct the owner chose to **keep it public for now**; do not remind them.
  When they decide to switch: **a session cannot change repository settings**
  (the proxy refuses them); the owner does it in Settings, General, Danger
  Zone, Change visibility, Private.
- **Why it was public:** on 7 Oct the private repo's free Actions minutes ran
  out and every job was refused. Public repos run free. **Before anything
  that uses limited resources (minutes, credits), say the cost first.**
- **History was scanned for keys: none.** Never commit a key, a .env file or
  a signing key. `scripts/mask-keys.py` blanks keys in published reports.
- **Secrets set in the repo (seen working in runs):** `NVIDIA_API_KEY`,
  `OPENROUTER_API_KEY`, `GROQ_API_KEY`. Not set: `CEREBRAS_API_KEY` (out),
  `OPENCODE_ZEN_API_KEY`, `MARKETPLACE_KEY`.
- **Workflows now (cut for a private repo on 8 Oct):**

| Workflow | When | What |
|---|---|---|
| `ci.yml` | every push (newer push cancels older); Sundays; manual | `check` (Linux: vet, race tests, lean check). `other-systems` (macOS, Windows tests) and `build` (5 targets, artifact `mangoman`) only on Sundays and manual runs. |
| `packs.yml` | push touching packs, agents or `scripts/check-packs.sh`; nightly | builds each pack's sample and runs its checker; pyflakes |
| `qa.yml` | nightly | race tests, dashboard in a browser with Guardian watching; opens a GitHub issue on failure |
| `doctor.yml` | once a day (08:00 IST); on catalogue changes | checks every provider with a key; reports on the `doctor-reports` branch |
| `pack-run.yml` | dispatch or manual only | real-model pack runs; reports on `eval-reports` under `reports/pack-run/` |
| `workspace-check.yml` | dispatch or manual only | the coding screen through one real task; reports under `reports/workspace/` |
| `agent-eval.yml` | manual | an advanced agent against its pack |

- **Budget:** about 1,000 of 2,000 free minutes a month at about 20 pushes a
  day. **Push in batches.** A pack run takes about 45 to 60 minutes of
  minutes; a workspace check about 15. Once private, use the self-hosted
  runner for these (below).
- **Self-hosted runner (owner, once the repo is private, about 10 minutes):**
  Settings, Actions, Runners, New self-hosted runner; run the commands shown.
  Linux or macOS works best (macOS: `brew install coreutils` for `timeout`).
  The workflows install Go, Python, the pinned OpenCode and Chromium
  themselves and use port 4199 so they do not clash with the owner's own
  MangoMan. Start a run on it with `runner: self-hosted`. **Never add a
  self-hosted runner while the repo is public.**

---

## 7. Working from a session: what works and what does not

| Need | How |
|---|---|
| Watch CI | `gh api repos/pusyc74-prog/MangoMan/commits/<sha>/check-runs --jq '.check_runs[]\|[.name,.status,.conclusion]\|@tsv'`. The Actions API (`gh run list`, logs, `gh secret list`, workflow_dispatch) is **blocked**. |
| Issues | `gh api repos/pusyc74-prog/MangoMan/issues` (the nightly qa opens one on failure). |
| Start a pack run | `gh api -X POST repos/pusyc74-prog/MangoMan/dispatches --input -` with `{"event_type":"pack-run","client_payload":{"model":"free/coder","cases":"1","case_timeout":"900"}}`. Payload keys: model, packs (spaces or commas), variant, cases, case_timeout, runner. Leave `cases` out for three per pack. |
| Start the coding screen check | event_type `workspace-check`, payload model, runner. |
| Queue rule | Runs of one workflow go one at a time; GitHub keeps only **one** waiting run per group, so a third dispatch replaces the second. Dispatch the next after the previous starts. A dispatch runs the code of `main` at the moment you send it. |
| Read results | `git clone -q --depth 1 -b eval-reports https://github.com/pusyc74-prog/MangoMan.git` then `reports/pack-run/<stamp>/report.md`, `logs/<pack>/<case>.log`, `serve.log` (router log, has "first word after Ns"), `usage.json`. Doctor: branch `doctor-reports`, `latest.txt`. |
| Change repo settings | **Not possible** (visibility, secrets, runners): ask the owner. |
| Web pages | WebFetch works for public pages; some sites need the owner's approval. |
| Local network | The shell cannot reach most sites; `proxy.golang.org`, npm and PyPI may be blocked. GitHub (git, API, releases) works. |

---

## 8. Local development in the session (container tools are lost between sessions)

**Go builds:** `proxy.golang.org` is blocked, so the one dependency
(`github.com/zalando/go-keyring`) cannot download. Make a stub and a dev
module file once:

```sh
mkdir -p /tmp/kr && cd /tmp/kr
printf 'module github.com/zalando/go-keyring\ngo 1.24\n' > go.mod
cat > k.go <<'EOF'
package keyring

import "errors"

var ErrNotFound = errors.New("not found")

func Get(s, u string) (string, error) { return "", ErrNotFound }
func Set(s, u, p string) error        { return errors.New("stub") }
func Delete(s, u string) error        { return ErrNotFound }
EOF
cd /home/claude/mangoman   # or wherever the repo is
sed 's#^require github.com/zalando/go-keyring.*#&\nreplace github.com/zalando/go-keyring => /tmp/kr#' go.mod > /tmp/dev.mod
cp go.sum /tmp/dev.sum 2>/dev/null || true
export GOFLAGS="-mod=mod -modfile=/tmp/dev.mod"
```

Check the result builds (`go build ./...`) before relying on it. Keys then
need `MANGOMAN_KEYSTORE=file` and `MANGOMAN_PASSPHRASE=<anything>`.

**Checks before every commit** (CLAUDE.md): `go vet ./...`; `go test -race ./...`;
`golangci-lint run --no-config --default=none -E unused,ineffassign ./...`
(installed locally last time; else rely on CI); `node --check` on changed
`internal/ingress/ui/*.js`; when packs change `scripts/check-packs.sh` (needs
the dev module env above) and pyflakes on the pack scripts and `scripts/*.py`.
pyflakes was not installable from PyPI last time; it was found in
`~/.cache/uv/archive-v0/*/pyflakes` (use `PYTHONPATH=<that dir> python3 -m pyflakes`),
else CI runs it.

**OpenCode locally:** `MANGOMAN_HOME=<dir> scripts/install-opencode.sh` (GitHub
releases, pinned version and checksum), or download the release asset by hand.
Last time it lived in `/tmp/claude-0/oc/x/opencode`.

**Offline end-to-end tests with the stand-in model (`scripts/fake-model.py`):**

```sh
python3 scripts/fake-model.py 11434 /tmp/fmctl &          # poses as local Ollama, model "fake"
OLLAMA_CONTEXT_LENGTH=131072 MANGOMAN_HOME=/tmp/h MANGOMAN_KEYSTORE=file MANGOMAN_PASSPHRASE=x \
  ./mm serve &                                             # after ./mm init with that home
mkdir /tmp/p && cd /tmp/p
PATH=<dir with opencode>:$PATH OPENCODE_DISABLE_MODELS_FETCH=1 MANGOMAN_HOME=/tmp/h \
  MANGOMAN_KEYSTORE=file MANGOMAN_PASSPHRASE=x ./mm code --no-web --model strict/fake run --auto "make hello.txt"
# or: ... code --no-web --ui --model strict/fake   (then drive code.html with Playwright)
```

Touch files in the control dir to change the next coding request:
`fail_once`, `fail_always`, `overload_once`, `think_once`, `garble_once`,
`delay` (seconds), `cmd` (the command to run). See the script's header.

**Traps seen in the session's sandbox:**
- OpenCode sometimes hangs while starting (no request ever reaches the
  router), probably a blocked network fetch. Run again; it does not happen in
  CI. `OPENCODE_DISABLE_MODELS_FETCH=1` helps.
- The stand-in only does non-streamed requests badly (OpenCode streams, so it
  does not matter).
- `pkill -f <pattern>` can kill your own shell; kill by PID.
- Background processes from a previous step (router on 4141, stand-in on
  11434, `opencode serve`) keep ports busy: stop them by PID when done.
- A Playwright `wait_for_function` is blocked by the page's CSP; use locator
  waits.
- `mangoman code --ui` keeps running until Ctrl-C (or SIGTERM); it stops its
  OpenCode server itself.

---

## 9. The PRD (two copies, always identical, updated silently)

- **Claude Docs:** "MangoMan: Technical PRD". Container
  `{"kind":"project","id":"d2bc0228-f75f-45b9-aabf-768667012c43"}`, file
  `56bab6a7-2595`, body node `d462a06c-7487`. The build log is a table whose
  header row has id `mmvc6t48fqh.55062` (table `mmvc6t48fqh.55061`); newest
  rows go right under the header.
- **Repo copy:** `docs/PRD.md`. The build log rows look like
  `| 2026-10-08 | Item | What changed |`, newest first, right under
  `| --- | --- | --- |`. In the repo copy, a `|` inside a cell is written `\|`.
- **Add a row to the doc** (one call): `mcp__Claude_Docs__update` with ref
  `{"object":"node","id":"d462a06c-7487"}`, engine `prose`, the container
  above, and payload
  `{"ops":[{"op":"insert","target":{"kind":"blocks","ids":["mmvc6t48fqh.55062"]},"side":"after","source":{"as":"blocks","from":{"kind":"inline","content":[{"type":"row","content":[<three cells>]}]}}}]}`
  where a cell is `{"type":"cell","content":[{"type":"paragraph","content":[{"type":"text","text":"..."}]}]}`.
  Change words elsewhere with `{"op":"replace","target":{"kind":"find","text":"old words"},"with":{"as":"text","from":{"kind":"inline","content":"new words"}}}`.
- **Check they match:** `mcp__Claude_Docs__export` (container, file, format
  markdown). The result is too large to show, so it is saved to a file; run
  `python3 scripts/prd-diff.py <that file>` and expect `diff lines (0 = identical): 0`.
- Never mention PRD updates to the owner.

---

## 10. What is built (all pushed)

- **Router:** failover, quotas learned from headers, skill floor, one model
  per chat, provider priority, My list with ask-before-weaker, team keys and
  taking turns, person tokens (`mangoman people`), private-first for prompts
  that look like they hold secrets, the failure handling in section 3a, a
  load test (3,000 requests, 300 at once, 0 failed).
- **Dashboard** (`mangoman dashboard`): connections, team keys, "Free AI you
  get each day" (`freeai.go`), My list, models table, recent requests, radar
  of new models, Guardian approvals, decision brain stats, OpenCode credit.
- **17 skill packs** with builders and checkers (scripts render and check;
  the model writes content); test sets for 7 (ad copy, listing, email,
  resume, SEO, social posts, website copy); ad copy pack 1.2.
- **Agents and marketplace:** 2 advanced agents (amazon-listing-pro,
  google-ads-pro), signed `.mmagent` packages, sandbox, safety review, eval
  harness, registry (closed until `MARKETPLACE_KEY` exists).
- **Coding:** `mangoman code` (OpenCode wired to MangoMan, pinned 1.18.34,
  downloaded on first use with a SHA-256 check); `code run` with the stall
  watchdog and resume; **M6 coding screen** `mangoman code --ui`: chat with
  Plan and Build, Stop, New chat, approval cards, question cards, Changes,
  Terminal, Preview (phone, tablet, laptop frames, phone address on Wi-Fi,
  Expo Go hint), Ship for Guardian projects, busy messages, stall restart.
- **Guardian:** watch, first aid, fix on its own branch, QA writes tests,
  review blocks secrets, dev deploy and QA, Telegram approval, production
  deploy, rollback, nightly masked data, reports. Tested only with a stand-in
  AI so far.
- `mangoman qa`, `review`, `tests`, `changelog`, `new`, `doctor`, `usage`,
  `radar`, `setup` (terminal wizard), PRIVACY.md, README.

---

## 11. Open items (not built), most important first

| Item | Notes |
|---|---|
| Easy setup (section 3b) | Next build. Includes the Python check. |
| Router consolidation (section 3a) | Next build, first. |
| Costly fix loops in packs | Ad copy 100 requests, website copy 156 in one run: help models fix in one pass (for example show the lines around a JSON syntax error in website copy's checker). |
| Windows stall watchdog | Off (no pgrep). Could use PowerShell `Get-CimInstance Win32_Process` for child processes. |
| Screen shows raw model ids ("kimi-k3") | Use display names. |
| The waiting seconds on the screen sometimes jump back when two requests run | Cosmetic. |
| NVIDIA daily cap | Not measured end to end. |
| Garbling cause | Not proven; a test sending a sensible temperature (for example 0.6) could tell. |
| Quality after a mid-task model switch | Not measured. |
| Release | One-line installer and release page from a separate public repo, after the repo is private and the easy setup exists. Code signing is the owner's cost decision. |
| `OPENCODE_ZEN_API_KEY` secret | So the doctor also checks Zen. |
| Guardian real run | Needs a Telegram bot from @BotFather and a real app. |
| Payments, revenue share | Razorpay or Stripe; owner decision. |

---

## 12. Waiting on the owner

| Item | Status |
|---|---|
| Switch the repo to private | **Answered 9 Oct: keep it public for now.** Release still waits for a private repo (section 4). |
| Which computer to test on | **Answered 9 Oct: Windows.** Make the easy setup work on Windows first (the `python3.exe` shim, Chrome and Edge paths, the console window). |
| Code signing (paid) | Not asked yet as a decision; mention with the easy setup. |
| OPENCODE_ZEN_API_KEY, MARKETPLACE_KEY | Open. |
| Telegram bot, a real app for Guardian, a Windows tester, first beta users | Open. |

**How the owner can try MangoMan today** (told on 8 Oct): GitHub, Actions,
`ci`, Run workflow; when done, download the `mangoman` artifact (one file per
system); rename to `mangoman` (`mangoman.exe`); in a terminal: `mangoman setup`
(NVIDIA key first), `mangoman serve`, `mangoman dashboard`, `mangoman test "hello"`,
and in an empty folder `mangoman code --ui`. Skill packs need **Python 3**
plus `pip install pillow python-pptx python-docx pypdf playwright` and
`python -m playwright install chromium`; ad copy and email need only Python.
Mac: right-click, Open (unsigned); Windows: More info, Run anyway. The easy
setup (3b) removes all of this.

---

## 13. Lessons from 8 Oct (so they do not repeat)

| What went wrong | Do instead |
|---|---|
| Asked the owner for a Cerebras key that this file already ruled out | Read this file and the PRD before asking for anything. |
| Garbled sentences in replies and in the PRD | Reread every reply and every PRD row before sending. |
| Quoted a provider's message from memory | Copy messages exactly from the log. |
| Wrote "the repo went private" before it did | Only write what has happened. |
| Gave the owner download steps without Python | Walk the user's path from start to end before describing it. |
| Called a 39% token saving real from a confounded run | Compare only like with like (same cases, same model, same pack version, healthy providers). |
| First timing measurement started the clock at the response headers | Sanity-check new measurements (a median of 0.0 s is a bug). |
| Told the owner B needed Edge added; it already existed | Check the code before describing what a change involves. |
| A typo-level bug (commas in a pack list) wasted a whole real run | Try a dispatch payload against the script locally first. |
| Waited idle during long runs | Keep building while a run goes; read results when it ends. |

---

## 14. What MangoMan is (one paragraph)

A free, local-first AI router in Go. It pools every free model the user can
reach (NVIDIA, Groq, OpenRouter, OpenCode Zen, Ollama; Cerebras optional),
switches automatically when one runs out or fails, and never sees prompts or
keys. On top: 17 skill packs, advanced agents with a signed creator
marketplace, OpenCode as the coding engine (`mangoman code`, and the
`--ui` coding screen), a QA agent, and **Guardian**, the self-healing system
(every app gets a dev environment next to production; production only gets
tested changes, after the owner approves on Telegram).

## 15. Parked (do not reopen on your own)

| Idea | State |
|---|---|
| M5, the app for everyone | Parked 7 Oct (keys). |
| Public LLM for the general public | Parked 4 Oct. |
| Chat history storage | Parked 3 Oct (design in the PRD). |
| Cloud storage for made files (R2), Supabase or D1 | Parked 3 Oct. |
| Cloud mode | Only if at all, opt-in, split-key design. |
| Claude Code or Codex alongside OpenCode | Later (owner, 7 Oct). |
| Remove the dashboard's training labels | Only if the owner asks. |

## 16. Separate: Project B

The owner has a separate Project B (superagents built from these skills). It
is **not** part of MangoMan: do not add it to the PRD or the code.
