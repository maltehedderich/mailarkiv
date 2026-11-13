<script lang="ts">
  import { invalidate } from "$app/navigation";

  import FailedLoad from "#lib/components/FailedLoad.svelte";
  import SourceFacts from "#lib/components/SourceFacts.svelte";
  import SourceState from "#lib/components/SourceState.svelte";
  import UntrustedText from "#lib/components/UntrustedText.svelte";
  import {
    KEY_GUIDE_URL,
    REFRESH_MS,
    SOURCES_DATA,
  } from "#lib/sources/describe.js";

  import type { PageProps } from "./$types";

  let { data }: PageProps = $props();

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
    <!-- Left out without the key, which the notice explains. -->
    {#if data.sources.kind === "ok" && data.sources.value.collection === "available"}
      <a class="button primary" href="/sources/new">Add a source</a>
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
        <p class="empty">No sources.</p>
      {:else}
        <ul class="sources">
          {#each sources as source (source.id)}
            <li>
              <h2 class="name">
                <a href="/sources/{source.id}"
                  ><UntrustedText value={source.name} /></a
                >
              </h2>
              <SourceState {source} />
              <SourceFacts {source} folders={source.folders} problems />
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
    padding: 0 var(--space-24);
    border-bottom: 1px solid var(--line);
  }

  h1 {
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
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

  .empty {
    color: var(--text-1);
  }

  .sources {
    display: grid;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .sources > li {
    display: grid;
    gap: var(--space-4);
    padding: var(--space-16) 0;
  }

  /* The view header's line already sits above the first source. */
  .sources > li:first-child {
    padding-top: 0;
  }

  .sources > li + li {
    border-top: 1px solid var(--line);
  }

  .name {
    min-width: 0;
    font-weight: var(--weight-medium);
  }

  .name a {
    display: inline-flex;
    max-width: 100%;
    min-width: 0;
  }

  /* The facts sit a little apart from the name and state above them. */
  .sources > li > :global(dl) {
    margin-top: var(--space-4);
  }

  /* DESIGN.md: 16 px gutters at 720 px and below. */
  @media (max-width: 720px) {
    header {
      padding: 0 var(--space-16);
    }

    .body {
      padding: var(--space-16);
    }
  }
</style>
