#!/usr/bin/env bash
# Build every skill pack's sample and run its checker; exits 1 if any fails.
# Needs Python 3 with the packages in internal/pyenv/requirements.txt (mangoman ready installs them).
set -u
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
go build -o "$work/mm" ./cmd/mangoman || exit 1
"$work/mm" skills install --dir "$work/skills" > /dev/null || exit 1
# Installed packs leave their tests out; the agent checks below score with them.
for t in internal/skills/packs/*/tests; do cp -r "$t" "$work/skills/$(basename "$(dirname "$t")")/"; done
export MANGOMAN_HOME="$work/home" M="$work/mm" AG="$PWD/agents"
fail=0
run() { # name, then the build and check commands (S = this pack's scripts folder)
  local name=$1 build=$2 check=$3 d="$work/$1"
  mkdir -p "$d" && cp internal/skills/testdata/assets/* "$d/" && cp -r internal/skills/testdata/"$name"/* "$d/"
  if (cd "$d" && eval "$build" > build.log 2>&1 && eval "$check" > check.log 2>&1); then
    echo "PASS  $name ($(grep -c '^PASS' "$d/check.log") checks)"
  else
    echo "FAIL  $name"; cat "$d/build.log" "$d/check.log" 2>/dev/null | grep -v '^PASS' | sed 's/^/      /'; fail=1
  fi
}
P="$work/skills"
run dashboard "python3 $P/mangoman-data-dashboard/scripts/build_dashboard.py analysis.py --out dashboard" "python3 $P/mangoman-data-dashboard/scripts/check.py analysis.py dashboard"
run deck      "python3 $P/mangoman-ceo-deck/scripts/build_deck.py analysis.py --out deck"            "python3 $P/mangoman-ceo-deck/scripts/check_deck.py analysis.py deck"
run resume    "python3 $P/mangoman-resume/scripts/build.py resume.json --template modern --out resume" "python3 $P/mangoman-resume/scripts/check.py resume.json resume.pdf --pages 1"
run social    "python3 $P/mangoman-social-posts/scripts/build_posts.py posts.json --out posts"       "python3 $P/mangoman-social-posts/scripts/check_posts.py posts.json posts"
run landing   "python3 $P/mangoman-landing-page/scripts/build_page.py page.json --out site"           "python3 $P/mangoman-landing-page/scripts/check_page.py page.json site"
run proposal  "python3 $P/mangoman-proposal/scripts/build_proposal.py proposal.json --out proposal"  "python3 $P/mangoman-proposal/scripts/check_proposal.py proposal.json proposal"
run email     "python3 $P/mangoman-email-campaign/scripts/build_emails.py campaign.json --out emails" "python3 $P/mangoman-email-campaign/scripts/check_emails.py campaign.json emails"
run listing   "python3 $P/mangoman-ecommerce-listing/scripts/build_listing.py listing.json --out listing" "python3 $P/mangoman-ecommerce-listing/scripts/check_listing.py listing.json listing"
run invoice   "python3 $P/mangoman-invoice/scripts/build_invoice.py invoice.json --out invoice"    "python3 $P/mangoman-invoice/scripts/check_invoice.py invoice.json invoice"
run ads       "python3 $P/mangoman-ad-copy/scripts/build_ads.py ads.json --out ads"                "python3 $P/mangoman-ad-copy/scripts/check_ads.py ads.json ads"
run seo       "python3 $P/mangoman-seo-article/scripts/build_article.py article.json --out article" "python3 $P/mangoman-seo-article/scripts/check_article.py article.json article"
run report    "python3 $P/mangoman-client-report/scripts/build_report.py analysis.py --out report" "python3 $P/mangoman-client-report/scripts/check_report.py analysis.py report"
run minutes   "python3 $P/mangoman-meeting-minutes/scripts/build_minutes.py minutes.json --out minutes" "python3 $P/mangoman-meeting-minutes/scripts/check_minutes.py minutes.json minutes"
run brand     "python3 $P/mangoman-brand-kit/scripts/build_brand.py brand.json --out brand"      "python3 $P/mangoman-brand-kit/scripts/check_brand.py brand.json brand"
run review    "bash setup.sh && python3 $P/mangoman-code-review/scripts/collect.py repo --out facts.json" "python3 $P/mangoman-code-review/scripts/check_review.py review.json facts.json --out review.md"
run copy      "python3 $P/mangoman-website-copy/scripts/brief_gate.py facts.md --pages 3 && ! python3 $P/mangoman-website-copy/scripts/brief_gate.py thin.md --pages 3 && python3 $P/mangoman-website-copy/scripts/build_copy.py site.json --out site && ! python3 $P/mangoman-website-copy/scripts/build_copy.py broken-quote.json --out x 2> bq.txt && grep -q 'column 26 is inside the text' bq.txt && ! python3 $P/mangoman-website-copy/scripts/build_copy.py broken-bracket.json --out x 2> bb.txt && grep -q 'delete this extra }, line' bb.txt" "python3 $P/mangoman-website-copy/scripts/check_copy.py site.json site"
run webapp    "python3 $P/mangoman-web-app/scripts/scaffold.py app.json --out starter"         "python3 $P/mangoman-web-app/scripts/check_app.py app/index.html tests.json"
# Advanced agents: sign, install and run through the sandbox, then score on the free pack's test set.
L="$P/mangoman-ecommerce-listing"
run listing-pro "cp $L/tests/cases/mango-pulp/* . && \$M agents keygen && \$M agents pack \$AG/amazon-listing-pro --out a.mmagent && \$M agents install a.mmagent && \$M agents exec amazon-listing-pro research.py --terms search_terms.csv --competitors competitors.md --current current_listing.md --brand Kesari --out research && \$M agents exec amazon-listing-pro optimise.py listing.json research.json --facts facts.md && \$M agents exec amazon-listing-pro build_listing.py listing.json --out listing" \
  "\$M agents exec amazon-listing-pro check_listing.py listing.json listing && python3 $L/tests/score.py . | python3 -c 'import json,sys; s=json.load(sys.stdin); print(\"PASS  score %s: %s\" % (s[\"score\"], s[\"notes\"])); sys.exit(s[\"score\"] < 85)'"
A="$P/mangoman-ad-copy"
run ads-pro "cp $A/tests/cases/a2-ghee/* . && python3 $A/tests/score.py . > before.json && \$M agents pack \$AG/google-ads-pro --out g.mmagent && \$M agents install g.mmagent && \$M agents exec google-ads-pro mine.py --terms search_terms.csv --facts facts.md --landing landing.md --out mining && \$M agents exec google-ads-pro fit.py ads.json mining.json --facts facts.md && \$M agents exec google-ads-pro build_ads.py ads.json --out ads" \
  "\$M agents exec google-ads-pro check_ads.py ads.json ads && python3 $A/tests/score.py . | python3 -c 'import json,sys; s=json.load(sys.stdin); b=json.load(open(\"before.json\")); print(\"PASS  score %s (free pack copy before fit %s): %s\" % (s[\"score\"], b[\"score\"], s[\"notes\"])); sys.exit(s[\"score\"] < 90 or s[\"score\"] <= b[\"score\"])'"
exit $fail
