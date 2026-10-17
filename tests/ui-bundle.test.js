import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

let importCount = 0;

function element(type, props, ...children) {
  return { type, props: props || {}, children };
}

// A minimal stand-in for host.React: state never changes (setter calls are
// recorded), effects run at once and their cleanups are collected so timers
// do not outlive the test.
function fakeReact(cleanups, setCalls) {
  return {
    useState: (init) => [typeof init === "function" ? init() : init, (v) => setCalls.push(v)],
    useEffect: (fn) => {
      const cleanup = fn();
      if (typeof cleanup === "function") cleanups.push(cleanup);
    },
    useCallback: (fn) => fn,
    useRef: (value) => ({ current: value }),
  };
}

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

function makeHost({ workspaceId = "ws1", calls = [], cleanups = [], setCalls = [], reply } = {}) {
  const ui = new Proxy({}, { get: (_, name) => String(name) });
  return {
    jsx: element,
    React: fakeReact(cleanups, setCalls),
    ui,
    utils: { formatRelativeTime: (iso) => `relative(${iso})`, cn: (...c) => c.join(" ") },
    toast: { success() {}, error() {} },
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
