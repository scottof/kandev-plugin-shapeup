# Smoke test on a local kandev

A manual check of the whole loop on a running kandev v0.97.0 at
`http://localhost:38429`. It takes about fifteen minutes, most of it waiting
for a short cycle to end. Use a throwaway workspace or throwaway tasks: the
circuit breaker really archives them.

## 1. Build and install

```sh
export GOTOOLCHAIN=auto
make verify-package-host
curl --fail --show-error \
  -F 'package=@kandev-plugin-shapeup-0.1.0.tar.gz;type=application/gzip' \
  http://localhost:38429/api/plugins/install
```

Check: **Settings > Plugins** lists **Shape Up** as running, and the sidebar
shows a **Shape Up** item.

## 2. Grant workspace access

In **Settings > Plugins > Shape Up > Workspace access**, grant
`host.v2.write:tasks` for the test workspace.

## 3. Import the workflow

In **Settings > Workspaces > (test workspace) > Workflows**, choose **Import**
and pick `workflows/shape-up.yaml`.

Check: a **Shape Up** workflow with the steps Raw ideas, Shape, Betting table,
Build and Ship. Move a task into **Shape** once and check that the agent starts
in plan mode and stops after saving a pitch.

## 4. Start a short cycle

The length field accepts fractions of a day, down to one minute.

1. Open **Shape Up** and start a cycle named `Smoke` with length `0.005`
   (7 minutes 12 seconds). Check the deadline shown matches your local time.
2. Create two throwaway tasks, `Smoke ship` and `Smoke cut`, and move them to
   **Betting table**.
3. Under **Place a bet**, bet on both. Check that they are listed first, with
   "(betting table)".
4. Mark `Smoke ship` as shipped. Optionally move `Smoke cut` to **Build** so an
   agent is running on it when the deadline hits.

## 5. Watch the circuit breaker

After the deadline passes (within one minute, or at once when you reload the
page):

- `Smoke cut` is archived in kandev. If an agent was running on it, it has
  stopped.
- The page shows no active cycle and offers to start a new one.
- **Past cycles** lists `Smoke` with 1 shipped and 1 cut.
- `Smoke ship` is not archived.

## 6. Restart behavior

1. Start another `0.005` day cycle and bet on a new throwaway task.
2. Stop kandev, wait until the deadline has passed, start kandev again.
3. Without opening the page, check within a minute that the task is archived:
   the breaker runs when the plugin starts.

## 7. Missing grant

1. Revoke the workspace grant, start a `0.005` day cycle and bet on a
   throwaway task.
2. Use **Cut now**: the page shows an error that names the missing grant, and
   the bet stays active.
3. Let the deadline pass: a red banner says the breaker could not cut every
   bet, and the cycle stays open.
4. Grant access again: within a minute the task is archived and the cycle
   closes.

Write down the kandev version, the package archive digest
(`shasum -a 256 kandev-plugin-shapeup-0.1.0.tar.gz`) and the result of each
step.
