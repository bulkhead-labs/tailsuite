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
// port is any protocol on it, "*" every port. An open port whose TCP or
// UDP was proven (see untested) is the protocols other than those.
export function portLabel(o) {
  const p = o.port;
  if (p === "app") return "an app capability";
  if (p === "*") return "all ports";
  const i = p.indexOf(":");
  if (i < 0) return o.except ? `protocols other than ${o.except.map((x) => x.toUpperCase()).join(" and ")} on ${p}` : "any protocol on " + p;
  const proto = p.slice(0, i).toUpperCase(), port = p.slice(i + 1);
  return proto + " " + (port === "*" ? "all ports" : port);
}

// shortPort is portLabel for a line on the map, where space is short.
export function shortPort(o) {
  const p = o.port;
  if (p === "*") return "all";
  const i = p.indexOf(":");
  if (i < 0) return p === "app" ? p : (o.except ? "other " : "any ") + p;
  return p.slice(0, i).toUpperCase() + " " + (p.slice(i + 1) === "*" ? "all" : p.slice(i + 1));
}

// protocols is what a test asserts: its own list, or TCP.
function protocols(t) {
  return t.protocols && t.protocols.length > 0 ? t.protocols : ["tcp"];
}

// probeLabel is what a test asserts, as "TCP 5432" or "UDP 53"; an HTTP
// probe of TCP says HTTP.
export function probeLabel(t, p) {
  const ps = protocols(t);
  const proto = p.probe === "http" && ps.length === 1 && ps[0] === "tcp" ? "HTTP" : ps.map((x) => x.toUpperCase()).join("+");
  return t.port ? proto + " " + t.port : proto;
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
// line per test on it, then the policy's ports no result has proven.
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
    lines.push({ result: "unverified", text: `The policy lets ${from} reach ${to} on ${open.map(portLabel).join(", ")}, but no test has proven it, so it's unverified.` });
  }
  if (lines.length === 0) lines.push({ result: "unverified", text: `Nothing has proven a path from ${from} to ${to} yet.` });
  return lines;
}

// probed is what the run proved, from → to, as "tcp:5432": only a PASS
// or a FAIL proves anything, for each protocol its test asserts.
export function probed(from, to, run) {
  const seen = new Set();
  for (const t of (run && run.tests) || []) {
    if (t.skipped) continue;
    if (!(t.pairs || []).some((p) => p.from === from && p.to === to && (p.result === "PASS" || p.result === "FAIL"))) continue;
    for (const proto of protocols(t)) seen.add(proto + ":" + t.port);
  }
  return seen;
}

// untested is a policy path's ports nothing proved, as {port, except}: a
// result on one port and protocol proves nothing about the others. A
// bare port is every protocol, so proving its TCP or UDP leaves the rest.
export function untested(ports, seen) {
  const out = [];
  for (const p of ports) {
    const i = p.indexOf(":");
    const n = i < 0 ? p : p.slice(i + 1);
    if (!/^\d+$/.test(n)) { out.push({ port: p }); continue; }
    if (i >= 0) {
      if (!seen.has(p.slice(0, i).toLowerCase() + ":" + n)) out.push({ port: p });
      continue;
    }
    const done = ["tcp", "udp"].filter((x) => seen.has(x + ":" + n));
    out.push(done.length > 0 ? { port: p, except: done } : { port: p });
  }
  return out;
}
