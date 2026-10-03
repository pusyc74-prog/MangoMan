#!/usr/bin/env bash
# Build every skill pack's sample and run its checker; exits 1 if any fails.
# Needs Python 3 with pandas, Pillow, python-pptx, python-docx, pypdf and Playwright.
set -u
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
go run ./cmd/mangoman skills install --dir "$work/skills" > /dev/null || exit 1
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
run webapp    "python3 $P/mangoman-web-app/scripts/scaffold.py app.json --out starter"         "python3 $P/mangoman-web-app/scripts/check_app.py app/index.html tests.json"
exit $fail
