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
  <span class="word">{STATE_WORDS[source.state]}</span>
  {#if source.state === "syncing" && source.progress !== null}
    <span class="detail progress">
      Archived {numbers.format(source.progress.archived)} of
      {numbers.format(source.progress.found)} found
    </span>
  {:else if source.state === "waiting" && source.nextSyncAt !== null}
    <span class="detail">
      Next sync
      <time datetime={source.nextSyncAt}>{formatTime(source.nextSyncAt)}</time>
    </span>
  {:else if source.state === "retrying" && failure !== null}
    <span class="detail">
      {failure.label}.
      {#if source.nextSyncAt !== null}
        Next attempt
        <time datetime={source.nextSyncAt}>{formatTime(source.nextSyncAt)}</time
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
</p>

<style>
  .state {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0 var(--space-8);
  }

  /* Centred on the word's lowercase letters, as baseline alignment would
     put an empty box on the text's bottom edge. */
  .dot {
    flex: none;
    align-self: center;
    width: var(--dot-size);
    height: var(--dot-size);
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
</style>
