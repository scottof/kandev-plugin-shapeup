# kandev-plugin-shapeup

Shape Up for [kandev](https://github.com/kdlbs/kandev): shaped pitches, a
betting table, fixed cycles and a circuit breaker that cuts unfinished work at
the deadline instead of extending it.

- **Workflow template** (`workflows/shape-up.yaml`): Raw ideas, Shape (the
  agent writes a pitch in plan mode and stops), Betting table (human only),
  Build (the agent builds the pitch and treats its no-gos as hard limits) and
  Ship (review and manual merge).
- **Betting table page**: a **Shape Up** item in the sidebar. Start a cycle,
  bet on tasks with a small or big appetite, mark bets shipped or cut them, and
  read the history of past cycles.
- **Circuit breaker**: when the active cycle's deadline passes, every bet
  still active is cut. Its task is archived, which also stops a running agent,
  and the cycle is closed. Nothing is extended: to continue, reshape the idea
  and bet on it again in a later cycle. The check runs every minute, when the
  plugin starts (so a deadline that passed while kandev was off is still
  enforced) and before every action on the page.

The v1 scope and its no-gos are in [`docs/PITCH.md`](docs/PITCH.md).

## Requirements

- kandev **v0.97.0** or later (`min_kandev_version` in `manifest.yaml`).
- A workspace manager to grant the plugin access to archive tasks (below).

## Install

Build the package for your platform and install it:

```sh
export GOTOOLCHAIN=auto
make verify-package-host
```

This writes `kandev-plugin-shapeup-0.1.0.tar.gz`. Install it from
**Settings > Plugins** (upload the archive), or with curl:

```sh
curl --fail --show-error \
  -F 'package=@kandev-plugin-shapeup-0.1.0.tar.gz;type=application/gzip' \
  "$KANDEV_URL/api/plugins/install"
```

## Grant workspace access

The circuit breaker archives tasks with kandev's exact (Host v2) archive
command. kandev only runs it when a workspace manager has granted the plugin
access in that workspace:

1. Open **Settings > Plugins > Shape Up**.
2. In **Workspace access**, select the workspace and grant
   `host.v2.write:tasks`, give a reason and save.

Do this for every workspace where you run cycles, and again after an upgrade
that changes the plugin's capabilities. Without the grant the plugin still
records bets, but it cannot cut them: the betting table shows a red banner and
the overdue cycle stays open until the grant is in place, then the next check
cuts the bets.

The manifest declares only what the plugin uses: `state`, `api_read: tasks,
workflows` and `api_write: tasks`.

Every action that changes a cycle decides which tasks get archived, so starting
a cycle, placing a bet, marking a bet shipped and **Cut now** are
`access: admin` actions: only a kandev administrator can use them, and kandev
answers 403 to other members, which the page shows as "Only a kandev
administrator can do this." Any signed-in member can view the betting table.

## Import the workflow

Open **Settings > Workspaces**, select the workspace, then **Workflows >
Import** and pick `workflows/shape-up.yaml`. kandev may ask you to pick an
agent profile for the Shape and Build steps. Import never overwrites a
workflow that already has the name "Shape Up".

## Run a first cycle

1. Create a task in the **Raw ideas** step and move it to **Shape**. The agent
   writes a pitch (problem, appetite, solution, rabbit holes, no-gos) as the
   task plan and stops.
2. Review the pitch and move the task to **Betting table**.
3. Open **Shape Up** in the sidebar and start a cycle (a name and a length in
   days, two weeks by default; fractions down to one minute are accepted for
   trying it out).
4. Under **Place a bet**, pick the task (tasks at the betting table come first),
   choose an appetite and place the bet. Move the task to **Build**.
5. When the work ships, use **Mark shipped**. **Cut now** cuts a bet early and
   archives its task.
6. At the deadline the circuit breaker cuts whatever is still active and closes
   the cycle. It moves to **Past cycles** with its shipped and cut bets.

Deadlines are stored in UTC. Dates and times use your browser's locale and
time zone, not kandev's language setting: `host.utils` only offers relative
times, which do follow kandev's language.

## How it works

- `server/shapeup/cycle.go`: the rules for cycles and bets. Pure functions; the
  current time is always passed in.
- `server/shapeup/store.go`: plugin Host state. Each value is versioned
  (`"v": 1`). The active cycle is under `workspace/<id>/active`, closed cycles
  under `workspace/<id>/cycle:<id>`, and `instance//workspaces` lists the
  workspaces with an active cycle so the breaker can find them at startup.
- `server/shapeup/breaker.go`: the circuit breaker. It is idempotent: an archive
  that fails (no grant, a version conflict, kandev busy) leaves the bet active
  with the reason, and the next run retries.
- `server/actions.go`: the authenticated page actions (`state.get`,
  `tasks.candidates`, `cycle.start`, `bet.place`, `bet.ship`, `bet.cut`). The
  workspace always comes from the host-verified context, never from the body.
- `server/plugin.go`: starts the breaker loop when kandev injects the Host.
- `ui/bundle.js`: the betting table page, a no-build ES module built from
  `host.ui` components.

## Develop

`pkg/pluginsdk` is not published as a module, so `go.mod` uses
`replace github.com/kandev/kandev => ../kandev/apps/backend`. Check kandev out
as a sibling at the pinned revision:

```sh
git clone https://github.com/kdlbs/kandev.git ../kandev
git -C ../kandev checkout "$(cat .kandev-sdk-ref)"
```

Checks (Go 1.26 via `GOTOOLCHAIN=auto`, Node 20.19+ or 22.12+):

```sh
export GOTOOLCHAIN=auto
npm ci --ignore-scripts
make check-format
make vet
make test-backend      # Go tests against a fake Host, no subprocess
npm run test:ui        # node --test for ui/bundle.js
make verify-package-host
```

`make test` runs the backend and UI tests plus the package and release
verifier tests. The smoke test on a real kandev is in
[`docs/SMOKE.md`](docs/SMOKE.md).

## About

Built with Claude Code agents running in Kandev.

Licensed under the MIT License, see [`LICENSE`](LICENSE).
