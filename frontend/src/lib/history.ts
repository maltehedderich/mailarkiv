/**
 * The address that opens a message's history. On the list it keeps the page
 * the list is on and adds the message. Anywhere else it opens the list's first
 * page with the history over it.
 */
export function historyHref(current: Pick<URL, "href">, id: number): string {
  const url = new URL(current.href);
  if (url.pathname !== "/") return `/?message=${id}`;
  url.searchParams.set("message", String(id));
  return url.pathname + url.search;
}
