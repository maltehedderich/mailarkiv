// Where the browser goes when a session ends, where it returns after sign-in,
// and how an open page notices that its session ended.
//
// The sign-in page says the session ended only if this tab has loaded a
// signed-in page since its last sign-out. A first visit with no session opens
// sign-in without that message. The server's download redirect always sends
// ended=1, because it can't know the tab, so the page checks this tab's flag.

import type { ApiResult, SessionInfo } from "#lib/api.js";

/** Set in sessionStorage while this tab has a signed-in page open. */
const SIGNED_IN = "mailarkiv:signed-in";

/** The signed-in layout calls this after each successful session read. */
export function markSignedIn(): void {
  try {
    sessionStorage.setItem(SIGNED_IN, "1");
  } catch {
    // Storage is blocked. A later sign-in page then won't say the session
    // ended, which is the only cost.
  }
}

/** Sign-out calls this, so its sign-in page doesn't say the session ended. */
export function clearSignedIn(): void {
  try {
    sessionStorage.removeItem(SIGNED_IN);
  } catch {
    // Storage is blocked, so there is no flag to clear.
  }
}

function wasSignedIn(): boolean {
  try {
    return sessionStorage.getItem(SIGNED_IN) === "1";
  } catch {
    return false;
  }
}

/**
 * The sign-in page for a signed-in page whose session read answered a marked
 * 401. A first visit to `/` gets exactly `/sign-in`.
 */
export function signInPath(current: Pick<URL, "href">): string {
  const params = new URLSearchParams();
  if (wasSignedIn()) params.set("ended", "1");
  // SvelteKit load URLs forbid reading hash. Parse href to preserve the
  // fragment when these client-only pages redirect to sign-in.
  const url = new URL(current.href);
  const next = url.pathname + url.search + url.hash;
  if (next !== "/") params.set("next", next);
  const query = params.toString();
  return query === "" ? "/sign-in" : `/sign-in?${query}`;
}

/**
 * Calls `end` once when the session ends while a signed-in page stays open,
 * so no mail stays on screen after that. Returns a function that stops
 * watching.
 *
 * A timer fires `expiresIn` seconds from now, because a session is never
 * extended. Browsers delay timers in hidden tabs and pause them during sleep,
 * so each time the page is shown it checks again. Past the deadline it ends
 * at once, before any request. Otherwise it reads the session, which catches
 * a sign-out in another tab and a new password.
 */
export function watchSession(
  expiresIn: number,
  readSession: () => Promise<ApiResult<SessionInfo>>,
  end: () => void,
): () => void {
  let deadline = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let reading = false;
  let stopped = false;

  function schedule(seconds: number) {
    clearTimeout(timer);
    deadline = Date.now() + seconds * 1000;
    timer = setTimeout(finish, seconds * 1000);
  }

  function finish() {
    if (stopped) return;
    stop();
    end();
  }

  async function recheck() {
    if (stopped || document.visibilityState !== "visible") return;
    if (Date.now() >= deadline) {
      finish();
      return;
    }
    // Showing a page can fire focus, pageshow, and visibilitychange
    // together, and one read answers for all of them.
    if (reading) return;
    reading = true;
    const session = await readSession();
    reading = false;
    if (stopped) return;
    if (session.kind === "session_ended") finish();
    if (session.kind === "ok") schedule(session.value.expiresIn);
    // When unavailable, the session may still be valid, and the timer still
    // ends the page at the deadline.
  }

  function stop() {
    stopped = true;
    clearTimeout(timer);
    document.removeEventListener("visibilitychange", recheck);
    window.removeEventListener("focus", recheck);
    window.removeEventListener("pageshow", recheck);
  }

  schedule(expiresIn);
  document.addEventListener("visibilitychange", recheck);
  window.addEventListener("focus", recheck);
  window.addEventListener("pageshow", recheck);
  return stop;
}

/** Whether the sign-in page at `url` says the session ended. */
export function showsSessionEnded(url: {
  readonly searchParams: Pick<URLSearchParams, "get">;
}): boolean {
  return url.searchParams.get("ended") === "1" && wasSignedIn();
}

/**
 * The page to open after sign-in. `next` comes from the URL, which anyone can
 * write, so only a page on this origin is accepted. Anything else opens the
 * list.
 */
export function safeNext(next: string | null, origin: string): string {
  if (next === null) return "/";
  let url: URL;
  try {
    url = new URL(next, origin);
  } catch {
    return "/";
  }
  // A path that normalizes to //evil.test names another origin once it's
  // resolved again, so the origin check alone isn't enough.
  if (url.origin !== origin || url.pathname.startsWith("//")) return "/";
  return url.pathname + url.search + url.hash;
}
