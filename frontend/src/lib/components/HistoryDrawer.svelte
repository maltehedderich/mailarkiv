<script lang="ts">
  import { untrack } from "svelte";
  import DownloadIcon from "~icons/lucide/download";
  import CloseIcon from "~icons/lucide/x";

  import type { ApiResult, MessageHistory } from "#lib/api.js";
  import EntryName from "#lib/components/EntryName.svelte";
  import FailedLoad from "#lib/components/FailedLoad.svelte";
  import FileName from "#lib/components/FileName.svelte";
  import FullHash from "#lib/components/FullHash.svelte";
  import Toasts from "#lib/components/Toasts.svelte";
  import UntrustedText from "#lib/components/UntrustedText.svelte";
  import { startDownload } from "#lib/download/start.js";
  import { entryName } from "#lib/format/entry.js";
  import { formatBytes, formatSize } from "#lib/format/size.js";
  import { formatTime } from "#lib/format/time.js";
  import { setDrawerOpen } from "#lib/toasts/store.svelte.js";

  let {
    history,
    onclose,
  }: {
    /** Null keeps the drawer closed. */
    history: ApiResult<MessageHistory> | null;
    onclose: () => void;
  } = $props();

  const numbers = new Intl.NumberFormat("en-US");

  let dialog: HTMLDialogElement;
  let closeButton: HTMLButtonElement;

  // The address drops the message as the drawer starts to slide out, so it
  // keeps the last history until it has gone. Otherwise it would leave empty.
  let kept: ApiResult<MessageHistory> | null = null;
  const shown = $derived.by(() => {
    if (history !== null) kept = history;
    return kept;
  });

  /** Counts openings, so each one starts the body afresh. */
  let opened = $state(0);

  // The address decides whether the drawer is open, so Back and a direct address
  // work. showModal() moves focus in, and close() gives it back. Try again goes
  // away when the data arrives, so focus moves on purpose to stay in the drawer.
  $effect(() => {
    if (history !== null && !dialog.open) {
      dialog.inert = false;
      dialog.removeAttribute("aria-hidden");
      dialog.showModal();
      setDrawerOpen(true);
      // Untracked, or the effect would run again for its own write.
      untrack(() => (opened += 1));
    }
    if (history === null && dialog.open) dialog.close();
    if (dialog.open && document.activeElement === document.body) {
      closeButton.focus();
    }
  });

  // Leaving the page with the drawer open fires no close event.
  $effect(() => () => setDrawerOpen(false));

  // Escape, Close, Back, and a click on the scrim all end here. The drawer is
  // still on screen as it slides out, so it leaves screen readers and the
  // pointer now. inert does both in browsers, and aria-hidden says so to tools
  // that only read ARIA. Its empty notifications region was read as a second one.
  function closed() {
    dialog.inert = true;
    dialog.setAttribute("aria-hidden", "true");
    setDrawerOpen(false);
    onclose();
  }

  // A click on the scrim closes the drawer, as on most drawers. The press must
  // start there too, so selecting the hash and letting go outside the drawer
  // keeps it open. Escape is the keyboard's way out.
  let pressedOnScrim = false;

  function pressed(event: PointerEvent) {
    pressedOnScrim = event.target === dialog;
  }

  function clicked(event: MouseEvent) {
    if (pressedOnScrim && event.target === dialog) dialog.close();
    pressedOnScrim = false;
  }

  /**
   * The download's file name, cut in the middle as Finder and the browser's
   * download list show it. The supplied name is never used.
   */
  function savedAs(contentHash: string): string {
    return `${contentHash.slice(0, 8)}…${contentHash.slice(-4)}.eml`;
  }
</script>

<dialog
  bind:this={dialog}
  aria-labelledby="history-title"
  onclose={closed}
  onpointerdown={pressed}
  onclick={clicked}
>
  <header>
    <!-- The newest entry's name, as its row shows it, so the drawer says
         which message it's about where it covers the list. -->
    {#if shown?.kind === "ok"}
      {@const newest = shown.value.observations[0]}
      <h2 id="history-title" class="titled">
        <span class="kind">History</span>
        <span class="visually-hidden">of</span>
        <span class="subject"
          >{#if newest}<EntryName entry={newest} />{:else}<FileName
              name={null}
            />{/if}</span
        >
      </h2>
    {:else}
      <h2 id="history-title">History</h2>
    {/if}
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

  <div class="main">
    {#if shown?.kind === "ok"}
      {@const { id, contentHash, size, observations } = shown.value}
      <!-- A new element for each opening and each message, so the history
           starts at its top, and a copy result doesn't outlive its drawer. A
           refresh of the open message keeps it, and the focus in it. -->
      {#key `${opened}-${id}`}
        <div class="body">
          <dl class="facts">
            <div>
              <dt>Content hash</dt>
              <dd><FullHash hash={contentHash} /></dd>
            </div>
            <div>
              <dt>Size</dt>
              <!-- Exact, as evidence, then as the row shows it. -->
              <dd class="size">
                {formatBytes(size)}
                {#if size >= 1000}
                  <span class="rounded">({formatSize(size)})</span>
                {/if}
              </dd>
            </div>
          </dl>

          <section class="entries" aria-labelledby="entries-title">
            <h3 id="entries-title">
              Entries <span class="count"
                >{numbers.format(observations.length)}</span
              >
            </h3>
            <ol>
              {#each observations as observation, index (index)}
                <li>
                  <time class="when" datetime={observation.observedAt}>
                    {formatTime(observation.observedAt)}
                  </time>
                  {#if observation.sourceType === "manual_import"}
                    <p class="name">
                      <FileName name={observation.fileName} full />
                    </p>
                    <p class="by">
                      Manual import by
                      <bdi>{observation.accountEmail}</bdi>
                    </p>
                  {:else}
                    <p class="name">
                      <UntrustedText value={observation.folder} full />
                    </p>
                    <!-- The time above is when MailArkiv collected it. A
                         removed source's entries read as they did before. -->
                    <dl class="collection">
                      <dt>Collected from</dt>
                      <dd>
                        <UntrustedText value={observation.source.name} full />
                      </dd>
                      <dt>Server</dt>
                      <dd><bdi>{observation.source.host}</bdi></dd>
                      <dt>User name</dt>
                      <dd>
                        <UntrustedText
                          value={observation.source.userName}
                          full
                        />
                      </dd>
                      <dt>Received</dt>
                      <dd>
                        {#if observation.receivedAt === null}
                          Unknown
                        {:else}
                          <time datetime={observation.receivedAt}
                            >{formatTime(observation.receivedAt)}</time
                          >
                        {/if}
                      </dd>
                    </dl>
                  {/if}
                </li>
              {/each}
            </ol>
          </section>
        </div>
      {/key}
    {:else if shown !== null}
      <div class="body">
        <FailedLoad />
      </div>
    {/if}
    <!-- The page's toasts are inert behind this modal, so the drawer has its
         own. -->
    <Toasts place="drawer" />
  </div>

  {#if shown?.kind === "ok"}
    {@const { id, contentHash, observations } = shown.value}
    <footer>
      <!-- The supplied name is never the download's name, so a file named
           after its hash isn't a surprise. -->
      <p class="saved-as">
        Saves as <span class="file">{savedAs(contentHash)}</span>
      </p>
      <!-- The newest entry's name, for the toasts, as the row shows it. -->
      <button
        class="button large"
        type="button"
        onclick={() =>
          startDownload({
            id,
            contentHash,
            fileName: observations[0] ? entryName(observations[0]) : null,
          })}
      >
        <DownloadIcon />
        Download
      </button>
    </footer>
  {/if}
</dialog>

<style>
  /* Fixed to the right edge, with its rows, in every state, so it keeps its
     place and its layout while it slides out after closing. Its rows were on
     [open] alone, and the body sank to the middle as it left. */
  dialog {
    position: fixed;
    inset: 0 0 0 auto;
    grid-template-rows: auto minmax(0, 1fr) auto;
    width: min(var(--drawer-width), 100%);
    max-width: none;
    height: 100dvh;
    max-height: none;
    margin: 0;
    padding: 0;
    border: 0;
    border-radius: var(--radius-large) 0 0 var(--radius-large);
    background: var(--raised);
    box-shadow: var(--shadow-pop);
    color: var(--text-1);
    translate: 100% 0;
    transition:
      translate var(--motion-exit) var(--ease-exit),
      display var(--motion-exit) allow-discrete,
      overlay var(--motion-exit) allow-discrete;
  }

  /* DESIGN.md: it slides in from the right, and out a little faster.
     display and overlay hold it on screen until it has left. Reduced motion
     turns this off in global.css. */
  dialog[open] {
    display: grid;
    translate: 0 0;
    transition:
      translate var(--motion-slide) var(--ease-enter),
      display var(--motion-slide) allow-discrete,
      overlay var(--motion-slide) allow-discrete;
  }

  @starting-style {
    dialog[open] {
      translate: 100% 0;
    }
  }

  /* The scrim fades with the drawer, rather than dimming the page at once. */
  dialog::backdrop {
    background: var(--scrim);
    opacity: 0;
    transition:
      opacity var(--motion-exit) var(--ease-exit),
      display var(--motion-exit) allow-discrete,
      overlay var(--motion-exit) allow-discrete;
  }

  dialog[open]::backdrop {
    opacity: 1;
    transition:
      opacity var(--motion-slide) var(--ease-enter),
      display var(--motion-slide) allow-discrete,
      overlay var(--motion-slide) allow-discrete;
  }

  @starting-style {
    dialog[open]::backdrop {
      opacity: 0;
    }
  }

  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-12);
    height: var(--header-height);
    padding: 0 var(--space-12) 0 var(--space-16);
    border-bottom: 1px solid var(--line);
  }

  h2 {
    min-width: 0;
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
  }

  /* With a message: what the drawer shows, small, over which message. The
     name keeps one line, cut in the middle as in its row, and the history
     below shows it in full. */
  .titled {
    display: grid;
  }

  .kind {
    color: var(--text-3);
    font-size: var(--size-label);
    font-weight: var(--weight-medium);
    letter-spacing: normal;
    line-height: var(--leading-label);
  }

  .subject {
    display: flex;
    min-width: 0;
    overflow: hidden;
  }

  /* Holds the scrolling body and the toasts over its end. */
  .main {
    position: relative;
    display: grid;
    min-height: 0;
  }

  /* --toast-room is set by the toasts, so their stack never hides the end of
     the history or a focused control in it. */
  .body {
    display: grid;
    align-content: start;
    gap: var(--space-20);
    padding: var(--space-16);
    padding-bottom: calc(var(--space-16) + var(--toast-room, 0px));
    overflow-y: auto;
    overscroll-behavior: contain;
    scroll-padding-bottom: var(--toast-room, 0px);
  }

  /* The message's facts: a label column beside the values, as on Status. */
  .facts {
    display: grid;
    gap: var(--space-12);
    margin: 0;
  }

  .facts > div {
    display: grid;
    grid-template-columns: 96px minmax(0, 1fr);
    align-items: baseline;
    column-gap: var(--space-16);
  }

  dt,
  h3 {
    color: var(--text-2);
    font-size: var(--size-label);
    font-weight: var(--weight-medium);
    line-height: var(--leading-label);
  }

  dd {
    min-width: 0;
    margin: 0;
  }

  .size {
    font-variant-numeric: tabular-nums;
  }

  .rounded {
    color: var(--text-3);
  }

  /* The history, below a line, so it reads apart from the facts. */
  .entries {
    padding-top: var(--space-20);
    border-top: 1px solid var(--line);
  }

  h3 {
    display: flex;
    gap: var(--space-6);
    margin-bottom: var(--space-12);
  }

  .count {
    color: var(--text-3);
    font-weight: var(--weight-regular);
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

  .when,
  .by {
    display: block;
    color: var(--text-3);
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  .when {
    font-variant-numeric: tabular-nums;
  }

  /* The history shows a long name in full, so it wraps. */
  .name {
    margin: var(--space-2) 0;
    font-weight: var(--weight-medium);
  }

  /* An address that doesn't fit beside "Manual import by" takes the next line
     whole, rather than splitting at its hyphen. One wider than the drawer
     still wraps. */
  .by bdi {
    display: inline-block;
    max-width: 100%;
    overflow-wrap: anywhere;
  }

  /* A collection's source, server, user name, and received time, in a label
     column under the folder. */
  .collection {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    column-gap: var(--space-12);
    margin: 0;
    color: var(--text-3);
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  .collection dt {
    color: var(--text-3);
    font-weight: var(--weight-regular);
  }

  .collection dd {
    color: var(--text-2);
    font-variant-numeric: tabular-nums;
    overflow-wrap: anywhere;
  }

  /* The note gives way to the button: it wraps, and the button keeps its
     size. */
  footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-12);
    padding: var(--space-12) var(--space-16);
    border-top: 1px solid var(--line);
  }

  .saved-as {
    min-width: 0;
    color: var(--text-3);
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  .file {
    font-family: var(--font-mono);
    font-variant-ligatures: none;
    white-space: nowrap;
  }

  footer .button {
    flex: none;
  }

  /* DESIGN.md: drawers fill the width, and each label moves above its
     value. */
  @media (max-width: 720px) {
    dialog {
      width: 100%;
      border-radius: 0;
    }

    .facts > div {
      grid-template-columns: minmax(0, 1fr);
      row-gap: var(--space-6);
    }
  }
</style>
