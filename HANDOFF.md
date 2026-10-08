# Handoff: read this first

This file lets a new Claude session continue MangoMan exactly where the last
one stopped (6 Oct 2026). Read it, then CLAUDE.md, then docs/PRD.md, and say
back your understanding before building anything.

## Who the owner is and how they work

- Building MangoMan for **non-technical Indian users** (small businesses,
  agencies, creators). Plain words, no jargon, Hindi later.
- Likes **crisp answers with tables**. No long essays.
- **PRD updates are silent.** Keep updating both PRD copies with every change and commit them to Git, but do not mention them or show the PRD in replies. Share it only when the owner asks for it.
- **Discusses before building.** When they say "let's discuss" or "before we
  build", answer and propose; do not code. When they say "start" or "build",
  build without asking again, test, commit, push.
- When they are away, keep building one item after another, testing and
  fixing as you go.
- Often asks "is there something like this already?" Answer honestly,
  including when a tool already does it.
- Hard rules (also in CLAUDE.md): **no em dashes** anywhere; **never mention
  the former company name**; keep the code small (no dead code, reuse what
  exists); vet, test and check before every commit; **record every change in
  the PRD build log**.
- Never use the harness's own credentials. Kill processes by PID, never with
  `pkill -f` (it killed the shell before).
- Commits: identity pusyc74-prog / pusyc74@gmail.com; repo
  github.com/pusyc74-prog/MangoMan (branch main). CI: ci.yml (Linux, Mac,
  Windows, plus the pack regression and the lean check, and nightly for those
  two), qa.yml (nightly, race tests, dashboard browser check, Guardian),
  doctor.yml (twice a day), agent-eval.yml, pack-run.yml (real-model pack
  runs, by hand only).

## M5 parked (7 Oct)

M5 (the app for everyone) is parked by the owner: teachers and shop owners
cannot bring their own keys, and serving them without keys needs servers and
a paid AI allowance. Do not start it unless the owner reopens it. M6 (coding
workspace) is under discussion instead.

## The repo is public (7 Oct)

**Switch the repo back to private before the first release or the beta**
(the owner's decision, 7 Oct: public only while we build, for the free
minutes). Then: Linux-only checks on GitHub, heavy runs on a self-hosted
runner on the owner's computer, and releases from a separate public repo that
holds only the compiled app.

The owner made the repo public on 7 Oct, after the private repo's free
Actions minutes ran out and every job was refused ("recent account payments
have failed or your spending limit needs to be increased"). Public repos get
standard runners free. History was scanned for keys first: none. Never commit
a key, a .env file or a signing key. **Before starting anything that uses
paid or limited resources (Actions minutes, API credits), say what it costs
and what limit it could hit, before running it.**

## Watching CI from a session

The GitHub Actions API is blocked from the session's network, so
`gh run list` and `gh secret list` fail. These work and are enough to watch a
run: `gh api repos/pusyc74-prog/MangoMan/commits/<sha>/check-runs` (job names,
status, conclusion) and `gh api repos/pusyc74-prog/MangoMan/issues` (the QA
workflow opens one when the nightly fails). Logs and step summaries are not
reachable, which is why the pack run publishes its report, its per-case logs
and the router log to the **eval-reports** branch: clone that branch to read
a result.

A session can also start a pack run without the Actions page:

    gh api -X POST repos/pusyc74-prog/MangoMan/dispatches \
      -f event_type=pack-run \
      -F 'client_payload[cases]=1' -F 'client_payload[case_timeout]=600'

`client_payload` takes model, packs, variant, cases, case_timeout and runner.
Leave cases out for the full set of three per pack.

## Self-hosted runner (once the repo is private)

A private repo gets 2,000 free Actions minutes a month; heavy runs (pack runs,
Mac and Windows tests) go to a runner on the owner's computer, which GitHub
does not bill. **Never add a self-hosted runner while the repo is public**: a
pull request from anyone could then run code on the owner's computer.

Owner's steps (about 10 minutes, once):
1. GitHub repo > Settings > Actions > Runners > New self-hosted runner; pick
   the computer's system and run the commands shown (download, `./config.sh`
   with the token shown, then `./run.sh`, or install it as a service).
2. Linux or macOS works best: pack-run.sh is a bash script and uses
   `timeout` (on macOS: `brew install coreutils`). The workflow installs Go,
   Python, Node, OpenCode and the Chromium for Playwright by itself.
3. The computer must be on and online while a run is going.

Start a run on it: `client_payload[runner]=self-hosted` (or runner:
self-hosted from the Actions page).

## Two standing rules the owner confirmed on 6 Oct

- **Sweep for bugs regularly, do not wait for a wave to end.** Every push
  runs vet, race tests on three systems, the pack regression and the lean
  check. Every night ci.yml reruns the pack regression and the lean check,
  qa.yml runs the race tests and clicks through the dashboard in a browser
  with Guardian watching, and doctor.yml checks every provider twice a day. A
  failing nightly qa opens or updates a GitHub issue. On top of that, end
  each wave of work with a read-through for bugs and dead code.
- **Keep the code lean.** No dead code, no unused options, no abstraction
  with one caller; reuse the Go packages under `internal/` and the pack
  helpers in `internal/skills/packs/shared/`; prefer deleting to adding. The
  lean job in ci.yml fails the build on dead code (functions, types, fields
  and constants nothing uses) and on assignments that never take effect, so
  this is checked, not just promised.

## The PRD

The live PRD is a Claude Docs document "MangoMan: Technical PRD" in the old
account. A full copy as of 6 Oct 2026 is in **docs/PRD.md**. In the new
account, create a new Claude Docs document from it and keep its **Build log**
table updated with every change (date, item, what changed), newest first.

## What MangoMan is (one paragraph)

A free, local-first AI router in Go. It pools every free model the user can
reach (Groq, Cerebras, NVIDIA, OpenRouter, OpenCode Zen, Ollama), switches
automatically when one runs out, and never sees prompts or keys. On top:
17 skill packs (marketing, reports, documents, tech), advanced agents with a
signed creator marketplace, OpenCode as the coding engine (`mangoman code`),
a QA agent, and **Guardian**, the self-healing system.

## About the owner

- **Why MangoMan exists:** to serve people left out of the AI tooling boom:
  non-technical Indian users who cannot pay for AI or wire up free tiers.
  Free, private and honest is the point. Judge every idea against it.
- **How ideas grow:** the owner starts with a feature and grows it into a
  system (Guardian began as a monitor and became a self-healing loop with a
  permanent dev environment). When you see a bigger version of an idea,
  propose it, in a table, and let the owner decide. Do not build it unasked.
- **Honesty:** say so when something already exists, when a number is a
  guess, and when a check could not be run.
- **Guardian, in full:** every app gets a dev environment from day 1, next to
  production, same code. The owner's own coding goes through dev too
  (`mangoman code`, then `mangoman guardian ship`). Production gets only
  tested changes, after the owner approves on Telegram.
- **Data sharing is a deliberate call:** the free-model training notice
  lives only in PRIVACY.md, with no accept switch.
- **Timeline:** no fixed date and no day-wise plan. Keep building at full
  pace; the owner sets go-live once the build is complete. (The earlier
  target was about 16 Oct 2026.) Put first what gets MangoMan to real
  users fastest.
- **Money:** the only budget is the Claude subscription. No paid services,
  hosting, APIs or tools. Everything runs on free tiers (free model keys,
  GitHub Actions, free hosting) or on the user's own machine. If something
  would cost money, tell the owner first and give the free option.
- **Success, two goals:** (1) 10,000 people using free AI through MangoMan;
  (2) creators earning money from their agents on the marketplace. Judge
  every idea by whether it moves toward one of these two.

## Parked: do not reopen on your own

| Idea | Where it stands |
|---|---|
| Public LLM for the general public (M5 general chat) | Parked 4 Oct. OpenCode stays the engine; every user brings their own keys. |
| Chat history storage | Parked 3 Oct (proposed design is in the PRD). |
| Cloud-drive style storage for made files (Cloudflare R2) and the Supabase or Cloudflare D1 options | Parked 3 Oct (options are in the PRD). |
| M6 coding workspace screen | Later, after M5. |
| Cloud mode | Only if at all, opt-in, split-key design. |

## Launch (no day-wise plan)

- The owner decides go-live once the build is complete. Do not propose day-by-day schedules.
- Publish the builds CI already makes (Windows, Mac Intel and M-series, Linux) as a GitHub Release.
- Beta users start with the router and chat. Skill packs need Python, so they come after, with a simple guide.
- A user count must be opt-in and PRIVACY.md updated in the same step (it says no analytics and no tracking). Otherwise count downloads and stars.
- Waits until after launch: Guardian's first real run, payments, M5, M6.
- Needed from the owner: NVIDIA key as a GitHub secret, a Windows tester, MARKETPLACE_KEY before the marketplace opens, and the names of the first beta users.

## Free capacity and quality (decided 6 Oct)

- Cerebras needs a card for its $5 trial (2 models, 5 requests a minute): optional, skipped under the no-card rule. NVIDIA and OpenCode Zen carry coding.
- Beta: one model per task, ask first for weaker ones. No step-splitting across models.
- Later, test with the eval harness whether small or local models can do work the user never sees (sorting, trimming tool output, script-checked cleanup). Never for the writing or the final answer. Local models are a labelled fallback only.
- Before go-live: measure real requests and tokens per coding task and per pack task on NVIDIA; tell the owner how many tasks a free user gets a day.
- After launch: slim coding mode and a capacity meter. Heavy coding is not promised at launch; chat and packs are.

## Team keys (built 7 Oct)

Several people working from **one computer** each add their own key, for any
provider: `mangoman keys add <provider> --team NAME`, or Add team key on the
dashboard. Requests take turns across every key of a model; a key at its limit
rests and the next one carries on with the same model. Keys are stored as
`provider#name` in the key store; the names are in config.json `team_keys`.
The catalogue field `no_team_keys` switches it off for a provider without a
release.

The owner decided this after we read the terms together: every provider bans
one person making many accounts (not this case), but NVIDIA and Groq say a key
is for its owner's use. So a notice is shown and the key's owner accepts the
risk. OmniRoute already does the same openly. Do not reopen this decision.

This is **not** Phase 4 team mode, where each person is on their own device and
keys are never shared. Keep the two apart.

## My list and OpenCode (built 6 Oct)

- With a My list set, the router uses only those models. When all are busy or out of limits it stops and asks before using any other model (1 hour or always). No list: normal ranking.
- `mangoman code` offers to download OpenCode (about 60 MB, official release, no Node) into MangoMan's folder when it is missing.
- OpenCode is pinned to the tested version (internal/opencode, `Version` and `sums`), the download is SHA-256 checked, and an older copy gets an update offer. To move the pin: test the new release, then change `Version` and `sums` together.

## Decisions already made (with reasons)

| Decision | Why |
|---|---|
| OpenCode is the default **engine**; models come from providers | OpenCode is the agent, not a model source. Zen is OpenCode's model gateway and is already a provider. |
| Model order: strong before weak, roomy before tight, then Cerebras/NVIDIA, then Zen, then Groq/OpenRouter | Biggest free limits first; Zen's free models are temporary with no published limits. |
| Every user brings their own keys | Local-first; no shared-key scaling problem. |
| Zen free models on by default, training notice **only in PRIVACY.md**, no accept switch | Owner's call: free comes with consequences. Dashboard still shows "may be used for training" labels; owner may ask to remove them. |
| Ask before a clearly weaker model (dashboard: 1 hour / always) | Better to stop than give bad answers. |
| One model per chat (stickiness) | Switching mid-task hurts quality. |
| Doctor twice a day; OpenRouter list-only except Monday | OpenRouter allows 50 requests a day for the whole account. |
| Public LLM for the general public: **parked** | Not building now. |
| Chat history storage, M5 simple app, M6 coding workspace screen: **parked** | Later. |
| Guardian: permanent dev environment from day 1, next to production, same code | Bugs are tackled in dev in real time; production only gets tested changes. |
| No approval for dev; approval (Telegram) only before production | Owner's call. |
| First aid (restart/retry) stays on | App stays up while the real fix is made. |
| Own server: dev at dev.yoursite.com on the same server | Free. |
| Nightly masked copy of production data to dev | Realistic bugs without exposing customers. |
| Owner's own coding goes through dev too (`mangoman code`, `guardian ship`) | Nothing reaches production untested. |
| Prompting style: keep our structure, scripts and checkers; STE-style short imperative steps are a possible improvement | Checkers guarantee results; STE only clarifies wording. |

## What is built (all pushed, CI green)

- Router: failover, quotas learned live, skill floor, stickiness, provider
  priority, live model panel in the dashboard, load test (3,000 requests,
  300 at once, 0 failed).
- 17 skill packs with builders and checkers; test sets for 7; 2 advanced
  agents (not sold yet: payments are not open) (amazon-listing-pro, google-ads-pro); signed `.mmagent` packages,
  sandbox, safety review, eval harness, registry (closed until a key exists).
- `mangoman qa`, `review`, `tests`, `changelog`, `new`.
- Guardian: watch, first aid, fix on own branch, QA writes tests (must fail
  without the fix), code review blocks secrets, dev deploy and QA, up to 3
  rounds, Telegram approval, production deploy and QA, rollback, version
  check, nightly masked data (SQL, JSON, CSV), reports 08:00 and 20:00,
  dashboard approvals.
- PRIVACY.md.
- M6 coding screen (`mangoman code --ui`): chat with Plan and Build,
  approval cards, Changes, Terminal, Preview with phone, tablet and laptop
  frames (7 Oct).
- Team keys and per-person tokens (7 Oct); the dashboard's "Free AI you get
  each day" section (8 Oct).

## Waiting on the owner

- CEREBRAS_API_KEY as a GitHub secret (NVIDIA_API_KEY is set). Groq's free
  per-minute token limits (6,000 to 8,000) are smaller than one request of a
  skill task (about 14,500 tokens), so Groq cannot carry pack work.
- OPENCODE_ZEN_API_KEY secret (doctor coverage).
- MARKETPLACE_KEY secret (opens the agent marketplace).
- Revenue share for paid agents; Razorpay or Stripe.
- A Telegram bot from @BotFather; a real app to point Guardian at.

## What a task costs on free models (measured 6 Oct)

First real-model run, seven packs, one case each, through OpenCode on the
NVIDIA key. A **light** pack task (resume, social posts, ad copy, listing)
takes 4 to 6 requests and 30K to 70K tokens. A **heavy** one (website copy,
SEO article, email campaign) takes 32 to 37 requests and 0.8M to 1.0M tokens.
Against the free tiers, one heavy task is about five times Groq's whole daily
token allowance and most of OpenRouter's 50 daily requests, so NVIDIA has to
carry pack work. Reports, per-case logs and the router log are on the **eval-reports** branch
under `reports/pack-run/`; clone that branch to read a result.

Four runs so far, same seven packs, one case each. **Scores swing with
whichever model answers**, which is the output consistency risk the owner
raised: email campaign 100 in run 1 and 0 in run 3; ad copy 0 then 51. The
run pinned to one model (`strict/nemotron-3-ultra-550b`) is the repeatable
one: tasks finished in 75 to 295 seconds instead of running out of time, and
e-commerce listing scored 79, email campaign 90, resume 90, SEO article 93.5.

**Every pack now works on a real free model**, pinned to one model: website
copy 100.0, social posts 95.6, SEO article 93.5, email campaign 90, resume
90, ad copy 79.1, e-commerce listing 79. Two router bugs had to be fixed to
get there (see the PRD build log for 7 Oct): a stream that died before saying
a word is now asked again when there is nothing to fail over to, and the gap
allowed mid-answer grows to 180 seconds once the answer has started.

**Still unmeasured:** NVIDIA's daily ceiling. Its first 429 came at the end
of the last run, so the limit is real. Measure it before promising users a
number of tasks a day.

**8 Oct.** Installed packs no longer carry their `tests/` folder: models
were reading the scorer instead of doing the task (`mangoman agents eval`
takes the test set from the binary). After that, free/coder passed 6 of 7
tasks (it passed 1 of 7 the run before). The skill rule and slimmer tools cut
tokens per request by 39%; pinned Nemotron scores held or rose except ad copy
(an edit-one-line-at-a-time loop, pack 1.1 addresses it) and email (100 to
90). A 3-case run of those two decides whether the slim setup stays. Pass
pack names to `pack-run` with spaces or commas.

## Still open (not built)

- Measure answer quality after a mid-task model switch (needs real keys).
- Remove dashboard training labels (only if the owner asks).
- Guardian has only been tested with a stand-in AI, not real models yet.
- A Telegram bot from @BotFather and a real app to point Guardian at.
- OPENCODE_ZEN_API_KEY secret, so the doctor also checks Zen.

## Order of work

Real-model runs, bug sweep, then CI cut to the free private-repo minutes and the repo made private, then the beta on today's tool. M5 is parked; M6 is built.

## Separate: Project B

The owner has a separate Project B (superagents built from these skills).
It is **not** part of MangoMan: do not add it to the PRD or the code. They
have an Excel reference and a zip of the skill packs.
