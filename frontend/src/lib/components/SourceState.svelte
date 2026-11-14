<script lang="ts">
  import type { Source } from "#lib/api.js";
  import { formatTime } from "#lib/format/time.js";
  import { stateTone } from "#lib/sources/describe.js";
  import { CATEGORY_WORDS, STATE_WORDS } from "#lib/sources/words.js";

  // The state word, and what the state is shown with: the next sync, the
  // progress, the failure, or when it was paused.

  let {
    source,
    format = formatTime,
    aligned = false,
  }: {
    source: Omit<Source, "folders">;
    /** Formats each time. The list drops today's date. */
    format?: (rfc3339: string) => string;
    /**
     * Put the state word in the facts' label column and its detail in their
     * value column, with the dot hung in the gutter, so the state and the
     * facts below it share one edge.
     */
    aligned?: boolean;
  } = $props();

  const numbers = new Intl.NumberFormat("en-US");

  const failure = $derived(
    source.failure === null ? null : CATEGORY_WORDS[source.failure.category],
  );
</script>

<p class="state" class:aligned>
  <span class="dot {stateTone(source.state)}"></span>
  <span class="text">
    <span class="word">{STATE_WORDS[source.state]}</span>
    {#if source.state === "syncing" && source.progress !== null}
      <!-- Before the sync finds anything, a count of 0 of 0 says nothing. -->
      <span class="detail progress">
        {#if source.progress.found === 0}
          Checking for new messages
        {:else}
          Archived {numbers.format(source.progress.archived)} of
          {numbers.format(source.progress.found)} found
        {/if}
      </span>
    {:else if source.state === "waiting" && source.nextSyncAt !== null}
      <span class="detail">
        Next sync
        <time datetime={source.nextSyncAt}>{format(source.nextSyncAt)}</time>
      </span>
    {:else if source.state === "retrying" && failure !== null}
      <span class="detail">
        {failure.label}.
        {#if source.nextSyncAt !== null}
          Next attempt
          <time datetime={source.nextSyncAt}>{format(source.nextSyncAt)}</time>
        {/if}
      </span>
    {:else if source.state === "needs_attention" && failure !== null}
      <span class="detail">{failure.label}. {failure.nextStep}.</span>
    {:else if source.state === "paused" && source.pausedAt !== null}
      <span class="detail">
        Since
        <time datetime={source.pausedAt}>{format(source.pausedAt)}</time>
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

  /* Gives the line its baseline, so a heading beside it can align. */
  .text {
    align-self: baseline;
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

  /* The facts' grid. The dot sits in the gutter left of the column, so the
     state word starts on the labels' edge. The detail takes the facts' size,
     leaving the state word as the line's one emphasis. */
  .aligned {
    position: relative;
    grid-template-columns: var(--facts-label, 136px) minmax(0, 1fr);
    column-gap: var(--space-16);
    align-items: baseline;
  }

  .aligned .text {
    display: contents;
  }

  .aligned .dot {
    position: absolute;
    top: 0;
    left: calc(-1 * (var(--dot-size) + var(--space-8)));
  }

  .aligned .detail {
    min-width: 0;
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  /* One column has no gutter to hang the dot in, so the line returns to its
     plain form. */
  @media (max-width: 1040px) {
    .aligned {
      grid-template-columns: var(--dot-size) minmax(0, 1fr);
      column-gap: var(--space-8);
      align-items: normal;
    }

    .aligned .text {
      display: flex;
    }

    .aligned .dot {
      position: static;
    }

    .aligned .detail {
      font-size: inherit;
      line-height: inherit;
    }
  }
</style>
