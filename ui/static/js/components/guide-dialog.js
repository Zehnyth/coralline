// guide-dialog — the getting-started dialog (templates/shell/guide-dialog.html),
// opened by any guide-trigger. Closes on its close button, on Escape (native
// <dialog>), and on a click that both starts and ends on the backdrop — so
// selecting text inside and releasing outside doesn't close it. Focus
// returns to whatever opened it.
//
// Parts: close. Events: listens for guide:open.
import { combine, listen, on } from "../core/dom.js";

export default function guideDialog(root, { part }) {
  const close = part("close");
  let opener = null;
  let pressedOnBackdrop = false;

  const onBackdrop = (event) => {
    if (event.target !== root) {
      return false;
    }
    const box = root.getBoundingClientRect();
    return event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom;
  };

  return combine(
    listen("guide:open", ({ opener: trigger }) => {
      if (root.open) {
        return;
      }
      opener = trigger ?? document.activeElement;
      root.showModal();
    }),
    close && on(close, "click", () => root.close()),
    on(root, "pointerdown", (event) => {
      pressedOnBackdrop = onBackdrop(event);
    }),
    on(root, "click", (event) => {
      if (pressedOnBackdrop && onBackdrop(event)) {
        root.close();
      }
      pressedOnBackdrop = false;
    }),
    on(root, "close", () => {
      opener?.focus?.();
      opener = null;
    }),
  );
}
