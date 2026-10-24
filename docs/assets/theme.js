// Theme switch: auto (follow the system), light or dark. Load it in <head>,
// before the stylesheet paints, so the page never flashes the wrong theme.
// Any element with data-theme-set="auto|light|dark" becomes a switch button;
// its aria-pressed tracks the current choice.
(function () {
  var KEY = "bh-theme";
  var current = "auto";
  try {
    current = localStorage.getItem(KEY) || "auto";
  } catch (e) {}

  function apply(theme) {
    current = theme === "light" || theme === "dark" ? theme : "auto";
    var root = document.documentElement;
    if (current === "auto") root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", current);
    var buttons = document.querySelectorAll("[data-theme-set]");
    for (var i = 0; i < buttons.length; i++) {
      buttons[i].setAttribute("aria-pressed", String(buttons[i].getAttribute("data-theme-set") === current));
    }
  }

  window.bhTheme = {
    get: function () {
      return current;
    },
    set: function (theme) {
      try {
        if (theme === "light" || theme === "dark") localStorage.setItem(KEY, theme);
        else localStorage.removeItem(KEY);
      } catch (e) {}
      apply(theme);
    },
  };

  apply(current);
  document.addEventListener("DOMContentLoaded", function () {
    apply(current);
  });
  document.addEventListener("click", function (e) {
    var b = e.target.closest && e.target.closest("[data-theme-set]");
    if (b) window.bhTheme.set(b.getAttribute("data-theme-set"));
  });
})();
