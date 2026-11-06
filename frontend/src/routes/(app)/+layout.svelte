<script lang="ts">
  import ArchiveIcon from "~icons/lucide/archive";

  import { page } from "$app/state";

  import FailedLoad from "#lib/components/FailedLoad.svelte";
  import Mark from "#lib/components/Mark.svelte";

  import type { LayoutProps } from "./$types";

  let { data, children }: LayoutProps = $props();
</script>

<div class="shell">
  <nav aria-label="Main">
    <p class="brand"><Mark /> MailArkiv</p>
    <a
      class="item"
      href="/"
      aria-current={page.url.pathname === "/" ? "page" : undefined}
    >
      <ArchiveIcon />
      Messages
    </a>
  </nav>

  <div class="panel">
    {#if data.session.kind === "ok"}
      {@render children()}
    {:else}
      <!-- No count pill: a failed read must not look like zero problems. -->
      <main class="failed">
        <FailedLoad />
      </main>
    {/if}
  </div>
</div>

<style>
  .shell {
    display: grid;
    grid-template-columns: var(--sidebar-width) minmax(0, 1fr);
    min-height: 100dvh;
  }

  nav {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-16) var(--space-8);
  }

  .brand {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    height: var(--control-height);
    margin-bottom: var(--space-12);
    padding: 0 var(--space-8);
    font-weight: var(--weight-strong);
  }

  .item {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    height: 30px;
    padding: 0 var(--space-8);
    border-radius: var(--radius-medium);
    color: var(--text-2);
    font-weight: var(--weight-medium);
    text-decoration: none;
    transition: background-color var(--motion-fade);
  }

  .item :global(.icon) {
    color: var(--text-3);
  }

  .item:hover {
    background: var(--fill-2);
    transition: none;
  }

  .item[aria-current="page"] {
    background: var(--fill-3);
    color: var(--text-1);
  }

  .item[aria-current="page"] :global(.icon) {
    color: var(--text-1);
  }

  .panel {
    min-width: 0;
    margin: var(--space-8) var(--space-8) var(--space-8) 0;
    border-radius: var(--radius-large);
    background: var(--panel);
    box-shadow: var(--shadow-panel);
  }

  .failed {
    max-width: var(--content-width);
    margin: 0 auto;
    padding: 0 var(--space-24);
  }
</style>
