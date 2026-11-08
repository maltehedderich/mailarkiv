<script lang="ts">
  import type { Snippet } from "svelte";

  import Logo from "#lib/components/Logo.svelte";

  let {
    title,
    children,
  }: {
    /** A heading for screen readers only. Leave it out when the content
        starts with its own visible h1. */
    title?: string;
    children: Snippet;
  } = $props();
</script>

<!-- A page with no sidebar, from DESIGN.md: sign-in. -->
<main>
  <div class="column">
    <div class="head">
      <Logo stacked />
      <!-- The logo heads the page. Shown under it, the title read as a
           tagline, so it's for screen readers. Pages set the tab title. -->
      {#if title !== undefined}
        <h1 class="visually-hidden">{title}</h1>
      {/if}
    </div>
    {@render children()}
  </div>
</main>

<style>
  /* Centred across and set a fifth of the way down, not centred vertically,
     so a line added below, such as an error, doesn't move the fields. */
  main {
    display: grid;
    justify-items: center;
    align-content: start;
    min-height: 100dvh;
    padding: max(var(--space-32), 20vh) var(--space-16) var(--space-32);
    background: var(--panel);
  }

  .column {
    display: grid;
    gap: var(--space-20);
    width: 100%;
    max-width: var(--form-width);
  }

  /* The logo sits centred over the column, 32 px above the content. The
     content keeps its left edge, so labels and fields line up. */
  .head {
    display: grid;
    justify-items: center;
    margin-bottom: var(--space-12);
  }
</style>
