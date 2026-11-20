import { expect, test, type Locator } from "@playwright/test";

import {
  collectMalformedSet,
  rowOf,
  SOURCE,
  startMalformedSet,
  type MalformedSet,
} from "./support/malformed";
import { expectNoSidewaysScroll } from "./support/sources";

// Rows show what processing read, as untrusted text that can't pass for
// something else, and each processing state shows its mark.

let set: MalformedSet;

test.beforeEach(async () => {
  set = await startMalformedSet();
});

test.afterEach(async () => {
  await set.stop();
});

/** Fails unless the element shows whole: in the viewport and not clipped. */
async function expectWhole(element: Locator) {
  await expect(element).toBeInViewport({ ratio: 1 });
}

for (const viewport of [
  { width: 1280, height: 800 },
  { width: 360, height: 780 },
]) {
  test.describe(`at ${viewport.width} px`, () => {
    test.use({ viewport });

    test("rows keep senders apart, neutralize codes, and mark each state", async ({
      page,
    }) => {
      const list = await collectMalformedSet(page, set);

      // A name that looks like an address sits before the real one, whose
      // domain shows whole.
      const spoofed = rowOf(list, "Dringende Ueberweisung");
      const name = spoofed.getByText("ceo@example.com", { exact: true });
      const domain = spoofed.getByText("@attacker.example", { exact: true });
      await spoofed.scrollIntoViewIfNeeded();
      await expectWhole(name);
      await expectWhole(domain);
      const nameBox = await name.boundingBox();
      const domainBox = await domain.boundingBox();
      expect(nameBox).not.toBeNull();
      expect(domainBox).not.toBeNull();
      if (nameBox !== null && domainBox !== null) {
        expect(nameBox.x + nameBox.width).toBeLessThanOrEqual(domainBox.x);
        expect(Math.abs(nameBox.y - domainBox.y)).toBeLessThan(4);
      }

      // A 500-character name is cut, and the domain isn't.
      const long = rowOf(list, "Langer Name");
      await long.scrollIntoViewIfNeeded();
      await expectWhole(long.getByText("@example.com", { exact: true }));

      // U+202E and U+0000 show as codes, in the name and the subject.
      const controls = rowOf(list, "RechnungU+202Efdp.exeU+0000");
      await controls.scrollIntoViewIfNeeded();
      await expect(
        controls.getByText("U+202E", { exact: true }).first(),
      ).toBeVisible();
      await expect(controls).not.toContainText("‮");

      // Markup in a subject is text.
      await expect(
        rowOf(list, "<b>Fett</b><script>alert(1)</script>"),
      ).toBeVisible();

      // A message read whole has no mark, one read in part has the problem
      // mark, and one that couldn't be read names itself by its entry beside
      // its state.
      await expect(spoofed.getByTitle("Processed with problems")).toHaveCount(
        0,
      );
      const problems = rowOf(list, "Bericht mit kaputtem Anhang");
      await problems.scrollIntoViewIfNeeded();
      await expect(
        problems.getByTitle("Processed with problems"),
      ).toBeVisible();
      const unreadable = rowOf(list, `${SOURCE}, INBOX`);
      await unreadable.scrollIntoViewIfNeeded();
      await expectWhole(
        unreadable.getByText("Couldn't be read", { exact: true }),
      );
      await expect(unreadable).not.toContainText("(no sender)");
      await expect(unreadable).not.toContainText("(no subject)");

      // Placeholders show only where processing read the message.
      const bare = rowOf(list, "(no subject)");
      await expect(
        bare.getByText("(no sender)", { exact: true }),
      ).toBeVisible();

      await expectNoSidewaysScroll(page, "Messages");
    });
  });
}
