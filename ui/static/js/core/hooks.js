// hooks.js — reusable pieces of behaviour, in the spirit of React hooks but
// with no framework and no re-rendering: plain functions a component calls
// from its setup. Anything that subscribes to something returns a way to
// unsubscribe, which the component hands back as (part of) its cleanup.

// useDebounce delays fn until calls stop for `wait` ms. The returned
// function has .cancel().
export function useDebounce(fn, wait) {
  let timer;
  const debounced = (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), wait);
  };
  debounced.cancel = () => clearTimeout(timer);
  return debounced;
}

// useStorage wraps one localStorage key. Storage can be missing or throw
// (private windows, blocked site data), so every access is guarded and a
// failure just means the preference isn't remembered.
export function useStorage(key) {
  return {
    get() {
      try {
        return localStorage.getItem(key);
      } catch {
        return null;
      }
    },
    set(value) {
      try {
        localStorage.setItem(key, value);
      } catch {
        // Not remembered; nothing else depends on it.
      }
    },
    remove() {
      try {
        localStorage.removeItem(key);
      } catch {
        // As above.
      }
    },
  };
}

// useMediaQuery tracks a media query. onChange(matches) runs on changes.
export function useMediaQuery(query, onChange) {
  const list = window.matchMedia(query);
  const handler = (event) => onChange?.(event.matches);
  list.addEventListener("change", handler);
  return {
    matches: () => list.matches,
    off: () => list.removeEventListener("change", handler),
  };
}

// useUrlState mirrors a few query parameters so a view can be bookmarked
// and shared. read() returns their current values ("" when absent);
// write(values) updates the URL with history.replaceState. Writes are
// debounced — browsers throttle replaceState, and Safari throws after 100
// calls in 30 seconds — and never throw: the URL is a convenience.
export function useUrlState(keys, wait = 300) {
  const read = () => {
    const params = new URLSearchParams(window.location.search);
    return Object.fromEntries(keys.map((key) => [key, params.get(key) ?? ""]));
  };
  const write = useDebounce((values) => {
    try {
      const url = new URL(window.location.href);
      for (const key of keys) {
        if (values[key]) {
          url.searchParams.set(key, values[key]);
        } else {
          url.searchParams.delete(key);
        }
      }
      if (url.href !== window.location.href) {
        window.history.replaceState(window.history.state, "", url);
      }
    } catch {
      // Throttled or unavailable; the view itself is already updated.
    }
  }, wait);
  return { read, write, cancel: write.cancel };
}
