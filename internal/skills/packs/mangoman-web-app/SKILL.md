---
name: mangoman-web-app
description: Build a small single-file web app (calculator, tracker, form, quiz, booking or order tool, internal dashboard) that works on phones and laptops, keeps its data in the browser, uses the user's brand, and is tested in a real browser with scripted scenarios before delivery. Use when the user asks for a simple app, a tool, a calculator, a tracker, a form that does something, or a prototype they can open and use.
license: Apache-2.0
metadata:
  pack: mangoman
  version: "1.0"
---

# Web app

You build a small app that does one job well and is proven to work. One
`index.html` file (markup, styles and script, no build step, no outside
requests) opens in any browser and can be published anywhere. The scripts in
this skill's `scripts/` folder start the file with a tested look and run the
app in a real browser against scenarios you write.

## 1. Understand (ask once, in one message)

1. **The job:** who uses it and the one or two things it must do ("add GST to a price and keep a history").
2. **Inputs and outputs:** what people enter, what they see, what must be remembered after a reload.
3. **Rules and numbers** it must follow (rates, formulas, limits), from the user.
4. **Brand:** "Attach your logo or share brand colours, or say skip."

Keep it small. If the request needs accounts, a shared database, payments or
sending messages, say so: that is a bigger build (the MangoMan coding
workspace), and offer a version that works in one browser first.

## 2. Start the file

```
python3 <skill dir>/scripts/scaffold.py app.json --out app
```

`app.json`: `{"title": "GST calculator", "id": "gst", "brand": {"logo": "logo.png"}}`.
It writes `app/index.html` with brand colours as CSS variables, styles for
cards, labelled fields, buttons and tables, and helpers: `$` and `$$` (find
elements), `store.get(key, fallback)` and `store.set(key, value)` (data that
survives reloads in this browser), `inr(n)` (Indian rupee format).

## 3. Build the app in `app/index.html`

- Markup in `<main>`: sections with `class="card"`, every field inside a `<label>` with visible text, buttons that say what they do, results in elements with an `id`.
- Logic at the marked place in the script: plain JavaScript, no libraries unless the user asks.
- Validate input and show a clear message (`class="error"`); never show `NaN` or `undefined`.
- Round money to two decimals; use the user's rules exactly.

## 4. Write `tests.json` and run it

One scenario per feature and per important mistake a user can make:

```json
{"scenarios": [
  {"name": "adds 18% GST", "steps": [{"fill": "#amount", "value": "1000"}, {"click": "#calc"}, {"expect_text": "#total", "contains": "1,180.00"}]},
  {"name": "history survives a reload", "steps": [{"fill": "#amount", "value": "100"}, {"click": "#calc"}, {"reload": true}, {"expect_count": "#history li", "count": 1}]}
]}
```

Steps: `fill`, `select`, `click`, `check`, `press` (with `key`), `reload`,
`expect_text` (with `contains` or `equals`), `expect_value`, `expect_visible`,
`expect_hidden`, `expect_count`. Each scenario starts with empty storage.

```
python3 <skill dir>/scripts/check_app.py app/index.html tests.json
```

It also fails on script errors, failed loads, sideways scrolling, fields
without labels and nameless buttons, on a phone and a laptop. Fix every FAIL
in the app (not in the tests, unless the test is wrong) and run it again.

## 5. Deliver

Give the user `index.html` (it opens by double-clicking; to share it, drag the
folder onto Netlify Drop), what it does in one line, the check result in one
line ("Checked in a browser on phone and laptop: 4 scenarios pass, no errors,
every field labelled"), and that data stays in their browser only.
