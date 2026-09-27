// app.js — the entry point every page loads (as a native ES module, so it
// runs after the document is parsed). It mounts each element marked
// data-component="<name>"; the code for a component is imported on demand
// from ./components/<name>.js, so a page only downloads what it uses.
import { mount } from "./core/component.js";

mount(document);
