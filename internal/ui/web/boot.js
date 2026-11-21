// boot.js runs before the first paint (a plain script in <head>, not a module, which would
// wait for the page), so a page saved as light or stark never shows a moment of dark lean
// first (spec 027). It applies the same rules as loadTheme and loadStyle in theme.js, which
// webtest/theme.test.js checks case by case; app.js applies them again once it loads.
(function () {
  var root = document.documentElement;
  var read = function (name, allowed, fallback) {
    try {
      var v = window.localStorage.getItem(name);
      return allowed.indexOf(v) >= 0 ? v : fallback;
    } catch (e) {
      return fallback;
    }
  };
  root.setAttribute("data-theme", read("theme", ["dark", "light"], "dark"));
  root.setAttribute("data-style", read("style", ["lean", "stark"], "lean"));
})();
