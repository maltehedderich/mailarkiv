<script lang="ts">
  import DownloadIcon from "~icons/lucide/download";

  import { goto } from "$app/navigation";

  import type { Message } from "#lib/api.js";
  import FileName from "#lib/components/FileName.svelte";
  import { displayName } from "#lib/format/file-name.js";
  import { formatSize } from "#lib/format/size.js";
  import { formatTime } from "#lib/format/time.js";

  let { message, historyHref }: { message: Message; historyHref: string } =
    $props();

  // The name with its codes in place, so a label can't be reordered either.
  const name = $derived(displayName(message.fileName).text);

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

<!-- One row of the import list. The parent <ul> sets the columns. -->
<li>
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
    <FileName name={message.fileName} />
  </a>
  <time class="time" datetime={message.importedAt}
    >{formatTime(message.importedAt)}</time
  >
  <span class="size">{formatSize(message.size)}</span>
  <span class="hash">{message.contentHash.slice(0, 8)}</span>
  <span class="count">
    {message.importCount > 1 ? `${message.importCount}×` : ""}
  </span>
  <!-- A plain link. The server's Content-Disposition makes it a download, and
       the router must not handle it. -->
  <a
    class="button icon-only download"
    href="/api/messages/{message.id}/original"
    aria-label="Download {name}"
    data-sveltekit-reload
  >
    <DownloadIcon />
  </a>
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

  li:hover {
    background: var(--fill-1);
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

  /* Above the name's ::after, so a click here doesn't open the history. */
  .download {
    position: relative;
    z-index: 1;
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

  /* DESIGN.md: each row stacks as name and count, then time and size, then
     hash and download, with a line between rows. */
  @media (max-width: 720px) {
    li {
      grid-template-columns: minmax(0, 1fr) auto;
      grid-template-areas:
        "name count"
        "time size"
        "hash download";
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

    .download {
      grid-area: download;
      justify-self: end;
    }

    .name:focus-visible::after {
      border-radius: var(--radius-medium);
    }
  }
</style>
