<script lang="ts">
  import DownloadIcon from "~icons/lucide/download";

  import type { Message } from "#lib/api.js";
  import { formatSize } from "#lib/format/size.js";
  import { formatTime } from "#lib/format/time.js";

  let { message }: { message: Message } = $props();

  const name = $derived(message.fileName ?? "(no file name)");
</script>

<!-- One row of the import list. The parent <ul> sets the columns. -->
<li>
  <span class="name" class:missing={message.fileName === null}>
    <bdi>{name}</bdi>
  </span>
  <time datetime={message.importedAt}>{formatTime(message.importedAt)}</time>
  <span class="number">{formatSize(message.size)}</span>
  <span class="hash">{message.contentHash.slice(0, 8)}</span>
  <span class="number">
    {message.importCount > 1 ? `${message.importCount}×` : ""}
  </span>
  <!-- A plain link. The server's Content-Disposition makes it a download, and
       the router must not handle it. -->
  <a
    class="button icon-only"
    href="/api/messages/{message.id}/original"
    aria-label="Download {name}"
    data-sveltekit-reload
  >
    <DownloadIcon />
  </a>
</li>

<style>
  li {
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
    overflow: hidden;
    font-weight: var(--weight-medium);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .name.missing {
    color: var(--text-3);
    font-weight: var(--weight-regular);
  }

  time,
  .number {
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  .number {
    text-align: right;
  }

  .hash {
    color: var(--text-3);
    font-family: var(--font-mono);
    font-size: var(--size-label);
    font-variant-ligatures: none;
  }
</style>
