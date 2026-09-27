// copy-button — copies the text of another element to the clipboard.
//
// Props: target (id of the element to copy), message (toast text). Where
// the Clipboard API is unavailable or refused, the text is selected instead
// so the user can copy it by hand.
import { on } from "../core/dom.js";
import { notify } from "../core/toast.js";

const isApple = /Mac|iPhone|iPad/.test(navigator.userAgent);

export default function copyButton(root, { props }) {
  return on(root, "click", async () => {
    const target = props.target ? document.getElementById(props.target) : null;
    if (!target) {
      return;
    }
    try {
      await navigator.clipboard.writeText(target.textContent.trim());
      notify(props.message || "Copied to clipboard");
    } catch {
      const range = document.createRange();
      range.selectNodeContents(target);
      const selection = window.getSelection();
      selection.removeAllRanges();
      selection.addRange(range);
      notify(`Text selected. Press ${isApple ? "⌘C" : "Ctrl+C"} to copy.`);
    }
  });
}
