import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import {
  clearSignedIn,
  markSignedIn,
  safeNext,
  showsSessionEnded,
  signInPath,
} from "./session";

// A session that ends while a page is open sends the next action to sign-in
// with a message, and signing in returns to the same page. A first visit with
// no session shows no such message.

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
