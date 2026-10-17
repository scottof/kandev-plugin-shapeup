// Shape Up plugin UI: the betting table page.
//
// A hand-written, no-build ES module. kandev serves it from the installed
// package and calls window.registerKandevPlugin below. It uses only the
// host's React (host.jsx), host.ui components and host.utils; never import or
// bundle React, Radix or recharts here.
//
// Every read and write goes through the manifest-declared actions
// (host.api.invokeAction). The host authenticates the caller and verifies the
// workspace, and the backend re-runs the circuit breaker before answering, so
// the page always shows the state after any overdue cut.

export const PLUGIN_ID = "kandev-plugin-shapeup";
export const ROUTE = "/shape-up";
const REFRESH_MS = 60 * 1000;

// errorMessage reads the safe message the backend puts in {"error": ...}.
export function errorMessage(err) {
  const body = err && err.body;
  if (body && typeof body === "object" && typeof body.error === "string" && body.error) {
    return body.error;
  }
  return "Something went wrong. Try again.";
}

// timeLeftLabel renders the time to the deadline in the viewer's locale.
export function timeLeftLabel(utils, endIso, nowMs) {
  if (Date.parse(endIso) <= nowMs) return "Deadline passed";
  return `Ends ${utils.formatRelativeTime(endIso)}`;
}

export function formatDate(iso) {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(
    new Date(iso),
  );
}

const APPETITE_LABEL = { small: "Small batch", big: "Big batch" };
const STATUS_VARIANT = { active: "default", shipped: "secondary", cut: "destructive" };

function useActiveWorkspaceId(host) {
  const React = host.React;
  const ctx = host.context;
  const [workspaceId, setWorkspaceId] = React.useState(() =>
    ctx && ctx.getActiveWorkspaceId ? ctx.getActiveWorkspaceId() || "" : "",
  );
  React.useEffect(() => {
    if (!ctx || !ctx.subscribeActiveWorkspace) return undefined;
    setWorkspaceId(ctx.getActiveWorkspaceId() || "");
    return ctx.subscribeActiveWorkspace((id) => setWorkspaceId(id || ""));
  }, []);
  return workspaceId;
}

function makeBettingTablePage(host) {
  const { jsx: h, ui, utils, toast } = host;
  const React = host.React;
  const {
    Alert,
    AlertDescription,
    AlertTitle,
    Badge,
    Button,
    Card,
    CardContent,
    CardDescription,
    CardHeader,
    CardTitle,
    Empty,
    EmptyDescription,
    EmptyHeader,
    EmptyTitle,
    Input,
    Label,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
    Skeleton,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
  } = ui;

  function invoke(key, workspaceId, body) {
    return host.api.invokeAction(key, { workspaceId, body: body || {} });
  }

  function TaskLink({ bet }) {
    return h(
      Button,
      {
        type: "button",
        variant: "link",
        className: "h-auto p-0 font-medium",
        onClick: () => host.navigate(`/t/${encodeURIComponent(bet.task_id)}`),
      },
      bet.task_title || bet.task_id,
    );
  }

  function StartCycleForm({ busy, onStart }) {
    const [name, setName] = React.useState("");
    const [days, setDays] = React.useState("14");
    const submit = (event) => {
      event.preventDefault();
      onStart({ name: name.trim(), days: Number.parseFloat(days) || 0 });
    };
    return h(
      Card,
      null,
      h(
        CardHeader,
        null,
        h(CardTitle, null, "No active cycle"),
        h(
          CardDescription,
          null,
          "Start a cycle to place bets. At the deadline, every unfinished bet is cut and its task is archived.",
        ),
      ),
      h(
        CardContent,
        null,
        h(
          "form",
          { className: "flex flex-wrap items-end gap-3", onSubmit: submit },
          h(
            "div",
            { className: "grid gap-1.5" },
            h(Label, { htmlFor: "shapeup-cycle-name" }, "Cycle name"),
            h(Input, {
              id: "shapeup-cycle-name",
              value: name,
              maxLength: 80,
              placeholder: "October cycle",
              onChange: (e) => setName(e.target.value),
            }),
          ),
          h(
            "div",
            { className: "grid gap-1.5" },
            h(Label, { htmlFor: "shapeup-cycle-days" }, "Length in days"),
            h(Input, {
              id: "shapeup-cycle-days",
              type: "number",
              min: 0.001,
              max: 90,
              step: "any",
              className: "w-28",
              value: days,
              onChange: (e) => setDays(e.target.value),
            }),
          ),
          h(
            Button,
            { id: "shapeup-start-cycle", type: "submit", disabled: busy || !name.trim() },
            "Start cycle",
          ),
        ),
      ),
    );
  }

  function BetsTable({ cycle, busy, overdue, onShip, onCut, nowMs }) {
    const [confirming, setConfirming] = React.useState("");
    if (!cycle.bets || cycle.bets.length === 0) {
      return h(
        Empty,
        { id: "shapeup-no-bets" },
        h(
          EmptyHeader,
          null,
          h(EmptyTitle, null, "No bets yet"),
          h(EmptyDescription, null, "Pick a shaped pitch below and bet on it for this cycle."),
        ),
      );
    }
    return h(
      Table,
      { id: "shapeup-bets" },
      h(
        TableHeader,
        null,
        h(
          TableRow,
          null,
          h(TableHead, null, "Task"),
          h(TableHead, null, "Appetite"),
          h(TableHead, null, "Status"),
          h(TableHead, null, "Time left"),
          h(TableHead, { className: "text-right" }, "Actions"),
        ),
      ),
      h(
        TableBody,
        null,
        cycle.bets.map((bet) =>
          h(
            TableRow,
            { key: bet.task_id },
            h(TableCell, null, h(TaskLink, { bet })),
            h(TableCell, null, APPETITE_LABEL[bet.appetite] || bet.appetite),
            h(TableCell, null, h(Badge, { variant: STATUS_VARIANT[bet.status] || "outline" }, bet.status)),
            h(
              TableCell,
              { className: "text-muted-foreground text-xs" },
              bet.status === "active" ? timeLeftLabel(utils, cycle.end, nowMs) : formatDate(bet.closed_at),
            ),
            h(
              TableCell,
              { className: "text-right" },
              bet.status !== "active"
                ? null
                : h(
                    "div",
                    { className: "flex justify-end gap-2" },
                    // Past the deadline only a cut is possible.
                    overdue
                      ? null
                      : h(
                          Button,
                          {
                            type: "button",
                            size: "sm",
                            variant: "outline",
                            disabled: busy,
                            onClick: () => onShip(bet.task_id),
                          },
                          "Mark shipped",
                        ),
                    confirming === bet.task_id
                      ? h(
                          Button,
                          {
                            type: "button",
                            size: "sm",
                            variant: "destructive",
                            disabled: busy,
                            onClick: () => {
                              setConfirming("");
                              onCut(bet.task_id);
                            },
                          },
                          "Confirm cut",
                        )
                      : h(
                          Button,
                          {
                            type: "button",
                            size: "sm",
                            variant: "ghost",
                            disabled: busy,
                            title: "Archives the task and stops its agent",
                            onClick: () => setConfirming(bet.task_id),
                          },
                          "Cut now",
                        ),
                  ),
            ),
          ),
        ),
      ),
    );
  }

  function PlaceBetForm({ workspaceId, busy, onPlace, refreshKey }) {
    const [state, setState] = React.useState({ loading: true, error: "", tasks: [] });
    const [taskId, setTaskId] = React.useState("");
    const [appetite, setAppetite] = React.useState("small");
    React.useEffect(() => {
      let live = true;
      setState((s) => ({ ...s, loading: true, error: "" }));
      invoke("tasks.candidates", workspaceId).then(
        (res) => live && setState({ loading: false, error: "", tasks: (res && res.tasks) || [] }),
        (err) => live && setState({ loading: false, error: errorMessage(err), tasks: [] }),
      );
      return () => {
        live = false;
      };
    }, [workspaceId, refreshKey]);

    let picker;
    if (state.loading) {
      picker = h(Skeleton, { className: "h-9 w-72" });
    } else if (state.error) {
      picker = h("p", { className: "text-destructive text-xs" }, state.error);
    } else if (state.tasks.length === 0) {
      picker = h("p", { className: "text-muted-foreground text-xs" }, "No open tasks to bet on in this workspace.");
    } else {
      picker = h(
        Select,
        { value: taskId, onValueChange: setTaskId },
        h(SelectTrigger, { id: "shapeup-bet-task", className: "w-72" }, h(SelectValue, { placeholder: "Pick a task" })),
        h(
          SelectContent,
          null,
          state.tasks.map((t) =>
            h(
              SelectItem,
              { key: t.task_id, value: t.task_id },
              t.at_betting_table ? `${t.title} (betting table)` : t.title,
            ),
          ),
        ),
      );
    }

    return h(
      Card,
      null,
      h(
        CardHeader,
        null,
        h(CardTitle, null, "Place a bet"),
        h(CardDescription, null, "Tasks waiting at the betting table come first."),
      ),
      h(
        CardContent,
        null,
        h(
          "div",
          { className: "flex flex-wrap items-end gap-3" },
          h("div", { className: "grid gap-1.5" }, h(Label, { htmlFor: "shapeup-bet-task" }, "Task"), picker),
          h(
            "div",
            { className: "grid gap-1.5" },
            h(Label, { htmlFor: "shapeup-bet-appetite" }, "Appetite"),
            h(
              Select,
              { value: appetite, onValueChange: setAppetite },
              h(SelectTrigger, { id: "shapeup-bet-appetite", className: "w-56" }, h(SelectValue, null)),
              h(
                SelectContent,
                null,
                h(SelectItem, { value: "small" }, "Small batch (part of the cycle)"),
                h(SelectItem, { value: "big" }, "Big batch (the whole cycle)"),
              ),
            ),
          ),
          h(
            Button,
            {
              id: "shapeup-place-bet",
              type: "button",
              disabled: busy || !taskId,
              onClick: () =>
                onPlace({ task_id: taskId, appetite }).then((placed) => {
                  if (placed) setTaskId("");
                }),
            },
            "Place bet",
          ),
        ),
      ),
    );
  }

  function History({ cycles }) {
    if (!cycles || cycles.length === 0) return null;
    return h(
      Card,
      null,
      h(CardHeader, null, h(CardTitle, null, "Past cycles")),
      h(
        CardContent,
        { className: "grid gap-4" },
        cycles.map((cycle) => {
          const bets = cycle.bets || [];
          const shipped = bets.filter((b) => b.status === "shipped").length;
          const cut = bets.filter((b) => b.status === "cut").length;
          return h(
            "div",
            { key: cycle.id, className: "grid gap-1" },
            h(
              "div",
              { className: "flex flex-wrap items-baseline gap-2" },
              h("span", { className: "font-medium" }, cycle.name),
              h(
                "span",
                { className: "text-muted-foreground text-xs" },
                `${formatDate(cycle.start)} to ${formatDate(cycle.end)}. ${shipped} shipped, ${cut} cut.`,
              ),
            ),
            bets.length === 0
              ? h("p", { className: "text-muted-foreground text-xs" }, "No bets in this cycle.")
              : h(
                  "ul",
                  { className: "grid gap-0.5 text-sm" },
                  bets.map((bet) =>
                    h(
                      "li",
                      { key: bet.task_id, className: "flex items-center gap-2" },
                      h(Badge, { variant: STATUS_VARIANT[bet.status] || "outline" }, bet.status),
                      h("span", null, bet.task_title || bet.task_id),
                    ),
                  ),
                ),
          );
        }),
      ),
    );
  }

  return function BettingTablePage() {
    const workspaceId = useActiveWorkspaceId(host);
    const [state, setState] = React.useState({ loading: true, error: "", data: null });
    const [busy, setBusy] = React.useState(false);
    const [refreshKey, setRefreshKey] = React.useState(0);
    const [reloadKey, setReloadKey] = React.useState(0);
    // The workspace the page shows now. A reply for another workspace is
    // dropped instead of drawing that workspace's cycle here.
    const currentWorkspace = React.useRef(workspaceId);
    currentWorkspace.current = workspaceId;

    React.useEffect(() => {
      if (!workspaceId) return undefined;
      // One flag per effect run covers the first load and every refresh.
      let live = true;
      const fetchState = () =>
        invoke("state.get", workspaceId).then(
          (data) => live && setState({ loading: false, error: "", data }),
          (err) => live && setState((s) => ({ loading: false, error: errorMessage(err), data: s.data })),
        );
      setState({ loading: true, error: "", data: null });
      fetchState();
      // Picks up a circuit-breaker cut while the page stays open.
      const timer = setInterval(fetchState, REFRESH_MS);
      return () => {
        live = false;
        clearInterval(timer);
      };
    }, [workspaceId, reloadKey]);

    // run resolves to true when the action succeeded for the workspace the
    // page still shows.
    const run = (key, body, success) => {
      const requested = workspaceId;
      const stale = () => currentWorkspace.current !== requested;
      setBusy(true);
      return invoke(key, requested, body)
        .then((data) => {
          if (stale()) return false;
          setState({ loading: false, error: "", data });
          setRefreshKey((k) => k + 1);
          if (success) toast.success(success);
          return true;
        })
        .catch((err) => {
          if (!stale()) toast.error(errorMessage(err));
          return false;
        })
        .finally(() => setBusy(false));
    };

    if (!workspaceId) {
      return h(
        "div",
        { className: "p-4" },
        h(Empty, null, h(EmptyHeader, null, h(EmptyTitle, null, "Pick a workspace"), h(EmptyDescription, null, "Cycles and bets belong to a workspace."))),
      );
    }
    if (state.loading) {
      return h(
        "div",
        { id: "shapeup-loading", className: "grid max-w-4xl gap-4 p-4" },
        h(Skeleton, { className: "h-32 w-full" }),
        h(Skeleton, { className: "h-48 w-full" }),
      );
    }
    if (!state.data) {
      return h(
        "div",
        { className: "max-w-4xl p-4" },
        h(
          Alert,
          { id: "shapeup-error", variant: "destructive" },
          h(AlertTitle, null, "Could not load the betting table"),
          h(AlertDescription, null, state.error),
        ),
        h(
          Button,
          { type: "button", variant: "outline", className: "mt-3", onClick: () => setReloadKey((k) => k + 1) },
          "Try again",
        ),
      );
    }

    const { active, history, now } = state.data;
    const nowMs = Date.parse(now) || Date.now();
    const failing = active ? (active.bets || []).filter((b) => b.status === "active" && b.error) : [];

    return h(
      "div",
      { className: "grid max-w-4xl gap-4 p-4" },
      state.error
        ? h(Alert, { variant: "destructive" }, h(AlertDescription, null, state.error))
        : null,
      failing.length > 0
        ? h(
            Alert,
            { id: "shapeup-breaker-blocked", variant: "destructive" },
            h(AlertTitle, null, "The circuit breaker could not cut every bet"),
            h(AlertDescription, null, failing[0].error),
          )
        : null,
      active
        ? h(
            Card,
            null,
            h(
              CardHeader,
              null,
              h(CardTitle, { id: "shapeup-cycle-name" }, active.name),
              h(
                CardDescription,
                null,
                `Deadline ${formatDate(active.end)}. ${timeLeftLabel(utils, active.end, nowMs)}.`,
              ),
            ),
            h(
              CardContent,
              null,
              h(BetsTable, {
                cycle: active,
                busy,
                overdue: state.data.overdue,
                nowMs,
                onShip: (taskId) => run("bet.ship", { task_id: taskId }, "Marked as shipped"),
                onCut: (taskId) => run("bet.cut", { task_id: taskId }, "Bet cut and task archived"),
              }),
            ),
          )
        : h(StartCycleForm, { busy, onStart: (body) => run("cycle.start", body, "Cycle started") }),
      active && !state.data.overdue
        ? h(PlaceBetForm, {
            workspaceId,
            busy,
            refreshKey,
            onPlace: (body) => run("bet.place", body, "Bet placed"),
          })
        : null,
      h(History, { cycles: history }),
    );
  };
}

window.registerKandevPlugin(PLUGIN_ID, {
  initialize(registry, host) {
    registry.registerNavItem({
      id: "shape-up",
      label: "Shape Up",
      path: ROUTE,
      icon: "calendar",
      section: "main",
    });
    registry.registerRoute(ROUTE, makeBettingTablePage(host), {
      topbar: { subtitle: "Betting table" },
    });
  },
});
