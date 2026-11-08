<script lang="ts">
  import { refreshAll } from "$app/navigation";
  import { page } from "$app/state";

  import CatalogDrawer from "#lib/components/CatalogDrawer.svelte";
  import Logo from "#lib/components/Logo.svelte";
  import Toasts from "#lib/components/Toasts.svelte";
  import { formatTime } from "#lib/format/time.js";

  // SvelteKit shows this page for an address no route matches (404), and for
  // a navigation that failed, such as a page's code not arriving because the
  // server or the network went down (500). Every load returns its failures
  // as data, so nothing else lands here.

  const notFound = $derived(page.status === 404);
  const title = $derived(
    notFound ? "Page not found" : "This page couldn't be loaded",
  );

  // When the page last failed to load, to find in the server log. A failed
  // Try again updates it, which is also how a person sees the try happened.
  let triedAt = $state("");
  /** Failed tries at this address. Each one shows the time afresh. */
  let tries = $state(0);

  function now(): string {
    return formatTime(new Date().toISOString());
  }

  // SvelteKit can keep this page when a 404 in the tab is followed by a
  // failed navigation, so the time is set when a failure shows, at each
  // address.
  $effect.pre(() => {
    if (notFound) return;
    void page.url.href;
    triedAt = now();
    tries = 0;
  });

  /**
   * A try shows on the button once it has lasted this long, so a quick one
   * doesn't flash, as in the failed-load block.
   */
  const SETTLE_MS = 400;

  let drawer: CatalogDrawer | undefined = $state();
  let busy = false;
  let slow = $state(false);

  async function retry() {
    // Not disabled while busy, so the focused button keeps focus.
    if (busy) return;
    busy = true;
    void drawer?.tug();
    const timer = setTimeout(() => (slow = true), SETTLE_MS);
    try {
      await refreshAll();
    } finally {
      clearTimeout(timer);
      busy = false;
      slow = false;
    }
    if (page.error !== null) {
      triedAt = now();
      tries += 1;
      return;
    }
    // The page arrived and took the focused button with it. refreshAll isn't
    // a navigation, so nothing is announced: focus moves to the new page's
    // heading on purpose, as after Try again in a failed-load block.
    if (document.activeElement === document.body) {
      document.querySelector<HTMLElement>("main h1[tabindex='-1']")?.focus();
    }
  }
</script>

<svelte:head>
  <title>{title} · MailArkiv</title>
</svelte:head>

<div class="page">
  <!-- No layout reaches an unknown address, so this page brings its own
       logo. It isn't a link: the action below is the way back. -->
  <header>
    <Logo />
  </header>

  <main>
    <div class="art">
      <CatalogDrawer bind:this={drawer} open={notFound} />
    </div>

    <div class="message">
      {#if notFound}
        <p class="code">404</p>
      {/if}
      <h1>{title}</h1>
      {#if notFound}
        <p>Check the address for a typo.</p>
      {:else}
        <p>Try again. If this repeats, check the server log.</p>
      {/if}
    </div>

    {#if notFound}
      <a class="button large primary" href="/">Open the message list</a>
    {:else}
      <button
        class="button large primary"
        type="button"
        aria-disabled={slow ? "true" : undefined}
        onclick={retry}
      >
        {slow ? "Trying again…" : "Try again"}
      </button>
      <!-- A status, so a failed Try again is announced by its new time. A new
           span for each try, so the same second is heard again too. -->
      <p class="tried" role="status" aria-atomic="true">
        Last tried
        {#key tries}
          <span class:fresh={tries > 0}>{triedAt}</span>
        {/key}
      </p>
    {/if}
  </main>
</div>

<!-- A navigation that failed takes the signed-in layout with it. An import
     that was running still reports here, so its result stays on screen
     (FR-032, FR-037). -->
<Toasts place="page" />

<style>
  .page {
    display: grid;
    grid-template-rows: auto 1fr;
    min-height: 100dvh;
    background: var(--panel);
  }

  /* As tall as a view header, so the logo sits where the sidebar's does. */
  header {
    display: flex;
    align-items: center;
    height: var(--header-height);
    padding: 0 var(--space-24);
  }

  /* Centred, and lifted by the header's height so it looks centred. Toasts
     at the foot push it up rather than cover the action. */
  main {
    display: grid;
    justify-items: center;
    align-content: center;
    padding: var(--space-32) var(--space-16)
      max(var(--header-height), var(--toast-room, 0px));
    text-align: center;
  }

  .art {
    width: 196px;
    margin-bottom: var(--space-32);
  }

  .message {
    display: grid;
    gap: var(--space-8);
    max-width: var(--form-width);
    margin-bottom: var(--space-24);
  }

  .code {
    color: var(--text-3);
    font-family: var(--font-mono);
    font-size: var(--size-label);
    line-height: var(--leading-label);
    letter-spacing: 0.08em;
  }

  h1 {
    font-size: var(--size-title);
    font-weight: var(--weight-strong);
    line-height: var(--leading-title);
    letter-spacing: var(--tracking-title);
    text-wrap: balance;
  }

  p {
    color: var(--text-3);
    text-wrap: pretty;
  }

  .button {
    padding: 0 var(--space-16);
  }

  .tried {
    margin-top: var(--space-12);
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

  @media (max-width: 720px) {
    header {
      padding: 0 var(--space-16);
    }

    .art {
      width: 168px;
      margin-bottom: var(--space-24);
    }

    .button {
      width: 100%;
      max-width: var(--form-width);
    }
  }
</style>
