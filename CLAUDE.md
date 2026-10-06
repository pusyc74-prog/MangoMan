# Working on MangoMan

Rules for every change, human or AI:

- **Keep the code small.** No dead code, no unused options, no abstractions for one caller. Reuse what exists (Go packages under `internal/`, pack helpers in `internal/skills/packs/shared/`: `render`, `vizlib`, `brandkit`, `checks`, `tracenum`) before writing new code. Prefer deleting to adding.
- **Check as you go.** Before every commit: `go vet ./...`, `go test ./...`, and when packs change, `scripts/check-packs.sh` (builds each pack's sample in `internal/skills/testdata/` and runs its checker) plus `pyflakes` on the pack scripts. Fix bugs when found, not later. Each wave of work ends with a review pass for bugs and dead code.
- **Packs:** the model writes content; scripts render and check. Every number in output traces to user facts or computed values. A new pack adds a sample to `internal/skills/testdata/` and a line to `scripts/check-packs.sh`.
- **Writing:** plain, short sentences in user-facing text; no em dashes; never mention the former company name.
- **Plan:** the PRD (Claude Docs, "MangoMan: Technical PRD") records every change in its build log. Every PRD change goes into both the Claude Docs PRD and `docs/PRD.md` in the same step.
