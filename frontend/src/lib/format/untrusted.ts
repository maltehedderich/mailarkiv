// Text from outside, such as a file name, a folder name, or a server's reply,
// is untrusted. It is only ever rendered as text, never as markup, a path, or
// a download name. Control and bidi characters become visible codes, so the
// text can't reorder or hide what is around it.

export type TextPart =
  | { kind: "text"; text: string }
  /** A neutralized character, written as its code point, such as U+202E. */
  | { kind: "code"; text: string };

export interface DisplayText {
  /** The whole text with codes in place, for labels. */
  text: string;
  /** Shortened from its end when space runs out. */
  start: TextPart[];
  /** Always shown, so a file name's extension stays visible. Empty for short text. */
  end: TextPart[];
}

/** Character clusters kept at the end of long text. */
const ENDING = 12;
/** The most clusters the ending grows to, so a cut can avoid reordering. */
const LONGEST_ENDING = 24;

// C0 and C1 controls and DEL, Unicode's Bidi_Control set, and the line and
// paragraph separators, which break a line like a control would.
const NEUTRALIZED = /[\p{Cc}\p{Bidi_Control}\u2028\u2029]/u;

// The scripts written right to left, which Unicode gives bidi class R or AL.
const RIGHT_TO_LEFT =
  /[\p{Script=Hebrew}\p{Script=Arabic}\p{Script=Syriac}\p{Script=Thaana}\p{Script=Nko}\p{Script=Samaritan}\p{Script=Mandaic}\p{Script=Adlam}]/u;

const clusters = new Intl.Segmenter("en", { granularity: "grapheme" });

export function displayText(value: string): DisplayText {
  // Cut between clusters, so no symbol is split across the two halves.
  const segments = Array.from(
    clusters.segment(value),
    ({ segment }) => segment,
  );
  const cut =
    segments.length > ENDING
      ? orderedCut(segments, segments.length - ENDING)
      : segments.length;
  const start = toParts(segments.slice(0, cut));
  const end = toParts(segments.slice(cut));
  const text = [...start, ...end].map((part) => part.text).join("");
  return { text, start, end };
}

function toParts(segments: string[]): TextPart[] {
  const parts: TextPart[] = [];
  let text = "";
  for (const segment of segments) {
    // A cluster can hold more than one control, such as CR LF.
    for (const character of segment) {
      if (!NEUTRALIZED.test(character)) {
        text += character;
        continue;
      }
      if (text !== "") parts.push({ kind: "text", text });
      text = "";
      parts.push({ kind: "code", text: codePoint(character) });
    }
  }
  if (text !== "") parts.push({ kind: "text", text });
  return parts;
}

function codePoint(character: string): string {
  // A neutralized character is never empty.
  const value = character.codePointAt(0) as number;
  return `U+${value.toString(16).toUpperCase().padStart(4, "0")}`;
}

type Kind = "ltr" | "rtl" | "number" | "neutral";
type Flow = "ltr" | "rtl" | "neutral";

/**
 * Moves the cut out of any run that reads against the text's direction.
 *
 * The two halves are laid out apart, each in the text's direction, so a cut
 * inside such a run draws the run's halves in swapped order. The ending of
 * "חשבונית לחודש נובמבר 2026 - סופי.eml" started inside "2026", and the row
 * read "6202" with room for the whole name. The nearest cut outside a run
 * wins, and the longer ending wins a tie. Text with no such cut keeps the
 * first one.
 */
function orderedCut(segments: string[], cut: number): number {
  const kinds = segments.map(kind);
  // The direction <bdi> takes: its first letter's, or left to right.
  const base = kinds.find((each) => each === "ltr" || each === "rtl") ?? "ltr";
  const flows = runFlows(kinds, base);
  for (let distance = 0; distance < segments.length; distance++) {
    for (const candidate of [cut - distance, cut + distance]) {
      const ending = segments.length - candidate;
      if (candidate < 1 || ending < 1 || ending > LONGEST_ENDING) continue;
      if (keepsOrder(flows, base, candidate)) return candidate;
    }
  }
  return cut;
}

/** A code, such as U+202E, is drawn in Latin letters. */
function kind(segment: string): Kind {
  if (Array.from(segment).some((character) => NEUTRALIZED.test(character))) {
    return "ltr";
  }
  // Before the scripts, because Arabic-Indic digits are in the Arabic script.
  if (/^\p{Nd}/u.test(segment)) return "number";
  if (RIGHT_TO_LEFT.test(segment)) return "rtl";
  if (/^\p{L}/u.test(segment)) return "ltr";
  return "neutral";
}

/**
 * The direction each cluster reads in, as the Unicode bidi rules order it.
 * Digits read left to right in right-to-left text. In left-to-right text,
 * digits after right-to-left letters join their run.
 */
function runFlows(kinds: Kind[], base: "ltr" | "rtl"): Flow[] {
  let strong = base;
  return kinds.map((each) => {
    if (each === "ltr" || each === "rtl") strong = each;
    if (each !== "number") return each;
    return base === "rtl" ? "ltr" : strong;
  });
}

/**
 * A cut keeps the order when the letters on either side of it don't both
 * belong to a run against the text's direction.
 */
function keepsOrder(flows: Flow[], base: Flow, cut: number): boolean {
  const before = flows.slice(0, cut).findLast((flow) => flow !== "neutral");
  const after = flows.slice(cut).find((flow) => flow !== "neutral");
  if (before === undefined || after === undefined) return true;
  return before === base || after === base;
}
