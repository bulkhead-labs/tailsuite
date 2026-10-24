// tailsuite status page. Renders docs/ as deployed from main, and reads
// milestones and design questions live from the GitHub API. Holds no plan text of its own.
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
      if (!r.ok) throw new Error(url + ": " + r.status);
      return r.json();
    });
  }

  function slug(text) {
    return text.toLowerCase().trim().replace(/[^\w\s-]/g, "").replace(/\s+/g, "-");
  }

  // Render markdown from a repo path into a container. Relative links point at
  // the file on GitHub; in-page anchors point at the rendered headings.
  function renderDoc(md, path, container, prefix) {
    var html = window.DOMPurify.sanitize(window.marked.parse(md, { gfm: true }));
    container.innerHTML = html;
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
    return { current: current, currentId: (current.match(/M\d+/) || [""])[0], checked: checked, status: status };
  }

  // The plan and GitHub disagree when the plan says "Not started" but issues are closed,
  // or says "Done" while issues are open or the milestone is still open.
  function disagrees(planStatus, ms) {
    var s = (planStatus || "").toLowerCase();
    if (s === "not started" && ms.closed_issues > 0) return "Issues are closed, but the plan says not started";
    if (s === "done" && (ms.open_issues > 0 || ms.state !== "closed")) return "The plan says done, but the milestone is open";
    if (ms.state === "closed" && s !== "done") return "The milestone is closed, but the plan doesn't say done";
    return "";
  }

  function renderMilestones(list, plan) {
    var ol = $("milestone-list");
    ol.innerHTML = "";
    var closed = 0, total = 0;
    list.sort(function (a, b) { return a.number - b.number; }).forEach(function (ms) {
      var id = (ms.title.match(/^M\d+/) || [""])[0];
      var name = ms.title.replace(/^M\d+\s*/, "");
      var count = ms.open_issues + ms.closed_issues;
      closed += ms.closed_issues; total += count;
      var pct = count ? Math.round((ms.closed_issues / count) * 100) : 0;
      var planStatus = plan.status[id] || "Not in plan";
      var pillClass = "pill" + (/progress/i.test(planStatus) ? " pill--progress" : /done/i.test(planStatus) ? " pill--done" : "");
      var why = disagrees(planStatus, ms);
      var fill = el("div", { class: "bar__fill" });
      var li = el("li", { class: "ms" + (id === plan.currentId ? " ms--current" : "") }, [
        el("div", { class: "ms__id", text: id }),
        el("div", {}, [
          el("h3", { class: "ms__title" }, [el("a", { href: ms.html_url, text: name })]),
          el("p", { class: "ms__desc", text: ms.description || "" })
        ]),
        el("div", { class: "ms__side" }, [
          el("div", { class: "ms__status" }, [
            el("span", { class: pillClass, text: planStatus }),
            why ? el("span", { class: "flag", title: why, text: "check" }) : null
          ]),
          el("div", { class: "bar", role: "img", "aria-label": pct + "% of issues closed" }, [fill]),
          el("span", { class: "ms__count", text: ms.closed_issues + " of " + count + " issues closed" })
        ])
      ]);
      ol.appendChild(li);
      requestAnimationFrame(function () { fill.style.width = pct + "%"; });
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
      var title = i.title.replace(/^Design:\s*/i, "");
      ul.appendChild(el("li", { class: "q" }, [
        el("a", { href: i.html_url, text: title }),
        el("span", { class: "q__meta", text: "#" + i.number + " · " + (i.milestone ? i.milestone.title.split(" ")[0] + " " : "later ") + "· " + i.comments + " comments" })
      ]));
    });
  }

  function liveFailed(err) {
    var msg = "Live data unavailable (" + err.message + "). See ";
    [["milestone-list", "milestones"], ["question-list", "issues?q=is%3Aopen+label%3Atype%3Adesign"]].forEach(function (pair) {
      var target = $(pair[0]);
      target.innerHTML = "";
      target.appendChild(el("li", { class: "muted" }, [
        document.createTextNode(msg),
        el("a", { href: "https://github.com/" + REPO + "/" + pair[1], text: "GitHub" }),
        document.createTextNode(".")
      ]));
    });
    $("now-issues").textContent = "—";
    $("now-questions").textContent = "—";
  }

  function start() {
    getText("build.json").then(JSON.parse).then(function (b) {
      var a = $("build-sha");
      a.textContent = b.sha.slice(0, 7);
      a.href = "https://github.com/" + REPO + "/commit/" + b.sha;
      $("build-time").textContent = " on " + b.time;
    }).catch(function () {});

    var planP = getText("docs/plan.md").then(function (md) {
      renderDoc(md, "docs/plan.md", $("plan-doc"), "plan-");
      var plan = readPlan(md);
      $("now-milestone").textContent = plan.current || "—";
      $("now-checked").textContent = plan.checked || "—";
      return plan;
    }).catch(function (err) {
      $("plan-doc").textContent = "Couldn't load docs/plan.md (" + err.message + ").";
      return readPlan("");
    });

    getText("docs/decisions/README.md").then(function (md) {
      renderDoc(md, "docs/decisions/README.md", $("decisions-doc"), "dec-");
    }).catch(function (err) {
      $("decisions-doc").textContent = "Couldn't load the decisions (" + err.message + ").";
    });

    Promise.all([planP, getJSON(API + "/milestones?state=all&per_page=100")])
      .then(function (r) { renderMilestones(r[1], r[0]); })
      .catch(liveFailed);

    getJSON(API + "/issues?state=open&labels=type:design&per_page=100")
      .then(renderQuestions)
      .catch(function () {});
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start);
  else start();
})();
