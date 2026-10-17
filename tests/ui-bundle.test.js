import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

let importCount = 0;

function element(type, props, ...children) {
  return { type, props: props || {}, children };
}

// A minimal stand-in for host.React: state never changes (setter calls are
// recorded), effects run at once and their cleanups are collected so timers
// do not outlive the test. initialStates, when given, replaces the initial
// value of the n-th useState call; refs collects every useRef object.
function fakeReact(cleanups, setCalls, initialStates = {}, refs = []) {
  let stateIndex = 0;
  return {
    useState: (init) => {
      const i = stateIndex++;
      const value = i in initialStates ? initialStates[i] : typeof init === "function" ? init() : init;
      return [value, (v) => setCalls.push(v)];
    },
    useEffect: (fn) => {
      const cleanup = fn();
      if (typeof cleanup === "function") cleanups.push(cleanup);
    },
    useCallback: (fn) => fn,
    useRef: (value) => {
      const ref = { current: value };
      refs.push(ref);
      return ref;
    },
  };
}

// find returns the first element in a rendered tree that matches pred.
function find(node, pred) {
  if (Array.isArray(node)) {
    for (const child of node) {
      const hit = find(child, pred);
      if (hit) return hit;
    }
    return null;
  }
  if (!node || typeof node !== "object") return null;
  if (pred(node)) return node;
  return find(node.children, pred);
}

function hasText(node, text) {
  if (typeof node === "string") return node === text;
  if (Array.isArray(node)) return node.some((child) => hasText(child, text));
  return Boolean(node && typeof node === "object" && hasText(node.children, text));
}

const byName = (name) => (node) => typeof node.type === "function" && node.type.name === name;

const ACTIVE_SNAPSHOT = {
  now: "2026-10-01T09:00:00Z",
  overdue: false,
  history: [],
  active: {
    id: "c1",
    name: "October",
    start: "2026-10-01T09:00:00Z",
    end: "2026-10-15T09:00:00Z",
    status: "active",
    bets: [{ task_id: "t1", task_title: "Search", appetite: "small", status: "active" }],
  },
};

async function loadPlugin() {
  let pluginId;
  let definition;
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: {
      registerKandevPlugin(id, pluginDefinition) {
        pluginId = id;
        definition = pluginDefinition;
      },
    },
  });
  const mod = await import(new URL(`../ui/bundle.js?test=${importCount++}`, import.meta.url));
  return { mod, pluginId, definition };
}

function makeHost({
  workspaceId = "ws1",
  calls = [],
  cleanups = [],
  setCalls = [],
  toasts = [],
  initialStates,
  refs,
  reply,
} = {}) {
  const ui = new Proxy({}, { get: (_, name) => String(name) });
  return {
    jsx: element,
    React: fakeReact(cleanups, setCalls, initialStates, refs),
    ui,
    utils: { formatRelativeTime: (iso) => `relative(${iso})`, cn: (...c) => c.join(" ") },
    toast: {
      success: (msg) => toasts.push(["success", msg]),
      error: (msg) => toasts.push(["error", msg]),
    },
    navigate() {},
    context: {
      getActiveWorkspaceId: () => workspaceId,
      subscribeActiveWorkspace: () => () => {},
    },
    api: {
      invokeAction(key, input) {
        calls.push({ key, input });
        return reply ? reply(key, input) : new Promise(() => {});
      },
    },
  };
}

function initialize(definition, host) {
  const navItems = [];
  const routes = [];
  definition.initialize(
    {
      registerNavItem: (item) => navItems.push(item),
      registerRoute: (path, component, options) => routes.push({ path, component, options }),
    },
    host,
  );
  return { navItems, routes };
}

test("registers under the manifest id with a nav item and a route", async () => {
  const { pluginId, definition } = await loadPlugin();
  const manifest = await readFile(new URL("../manifest.yaml", import.meta.url), "utf8");
  assert.equal(pluginId, "kandev-plugin-shapeup");
  assert.match(manifest, /^id: "kandev-plugin-shapeup"$/m);

  const { navItems, routes } = initialize(definition, makeHost());
  assert.equal(navItems.length, 1);
  assert.equal(navItems[0].path, "/shape-up");
  assert.equal(navItems[0].label, "Shape Up");
  assert.equal(routes.length, 1);
  assert.equal(routes[0].path, "/shape-up");
});

test("the page loads its state for the active workspace through an action", async () => {
  const { definition } = await loadPlugin();
  const calls = [];
  const cleanups = [];
  const host = makeHost({ calls, cleanups });
  const { routes } = initialize(definition, host);

  const tree = routes[0].component();
  cleanups.forEach((fn) => fn());

  assert.equal(tree.props.id, "shapeup-loading", "renders a loading state first");
  assert.deepEqual(calls, [{ key: "state.get", input: { workspaceId: "ws1", body: {} } }]);
});

test("a refresh that resolves after the page moved on is dropped", async () => {
  const { definition } = await loadPlugin();
  const cleanups = [];
  const setCalls = [];
  const pending = [];
  const host = makeHost({
    cleanups,
    setCalls,
    reply: () => new Promise((r) => pending.push(r)),
  });
  const { routes } = initialize(definition, host);

  const realSetInterval = globalThis.setInterval;
  const realClearInterval = globalThis.clearInterval;
  let tick;
  globalThis.setInterval = (fn) => {
    tick = fn;
    return 1;
  };
  globalThis.clearInterval = () => {};
  try {
    routes[0].component();
    tick(); // a periodic refresh is in flight...
    const before = setCalls.length;
    cleanups.forEach((fn) => fn()); // ...when the workspace changes
    const snapshot = { now: "2026-10-01T09:00:00Z", active: null, history: [] };
    pending.forEach((resolve) => resolve(snapshot));
    await new Promise((r) => setTimeout(r, 0));
    assert.equal(pending.length, 2, "first load and one refresh");
    assert.equal(setCalls.length, before, "no state update from a stale reply");
  } finally {
    globalThis.setInterval = realSetInterval;
    globalThis.clearInterval = realClearInterval;
  }
});

// The page's useState calls, in order: the active workspace, the page state,
// busy, the candidates refresh key and the reload key.
const LOADED = { 1: { loading: false, error: "", data: ACTIVE_SNAPSHOT } };

function settle() {
  return new Promise((r) => setTimeout(r, 0));
}

for (const outcome of ["resolves", "rejects"]) {
  test(`a mutation that ${outcome} after a workspace switch is dropped`, async () => {
    const { definition } = await loadPlugin();
    const cleanups = [];
    const setCalls = [];
    const toasts = [];
    const refs = [];
    let settleCut;
    const host = makeHost({
      cleanups,
      setCalls,
      toasts,
      refs,
      initialStates: LOADED,
      reply: (key) =>
        key === "bet.cut"
          ? new Promise((resolve, reject) => {
              settleCut = outcome === "resolves" ? () => resolve(ACTIVE_SNAPSHOT) : () => reject({ body: { error: "Nope" } });
            })
          : new Promise(() => {}),
    });
    const { routes } = initialize(definition, host);

    const tree = routes[0].component();
    cleanups.forEach((fn) => fn());
    find(tree, byName("BetsTable")).props.onCut("t1");
    refs[0].current = "ws2"; // the viewer switched workspace meanwhile
    const before = setCalls.length;
    settleCut();
    await settle();

    assert.deepEqual(toasts, [], "no toast about the previous workspace");
    assert.deepEqual(setCalls.slice(before), [], "busy now belongs to the new workspace");
  });
}

// The page is not remounted on a workspace switch, so busy from a mutation in
// the previous workspace is cleared when the new one loads.
test("a workspace switch clears busy", async () => {
  const { definition } = await loadPlugin();
  const cleanups = [];
  const setCalls = [];
  const host = makeHost({ cleanups, setCalls, initialStates: { ...LOADED, 2: true } });
  const { routes } = initialize(definition, host);

  routes[0].component();
  cleanups.forEach((fn) => fn());
  assert.ok(setCalls.includes(false), "busy is reset");
});

test("a non-admin is told why a change was refused", async () => {
  const { definition } = await loadPlugin();
  const cleanups = [];
  const toasts = [];
  const host = makeHost({
    cleanups,
    toasts,
    initialStates: LOADED,
    reply: (key) =>
      key === "bet.cut"
        ? Promise.reject({ status: 403, body: { error: "admin role required" } })
        : new Promise(() => {}),
  });
  const { routes } = initialize(definition, host);

  const tree = routes[0].component();
  cleanups.forEach((fn) => fn());
  find(tree, byName("BetsTable")).props.onCut("t1");
  await settle();

  assert.deepEqual(toasts, [["error", "Only a kandev administrator can do this."]]);
});

test("Mark shipped is hidden once the cycle is overdue", async () => {
  const { definition } = await loadPlugin();
  const cleanups = [];
  const overdue = { ...ACTIVE_SNAPSHOT, overdue: true };
  const host = makeHost({ cleanups, initialStates: { 1: { loading: false, error: "", data: overdue } } });
  const { routes } = initialize(definition, host);

  const tree = routes[0].component();
  cleanups.forEach((fn) => fn());
  const table = find(tree, byName("BetsTable"));
  assert.equal(hasText(table.type(table.props), "Mark shipped"), false);
  assert.equal(hasText(table.type({ ...table.props, overdue: false }), "Mark shipped"), true);
});

for (const ok of [true, false]) {
  test(`the task picker is ${ok ? "cleared after a placed" : "kept after a failed"} bet`, async () => {
    const { definition } = await loadPlugin();
    const cleanups = [];
    const setCalls = [];
    const host = makeHost({ cleanups, setCalls, initialStates: LOADED });
    const { routes } = initialize(definition, host);

    const tree = routes[0].component();
    cleanups.forEach((fn) => fn());
    const form = find(tree, byName("PlaceBetForm"));
    const rendered = form.type({ ...form.props, onPlace: () => Promise.resolve(ok) });
    cleanups.forEach((fn) => fn());
    const before = setCalls.length;
    find(rendered, (n) => n.props && n.props.id === "shapeup-place-bet").props.onClick();
    await settle();

    assert.equal(setCalls.slice(before).includes(""), ok);
  });
}

test("the page asks for a workspace instead of calling the backend without one", async () => {
  const { definition } = await loadPlugin();
  const calls = [];
  const cleanups = [];
  const { routes } = initialize(definition, makeHost({ workspaceId: "", calls, cleanups }));

  routes[0].component();
  cleanups.forEach((fn) => fn());
  assert.deepEqual(calls, []);
});

test("errorMessage shows only the backend's safe message", async () => {
  const { mod } = await loadPlugin();
  assert.equal(mod.errorMessage({ body: { error: "A cycle is already active in this workspace." } }), "A cycle is already active in this workspace.");
  assert.equal(mod.errorMessage(new Error("Request failed: 500 Internal Server Error")), "Something went wrong. Try again.");
  assert.equal(mod.errorMessage(undefined), "Something went wrong. Try again.");
  assert.equal(mod.errorMessage({ status: 403, body: { error: "admin role required" } }), "Only a kandev administrator can do this.");
});

test("timeLeftLabel uses the host's locale-aware relative time", async () => {
  const { mod } = await loadPlugin();
  const utils = { formatRelativeTime: (iso) => `relative(${iso})` };
  const end = "2026-10-15T09:00:00Z";
  assert.equal(mod.timeLeftLabel(utils, end, Date.parse("2026-10-14T09:00:00Z")), `Ends relative(${end})`);
  assert.equal(mod.timeLeftLabel(utils, end, Date.parse(end)), "Deadline passed");
});

test("UI copy has no em dashes", async () => {
  const source = await readFile(new URL("../ui/bundle.js", import.meta.url), "utf8");
  assert.equal(source.includes("—"), false);
});
