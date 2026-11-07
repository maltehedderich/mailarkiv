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

  let signingOut = false;
  let signOutFailed = $state(false);

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
          >{problemCount}<span class="visually-hidden"
            >{problemCount === 1 ? " problem" : " problems"}</span
          ></span
        >
      {/if}
    </a>

    <div class="sign-out">
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
      <p class="problem" role="status">
        {signOutFailed
          ? "Sign-out didn't finish, so you're still signed in. Try again."
          : ""}
      </p>
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
    padding: var(--space-16) var(--space-8);
  }

  .brand {
    display: flex;
    align-items: center;
    height: var(--control-height);
    margin-bottom: var(--space-12);
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
     clear of them. */
  .panel {
    min-width: 0;
    margin: var(--space-8) var(--space-8) var(--space-8) 0;
    padding-bottom: var(--toast-room, 0px);
    border-radius: var(--radius-large);
    background: var(--panel);
    box-shadow: var(--shadow-panel);
  }

  /* DESIGN.md: the sidebar becomes a bottom bar of 44 px tabs, icon over
     label, and the panel takes the whole width above it. */
  @media (max-width: 720px) {
    .shell {
      grid-template-columns: minmax(0, 1fr);
      padding-bottom: 44px;
    }

    nav {
      position: fixed;
      inset: auto 0 0;
      z-index: 1;
      flex-direction: row;
      gap: 0;
      height: 44px;
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
      height: 44px;
      border-radius: 0;
      font-size: var(--size-label);
      line-height: var(--leading-label);
    }

    button.item {
      width: 56px;
      justify-content: center;
      align-items: center;
    }

    .item .label {
      display: none;
    }

    /* Beside the icon, so the label stays centred. */
    .count {
      position: absolute;
      top: var(--space-2);
      left: calc(50% + var(--space-6));
      margin: 0;
    }

    .sign-out {
      flex: none;
      margin: 0;
    }

    /* Above the bar, and only drawn when it has something to say. */
    .sign-out .problem {
      position: fixed;
      right: var(--space-16);
      bottom: 52px;
      left: var(--space-16);
      padding: 0;
    }

    .sign-out .problem:not(:empty) {
      padding: var(--space-8) var(--space-12);
      border-radius: var(--radius-medium);
      background: var(--raised);
      box-shadow: var(--shadow-pop);
    }

    /* The shell already keeps 44 px for the bar, which the room includes. */
    .panel {
      margin: 0;
      padding-bottom: max(0px, calc(var(--toast-room, 0px) - 44px));
      border-radius: 0;
      box-shadow: none;
    }
  }
</style>
