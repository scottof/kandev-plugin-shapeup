# Changelog

## Unreleased

### Added

- Shape Up workflow template (`workflows/shape-up.yaml`): Raw ideas, Shape,
  Betting table, Build and Ship.
- Betting table page: start a cycle, bet on tasks with a small or big appetite,
  mark bets shipped or cut them, and see past cycles.
- Circuit breaker: at the cycle deadline every active bet is cut and its task
  archived, then the cycle closes. Runs every minute, at plugin start and
  before page actions. Needs the `host.v2.write:tasks` workspace grant.
  Each archive gets its own 25 second budget. A run stops starting archives
  once its own deadline passes and keeps what it cut; the remaining bets are
  cut on the next run.
- Only a kandev administrator can start a cycle, place a bet, mark a bet
  shipped or use Cut now. Other members can view the betting table.
  The page tells a non-admin that only an administrator can make the change.
- A bet whose task is already archived (for example by a Cut now whose reply
  timed out) is shown as cut.
- A cycle holds at most 50 bets. Only the 12 most recent past cycles are kept
  and shown; older ones are deleted when a cycle closes.

### Changed

- The plugin is now `kandev-plugin-shapeup` 0.1.0, manifest API version 2,
  requiring kandev 0.97.0 or later.
- `make verify-package-host` accepts the manifest that plugin-pack rewrites
  for a host-only package.

### Removed

- The template's demo page, composer action, `ping` webhook, demo settings and
  the source-control recipe.

## [0.2.0] - 2026-10-05

### Changed

- feat(template): adopt host Action and harden release checks (#8) (b6d5a7c)
- Fix marketplace release archive checksum (#9) (b13b7ee)
- feat: add provider-neutral source-control recipe (#6) (be2f0c5)
- Teach the post-0.86 host surface (#5) (c107c22)
- chore(security): bump x/net & x/text, pin CI actions to commit SHA (#3) (e034b08)
- ci: add manual release workflow (#2) (bd7aed6)
- ci: standardize plugin automation (542f331)
- docs: describe release checksums.txt as advisory, not index-pinned (2c5fd80)
- ci: add release workflow that publishes package + checksums on tag (a25f3d3)
- chore: scaffold kandev plugin template (526ca58)

