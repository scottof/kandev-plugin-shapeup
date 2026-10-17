# Changelog

## [0.1.0] - 2026-10-17

First release, started from `kdlbs/kandev-plugin-template`.

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

### Known issues

- If you start a change in workspace A, switch to workspace B, start another
  change there and return to A before the first one finishes, the buttons in A
  are enabled again too early. A second submit is refused by the plugin with a
  conflict, so no data is lost.
