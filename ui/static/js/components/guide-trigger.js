// guide-trigger — a button that opens the getting-started guide dialog.
import { emit, on } from "../core/dom.js";

export default function guideTrigger(root) {
  root.setAttribute("aria-haspopup", "dialog");
  return on(root, "click", () => emit("guide:open", { opener: root }));
}
