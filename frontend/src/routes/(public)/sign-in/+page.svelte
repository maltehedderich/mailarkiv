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

  // The session cookie is Secure, so a browser on plain HTTP drops it without
  // saying so. localhost counts as secure.
  const secure = window.isSecureContext;

  let email = $state("");
  let password = $state("");
  let busy = $state(false);
  let problem: Problem | null = $state(null);

  // FR-040: only a tab that had a signed-in page open sees this.
  const ended = $derived(showsSessionEnded(page.url));

  // FR-003 can't say which field is wrong, so both are marked.
  const invalid = $derived(problem === "invalid");

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    busy = true;
    // Clear first, so the same line is announced again after a second try.
    problem = null;
    const result = await signIn(email, password);
    if (result.kind === "ok") {
      // Back to the page the session ended on (FR-040). Still busy until the
      // page opens, so a second press can't send the form again.
      const next = safeNext(page.url.searchParams.get("next"), page.url.origin);
      try {
        await goto(next, { replace: true });
      } finally {
        busy = false;
      }
      return;
    }
    busy = false;
    problem = result.kind;
  }
</script>

<svelte:head>
  <title>Sign in · MailArkiv</title>
</svelte:head>

<Standalone title="Sign in">
  {#if ended || !secure}
    <div class="notices">
      {#if ended}
        <p class="notice">Your session ended. Sign in again to continue.</p>
      {/if}
      {#if !secure}
        <p class="notice problem">
          This address uses plain HTTP, so the browser won't keep the sign-in.
          Open MailArkiv over HTTPS, or on this machine at localhost.
        </p>
      {/if}
    </div>
  {/if}

  <form onsubmit={submit} novalidate>
    <label class="field">
      <span>Email address</span>
      <input
        type="email"
        name="email"
        autocomplete="username"
        spellcheck="false"
        aria-invalid={invalid}
        aria-describedby={invalid ? "sign-in-problem" : undefined}
        bind:value={email}
      />
    </label>
    <label class="field">
      <span>Password</span>
      <input
        type="password"
        name="password"
        autocomplete="current-password"
        aria-invalid={invalid}
        aria-describedby={invalid ? "sign-in-problem" : undefined}
        bind:value={password}
      />
    </label>
    <!-- aria-disabled, not disabled, so focus stays on the button. -->
    <button
      class="button large primary"
      type="submit"
      aria-disabled={busy ? "true" : undefined}
    >
      {busy ? "Signing in…" : "Sign in"}
    </button>
    <p id="sign-in-problem" class="problem" role="status">
      {problem === null ? "" : PROBLEMS[problem]}
    </p>
  </form>
</Standalone>

<style>
  .notices {
    display: grid;
    gap: var(--space-8);
  }

  /* On --fill-1, like the import box, so it reads as a message and not as part
     of the form. The transparent border is the edge in forced colours. */
  .notice {
    padding: var(--space-8) var(--space-12);
    border: 1px solid transparent;
    border-radius: var(--radius-medium);
    background: var(--fill-1);
    color: var(--text-2);
    text-wrap: pretty;
  }

  .notice.problem {
    color: var(--state-bad-text);
  }

  form {
    display: grid;
    gap: var(--space-12);
  }

  form .button {
    width: 100%;
    margin-top: var(--space-4);
  }
</style>
