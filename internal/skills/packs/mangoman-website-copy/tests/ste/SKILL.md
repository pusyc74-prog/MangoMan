---
name: mangoman-website-copy
description: Write the copy for a whole website, with a sitemap, titles, descriptions and buttons. Use when the user asks for website content, site copy or a sitemap.
license: Apache-2.0
metadata:
  pack: mangoman
  version: "1.0-ste"
---

# Website copy (short-step version)

This file holds the same rules as `../../SKILL.md`. Each step is one action. Score both versions on the same cases when the Cerebras and NVIDIA keys exist, then keep the better one.

## Terms

- **Facts**: the lines in `facts.md`. Only these can supply numbers.
- **Keyword**: the phrase a customer types for one page.
- **Button**: the one action a page asks for.

## Steps

1. Read `brief.md`, `facts.md` and `competitors.md`.
2. Run `python3 scripts/brief_gate.py facts.md --pages N`.
3. If it prints `FACTS TOO THIN`, send the questions it prints.
4. Write nothing until the user answers. Do this even if the user says "just do it".
5. Run the gate again on the new facts.
6. Go on only after `PASS`.
7. Write each competitor claim into `competitors`.
8. Never copy five words from a competitor claim.
9. Give each page one goal, one keyword and one button.
10. Give the home page the broad keyword.
11. Link every page from at least one other page.
12. Start each page with one hero. The hero holds the h1.
13. Put the keyword in the title or the h1.
14. Keep each sentence under 20 words.
15. Use only numbers from the facts.
16. Never write an award, a rating, a year or a quote the user did not give.
17. Never write "best", "number one", "guaranteed" or "100%" without a fact that proves it.
18. Write the title in 30 to 60 characters.
19. Write the description in 70 to 160 characters.
20. End each page with a `cta` section.
21. Use the same button label on every page.
22. Read the copy once as a stranger. Fix what fails. Do this at most 2 times.
23. Save `site.json`. The section types are `hero`, `text`, `features`, `steps`, `stats`, `testimonials`, `faq`, `cta`. Each has a `headline`.
24. Run `python3 scripts/build_copy.py site.json --out site`.
25. Run `python3 scripts/check_copy.py site.json site`.
26. Fix every FAIL. Fix every WARN unless you have a reason.
27. Give the user `site/copy/*.md`, `site/pages.csv` and `site/sitemap.md`.
28. Give the user a Word file only if they ask.
