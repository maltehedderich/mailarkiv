<script lang="ts">
  import { tick } from "svelte";
  import CheckIcon from "~icons/lucide/check";
  import CopyIcon from "~icons/lucide/copy";

  let { hash }: { hash: string } = $props();

  const id = $props.id();

  // Groups make the hash comparable by eye, so it's never shortened.
  const groups = $derived(hash.match(/.{4}/g) ?? []);

  /** How long "Hash copied." stays. The clipboard can change after that. */
  const COPIED_MS = 2000;

  let code: HTMLElement | undefined = $state();
  let result: "copied" | "failed" | null = $state(null);
  /** The next step after a failure, which depends on the device. */
  let byHand = $state("");
  let timer: ReturnType<typeof setTimeout> | undefined;

  $effect(() => () => clearTimeout(timer));

  async function copy() {
    clearTimeout(timer);
    // Clear first, so the same line is announced again after a second try.
    result = null;
    await tick();
    try {
      await navigator.clipboard.writeText(hash);
    } catch {
      // Selected, the hash goes to the clipboard as 64 characters with the
      // person's own copy, through copied below.
      select();
      byHand = matchMedia("(pointer: coarse)").matches
        ? "Touch and hold it, then choose Copy."
        : `It's selected, so press ${/Mac|iPhone|iPad/.test(navigator.userAgent) ? "⌘C" : "Ctrl+C"}.`;
      result = "failed";
      return;
    }
    result = "copied";
    timer = setTimeout(() => (result = null), COPIED_MS);
  }

  function select() {
    const selection = getSelection();
    if (code === undefined || selection === null) return;
    selection.selectAllChildren(code);
  }

  // The groups are grid items, so the browser copied a selection of the hash
  // as 16 lines. A selection inside the hash copies the characters alone.
  function copied(event: ClipboardEvent) {
    const selection = getSelection();
    if (
      code === undefined ||
      selection === null ||
      selection.rangeCount === 0 ||
      event.clipboardData === null ||
      !code.contains(selection.getRangeAt(0).commonAncestorContainer)
    ) {
      return;
    }
    event.clipboardData.setData(
      "text/plain",
      selection.toString().replace(/\s/g, ""),
    );
    event.preventDefault();
  }
</script>

<div class="full-hash">
  <code bind:this={code} oncopy={copied}>
    {#each groups as group, index (index)}
      <span>{group}</span>
    {/each}
  </code>
  <!-- Described by its short hash, so a list of them on Status isn't a run of
       identical buttons. -->
  <button
    class="button ghost icon-only"
    type="button"
    aria-label="Copy the content hash"
    aria-describedby="{id}-short"
    onclick={copy}
  >
    {#if result === "copied"}
      <CheckIcon />
    {:else}
      <CopyIcon />
    {/if}
  </button>
  <span id="{id}-short" hidden>{hash.slice(0, 8)}</span>
  <p class="result" class:problem={result === "failed"} role="status">
    {#if result === "copied"}
      Hash copied.
    {:else if result === "failed"}
      The hash wasn't copied. {byHand}
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

  /* A click selects the whole hash, as it's only useful whole. */
  code {
    display: grid;
    grid-row: span 2;
    grid-template-columns: repeat(4, max-content);
    column-gap: var(--space-8);
    font-family: var(--font-mono);
    font-size: var(--size-evidence);
    font-variant-ligatures: none;
    line-height: var(--leading-body);
    user-select: all;
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
