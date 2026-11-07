// FR-021: a supplied file name is untrusted. It is only ever rendered as text,
// never as markup, a path, or a download name. Control and bidi characters
// become visible codes, so a name can't reorder or hide the text around it.

export type NamePart =
  | { kind: "text"; text: string }
  /** A neutralized character, written as its code point, such as U+202E. */
  | { kind: "code"; text: string };

export type DisplayName =
  | { kind: "missing"; text: "(no file name)" }
  | {
      kind: "name";
      /** The whole name as plain text, with codes in place, for labels. */
      text: string;
      /** Shortened from its end when space runs out. */
      start: NamePart[];
      /** Always shown, so the extension stays visible. Empty for a short name. */
      end: NamePart[];
    };

/** Character clusters kept at the end of a long name. */
const ENDING = 12;

// C0 and C1 controls and DEL, Unicode's Bidi_Control set, and the line and
// paragraph separators, which break a line like a control would.
const NEUTRALIZED = /[\p{Cc}\p{Bidi_Control}\u2028\u2029]/u;

const clusters = new Intl.Segmenter("en", { granularity: "grapheme" });

export function displayName(name: string | null): DisplayName {
  // An empty name, which only a direct request can store, has nothing to show.
  if (name === null || name === "") {
    return { kind: "missing", text: "(no file name)" };
  }
  // Cut between clusters, so no symbol is split across the two halves.
  const segments = Array.from(clusters.segment(name), ({ segment }) => segment);
  const cut =
    segments.length > ENDING ? segments.length - ENDING : segments.length;
  const start = toParts(segments.slice(0, cut));
  const end = toParts(segments.slice(cut));
  const text = [...start, ...end].map((part) => part.text).join("");
  return { kind: "name", text, start, end };
}

function toParts(segments: string[]): NamePart[] {
  const parts: NamePart[] = [];
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
