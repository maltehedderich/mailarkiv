<script lang="ts">
  import ActivityIcon from "~icons/lucide/activity";
  import ArchiveIcon from "~icons/lucide/archive";
  import LogOutIcon from "~icons/lucide/log-out";

  import { goto } from "$app/navigation";
  import { page } from "$app/state";

  import { signOut } from "#lib/api.js";
  import Logo from "#lib/components/Logo.svelte";
  import Toasts from "#lib/components/Toasts.svelte";
  import { clearSignedIn } from "#lib/session.js";

  import type { LayoutProps } from "./$types";

  let { children, data }: LayoutProps = $props();

  // FR-039: failure events plus unreferenced originals, read at each page load.
  // A failed read shows no count, and the page shows the failed-load state, so
  // it never looks like zero (FR-045).
  const problemCount = $derived(
    data.session.kind === "ok" ? data.session.value.problemCount : 0,
  );

  // Separators, as the pager shows totals.
  const numbers = new Intl.NumberFormat("en-US");

  let signingOut = false;
  let signOutFailed = $state(false);

  // WCAG 2.4.11: at 720 px and below the sign-out line floats above the tab
  // bar, so the page keeps room for it, as it does for the toasts. Only the
  // narrow layout reads --sign-out-room. The 8 px gap matches the toasts'.
  const GAP = 8;
  let lineHeight = $state(0);
  $effect(() => {
    const root = document.documentElement;
    const room = lineHeight === 0 ? 0 : lineHeight + GAP;
    root.style.setProperty("--sign-out-room", `${room}px`);
    return () => root.style.removeProperty("--sign-out-room");
  });

  async function signOutNow() {
    // Not disabled while busy, so the focused button keeps focus.
    if (signingOut) return;
    signingOut = true;
    // Clear first, so the same line is announced again after a second try.
    signOutFailed = false;
    const result = await signOut();
    signingOut = false;
    if (result.kind === "unavailable") {
      signOutFailed = true;
      return;
    }
    // A marked 401 means the session had already ended, so the browser is
    // signed out either way. Clearing the flag keeps the session-ended message
    // off this sign-in page. The push lets Back reach the list, whose load
    // then opens sign-in again (US1 scenario 6).
    clearSignedIn();
    await goto("/sign-in");
  }
</script>

<div class="shell">
  <nav aria-label="Main">
    <p class="brand"><Logo /></p>
    <a
      class="item"
      href="/"
      aria-current={page.url.pathname === "/" ? "page" : undefined}
    >
      <ArchiveIcon />
      Messages
    </a>
    <a
      class="item"
      href="/status"
      aria-current={page.url.pathname === "/status" ? "page" : undefined}
    >
      <ActivityIcon />
      Status
      {#if problemCount > 0}
        <span class="count"
          >{numbers.format(problemCount)}<span class="visually-hidden"
            >{problemCount === 1 ? " problem" : " problems"}</span
          ></span
        >
      {/if}
    </a>

    <div class="sign-out">
      <!-- Above the button, so the line grows upward and the button stays
           under the pointer for the second try. -->
      <p class="problem" role="status" bind:offsetHeight={lineHeight}>
        {signOutFailed
          ? "Sign-out didn't finish, so you're still signed in. Try again."
          : ""}
      </p>
      <!-- The label hides at 720 px and below, where this is an icon tab. -->
      <button
        class="item"
        type="button"
        aria-label="Sign out"
        onclick={signOutNow}
      >
        <LogOutIcon />
        <span class="label">Sign out</span>
      </button>
    </div>
  </nav>

  <div class="panel">
    <!-- Always the page, even when the session read failed. Swapping it out
         would take an import's result with it (FR-032), so each page shows
         the failed-load state itself. -->
    {@render children()}
  </div>
</div>

<!-- After the panel, so the toasts come last in the tab order. They stay
     across pages, so a result isn't lost on the way to Status. -->
<Toasts place="page" />

<style>
  .shell {
    display: grid;
    grid-template-columns: var(--sidebar-width) minmax(0, 1fr);
    min-height: 100dvh;
  }

  /* Sticky, so Sign out stays in view beside a long list. */
  nav {
    position: sticky;
    top: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    height: 100dvh;
    padding: var(--space-8);
  }

  /* As tall as a view header and inset like the panel, so the logo lines up
     with the view title. */
  .brand {
    display: flex;
    flex: none;
    align-items: center;
    height: var(--header-height);
    padding: 0 var(--space-8);
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

  button.item {
    width: 100%;
    border: 0;
    background: transparent;
    text-align: left;
    cursor: pointer;
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

  /* The problem count on the Status item (FR-039). */
  .count {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 18px;
    height: 18px;
    margin-left: auto;
    padding: 0 var(--space-6);
    border-radius: 9px;
    background: var(--state-bad-fill);
    color: var(--state-bad-text);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    line-height: var(--leading-label);
  }

  .sign-out {
    display: grid;
    gap: var(--space-4);
    margin-top: auto;
  }

  .sign-out .problem {
    padding: 0 var(--space-8);
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  /* --toast-room is set by the toasts, so the end of the page can scroll
     clear of them. Isolated, so a raised control in the page, such as a row's
     download, can't paint over the bottom tab bar. */
  .panel {
    isolation: isolate;
    min-width: 0;
    margin: var(--space-8) var(--space-8) var(--space-8) 0;
    padding-bottom: var(--toast-room, 0px);
    border-radius: var(--radius-large);
    background: var(--panel);
    box-shadow: var(--shadow-panel);
  }

  /* DESIGN.md: the sidebar becomes a bottom bar of tabs, icon over label, and
     the panel takes the whole width above it. */
  @media (max-width: 720px) {
    .shell {
      grid-template-columns: minmax(0, 1fr);
      padding-bottom: var(--tab-bar-height);
    }

    nav {
      position: fixed;
      inset: auto 0 0;
      z-index: 1;
      flex-direction: row;
      gap: 0;
      height: var(--tab-bar-height);
      padding: 0;
      border-top: 1px solid var(--line);
      background: var(--canvas);
    }

    .brand {
      display: none;
    }

    .item {
      position: relative;
      flex: 1;
      flex-direction: column;
      justify-content: center;
      gap: var(--space-2);
      height: var(--tab-bar-height);
      border-radius: 0;
      font-size: var(--size-label);
      line-height: var(--leading-label);
    }

    /* Inside the tab, so the screen edge and the next tab don't cut it. */
    .item:focus-visible {
      outline-offset: -2px;
    }

    /* Room for the hidden label below, so this icon sits level with the
       others. */
    button.item {
      width: 56px;
      padding-bottom: calc(var(--leading-label) + var(--space-2));
    }

    .item .label {
      display: none;
    }

    /* Beside the icon and level with it, so the label stays centred and the
       see-through pill doesn't cover the icon. */
    .count {
      position: absolute;
      top: var(--space-4);
      left: calc(50% + var(--icon-size) / 2 + var(--space-2));
      margin: 0;
    }

    .sign-out {
      flex: none;
      margin: 0;
    }

    /* Above the bar and any toasts, which have no timer, and only drawn when
       it has something to say. */
    .sign-out .problem {
      position: fixed;
      right: var(--space-16);
      bottom: max(
        var(--tab-bar-height) + var(--space-8),
        var(--toast-room, 0px)
      );
      left: var(--space-16);
      padding: 0;
    }

    .sign-out .problem:not(:empty) {
      padding: var(--space-8) var(--space-12);
      border-radius: var(--radius-medium);
      background: var(--raised);
      box-shadow: var(--shadow-pop);
    }

    /* The shell already keeps room for the bar, which the toasts' room
       includes. The sign-out line sits above both. */
    .panel {
      margin: 0;
      padding-bottom: calc(
        max(0px, var(--toast-room, 0px) - var(--tab-bar-height)) +
          var(--sign-out-room, 0px)
      );
      border-radius: 0;
      box-shadow: none;
    }
  }
</style>
