# Handoff: read this first

This file lets a new Claude session continue MangoMan exactly where the last
one stopped (6 Oct 2026). Read it, then CLAUDE.md, then docs/PRD.md, and say
back your understanding before building anything.

## Who the owner is and how they work

- Building MangoMan for **non-technical Indian users** (small businesses,
  agencies, creators). Plain words, no jargon, Hindi later.
- Likes **crisp answers with tables**. No long essays.
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
  Windows), qa.yml (nightly, race tests, dashboard browser check, Guardian),
  doctor.yml (twice a day), agent-eval.yml.

## The PRD

The live PRD is a Claude Docs document "MangoMan: Technical PRD" in the old
account. A full copy as of 6 Oct 2026 is in **docs/PRD.md**. In the new
account, create a new Claude Docs document from it and keep its **Build log**
table updated with every change (date, item, what changed), newest first.

## What MangoMan is (one paragraph)

A free, local-first AI router in Go. It pools every free model the user can
reach (Groq, Cerebras, NVIDIA, OpenRouter, OpenCode Zen, Ollama), switches
automatically when one runs out, and never sees prompts or keys. On top:
16 skill packs (marketing, reports, documents, tech), advanced agents with a
signed creator marketplace, OpenCode as the coding engine (`mangoman code`),
a QA agent, and **Guardian**, the self-healing system.

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
- 16 skill packs with builders and checkers; test sets for 6; 2 advanced
  agents (amazon-listing-pro, google-ads-pro); signed `.mmagent` packages,
  sandbox, safety review, eval harness, registry (closed until a key exists).
- `mangoman qa`, `review`, `tests`, `changelog`, `new`.
- Guardian: watch, first aid, fix on own branch, QA writes tests (must fail
  without the fix), code review blocks secrets, dev deploy and QA, up to 3
  rounds, Telegram approval, production deploy and QA, rollback, version
  check, nightly masked data (SQL, JSON, CSV), reports 08:00 and 20:00,
  dashboard approvals.
- PRIVACY.md.

## Waiting on the owner

- CEREBRAS_API_KEY and NVIDIA_API_KEY as GitHub secrets (and
  `mangoman keys add cerebras` locally): unblocks real-model agent evals and
  the first real Guardian run. Groq's free tier refuses most coding requests.
- OPENCODE_ZEN_API_KEY secret (doctor coverage).
- MARKETPLACE_KEY secret (opens the agent marketplace).
- Revenue share for paid agents; Razorpay or Stripe.
- A Telegram bot from @BotFather; a real app to point Guardian at.

## Still open (not built)

- Measure answer quality after a mid-task model switch (needs real keys).
- Remove dashboard training labels (only if the owner asks).
- Guardian has only been tested with a stand-in AI, not real models yet.

## Separate: Project B

The owner has a separate Project B (superagents built from these skills).
It is **not** part of MangoMan: do not add it to the PRD or the code. They
have an Excel reference and a zip of the skill packs.
