-- This table starts empty on every cluster, including one upgraded in place from a version
-- that already had Rulesets (e.g. the old test/e2e seed step, which used to create a
-- "Timadorus" Ruleset directly). The uniqueness constraint below only applies going forward:
-- it reserves names for Rulesets created after this migration runs, and does not backfill
-- names for pre-existing Ruleset aggregates. So the first timadorus-engine startup after
-- upgrading an already-seeded cluster in place will reserve "Timadorus" successfully and
-- create a *second* Ruleset with that name, rather than recognizing the one that already
-- exists. This is a known, accepted gap (see docs/BACKLOG.md) -- a real backfill would need
-- to derive each existing Ruleset's current name from its RulesetCreated event plus the
-- latest RulesetRenamed event, which is non-trivial and out of scope here. For an existing
-- dev cluster, the fix is to reset it (`make dev-down && make dev-up`) rather than upgrading
-- it in place.
CREATE TABLE ruleset_names (
    name TEXT PRIMARY KEY
);
