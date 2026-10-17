# Pitch: Shape Up for kandev, v1

## Problem

Agents make it cheap to start everything. With several side projects running in kandev at once, the hard part is no
longer writing code. It is deciding what deserves attention and stopping work that drags on. Kandev workflows start
from a Backlog column, so ideas pile up as a queue, nothing has a fixed time box, and unfinished tasks get extended by
default. Shape Up answers this with shaped pitches, a betting table, fixed cycles, and a circuit breaker that cuts
unfinished work at the end of a cycle instead of extending it.

## Appetite

Small batch: about two weeks of evenings for one developer (or the agent equivalent). The scope below is cut to fit
that, not estimated from it.

## Solution

### 1. Shape Up workflow template

`workflows/shape-up.yaml`, importable through kandev's workflow import (same `version: 2`, `type: kandev_workflow`
format as a workflow export from kandev v0.97.0). Steps:

- **Raw ideas**: start step. Ideas are not a backlog; nothing here is promised.
- **Shape**: the agent drafts a pitch (problem, appetite, solution, rabbit holes, no-gos) as the task plan, in plan mode,
  then stops for human review. It does not write code.
- **Betting table**: a human-only step. Tasks wait here until a bet is placed on them in the plugin page.
- **Build**: the agent implements the pitch. The step prompt tells it to read the pitch first and to treat no-gos and
  rabbit holes as hard constraints; discovered work goes into sub-tasks (scopes) only when it is inside the pitch.
- **Ship**: review and manual merge.

### 2. Cycles and bets (plugin backend)

Stored in kandev Host state (`capabilities.state`), scoped to the workspace:

- **Cycle**: id, name, start, end (the deadline), status (`active`, `closed`). At most one active cycle per workspace.
- **Bet**: task id, cycle id, appetite (`small` = part of a cycle, `big` = the whole cycle), placed at, status
  (`active`, `shipped`, `cut`), closed at.

Domain logic (start cycle, place bet, mark shipped, cut, close cycle) is pure Go with time injected and covered by
tests against a fake Host.

### 3. Betting table page (plugin UI)

A sidebar nav item and native route, built from `host.ui` components:

- The active cycle: name, deadline, time left. A form to start a cycle when none is active (name and length in days).
- The bets of the active cycle: task title (linking to the task), appetite, status, time left. Actions: mark shipped,
  cut now.
- Place a bet: pick a task from the workspace (prefer tasks in the Betting table step) and an appetite.
- History of closed cycles with their shipped and cut bets (read-only).

The UI calls the backend through manifest `actions` (authenticated by the host), never through a public webhook.

### 4. Circuit breaker

When the active cycle's deadline passes, every bet still `active` is cut: its kandev task is archived through the Host
task API, the bet is marked `cut`, and the cycle is closed. Nothing is extended automatically; to continue, someone
re-shapes the idea and places a new bet in a later cycle. The check must be idempotent and must also run when the plugin
starts (kandev may have been off when the deadline passed), not only on a timer.

## Rabbit holes

- **Running agents.** Verify in `../kandev` what archiving a task does to a running agent session. If it does not stop
  the agent, report it rather than building a workaround.
- **Background work in the plugin process.** Verify the SDK allows a long-lived goroutine with Host access, and what
  happens across plugin restarts and upgrades. If not, run the check on events and on page actions instead.
- **Capabilities.** Declare only what is used (`state`, `api_read: tasks` and whatever else is needed, `api_write:
tasks`). Host v2 capabilities may need a workspace grant in Settings > Plugins; document it in the README.
- **Host state values** are JSON objects, numbers come back as float64. Keep the stored shape versioned.
- **Workflow YAML** must match the importer in v0.97.0 exactly. Check it against the import code, not just an export.
- **Time zones.** Store deadlines in UTC, render them in the viewer's locale with `host.utils`.
- **Template leftovers.** Remove the source-control recipe, the ping webhook, the demo page parts and the demo config
  fields, and keep the Makefile, `package.json` and package verifier consistent with what remains.

## No-gos

- Hill charts.
- Token, cost or review budgets (appetite stays a time box in v1).
- Multi-user features, roles or permissions beyond what kandev provides.
- An MCP server or agent skills outside kandev.
- Pitch expiry for unbet ideas, cool-down tracking, notifications.
- Publishing to the plugin marketplace, release automation changes, pushing to GitHub.
- Any change to kandev itself.

## Done means

- The checks listed in the README are green and `make verify-package-host` produces a tarball.
- README explains install, the workspace grant, importing the workflow, and a first cycle.
- A smoke test plan Michele can run on his kandev (localhost:38429): install the tarball, import the workflow, start a
  cycle a few minutes long, bet on a throwaway task, and watch the circuit breaker archive it.

## Circuit breaker for this project

If after one real cycle on his own projects Michele has not used the plugin to make a single real decision about
stopping or re-betting something, the project stops there.
