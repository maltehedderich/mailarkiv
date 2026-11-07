<script lang="ts">
  import DownloadIcon from "~icons/lucide/download";
  import CloseIcon from "~icons/lucide/x";

  import type { ApiResult, MessageHistory, SourceType } from "#lib/api.js";
  import FailedLoad from "#lib/components/FailedLoad.svelte";
  import FileName from "#lib/components/FileName.svelte";
  import FullHash from "#lib/components/FullHash.svelte";
  import { formatBytes } from "#lib/format/size.js";
  import { formatTime } from "#lib/format/time.js";

  let {
    history,
    onclose,
  }: {
    /** Null keeps the drawer closed. */
    history: ApiResult<MessageHistory> | null;
    onclose: () => void;
  } = $props();

  const SOURCES: Record<SourceType, string> = {
    manual_import: "Manual import",
  };

  let dialog: HTMLDialogElement;
  let closeButton: HTMLButtonElement;

  // The address decides whether the drawer is open, so Back and a direct address
  // work. showModal() moves focus in, and close() gives it back. Try again goes
  // away when the data arrives, so focus moves on purpose to stay in the drawer.
  $effect(() => {
    if (history !== null && !dialog.open) dialog.showModal();
    if (history === null && dialog.open) dialog.close();
    if (dialog.open && document.activeElement === document.body) {
      closeButton.focus();
    }
  });
</script>

<dialog bind:this={dialog} aria-labelledby="history-title" {onclose}>
  <header>
    <h2 id="history-title">Import history</h2>
    <button
      bind:this={closeButton}
      class="button ghost icon-only"
      type="button"
      aria-label="Close"
      onclick={() => dialog.close()}
    >
      <CloseIcon />
    </button>
  </header>

  <div class="body">
    {#if history?.kind === "ok"}
      {@const { contentHash, size, observations } = history.value}
      <section>
        <h3>Content hash</h3>
        <FullHash hash={contentHash} />
      </section>

      <section>
        <h3>Size</h3>
        <p class="size">{formatBytes(size)}</p>
      </section>

      <section>
        <h3>Imports</h3>
        <ol>
          {#each observations as observation, index (index)}
            <li>
              <p class="when">
                <time datetime={observation.observedAt}>
                  {formatTime(observation.observedAt)}
                </time>
                <span>{SOURCES[observation.sourceType]}</span>
              </p>
              <p class="name">
                <FileName name={observation.fileName} full />
              </p>
              <p class="account"><bdi>{observation.accountEmail}</bdi></p>
            </li>
          {/each}
        </ol>
      </section>
    {:else if history !== null}
      <FailedLoad />
    {/if}
  </div>

  {#if history?.kind === "ok"}
    <footer>
      <!-- A plain link, as on the row: the server's Content-Disposition makes it
           a download, and the router must not handle it. -->
      <a
        class="button large"
        href="/api/messages/{history.value.id}/original"
        data-sveltekit-reload
      >
        <DownloadIcon />
        Download
      </a>
    </footer>
  {/if}
</dialog>

<style>
  dialog {
    width: min(var(--drawer-width), 100%);
    max-width: none;
    height: 100dvh;
    max-height: none;
    margin: 0 0 0 auto;
    padding: 0;
    border: 0;
    border-radius: var(--radius-large) 0 0 var(--radius-large);
    background: var(--raised);
    box-shadow: var(--shadow-pop);
    color: var(--text-1);
  }

  /* Only when open, or the closed dialog would show. DESIGN.md: it slides in
     from the right. Reduced motion turns this off in global.css. */
  dialog[open] {
    display: grid;
    grid-template-rows: auto minmax(0, 1fr) auto;
    animation: slide-in var(--motion-slide) var(--ease-enter);
  }

  @keyframes slide-in {
    from {
      transform: translateX(100%);
    }
  }

  dialog::backdrop {
    background: var(--scrim);
  }

  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: var(--header-height);
    padding: 0 var(--space-12) 0 var(--space-16);
    border-bottom: 1px solid var(--line);
  }

  h2 {
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
  }

  .body {
    display: grid;
    align-content: start;
    gap: var(--space-20);
    padding: var(--space-16);
    overflow-y: auto;
    overscroll-behavior: contain;
  }

  h3 {
    margin-bottom: var(--space-6);
    color: var(--text-2);
    font-size: var(--size-label);
    font-weight: var(--weight-medium);
    line-height: var(--leading-label);
  }

  .size {
    font-variant-numeric: tabular-nums;
  }

  ol {
    display: grid;
    gap: var(--space-16);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  li {
    position: relative;
    padding-left: var(--space-16);
  }

  /* Dots centred on the first line, joined by a line down to the next one. */
  li::before {
    content: "";
    position: absolute;
    top: calc((var(--leading-label) - var(--dot-size)) / 2);
    left: 0;
    width: var(--dot-size);
    height: var(--dot-size);
    border-radius: 50%;
    background: var(--text-4);
  }

  li:not(:last-child)::after {
    content: "";
    position: absolute;
    top: var(--leading-label);
    bottom: calc(var(--space-16) * -1);
    left: calc((var(--dot-size) - 1px) / 2);
    width: 1px;
    background: var(--line-strong);
  }

  .when {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: 0 var(--space-12);
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    line-height: var(--leading-label);
  }

  /* The history shows a long name in full, so it wraps. */
  .name {
    margin-top: var(--space-2);
    font-weight: var(--weight-medium);
  }

  .account {
    color: var(--text-3);
    font-size: var(--size-label);
    line-height: var(--leading-label);
    overflow-wrap: anywhere;
  }

  footer {
    display: grid;
    padding: var(--space-12) var(--space-16);
    border-top: 1px solid var(--line);
  }

  @media (max-width: 720px) {
    dialog {
      width: 100%;
      border-radius: 0;
    }
  }
</style>
