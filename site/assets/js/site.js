// Theme toggle, code-copy buttons, sidebar scrollspy, tabs.
// No build step, no dependencies.

// ---- shared docs sidebar ------------------------------------------
var SIDEBAR = [
  { group: "Get Started", items: [
    { href: "index.html",      text: "Introduction" },
    { href: "install.html",    text: "Installation" },
    { href: "quickstart.html", text: "Quickstart" },
    { href: "concepts.html",   text: "Concepts" },
  ]},
  { group: "Commands", items: [
    { href: "audit.html",     text: "audit" },
    { href: "orphans.html",   text: "orphans" },
    { href: "pii.html",       text: "pii" },
    { href: "bench.html",     text: "bench" },
    { href: "migration.html", text: "migration" },
    { href: "cost.html",      text: "cost" },
    { href: "export.html",    text: "export" },
  ]},
  { group: "Configuration", items: [
    { href: "catalogs.html", text: "Catalogs" },
    { href: "policy.html",   text: "Policy (sentry.yaml)" },
  ]},
  { group: "Operate", items: [
    { href: "ci-integration.html", text: "CI Integration" },
    { href: "deployment.html",     text: "Deployment" },
    { href: "architecture.html",   text: "Architecture" },
  ]},
];

function renderSidebar() {
  var host = document.querySelector("[data-docs-sidebar]");
  if (!host) return;
  var current = (location.pathname.split("/").pop() || "index.html");
  var html = "";
  SIDEBAR.forEach(function (g) {
    html += "<h4>" + g.group + "</h4><ul>";
    g.items.forEach(function (it) {
      var active = it.href === current ? " class=\"active\"" : "";
      html += "<li><a href=\"" + it.href + "\"" + active + ">" + it.text + "</a></li>";
    });
    html += "</ul>";
  });
  host.innerHTML = html;
}

(function () {
  renderSidebar();
  // ---- theme ---------------------------------------------------------
  var root = document.documentElement;
  var saved = localStorage.getItem("sentry-theme");
  if (saved === "light") root.setAttribute("data-theme", "light");

  document.addEventListener("click", function (e) {
    var t = e.target.closest("[data-theme-toggle]");
    if (!t) return;
    var current = root.getAttribute("data-theme") === "light" ? "dark" : "light";
    if (current === "dark") root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", current);
    localStorage.setItem("sentry-theme", current);
    t.textContent = current === "light" ? "dark" : "light";
  });
  var toggle = document.querySelector("[data-theme-toggle]");
  if (toggle) toggle.textContent = root.getAttribute("data-theme") === "light" ? "dark" : "light";

  // ---- code copy buttons --------------------------------------------
  document.querySelectorAll(".install-copy [data-copy], pre[data-copy]").forEach(function (el) {
    var btn = el.tagName === "BUTTON" ? el : el.querySelector("button");
    if (!btn) return;
    btn.addEventListener("click", function () {
      var text = el.getAttribute("data-copy") || el.textContent.replace(/^\s*\$\s?/m, "");
      navigator.clipboard.writeText(text.trim()).then(function () {
        var prev = btn.textContent;
        btn.textContent = "copied";
        setTimeout(function () { btn.textContent = prev; }, 1200);
      });
    });
  });

  // ---- sidebar scrollspy --------------------------------------------
  var headings = document.querySelectorAll(".docs-main h2[id], .docs-main h3[id]");
  var links = document.querySelectorAll(".sidebar a[href^='#']");
  if (headings.length && links.length) {
    var byHash = {};
    links.forEach(function (a) { byHash[a.getAttribute("href")] = a; });
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          links.forEach(function (a) { a.classList.remove("active"); });
          var a = byHash["#" + entry.target.id];
          if (a) a.classList.add("active");
        }
      });
    }, { rootMargin: "-30% 0px -65% 0px" });
    headings.forEach(function (h) { io.observe(h); });
  }

  // ---- tabs ---------------------------------------------------------
  document.querySelectorAll(".tabs").forEach(function (group) {
    var buttons = group.querySelectorAll("button");
    var panels = document.querySelectorAll("[data-tab-group='" + group.dataset.tabGroup + "'] .tab-panel");
    buttons.forEach(function (b) {
      b.addEventListener("click", function () {
        buttons.forEach(function (x) { x.classList.remove("active"); });
        panels.forEach(function (p) { p.classList.remove("active"); });
        b.classList.add("active");
        var target = group.parentElement.querySelector(".tab-panel[data-tab='" + b.dataset.tab + "']");
        if (target) target.classList.add("active");
      });
    });
  });
})();
