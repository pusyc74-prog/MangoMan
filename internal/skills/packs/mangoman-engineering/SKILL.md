---
name: mangoman-engineering
description: How a senior engineer builds and changes software - agree a spec, plan thin slices, test first, debug from the root cause, verify before claiming done, check security, make surgical changes. Use whenever writing, fixing or changing code.
license: MIT (adapted from obra/superpowers, forrestchang/andrej-karpathy-skills and affaan-m/everything-claude-code; see LICENSE.txt)
metadata:
  pack: mangoman
  kind: method
  version: "1.0"
---

# Engineering method

The habits that make code right the first time. They cost a little speed and
save a lot of rework. For a one-line change, use judgment.

## The four rules

1. **Think before coding.** Say your assumptions. If the request can mean
   two things, say both and ask; do not pick silently. If something is
   unclear, stop and name it. If a simpler way exists, say so.
2. **Simplest thing that works.** No features nobody asked for, no
   abstractions for one use, no settings nobody needs, no handling for cases
   that cannot happen. If 200 lines could be 50, write 50.
3. **Surgical changes.** Touch only what the task needs. Do not reformat,
   rename or "improve" nearby code. Match the existing style. Remove only
   what your own change made unused; mention other dead code, do not delete
   it. Every changed line should trace to the request.
4. **Goal first, then loop until proven.** Turn the task into a check:
   "add validation" becomes "tests for bad input, then make them pass"; "fix
   the bug" becomes "a test that shows the bug, then make it pass".

## 1. Spec

Look before you ask: read the project, the brief and any files first. Then
play the job back in a few lines: who uses it, what they do, what must be
remembered, the rules and numbers it must follow, and what is not included.
Size it: if it is too big to finish well in one go, say so and pick the first
useful part. Save it as `spec.md` and get a yes before building. When nobody
can answer (an unattended run), write your assumptions into `spec.md` and go
on.

## 2. Plan in thin slices

Order the work as slices that each work end to end (one screen that saves
and shows one thing), not all the data first and all the screens later. In
`plan.md`, each step is one action with its check:

```
1. Save an order and list it -> check: test_saves_order passes; the list shows it
2. Add the GST total -> check: test_gst_total passes for 0, 5, 12 and 18 percent
```

## 3. Build each slice, test first

1. Write a test for the next piece of behaviour.
2. Run it and watch it fail, for the right reason. A test that passes at once
   tests nothing new: fix the test.
3. Write the least code that makes it pass.
4. Run it and the whole test suite. Fix the code, not the test, unless the
   test is wrong.
5. Tidy only your own new code, then the next test.

No framework yet? Use the language's own (Python unittest, Node's built-in
test runner, Go's testing) and a one-command way to run them all. A page or
one-file app built with a MangoMan skill (web app, landing page) needs no
unit tests: that skill's checker, with your scenarios, is the test.

## 4. Debug from the root cause

Never guess-fix. In order:

1. Read the whole error and stack trace. Reproduce it every time.
2. Check what changed (the diff, new dependencies, settings).
3. Find where good data turns bad: follow the value back to its source. For
   several parts in a chain, log what enters and leaves each part once.
4. One hypothesis at a time ("X causes it because Y"), tested with the
   smallest change. Wrong? Undo it and form a new one; do not pile fixes up.
5. Fix with a test that fails before and passes after. After three failed
   fixes, stop and question the design, and tell the owner.

## 5. Verify before saying done

Before you say anything works, is fixed or passes:

1. Name the command that proves it.
2. Run it in full, fresh.
3. Read the whole output and the exit code; count failures.
4. Say what the output shows, with the evidence. If it fails, say so.

"Should work" and "looks right" are not evidence. Check web pages and apps
the way a person uses them: a one-file app with the web app skill's checker;
a server app by starting it and requesting its pages and forms as a browser
does (a short script with MangoMan's Python, which has Playwright), checking
each answer. Then ask the owner to try it in the Preview tab.

## 6. Security check

Whenever code handles input, logins, money, files, other services or
personal data:

| Look for | Do instead |
| --- | --- |
| Keys, passwords or tokens in code or logs | Environment variables or the key store; never commit them |
| SQL or shell commands built from user text | Parameterised queries; argument lists, never a shell string |
| User text put into HTML as markup | Insert as text, or escape it |
| Fetching any address a user gives | Allow only known addresses |
| Plain-text passwords | A slow password hash (Python's `hashlib.scrypt`, or bcrypt or argon2 where the project has them) |
| A page or action with no login check | Check the user on every request that needs it |
| Errors that show internals to users | A plain message to users, details in the log |

## 7. No silent failures

Never swallow an error: no empty catch blocks, no `catch: return []`, no
default value that hides a failure. Every network and database call has a
timeout, and every failure a clear error. A failure the user can fix gets a message
that says how.

## 8. Safety

- Work only in the folders this task is about.
- Ask before anything that deletes what you did not make, overwrites history
  or reaches outside: deleting folders, discarding uncommitted changes,
  force-pushing or hard-resetting git, dropping a table, publishing a
  package, deploying, paying or signing up for a service.
- Use what the language already has before installing anything; install
  packages only when the owner agrees.
- In an unattended run nobody can answer: skip such a step and report it
  instead.
- Before changing many files, make sure the work is saved (a git commit or a
  copy) so it can be undone.

## 9. Review your own change

Before handing over, review it as a careful senior engineer would, with the
`mangoman-code-review` skill when the project uses git: correctness first,
then security, then clarity. Fix every blocker and major finding, then
verify again.
