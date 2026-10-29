// The tailsuite lab UI (decision 0017). It trades the link's one-time
// secret for a session, streams the map and the results, and draws them.
import { h, render } from "./vendor/preact.mjs";
import { useEffect, useRef, useState } from "./vendor/hooks.mjs";
import htm from "./vendor/htm.mjs";
import cytoscape from "./vendor/cytoscape.mjs";
import { MEANING, probeLabel, probed, provesPair, provesPath, shortPort, untested } from "./explain.mjs";

const html = htm.bind(h);
const KEY = "tailsuite-session";

function stored() {
  try { return sessionStorage.getItem(KEY) || ""; } catch { return ""; }
}
function store(s) {
  try { if (s) sessionStorage.setItem(KEY, s); else sessionStorage.removeItem(KEY); } catch { /* the session lasts this load only */ }
}

// session returns a live session secret. A link in the address bar wins,
// even over a stored session, since a fresh link is pasted to replace a
// dead one: its secret leaves the address bar and is traded. Otherwise
// the stored session.
async function session() {
  const secret = location.hash.slice(1);
  if (!secret) {
    const have = stored();
    if (have) return have;
    throw new Error("Open the link tailsuite up printed. Press Enter in its terminal for a fresh one.");
  }
  history.replaceState(null, "", location.pathname);
  const res = await fetch("/api/session", { method: "POST", headers: { "X-Tailsuite-Link": secret } });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || "The link was refused.");
  store(body.session);
  return body.session;
}

// stream reads Server-Sent Events from fetch, which can send the session
// header that EventSource can't.
async function stream(sess, on, signal) {
  const res = await fetch("/api/events", { headers: { "X-Tailsuite-Session": sess }, signal });
  if (res.status === 401) {
    store("");
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || "The session has ended.");
  }
  if (!res.ok || !res.body) throw new Error("The lab's stream failed (" + res.status + ").");
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) throw new Error("tailsuite up has stopped. Start it again for a fresh link.");
    buf += value;
    let i;
    while ((i = buf.indexOf("\n\n")) >= 0) {
      const chunk = buf.slice(0, i);
      buf = buf.slice(i + 2);
      let event = "message", data = "";
      for (const line of chunk.split("\n")) {
        if (line.startsWith("event: ")) event = line.slice(7);
        else if (line.startsWith("data: ")) data += line.slice(6);
      }
      if (event === "ended") {
        store("");
        throw new Error(data ? JSON.parse(data) : "The session has ended.");
      }
      on(event, data ? JSON.parse(data) : null);
    }
  }
}

// edgeClass is how a tested path is drawn. A deny that held is proven
// too, so it's drawn as proven, dotted for blocked.
function edgeClass(result, expect) {
  if (result === "PASS") return expect === "deny" ? "blocked" : "pass";
  if (result === "FAIL") return "fail";
  return "unproven";
}

function elements(view, run) {
  const els = [];
  const names = new Set();
  for (const d of view.devices || []) {
    names.add(d.name);
    els.push({ data: { id: "n:" + d.name, label: d.name, sub: (d.tags && d.tags.join(" ")) || d.user || "" } });
  }
  const known = (p) => names.has(p.from) && names.has(p.to);
  if (run) {
    for (const t of run.tests || []) {
      if (t.skipped) continue;
      (t.pairs || []).forEach((p, i) => {
        if (!known(p)) return;
        els.push({
          data: { id: "t:" + t.name + ":" + i, source: "n:" + p.from, target: "n:" + p.to,
            from: p.from, to: p.to, test: t.name,
            label: probeLabel(t, p) + (t.expect === "deny" ? " ✕" : "") },
          classes: edgeClass(p.result, t.expect),
        });
      });
    }
  }
  for (const p of view.paths || []) {
    if (!known(p)) continue;
    const ports = untested(p.ports || [], probed(p.from, p.to, run));
    if (ports.length === 0) continue;
    els.push({ data: { id: "p:" + p.from + ">" + p.to, source: "n:" + p.from, target: "n:" + p.to,
      from: p.from, to: p.to, label: ports.map(shortPort).join(", ") }, classes: "unverified" });
  }
  return els;
}

// paths is every from → to the map draws, in order: the policy's and
// the tests'.
function paths(view, run) {
  const seen = new Map();
  const add = (p) => { const k = p.from + ">" + p.to; if (!seen.has(k)) seen.set(k, { from: p.from, to: p.to }); };
  (view.paths || []).forEach(add);
  for (const t of (run && run.tests) || []) if (!t.skipped) (t.pairs || []).forEach(add);
  return [...seen.values()].sort((a, b) => a.from.localeCompare(b.from) || a.to.localeCompare(b.to));
}

// picked says whether a test is part of the selection: the test itself,
// or one that probed the selected path.
function picked(sel, t) {
  if (!sel) return false;
  if (sel.test) return sel.test === t.name;
  return !t.skipped && (t.pairs || []).some((p) => p.from === sel.from && p.to === sel.to);
}

function token(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

function cyStyle() {
  const text = token("--bh-text"), muted = token("--bh-muted"), surface = token("--bh-surface");
  return [
    { selector: "node", style: {
      "background-color": surface, "border-width": 2, "border-color": text, shape: "round-rectangle",
      width: 120, height: 44, label: "data(label)", color: text, "text-valign": "center",
      "font-family": token("--bh-font-mono"), "font-size": 13 } },
    { selector: "edge", style: {
      "curve-style": "bezier", "target-arrow-shape": "triangle", width: 3, label: "data(label)",
      "font-family": token("--bh-font-mono"), "font-size": 11, color: text,
      "text-background-color": surface, "text-background-opacity": 1, "text-background-padding": 2 } },
    { selector: "edge.unverified", style: { "line-style": "dashed", "line-color": muted, "target-arrow-color": muted, width: 2, color: muted } },
    { selector: "edge.pass", style: { "line-color": token("--ui-pass"), "target-arrow-color": token("--ui-pass") } },
    { selector: "edge.blocked", style: { "line-style": "dotted", "line-color": token("--ui-pass"), "target-arrow-color": token("--ui-pass"), "target-arrow-shape": "tee" } },
    { selector: "edge.fail", style: { "line-color": token("--ui-fail"), "target-arrow-color": token("--ui-fail"), width: 4 } },
    { selector: "edge.unproven", style: { "line-style": "dotted", "line-color": token("--ui-unproven"), "target-arrow-color": token("--ui-unproven") } },
    { selector: "edge.dim", style: { opacity: 0.25 } },
    { selector: "edge.sel", style: { width: 5, "overlay-color": token("--bh-accent"), "overlay-opacity": 0.18, "overlay-padding": 6, "z-index": 10 } },
  ];
}

function LabMap({ view, run, sel, onSelect }) {
  const box = useRef(null), cy = useRef(null), pick = useRef(onSelect);
  pick.current = onSelect;
  useEffect(() => {
    const c = cytoscape({ container: box.current, style: cyStyle(), autoungrabify: false,
      userZoomingEnabled: true, boxSelectionEnabled: false, autounselectify: true });
    cy.current = c;
    // A tap on a line selects its path; a tap on the background clears it.
    c.on("tap", "edge", (e) => pick.current({ from: e.target.data("from"), to: e.target.data("to"), via: "map" }));
    c.on("tap", (e) => { if (e.target === c) pick.current(null); });
    const mq = matchMedia("(prefers-color-scheme: dark)");
    const restyle = () => cy.current && cy.current.style(cyStyle());
    mq.addEventListener("change", restyle);
    // Refit when the panel changes size, as on rotating a phone.
    const ro = new ResizeObserver(() => { c.resize(); c.fit(undefined, 30); });
    ro.observe(box.current);
    return () => { ro.disconnect(); mq.removeEventListener("change", restyle); c.destroy(); };
  }, []);
  useEffect(() => {
    const c = cy.current;
    c.elements().remove();
    c.add(elements(view, run));
    c.layout({ name: c.nodes().length > 8 ? "cose" : "circle", padding: 40, animate: false }).run();
  }, [view, run]);
  useEffect(() => {
    const c = cy.current;
    c.edges().removeClass("sel dim");
    if (!sel) return;
    const on = c.edges().filter((e) => sel.test ? e.data("test") === sel.test
      : e.data("from") === sel.from && e.data("to") === sel.to);
    on.addClass("sel");
    c.edges().difference(on).addClass("dim");
  }, [sel, view, run]);
  return html`<div class="map" ref=${box} role="img" aria-label="Map of the lab's devices and paths. The same paths are listed under it."></div>`;
}

// Outcome is a result's badge, which says what it means on hover or
// focus (and to a screen reader, always).
function Outcome({ kind }) {
  return html`<span class="outcome" tabindex="0"><span class=${"result " + kind}>${kind}</span><span class="tip" role="tooltip">${kind}: ${MEANING[kind]}</span></span>`;
}

// Proves is what the selection proves, in plain English.
function Proves({ view, run, sel, onSelect }) {
  let title, lines;
  if (!sel) {
    title = "What a line proves";
    lines = [{ text: "Select a line on the map, a path below, or a test, to see what it proves." }];
  } else if (sel.test) {
    const t = ((run && run.tests) || []).find((x) => x.name === sel.test);
    if (!t) return null;
    title = t.name;
    const kind = t.skipped ? "skipped" : t.result;
    lines = t.skipped || !(t.pairs || []).length
      ? [{ result: kind, text: (t.skipped || t.reason || MEANING[kind]) }]
      : t.pairs.map((p) => ({ result: p.result, text: provesPair(t, p) }));
  } else {
    title = sel.from + " → " + sel.to;
    lines = provesPath(sel.from, sel.to, view, run);
  }
  const all = paths(view, run);
  return html`<div class="proves">
    <h3>${title}</h3>
    <ul aria-live="polite">${lines.map((l) => html`<li>
      ${l.result ? html`<${Outcome} kind=${l.result} />` : html`<span></span>`}
      <span>${l.text}${l.test && html` <span class="why">(${l.test})</span>`}</span></li>`)}</ul>
    ${all.length > 0 && html`<details class="paths"><summary>Paths as a list (${all.length})</summary>
      <ul>${all.map((p) => {
        const on = sel && !sel.test && sel.from === p.from && sel.to === p.to;
        return html`<li><button type="button" class="pick" aria-pressed=${on ? "true" : "false"}
          onClick=${() => onSelect(on ? null : { from: p.from, to: p.to, via: "list" })}>${p.from} → ${p.to}</button></li>`;
      })}</ul></details>`}
  </div>`;
}

function Tests({ run, sel, onSelect }) {
  const list = useRef(null);
  useEffect(() => {
    // A line picked on the map brings its tests into view.
    if (!sel || sel.via !== "map" || !list.current) return;
    const first = list.current.querySelector("li.sel");
    if (first) first.scrollIntoView({ block: "nearest" });
  }, [sel]);
  if (!run) {
    return html`<p class="summary">Waiting for results. Run <code>tailsuite test</code>; they appear here as each run finishes.</p>`;
  }
  const s = run.summary || { pass: 0, fail: 0, unknown: 0, refused: 0, skipped: 0 };
  const tests = run.tests || [];
  return html`
    <p class="summary"><b>${run.version}</b>, profile ${run.profile}${run.strict ? ", strict" : ""}<br/>
      ${s.pass} passed, ${s.fail} failed, ${s.unknown} unknown, ${s.refused} refused, ${s.skipped} skipped</p>
    <ul class="tests" ref=${list}>
      ${tests.map((t) => {
        const on = picked(sel, t);
        return html`<li class=${on ? "sel" : ""}>
          <${Outcome} kind=${t.skipped ? "skipped" : t.result} />
          <button type="button" class="pick" aria-pressed=${sel && sel.test === t.name ? "true" : "false"}
            onClick=${() => onSelect(sel && sel.test === t.name ? null : { test: t.name, via: "list" })}>${t.name}</button>
          ${(t.reason || t.skipped) && html`<span class="why">${t.reason || t.skipped}</span>`}
        </li>`;
      })}
    </ul>`;
}

function App() {
  const [view, setView] = useState(null);
  const [run, setRun] = useState(null);
  const [sel, setSel] = useState(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let abort = null;
    const connect = async () => {
      if (abort) abort.abort();
      const mine = new AbortController();
      abort = mine;
      setError("");
      try {
        const s = await session();
        await stream(s, (event, data) => {
          if (event === "view") setView(data);
          if (event === "results") setRun(data);
        }, mine.signal);
      } catch (e) {
        if (!mine.signal.aborted) setError(e.message);
      }
    };
    // A fresh link pasted into this tab changes only the fragment, which
    // doesn't reload the page.
    const onHash = () => { if (location.hash.length > 1) connect(); };
    addEventListener("hashchange", onHash);
    connect();
    return () => { removeEventListener("hashchange", onHash); if (abort) abort.abort(); };
  }, []);
  const state = error ? "Disconnected" : !view ? "Connecting…" : run
    ? html`<b>results</b> from the last <code>tailsuite test</code>` : html`<b>unverified</b>: drawn from the policy, before any traffic`;
  return html`
    <header class="bar"><span class="brand">tailsuite</span><span class="state">${state}</span></header>
    ${error && html`<p class="notice" role="alert">${error}</p>`}
    ${view && html`<main>
      <section class="panel" aria-labelledby="map-h"><h2 id="map-h">The lab</h2>
        <${LabMap} view=${view} run=${run} sel=${sel} onSelect=${setSel} />
        <div class="legend">
          <span class="key unverified" tabindex="0"><i></i>unverified, from the policy<span class="tip" role="tooltip">${MEANING.unverified}</span></span>
          <span class="key pass" tabindex="0"><i></i>allowed, proven<span class="tip" role="tooltip">PASS on an allow test: traffic arrived, as the test expected.</span></span>
          <span class="key blocked" tabindex="0"><i></i>blocked, proven<span class="tip" role="tooltip">PASS on a deny test: traffic didn't arrive, and a positive control showed the target was up.</span></span>
          <span class="key fail" tabindex="0"><i></i>FAIL<span class="tip" role="tooltip">FAIL: ${MEANING.FAIL}</span></span>
          <span class="key unproven" tabindex="0"><i></i>UNKNOWN or REFUSED<span class="tip" role="tooltip">Neither a pass nor a fail: the lab couldn't reproduce it, or the engine can't run it.</span></span>
        </div>
        <${Proves} view=${view} run=${run} sel=${sel} onSelect=${setSel} />
        ${view.notes && view.notes.length > 0 && html`<ul class="notes">${view.notes.map((n) => html`<li>${n}</li>`)}</ul>`}
      </section>
      <section class="panel" aria-labelledby="tests-h"><h2 id="tests-h">Tests</h2><${Tests} run=${run} sel=${sel} onSelect=${setSel} /></section>
    </main>`}`;
}

render(html`<${App} />`, document.getElementById("app"));
