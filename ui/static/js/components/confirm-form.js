// confirm-form — asks before a destructive form submits.
// Props: confirm (the question).
import { on } from "../core/dom.js";

export default function confirmForm(root, { props }) {
  return on(root, "submit", (event) => {
    if (!window.confirm(props.confirm || "Are you sure?")) {
      event.preventDefault();
      event.stopImmediatePropagation();
    }
  });
}
