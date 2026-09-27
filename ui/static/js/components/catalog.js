// catalog — client-side search, provider filter, and sort over a grid of
// cards (templates/components/catalog.html). The view is mirrored into the
// URL (?q=, ?provider=, ?sort=) so it can be bookmarked and shared.
//
// Parts: search, sort, provider (optional), items, count, no-results,
// reset. Props: singular, plural. Items carry data-name (sort key),
// data-search (search text), and optionally data-provider. The server
// renders items in ascending order.
import { combine, isEditable, on } from "../core/dom.js";
import { useUrlState } from "../core/hooks.js";

const compare = (a, b) => (a < b ? -1 : a > b ? 1 : 0);

export default function catalog(root, { part, props }) {
  const search = part("search");
  const sort = part("sort");
  const provider = part("provider");
  const list = part("items");
  const count = part("count");
  const noResults = part("no-results");
  const reset = part("reset");
  if (!search || !sort || !list) {
    return undefined;
  }

  const items = [...list.children].filter((element) => element.hasAttribute("data-item"));
  const noun = (n) => (n === 1 ? props.singular : props.plural);
  const url = useUrlState(["q", "provider", "sort"]);
  let appliedSort = "asc";

  // Re-append the cards only when the order actually changes.
  function applySort() {
    const direction = sort.value === "desc" ? "desc" : "asc";
    if (direction === appliedSort) {
      return;
    }
    appliedSort = direction;
    const sign = direction === "desc" ? -1 : 1;
    list.append(...items.slice().sort((a, b) => sign * compare(a.dataset.name, b.dataset.name)));
  }

  function applyFilter() {
    const query = search.value.trim().toLocaleLowerCase();
    const chosen = provider?.value ?? "";
    let visible = 0;
    for (const item of items) {
      const match =
        (query === "" || (item.dataset.search ?? "").toLocaleLowerCase().includes(query)) &&
        (chosen === "" || item.dataset.provider === chosen);
      item.hidden = !match;
      if (match) {
        visible++;
      }
    }
    if (noResults) {
      noResults.hidden = visible !== 0;
    }
    if (count) {
      const filtered = query !== "" || chosen !== "";
      count.textContent = filtered
        ? `${visible} of ${items.length} ${noun(items.length)}`
        : `${items.length} ${noun(items.length)}`;
    }
  }

  function update() {
    applySort();
    applyFilter();
    url.write({ q: search.value.trim(), provider: provider?.value ?? "", sort: sort.value === "desc" ? "desc" : "" });
  }

  // Restore a bookmarked view. Values that don't match an option fall back
  // to the defaults.
  const state = url.read();
  search.value = state.q;
  sort.value = state.sort === "desc" ? "desc" : "asc";
  if (provider) {
    provider.value = [...provider.options].some((option) => option.value === state.provider) ? state.provider : "";
  }
  applySort();
  applyFilter();
  // Only announce counts the user causes, not the initial render.
  count?.setAttribute("aria-live", "polite");

  const cleanups = [
    url.cancel,
    on(search, "input", update),
    on(sort, "change", update),
    provider && on(provider, "change", update),
    reset &&
      on(reset, "click", () => {
        search.value = "";
        sort.value = "asc";
        if (provider) {
          provider.value = "";
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
