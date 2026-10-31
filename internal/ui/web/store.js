// One state object with subscribe/notify.
export function createStore(initial) {
  let state = initial;
  const subs = new Set();
  return {
    get: () => state,
    set(patch) {
      state = { ...state, ...patch };
      for (const f of subs) f(state);
    },
    subscribe(f) {
      subs.add(f);
      return () => subs.delete(f);
    },
  };
}
