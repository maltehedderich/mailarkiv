<script lang="ts">
  import ArchiveIcon from "~icons/lucide/archive";
  import LogOutIcon from "~icons/lucide/log-out";

  import { goto } from "$app/navigation";
  import { page } from "$app/state";

  import { signOut } from "#lib/api.js";
  import Mark from "#lib/components/Mark.svelte";
  import { clearSignedIn } from "#lib/session.js";

  import type { LayoutProps } from "./$types";

  let { children }: LayoutProps = $props();

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
    <p class="brand"><Mark /> MailArkiv</p>
    <a
      class="item"
      href="/"
      aria-current={page.url.pathname === "/" ? "page" : undefined}
    >
      <ArchiveIcon />
      Messages
    </a>

    <div class="sign-out">
      <button class="item" type="button" onclick={signOutNow}>
        <LogOutIcon />
        Sign out
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

  .panel {
    min-width: 0;
    margin: var(--space-8) var(--space-8) var(--space-8) 0;
    border-radius: var(--radius-large);
    background: var(--panel);
    box-shadow: var(--shadow-panel);
  }
</style>
