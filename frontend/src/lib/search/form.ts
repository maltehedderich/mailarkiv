import type { SearchQuery } from "#lib/api.js";

import { dayRange } from "./dates.js";

/** The search form's fields as typed. Days are `2026-03-29` or empty. */
export interface SearchForm {
  words: string;
  from: string;
  to: string;
  subject: string;
  attachment: string;
  firstDay: string;
  lastDay: string;
  hasAttachments: boolean;
}

/** What a submitted form asks for. */
export type FormReading =
  /** Nothing to search for, so the list shows. */
  | { kind: "list" }
  | { kind: "search"; query: SearchQuery }
  /** A mistake the form can name before any request. */
  | { kind: "invalid"; problem: string };

export function emptyForm(): SearchForm {
  return {
    words: "",
    from: "",
    to: "",
    subject: "",
    attachment: "",
    firstDay: "",
    lastDay: "",
    hasAttachments: false,
  };
}

/** Search splits text at everything but letters and digits. */
function hasTerms(text: string): boolean {
  return /[\p{L}\p{N}]/u.test(text);
}

/** How many filters hold something, for the Filters button. */
export function filterCount(form: SearchForm): number {
  const texts = [form.from, form.to, form.subject, form.attachment];
  const days = [form.firstDay, form.lastDay];
  return (
    texts.filter((text) => text.trim() !== "").length +
    days.filter((day) => day !== "").length +
    (form.hasAttachments ? 1 : 0)
  );
}

/** Reads the form as a search for page, counting from 1. */
export function readForm(form: SearchForm, page = 1): FormReading {
  const texts = [form.from, form.to, form.subject, form.attachment];
  const filtered =
    texts.some(hasTerms) ||
    form.firstDay !== "" ||
    form.lastDay !== "" ||
    form.hasAttachments;
  if (!hasTerms(form.words) && !filtered) return { kind: "list" };
  // Days compare as text in their fixed `yyyy-mm-dd` form.
  if (
    form.firstDay !== "" &&
    form.lastDay !== "" &&
    form.lastDay < form.firstDay
  ) {
    return {
      kind: "invalid",
      problem: "The last day is before the first day.",
    };
  }
  return {
    kind: "search",
    query: {
      words: form.words.trim(),
      from: form.from.trim(),
      to: form.to.trim(),
      subject: form.subject.trim(),
      attachment: form.attachment.trim(),
      ...dayRange(form.firstDay, form.lastDay),
      hasAttachments: form.hasAttachments,
      page,
    },
  };
}
