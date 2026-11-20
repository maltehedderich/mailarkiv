import type { SearchQuery } from "#lib/api.js";

/** A search for words alone, from the first page. */
export function wordsSearch(words: string): SearchQuery {
  return {
    words,
    from: "",
    to: "",
    subject: "",
    attachment: "",
    listDateFrom: null,
    listDateBefore: null,
    hasAttachments: false,
    page: 1,
  };
}
