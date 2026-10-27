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
    ],
    "summary": {"pass": 1, "fail": 1, "unknown": 0, "refused": 0, "skipped": 0}, "exit": 1,
}


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
        expect(page.locator(".tests li")).to_have_count(2, timeout=10000)
        expect(page.locator(".result.FAIL")).to_have_text("FAIL")
        expect(page.locator(".state")).to_contain_text("results")
        if shots:
            page.screenshot(path=shots + "/desktop-light.png", full_page=True)
        # A reload keeps the session, without trading again.
        page.reload()
        expect(page.locator(".tests li")).to_have_count(2, timeout=10000)
        # A fresh link pasted into the open tab is traded, and leaves the
        # address bar.
        page.goto(link2)
        page.wait_for_function("location.hash === ''", timeout=10000)
        expect(page.locator(".tests li")).to_have_count(2, timeout=10000)
        check(not page.locator("[role=alert]").is_visible(), "an error after pasting a fresh link")
        # Phone width, dark: no sideways scroll.
        page.set_viewport_size({"width": 390, "height": 800})
        page.emulate_media(color_scheme="dark")
        time.sleep(0.3)
        check(page.evaluate("document.documentElement.scrollWidth <= window.innerWidth"), "the page scrolls sideways at 390px")
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
