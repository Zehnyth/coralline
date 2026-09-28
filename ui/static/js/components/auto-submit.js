// auto-submit — submits its form as soon as a field changes, and hides the
// form's explicit submit button (data-part="submit"), which stays as the
// no-JavaScript fallback.
//
// Parts: submit (optional).
import { on } from "../core/dom.js";

export default function autoSubmit(root, { part }) {
  const submit = part("submit");
  if (submit) {
    submit.hidden = true;
  }
  return on(root, "change", () => root.requestSubmit());
}
