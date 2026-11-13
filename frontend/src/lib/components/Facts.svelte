<script lang="ts">
  import type { Snippet } from "svelte";

  // Facts in the 136 px label column Status uses, at label size. Labels move
  // above their values at 720 px and below.

  let {
    children,
  }: {
    /** Rows, each a `<div>` holding a `<dt>` and a `<dd>`. */
    children: Snippet;
  } = $props();
</script>

<dl class="facts">{@render children()}</dl>

<style>
  .facts {
    display: grid;
    gap: var(--space-4);
    margin: 0;
    font-size: var(--size-label);
    line-height: var(--leading-label);
    font-variant-numeric: tabular-nums;
  }

  .facts > :global(div) {
    display: grid;
    grid-template-columns: 136px minmax(0, 1fr);
    column-gap: var(--space-16);
  }

  .facts :global(dt) {
    color: var(--text-3);
  }

  .facts :global(dd) {
    min-width: 0;
    margin: 0;
    color: var(--text-2);
  }

  /* Tabular figures widen Inter's hyphen, which spaced out host names such
     as "mail-1.example.org". Only times and counts need them. */
  .facts :global(bdi) {
    font-variant-numeric: normal;
  }

  @media (max-width: 720px) {
    .facts > :global(div) {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
