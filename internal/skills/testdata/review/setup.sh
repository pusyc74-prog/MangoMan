#!/usr/bin/env bash
# Builds a small repository with one reviewable change in ./repo.
set -e
rm -rf repo && mkdir repo && cd repo
git init -q && git config user.email t@example.com && git config user.name t
cat > pricing.py <<'PY'
def box_price(size):
    prices = {6: 749, 12: 1299, 24: 2399}
    return prices[size]
PY
cat > test_pricing.py <<'PY'
from pricing import box_price, with_gst


def test_box_price():
    assert box_price(12) == 1299


def test_with_gst():
    assert with_gst(1000) == 1180
PY
printf 'def with_gst(amount):\n    return amount\n' >> pricing.py
git add . && git commit -qm "prices"
cat > pricing.py <<'PY'
API_KEY = "sk-live-abcdefghijklmnopqrstuvwx"


def box_price(size):
    prices = {6: 749, 12: 1299, 24: 2399}
    return prices[size]


def with_gst(amount, rate=0.18):
    breakpoint()
    return amount * rate
PY
