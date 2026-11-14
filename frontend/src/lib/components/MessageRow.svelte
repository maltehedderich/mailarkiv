<script lang="ts">
  import DownloadIcon from "~icons/lucide/download";

  import { goto } from "$app/navigation";
  import { navigating, page } from "$app/state";

  import type { Message } from "#lib/api.js";
  import EntryName from "#lib/components/EntryName.svelte";
  import { startDownload } from "#lib/download/start.js";
  import { entryName } from "#lib/format/entry.js";
  import { displayName } from "#lib/format/file-name.js";
  import { formatSize } from "#lib/format/size.js";
  import { formatTime } from "#lib/format/time.js";

  let { message, historyHref }: { message: Message; historyHref: string } =
    $props();

  const numbers = new Intl.NumberFormat("en-US");

  // The row names the message by its latest entry. The name has its codes in
  // place, so a label can't be reordered either.
  const shownName = $derived(entryName(message.latest));
  const name = $derived(displayName(shownName).text);

  // The drawer opens once its history has arrived, so the row stays marked
  // from the click until then, and while the drawer is open. Beside the drawer,
  // it shows which row the drawer is about.
  const open = $derived(
    [page.url, navigating.to?.url].some(
      (url) => url?.searchParams.get("message") === String(message.id),
    ),
  );

  // DESIGN.md: the key press must end here. Otherwise it could reach Close once
  // the drawer has focus. With a modifier, the browser opens a new tab or window.
  function openOnEnter(event: KeyboardEvent) {
    if (event.key !== "Enter") return;
    if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey)
      return;
    event.preventDefault();
    void goto(historyHref, { reset: false });
  }
</script>

<!-- One row of the message list. The parent <ul> sets the columns. -->
<li class:open>
  <!-- Its ::after stretches over the row, so the whole row opens the history.
       Keeping focus and scroll lets the dialog give focus back to this link. -->
  <a
    id="history-{message.id}"
    class="name"
    href={historyHref}
    aria-label="History of {name}"
    data-sveltekit-reset="false"
    onkeydown={openOnEnter}
  >
    <EntryName entry={message.latest} />
  </a>
  <!-- The column heads are hidden from screen readers, so values that don't
       name themselves carry their head. -->
  <time class="time" datetime={message.observedAt}
    ><span class="visually-hidden"
      >{message.latest.sourceType === "manual_import"
        ? "Imported"
        : "Collected"}
    </span>{formatTime(message.observedAt)}</time
  >
  <span class="size">{formatSize(message.size)}</span>
  <span class="hash"
    ><span class="visually-hidden">Hash </span>{message.contentHash.slice(
      0,
      8,
    )}</span
  >
  <span class="count">
    {#if message.observationCount > 1}
      {@const count = numbers.format(message.observationCount)}
      <span aria-hidden="true">{count}×</span>
      <span class="visually-hidden">{count} entries</span>
    {/if}
  </span>
  <!-- The page fetches the file, so a failure shows as a toast here. -->
  <button
    class="button ghost icon-only download"
    type="button"
    aria-label="Download {name}"
    onclick={() =>
      startDownload({
        id: message.id,
        contentHash: message.contentHash,
        fileName: shownName,
      })}
  >
    <DownloadIcon />
  </button>
</li>

<style>
  li {
    position: relative;
    display: grid;
    grid-template-columns: subgrid;
    grid-column: 1 / -1;
    align-items: center;
    min-height: var(--row-height);
    padding: 0 var(--space-4) 0 var(--space-10);
    border-radius: var(--radius-medium);
    transition: background-color var(--motion-fade);
  }

  /* DESIGN.md: a fill appears at once and fades out. Only a pointer that can
     hover lights the row, so a tap doesn't leave it lit on a touch screen.
     Keyboard focus on the row or its download lights it the same way. */
  @media (hover: hover) {
    li:hover {
      background: var(--fill-1);
      transition: none;
    }
  }

  li:has(:focus-visible) {
    background: var(--fill-1);
    transition: none;
  }

  /* Pressed, waiting for its history, or with its history open. */
  li:has(.name:active),
  li.open {
    background: var(--fill-2);
    transition: none;
  }

  .name {
    display: flex;
    min-width: 0;
    overflow: hidden;
    color: inherit;
    font-weight: var(--weight-medium);
    text-decoration: none;
  }

  .name::after {
    content: "";
    position: absolute;
    inset: 0;
    border-radius: var(--radius-medium);
  }

  .name:focus-visible {
    outline: none;
  }

  .name:focus-visible::after {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }

  /* Above the name's ::after, so a click here doesn't open the history.
     Quiet at rest, so a column of icons doesn't compete with the names, and
     raised with its row. Its own hover raises it again, as on any button. */
  .download {
    position: relative;
    z-index: 1;
    color: var(--text-3);
  }

  li:is(:has(:focus-visible), :has(.name:active), .open) .download {
    color: var(--text-2);
  }

  @media (hover: hover) {
    li:hover .download {
      color: var(--text-2);
    }

    li .download:hover {
      color: var(--text-1);
    }
  }

  .time,
  .size,
  .count {
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  .size,
  .count {
    text-align: right;
  }

  .hash {
    color: var(--text-3);
    font-family: var(--font-mono);
    font-size: var(--size-label);
    font-variant-ligatures: none;
  }

  /* DESIGN.md: beside the sidebar, one line leaves the name too little room,
     so it takes the first line. Time, size, and hash keep their columns on
     the second, and the count and download sit across both. The parent sets
     the columns. */
  @media (720px < width <= 1040px) {
    li {
      row-gap: var(--space-2);
      padding-block: var(--space-8);
    }

    .name {
      grid-row: 1;
      grid-column: 1 / 4;
    }

    .time,
    .size,
    .hash {
      grid-row: 2;
      line-height: var(--leading-label);
    }

    .time {
      grid-column: 1;
    }

    .size {
      grid-column: 2;
    }

    .hash {
      grid-column: 3;
    }

    .count {
      grid-row: 1 / 3;
      grid-column: 4;
    }

    .download {
      grid-row: 1 / 3;
      grid-column: 5;
    }
  }

  /* DESIGN.md: each row stacks as the name, then time and size, then hash
     and count, with a line between rows. The name keeps the whole width, and
     the download sits beside the two lines below it. On a line of its own it
     made each row 97 px tall with its divider, and now rows are 75. Its fixed
     column puts every size and count on one right edge. */
  @media (max-width: 720px) {
    li {
      grid-template-columns: minmax(0, 1fr) auto var(--control-height-touch);
      grid-template-areas:
        "name name name"
        "time size download"
        "hash count download";
      gap: var(--space-2) var(--space-12);
      padding: var(--space-8) var(--space-4) var(--space-8) var(--space-8);
      border-radius: 0;
    }

    li:not(:last-child) {
      border-bottom: 1px solid var(--line);
    }

    .name {
      grid-area: name;
    }

    .count {
      grid-area: count;
    }

    .time {
      grid-area: time;
    }

    .size {
      grid-area: size;
    }

    .hash {
      grid-area: hash;
    }

    .time,
    .size,
    .hash,
    .count {
      line-height: var(--leading-label);
    }

    .download {
      grid-area: download;
    }

    .name:focus-visible::after {
      border-radius: var(--radius-medium);
    }
  }
</style>
