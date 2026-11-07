<script lang="ts">
  import type { Snippet } from "svelte";

  import Logo from "#lib/components/Logo.svelte";

  let { title, children }: { title: string; children: Snippet } = $props();
</script>

<!-- A page with no sidebar, from DESIGN.md: sign-in. -->
<main>
  <div class="column">
    <p><Logo /></p>
    <h1>{title}</h1>
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
    gap: var(--space-16);
    width: 100%;
    max-width: var(--form-width);
  }

  h1 {
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
  }
</style>
