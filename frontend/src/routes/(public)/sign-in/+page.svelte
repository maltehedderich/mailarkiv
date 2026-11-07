<script lang="ts">
  import { goto } from "$app/navigation";
  import { page } from "$app/state";

  import { signIn, type SignInResult } from "#lib/api.js";
  import Standalone from "#lib/components/Standalone.svelte";
  import { safeNext, showsSessionEnded } from "#lib/session.js";

  type Problem = Exclude<SignInResult["kind"], "ok">;

  // FR-003, T071, and FR-045's sign-in line.
  const PROBLEMS: Record<Problem, string> = {
    invalid: "That email address and password don't match.",
    blocked:
      "Too many failed sign-ins for this address. Try again in 15 minutes, or ask the operator to set a new password.",
    unavailable:
      "Sign-in didn't finish. Try again. If this repeats, check the server log.",
  };

  // FR-001. The path is the link's text, so the note still helps on a network
  // that can't reach GitHub.
  const GUIDE =
    "https://github.com/maltehedderich/mailarkiv/blob/main/docs/OPERATIONS.md";

  // The session cookie is Secure, so a browser on plain HTTP drops it without
  // saying so. localhost counts as secure.
  const secure = window.isSecureContext;

  let email = $state("");
  let password = $state("");
  let busy = $state(false);
  let problem: Problem | null = $state(null);

  // FR-040: only a tab that had a signed-in page open sees this.
  const ended = $derived(showsSessionEnded(page.url));

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    busy = true;
    // Clear first, so the same line is announced again after a second try.
    problem = null;
    const result = await signIn(email, password);
    busy = false;
    if (result.kind === "ok") {
      // Back to the page the session ended on (FR-040).
      const next = safeNext(page.url.searchParams.get("next"), page.url.origin);
      await goto(next, { replace: true });
      return;
    }
    problem = result.kind;
  }
</script>

<svelte:head>
  <title>Sign in · MailArkiv</title>
</svelte:head>

<Standalone title="Sign in">
  {#if ended}
    <p class="ended">Your session ended. Sign in again to continue.</p>
  {/if}

  {#if !secure}
    <p class="problem">
      This address uses plain HTTP, so the browser won't keep the sign-in. Open
      MailArkiv over HTTPS, or on this machine at localhost.
    </p>
  {/if}

  <form onsubmit={submit} novalidate>
    <label class="field">
      <span>Email address</span>
      <input
        type="email"
        name="email"
        autocomplete="username"
        spellcheck="false"
        bind:value={email}
      />
    </label>
    <label class="field">
      <span>Password</span>
      <input
        type="password"
        name="password"
        autocomplete="current-password"
        bind:value={password}
      />
    </label>
    <button class="button large primary" type="submit">Sign in</button>
    <p class="problem" role="status">
      {problem === null ? "" : PROBLEMS[problem]}
    </p>
  </form>

  <p class="note">
    Accounts and passwords are managed on the server. Operator guide:
    <a href={GUIDE}>docs/OPERATIONS.md</a>
  </p>
</Standalone>

<style>
  .ended {
    color: var(--text-2);
  }

  form {
    display: grid;
    gap: var(--space-12);
  }

  form .button {
    width: 100%;
    margin-top: var(--space-4);
  }

  .note {
    color: var(--text-3);
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  .note a {
    color: var(--accent);
  }
</style>
