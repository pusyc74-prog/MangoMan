# Build an advanced agent for MangoMan

An advanced agent does a whole job better than MangoMan's free skill pack for
the same skill. You write the instructions and scripts; MangoMan signs,
checks, sandboxes and scores them, and the marketplace lists agents that win.

## 1. Start

```sh
mangoman agents new my-agent      # a folder with agent.json, SKILL.md, scripts/hello.py
mangoman agents keygen            # once: your signing key (back it up)
```

## 2. agent.json

| Field | Meaning |
| --- | --- |
| `name`, `version` | lower-case words joined by dashes; `1.0.0` style versions, higher for every update |
| `title`, `description` | what it does, in plain words, for the marketplace page |
| `skill` | the free pack it improves on, for example `mangoman-ecommerce-listing`; your agent must beat it on that pack's test set |
| `author` | your name and a contact address |
| `runs_on` | `local` (on the user's computer, in the sandbox) or `developer-cloud` (with an `https` `endpoint`; the user's data goes to you) |
| `permissions.network` | hosts your scripts may reach, for example `["api.example.com", "*.example.org"]`; empty means none |
| `permissions.commands` | programs your scripts may start besides Python (bare names); empty means none |
| `models` | `free` (works on MangoMan's free models) or `paid` |
| `price_inr_month` | monthly price in rupees; 0 for free |
| `data_policy` | what happens to the user's data, in one or two sentences |

## 3. SKILL.md and scripts

SKILL.md uses the open Agent Skills format: frontmatter with `name` (same as
agent.json) and a `description` that says when to use it, then the steps the
model follows. Keep the description short: it is sent with every request.

Your scripts run only like this:

```sh
mangoman agents exec my-agent script.py ARGS
```

They can import the shared helpers (`render`, `vizlib`, `brandkit`, `checks`,
`tracenum`) and the free pack's own scripts (its builder and checker), so you
build on tested parts. Let scripts do the counting and checking; let the
model write.

## 4. Sandbox rules

Packages that break these are refused at review or stopped while running:

- Python scripts only; no compiled files, `ctypes` or `cffi`, `eval`, `exec`
  or `compile`, `pickle`/`marshal` loading, `__builtins__` tricks, shells
  (`os.system`, `shell=True`), or changes to the sandbox.
- Network only to the hosts you declare; files written only in the user's
  work folder and your agent's temp folder; no reading of the user's SSH,
  cloud, browser or MangoMan secrets; only a few safe environment variables.
- Python child processes keep the sandbox; other programs only if declared.
- On Linux, an agent with no network runs with no network at all.

Check yourself before submitting: `mangoman agents review my-agent`.

## 5. Score it

Each free pack with a public test set has `tests/cases` and `tests/score.py`
in its folder (`mangoman skills install --dir DIR` to see them). Run:

```sh
mangoman agents pack my-agent && mangoman agents install my-agent-1.0.0.mmagent
mangoman agents eval my-agent
```

Both your agent and the free pack run every case through the same headless
OpenCode and models; your agent lists as Advanced only if it scores higher on
average and scores on every case. Scores reward what users need (rules
followed, demand covered, facts used) and never reward claims the facts do
not support.

Test sets exist today for: e-commerce listing, resume, social posts, email
campaign, SEO article and ad copy.

## 6. Submit

Open a pull request against the `registry` branch adding
`packages/my-agent-1.0.0.mmagent` (and `my-agent-1.0.0.eval.json` with
`{"pack": N, "agent": M}` from your eval). The safety review runs on the pull
request; after a maintainer merges, the signed index lists your agent and
users can install it with `mangoman agents install my-agent` or from the
dashboard. Updates must be signed with the same key and have a higher version.

Examples: `agents/amazon-listing-pro` and `agents/google-ads-pro` in this
repository.

Payments and payouts for paid agents are not open yet.
