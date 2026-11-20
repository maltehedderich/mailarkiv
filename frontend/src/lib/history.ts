/** The lists a history opens over. */
const LISTS = new Set(["/", "/problems"]);

/**
 * The address that opens a message's history. On a list it keeps the page
 * the list is on and adds the message. Anywhere else it opens the message
 * list's first page with the history over it.
 */
export function historyHref(current: Pick<URL, "href">, id: number): string {
  const url = new URL(current.href);
  if (!LISTS.has(url.pathname)) return `/?message=${id}`;
  url.searchParams.set("message", String(id));
  return url.pathname + url.search;
}
