import { expect, test } from "vitest";

import { wordsSearch } from "./form";

test("a words-only search leaves every filter empty and asks for page 1", () => {
  expect(wordsSearch("quartals müller")).toEqual({
    words: "quartals müller",
    from: "",
    to: "",
    subject: "",
    attachment: "",
    listDateFrom: null,
    listDateBefore: null,
    hasAttachments: false,
    page: 1,
  });
});
