<script lang="ts">
  import type { ComponentProps } from "svelte";

  import Pager from "./Pager.svelte";

  // A list's header: its heading, and the pager once there is a page to show.

  let {
    title,
    pager,
    heading = $bindable(),
  }: {
    title: string;
    /** Without a label, the spoken range starts with the title. */
    pager:
      (Omit<ComponentProps<typeof Pager>, "label"> & { label?: string }) | null;
    /** Focused only when the content it heads was replaced. */
    heading?: HTMLHeadingElement;
  } = $props();
</script>

<!-- Sticky, with the pager, so the next page is in view wherever the list
     is scrolled. A turn goes to the top of the page. Below the list, the
     next Next was then 2,100 px down at 1280 px wide, and 4,000 at 360. -->
<header class="list-header">
  <h1 bind:this={heading} tabindex="-1">{title}</h1>
  {#if pager !== null}
    <Pager {...pager} label={pager.label ?? title} />
  {/if}
</header>

<style>
  /* Above the rows' raised downloads as they scroll under it. Its corners
     follow the panel's, which a square fill would cover. */
  header {
    position: sticky;
    top: 0;
    z-index: 2;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-16);
    height: var(--header-height);
    padding: 0 var(--header-inset);
    border-bottom: 1px solid var(--line);
    border-radius: var(--radius-large) var(--radius-large) 0 0;
    background: var(--panel);
  }

  /* WCAG 2.4.11: focus scrolls a control clear of the header. */
  :global(html:has(.list-header)) {
    scroll-padding-top: var(--header-height);
  }

  h1 {
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
  }

  h1:focus {
    outline: none;
  }

  @media (max-width: 720px) {
    header {
      padding: 0 var(--space-16);
      border-radius: 0;
    }
  }
</style>
