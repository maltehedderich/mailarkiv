<script lang="ts">
  import ChevronRightIcon from "~icons/lucide/chevron-right";
  import FolderIcon from "~icons/lucide/folder";

  import { invalidate } from "$app/navigation";

  import FailedLoad from "#lib/components/FailedLoad.svelte";
  import SourceFacts from "#lib/components/SourceFacts.svelte";
  import SourceState from "#lib/components/SourceState.svelte";
  import UntrustedText from "#lib/components/UntrustedText.svelte";
  import {
    KEY_GUIDE_URL,
    REFRESH_MS,
    SECURITY_WORDS,
    SOURCES_DATA,
  } from "#lib/sources/describe.js";

  import type { PageProps } from "./$types";

  let { data }: PageProps = $props();

  const numbers = new Intl.NumberFormat("en-US");

  const loaded = $derived(
    data.session.kind === "ok" && data.sources.kind === "ok",
  );

  // While a source syncs, reload so its archived count climbs. Each load
  // gives a new result, so the next timer starts only once the last load
  // has finished.
  $effect(() => {
    const result = data.sources;
    if (result.kind !== "ok") return;
    if (!result.value.sources.some((source) => source.state === "syncing")) {
      return;
    }
    const timer = setTimeout(() => void invalidate(SOURCES_DATA), REFRESH_MS);
    return () => clearTimeout(timer);
  });

  // Try again went away with the failed-load block, so focus moves on purpose
  // instead of falling to the page.
  let heading: HTMLHeadingElement;
  let wasLoaded: boolean | null = null;
  $effect(() => {
    if (wasLoaded === false && loaded) {
      if (document.activeElement === document.body) heading.focus();
    }
    wasLoaded = loaded;
  });
</script>

<svelte:head>
  <title>Sources · MailArkiv</title>
</svelte:head>

<main>
  <header>
    <h1 bind:this={heading} tabindex="-1">Sources</h1>
    <!-- With no sources, the empty state carries the button. Without the key
         there is none, and the notice says why. -->
    {#if data.sources.kind === "ok" && data.sources.value.sources.length > 0}
      {@const { collection, sources } = data.sources.value}
      <div class="actions">
        <p class="count">
          {numbers.format(sources.length)}
          {sources.length === 1 ? "source" : "sources"}
        </p>
        {#if collection === "available"}
          <a class="button primary" href="/sources/new">Add a source</a>
        {/if}
      </div>
    {/if}
  </header>

  <div class="body">
    {#if data.session.kind === "ok" && data.sources.kind === "ok"}
      {@const { collection, sources } = data.sources.value}
      {#if collection === "no_key"}
        <div class="notice">
          <p class="what">
            Collection needs the server's key, and the server started without
            one. No source syncs, and none can be added.
          </p>
          <p class="next">
            Start the server with its key file, as the operator guide's
            <a href={KEY_GUIDE_URL}>section on the key</a> explains.
          </p>
        </div>
      {/if}
      {#if sources.length === 0}
        <!-- Without the key, the notice above already says why the list is
             empty and nothing can be added. -->
        {#if collection === "no_key"}
          <h2 class="none">No sources yet</h2>
        {:else}
          <div class="empty">
            <h2>No sources yet</h2>
            <p class="what">
              A source is an IMAP mailbox. MailArkiv first archives every
              message in the folders you choose, then checks for new mail every
              5&nbsp;minutes. It only reads the mailbox and never changes it.
            </p>
            <p class="next">
              You'll need the server's address, a user name, and a password.
            </p>
            <a class="button primary" href="/sources/new">Add a source</a>
          </div>
        {/if}
      {:else}
        <ul class="sources" aria-label="Sources">
          {#each sources as source (source.id)}
            <li>
              <div class="identity">
                <!-- Its ::after stretches over the row, so the whole row
                     opens the source. -->
                <h2 class="name">
                  <a href="/sources/{source.id}"
                    ><UntrustedText value={source.name} /></a
                  >
                </h2>
                <!-- The comma stays on the server's line. -->
                <p class="mailbox">
                  <UntrustedText value={source.userName} /> on
                  <span class="unit"
                    ><UntrustedText value={source.host} />,</span
                  >
                  port {source.port}, {SECURITY_WORDS[source.security]}
                </p>
                <p class="folders">
                  <FolderIcon />
                  <span class="visually-hidden">Folders</span>
                  <!-- A comma stays on its folder's line. The space after it
                       is an expression, so formatting can't drop it. -->
                  <span class="names"
                    >{#each source.folders as folder, index (index)}<span
                        class="unit"
                        ><UntrustedText
                          value={folder}
                        />{#if index < source.folders.length - 1},{/if}</span
                      >{" "}{/each}</span
                  >
                </p>
              </div>
              <div class="evidence">
                <SourceState {source} />
                <SourceFacts {source} problems mailbox={false} />
              </div>
              <ChevronRightIcon />
            </li>
          {/each}
        </ul>
      {/if}
    {:else}
      <FailedLoad />
    {/if}
  </div>
</main>

<style>
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-12);
    height: var(--header-height);
    padding: 0 var(--header-inset);
    border-bottom: 1px solid var(--line);
  }

  h1 {
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
  }

  .actions {
    display: flex;
    align-items: center;
    gap: var(--space-12);
  }

  .count {
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    line-height: var(--leading-label);
  }

  .body {
    max-width: var(--content-width);
    margin: 0 auto;
    padding: var(--space-24);
  }

  /* A box with a transparent edge, so forced colours still draw it. */
  .notice {
    display: grid;
    gap: var(--space-4);
    margin-bottom: var(--space-16);
    padding: var(--space-12) var(--space-16);
    border: 1px solid transparent;
    border-radius: var(--radius-medium);
    background: var(--fill-1);
  }

  .what {
    color: var(--text-2);
  }

  .next {
    color: var(--text-3);
  }

  /* As quiet as the empty Messages list. */
  .none {
    color: var(--text-3);
    font-weight: var(--weight-regular);
  }

  /* Shaped like the Messages drop zone, the other way mail comes in. */
  .empty {
    display: grid;
    justify-items: start;
    gap: var(--space-4);
    padding: var(--space-20) var(--space-24) var(--space-24);
    border: 1px solid var(--line);
    border-radius: var(--radius-large);
    background: var(--fill-1);
  }

  .empty h2 {
    margin-bottom: var(--space-2);
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
  }

  .empty p {
    max-width: 64ch;
    text-wrap: pretty;
  }

  .empty .button {
    margin-top: var(--space-12);
  }

  /* The rows' own padding already clears the header's line. */
  .sources {
    display: grid;
    margin: calc(-1 * var(--space-8)) 0;
    padding: 0;
    list-style: none;
  }

  /* Who the source is beside its state and the evidence, so the states of
     every source sit in one column. The evidence column fits the label
     column and the widest value, a time with "archived 1,204". */
  .sources > li {
    position: relative;
    display: grid;
    grid-template-columns: minmax(0, 1fr) 440px var(--icon-size);
    column-gap: var(--space-16);
    padding: var(--space-12) var(--space-6) var(--space-12) var(--space-10);
    border-radius: var(--radius-medium);
    transition: background-color var(--motion-fade);
  }

  /* DESIGN.md: a fill appears at once and fades out, only where the pointer
     can hover, and for keyboard focus too. */
  @media (hover: hover) {
    .sources > li:hover {
      background: var(--fill-1);
      transition: none;
    }
  }

  .sources > li:has(:focus-visible) {
    background: var(--fill-1);
    transition: none;
  }

  .sources > li:has(.name a:active) {
    background: var(--fill-2);
    transition: none;
  }

  /* A line between sources, inset to the text and hidden beside a lit row,
     whose fill already bounds it. */
  .sources > li + li::before {
    content: "";
    position: absolute;
    top: 0;
    right: var(--space-6);
    left: var(--space-10);
    border-top: 1px solid var(--line);
  }

  .sources > li + li {
    margin-top: 1px;
  }

  .sources > li:has(:focus-visible)::before,
  .sources > li:has(:focus-visible) + li::before {
    border-color: transparent;
  }

  @media (hover: hover) {
    .sources > li:hover::before,
    .sources > li:hover + li::before {
      border-color: transparent;
    }
  }

  .identity {
    display: grid;
    align-content: start;
    min-width: 0;
  }

  .name {
    min-width: 0;
    font-weight: var(--weight-medium);
  }

  .name a {
    display: inline-flex;
    max-width: 100%;
    min-width: 0;
    color: inherit;
    text-decoration: none;
  }

  .name a::after {
    content: "";
    position: absolute;
    inset: 0;
    border-radius: var(--radius-medium);
  }

  .name a:focus-visible {
    outline: none;
  }

  .name a:focus-visible::after {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }

  .mailbox,
  .folders {
    min-width: 0;
    color: var(--text-3);
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  .mailbox {
    margin-top: var(--space-4);
    overflow-wrap: anywhere;
    text-wrap: balance;
  }

  /* The icon hangs beside the first line of names. */
  .folders {
    display: grid;
    grid-template-columns: var(--icon-size-small) minmax(0, 1fr);
    column-gap: var(--space-6);
    margin-top: var(--space-4);
  }

  .folders :global(.icon) {
    width: var(--icon-size-small);
    height: var(--icon-size-small);
    margin-top: 1px;
  }

  /* A name and its comma break together, and a long name still cuts in its
     middle. */
  .unit {
    display: inline-flex;
    max-width: 100%;
    min-width: 0;
    vertical-align: bottom;
  }

  /* Lines sit 4 px apart on both sides, so the mailbox is level with the
     first fact and the folders with the second. */
  .evidence {
    display: grid;
    align-content: start;
    gap: var(--space-4);
    min-width: 0;
  }

  /* Level with the name, and quiet until its row is lit. */
  .sources > li > :global(.icon) {
    margin-top: var(--space-2);
    color: var(--text-4);
  }

  .sources > li:is(:hover, :has(:focus-visible)) > :global(.icon) {
    color: var(--text-2);
  }

  /* Beside the sidebar the two columns have too little room, so a source
     takes one. The state moves up under the name, because it's what a
     reader looks for first. */
  @media (max-width: 1040px) {
    .sources > li {
      grid-template-columns: minmax(0, 1fr) var(--icon-size);
      column-gap: var(--space-12);
    }

    .identity,
    .evidence {
      display: contents;
    }

    .name {
      order: 1;
    }

    .sources > li > :global(.icon) {
      order: 2;
    }

    .evidence > :global(*),
    .mailbox,
    .folders {
      grid-column: 1 / -1;
    }

    .evidence > :global(.state) {
      order: 3;
      margin-top: var(--space-2);
    }

    .mailbox {
      order: 4;
      margin-top: var(--space-8);
    }

    .folders {
      order: 5;
    }

    .evidence > :global(.facts) {
      order: 6;
      margin-top: var(--space-8);
    }
  }

  /* DESIGN.md: 16 px gutters at 720 px and below. */
  @media (max-width: 720px) {
    header {
      padding: 0 var(--space-16);
    }

    .body {
      padding: var(--space-16);
    }

    /* As in the drop zone, the button takes the full width below the text. */
    .empty {
      justify-items: stretch;
      padding: var(--space-16);
    }

    .sources > li {
      padding-inline: var(--space-8) var(--space-4);
    }

    .sources > li + li::before {
      right: 0;
      left: 0;
    }
  }
</style>
