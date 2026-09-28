// tag-editor — shows a key=value-per-line textarea as removable chips plus
// an input (templates/components/tag-editor.html). The textarea stays the
// form field and the source of truth; without JavaScript it is simply
// visible. The server validates tags; this only catches obvious typos.
//
// Parts: source (textarea), chips (container), entry (input), error.
import { combine, on } from "../core/dom.js";

const TAG = /^[^\s=]+=\S.*$/;

export default function tagEditor(root, { part }) {
  const source = part("source");
  const chips = part("chips");
  const entry = part("entry");
  const error = part("error");
  if (!source || !chips || !entry) {
    return undefined;
  }

  const read = () =>
    source.value
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean);
  const write = (tags) => {
    source.value = tags.length ? tags.join("\n") + "\n" : "";
  };
  const showError = (message) => {
    if (error) {
      error.textContent = message;
      error.hidden = !message;
    }
  };

  function render() {
    for (const chip of chips.querySelectorAll(".chip")) {
      chip.remove();
    }
    for (const tag of read()) {
      const chip = document.createElement("span");
      chip.className = "chip";
      chip.textContent = tag;
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "chip__remove";
      remove.setAttribute("aria-label", `Remove ${tag}`);
      remove.textContent = "×";
      remove.dataset.tag = tag;
      chip.append(remove);
      chips.insertBefore(chip, entry);
    }
  }

  // add returns false (and explains why) when the entry isn't a tag.
  function add() {
    const value = entry.value.trim();
    if (!value) {
      return true;
    }
    if (!TAG.test(value)) {
      showError("Use key=value, like owner=alice.");
      return false;
    }
    const key = value.slice(0, value.indexOf("="));
    // A key appears once; entering it again replaces its value.
    write([...read().filter((tag) => tag.slice(0, tag.indexOf("=")) !== key), value]);
    entry.value = "";
    showError("");
    render();
    return true;
  }

  source.hidden = true;
  chips.hidden = false;
  render();

  return combine(
    on(entry, "keydown", (event) => {
      if (event.key === "Enter" || event.key === ",") {
        event.preventDefault();
        add();
      } else if (event.key === "Backspace" && entry.value === "") {
        const tags = read();
        if (tags.length) {
          write(tags.slice(0, -1));
          render();
        }
      }
    }),
    on(entry, "input", () => showError("")),
    on(chips, "click", (event) => {
      const remove = event.target.closest(".chip__remove");
      if (remove) {
        write(read().filter((tag) => tag !== remove.dataset.tag));
        render();
        entry.focus();
      } else if (event.target === chips) {
        entry.focus();
      }
    }),
    // A tag typed but not yet entered is saved too, not silently dropped.
    root.closest("form") &&
      on(root.closest("form"), "submit", (event) => {
        if (!add()) {
          event.preventDefault();
          event.stopImmediatePropagation();
          entry.focus();
        }
      }),
  );
}
