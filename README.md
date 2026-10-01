# MangoMan

Free-first, local AI router. One signed Go binary on your machine pools every free model you can reach, fails over when one runs dry or gives a bad answer, and never sends your keys or prompts anywhere except the provider you are calling.

Status: **Phase 1, milestone M1 (Foundations)**. See the [PRD](https://claude.ai/code/artifact/cca2344e-8ddc-431f-b053-6c0b2617c29e) and the [Phase 1 build plan](https://claude.ai/code/artifact/1b002e4f-d746-47c0-be64-d6fad377d2d0).

## Quick start

```sh
go build -o bin/mangoman ./cmd/mangoman
./bin/mangoman init            # config + local token, prints tool setup
./bin/mangoman keys add groq   # validated, stored in the OS keychain
./bin/mangoman serve           # http://127.0.0.1:4141/v1
./bin/mangoman test "hello"    # shows provider, model, attempts, data policy
```

Point any OpenAI-compatible tool (Cursor, Cline, Continue, n8n, your code) at `http://127.0.0.1:4141/v1`, use the local token from `init` as the API key, and pick a model: `free/auto`, `free/coder`, `free/writer`, `free/fast`, `free/long`, or a real model such as `llama-3.3-70b`.

Every response carries `X-MangoMan-Provider`, `X-MangoMan-Model`, `X-MangoMan-Attempts`, `X-MangoMan-Class` and `X-MangoMan-Data-Policy`.

## What M1 includes

| Area | Done in M1 |
| --- | --- |
| Endpoint | `POST /v1/chat/completions` (streaming and non-streaming), `GET /v1/models`, `GET /mangoman/status`, `GET /healthz` |
| Local security | Binds 127.0.0.1 only; local bearer token (or `x-api-key`); Host check against DNS rebinding; browser Origin allow-list |
| Providers | One OpenAI-compatible adapter covering Groq, Cerebras, OpenRouter (free), NVIDIA, Ollama (auto-discovered) |
| Routing | Free-first scoring (quality, quota left, health, speed) per task class; same model on another provider first; local Ollama as last-resort backstop |
| Failover | 429 (bucket blocked until reset), 5xx and timeouts (circuit breaker), 401/403 (key disabled), 404 (model avoided), unreachable provider (skip its other models), client errors (one retry, then return) |
| Streaming | Events held until the first real token, so empty or broken streams fail over invisibly; idle timeout; clean error event if a stream breaks after output started |
| Quality guard | Empty, truncated, invalid JSON (when JSON mode asked), malformed or unknown tool calls; if every answer fails, the first is returned with `X-MangoMan-Guard` |
| Quota | Per provider, account, model: requests and tokens per minute and per day; pre-flight check; corrected from rate-limit headers; survives restarts |
| Keys | OS keychain; encrypted file fallback; env var overrides; validated on add |
| Catalogue | Embedded seed with limits and per-provider data policy labels; Ed25519 verification ready for the live feed (M5) |
| CLI | `init`, `serve`, `keys add/list/rm`, `status`, `models`, `test`, `version` |
| Usage log | `usage.jsonl`: outcome, latency, tokens per attempt. Never prompt or answer content |

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
internal/store/      usage log
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
| Usage log is JSONL, not SQLite | No dependency until the dashboard needs queries | M2 |
| Internal model is the OpenAI Chat shape | All launch providers speak it; Anthropic and Responses adapters translate into it | M3 |
| Refusal detection not in the guard yet | Avoid false positives; ships behind a setting | M4 |
| One dependency (`zalando/go-keyring`) | OS keychain access on all three platforms | M6 SBOM |

## Before the next milestone

- **Verify the seed catalogue.** Model ids, limits and data policies in `internal/catalogue/seed.json` are best-effort and marked `verified: false`. Check each against the provider's docs and terms (human review, per the PRD).
- **Run `go mod tidy` once** with normal internet access to complete `go.sum` (it was built in a sandbox without the Go module proxy).
- **Test with real keys.** Upstream calls could not be made from the build sandbox.

## Next: M2 (Routing core)

Live provider testing, native quirks per provider, SQLite store, quota tuning from real headers, and the conformance corpus groundwork.
