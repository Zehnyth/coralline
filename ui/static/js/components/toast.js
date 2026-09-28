// toast — the shell's status message area (templates/shell/toast.html).
// Anything can show a message with notify() from core/toast.js.
//
// The element is a live region that stays rendered; only its visibility
// changes. Text is cleared and set again on the next frame so that even an
// identical message is announced a second time.
import { listen } from "../core/dom.js";

const VISIBLE_MS = 3500;

export default function toast(root) {
  let hideTimer;
  let clearTimer;
  let frame;

  const off = listen("toast", ({ message }) => {
    if (!message) {
      return;
    }
    clearTimeout(hideTimer);
    clearTimeout(clearTimer);
    cancelAnimationFrame(frame);
    root.textContent = "";
    frame = requestAnimationFrame(() => {
      root.textContent = message;
      root.classList.add("is-visible");
    });
    hideTimer = setTimeout(() => {
      root.classList.remove("is-visible");
      clearTimer = setTimeout(() => {
        root.textContent = "";
      }, 300);
    }, VISIBLE_MS);
  });

  return () => {
    off();
    clearTimeout(hideTimer);
    clearTimeout(clearTimer);
    cancelAnimationFrame(frame);
  };
}
