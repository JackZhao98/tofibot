// A small enhancement only: the server-rendered status and links need no JS.
(() => {
  const system = window.matchMedia("(prefers-color-scheme: dark)");
  const applyTheme = () => {
    let preference = "system";
    try { preference = localStorage.getItem("tofi:appearance") || "system"; } catch { /* Storage may be unavailable. */ }
    document.documentElement.dataset.theme = preference === "dark" || preference === "light"
      ? preference : system.matches ? "dark" : "light";
  };
  applyTheme();
  system.addEventListener("change", applyTheme);
  window.addEventListener("storage", event => {
    if (event.key === "tofi:appearance" || event.key === null) applyTheme();
  });
  // Keep the callback pathname for existing title polling. No query data is
  // read, displayed, sent to an opener, or carried into a return link.
  if (/^\/api\/extensions\/mcp\/[^/]+\/oauth\/callback$/.test(location.pathname)) {
    try { history.replaceState(null, "", location.pathname); } catch { /* Status remains usable. */ }
  }
})();
