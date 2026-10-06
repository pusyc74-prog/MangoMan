"""Stop-and-ask gate: are the user's facts enough to write the website copy?

Usage: python3 brief_gate.py facts.md --pages N
facts.md holds one fact per line (a bullet or a plain line). Exits 0 when there
are enough facts to write N pages without inventing anything. Exits 2 and
prints what to ask the user when there are not: send those questions and write
nothing until the user answers, even if they said "just do it".
"""
import re
import sys

MIN_FACTS, PER_PAGE, MIN_FIGURES = 6, 2, 2


def facts_in(text):
    """The distinct facts in a file: bullets or plain lines, headings left out."""
    lines = [re.sub(r"^\s*(?:[-*]|\d+[.)])\s*", "", l).strip() for l in text.splitlines() if not l.lstrip().startswith("#")]
    return list(dict.fromkeys(l for l in lines if len(l.split()) >= 3))


def gaps(facts, pages):
    """What is missing, as questions for the user (empty when the facts are enough)."""
    need = max(MIN_FACTS, PER_PAGE * pages)
    q = []
    if len(facts) < need:
        q.append("Give me %d more facts a customer would care about (you gave %d, %d pages need at least %d): "
                 "what exactly you sell, who it is for, how it works, where you work, why people trust you." % (need - len(facts), len(facts), pages, need))
    if sum(1 for f in facts if re.search(r"\d", f)) < MIN_FIGURES:
        q.append("Which numbers can I show? For example prices, years in business, customers served, delivery time, ratings.")
    return q


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    pages = int(sys.argv[sys.argv.index("--pages") + 1]) if "--pages" in sys.argv else 1
    with open(sys.argv[1], encoding="utf-8") as f:
        facts = facts_in(f.read())
    q = gaps(facts, pages)
    if q:
        print("FACTS TOO THIN: do not write yet. Ask the user:")
        for x in q:
            print("- " + x)
        sys.exit(2)
    print("PASS  %d facts for %d pages" % (len(facts), pages))


if __name__ == "__main__":
    main()
