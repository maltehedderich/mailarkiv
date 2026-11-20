import { describe, expect, test } from "vitest";

import { senderDisplay } from "./sender";

// A sender is untrusted text in two parts. The name may hold "@" and "<", so
// the parts stay apart, and the address keeps its domain in view.

describe("senderDisplay", () => {
  test("a name and an address are separate parts", () => {
    expect(
      senderDisplay({
        name: "Anna Schmidt",
        address: "anna@example.com",
        more: 0,
      }),
    ).toEqual({
      kind: "sender",
      name: {
        text: "Anna Schmidt",
        start: [{ kind: "text", text: "Anna Schmidt" }],
        end: [],
      },
      address: {
        text: "anna@example.com",
        start: [{ kind: "text", text: "anna" }],
        end: [{ kind: "text", text: "@example.com" }],
      },
    });
  });

  // A quoted local part can hold "@", and the domain can't.
  test("the address splits at its last @", () => {
    const shown = senderDisplay({
      name: null,
      address: '"ceo@example.com"@attacker.example',
      more: 0,
    });
    expect(shown.kind === "sender" && shown.address).toMatchObject({
      start: [{ kind: "text", text: '"ceo@example.com"' }],
      end: [{ kind: "text", text: "@attacker.example" }],
    });
  });

  test("an address with no @ shortens at its end", () => {
    const shown = senderDisplay({ name: null, address: "postmaster", more: 0 });
    expect(shown.kind === "sender" && shown.address).toEqual({
      text: "postmaster",
      start: [{ kind: "text", text: "postmaster" }],
      end: [],
    });
  });

  test("no name shows the address once", () => {
    const shown = senderDisplay({
      name: null,
      address: "anna@example.com",
      more: 0,
    });
    expect(shown).toMatchObject({ kind: "sender", name: null });
    expect(shown.kind === "sender" && shown.address?.text).toBe(
      "anna@example.com",
    );
  });

  test("a name equal to the address, ignoring case, shows the address once", () => {
    const shown = senderDisplay({
      name: "Anna@Example.com",
      address: "anna@example.com",
      more: 0,
    });
    expect(shown).toMatchObject({ kind: "sender", name: null });
  });

  // From that isn't a list of addresses keeps its decoded text as the name.
  test("a name without an address shows the name", () => {
    expect(
      senderDisplay({ name: "Undisclosed recipients", address: null, more: 0 }),
    ).toMatchObject({
      kind: "sender",
      name: { text: "Undisclosed recipients" },
      address: null,
    });
  });

  test("control and bidirectional characters become badges", () => {
    const shown = senderDisplay({
      name: "Anna‮evil",
      address: "anna\u0007@exa⁦mple.com",
      more: 0,
    });
    expect(shown).toEqual({
      kind: "sender",
      name: {
        text: "AnnaU+202Eevil",
        start: [
          { kind: "text", text: "Anna" },
          { kind: "code", text: "U+202E" },
          { kind: "text", text: "evil" },
        ],
        end: [],
      },
      address: {
        text: "annaU+0007@exaU+2066mple.com",
        start: [
          { kind: "text", text: "anna" },
          { kind: "code", text: "U+0007" },
        ],
        end: [
          { kind: "text", text: "@exa" },
          { kind: "code", text: "U+2066" },
          { kind: "text", text: "mple.com" },
        ],
      },
    });
  });

  test("no sender reads (no sender)", () => {
    expect(senderDisplay(null)).toEqual({
      kind: "missing",
      text: "(no sender)",
    });
  });
});
