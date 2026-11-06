<script lang="ts">
  import { goto } from "$app/navigation";

  import { signIn, type SignInResult } from "#lib/api.js";
  import Mark from "#lib/components/Mark.svelte";

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

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    busy = true;
    // Clear first, so the same line is announced again after a second try.
    problem = null;
    const result = await signIn(email, password);
    busy = false;
    if (result.kind === "ok") {
      await goto("/", { replace: true });
      return;
    }
    problem = result.kind;
  }
</script>

<svelte:head>
  <title>Sign in · MailArkiv</title>
</svelte:head>

<main>
  <div class="column">
    <p class="brand"><Mark /> MailArkiv</p>
    <h1>Sign in</h1>

    {#if !secure}
      <p class="problem">
        This address uses plain HTTP, so the browser won't keep the sign-in.
        Open MailArkiv over HTTPS, or on this machine at localhost.
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

    <p class="note">Accounts and passwords are managed on the server.</p>
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

  .brand {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    font-weight: var(--weight-strong);
  }

  h1 {
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
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
</style>
