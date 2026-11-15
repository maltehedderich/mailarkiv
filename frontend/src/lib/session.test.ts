import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { ApiResult, SessionInfo } from "./api";
import {
  clearSignedIn,
  markSignedIn,
  safeNext,
  showsSessionEnded,
  signInPath,
  watchSession,
} from "./session";

// A session that ends while a page is open sends the page to sign-in with a
// message, and signing in returns to the same page. A first visit with no
// session shows no such message.

const ORIGIN = "http://localhost:8080";

/** sessionStorage, which Node doesn't provide the way a browser tab does. */
function memoryStorage(): Storage {
  const items = new Map<string, string>();
  return {
    get length() {
      return items.size;
    },
    clear: () => items.clear(),
    getItem: (key) => items.get(key) ?? null,
    key: (index) => [...items.keys()][index] ?? null,
    removeItem: (key) => void items.delete(key),
    setItem: (key, value) => void items.set(key, String(value)),
  };
}

/** Storage a browser has blocked, for example in some private windows. */
function blockedStorage(): Storage {
  const refuse = () => {
    throw new DOMException("The operation is insecure.", "SecurityError");
  };
  return {
    length: 0,
    clear: refuse,
    getItem: refuse,
    key: refuse,
    removeItem: refuse,
    setItem: refuse,
  };
}

const at = (path: string) => new URL(path, ORIGIN);

beforeEach(() => {
  vi.stubGlobal("sessionStorage", memoryStorage());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("signInPath", () => {
  test("a first visit to the list opens plain sign-in", () => {
    expect(signInPath(at("/"))).toBe("/sign-in");
  });

  test("a first visit elsewhere keeps the page to return to", () => {
    expect(signInPath(at("/?page=3"))).toBe("/sign-in?next=%2F%3Fpage%3D3");
  });

  test.each([
    ["/", "/sign-in"],
    ["/status", "/sign-in?next=%2Fstatus"],
    ["/?page=3#top", "/sign-in?next=%2F%3Fpage%3D3%23top"],
  ])(
    "redirects from a load URL at %s without reading its hash",
    (path, want) => {
      const url = at(path);
      // SvelteKit forbids reading load's url.hash in development.
      Object.defineProperty(url, "hash", {
        get() {
          throw new Error("Cannot access event.url.hash");
        },
      });

      expect(signInPath(url)).toBe(want);
    },
  );

  test("an ended session on the list says it ended", () => {
    markSignedIn();
    expect(signInPath(at("/"))).toBe("/sign-in?ended=1");
  });

  test("an ended session keeps the path, query, and fragment", () => {
    markSignedIn();
    expect(signInPath(at("/?page=3#top"))).toBe(
      "/sign-in?ended=1&next=%2F%3Fpage%3D3%23top",
    );
  });

  test("after sign-out, the tab counts as a first visit again", () => {
    markSignedIn();
    clearSignedIn();
    expect(signInPath(at("/?page=3"))).toBe("/sign-in?next=%2F%3Fpage%3D3");
  });

  test("blocked storage counts as a first visit", () => {
    vi.stubGlobal("sessionStorage", blockedStorage());
    expect(() => markSignedIn()).not.toThrow();
    expect(() => clearSignedIn()).not.toThrow();
    expect(signInPath(at("/"))).toBe("/sign-in");
  });
});

describe("showsSessionEnded", () => {
  test("shows when the URL says so and this tab was signed in", () => {
    markSignedIn();
    expect(showsSessionEnded(at("/sign-in?ended=1&next=%2F"))).toBe(true);
  });

  // The server's redirect always says ended=1, because it can't know the tab.
  test("hides on a tab that was never signed in", () => {
    expect(showsSessionEnded(at("/sign-in?ended=1&next=%2F"))).toBe(false);
  });

  test("hides after sign-out", () => {
    markSignedIn();
    clearSignedIn();
    expect(showsSessionEnded(at("/sign-in?ended=1"))).toBe(false);
  });

  test.each(["/sign-in", "/sign-in?ended=0", "/sign-in?ended=true"])(
    "hides for %s",
    (path) => {
      markSignedIn();
      expect(showsSessionEnded(at(path))).toBe(false);
    },
  );

  test("hides when storage is blocked", () => {
    vi.stubGlobal("sessionStorage", blockedStorage());
    expect(showsSessionEnded(at("/sign-in?ended=1"))).toBe(false);
  });
});

describe("safeNext", () => {
  test.each([
    [null, "/"],
    ["", "/"],
    ["/", "/"],
    ["/?page=3", "/?page=3"],
    ["/?page=3#top", "/?page=3#top"],
    [`${ORIGIN}/?page=3`, "/?page=3"],
  ])("accepts %j as %j", (next, want) => {
    expect(safeNext(next, ORIGIN)).toBe(want);
  });

  // Only this origin. Anything else would turn sign-in into an open redirect.
  test.each([
    "//evil.test",
    "//evil.test/?page=3",
    "https://evil.test/",
    "http://localhost:8081/",
    "https://localhost:8080/",
    "http://localhost:8080.evil.test/",
    "/\\evil.test",
    "\\\\evil.test",
    "/\t/evil.test",
    // On this origin as written, but they normalize to //evil.test.
    "/.//evil.test",
    "/..//evil.test",
    `${ORIGIN}//evil.test/`,
    "javascript:alert(1)",
    "data:text/html,<script>alert(1)</script>",
    "http://[::1",
  ])("refuses %j", (next) => {
    expect(safeNext(next, ORIGIN)).toBe("/");
  });
});

describe("watchSession", () => {
  type Read = ApiResult<SessionInfo>;

  let page: EventTarget & { visibilityState: DocumentVisibilityState };
  let windowEvents: EventTarget;

  beforeEach(() => {
    vi.useFakeTimers();
    page = Object.assign(new EventTarget(), {
      visibilityState: "visible" as DocumentVisibilityState,
    });
    windowEvents = new EventTarget();
    vi.stubGlobal("document", page);
    vi.stubGlobal("window", windowEvents);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  /** A session read that answers when the test says so. */
  function heldRead() {
    let answer: (result: Read) => void = () => {};
    const read = vi.fn(
      () => new Promise<Read>((resolve) => (answer = resolve)),
    );
    return { read, answer: (result: Read) => answer(result) };
  }

  const ok = (expiresIn: number): Read => ({
    kind: "ok",
    value: { problemCount: 0, expiresIn },
  });

  const show = () => page.dispatchEvent(new Event("visibilitychange"));
  // Lets an answered read finish its work.
  const settle = () => vi.advanceTimersByTimeAsync(0);

  test("ends the page when the time left runs out, and not before", () => {
    const end = vi.fn();
    watchSession(60, heldRead().read, end);

    vi.advanceTimersByTime(59_999);
    expect(end).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(end).toHaveBeenCalledOnce();
  });

  test("a page shown after the deadline ends at once, without a request", () => {
    const end = vi.fn();
    const { read } = heldRead();
    watchSession(60, read, end);

    // The laptop slept through the deadline, so the timer hasn't fired yet.
    vi.setSystemTime(Date.now() + 61_000);
    show();

    expect(end).toHaveBeenCalledOnce();
    expect(read).not.toHaveBeenCalled();
    vi.runAllTimers();
    expect(end).toHaveBeenCalledOnce();
  });

  test("a page shown before the deadline ends if the server says the session ended", async () => {
    const end = vi.fn();
    const { read, answer } = heldRead();
    watchSession(3600, read, end);

    show();
    expect(read).toHaveBeenCalledOnce();
    answer({ kind: "session_ended" });
    await settle();

    expect(end).toHaveBeenCalledOnce();
  });

  test.each(["focus", "pageshow"])("%s reads the session too", async (name) => {
    const end = vi.fn();
    const { read, answer } = heldRead();
    watchSession(3600, read, end);

    windowEvents.dispatchEvent(new Event(name));
    answer({ kind: "session_ended" });
    await settle();

    expect(end).toHaveBeenCalledOnce();
  });

  test("a session still open moves the deadline to the server's time left", async () => {
    const end = vi.fn();
    const { read, answer } = heldRead();
    watchSession(3600, read, end);

    show();
    answer(ok(30));
    await settle();

    await vi.advanceTimersByTimeAsync(29_999);
    expect(end).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(end).toHaveBeenCalledOnce();
  });

  test("a read that fails keeps the page and its deadline", async () => {
    const end = vi.fn();
    const { read, answer } = heldRead();
    watchSession(60, read, end);

    show();
    answer({ kind: "unavailable" });
    await settle();
    expect(end).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(60_000);
    expect(end).toHaveBeenCalledOnce();
  });

  test("a hidden page reads nothing", () => {
    const { read } = heldRead();
    watchSession(3600, read, vi.fn());

    page.visibilityState = "hidden";
    show();

    expect(read).not.toHaveBeenCalled();
  });

  test("events that fire together send one read", () => {
    const { read } = heldRead();
    watchSession(3600, read, vi.fn());

    show();
    windowEvents.dispatchEvent(new Event("focus"));
    windowEvents.dispatchEvent(new Event("pageshow"));

    expect(read).toHaveBeenCalledOnce();
  });

  test("after stop, nothing ends the page", async () => {
    const end = vi.fn();
    const { read, answer } = heldRead();
    const stop = watchSession(60, read, end);

    show();
    stop();
    answer({ kind: "session_ended" });
    await settle();
    show();
    await vi.advanceTimersByTimeAsync(120_000);

    expect(end).not.toHaveBeenCalled();
    expect(read).toHaveBeenCalledOnce();
  });
});
