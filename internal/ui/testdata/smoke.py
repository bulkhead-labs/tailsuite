# The UI's Playwright smoke test (#11). smoke_test.go runs it in the
# pinned Playwright image, against a server it started, as:
#   python3 smoke.py <link> <a second link> <results file> [screenshots folder]
# Synthetic data only.
import json
import sys
import time
from urllib.parse import urlparse

from playwright.sync_api import expect, sync_playwright

link, link2, results = sys.argv[1], sys.argv[2], sys.argv[3]
shots = sys.argv[4] if len(sys.argv) > 4 else ""  # a folder for screenshots, to look at by hand
origin = urlparse(link)
failures = []

RUN = {
    "engine": "headscale", "version": "headscale 0.29.5", "profile": "default", "strict": False,
    "tests": [
        {"name": "engineers reach the web tier", "source": "tests", "where": "t", "labels": [], "expect": "allow", "port": 80,
         "result": "PASS", "pairs": [{"from": "laptop", "to": "server", "probe": "http", "result": "PASS"}]},
        {"name": "laptops can't reach the database", "source": "tests", "where": "t", "labels": [], "expect": "deny", "port": 5432,
         "result": "FAIL", "reason": "laptop reached db:5432", "pairs": [{"from": "laptop", "to": "db", "probe": "tcp", "result": "FAIL"}]},
        {"name": "the web tier's admin port is closed to engineers", "source": "tests", "where": "t", "labels": [], "expect": "deny", "port": 9000,
         "result": "PASS", "pairs": [{"from": "laptop", "to": "server", "probe": "tcp", "result": "PASS",
                                      "halves": [{"protocol": "tcp", "saw": "blocked", "control": {"from": "server", "alive": True}}]}]},
    ],
    "summary": {"pass": 2, "fail": 1, "unknown": 0, "refused": 0, "skipped": 0}, "exit": 1,
}


# click_line clicks the middle of a line on the map, found through
# Cytoscape's own registry on the container.
def click_line(page, pred):
    at = page.evaluate("""pred => {
        const box = document.querySelector('.map'), cy = box._cyreg.cy;
        const e = cy.edges().filter(x => Object.keys(pred).every(k => x.data(k) === pred[k]))[0];
        if (!e) return null;
        const m = e.midpoint(), pan = cy.pan(), z = cy.zoom(), r = box.getBoundingClientRect();
        return {x: r.left + m.x * z + pan.x, y: r.top + m.y * z + pan.y};
    }""", pred)
    if at is None:
        failures.append("no line on the map for %s" % pred)
        return
    page.mouse.click(at["x"], at["y"])


# expect_lines waits for the map's selected lines to be want.
def expect_lines(page, want, what):
    try:
        page.wait_for_function("""want => JSON.stringify(document.querySelector('.map')._cyreg.cy
            .edges('.sel').map(e => e.data('label'))) === JSON.stringify(want)""", arg=want, timeout=5000)
    except Exception:
        failures.append(what + " didn't select only %s" % want)


def check(cond, what):
    if not cond:
        failures.append(what)


with sync_playwright() as p:
    browser = p.chromium.launch()
    for scheme, width in (("light", 1280), ("dark", 390)):
        ctx = browser.new_context(color_scheme=scheme, viewport={"width": width, "height": 800})
        page = ctx.new_page()
        external, errors = [], []
        page.on("request", lambda r: external.append(r.url) if urlparse(r.url).netloc != origin.netloc else None)
        page.on("console", lambda m: errors.append(m.text) if m.type == "error" else None)
        page.on("pageerror", lambda e: errors.append(str(e)))
        if scheme == "light":
            page.goto(link)
        else:
            # The link was traded: a fresh tab without it is told what to do.
            page.goto(origin.scheme + "://" + origin.netloc + "/")
            expect(page.get_by_role("alert")).to_contain_text("Open the link tailsuite up printed")
            ctx.close()
            continue
        expect(page.locator(".state")).to_contain_text("unverified")
        check("#" not in page.url, "the secret stayed in the address bar")
        expect(page.locator(".map canvas").first).to_be_attached()
        expect(page.locator(".summary")).to_contain_text("Waiting for results")
        with open(results, "w") as f:
            json.dump(RUN, f)
        expect(page.locator(".tests li")).to_have_count(3, timeout=10000)
        expect(page.locator(".tests .result.FAIL")).to_have_text("FAIL")
        expect(page.locator(".state")).to_contain_text("results")
        # An outcome says what it means on focus (#89).
        page.locator(".tests .outcome").nth(1).focus()
        expect(page.locator(".tests .outcome").nth(1).locator(".tip")).to_be_visible()
        expect(page.locator(".tests .outcome").nth(1).locator(".tip")).to_contain_text("FAIL: The traffic didn't do what the test expected.")
        # Lines carry their protocol and port.
        labels = page.evaluate("document.querySelector('.map')._cyreg.cy.edges().map(e => e.data('label'))")
        check("TCP 5432 ✕" in labels and "HTTP 80" in labels and "TCP 5432" in labels, "line labels are %s" % labels)
        # Clicking a line says what it proves, and picks its tests.
        click_line(page, {"from": "laptop", "to": "db"})
        expect(page.locator(".proves h3")).to_have_text("laptop → db")
        expect(page.locator(".proves")).to_contain_text("laptop reached db on TCP 5432, which the test says it mustn't.")
        expect(page.locator(".tests li.sel")).to_have_count(1)
        expect(page.locator(".tests li.sel")).to_contain_text("laptops can't reach the database")
        click_line(page, {"test": "the web tier's admin port is closed to engineers"})
        expect(page.locator(".proves")).to_contain_text("Proved laptop can't reach server on TCP 9000 (deny), and server is up: it answered itself (positive control).")
        expect(page.locator(".tests li.sel")).to_have_count(2)
        # A path no test probed says so.
        click_line(page, {"from": "server", "to": "db"})
        expect(page.locator(".proves")).to_contain_text("no test has sent traffic there")
        if shots:
            page.screenshot(path=shots + "/desktop-light.png", full_page=True)
        # Picking a test picks its line.
        page.get_by_role("button", name="engineers reach the web tier").click()
        expect(page.locator(".proves h3")).to_have_text("engineers reach the web tier")
        expect_lines(page, ["HTTP 80"], "picking a test")
        # By keyboard: the paths as a list.
        page.locator(".paths summary").focus()
        page.keyboard.press("Enter")
        page.get_by_role("button", name="server → db").focus()
        page.keyboard.press("Enter")
        expect(page.locator(".proves h3")).to_have_text("server → db")
        expect_lines(page, ["TCP 5432"], "the keyboard's path")
        # A reload keeps the session, without trading again.
        page.reload()
        expect(page.locator(".tests li")).to_have_count(3, timeout=10000)
        # A fresh link pasted into the open tab is traded, and leaves the
        # address bar.
        page.goto(link2)
        page.wait_for_function("location.hash === ''", timeout=10000)
        expect(page.locator(".tests li")).to_have_count(3, timeout=10000)
        check(not page.locator("[role=alert]").is_visible(), "an error after pasting a fresh link")
        # Phone width, dark: no sideways scroll.
        page.set_viewport_size({"width": 390, "height": 800})
        page.emulate_media(color_scheme="dark")
        time.sleep(0.3)
        check(page.evaluate("document.documentElement.scrollWidth <= window.innerWidth"), "the page scrolls sideways at 390px")
        # A tap on a line works at phone width too.
        page.locator(".map").scroll_into_view_if_needed()
        click_line(page, {"from": "laptop", "to": "db"})
        expect(page.locator(".proves h3")).to_have_text("laptop → db")
        if shots:
            page.screenshot(path=shots + "/phone-dark.png", full_page=True)
        bg = page.evaluate("getComputedStyle(document.body).backgroundColor")
        check(bg == "rgb(21, 25, 29)", "dark mode background is " + bg)
        check(not external, "requests outside the UI: %s" % external)
        check(not errors, "console errors: %s" % errors)
        ctx.close()
    browser.close()

if failures:
    print("FAIL:", *failures, sep="\n  ")
    sys.exit(1)
print("ok")
