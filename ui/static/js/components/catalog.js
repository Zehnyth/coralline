// catalog — client-side search, filters, sort, and list/grid layout over a
// list of items (templates/components/catalog.html). The view is mirrored
// into the URL (?q=, ?sort=, and one parameter per filter) so it can be
// bookmarked and shared; the layout is a per-browser preference.
//
// Parts: search, items, and optionally count, no-results, reset, sort,
// views + view (layout buttons), and any number of facet parts. A facet is
// a <select> or a group of radio inputs with data-facet="<key>"; an item
// matches when its data-<key> equals the chosen value ("" means any).
// Props: singular, plural. Items carry data-name (name sort key),
// data-search (search text), and optionally data-updated (unix seconds).
// The server renders items in name order.
import { combine, isEditable, on } from "../core/dom.js";
import { useStorage, useUrlState } from "../core/hooks.js";

const compare = (a, b) => (a < b ? -1 : a > b ? 1 : 0);

export default function catalog(root, { part, parts, props }) {
  const search = part("search");
  const list = part("items");
  if (!search || !list) {
    return undefined;
  }
  const sort = part("sort");
  const count = part("count");
  const noResults = part("no-results");
  const reset = part("reset");
  const views = part("views");
  const viewButtons = parts("view");
  const facets = parts("facet").map((element) => {
    const radios = element.tagName === "SELECT" ? [] : [...element.querySelectorAll('input[type="radio"]')];
    return {
      key: element.dataset.facet,
      element,
      radios,
      get: () => (radios.length ? (radios.find((radio) => radio.checked)?.value ?? "") : element.value),
      set(value) {
        if (radios.length) {
          const target = radios.find((radio) => radio.value === value) ?? radios.find((radio) => radio.value === "");
          if (target) {
            target.checked = true;
          }
        } else {
          element.value = [...element.options].some((option) => option.value === value) ? value : "";
        }
      },
    };
  });

  const items = [...list.children].filter((element) => element.hasAttribute("data-item"));
  const noun = (n) => (n === 1 ? props.singular : props.plural);
  const url = useUrlState(["q", "sort", ...facets.map((facet) => facet.key)]);
  const layout = useStorage(`coralline-layout-${props.plural}`);
  let appliedSort = "";

  // Re-append the items only when the order actually changes.
  function applySort() {
    const order = sort?.value === "recent" ? "recent" : "";
    if (order === appliedSort) {
      return;
    }
    appliedSort = order;
    const sorted = items.slice().sort((a, b) =>
      order === "recent"
        ? Number(b.dataset.updated ?? 0) - Number(a.dataset.updated ?? 0) || compare(a.dataset.name, b.dataset.name)
        : compare(a.dataset.name, b.dataset.name),
    );
    list.append(...sorted);
  }

  function applyFilter() {
    const query = search.value.trim().toLocaleLowerCase();
    const chosen = facets.map((facet) => [facet.key, facet.get()]).filter(([, value]) => value !== "");
    let visible = 0;
    for (const item of items) {
      const match =
        (query === "" || (item.dataset.search ?? "").toLocaleLowerCase().includes(query)) &&
        chosen.every(([key, value]) => item.dataset[key] === value);
      item.hidden = !match;
      if (match) {
        visible++;
      }
    }
    if (noResults) {
      noResults.hidden = visible !== 0;
    }
    if (count) {
      // The heading already shows the total; the line only explains a
      // narrowed list.
      const filtered = query !== "" || chosen.length > 0;
      count.textContent = filtered && visible > 0 ? `${visible} of ${items.length} ${noun(items.length)}` : "";
    }
  }

  function applyLayout(value) {
    const grid = value === "grid";
    root.dataset.layout = grid ? "grid" : "list";
    for (const button of viewButtons) {
      button.setAttribute("aria-pressed", String(button.dataset.value === root.dataset.layout));
    }
  }

  function update() {
    applySort();
    applyFilter();
    const state = { q: search.value.trim(), sort: sort?.value === "recent" ? "recent" : "" };
    for (const facet of facets) {
      state[facet.key] = facet.get();
    }
    url.write(state);
  }

  // Restore a bookmarked view. Values that don't match an option fall back
  // to the defaults.
  const state = url.read();
  search.value = state.q;
  if (sort) {
    sort.value = state.sort === "recent" ? "recent" : "";
  }
  for (const facet of facets) {
    facet.set(state[facet.key]);
  }
  applySort();
  applyFilter();
  if (views) {
    views.hidden = false;
    applyLayout(layout.get());
  }
  // Only announce counts the user causes, not the initial render.
  count?.setAttribute("aria-live", "polite");

  const cleanups = [
    url.cancel,
    on(search, "input", update),
    sort && on(sort, "change", update),
    ...facets.map((facet) => on(facet.element, "change", update)),
    ...viewButtons.map((button) =>
      on(button, "click", () => {
        layout.set(button.dataset.value);
        applyLayout(button.dataset.value);
      }),
    ),
    reset &&
      on(reset, "click", () => {
        search.value = "";
        if (sort) {
          sort.value = "";
        }
        for (const facet of facets) {
          facet.set("");
        }
        update();
        search.focus();
      }),
  ];

  // "/" focuses the search box; only the page's first catalog claims it.
  if (document.querySelector('[data-component~="catalog"]') === root) {
    cleanups.push(
      on(document, "keydown", (event) => {
        if (event.key !== "/" || event.ctrlKey || event.metaKey || event.altKey || event.defaultPrevented) {
          return;
        }
        if (isEditable(document.activeElement) || document.querySelector("dialog[open]")) {
          return;
        }
        event.preventDefault();
        search.focus();
      }),
    );
  }
  return combine(...cleanups);
}
