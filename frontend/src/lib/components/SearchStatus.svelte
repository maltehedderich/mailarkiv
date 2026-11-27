<script lang="ts">
  import type { SearchStatus } from "#lib/api.js";
  import { formatTime } from "#lib/format/time.js";
  import { NEEDS_REBUILD_WORDS } from "#lib/status/detail.js";
  import {
    canRebuild,
    INDEXING_WORDS,
    pausedNote,
    progressNote,
    SEARCH_WORDS,
  } from "#lib/status/search.js";

  // The search and indexing states, with what each shows with, the backlog,
  // and the restarts. It holds no search words and no mail.

  let { search, onrebuild }: { search: SearchStatus; onrebuild: () => void } =
    $props();

  const numbers = new Intl.NumberFormat("en-US");

  const searchWords = $derived(SEARCH_WORDS[search.state]);
  const indexingWords = $derived(INDEXING_WORDS[search.indexing.state]);
  const progress = $derived(progressNote(search));
</script>

<dl class="facts">
  <div>
    <dt>Search</dt>
    <dd>
      <p class="state">
        <span class="dot {searchWords.tone}"></span>{searchWords.word}
      </p>
      {#if search.needsRebuild !== null}
        <p class="note">
          {NEEDS_REBUILD_WORDS[search.needsRebuild.cause]}, since
          <time datetime={search.needsRebuild.since}
            >{formatTime(search.needsRebuild.since)}</time
          >
        </p>
        <p class="what">
          Search answers when it can, with a notice that results may be
          incomplete. Indexing stays stopped until a rebuild finishes.
        </p>
      {:else if search.state === "unavailable"}
        <p class="note">
          {#if search.nextStartAt !== null}
            Next start
            <time datetime={search.nextStartAt}
              >{formatTime(search.nextStartAt)}</time
            >
          {:else}
            A start is under way
          {/if}
        </p>
      {/if}
    </dd>
  </div>
  <div>
    <dt>Indexing</dt>
    <dd>
      <p class="state">
        <span class="dot {indexingWords.tone}"></span>{indexingWords.word}
      </p>
      {#if progress !== null}
        <p class="note">{progress}</p>
      {/if}
      {#if search.indexing.state === "paused"}
        <p class="note">{pausedNote(search)}</p>
      {/if}
      {#if search.rebuild !== null}
        <p class="note">
          Started by {search.rebuild.startedBy} at
          <time datetime={search.rebuild.startedAt}
            >{formatTime(search.rebuild.startedAt)}</time
          >
        </p>
      {/if}
    </dd>
  </div>
  <div class="count">
    <dt>Waiting to be indexed</dt>
    <dd>{numbers.format(search.indexing.waiting)}</dd>
  </div>
  <div class="count">
    <dt>Search process restarts</dt>
    <dd>{numbers.format(search.restarts)}</dd>
  </div>
</dl>

{#if canRebuild(search)}
  <!-- Primary only while search needs it: then it's the screen's main
       action. -->
  <p class="actions">
    <button
      class="button"
      class:primary={search.state === "needs_rebuild"}
      type="button"
      onclick={onrebuild}
    >
      Rebuild index
    </button>
  </p>
{/if}

<style>
  dl {
    display: grid;
    gap: var(--space-4);
    margin: 0;
  }

  /* A 200 px label column, as the Recovery counts have. */
  .facts > div {
    display: grid;
    grid-template-columns: 200px minmax(0, 1fr);
    column-gap: var(--space-16);
  }

  dt {
    color: var(--text-3);
  }

  dd {
    display: grid;
    gap: var(--space-2);
    min-width: 0;
    margin: 0;
    color: var(--text-1);
    font-variant-numeric: tabular-nums;
  }

  .state {
    display: flex;
    align-items: center;
    gap: var(--space-8);
  }

  .dot {
    flex: none;
    width: var(--dot-size);
    height: var(--dot-size);
    border-radius: 50%;
  }

  .dot.ok {
    background: var(--state-ok);
  }

  .dot.neutral {
    background: var(--text-4);
  }

  .dot.warn {
    background: var(--state-warn);
  }

  .dot.bad {
    background: var(--state-bad);
  }

  .note {
    color: var(--text-3);
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  .what {
    color: var(--text-2);
  }

  .actions {
    margin-top: var(--space-8);
  }

  /* DESIGN.md: a state's label moves above it, and a count stays beside
     its label. */
  @media (max-width: 720px) {
    .facts > div {
      grid-template-columns: minmax(0, 1fr);
    }

    .facts > div.count {
      grid-template-columns: minmax(0, 1fr) auto;
    }

    .count dd {
      text-align: right;
    }
  }
</style>
