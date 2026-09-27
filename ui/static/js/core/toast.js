// toast.js — show a short status message in the shell's toast component.
import { emit } from "./dom.js";

export function notify(message) {
  emit("toast", { message });
}
