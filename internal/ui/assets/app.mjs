// The tailsuite lab UI (decision 0017). It trades the link's one-time
// secret for a session, streams the map and the results, and draws them.
import { h, render } from "./vendor/preact.mjs";
import { useEffect, useRef, useState } from "./vendor/hooks.mjs";
import htm from "./vendor/htm.mjs";
import cytoscape from "./vendor/cytoscape.mjs";

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

// untested is a policy path's ports that no test probed: a test on one
// port proves nothing about the others, which stay unverified.
function untested(ports, probed) {
  return ports.filter((p) => {
    const n = p.replace(/^tcp:/, "");
    return !/^\d+$/.test(n) || !probed.has(Number(n));
  });
}

function elements(view, run) {
  const els = [];
  const names = new Set();
  for (const d of view.devices || []) {
    names.add(d.name);
    els.push({ data: { id: "n:" + d.name, label: d.name, sub: (d.tags && d.tags.join(" ")) || d.user || "" } });
  }
  const known = (p) => names.has(p.from) && names.has(p.to);
  const tested = new Map(); // "from>to" → ports probed
  if (run) {
    for (const t of run.tests || []) {
      if (t.skipped) continue;
      (t.pairs || []).forEach((p, i) => {
        if (!known(p)) return;
        const key = p.from + ">" + p.to;
        if (!tested.has(key)) tested.set(key, new Set());
        tested.get(key).add(t.port);
        els.push({
          data: { id: "t:" + t.name + ":" + i, source: "n:" + p.from, target: "n:" + p.to,
            label: (t.port || p.probe) + (t.expect === "deny" ? " ✕" : "") },
          classes: edgeClass(p.result, t.expect),
        });
      });
    }
  }
  for (const p of view.paths || []) {
    if (!known(p)) continue;
    const ports = untested(p.ports || [], tested.get(p.from + ">" + p.to) || new Set());
    if (ports.length === 0) continue;
    els.push({ data: { id: "p:" + p.from + ">" + p.to, source: "n:" + p.from, target: "n:" + p.to,
      label: ports.join(", ") }, classes: "unverified" });
  }
  return els;
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
  ];
}

function LabMap({ view, run }) {
  const box = useRef(null), cy = useRef(null);
  useEffect(() => {
    cy.current = cytoscape({ container: box.current, style: cyStyle(), autoungrabify: false,
      userZoomingEnabled: true, boxSelectionEnabled: false });
    const mq = matchMedia("(prefers-color-scheme: dark)");
    const restyle = () => cy.current && cy.current.style(cyStyle());
    mq.addEventListener("change", restyle);
    // Refit when the panel changes size, as on rotating a phone.
    const ro = new ResizeObserver(() => { cy.current.resize(); cy.current.fit(undefined, 30); });
    ro.observe(box.current);
    return () => { ro.disconnect(); mq.removeEventListener("change", restyle); cy.current.destroy(); };
  }, []);
  useEffect(() => {
    const c = cy.current;
    c.elements().remove();
    c.add(elements(view, run));
    c.layout({ name: c.nodes().length > 8 ? "cose" : "circle", padding: 40, animate: false }).run();
  }, [view, run]);
  return html`<div class="map" ref=${box} role="img" aria-label="Map of the lab's devices and paths"></div>`;
}

function Tests({ run }) {
  if (!run) {
    return html`<p class="summary">Waiting for results. Run <code>tailsuite test</code>; they appear here as each run finishes.</p>`;
  }
  const s = run.summary || { pass: 0, fail: 0, unknown: 0, refused: 0, skipped: 0 };
  const tests = run.tests || [];
  return html`
    <p class="summary"><b>${run.version}</b>, profile ${run.profile}${run.strict ? ", strict" : ""}<br/>
      ${s.pass} passed, ${s.fail} failed, ${s.unknown} unknown, ${s.refused} refused, ${s.skipped} skipped</p>
    <ul class="tests">
      ${tests.map((t) => html`<li>
        <span class=${"result " + (t.skipped ? "skipped" : t.result)}>${t.skipped ? "skipped" : t.result}</span>
        <span>${t.name}</span>
        ${(t.reason || t.skipped) && html`<span class="why">${t.reason || t.skipped}</span>`}
      </li>`)}
    </ul>`;
}

function App() {
  const [view, setView] = useState(null);
  const [run, setRun] = useState(null);
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
        <${LabMap} view=${view} run=${run} />
        <div class="legend">
          <span class="key unverified"><i></i>unverified, from the policy</span>
          <span class="key pass"><i></i>allowed, proven</span>
          <span class="key blocked"><i></i>blocked, proven</span>
          <span class="key fail"><i></i>FAIL</span>
          <span class="key unproven"><i></i>UNKNOWN or REFUSED</span>
        </div>
        ${view.notes && view.notes.length > 0 && html`<ul class="notes">${view.notes.map((n) => html`<li>${n}</li>`)}</ul>`}
      </section>
      <section class="panel" aria-labelledby="tests-h"><h2 id="tests-h">Tests</h2><${Tests} run=${run} /></section>
    </main>`}`;
}

render(html`<${App} />`, document.getElementById("app"));
