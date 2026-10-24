// tailsuite status page, served by GitHub Pages from docs/ on main. Renders the docs beside it,
// and reads milestones and design questions live from the GitHub API. Holds no plan text of its own.
(function () {
  "use strict";

  var REPO = "bulkhead-labs/tailsuite";
  var API = "https://api.github.com/repos/" + REPO;
  var BLOB = "https://github.com/" + REPO + "/blob/main/";

  function $(id) { return document.getElementById(id); }

  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    Object.keys(attrs || {}).forEach(function (k) {
      if (k === "text") node.textContent = attrs[k];
      else if (k === "class") node.className = attrs[k];
      else node.setAttribute(k, attrs[k]);
    });
    (children || []).forEach(function (c) { if (c) node.appendChild(c); });
    return node;
  }

  function getText(url) {
    return fetch(url, { cache: "no-cache" }).then(function (r) {
      if (!r.ok) throw new Error(url + ": " + r.status);
      return r.text();
    });
  }

  function getJSON(url) {
    return fetch(url, { headers: { Accept: "application/vnd.github+json" } }).then(function (r) {
      if (!r.ok) throw new Error("GitHub API " + r.status);
      return r.json();
    });
  }

  function slug(text) {
    return text.toLowerCase().trim().replace(/[^\w\s-]/g, "").replace(/\s+/g, "-");
  }

  function idNum(id) { return parseInt(String(id).replace(/\D/g, ""), 10) || 0; }

  function flag(why) { return el("span", { class: "flag", title: why, text: "check" }); }

  // The plan contradicts itself when "Current milestone" isn't the lowest one it doesn't call done.
  function checkCurrent(plan) {
    var expected = Object.keys(plan.status).filter(function (id) { return !isDone(plan.status[id]); })
      .sort(function (a, b) { return idNum(a) - idNum(b); })[0] || "";
    if (expected === plan.currentId) return;
    $("now-milestone").appendChild(document.createTextNode(" "));
    $("now-milestone").appendChild(flag("The plan names " + (plan.currentId || "nothing") + ", but the lowest milestone not done is " + (expected || "none")));
  }

  // A list showing one message and a link to the same data on GitHub.
  function fallback(listId, message, path) {
    var target = $(listId);
    target.innerHTML = "";
    target.appendChild(el("li", { class: "muted" }, [
      document.createTextNode(message + " See "),
      el("a", { href: "https://github.com/" + REPO + "/" + path, text: "GitHub" }),
      document.createTextNode(".")
    ]));
  }

  // Render markdown from a repo path into a container. Relative links point at
  // the file on GitHub; in-page anchors point at the rendered headings. If the
  // renderer didn't load (a blocked CDN), show the raw markdown instead.
  function rawDoc(md, container, why) {
    container.innerHTML = "";
    container.appendChild(el("p", { class: "muted", text: why + ", so this is the raw file." }));
    container.appendChild(el("pre", { text: md }));
  }

  function renderDoc(md, path, container, prefix) {
    if (!window.marked || !window.DOMPurify) return rawDoc(md, container, "The markdown renderer didn't load");
    try {
      container.innerHTML = window.DOMPurify.sanitize(window.marked.parse(md, { gfm: true }));
    } catch (e) {
      return rawDoc(md, container, "The markdown renderer failed");
    }
    container.querySelectorAll("h2, h3").forEach(function (h) { h.id = prefix + slug(h.textContent); });
    container.querySelectorAll("table").forEach(function (t) {
      var wrap = el("div", { class: "table-scroll" });
      t.parentNode.insertBefore(wrap, t);
      wrap.appendChild(t);
    });
    var base = BLOB + path;
    container.querySelectorAll("a[href]").forEach(function (a) {
      var href = a.getAttribute("href");
      if (href.charAt(0) === "#") {
        if (container.querySelector("#" + CSS.escape(prefix + href.slice(1)))) a.setAttribute("href", "#" + prefix + href.slice(1));
        else a.setAttribute("href", base + href);
      } else if (!/^[a-z]+:/i.test(href)) {
        a.setAttribute("href", new URL(href, base).toString());
      }
      if (/^https?:/i.test(a.getAttribute("href"))) a.setAttribute("rel", "noopener");
    });
  }

  // What docs/plan.md says: the current milestone, the last-checked date, each milestone's status.
  function readPlan(md) {
    var current = ((md.match(/Current milestone:\s*\*\*([\s\S]+?)\*\*/) || [])[1] || "").replace(/\s+/g, " ").replace(/\.$/, "");
    var checked = (md.match(/Last checked against the repo:\s*(\d{4}-\d{2}-\d{2})/) || [])[1] || "";
    var status = {};
    md.split("\n").forEach(function (line) {
      var m = line.match(/^\|\s*\*\*(M\d+)\*\*\s*\|[^|]*\|\s*([^|]+?)\s*\|/);
      if (m) status[m[1]] = m[2];
    });
    return { loaded: !!md, current: current, currentId: (current.match(/M\d+/) || [""])[0], checked: checked, status: status };
  }

  function isDone(s) { return /^done\b/i.test((s || "").trim()); }

  // Where the plan and GitHub disagree about one milestone. Empty when they agree.
  function disagrees(planStatus, ms) {
    var s = (planStatus || "").trim().toLowerCase();
    if (!s) return "This milestone is on GitHub but not in the plan";
    if (/^not started\b/.test(s) && ms.closed_issues > 0) return "Issues are closed, but the plan says not started";
    if (isDone(s) && ms.open_issues > 0) return "The plan says done, but issues are still open";
    if (isDone(s) && ms.state !== "closed") return "The plan says done, but the GitHub milestone is still open";
    if (ms.state === "closed" && !isDone(s)) return "The GitHub milestone is closed, but the plan doesn't say done";
    return "";
  }

  function pillFor(status) {
    var c = "pill" + (/progress/i.test(status) ? " pill--progress" : isDone(status) ? " pill--done" : "");
    return el("span", { class: c, text: status || "Not in plan" });
  }

  function renderMilestones(list, plan) {
    var ol = $("milestone-list");
    ol.innerHTML = "";
    var closed = 0, total = 0, seen = {};
    list.sort(function (a, b) { return a.number - b.number; }).forEach(function (ms) {
      var id = (ms.title.match(/^M\d+/) || [""])[0];
      seen[id] = true;
      var count = ms.open_issues + ms.closed_issues;
      closed += ms.closed_issues; total += count;
      var pct = count ? Math.round((ms.closed_issues / count) * 100) : 0;
      var planStatus = plan.status[id];
      var why = plan.loaded ? disagrees(planStatus, ms) : "";
      var fill = el("div", { class: "bar__fill" });
      ol.appendChild(el("li", { class: "ms" + (id === plan.currentId ? " ms--current" : "") }, [
        el("div", { class: "ms__id", text: id }),
        el("div", {}, [
          el("h3", { class: "ms__title" }, [el("a", { href: ms.html_url, text: ms.title.replace(/^M\d+\s*/, "") })]),
          el("p", { class: "ms__desc", text: ms.description || "" })
        ]),
        el("div", { class: "ms__side" }, [
          el("div", { class: "ms__status" }, [pillFor(plan.loaded ? planStatus : "Plan not loaded"), why ? flag(why) : null]),
          el("div", { class: "bar", role: "img", "aria-label": pct + "% of issues closed" }, [fill]),
          el("span", { class: "ms__count", text: ms.closed_issues + " of " + count + " issues closed" })
        ])
      ]));
      requestAnimationFrame(function () { fill.style.width = pct + "%"; });
    });
    // Milestones the plan lists that GitHub doesn't have.
    Object.keys(plan.status).filter(function (id) { return !seen[id]; }).sort(function (a, b) { return idNum(a) - idNum(b); }).forEach(function (id) {
      ol.appendChild(el("li", { class: "ms" }, [
        el("div", { class: "ms__id", text: id }),
        el("div", {}, [el("p", { class: "ms__desc", text: "In docs/plan.md, but there is no GitHub milestone for it." })]),
        el("div", { class: "ms__side" }, [el("div", { class: "ms__status" }, [pillFor(plan.status[id]), flag("In the plan but not on GitHub")])])
      ]));
    });
    $("now-issues").textContent = closed + " / " + total;
  }

  function renderQuestions(issues) {
    var ul = $("question-list");
    ul.innerHTML = "";
    var qs = issues.filter(function (i) { return !i.pull_request; });
    $("now-questions").textContent = String(qs.length);
    if (!qs.length) { ul.appendChild(el("li", { class: "muted", text: "None open." })); return; }
    qs.sort(function (a, b) {
      var am = a.milestone ? a.milestone.number : 99, bm = b.milestone ? b.milestone.number : 99;
      return am - bm || a.number - b.number;
    }).forEach(function (i) {
      ul.appendChild(el("li", { class: "q" }, [
        el("a", { href: i.html_url, text: i.title.replace(/^Design:\s*/i, "") }),
        el("span", { class: "q__meta", text: "#" + i.number + " · " + (i.milestone ? i.milestone.title.split(" ")[0] : "later") + " · " + i.comments + " comments" })
      ]));
    });
  }

  function start() {
    getJSON(API + "/commits/main").then(function (c) {
      var a = $("build-sha");
      a.textContent = c.sha.slice(0, 7);
      a.href = c.html_url;
      $("build-time").textContent = ", " + c.commit.committer.date.slice(0, 10);
    }).catch(function () {});

    var planP = getText("plan.md").then(function (md) {
      var plan = readPlan(md);
      $("now-milestone").textContent = plan.current || "—";
      $("now-checked").textContent = plan.checked || "—";
      checkCurrent(plan);
      renderDoc(md, "docs/plan.md", $("plan-doc"), "plan-");
      return plan;
    }).catch(function (err) {
      $("plan-doc").textContent = "Couldn't load docs/plan.md (" + err.message + ").";
      $("now-milestone").textContent = "—";
      $("now-checked").textContent = "—";
      return readPlan("");
    });

    getText("decisions/README.md").then(function (md) {
      renderDoc(md, "docs/decisions/README.md", $("decisions-doc"), "dec-");
    }).catch(function (err) {
      $("decisions-doc").textContent = "Couldn't load the decisions (" + err.message + ").";
    });

    Promise.all([planP, getJSON(API + "/milestones?state=all&per_page=100")])
      .then(function (r) { renderMilestones(r[1], r[0]); })
      .catch(function (err) {
        fallback("milestone-list", "Live milestones are unavailable (" + err.message + ").", "milestones");
        $("now-issues").textContent = "—";
      });

    getJSON(API + "/issues?state=open&labels=type:design&per_page=100")
      .then(renderQuestions)
      .catch(function (err) {
        fallback("question-list", "Live design questions are unavailable (" + err.message + ").", "issues?q=is%3Aopen+label%3Atype%3Adesign");
        $("now-questions").textContent = "—";
      });
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start);
  else start();
})();
