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

## 2. Where things stand (10 Oct 2026, night)

| Area | State |
|---|---|
| Phase 1 (M1 to M4) | Built: router, dashboard, 18 skill packs, 3 super agents and the skills library, agent kit and marketplace (closed), Guardian, QA agent, team keys, people tokens. |
| M5 (app for everyone) | **Parked** by the owner (7 Oct): teachers and first-time users cannot bring their own keys. Do not start it. |
| M6 (coding screen, `mangoman code --ui`) | **Complete** (8 Oct). Passed a real-model check: 7 of 7 in 133 s. |
| Real-model quality | free/coder, one case per pack, installed the user's way since 9 Oct: 20261009-165742 passed **6 of 7** (website copy 0: answers cut at the old 180 s stall limit), 20261009-174404 (stall limit 300 s) passed **7 of 7**, average 90.6 (ad copy 90.7, listing 85, email 95, resume 86.7, SEO 96.5, social 86.4, website copy 94). Website copy alone: 2 failures in 6 runs from invalid site.json; the JSON error helper (3ef2091) is being measured with 3 runs. |
| Easy setup (3b) | **Built and pushed (9 Oct).** Double-click opens a setup page; `mangoman ready` in a terminal. Windows check (`windows-check.yml`, stand-in model) passed: OpenCode ran pack commands in Windows PowerShell and all 19 pack samples passed with MangoMan's own Python and the computer's own browser. Waiting: the owner's own try on Windows. |
| Release | **Download site (13 Oct).** `scripts/make-site.sh` builds `dist/site` (page, `install.sh`, `install.ps1`, five binaries, `SHA256SUMS.txt`); the ci build job's artifact is that folder. The owner drops it on Netlify by hand (Sites, Add new site, Deploy manually), site name `getmangoman` (https://getmangoman.netlify.app). Users paste one line: Windows PowerShell or Mac Terminal. |
| Repo | **Public, by the owner's choice (9 Oct: "keep it public for now").** Do not ask again until they raise it. CI is already cut down for private minutes. |
| CI | Green on 2933a9c, Linux, Mac and Windows (ci run 38074112513, which has the build to try). Issue #2 closed. mac-check passed 19 of 19 pack samples. |
| App layout (15 Oct) | **Built, owner's request.** The dashboard is one app with a left menu: + New task, Super Agents, Chargesheets (owner's name; built 16 Oct, see the next row), Templates, My Files, History (coming soon); Connections, Usage, Settings hold every old dashboard part with the same IDs. Agents and templates live in `internal/ingress/ui/starters.json` (12 ready agents, 4 "coming later" with the reason, 17 templates; `TestStartersMatchPacks` keeps it in step with the packs). Start opens the coding screen in one named tab with the agent's first message in a fresh chat (the user reads and sends it). |
| Chargesheets (15 and 16 Oct) | **Built, owner's request** ("replace the hours people spend in Excel, Tableau and Power BI"; a feature, not an agent). Add data by uploading Excel or CSV files (several: same columns are stacked with a Source column; a lookup table is joined by a unique key only when 80% or more match, row count never changes) or by a Google Sheet link shared as "anyone with the link" (whole sheet as .xlsx from `docs.google.com/.../export?format=xlsx`, every tab; checked every 5 minutes while the dashboard is open; owner: link now, Google sign-in for private sheets later). `internal/sheets/engine.py` (standard library plus openpyxl, run with MangoMan's Python) reads and cleans: title and totals rows, two header rows, years as column names, rupee and lakh/crore amounts, (1,200) negatives, day-first dates, Excel date serials, spellings merged; writes data.json (columns for the browser), profile.json (the check screen and the models) and data.sqlite (Ask). Up to 300,000 rows a table, 50 MB a file. The page (`ui/sheets.js`, `ui/sheets.css`, ECharts 5.5.1 Apache-2.0 in `ui/vendor/`, tooltips drawn on the canvas because the CSP blocks inline styles) counts again in the browser for every filter: Overview, Trends, Breakdown, Drill-down, Spread, Data quality, All rows (CSV), Readout (PDF by printing), plus expert tabs Feed health, Customers (only when names repeat), Campaigns. Palette: six brand-led slots checked with the dataviz validator in light and dark. Ask: the model writes one read-only SQLite query at a time (authorizer, 10 s limit, 10 MB value limit), code runs it, up to 5 queries, then an answer whose numbers are checked against the results (`check.go`: values in one column, row or fact may be totalled, added or compared; shares only in groups of 12 or fewer). Describe, Ask and the readout go through the router as free/auto; describe and Ask ask for JSON (class extraction, the guard rejects invalid JSON). Tests: `sheets-test.yml` (real models, 5 made-up files, 17 questions with answers the script works out, plus Google's public sample sheet) passed 17 of 17 on 15 Oct (run 38788709770, on c09c406; median 28 s an answer, slowest 138 s; the run before passed 16 of 17, the miss was the script reading a null step list, now never null). On "why did sales drop" the checker flagged two true monthly totals the model added up from three channel rows itself: a false warning on the safe side. The model's description waits 12 s on load, then lands on its own. `qa.yml` now clicks through Chargesheets nightly with the stand-in model (`scripts/sheets-ui-check.py`). A separate review found 15 issues; all fixed (c09c406). |
| Chargesheets: connections and dashboards (16 Oct) | **Built, owner's request** ("Shopify is 1 source, Meta ads 1, Google ads 1, sales team 1 ... a complete overview of the business"; custom dashboards with suggestions, and talking to the AI before building). Connections (`internal/sheets/connect.go`, `shopify.go`, `meta.go`; Connect an app on the add panel): Shopify by an app the owner makes in the Dev Dashboard (Client ID and secret; client credentials grant at `/admin/oauth/access_token`, 24-hour token bought again by MangoMan; Admin GraphQL 2026-10 bulk export of orders and their lines; two CSV tables, "Shopify orders" and "Shopify products sold", dates in the shop's time zone, test orders left out; Shopify shares the last 60 days unless the app may read all orders, and order data needs protected customer data level 1). Meta by a system user's token with ads_read (Graph API v26.0 insights, campaign by day, last 364 days in four 91-day requests, only Meta's own next pages followed; purchases are omni_purchase, else website purchases, never both added). Google Ads by a Google Sheet that a Google Ads script (shown on the page) fills daily: Google's API needs an approved developer token with brand checks. Secrets in the key store as `chargesheets:<sheet>:<kind>:<account>`, deleted with the last table that uses them; refresh reads connections at most every 30 minutes unless Update now. Business overview tab when two or more tables have dates and measures: each added up by date, sales against ad spend (return on ad spend, ad spend as % of sales, per order), ratios counted only over the days all their sources cover; joins by date are exact, which ad brought which order is not known without tracking tags. My dashboards tab (`internal/sheets/boards.go`, boards.json per sheet): suggestions worked out by code (business overview, each table at a glance, ads per campaign, customers), own dashboards (chart builder with live preview, size, drag to arrange, PDF), own metrics ((add up - take away) / divide by x 1 or 100), and Plan with AI (the model sees column names only, proposes JSON that code checks against the data; parts that do not fit are left out and listed). Tests: stand-in Shopify and Meta servers in `connect_test.go`, `boards_test.go`, API test, and the nightly browser check now keeps a suggestion, adds a chart and plans one. A separate review found 10 issues (a failing connection stopped Google Sheet refreshes; two quick saves could lose every dashboard; shipping or goods costs counted as ad spend; sales and spend in different currencies divided; spend per order counted order lines; cancelled orders counted as sales; filters on missing columns ignored; the Shopify token cache ignored the secret; connections held the sheet's lock for minutes; small editor limits); all fixed with tests. Shopify orders now carry Sales after refunds, and products of cancelled orders are left out; Meta reads to yesterday. Not yet tried against a real Shopify store or Meta account: the owner makes a free dev store first. |
| Chargesheets look (16 Oct) | **Built, owner's request**: in the style of the hand-made sample dashboard (cards with shadows, number tiles with context, rings, tags, donuts with the total in the middle, labelled meter bars, Fix these first for feeds, In brief written by code, header with logo and file line, Data quality badge). Palette kept (validated), in the sample's order: green, amber, blue, orange, purple, pink. Custom dashboards: one scale a chart, pies as donuts. Code-like numbers (category numbers, ids, years) are never measures (`codeLike` in `ai.go` and `sheets.js`). |
| Guardian page (15 Oct; first called Self Healing System, renamed by the owner) | **Built, owner's request.** Left-menu page (`#healing`) that explains Guardian and has Connect an app: a name, the live address, and optionally the app's Git folder (a Projects folder name or a full path). Live address only: checked every 5 minutes (up, status, under 5 s), likely cause shown. Git folder: Guardian also fixes in the dev copy and asks before going live. Runs inside the main MangoMan process (`Server.Watch`, `StartHealing`) while it is open; list in `<home>/guardian/watch.json`, address-only configs in `<home>/guardian/apps/<name>/`. Approvals table moved to this page. Watch and diagnose (15 Oct, owner's yes): apps connected from the page get `pages` (qa.Crawl, 10 pages, daily), `dates` (certificate under 14 days, domain under 30 days via rdap.org, daily) and journeys (owner's words, a model writes go/click/fill/see steps, tried once, then hourly; `qa.Journey`, `internal/qa/journey.py`). A failure on an app without code gets a fix request to paste into its builder (`Event.Request`, one model call per new problem). The page shows a 24-hour strip per app (`Guardian.Strip`). |
| Mango Arch phase 1 (15 Oct) | **Built, not live.** Shared learning: opt-in hourly counts per free model (Settings), a Cloudflare Worker (`server/arch`) that turns per-network votes into signed "try this model last" lessons, and a client that applies them (reorder only, at most 3 hours). Off in every build until the owner deploys the Worker and sets the `ARCH_URL` and `ARCH_KEY` repository variables (`server/arch/README.md`). |
| Next build | Owner to try Chargesheets, the new layout and the agents. Then: History page, more Chargesheets expert tabs (owner picks), the scheduler for agents that run on their own (Social Media Manager weekly, waiting on the owner's three answers). |

---

## 3. THE NEXT BUILD (agreed with the owner on 8 Oct)

Order: **first the consolidation (about half a day), then the easy setup
(about 3 to 4 days).** Show the plan in a table first; the owner approved the
direction, not the details.

### 3a. Consolidate the router's failure handling

**Status (9 Oct): done.** Commit 650affa; confirming run 20261009-065414 passed 7 of 7 (details in the PRD build log). Next: 3b.

**Why:** over 7 and 8 Oct, layers were added one at a time, each fixing a real
problem seen in a real run. Each works and is tested, but together they are
hard to reason about, and some overlap. The owner asked whether "we have gone
too far with the code"; the honest answer was "not in size, yes in this area".

**What exists today** (all in `internal/router`; verify in the code):

| Mechanism | Where | What it does |
|---|---|---|
| **Place in line (done 9 Oct)** | `standing.go`: `ruleFor` (the one rule), `Line` (`judge`, `allow`, `order`, `SkippedUntil`, `probation`) | Replaced the circuit breaker package and the tried-last map. `ruleFor` maps each outcome to strikes, tried-last time and the screen's reason word. 3 strikes in a row: skipped for 30 s, doubling up to 5 min, then one probe. `Handle` calls `judge` once per attempt; nothing else demotes. |
| Quota block | `internal/quota` (`Block`, `Allow`, `RetryAfter`) | A 429 blocks the key and model until the provider's reset (Retry-After, x-ratelimit-reset-*, OpenRouter's x-ratelimit-reset in unix ms). |
| Tried last | Now part of `standing.go`: `busyFor` 2 min (overloaded, empty answer), `silentFor` 10 min (no first word in time), `garbledFor` 30 min (leaked control tokens) | Moves the model behind the others; never removes it, so a pinned (strict/) model is still asked. |
| Health ranking | `health.go`: `Rate` (needs 3 samples), `Speed` (needs 2 timed samples) | Feeds the candidate score. |
| Retry after a dropped stream | `router.go` Handle loop: a `stream_error` candidate is appended again, `maxRetries` 2, 1 s pause (`asked` map) | For overloaded providers. |
| Garbled stop | `stream.go`: `controlToken` regexp | Before the answer starts: fail over. After: error event to the client. |
| Empty answer stop | `stream.go`: reasoning only, then the end | Holds back `[DONE]`, sends an error event so an unattended run continues. |
| Time limits | `router.New`: `StreamIdle` 30 s (no first word; set from measurement on 8 Oct), `StreamStall` 180 s (gap after the answer started), `NonStreamTimeout` 180 s | |
| Wait told to clients | `plan.go` (`EarliestReset` includes `Line.SkippedUntil`), `router.go` `activity.waitUntil` (`maxHold` 2 min) | Retry-After counts models only cooling off; watchdogs treat a told-to-wait client as busy. |
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

**Status (9 Oct): built.** What was built, and what changed from the notes
below (the notes are kept as they were written):

| Part | Built |
|---|---|
| A | `internal/pyenv`: uv 0.12.24 pinned by checksum per system installs Python 3.12.15 and `requirements.txt` (made by `scripts/lock-python.sh` in `pylock.yml`, every package with checksums) into `<home>/python`. Only `python3` goes first in PATH (`env/shim`), so users' own `python` and `pip` stay theirs; `pyenv.Use` also turns on Python's UTF-8 mode (Windows printed ₹ through cp1252 and five packs crashed). |
| Packages | **pandas is kept**: the three data packs have the model write `analysis.py` with it (the note below was wrong). **Playwright is kept** (owner, 9 Oct): `render.inspect` and `render_frames` need it for layout checks; it drives the computer's own Edge or Chrome (`render.launch`), no browser download. |
| B | `render.py`: Windows paths added, `file_url` fixed for Windows paths, plain message with no browser, fallback to Playwright's own Chromium. The web app checker and `mangoman qa` use `render.launch` too. |
| D | `mangoman` alone: setup page (`ui/setup.html`) until set up, the dashboard after; one line in the terminal. Endpoints in `internal/ingress/ready.go`. `mangoman ready [--yes]` is the same in a terminal. |
| Sizes (Windows, measured) | OpenCode 62 MB, uv 16 MB, Python 22 MB, packages 75 MB (Playwright 39 MB, pandas and numpy about 23 MB): **about 175 MB**. Python folder on disk: 333 MB. Get ready took 18 to 24 s in CI. |
| Code signing | **Stays unsigned (owner, 9 Oct).** Windows Artifact Signing is not open to individuals in India; an OV certificate is from about USD 219 a year; Apple USD 99 a year. |
| Agent sandbox | Allows Python's own socket pair on Windows (asyncio, so Playwright), checked by code object and listener address; a faked pair is refused (found by the review). |

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
| Dashboard look (15 Oct, owner: "make this a worldclass UI") | Tokens in `ui/app.css`: page #f3f5ef, ink #15241c, leaf #1e6b47 for actions, mango #f2a516 only for what is alive or new (active menu bar, badges, fix requests), dark mode with the same roles. Headings in Bricolage Grotesque (OFL, `ui/fonts/`, a 40 KB Latin subset served from the binary, CSP `font-src 'self'`); body in the system font so every language reads well. Icons are inline SVG symbols. The one bold element is Guardian's 24-hour strip. Setup and coding pages share the file. |
| "Guardian" in the left menu, after Super Agents (first "Self Healing System", renamed 15 Oct); "Mango Arch" is the name of the shared learning (owner, 15 Oct) | Owner's names. |
| Mango Arch: lessons verified automatically, the owner gets a daily report; sharing covered by the privacy policy, accepted once; universal, not India-first; start on a free tier (owner, 14 and 15 Oct) | Phase 1 only learns what can be checked by machine (model health by per-network quorum). Knowledge lessons (facts) need multi-source checking and are designed, not built. The privacy policy text needs a lawyer; until it exists, consent is the Settings checkbox. |
| Left menu: + New task, Super Agents, Chargesheets, Templates, My Files, History; bottom: Connections, Usage, Settings (owner, 15 Oct) | Owner: "like Claude's sidebar". "Chargesheets" is the owner's name for the data dashboards (built 16 Oct; decisions: a feature not an agent, upload or Google link, Ask answers from queries code runs, packs added one at a time). |
| The coding screen stays in its own tab, not inside the app page (15 Oct) | The pages refuse to be framed (frame-ancestors 'none', X-Frame-Options DENY) against clickjacking; kept. |
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
| Release from Netlify, dropped by hand; the owner's own domain later (owner, 13 Oct) | Replaces "a separate public repo": only built files go to Netlify, the code stays here. Free plan: 300 credits a month, 15 per deploy, 20 per GB served (about 1,000 downloads a month); past that the site pauses until the next month. Automatic deploys need a Netlify token (`NETLIFY_AUTH_TOKEN`), later. |
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
| Longest pause inside an answer, after its first word (run 20261009-174404, 117 answers): Kimi K3 117.9 s (SEO), Nemotron 3.5 Lightning 91.4 s (ad copy), Nemotron Ultra 84.1 s (email), Nemotron Super 34.0 s; none over 120 s. Nemotron Ultra writing website copy's whole site.json: about 171 s (065414) and over 180 s twice (165742): the reason for the 300 s stall limit | pack runs 20261009-174404, 065414, 165742 (serve.log "longest pause" since 20c2269) |
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
| `windows-check.yml` | dispatch or manual only | Windows the user's way with the stand-in model: `mangoman ready`, OpenCode runs pack commands in Windows PowerShell 5.1, all pack samples; reports under `reports/windows/` (about 15 minutes, Windows minutes count twice) |
| `pylock.yml` | dispatch or manual only | locks the packs' Python packages and measures sizes per system; result under `reports/pylock/`, copy into `internal/pyenv/` |
| `sheets-test.yml` | dispatch (`sheets-test`) or manual only | Chargesheets end to end with real models through a served MangoMan; report, router log and console log under `reports/sheets-test/` on eval-reports (about 100 free requests, 15 to 40 minutes) |
| `dash-test.yml` | dispatch (`dash-test`) or manual only | the free models on the AI jobs of a data dashboard (read, plan, insight) for 5 made-up files; report under `reports/dash-test/` |
| `mac-check.yml` | dispatch or manual only | the Windows check's steps on macos-latest (Apple silicon): `mangoman ready` in the usual folder (~/Library/Application Support/mangoman), OpenCode runs pack commands in zsh, all pack samples; reports under `reports/mac/`. Gatekeeper is not covered (a CI build has no quarantine flag) |

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
| Start the Windows check, or a new lock | event_type `windows-check`, `mac-check` or `pylock`, no payload. |
| A failed job's error | `gh api repos/pusyc74-prog/MangoMan/check-runs/<id>/annotations` shows the error lines (the Actions logs are blocked). |
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

**OpenCode and Python locally:** `MANGOMAN_HOME=<dir> mangoman ready --yes`
(pinned versions and checksums). In the session's sandbox PyPI and uv's
Python host are blocked: set `UV_PYTHON_INSTALL_MIRROR=https://github.com/astral-sh/python-build-standalone/releases/download`
and expect the package step to fail; CI (`windows-check.yml`) runs it fully.
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
- **Super agents (16 Oct):** Social Media Manager, Marketing Strategist and
  SEO Strategist start from a brief form (`internal/ingress/brief.go`,
  `openBrief` in `ui/app.js`); the strategy report pack; 5 know-how skills
  (MIT, adapted from coreyhaines31/marketingskills) and 3 agent playbooks
  (`kind: method` and `kind: agent` in SKILL.md metadata); the Skills
  library and agent builder (`internal/ingress/library.go`), with the
  user's own skills in `<config>/opencode/skills` (no marker file).
- **18 skill packs** with builders and checkers (scripts render and check;
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
- **Chargesheets** (section 2): `internal/sheets`, `internal/ingress/sheetsapi.go`, `ui/sheets.js`.
- `mangoman qa`, `review`, `tests`, `changelog`, `new`, `doctor`, `usage`,
  `radar`, `setup` (terminal wizard), PRIVACY.md, README.

---

## 11. Open items (not built), most important first

| Item | Notes |
|---|---|
| Super agents: not yet run on a real model | The three flagship agents and the strategy report were tested with sample files and browser checks only. Next: `scripts/pack-run.sh` by hand on one brief each (spends free requests), then fix what the free models get wrong. Web research quality on free models is not measured. |
| Your own key for a better brain | Owner, 16 Oct: users may add a paid key for a stronger model; free keys stay the default; it is the user's choice, no promotion unless a provider has an affiliate programme. Not built: the catalogue and router hold free models only, so paid providers need entries, prices shown before use, and a rule that paid keys are used only when the user picks them. |
| Chargesheets: what is not done | Private Google Sheets (needs Google sign-in and Google's app review: owner said later). Shopify and Meta connections not yet run against a real store or ad account (stand-in servers only); Google Ads API direct (needs Google's approval; the Sheet route works now). Old .xls files (the user is told to save as .xlsx). Over 300,000 rows a table. Ask speed depends on the free models: 10 to 50 s usually, once 224 s in the test. The readout and Ask quality depend on the model the router picks; their numbers are always checked, the words are not. More expert tabs (finance, leads, stock) one at a time. The owner's own Ozonics file was never used in a test: it is client data and the repo is public. |
| Owner tries the download site | Drop `dist/site` on Netlify, then install with the line on the page (Windows and Mac). |
| Real-model run on Windows | Only the stand-in model ran on Windows so far; a pack run on a Windows runner would check real models' commands in PowerShell (quoting, `&&`). |
| `mangoman ready` in a terminal and Get ready on the page at the same time | Both install into the same folder; no lock between processes (rare). |
| packs.yml and qa.yml still download Playwright's Chromium | Unused now that the runner's Chrome is used; could be dropped to save minutes. |
| Mac and Linux downloads need `chmod +x` and, on a Mac, Terminal | The artifact zip drops the program flag (upload-artifact keeps no permissions). A `.tar.gz` per system (keeps the flag) or a Mac `.app` in the release would remove the Terminal step; part of the release work. |
| Website copy writes `site.json` page by page | Owner, 9 Oct: a separate change with its own scored run; **not needed so far**. After the JSON error helper (3ef2091) and the facts message (cc02610), 6 of 6 website copy runs passed (100, 82, 97, 94, 100, 97). Revisit if site.json breaks again in a way the helper cannot name, or if a pause nears the 300 s stall limit (longest seen: 161 s, Kimi K3 and Nemotron Ultra). |
| Costly fix loops in packs | Ad copy 100 requests, website copy 156 in one run (8 Oct). Website copy: done for JSON errors (`checks.load_json` names the exact fix; first real case fixed in one edit, run 20261009-200959) and for facts missing from site.json (check_copy says where they go). Other packs that read model-written JSON (ad copy, listing, email and more) could use `checks.load_json` too. |
| Windows stall watchdog | No pgrep on Windows, so any run counts as "command running" and a stall is caught only after 10 minutes (Mac fixed 10 Oct: it needs `pgrep -lf`). Could use PowerShell `Get-CimInstance Win32_Process` for child processes. |
| Screen shows raw model ids ("kimi-k3") | Use display names. |
| Agents not built (15 Oct) | Customer Support, Appointment Scheduler, Lead Follow-up (need app connections and a scheduler) and Market Research (OpenCode's web search is on but untested for free users, and no pack checks a research report). Listed as coming later on the Super Agents page. |
| History page | Placeholder. OpenCode keeps the chats (the coding screen resumes the last one); a page listing them needs OpenCode's session list through the router. |
| Social media agent (owner, 13 Oct) | The coding screen built a planner page and 5 posts and called it an agent. Nothing in MangoMan runs on its own yet (no scheduler, no posting). Asked the owner: which platforms, post automatically or draft for approval, how often. Facts given: LinkedIn and Telegram posting free; Instagram and Facebook need a Business account, a Page and a Meta app; X's free API is limited (check current terms); WhatsApp Status has no API; runs only while the computer is on. |
| NVIDIA slow at Indian evening hours (13 Oct, owner's PC) | kimi-k3, then deepseek-v4.1-flash, missed the 30 s first-word limit at 23:00 IST; the doctor check at 15:10 IST had kimi-k3 first word in 1.3 s. When every model of a provider is slow, hopping restarts the wait. Measure before changing the 30 s limit (it came from measurements); maybe more time for large first requests. |
| "Next if it runs out: kimi-k3 (nvidia), kimi-k3 (nvidia)" | Shown once per team key on the dashboard; show each model once. |
| The waiting seconds on the screen sometimes jump back when two requests run | Cosmetic. |
| NVIDIA daily cap | Not measured end to end. |
| Garbling cause | Not proven; a test sending a sensible temperature (for example 0.6) could tell. |
| Quality after a mid-task model switch | Not measured. |
| Release, next steps | Automatic Netlify deploy from ci (token); the owner's domain; Windows in the background like the Mac (the Mac app runs in the background since 19 Oct: `mangoman app`, Stop MangoMan on Settings; Windows still opens a black window); Windows Smart App Control (new Windows 11 installs) may block the unsigned app whatever the install route. Code signing is the owner's cost decision. |
| A Windows test failed once (19 Oct) | ci run 39213185632: other-systems (windows-latest) failed in Test, then passed on a rerun of the same commit. The log could not be read from the session (log storage blocked), so the test is not known. If it fails again, read the log on github.com. |
| mac-check (19 Oct) | Run 39212621552 passed every step: install, the app in the background (opened twice, one MangoMan, stopped from the page), Get ready, OpenCode in zsh, every pack sample. The 13 Oct hangs are not seen since; the app had never run the real program before 19 Oct (the launcher overwrote it, a Mac ignores case in names). |
| `OPENCODE_ZEN_API_KEY` secret | So the doctor also checks Zen. |
| Guardian real run | Needs a Telegram bot from @BotFather and a real app. |
| Payments, revenue share | Razorpay or Stripe; owner decision. |
| Guardian page: known limits (15 Oct review) | Checks and first-aid commands run in MangoMan's own folder, not the config's (relative scripts in a hand-written guardian.json break when connected from the page). A Mac app opened from Finder has a short PATH, so npx or docker may not be found. A terminal `guardian run` and the page watching the same app both work on its incidents (no lock between processes). Likely-cause lookups use the configured port, not a `serve --port` override. Netlify dev deploys may need a build step first. |
| Data Analyst on a real file (owner, 15 Oct 17:54 IST) | The agent asked three times for the file path, asked about a dozen harmless commands (mkdir, Copy-Item, Get-Item, the skill's own script), and the model hunted for Python (python, where, whereis, dir C:\Python*) for minutes. Fixed: Data Analyst, Pitch and Board Deck and Resume and Job Helper cards have Choose file; the file is saved in MangoMan Projects/<file name>/ (`POST /mangoman/files/upload`, 50 MB, documents, sheets and pictures only) and the first message gives its exact place; in MangoMan Projects the coding screen now asks only about commands that delete, move, download, install, publish or change the computer (`projectsAsk` in cmd/mangoman/assist.go; developer folders still ask about everything but reading); a rule says Python is python3. Not done: a test with a real model on such a file (the owner's file is client data and the repo is public, so it must not become a test case). The owner's log at 17:48 still showed kimi-k3, so the PC was on the build before 30c3e9b. |
| NVIDIA models (15 Oct, owner's PC and nvidia-check.yml at 17:25 IST) | Kimi K3 retired by NVIDIA on 15 Oct 08:00 UTC (410 Gone), removed from the catalogue (seed-2026-10-15.1). Live: gpt-oss-20b 1.3 s, nemotron-3-ultra 1.4 s, gemma-4-31b 2.2 s, nemotron-3-super 13.5 s. Not answering within 90 s: deepseek-v4.1-flash, glm-5.3-flash (both also timed out all morning in the search test). kimi-k2.6 listed but 404. The router now skips a retired model for a day and never retries a silent or retired model on another team key in the same request. Rerun nvidia-check.yml before changing the catalogue again. The doctor's daily reports stopped reaching the doctor-reports branch after 13 Oct although the runs pass: find out why. |
| Web search: Exa or Parallel (15 Oct test) | search-test.yml (run 38688426331, report on eval-reports): Parallel had the answer in 19 of 19 search results (0.7 s), Exa 15 of 19 (17 of 19 in the run before; misses came back in 0.2 s, likely its keyless limit). With either, kimi-k3, glm-5.3-flash and gemma-4-31b answered 15 to 17 of 17 recent questions; from memory 0, and all said "I don't know" instead of guessing. deepseek-v4.1-flash on NVIDIA timed out on all 57 requests (the run went from 11:15 to 11:42 IST). Switching OpenCode to Parallel is one environment variable (`OPENCODE_ENABLE_PARALLEL=1` or `OPENCODE_WEBSEARCH_PROVIDER=parallel` instead of `OPENCODE_ENABLE_EXA=1` in `cmd/mangoman/assist.go`); check Parallel's terms for keyless use first. Owner decision. |
| Mango Arch: go live | Owner: Cloudflare account, then the steps in `server/arch/README.md` (about 15 minutes), then the two repository variables. Then: the privacy policy (lawyer) and its one-time acceptance on the setup page; knowledge lessons (phase 2, see the PRD). |

---

## 12. Waiting on the owner

| Item | Status |
|---|---|
| Switch the repo to private | **Answered 9 Oct: keep it public for now.** Release still waits for a private repo (section 4). |
| Which computer to test on | **Answered 9 Oct: Windows.** Make the easy setup work on Windows first (the `python3.exe` shim, Chrome and Edge paths, the console window). |
| Code signing (paid) | **Answered 9 Oct: stay unsigned for now.** |
| Try the easy setup on Windows | Asked 9 Oct. |
| OPENCODE_ZEN_API_KEY, MARKETPLACE_KEY | Open. |
| Telegram bot, a real app for Guardian, a Windows tester, first beta users | Open. |
| Mango Arch server | Open: Cloudflare account and deploy (`server/arch/README.md`). |

**How the owner can try MangoMan today** (told on 8 Oct): GitHub, Actions,
`ci`, Run workflow; when done, download the `mangoman` artifact (one file per
system); rename to `mangoman` (`mangoman.exe`); in a terminal: `mangoman setup`
(NVIDIA key first), `mangoman serve`, `mangoman dashboard`, `mangoman test "hello"`,
and in an empty folder `mangoman code --ui`. Skill packs need **Python 3**
plus `pip install pillow python-pptx python-docx pypdf playwright` and
`python -m playwright install chromium`; ad copy and email need only Python.
Mac (macOS 15 and later): `chmod +x mangoman` once (the artifact zip drops the program flag), then System Settings, Privacy & Security, Open Anyway (right-click Open no longer works); Windows: More info, Run anyway. **Since
9 Oct (3b):** just double-click `mangoman.exe`; the setup page does the rest
(no Python, no pip, no terminal).

---

## 13. Lessons (so they do not repeat)

| What went wrong | Do instead |
|---|---|
| 13 Oct: the download site sent to the owner was built in the session, whose GOFLAGS (`-modfile=/tmp/dev.mod`) swap the key store for a stand-in; on Windows it said "could not store key: stub" | Releases come only from ci's build job (its artifact is the site folder). `make-site.sh` now empties GOFLAGS, so a session build fails instead. |
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
| 9 Oct: planned to drop Playwright; the owner caught that `render.inspect` and `render_frames` need it | Before removing a dependency, find every call, including optional paths. |
| 9 Oct: said pandas was used by one script; the model's own `analysis.py` uses it in three packs | Search the SKILL.md instructions and test samples too, not only the scripts. |
| 10 Oct: every test moved MangoMan's folder somewhere temporary, so no agent had ever run from the usual folder, where the sandbox refused its own files; the first Mac check found it | Test the way users run it: the usual folder too, not only a temporary one. |

---

## 14. What MangoMan is (one paragraph)

A free, local-first AI router in Go. It pools every free model the user can
reach (NVIDIA, Groq, OpenRouter, OpenCode Zen, Ollama; Cerebras optional),
switches automatically when one runs out or fails, and never sees prompts or
keys. On top: 18 skill packs, advanced agents with a signed creator
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
