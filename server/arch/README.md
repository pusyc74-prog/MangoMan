# Mango Arch server

Mango Arch is how MangoMans learn from each other. Phase 1 learns one thing,
which can be checked automatically: which free models are slow or failing
for many users right now.

## How it works

1. Each MangoMan whose owner agreed (Settings, Mango Arch) sends one report
   an hour: for each free model it used, how many requests were answered,
   busy, slow, garbled, empty or failed. Nothing else: no prompts, answers,
   files or keys. The id is a random number made on that computer, new each day.
2. Five minutes past every hour the server looks at the last full hour.
   Votes are per network, not per MangoMan. A model gets a lesson only when
   it failed at least half the time on at least `QUORUM` networks (default
   5), and those are at least 60% of the networks that used it.
3. The lesson says "try this model last until a time two hours away". The
   server signs all lessons in force with its Ed25519 key.
4. Every MangoMan fetches the lessons when it starts and every hour. One
   that was off gets them when it is next online. It checks the signature,
   ignores anything it does not know, and never keeps a lesson longer than
   three hours.

A lesson can only change the order models are tried in. It never removes a
model. So a wrong or forged lesson costs some speed, never a task.

Network: the first three parts of an IPv4 address (the first three groups,
a /48, of IPv6), stored only as a hash salted with a secret and the day. One
office, or one person with many computers or made-up ids, is one vote. A
network may send at most 20 reports an hour. For more protection, add a
Cloudflare rate-limiting rule on `/v1/report`.

## Cost

Cloudflare's free plan: Workers 100,000 requests a day, D1 100,000 rows
written a day. Each MangoMan sends at most 48 requests a day (24 reports, 24
fetches) and writes 24 rows. So the free plan carries about 2,000 MangoMans
that are on all day. Check Cloudflare's current limits and prices before
relying on these numbers.

## Set it up (owner, once, about 15 minutes)

You need a free Cloudflare account and Node.js on your computer.

```sh
cd server/arch
npx wrangler login
npx wrangler d1 create mango-arch            # paste the id into wrangler.toml
npx wrangler d1 execute mango-arch --remote --file schema.sql
node keygen.mjs                              # prints the private and public key
npx wrangler secret put ARCH_PRIVATE_KEY     # paste the private key
npx wrangler secret put SALT                 # any long random text
npx wrangler secret put OWNER_TOKEN          # any long random text, for /v1/daily
npx wrangler deploy                          # prints the address
```

Then, in GitHub, Settings, Secrets and variables, Actions, Variables, add
`ARCH_URL` (the address) and `ARCH_KEY` (the public key). The next release
build turns Mango Arch on. Builds without them have it off.

Keep the private key safe. Whoever has it can sign lessons. If it leaks,
make a new pair, set the new secret, and release MangoMan with the new
public key.

## Daily report

```sh
curl -H "Authorization: Bearer $OWNER_TOKEN" https://<address>/v1/daily
```

It lists the MangoMans and reports in the last 24 hours and every lesson in
force during that time.

## Test

`node server/arch/test.mjs` runs the Worker against a real SQLite database
(Node 22 or later). CI runs it on every push. With `--fixture` it also
rewrites `internal/arch/testdata/signed.json`, which the Go tests verify.
