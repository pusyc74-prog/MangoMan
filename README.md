# MangoMan

Free-first, local AI router. One signed Go binary on your machine pools every free model you can reach, fails over when one runs dry or gives a bad answer, and never sends your keys or prompts anywhere except the provider you are calling.

Status: **Phase 1, milestones M1 (Foundations) and M2 (Routing core)**. See the [PRD](https://claude.ai/code/artifact/cca2344e-8ddc-431f-b053-6c0b2617c29e) and the [Phase 1 build plan](https://claude.ai/code/artifact/1b002e4f-d746-47c0-be64-d6fad377d2d0).

## Quick start

```sh
go build -o bin/mangoman ./cmd/mangoman
./bin/mangoman setup           # guided: connect free providers one by one
./bin/mangoman serve           # http://127.0.0.1:4141/v1
./bin/mangoman dashboard       # opens the dashboard in your browser
./bin/mangoman test "hello"    # shows provider, model, attempts, data policy
./bin/mangoman doctor          # live-checks every connected provider and model
./bin/mangoman usage           # requests, failovers, tokens, latency
```

Point any OpenAI-compatible tool (Cursor, Cline, Continue, n8n, your code) at `http://127.0.0.1:4141/v1`, use the local token from `init` as the API key, and pick a model: `free/auto`, `free/coder`, `free/writer`, `free/fast`, `free/long`, or a real model such as `llama-3.3-70b`.

Every response carries `X-MangoMan-Provider`, `X-MangoMan-Model`, `X-MangoMan-Attempts`, `X-MangoMan-Class` and `X-MangoMan-Data-Policy`.

## Test in the cloud (nothing to install)

Push this repo to GitHub (private is fine), then use either option. Keys go in as GitHub secrets, become environment variables, and are never written to disk.

### Option A: GitHub Codespaces (interactive)

1. Add `GROQ_API_KEY` (and any other provider keys) at github.com/settings/codespaces > Secrets, and give them access to this repo.
2. On the repo page: Code > Codespaces > Create codespace. Setup builds MangoMan automatically.
3. In the codespace terminal: `scripts/try.sh` (or `scripts/try.sh --full`).

It starts the router, sends a plain and a streaming request, runs `doctor` and shows `usage`. After that, use any command from Quick start in the same terminal. Commit `go.sum` from the codespace once, since setup completes it.

### Option B: GitHub Actions (repeatable)

1. Add the same keys under repo Settings > Secrets and variables > Actions.
2. Actions tab > doctor > Run workflow (quick or full, optionally one provider).
3. The results table appears on the run page; the JSON report is attached as `doctor-report`.

It also runs daily at 08:00 IST in quick mode, which catches provider changes early.

## What M1 includes

| Area | Done in M1 |
| --- | --- |
| Endpoint | `POST /v1/chat/completions` (streaming and non-streaming), `GET /v1/models`, `GET /mangoman/status`, `GET /healthz` |
| Local security | Binds 127.0.0.1 only; local bearer token (or `x-api-key`); Host check against DNS rebinding; browser calls refused unless they come from the dashboard |
| Providers | One OpenAI-compatible adapter covering Groq, Cerebras, OpenRouter (free), NVIDIA, Ollama (auto-discovered) |
| Routing | Free-first scoring (quality, quota left, health, speed) per task class; same model on another provider first; local Ollama as last-resort backstop |
| Failover | 429 (bucket blocked until reset), 5xx and timeouts (circuit breaker), 401/403 (key disabled), 404 (model avoided), unreachable provider (skip its other models), client errors (one retry, then return) |
| Streaming | Events held until the first real token, so empty or broken streams fail over invisibly; idle timeout; clean error event if a stream breaks after output started |
| Quality guard | Empty, truncated, invalid JSON (when JSON mode asked), malformed or unknown tool calls; if every answer fails, the first is returned with `X-MangoMan-Guard` |
| Quota | Per provider, account, model: requests and tokens per minute and per day; pre-flight check; corrected from rate-limit headers; survives restarts |
| Keys | OS keychain; encrypted file fallback; env var overrides; validated on add |
| Catalogue | Embedded seed with limits and per-provider data policy labels; Ed25519 verification ready for the live feed (M5) |
| CLI | `init`, `serve`, `keys add/list/rm` (with `--team NAME` for team keys), `people`, `code [--ui]`, `status`, `models`, `test`, `version` |
| Usage log | `usage.jsonl`: outcome, latency, tokens per attempt. Never prompt or answer content |

## Setup wizard and dashboard

`mangoman setup` walks through each free provider in turn: it opens the sign-up page, you paste the key, it is checked with the provider and stored in the OS keychain, or in an encrypted file when there is no keychain. Skip any provider; one is enough. It also detects Ollama and offers to download a small local model.

`mangoman dashboard` opens a local page (served by the router, nothing loaded from the internet) that shows:

- Which providers are connected, not connected, turned off or rejecting their key, with Connect, Remove key and Turn off/on in each row
- How much free AI you get each day: for each connected provider, its free limits across all your keys and about how many big and small skill tasks they allow, with the method
- Requests per hour for the last 24 hours, by provider
- Every model: state (ready, rate limited, cooling down), requests, success rate, typical speed, tokens, free limit (catalogue or reported by the provider) and whether your data may be used for training
- Recent requests with their outcome (never their content)

The page gets the local token through the URL fragment, which browsers never send to servers or logs, and runs under a strict content security policy. Requests from other websites are refused.

Routing also uses measured speed: after two timed answers from a model, its real response time replaces the catalogue estimate, so slow models sink in the ranking.

## My list and new models

**My list** is your ordered set of preferred models. Every request tries them first, top to bottom; when they are used up (rate limited, cooling down after errors, or missing a capability the request needs), MangoMan stops and asks before using any model outside the list (dashboard: allow other models for 1 hour or always, or the `X-MangoMan-Allow-Weaker: 1` header). With no list set, it uses its normal best-free-model ranking.

| Entry | Meaning |
| --- | --- |
| `kimi-k3` | that model on any connected provider |
| `groq/gpt-oss-120b` | that model on Groq only |

**New models.** The router lists each connected provider's models every 6 hours (model lists only, no prompts). Free chat models that are not in the catalogue appear behind the floating "New models" button on the dashboard, newest first, marked New for 14 days. One click adds a model to the catalogue and to the end of My list; it stays after restarts.

```sh
mangoman list                              # show My list
mangoman list add kimi-k3 groq/gpt-oss-120b
mangoman list up groq/gpt-oss-120b         # reorder
mangoman list rm kimi-k3
mangoman list new [--scan]                 # new free models not yet in the catalogue
mangoman list add openrouter/acme/model:free   # add one of those
```

On the dashboard: the My list section (reorder, remove, add from a picker), a star on every row of the models table, and the New models drawer with Check now.

## Team keys (several people on one machine)

When a team works from one computer, each person can add their own key for any provider. Requests take turns across every key of a provider, so the team gets more free use of the same models. When one key reaches its limit it rests until its reset, and the next key carries on with the same model, so answers stay consistent. A rejected key drops out on its own; the others keep working.

```sh
mangoman keys add nvidia --team ravi       # Ravi's NVIDIA key
mangoman keys add zen --team asha          # works for every provider
mangoman keys list                         # team keys are listed under each provider
mangoman keys rm nvidia --team ravi
```

On the dashboard, each provider has an **Add team key** button. Each team key shows whether it is working, resting or rejected, and its requests today. The live panel says whose key answered, and so does the `X-MangoMan-Team-Key` response header.

**Usage per person.** `mangoman people add ravi` gives Ravi his own local token for his tools (or `mangoman code --as ravi`); `mangoman usage` then shows requests by person. `mangoman people rm ravi` removes it.

**Keys pasted into a chat.** When a request seems to carry an API key, token or private key (pasted by you, or read from a file by a coding tool), MangoMan sends it unchanged, but to a provider that does not train on data first, for the same model. The response carries `X-MangoMan-Secret: 1` and `mangoman usage` counts these requests. The text is never altered, so code and answers stay correct.

Each key stays under its owner's provider account and that provider's terms; some providers say a key is for its owner's use only. MangoMan shows this notice before a team key is saved. Team keys can be switched off for any provider from the signed catalogue (`no_team_keys`), without a new release. Different people working on their own devices is a separate, later feature (team mode).

## What M2 adds

| Area | Done in M2 |
| --- | --- |
| Provider quirks | Catalogue data, not code: fields each provider rejects are dropped, `max_tokens` renamed where needed, final usage chunk requested on streams (and hidden from clients that did not ask) |
| Real limits | Rate-limit headers are mapped per provider (Groq, Cerebras); the router learns the real limits, syncs its counters to the provider's, and blocks until the reported reset. Learned limits override the seed and survive restarts |
| Errors in a 200 | `{"error":...}` bodies sent with HTTP 200 (OpenRouter) are treated as failures; a 429 inside one blocks the bucket |
| Conformance corpus | 9 cases: basic, multi-turn, stream, tools, tool result, streamed tools, JSON mode, truncation, unknown model. Runs offline in tests (a reference provider, a sloppy one, and through the full router) and live in `doctor` |
| `mangoman doctor` | Per connected provider: key check, catalogue ids vs the provider's model list, new free models (radar), the corpus per model, rate-limit headers vs catalogue rules. Asks before spending quota, paces to each model's RPM, writes a JSON report with no keys and no answers |
| `mangoman usage` | Requests served, failover rate, and per model: attempts, success, rate limits, errors, bad answers, tokens, median latency |

### Run doctor first

```sh
mangoman keys add groq          # and any other providers you have
mangoman doctor                 # full corpus, about 8 requests per model
mangoman doctor --quick         # basic, stream, tools, json only
mangoman doctor --provider groq --model llama-3.3-70b
```

The report tells you which catalogue entries to fix (wrong model ids, missing rate-limit headers, failing capabilities) and which new free models exist. Send it back to update `seed.json`.

## Layout

```text
cmd/mangoman/        CLI and server entry point
internal/core/       internal request model (OpenAI Chat shape), errors
internal/classify/   rules-first task classifier, virtual models
internal/router/     plan (filter, score, rank), failover loop, stream relay
internal/quota/      buckets, reset parsing, persistence
internal/breaker/    circuit breakers
internal/guard/      quality guard
internal/providers/  OpenAI-compatible client, key validation, Ollama discovery
internal/catalogue/  catalogue model, embedded seed.json, signature check
internal/keys/       keychain, encrypted file store, resolver
internal/ingress/    local HTTP endpoint and security checks
internal/config/     config.json (never holds provider keys)
internal/store/      usage log and summary
internal/conformance/ corpus, checks, reference provider
internal/doctor/     live provider checks, report, markdown summary
internal/radar/      new-model radar (watches provider model lists)
internal/adapt/      Anthropic Messages and OpenAI Responses translation
internal/mcp/        assist mode: MCP server and free_ask / free_review / free_status
internal/brain/      decision brain: typed questions, budget, cache, stats
internal/skills/     skill packs (embedded) and their installer
internal/setup/      setup wizard and key connection
internal/ingress/ui/ dashboard page (embedded in the binary)
scripts/try.sh       one-command live test (Codespaces or any machine)
.devcontainer/       Codespaces setup
.github/workflows/   ci (tests, builds) and doctor (live checks)
```

## Tests

```sh
go test -race ./...
```

The router tests run fake TLS providers to prove each failover path, same-model-first ordering, stream buffering, idle timeouts and the guard.

## Decisions taken in M1

| Decision | Why | Revisit |
| --- | --- | --- |
| Encrypted key file uses AES-256-GCM + PBKDF2-SHA256 (600k rounds), not XChaCha20 + Argon2 as in the PRD | Standard library only, so no extra crypto dependency | Before the M6 audit, if the auditor prefers Argon2 |
| Usage log is JSONL, not SQLite | No dependency yet; `usage` summarises it fine at this size | M5, with the dashboard |
| Internal model is the OpenAI Chat shape | All launch providers speak it; Anthropic and Responses adapters translate into it | M3 |
| Refusal detection not in the guard yet | Avoid false positives; ships behind a setting | M4 |
| One dependency (`zalando/go-keyring`) | OS keychain access on all three platforms | M6 SBOM |

## Before the next milestone

- **Verify the seed catalogue.** Model ids, limits and data policies in `internal/catalogue/seed.json` are best-effort and marked `verified: false`. Check each against the provider's docs and terms (human review, per the PRD).
- **Run `go mod tidy` once** with normal internet access to complete `go.sum` (it was built in a sandbox without the Go module proxy).
- **Run `mangoman doctor` with real keys.** Upstream calls could not be made from the build sandbox; doctor is the way to verify every provider, model id, quirk and header rule.
- **Quirks and header rules in the seed are from memory** (Groq, Cerebras), so doctor will flag any that are wrong.

## What M3 adds: Claude Code and Codex

MangoMan now speaks all three client formats. Each one is translated into the router's internal format and back, so free-first routing, switching on bad answers, My list and quota tracking work the same for every tool.

| Endpoint | Format | Used by |
| --- | --- | --- |
| `POST /v1/chat/completions` | OpenAI Chat Completions | Cursor, Cline, Continue, n8n, most SDKs |
| `POST /v1/messages` (+ `/count_tokens`) | Anthropic Messages | Claude Code, Anthropic SDKs |
| `POST /v1/responses` | OpenAI Responses | Codex CLI (required since Feb 2026), newer OpenAI SDKs |

Streaming and tool calls work in all three, checked with the official Anthropic and OpenAI Python SDKs (text, tool loops, streamed tool calls, token counting, errors).

**Claude Code**

```sh
export ANTHROPIC_BASE_URL=http://127.0.0.1:4141
export ANTHROPIC_AUTH_TOKEN=<your local token from mangoman init>
claude
```

Claude model names map to free models: `haiku` goes to `free/fast`, everything else to `free/coder`. Anthropic does not support Claude Code on other models, so some features (extended thinking, Anthropic-hosted tools like web search) are not available; assist mode (MCP, next) is the supported path for keeping Claude as the main model.

**Codex CLI** (`~/.codex/config.toml`)

```toml
model = "free/coder"
model_provider = "mangoman"

[model_providers.mangoman]
name = "MangoMan"
base_url = "http://127.0.0.1:4141/v1"
env_key = "MANGOMAN_TOKEN"
wire_api = "responses"
```

Then `export MANGOMAN_TOKEN=<your local token>`. MangoMan keeps no conversation state, so `previous_response_id` is refused; Codex sends the full history, which is what it does with custom providers. Freeform tools (such as `apply_patch`) are passed to free models as a function with one `input` string and turned back into custom tool calls. OpenAI-hosted tools (web search, file search) are skipped.

**OpenCode** (open-source coding agent, officially supports any model): add a `mangoman` provider to `opencode.json` using `@ai-sdk/openai-compatible` with `baseURL` `http://127.0.0.1:4141/v1` and `apiKey` `{env:MANGOMAN_TOKEN}`. `mangoman init` prints the full block.

## Same model every time (workflows)

Normal routing switches models to keep answering. For workflows where output must stay consistent:

| Model field | Uses | When all are used up |
| --- | --- | --- |
| `strict/kimi-k3` | Only kimi-k3, on any provider that serves it | 429 with the time capacity returns |
| `strict/groq/gpt-oss-120b` | Only that model on that provider | 429 |
| `group/<name>` | Only the group's models, in your order (My list is ignored) | 429 |

```sh
mangoman group set coding kimi-k3 groq/gpt-oss-120b   # models you tested as equivalent
mangoman group                                        # list
mangoman group rm coding
```

The context check still applies inside a scope: a request too long for a model skips it.

## What M4 adds: assist mode and `mangoman code`

**Assist mode** keeps Claude or GPT as your main model and lets it hand routine work to free models, so your paid limits last longer. MangoMan runs as an MCP server the coding tool starts itself:

```sh
claude mcp add --scope user mangoman -- mangoman mcp      # Claude Code
```

```toml
# Codex: ~/.codex/config.toml
[mcp_servers.mangoman]
command = "mangoman"
args = ["mcp"]
```

| Tool | What the main model can do with it |
| --- | --- |
| `free_ask` | Send a self-contained task (tests, docs, summaries, boilerplate) plus project files to a free model |
| `free_review` | Get a free model's review of the current git changes (bugs, edge cases, leaked secrets) |
| `free_status` | See which free providers are connected and how many models are ready |

Guardrails: only files inside the project are sent, never files that look like secrets (`.env`, keys, credentials), symlinks out of the project are refused, 200 KB per file and 600 KB per call. `free_review` excludes secrets files from the diff.

**`mangoman code`** opens OpenCode already connected to MangoMan (no config files touched), starts the router for the session if it is not running, offers to download OpenCode into MangoMan's own folder if it is not installed (about 60 MB from its official GitHub release, asks first, no Node needed; MangoMan installs only the OpenCode version it has tested, checks the download's SHA-256, and offers an update when a newer tested version ships), and turns on OpenCode's web search so it can research while it codes (`--no-web` to turn it off). Pick the model with `--model free/coder`, `strict/<model>` or `group/<name>`.

Checked end to end with the real tools: OpenCode 1.18 through `mangoman code`, and Claude Code 2.1 running on MangoMan and calling `free_status` through assist mode.

**Local models:** Ollama serves a small context by default, too small for coding agents' instructions. Start Ollama with `OLLAMA_CONTEXT_LENGTH=32768` (or more) and MangoMan uses that size.

**Tray icon: deferred.** A tray needs native GUI libraries on macOS and Linux, which would end the single cross-platform binary built without C toolchains. The dashboard and `mangoman code` cover "is it running" for now; the tray returns with the desktop app.

## Coding screen (M6): `mangoman code --ui`

Run it in your project folder. MangoMan starts the router if needed, starts OpenCode as a private local server (password known only to MangoMan) and opens the coding screen in your browser:

- **Chat** on the left: say what to build. **Plan** explains the change first; **Build** makes it. **Stop** ends a step; **New chat** starts fresh (the files stay as they are).
- **Approvals:** before any command that could change something (installs, deletes, scripts), a card asks **Allow**, **Always allow this kind** or **Deny**. Reading commands (`ls`, `git status`, `git diff`, `git log`) run without asking.
- **Changes:** every file the AI writes or edits, old lines in red, new in green.
- **Terminal:** every command it ran and its output; you can run your own.
- **Preview:** your app in a phone, tablet or laptop frame (or all three), picked up from the address it prints. The address for your phone on the same Wi-Fi is shown too; for an Expo app, scan its QR code with Expo Go.
- **Ship** (Guardian projects): your work happens in a copy made from dev; Ship sends it to QA in dev, then you approve it for production.
- **While it waits:** the screen says which free model it is waiting on and why it switched ("Kimi K3 was busy, so MangoMan switched to Nemotron 3 Ultra"). If nothing at all happens for a minute (no answer coming, no command running, nothing waiting for you), it restarts the step, twice at most. `mangoman code run` does the same for unattended runs.

Press Ctrl-C in the terminal to close the workspace. The screen talks only to the local router, which checks the local token and passes calls to OpenCode.

## Decision brain (smart layer, part 1)

A fast model answers small, typed questions where the router's rules are unsure, and its answer comes with a confidence. Two decisions so far:

| Decision | When it is asked | Effect |
| --- | --- | --- |
| Which kind of task is this? (code, reasoning, writing, extraction, fast) | Only when the keyword rules are unsure; once per conversation, then remembered | Better model choice |
| Is this short reply a refusal or non-answer? | Only for short replies that sound like "I can't help with that" | A confident "yes" (75%+) tries the next model; a justified refusal is kept |

Safety: each decision has a 1.5 second budget, and below 60% confidence it is ignored. A slow, failing or unsure engine leaves today's rules in charge, so the brain can only help. Its own requests go through the router (free-first, failover, quota counted) and never trigger the brain or My list.

```sh
mangoman brain                       # engine, counts, recent decisions
mangoman brain off | on
mangoman brain set strict/ollama/qwen3:4b    # a local engine; default free/fast
mangoman brain test "Is this a coding question: how do I sort a list?"
```

The dashboard shows the same, with an on/off switch. Jev can be the engine once added from the new-models list (OpenCode Zen key needed); whether it follows the JSON answer format has to be checked with a real key.

## Skill packs

Skill packs make specific outputs come out polished on free models. Each is a folder in the open Agent Skills format (a `SKILL.md` with expert instructions, plus Python scripts), so OpenCode, Claude Code and Codex load it by themselves. The model writes the content and the analysis code; a tested renderer applies the design; a checker verifies the result before delivery.

| Pack | Asks first | Builds | Checks |
| --- | --- | --- | --- |
| `mangoman-resume` | Target role, classic (ATS-safe) or modern design, length, region | PDF and HTML in two tested designs, plus a matching cover letter | Page count, page well filled, selectable text, every section filled, action-first bullets with results, no "I", one date format |
| `mangoman-code-review` | The repository and what to compare against (uncommitted work, last commit or a base branch) | facts.json (diff, tests and linters run, secrets, debug code), review.md with verdict and findings by severity | Findings point at changed files and lines, failing checks and leaked secrets are blockers and block approval, no approval with blockers or majors, no duplicates |
| `mangoman-data-dashboard` | Audience and decision, period and measures, currency | Interactive HTML dashboard (KPI tiles, charts, table view, light and dark) | Analysis reproduces the numbers, spec valid, every number in the text traces to computed data, page renders |
| `mangoman-brand-kit` | Logo and colours, personality, type preference, extra colours | Brand guidelines PDF (logo, colour with contrast results, type, voice), brand-tokens.json with a block for every pack, brand.css | Every text and background pair passes WCAG AA, extra colours distinct, logo size, voice complete, five pages |
| `mangoman-ceo-deck` | Audience and decision, the main question, period and currency, brand (logo, colours) | Editable PowerPoint (native charts and tables), PDF, web version; 12 themes, 3 motifs, light mode, brand colours and logo | Same number checks, answer-first structure, headline and bullet length, no slide overflows, one PDF page per slide |
| `mangoman-landing-page` | What it is and for whom, the one action, how visitors reach the business (WhatsApp, phone, email, form service), facts, brand, photos, site address | A one-page site folder (index.html plus web-sized images, no outside requests): hero, proof, features, steps, stats, pricing, testimonials, FAQ, gallery, lead form that opens WhatsApp or email or posts to a form service; search and sharing tags, favicon | Opened at phone, tablet and desktop widths: no sideways scrolling or cut-off text, main button visible on the first phone screen, one h1, alt text, labelled fields, tap sizes, readable text, live links and images, title and description length, page weight, every number from the user's facts, no placeholder text, risky claims flagged |
| `mangoman-meeting-minutes` | Transcript (.txt, .vtt, .srt) or notes, title, date, attendees | Minutes as PDF and paste-ready Markdown, actions CSV: summary, decisions, actions with owners and dates, discussion, open questions, next meeting | Every action has an owner and a due date after the meeting, owners attended, decisions recorded, and every number, quote and owner appears in the transcript |
| `mangoman-proposal` | Client and need, deliverables and exclusions, price lines (qty and rate), discount, GST, payment terms, validity, phases, proof, team, brand | A4 PDF with cover and numbered pages, editable Word file, HTML: summary with key figures, scope, timeline chart, pricing with GST and payment schedule computed by the script, proof, team, terms, sign-off | Arithmetic recomputed (lines, discount, GST, totals, milestones add to the total), milestones add to 100%, dates in order, valid-until after the date, every number in the summary and results from the user's facts, placeholders and risky promises, client name consistent, every section and the total in the PDF |
| `mangoman-email-campaign` | Goal and audience, number of emails and timing, email tool (Mailchimp, Klaviyo, Brevo, MailerLite), facts, sender name and postal address, brand | One HTML email per step (tables and inline styles, 600 px, phone layout, dark-mode safe, preheader), plain-text versions, merge tags in the tool's syntax, sequence.md with schedule, subjects, preview text and A/B options, images ready to upload, desktop and phone previews | Unsubscribe link and postal address in every email, under Gmail's 102 KB clipping size, subject and preview length, spam phrases, capitals, exclamation marks, real https links, alt text and hosted images, no sideways scrolling on a phone, every number from the user's facts |
| `mangoman-ecommerce-listing` | Product facts and label, marketplaces (Amazon India and US, Flipkart, Meesho, Shopify), keywords and their source, price and MRP, competitors, photos, Indian disclosures | listing.md with every field and its count, amazon.csv, flipkart.csv, shopify_products.csv (import format), A+ copy, image plan, phone-width preview | Amazon title rules (75 + 125, 200 or 125 for apparel, banned characters, no word more than twice, no promotional words), bullets 5 x 500, description 2,000, search terms 249 bytes without brands, ASINs or repeats; Shopify 70 and 320; no unproven health claims or competitor names; every number from the facts; primary keywords used; Indian disclosures (FSSAI for food); main image white and at least 1,000 px |
| `mangoman-invoice` | Kind (tax invoice, proforma, quotation), number and dates, your GSTIN and bank or UPI, buyer and GSTIN or state, items with HSN or SAC and GST rate | A4 PDF with CGST and SGST or IGST from the place of supply, GST rate breakup, round-off, amount in words, payment details, signature block | GSTIN format and check digit, number up to 16 characters, HSN digits, tax type matches the states, arithmetic and words, rule 46 details for unregistered buyers over ₹50,000, rates in force since 22 Sep 2025, e-invoice reminder |
| `mangoman-client-report` | Client and period, the data, what the client cares about, events, sender's logo | A4 PDF: summary, KPI tiles against last period, sections with takeaway headlines, charts and tables, wins, issues, plan for next month | Analysis reproduces the numbers, spec and charts valid, short takeaway headlines, every number traces to computed data or facts, a plan for next month, every section in the PDF, length |
| `mangoman-ad-copy` | What is advertised and the action, landing page, platforms, audience and area, facts, known keywords and exclusions | ads.md with counts, Google Ads Editor files (responsive search ads, keywords with match types, negatives), Meta ad variants with button and image brief | Google: 3 to 15 headlines of 30, 2 to 4 descriptions of 90, paths of 15, no "!" in headlines, no repeated punctuation, capitals or phone numbers, no duplicates, https URLs, keywords in headlines, negatives not blocking keywords; Meta: text that fits phones, valid button, no personal-attribute wording; numbers from facts, risky claims |
| `mangoman-seo-article` | Topic and keyword, who is searching, the next step for the reader, the user's own facts, length | article.html (with title, description and Article and FAQ structured data), article.md for the CMS, seo.md, phone preview | Every number from the facts or cited to a source, length and not thin, keyword in title, h1, opening, description, a subheading and the address without stuffing, title and description length, readable sentences and paragraphs, internal links, sources used, short FAQ answers, repeats, placeholders, risky claims |
| `mangoman-website-copy` | What the business sells and to whom, the one action, pages wanted, facts (it stops and asks when they are too thin), competitors, voice, site address | Markdown per page, pages.csv for a CMS, a sitemap and pages.json for the landing page pack | Every number from your facts, no five-word run copied from a competitor, keyword in each title or h1, title and description length, a button and a link in for every page, no repeated sentences, short sentences |
| `mangoman-web-app` | The job and who uses it, inputs and outputs, what must be remembered, rules and numbers, brand | One index.html (no build, no outside requests) started from a branded template with storage helpers, plus tests.json scenarios | Run in a real browser on phone and laptop: no script errors or failed loads, no sideways scrolling, labelled fields and named buttons, and every scripted scenario passes |
| `mangoman-social-posts` | Goal and action, platforms, number of posts, facts to use, brand, language and tone | Posting calendar (CSV and table), images at each platform's size (Instagram, LinkedIn, Facebook, X, Threads, WhatsApp status, stories), carousels (plus a LinkedIn PDF), captions per platform, hashtags, alt text, a preview sheet | Every size right, text fits at a readable size, captions within each platform's limit, valid hashtags, every number from the user's facts, "five tips" really has five, risky claims flagged, links in Instagram captions flagged |

Numbers are never typed by the model: an `analysis.py` it writes computes them, and the checker re-runs it and traces every figure in the text (headline, titles, notes) back to computed values. Numbers that appear only in text go in a `facts` field.

```sh
mangoman skills                       # list
mangoman skills install               # for Claude Code, Codex and OpenCode (~/.claude, ~/.codex, ~/.agents)
mangoman skills install --for claude  # or one tool, or --dir PATH
mangoman code                         # OpenCode with the packs already loaded, nothing installed
```

Requirements on the user's machine: Python 3; for PDFs, Chrome, Chromium or Playwright; for PowerPoint, `pip install python-pptx`; pandas optional.

## Advanced agents

An advanced agent improves on a free pack: it is a skill folder with an `agent.json` that says what it may do (network hosts, programs, models, price, data policy) and which free pack it must beat. Creators sign it with their own key; MangoMan checks the signature and every file before installing, and later versions must come from the same key.

```sh
mangoman agents new my-agent           # start a folder from a template
mangoman agents keygen                 # your signing key (back it up)
mangoman agents pack my-agent          # sign it into my-agent-1.0.0.mmagent
mangoman agents install FILE.mmagent   # check and install
mangoman agents exec NAME script.py    # run a script in the sandbox
mangoman agents eval NAME              # score it against its free pack's test set
```

Agent scripts run only through `mangoman agents exec`, in layers: every package (from a file or the marketplace) must pass the safety review, which refuses native code, code built at run time, shells and undeclared hosts; a Python guard then allows only the declared hosts and programs, writes only in the work folder and the agent's temp folder, keeps any Python child under the guard, and blocks SSH, cloud, browser and MangoMan secrets; only a short list of safe environment variables is passed; on Linux, agents without network also run with no network at all. `mangoman code` loads installed agents and tells OpenCode not to run their scripts directly (a best-effort rule on the command text). An agent's scripts can use its free pack's scripts and the shared helpers.

Each free pack can carry a public test set (`tests/cases`, `tests/score.py`); `mangoman agents eval` runs the pack and the agent on every case through the same headless OpenCode and an agent lists as Advanced only when it scores higher. In-house agents: `agents/amazon-listing-pro` (keyword research from the seller's search term report, competitor gaps, backend terms filled to 249 bytes, a before-and-after demand score) and `agents/google-ads-pro` (converting searches and wasted spend mined from the search term report, keyword groups and negatives that never block a converting search). Creators: see [CREATORS.md](CREATORS.md).

## QA agent and Guardian

`mangoman qa` tests a project: it runs the project's own tests (Go, npm,
pytest, Rust or `make test`) and, with `--url`, clicks through the running
web app in a browser. It writes `qa-report.md` with each bug and the steps
to reproduce it. A free model adds likely causes, marked as a guess.

```sh
mangoman qa ./my-shop --url http://localhost:3000/
```

`mangoman guardian` keeps a running app healthy, on its own. Every app gets
a permanent **development environment** from day 1: the `dev` branch,
deployed next to production all the time (`dev.yoursite.com` on the same
server, a second Docker copy, or a Vercel preview), always with production's
code. Guardian keeps it in step and reports any drift.

1. **Watch.** Checks from `guardian.json`: is the site up and fast, are there
   new error lines in a log, does a jobs script pass, does dev match
   production.
2. **First aid.** Runs only the fixes you listed for that check (restart,
   retry), so the app comes back while the real fix is made.
3. **Fix in dev, no approval needed.** Guardian finds the root cause and
   writes the fix on its own branch; the QA agent writes tests for it (only
   test files may change); the fix goes into dev, dev is deployed and QA
   tests it there. Up to 3 rounds, then Guardian hands it to you. Dev holds
   one change at a time; others wait their turn.
4. **Your changes take the same path.** `mangoman guardian change "add a
   contact page"`, or just write it to your Telegram bot.
5. **Your approval.** When QA passes in dev, Telegram shows you the change
   with Approve and Reject buttons. Nothing goes live without Approve.
6. **Production.** Guardian merges dev into production, deploys and has QA
   test production. If that fails it rolls back, puts dev back to
   production's code and works on it again.
7. **Data.** Every night production data is copied to dev's own database
   with names, emails, phone numbers, addresses, Aadhaar and PAN masked
   (SQL dumps, for example `pg_dump --column-inserts`). Live passwords and
   keys never reach dev.
8. **Reports** at 08:00 and 20:00 every day, also when all is well.

```sh
mangoman guardian init                    # writes guardian.json for this project
mangoman guardian setup                   # day 1: the dev environment
mangoman guardian telegram BOT_TOKEN      # optional: your bot from @BotFather
mangoman guardian run --every 5m          # keep watching, fixing and reporting
mangoman guardian change "add a contact page"
mangoman guardian incidents               # what is queued, in dev, waiting or live
mangoman guardian approve ID              # or the Approve button in Telegram
```

To watch MangoMan itself, point a check at `http://127.0.0.1:4141/healthz`
with the fix `mangoman serve` started in the background. MangoMan's own
nightly QA (`.github/workflows/qa.yml`) runs every test with the race
detector, clicks through the dashboard and has Guardian watch the router,
then opens an issue when anything fails.

## Credits

The coding engine behind `mangoman code` and the coding screen is
[OpenCode](https://github.com/anomalyco/opencode), open source under the MIT
licence (Copyright (c) 2025 opencode). MangoMan downloads the tested release
from OpenCode's own releases and runs it on your computer.

## Privacy

MangoMan runs on your computer and never receives your prompts or keys.
Requests go straight to the providers you connect, and some free models are
used for training. See [PRIVACY.md](PRIVACY.md).

## Next

1. **Marketplace:** listing page, creator portal and review pipeline, then payments (Razorpay Route, Stripe Connect) and payouts.
2. **More advanced agents and test sets**, one per skill area.
3. **MangoMan app for everyone (M5)**, parked for now: teachers and other first-time users cannot bring their own keys yet.
