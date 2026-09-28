// dom.js — small DOM helpers shared by components.

// on adds an event listener and returns the function that removes it.
export function on(target, type, handler, options) {
  target.addEventListener(type, handler, options);
  return () => target.removeEventListener(type, handler, options);
}

// combine merges several cleanup functions into one (falsy entries are
// skipped), so setup can `return combine(on(...), on(...))`.
export function combine(...cleanups) {
  return () => {
    for (const cleanup of cleanups) {
      cleanup?.();
    }
  };
}

// emit and listen are the only way components talk to each other: a
// component never reaches into another component's markup.
const PREFIX = "coralline:";

export function emit(name, detail = {}) {
  document.dispatchEvent(new CustomEvent(PREFIX + name, { detail }));
}

export function listen(name, handler) {
  return on(document, PREFIX + name, (event) => handler(event.detail ?? {}));
}

// isEditable reports whether keystrokes on element are text input.
export function isEditable(element) {
  if (!element) {
    return false;
  }
  return element.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(element.tagName);
}
