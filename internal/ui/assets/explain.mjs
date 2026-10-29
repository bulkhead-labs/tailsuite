// What the page's outcomes and paths mean, in plain English (#89). The
// outcome words are the same as docs/architecture.md's Results section.

export const MEANING = {
  PASS: "The traffic did what the test expected.",
  FAIL: "The traffic didn't do what the test expected.",
  UNKNOWN: "The lab can't reproduce a condition the result depends on, so it's neither a pass nor a fail.",
  REFUSED: "The engine doesn't support a feature the test needs, so the test didn't run. It's neither a pass nor a fail.",
  skipped: "A profile or flag left this test out of the run. It's counted, never dropped silently.",
  unverified: "Drawn from the policy, before any traffic: no test has proven it yet.",
};

// portLabel says a policy port in words: "tcp:80" is TCP 80, a bare
// port is any protocol on it, "*" every port.
export function portLabel(p) {
  if (p === "app") return "an app capability";
  if (p === "*") return "all ports";
  const i = p.indexOf(":");
  if (i < 0) return "any protocol " + p;
  const proto = p.slice(0, i).toUpperCase(), port = p.slice(i + 1);
  return proto + " " + (port === "*" ? "all ports" : port);
}

// shortPort is portLabel for a line on the map, where space is short.
export function shortPort(p) {
  if (p === "*") return "all";
  const i = p.indexOf(":");
  if (i < 0) return p === "app" ? p : "any " + p;
  return p.slice(0, i).toUpperCase() + " " + (p.slice(i + 1) === "*" ? "all" : p.slice(i + 1));
}

// probeLabel is what a test sent: its probe and port, as "TCP 5432".
export function probeLabel(t, p) {
  const probe = (p.probe || "tcp").toUpperCase();
  return t.port ? probe + " " + t.port : probe;
}

// control is a blocked pair's positive control, in words, or "".
function control(p) {
  for (const half of p.halves || []) {
    const c = half.control;
    if (!c || !c.alive) continue;
    return c.from === p.to ? `, and ${p.to} is up: it answered itself (positive control)`
      : `, and ${p.to} is up: it answered ${c.from} (positive control)`;
  }
  return "";
}

// provesPair is what one test's result on one pair proves.
export function provesPair(t, p) {
  const how = probeLabel(t, p), deny = t.expect === "deny";
  const reason = p.reason || t.reason;
  switch (p.result) {
    case "PASS":
      return deny ? `Proved ${p.from} can't reach ${p.to} on ${how} (deny)${control(p)}.`
        : `Proved ${p.from} reaches ${p.to} on ${how} (allow).`;
    case "FAIL":
      return deny ? `${p.from} reached ${p.to} on ${how}, which the test says it mustn't.`
        : `${p.from} couldn't reach ${p.to} on ${how}, which the test says it should.` + (reason ? " " + reason + "." : "");
    case "REFUSED":
      return `Not tested on ${how}: ${reason || MEANING.REFUSED}`;
    default:
      return `Not proven either way on ${how}: ${reason || MEANING.UNKNOWN}`;
  }
}

// provesPath is everything the run says about one path, from → to: a
// line per test that probed it, then the policy's ports no test probed.
export function provesPath(from, to, view, run) {
  const lines = [];
  for (const t of (run && run.tests) || []) {
    if (t.skipped) continue;
    for (const p of t.pairs || []) {
      if (p.from === from && p.to === to) lines.push({ test: t.name, result: p.result, text: provesPair(t, p) });
    }
  }
  const path = (view.paths || []).find((p) => p.from === from && p.to === to);
  const open = path ? untested(path.ports || [], probed(from, to, run)) : [];
  if (open.length > 0) {
    lines.push({ result: "unverified", text: `The policy lets ${from} reach ${to} on ${open.map(portLabel).join(", ")}, but no test has sent traffic there, so nothing has proven it yet.` });
  }
  if (lines.length === 0) lines.push({ result: "unverified", text: `Nothing has proven a path from ${from} to ${to} yet.` });
  return lines;
}

// probed is the ports the run's tests sent traffic to, from → to.
export function probed(from, to, run) {
  const ports = new Set();
  for (const t of (run && run.tests) || []) {
    if (t.skipped) continue;
    if ((t.pairs || []).some((p) => p.from === from && p.to === to)) ports.add(t.port);
  }
  return ports;
}

// untested is a policy path's ports that no test probed: a test on one
// port proves nothing about the others, which stay unverified.
export function untested(ports, seen) {
  return ports.filter((p) => {
    const n = p.replace(/^tcp:/, "");
    return !/^\d+$/.test(n) || !seen.has(Number(n));
  });
}
