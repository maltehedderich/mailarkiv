import type { MessagePage } from "#lib/api.js";

/**
 * The page an address asks for: 1 with no parameter, or null for a value the
 * server refuses.
 */
export function wantedPage(param: string | null): number | null {
  if (param === null) return 1;
  return /^[1-9]\d*$/.test(param) ? Number(param) : null;
}

/**
 * Where a page past the end, from an old link or a typed address, moves back
 * to: the last page, with every other parameter kept. Page 1 has no
 * parameter, as in the pager's links. Null when the page has rows or the list
 * is empty.
 */
export function pastTheEndHref(
  current: Pick<URL, "href">,
  {
    items,
    total,
    pageSize,
  }: Pick<MessagePage, "total" | "pageSize"> & {
    items: readonly unknown[];
  },
): string | null {
  if (items.length > 0 || total === 0) return null;
  const url = new URL(current.href);
  const last = Math.ceil(total / pageSize);
  if (last === 1) url.searchParams.delete("page");
  else url.searchParams.set("page", String(last));
  return url.pathname + url.search;
}
