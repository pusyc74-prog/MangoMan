---
name: mangoman-resume
description: Write or redesign a polished resume or CV, and a matching cover letter, delivered as checked PDFs in an ATS-safe or modern design. Use when the user asks for a resume, CV, biodata for a job, a cover letter, to update or improve their resume, or to tailor it to a job description.
license: Apache-2.0
metadata:
  pack: mangoman
  version: "1.1"
---

# Resume

You produce a one-page (or two-page) resume that reads well to a recruiter in
ten seconds and passes applicant tracking systems. The scripts in this skill's
`scripts/` folder render one of two tested designs and check the result, so
your job is the content: truthful, specific, results first.

## 1. Understand (ask before writing)

Ask only what you do not already know, in one short message (use your
question tool if you have one):

1. **Target role**, ideally with the job description or link.
2. **Design:** "classic" (default; single column, safest for job portals and applicant tracking systems) or "modern" (accent colour and a side column; best for sending directly, creative and design roles).
3. **Length:** one page (default, under about 10 years of experience) or two.
4. **Region:** India (A4, no photo, no date of birth), US (Letter, no photo, no age) or elsewhere.

Then get the content. If the user has an old resume or LinkedIn export, run:

```
python3 <skill dir>/scripts/extract_text.py <file>
```

## 2. Write the content

Rules (these are what recruiters and screening software reward):
- **Never invent facts.** No made-up numbers, employers, titles or skills. If a bullet needs a result the user has not given, ask for it ("roughly how many users / how much time or money?") or keep it qualitative.
- **Bullets start with a strong verb and end with a result:** "Cut onboarding time from 9 days to 2 by redesigning setup", not "Responsible for onboarding". Aim for a number in at least half the bullets.
- 3 to 5 bullets for recent roles, 1 to 3 for older ones; at most about 25 words each. No "I", "my" or "me".
- **Tailor truthfully** to the job description: mirror its key terms where they genuinely apply, and order bullets so the most relevant come first.
- Summary: 2 or 3 lines, role plus years plus the strongest proof.
- One date format throughout ("Mar 2022 – Present"). Most recent first.
- Skills grouped (for example Product, Data, Tools); no rating bars, no "proficient in MS Office".

Write `resume.json`:

```json
{
  "name": "Ananya Rao",
  "headline": "Senior Product Manager, B2B SaaS",
  "contact": {"email": "ananya@example.com", "phone": "+91 98xxx 12345", "location": "Bengaluru, India",
              "links": [{"label": "linkedin.com/in/ananyarao", "url": "https://linkedin.com/in/ananyarao"}]},
  "summary": "Product manager with 7 years ...",
  "experience": [{"role": "Senior Product Manager", "company": "Ledgerly", "location": "Bengaluru",
                  "start": "Mar 2022", "end": "Present", "bullets": ["Led ...", "Cut ..."]}],
  "education": [{"degree": "MBA, Operations", "school": "IIM Indore", "year": "2017", "details": "optional"}],
  "skills": [{"group": "Product", "items": ["Discovery", "Pricing"]}],
  "projects": [{"name": "optional", "description": "...", "link": "https://..."}],
  "certifications": ["optional"], "awards": ["optional"], "languages": ["English", "Hindi"],
  "page_size": "A4",
  "accent": "#1f5fa8"
}
```

`page_size` is `A4` (India, Europe) or `Letter` (US). `accent` only affects the modern design.

## 3. Build

```
python3 <skill dir>/scripts/build.py resume.json --template classic --out resume
```

Writes `resume.html`, `resume.pdf` and, when Playwright is installed, a preview `resume.png`.

## 4. Check, fix, re-check

```
python3 <skill dir>/scripts/check.py resume.json resume.pdf --pages 1
```

Fix every FAIL and rebuild. If it runs over a page, tighten wording first, then
drop the oldest or least relevant bullets; never shrink the font below the
design. Then look at the PDF or `resume.png`: nothing cut off, the most
important role and result visible at a glance.

## Cover letter

When asked (or offered and accepted), add `cover_letter` to `resume.json`:

```json
"cover_letter": {"company": "Paybright", "role": "Group Product Manager", "hiring_manager": "Ms Kavya Menon",
                 "location": "Bengaluru", "date": "3 October 2026", "paragraphs": ["...", "...", "..."]}
```

Three or four paragraphs, 200 to 400 words: why this company and role, the two
or three strongest results from the resume that match the job, how you work,
and a short close. Every number must come from the resume (the checker fails
others); never open with "I am writing to apply" or "To whom it may concern".
The build writes `<out>-letter.pdf` in the same look; the checker adds the
letter checks.

## 5. Deliver

Give the user the PDF (and the HTML if they want to edit), list what you
changed or still need from them (missing results, dates), and the check result
in one line ("Checked: one page, readable by job portals, every section
filled"). Offer a cover letter tailored to the same job.
