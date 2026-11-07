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
  main {
    display: grid;
    place-items: center;
    min-height: 100dvh;
    padding: var(--space-32) var(--space-16);
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
