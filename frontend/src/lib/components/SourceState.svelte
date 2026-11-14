<script lang="ts">
  import type { Source } from "#lib/api.js";
  import { formatTime } from "#lib/format/time.js";
  import { stateTone } from "#lib/sources/describe.js";
  import { CATEGORY_WORDS, STATE_WORDS } from "#lib/sources/words.js";

  // The state word, and what the state is shown with: the next sync, the
  // progress, the failure, or when it was paused.

  let { source }: { source: Omit<Source, "folders"> } = $props();

  const numbers = new Intl.NumberFormat("en-US");

  const failure = $derived(
    source.failure === null ? null : CATEGORY_WORDS[source.failure.category],
  );
</script>

<p class="state">
  <span class="dot {stateTone(source.state)}"></span>
  <span class="text">
    <span class="word">{STATE_WORDS[source.state]}</span>
    {#if source.state === "syncing" && source.progress !== null}
      <span class="detail progress">
        Archived {numbers.format(source.progress.archived)} of
        {numbers.format(source.progress.found)} found
      </span>
    {:else if source.state === "waiting" && source.nextSyncAt !== null}
      <span class="detail">
        Next sync
        <time datetime={source.nextSyncAt}>{formatTime(source.nextSyncAt)}</time
        >
      </span>
    {:else if source.state === "retrying" && failure !== null}
      <span class="detail">
        {failure.label}.
        {#if source.nextSyncAt !== null}
          Next attempt
          <time datetime={source.nextSyncAt}
            >{formatTime(source.nextSyncAt)}</time
          >
        {/if}
      </span>
    {:else if source.state === "needs_attention" && failure !== null}
      <span class="detail">{failure.label}. {failure.nextStep}.</span>
    {:else if source.state === "paused" && source.pausedAt !== null}
      <span class="detail">
        Since
        <time datetime={source.pausedAt}>{formatTime(source.pausedAt)}</time>
      </span>
    {/if}
  </span>
</p>

<style>
  /* The dot hangs beside the first line, so a detail that wraps starts
     under the state word. */
  .state {
    display: grid;
    grid-template-columns: var(--dot-size) minmax(0, 1fr);
    column-gap: var(--space-8);
  }

  .text {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0 var(--space-8);
    min-width: 0;
  }

  /* Centred on the first line. */
  .dot {
    width: var(--dot-size);
    height: var(--dot-size);
    margin-top: calc((var(--leading-body) - var(--dot-size)) / 2);
    border-radius: 50%;
    background: var(--text-4);
  }

  .dot.ok {
    background: var(--state-ok);
  }

  .dot.warn {
    background: var(--state-warn);
  }

  .dot.bad {
    background: var(--state-bad);
  }

  .word {
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  .detail {
    color: var(--text-2);
  }

  /* Not the whole line: tabular figures widen the hyphen in "Sign-in". */
  .progress,
  time {
    font-variant-numeric: tabular-nums;
  }

  /* A line breaks before a time, never inside it. */
  time {
    white-space: nowrap;
  }
</style>
