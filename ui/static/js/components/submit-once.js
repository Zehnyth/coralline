// submit-once — stops a form being submitted twice (double clicks,
// impatient re-clicks) and shows its submit buttons as busy.
//
// Another component's submit handler (confirm-form) may still veto the
// submission, and handler order isn't guaranteed, so the buttons are only
// disabled once the event has finished dispatching without being
// prevented. The server-side submission key remains the real guarantee.
import { combine, on } from "../core/dom.js";

export default function submitOnce(root) {
  let submitting = false;
  const buttons = () => [...root.querySelectorAll('button[type="submit"], button:not([type]), input[type="submit"]')];

  const reset = () => {
    submitting = false;
    for (const button of buttons()) {
      button.disabled = false;
      button.removeAttribute("aria-busy");
    }
  };

  return combine(
    on(root, "submit", (event) => {
      if (submitting) {
        event.preventDefault();
        return;
      }
      submitting = true;
      setTimeout(() => {
        if (event.defaultPrevented) {
          submitting = false;
          return;
        }
        for (const button of buttons()) {
          button.disabled = true;
          button.setAttribute("aria-busy", "true");
        }
      });
    }),
    // Coming back through the back/forward cache restores a disabled form.
    on(window, "pageshow", (event) => {
      if (event.persisted) {
        reset();
      }
    }),
  );
}
