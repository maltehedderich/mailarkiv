<script lang="ts">
  import ArchiveIcon from "~icons/lucide/archive";
  import DownloadIcon from "~icons/lucide/download";
  import PaperclipIcon from "~icons/lucide/paperclip";
  import ProblemsIcon from "~icons/lucide/triangle-alert";

  import { navigating, page } from "$app/state";

  import type { Message } from "#lib/api.js";
  import EntryName from "#lib/components/EntryName.svelte";
  import Sender from "#lib/components/Sender.svelte";
  import UntrustedText from "#lib/components/UntrustedText.svelte";
  import { startDownload } from "#lib/download/start.js";
  import { entryName } from "#lib/format/entry.js";
  import { rowDisplay } from "#lib/format/row.js";
  import { formatSize } from "#lib/format/size.js";
  import { formatTime } from "#lib/format/time.js";

  let { message }: { message: Message } = $props();

  const viewHref = $derived(`/messages/${message.id}`);

  // A message processing has read shows its sender and subject. Any other
  // names itself by its latest entry, beside its state. The label has its
  // codes in place, so it can't be reordered either.
  const shown = $derived(rowDisplay(message));

  // The view opens once its content has arrived, so the row stays marked
  // from the click until then. A history opened over the list, from a toast
  // or Status, marks the row it's about while it shows.
  const open = $derived(
    navigating.to?.url.pathname === viewHref ||
      [page.url, navigating.to?.url].some(
        (url) => url?.searchParams.get("message") === String(message.id),
      ),
  );
</script>

<!-- One row of the message list. The parent <ul> sets the columns. -->
<li class:open>
  {#if shown.kind === "summary"}
    <span class="sender"><Sender shown={shown.sender} /></span>
  {/if}
  <!-- The title is the subject, or the entry's name when processing hasn't
       read the message. It is one element in both, so focus stays on the link
       when processing finishes and the list refreshes. The link's label
       replaces its content, so the marks sit beside it. -->
  <span class="title" class:entry={shown.kind === "capture"}
    ><!-- Its ::after stretches over the row, so the whole row opens the
         message. The ID lets a history closed over the list give focus
         back here. --><a
      id="history-{message.id}"
      class="link"
      href={viewHref}
      aria-label="Open {shown.label}"
      >{#if shown.kind === "capture"}<EntryName
          entry={shown.entry}
        />{:else if shown.subject.kind === "missing"}<span class="missing"
          >{shown.subject.text}</span
        >{:else}<UntrustedText split={shown.subject.subject} />{/if}</a
    >{#if shown.kind === "summary" && message.hasAttachments}<span
        class="attachments"
        ><PaperclipIcon /><span class="visually-hidden">Has attachments</span
        ></span
      >{/if}{#if shown.kind === "summary" && shown.problems !== null}<span
        class="problems"
        title={shown.problems}
        ><ProblemsIcon /><span class="visually-hidden">{shown.problems}</span
        ></span
      >{/if}</span
  >
  {#if shown.kind === "capture"}
    <span class="state"
      ><span class="dot {shown.tone}"></span><span>{shown.state}</span></span
    >
  {/if}
  <!-- The column heads are hidden from screen readers, so values that don't
       name themselves carry their head. The mark keeps its space in every
       row, so the dates line up. -->
  <span class="date"
    ><span
      class="mark"
      title={message.listDateIsArchiveTime ? "Archive time" : undefined}
      >{#if message.listDateIsArchiveTime}<ArchiveIcon />{/if}</span
    ><time datetime={message.listDate}
      ><span class="visually-hidden"
        >{message.listDateIsArchiveTime ? "Archive time " : "Date "}</span
      >{formatTime(message.listDate)}</time
    ></span
  >
  <span class="size">{formatSize(message.size)}</span>
  <!-- The page fetches the file, so a failure shows as a toast here. -->
  <button
    class="button ghost icon-only download"
    type="button"
    aria-label="Download {shown.label}"
    onclick={() =>
      startDownload({
        id: message.id,
        contentHash: message.contentHash,
        fileName: entryName(message.latest),
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

  /* Pressed, waiting for its message, or with its history open. */
  li:has(.link:active),
  li.open {
    background: var(--fill-2);
    transition: none;
  }

  .sender,
  .title {
    display: flex;
    align-items: center;
    gap: var(--space-4);
    min-width: 0;
  }

  /* Placed by column, because a row processing hasn't read has no sender
     cell. */
  .sender {
    grid-column: 1;
  }

  .title,
  .state {
    grid-column: 2;
  }

  .date {
    grid-column: 3;
  }

  .size {
    grid-column: 4;
  }

  .download {
    grid-column: 5;
  }

  /* The entry's name takes the sender's place, and the state the
     subject's. */
  .title.entry {
    grid-column: 1;
  }

  .sender,
  .entry {
    font-weight: var(--weight-medium);
  }

  .link {
    display: flex;
    min-width: 0;
    overflow: hidden;
    color: inherit;
    text-decoration: none;
  }

  .entry .link {
    flex: auto;
  }

  .link::after {
    content: "";
    position: absolute;
    inset: 0;
    border-radius: var(--radius-medium);
  }

  .link:focus-visible {
    outline: none;
  }

  .link:focus-visible::after {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }

  .missing {
    color: var(--text-3);
  }

  .state {
    display: flex;
    align-items: center;
    gap: var(--space-6);
    min-width: 0;
    color: var(--text-3);
    white-space: nowrap;
  }

  .dot {
    flex: none;
    width: var(--dot-size);
    height: var(--dot-size);
    border-radius: 50%;
    background: var(--text-4);
  }

  .dot.warn {
    background: var(--state-warn);
  }

  .attachments,
  .problems,
  .mark {
    display: flex;
    flex: none;
    color: var(--text-3);
  }

  .attachments :global(.icon),
  .problems :global(.icon),
  .mark :global(.icon) {
    width: var(--icon-size-small);
    height: var(--icon-size-small);
  }

  /* Above the link's ::after, so a hover shows its title. */
  .problems,
  .mark {
    position: relative;
    z-index: 1;
  }

  .mark {
    width: var(--icon-size-small);
  }

  .date {
    display: flex;
    align-items: center;
    gap: var(--space-4);
  }

  /* Above the link's ::after, so a click here doesn't open the message.
     Quiet at rest, so a column of icons doesn't compete with the names, and
     raised with its row. Its own hover raises it again, as on any button. */
  .download {
    position: relative;
    z-index: 1;
    color: var(--text-3);
  }

  li:is(:has(:focus-visible), :has(.link:active), .open) .download {
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

  .date,
  .size {
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  .size {
    text-align: right;
  }

  /* DESIGN.md: beside the sidebar, one line leaves the sender too little
     room. The sender and date take the first line, the subject and size the
     second, and the download sits across both. Date and size end on one
     edge. The parent sets the columns. */
  @media (720px < width <= 1040px) {
    li {
      row-gap: var(--space-2);
      padding-block: var(--space-8);
    }

    .sender,
    .title.entry {
      grid-row: 1;
      grid-column: 1;
    }

    .title,
    .state {
      grid-row: 2;
      grid-column: 1;
    }

    .date {
      grid-row: 1;
      grid-column: 2;
      justify-content: flex-end;
    }

    .size {
      grid-row: 2;
      grid-column: 2;
      line-height: var(--leading-label);
    }

    .download {
      grid-row: 1 / 3;
      grid-column: 3;
    }
  }

  /* DESIGN.md: each row stacks as the sender, the subject, then date and
     size, with a line between rows. The download sits beside the two lines
     below the sender. Its fixed column puts every size on one right edge. */
  @media (max-width: 720px) {
    li {
      grid-template-columns: minmax(0, 1fr) auto var(--control-height-touch);
      grid-template-areas:
        "sender sender sender"
        "subject subject download"
        "date size download";
      gap: var(--space-2) var(--space-12);
      padding: var(--space-8) var(--space-4) var(--space-8) var(--space-8);
      border-radius: 0;
    }

    li:not(:last-child) {
      border-bottom: 1px solid var(--line);
    }

    .sender,
    .title.entry {
      grid-area: sender;
    }

    .title,
    .state {
      grid-area: subject;
    }

    .date {
      grid-area: date;
    }

    .size {
      grid-area: size;
    }

    .date,
    .size {
      line-height: var(--leading-label);
    }

    /* A stacked date has no column to line up with, so only a mark takes
       room. */
    .mark:empty {
      display: none;
    }

    .download {
      grid-area: download;
    }

    .link:focus-visible::after {
      border-radius: var(--radius-medium);
    }
  }
</style>
