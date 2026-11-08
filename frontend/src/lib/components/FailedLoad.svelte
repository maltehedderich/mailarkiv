<script lang="ts">
  import { onDestroy, tick } from "svelte";

  import { refreshAll } from "$app/navigation";
  import { formatTime } from "#lib/format/time.js";

  // FR-045: data that didn't arrive leaves this block in place of the content,
  // never an empty list.

  let {
    retry = refreshAll,
  }: {
    /**
     * Loads the data again, and settles once it has arrived or failed. A list
     * that streams settles later than the load, so its page passes its own.
     */
    retry?: () => Promise<unknown>;
  } = $props();

  /**
   * A try shows on the button once it has lasted this long, so a quick one
   * doesn't flash. The import box waits as long before it announces a step.
   */
  const SETTLE_MS = 400;

  function now(): string {
    return formatTime(new Date().toISOString());
  }

  // When the data last failed to arrive, to find in the server log, as on the
  // error page. A failed Try again updates it, so the try is seen and heard.
  let triedAt = $state(now());
  /** Failed tries since the block appeared. Each one shows the time afresh. */
  let tries = $state(0);
  let busy = false;
  let slow = $state(false);

  let gone = false;
  onDestroy(() => {
    gone = true;
  });

  async function tryAgain() {
    // Not disabled while busy, so the focused button keeps focus.
    if (busy) return;
    busy = true;
    const timer = setTimeout(() => (slow = true), SETTLE_MS);
    try {
      await retry();
      await tick();
    } finally {
      clearTimeout(timer);
      busy = false;
      slow = false;
    }
    // The data arrived and took this block with it.
    if (gone) return;
    triedAt = now();
    tries += 1;
  }
</script>

<div class="failed">
  <div class="message" role="status">
    <p class="what">This page couldn't be loaded.</p>
    <p class="next">Try again. If this repeats, check the server log.</p>
  </div>
  <button
    class="button"
    type="button"
    aria-disabled={slow ? "true" : undefined}
    onclick={tryAgain}
  >
    {slow ? "Trying again…" : "Try again"}
  </button>
  <!-- Its own status, so a failed try is announced by its new time. A new
       span for each try, so the same second is heard again too. -->
  <p class="tried" role="status" aria-atomic="true">
    Last tried
    {#key tries}
      <span class:fresh={tries > 0}>{triedAt}</span>
    {/key}
  </p>
</div>

<style>
  /* Where the content it replaces would start, so nothing moves when the data
     arrives. */
  .failed {
    display: grid;
    justify-items: start;
    padding-bottom: var(--space-24);
  }

  .message {
    display: grid;
    gap: var(--space-4);
    margin-bottom: var(--space-12);
  }

  .what {
    color: var(--text-2);
  }

  .next {
    color: var(--text-3);
  }

  .tried {
    margin-top: var(--space-8);
    color: var(--text-3);
    font-size: var(--size-label);
    line-height: var(--leading-label);
    font-variant-numeric: tabular-nums;
  }

  /* A failed try fades the new time in, so a quick one is seen to happen. */
  .fresh {
    animation: arrive var(--motion-enter) var(--ease-enter);
  }

  @keyframes arrive {
    from {
      opacity: 0;
    }
  }
</style>
