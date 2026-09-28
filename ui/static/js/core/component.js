// component.js — the component runtime.
//
// Markup opts in with data-component="name" (an element may list several
// names, space-separated). mount(root) imports ../components/<name>.js the
// first time a name is seen and calls its default export once per element:
//
//   export default function name(root, ctx) { …; return cleanup; }
//
// ctx.part(name) / ctx.parts(name) find data-part="name" descendants that
// belong to this component (not to a nested one), and ctx.props is the
// element's dataset. setup may return a cleanup function; unmount(root) runs
// it. mount is idempotent, so it can run again after new markup is inserted.
const NAME = /^[a-z][a-z0-9-]*$/;
const mounted = new WeakMap(); // Element -> Map<name, cleanup | null>
const modules = new Map(); // name -> Promise<setup>

function load(name) {
  if (!modules.has(name)) {
    const url = new URL(`../components/${name}.js`, import.meta.url);
    modules.set(name, import(url.href).then((module) => module.default));
  }
  return modules.get(name);
}

// ownerOf returns the component element a part belongs to.
function ownerOf(element) {
  const start = element.hasAttribute("data-component") ? element.parentElement : element;
  return start ? start.closest("[data-component]") : null;
}

export function context(root) {
  const mine = (element) => ownerOf(element) === root;
  const find = (name) => [...root.querySelectorAll(`[data-part~="${name}"]`)].filter(mine);
  return {
    root,
    props: root.dataset,
    part: (name) => find(name)[0] ?? null,
    parts: find,
  };
}

function componentsIn(root) {
  const elements = [...root.querySelectorAll("[data-component]")];
  if (root instanceof Element && root.hasAttribute("data-component")) {
    elements.unshift(root);
  }
  return elements;
}

export async function mount(root = document) {
  const pending = [];
  for (const element of componentsIn(root)) {
    for (const name of element.dataset.component.split(/\s+/).filter(Boolean)) {
      if (!NAME.test(name)) {
        console.error(`coralline: invalid component name "${name}"`);
        continue;
      }
      let instances = mounted.get(element);
      if (!instances) {
        instances = new Map();
        mounted.set(element, instances);
      }
      if (instances.has(name)) {
        continue;
      }
      // Reserve synchronously so an overlapping mount() can't double-mount.
      instances.set(name, null);
      pending.push(
        load(name)
          .then((setup) => {
            const cleanup = setup(element, context(element));
            if (typeof cleanup === "function") {
              instances.set(name, cleanup);
            }
          })
          .catch((error) => console.error(`coralline: component "${name}" failed to mount`, error)),
      );
    }
  }
  await Promise.all(pending);
}

export function unmount(root = document) {
  for (const element of componentsIn(root)) {
    const instances = mounted.get(element);
    if (!instances) {
      continue;
    }
    for (const cleanup of instances.values()) {
      try {
        cleanup?.();
      } catch (error) {
        console.error("coralline: component cleanup failed", error);
      }
    }
    mounted.delete(element);
  }
}
