---
name: mangoman-meeting-minutes
description: Turn a meeting transcript, recording notes or rough notes into clear minutes (summary, decisions, actions with owners and due dates, discussion, open questions) as a PDF, paste-ready text and an action list, checked so nothing is invented. Use when the user asks for meeting minutes, MoM, meeting notes, a meeting summary, action items or a follow-up email after a meeting.
license: Proprietary. Free to use inside MangoMan only; no copying, changing or reselling (see the MangoMan LICENSE).
metadata:
  pack: mangoman
  version: "1.0"
---

# Meeting minutes

You turn a meeting into a record people act on: what was decided, who does
what by when. The scripts in this skill's `scripts/` folder render the minutes
and check them against the transcript, so nothing is added that was not said.

## 1. Collect

- The transcript (`.txt`, `.md`, `.vtt` or `.srt` from Zoom, Meet or Teams) or the user's notes.
- Meeting title, date, and attendees with their roles, if the transcript does not show them.
- Anything the user wants emphasised or left out.

## 2. Write `minutes.json`

```json
{
  "title": "2027 pre-order launch plan", "date": "2027-01-09", "time": "11:00", "location": "Video call",
  "attendees": [{"name": "Rohan Iyer", "role": "Kesari Foods"}, {"name": "Meera Nair", "role": "Northbeam"}],
  "external": ["Courier partner"],
  "transcript": "transcript.vtt",
  "summary": "Two or three sentences: the outcome of the meeting.",
  "decisions": [{"text": "Stay with the current courier this season."}],
  "actions": [{"action": "Confirm courier capacity for 2,000 boxes in March, in writing", "owner": "Priya Shah", "due": "2027-01-15"}],
  "topics": [{"title": "Courier", "points": ["..."]}],
  "open_questions": ["..."],
  "next_meeting": {"date": "2027-01-16", "time": "11:00", "agenda": "..."}
}
```

Rules:
- **Only what was said.** Numbers, names and quotes must appear in the transcript (the checker fails others). Write numbers the way they were said with digits ("2,000 boxes"), and put quotes in double quotes so they are checked.
- **Decisions are decisions**, not discussion: what was agreed, in one sentence each.
- **Every action has one owner and a due date**, and starts with a verb ("Send", "Confirm"). If the meeting gave no date, ask the user or write the date they suggest and say so.
- Summary first: the outcome in two or three sentences, for people who read nothing else.
- Keep discussion points short; skip small talk and repetition.

## 3. Build, check, fix

```
python3 <skill dir>/scripts/build_minutes.py minutes.json --out minutes
python3 <skill dir>/scripts/check_minutes.py minutes.json minutes
```

Fix every FAIL and rebuild.

## 4. Deliver

Give the user `minutes.md` (ready to paste into an email or Slack), the PDF
and `minutes-actions.csv` (for a task tracker), and the check result in one
line ("Checked: every action has an owner and a date, every number and quote
is in the transcript"). Offer a short follow-up email to the attendees.
