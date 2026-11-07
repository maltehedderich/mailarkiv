<script lang="ts">
  import CopyIcon from "~icons/lucide/copy";

  let { hash }: { hash: string } = $props();

  // Groups make the hash comparable by eye, so it's never shortened (FR-020).
  const groups = $derived(hash.match(/.{4}/g) ?? []);

  let copied: boolean | null = $state(null);

  async function copy() {
    // Clear first, so the same line is announced again after a second try.
    copied = null;
    try {
      await navigator.clipboard.writeText(hash);
      copied = true;
    } catch {
      copied = false;
    }
  }
</script>

<div class="full-hash">
  <code>
    {#each groups as group, index (index)}
      <span>{group}</span>
    {/each}
  </code>
  <button
    class="button icon-only"
    type="button"
    aria-label="Copy the content hash"
    onclick={copy}
  >
    <CopyIcon />
  </button>
  <p class="result" class:problem={copied === false} role="status">
    {#if copied === true}
      Hash copied.
    {:else if copied === false}
      The hash wasn't copied. Select it and copy it by hand.
    {/if}
  </p>
</div>

<style>
  .full-hash {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    grid-template-rows: auto 1fr;
    column-gap: var(--space-12);
  }

  code {
    display: grid;
    grid-row: span 2;
    grid-template-columns: repeat(4, max-content);
    column-gap: var(--space-8);
    font-family: var(--font-mono);
    font-size: var(--size-evidence);
    font-variant-ligatures: none;
    line-height: var(--leading-body);
  }

  /* Centred on the hash's first line. */
  button {
    justify-self: start;
    margin-top: calc((var(--leading-body) - var(--control-height)) / 2);
  }

  /* Beside the hash, so a result moves nothing, and fits even at 360 px. */
  .result {
    margin-top: var(--space-2);
    color: var(--text-3);
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  .result.problem {
    color: var(--state-bad-text);
  }
</style>
