-- Mango Arch database (Cloudflare D1). Reports are kept a week, bundles a month.
CREATE TABLE IF NOT EXISTS reports (
  hour    TEXT NOT NULL, -- UTC, like 2026-10-15T09
  install TEXT NOT NULL, -- a MangoMan's random id
  net     TEXT NOT NULL, -- salted hash of its network, to count independent places
  models  TEXT NOT NULL, -- JSON: {"provider/model": {"ok": 12, "slow": 3}}
  PRIMARY KEY (hour, install)
);
CREATE TABLE IF NOT EXISTS bundles (
  issued TEXT PRIMARY KEY, -- UTC time
  data   TEXT NOT NULL,    -- base64 of the signed JSON
  sig    TEXT NOT NULL     -- base64 Ed25519 signature
);
