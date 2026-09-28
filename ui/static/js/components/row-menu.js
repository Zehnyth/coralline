// row-menu — a small <details> actions menu on a list row. Without
// JavaScript it still opens and closes; this adds the expected closing
// behaviour: a click outside, Escape (returning focus to the ⋯ button),
// choosing an item, or opening another row's menu.
import { combine, on } from "../core/dom.js";

export default function rowMenu(root) {
  const summary = root.querySelector("summary");
  const close = (restoreFocus) => {
    if (!root.open) {
      return;
    }
    root.open = false;
    if (restoreFocus) {
      summary?.focus();
    }
  };
  return combine(
    on(document, "click", (event) => {
      if (!root.contains(event.target)) {
        close(false);
      }
    }),
    on(root, "keydown", (event) => {
      if (event.key === "Escape") {
        event.stopPropagation();
        close(true);
      }
    }),
    on(root, "click", (event) => {
      if (event.target.closest("a, button") && !event.target.closest("summary")) {
        // Let the item act first (copy-button reads the page), then close.
        setTimeout(() => close(false));
      }
    }),
    on(root, "toggle", () => {
      if (root.open) {
        for (const other of document.querySelectorAll("details.row-menu[open]")) {
          if (other !== root) {
            other.open = false;
          }
        }
      }
    }),
  );
}
