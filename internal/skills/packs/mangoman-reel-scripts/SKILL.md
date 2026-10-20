---
name: mangoman-reel-scripts
description: Write ready-to-shoot short video scripts (Instagram Reels, YouTube Shorts, Facebook Reels, TikTok) built on the audience's real problems - a goal per reel, three scroll-stopping hook options, value, a clear solution, one call to action that fits the goal, a timed shot list with on-screen text, caption, search keywords, hashtags, cover text and sound - checked before delivery. Use when the user asks for reels, shorts, short videos, video scripts, or reel ideas.
license: Proprietary. Free to use inside MangoMan only; no copying, changing or reselling (see the MangoMan LICENSE).
metadata:
  pack: mangoman
  version: "1.0"
---

# Reel scripts

You write short videos the way the best creators in the niche do: every reel
starts from a real problem the audience has, stops the scroll in the first
second, earns the watch with value, gives a clear solution, and ends with one
action that serves the reel's goal. The scripts in this skill's `scripts/`
folder lay out each reel as a timed shot list and check it, so your job is
the idea and the words.

## 1. Understand (ask once, in one message)

Ask only what you do not know (the brief or the research may already say):

1. **The business and who it serves.**
2. **Goal of this set:** more followers, comments, saves, shares or leads (a mix is normal).
3. **Platforms:** Instagram, YouTube Shorts, Facebook, TikTok.
4. **Who appears on camera,** or no face (voice-over, hands, screen, product).
5. **Facts to use:** prices, results, numbers, real customer words. You will use only these.
6. **Language:** the one the audience speaks (mixing is fine when they do).

If the user says "just do it", pick sensible answers, state them in one line,
and continue (facts are never among them).

## 2. Research the audience first

Reels that grow accounts answer what people already feel. Before writing,
build the audience map from what you found (reviews, comments under
competitor posts, forums, marketplace questions, search suggestions), in the
audience's own words, up to 10 of each:

| List | What goes in it |
|---|---|
| `problems` | What goes wrong for them |
| `frustrations` | What annoys them about it, or about the usual fixes |
| `outcomes` | What they want instead |
| `questions` | What they ask again and again |
| `mistakes` | What they commonly get wrong |
| `objections` | Why they do not buy or act |
| `beliefs` | Limiting beliefs ("I'm too old to start", "good food is never cheap") |
| `emotions` | The feeling behind the problem (fear, guilt, embarrassment, pride) |
| `phrases` | Exact words they use, to put in hooks and captions |

When `mangoman-market-research` already ran, take the map from it. Every
reel answers one item from these lists (its `angle`), copied word for word.

## 3. Write each reel

- **Goal first.** One per reel. The call to action must serve it:

  | Goal | Call to action (`cta.action`) | What it says |
  |---|---|---|
  | followers | `follow` | A reason to follow ("Follow for one kitchen fix a day") |
  | comments | `comment` | An easy but real question |
  | saves | `save` | Why it is worth coming back to |
  | shares | `share` | Who should get it ("Send this to the friend who...") |
  | leads | `dm`, `link`, `call`, `whatsapp`, `book`, `order`, `visit` | A natural next step, never desperate |

- **Hook: three options,** each said within 3 seconds (8 words or fewer),
  different in kind: a mistake called out, a surprising fact from the user's
  facts, a bold opinion, a question in the audience's words, a result shown
  first. Pair each with on-screen text (8 words or fewer) and what the viewer
  sees in the first frame. Movement or a face in the first second.
- **Value:** say why the problem happens, so the viewer feels understood. No
  filler, no "in this video".
- **Solution:** clear steps, a simple framework, or a before and after. One
  idea per reel; if the hook promises three steps, give three.
- **Call to action:** one, at the end. Never "follow", "save" or "link in
  bio" earlier in the reel.
- **On-screen text on every shot:** most people watch without sound.
- **Length:** set `length` in seconds (15 to 30 for reach, up to 60 for
  how-tos, longer only when the topic earns it). The builder times the script
  at a normal speaking pace; the check fails a script far over its target.
- **Search:** 2 to 4 `keywords` people type into the platform's search; put
  at least one in the caption and one in the spoken lines or on-screen text.
- **Caption:** the point in the first line (about 125 characters show), then
  the useful detail, then the same action. 3 to 8 one-word hashtags.
- **Cover text:** what the reel is about in a few words, for the profile grid.
- **Sound:** say the kind of sound (calm, upbeat, a trending audio in the
  niche) and tell the user to pick one trending this week from the
  platform's audio list. Never name a song as trending unless you saw it.
- **Never invent facts.** Every number, price, result or quote comes from the
  user; put them in `facts`. The checker fails any other number.
- Write the way the brand's best person talks to one customer. No "it's not
  X, it's Y", no "game-changer", no engagement bait, no em dashes.

Write `reels.json`:

```json
{
  "brand": "Kesari Foods",
  "language": "en",
  "facts": {"price": "₹749 for a box of 6"},
  "audience": {
    "mistakes": ["ripening mangoes in the fridge"],
    "questions": ["how do I know a mango is naturally ripened"],
    "phrases": ["chemical mangoes"]
  },
  "reels": [
    {"id": "fridge-mistake", "title": "Stop ripening mangoes in the fridge", "goal": "saves",
     "angle": {"from": "mistakes", "text": "ripening mangoes in the fridge"},
     "platforms": ["instagram", "youtube"], "length": 25, "date": "2026-11-02", "time": "19:00",
     "hooks": [{"say": "Your fridge is ruining your mangoes.", "text": "Fridge = flavourless mangoes?", "show": "Hand pulls a hard mango out of the fridge"}],
     "beats": [{"part": "value", "say": "...", "text": "...", "show": "..."},
               {"part": "solution", "say": "...", "text": "...", "show": "..."}],
     "cta": {"action": "save", "say": "Save this for your next box.", "text": "Save for mango season", "show": "Ripe mango sliced"},
     "caption": "Ripening mangoes in the fridge? Here is why they taste flat...", "cover": "Fridge mistake",
     "keywords": ["ripening mangoes"], "hashtags": ["Mangoes", "Alphonso", "KitchenTips"],
     "sound": "Calm acoustic; pick one trending this week in the Reels audio list"}
  ]
}
```

Write 5 to 10 reels for a set, with a mix of goals and angles.

## 4. Build, check, fix

```
python3 <skill dir>/scripts/build_reels.py reels.json --out reels
python3 <skill dir>/scripts/check_reels.py reels.json reels
```

`reels.md` has every script as a timed shot table with the hook options,
caption and posting notes; `reels.csv` has one row per reel and platform for
a posting sheet. Fix every FAIL and build again; read the WARN lines and fix
what you can.

## 5. Deliver

Give the user `reels.md` and `reels.csv`, one line on the set (goals and
angles covered), and the check result in one line ("Checked: every reel
answers a real audience problem, hooks within 3 seconds, one call to action
fitting its goal, keywords in every caption, every number from your facts").
Offer the next set, or images for the covers with `mangoman-social-posts`.
