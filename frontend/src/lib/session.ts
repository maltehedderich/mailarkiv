// Where the browser goes when a session ends, and where it returns after
// sign-in (FR-040).
//
// The sign-in page says the session ended only if this tab has loaded a
// signed-in page since its last sign-out. A first visit with no session opens
// sign-in without that message. The server's download redirect always sends
// ended=1, because it can't know the tab, so the page checks this tab's flag.

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
export function signInPath(
  current: Pick<URL, "pathname" | "search" | "hash">,
): string {
  const params = new URLSearchParams();
  if (wasSignedIn()) params.set("ended", "1");
  const next = current.pathname + current.search + current.hash;
  if (next !== "/") params.set("next", next);
  const query = params.toString();
  return query === "" ? "/sign-in" : `/sign-in?${query}`;
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
