<script lang="ts">
  import ChevronRightIcon from "~icons/lucide/chevron-right";
  import { tick } from "svelte";

  import { goto } from "$app/navigation";
  import { page } from "$app/state";

  import {
    addSource,
    checkNewSource,
    type Connection,
    type Security,
    type SourceField,
  } from "#lib/api.js";
  import FailedLoad from "#lib/components/FailedLoad.svelte";
  import FolderChoice from "#lib/components/FolderChoice.svelte";
  import FormField from "#lib/components/FormField.svelte";
  import FormOutcome from "#lib/components/FormOutcome.svelte";
  import RemovalWarning from "#lib/components/RemovalWarning.svelte";
  import SecurityField from "#lib/components/SecurityField.svelte";
  import UntrustedText from "#lib/components/UntrustedText.svelte";
  import { signInPath } from "#lib/session.js";
  import { SECURITY_WORDS } from "#lib/sources/describe.js";
  import {
    chosenNames,
    DEFAULT_PORTS,
    FIELD_PROBLEMS,
    firstChoice,
    isValidName,
    parsePort,
    type FolderOption,
    type FormOutcome as Outcome,
  } from "#lib/sources/form.js";

  import type { PageProps } from "./$types";

  let { data }: PageProps = $props();

  let name = $state("");
  let host = $state("");
  let security: Security = $state("tls");
  let port = $state(String(DEFAULT_PORTS.tls));
  let userName = $state("");
  let password = $state("");

  let step: "connect" | "folders" = $state("connect");
  /** The details Connect signed in with, which Save sends. */
  let connected: Connection | null = $state(null);
  let folders: FolderOption[] = $state([]);
  let busy = $state(false);
  let outcome: Outcome | null = $state(null);
  /** The fields the last attempt refused, each with a line under it. */
  let refused: SourceField[] = $state([]);

  let connectForm: HTMLFormElement | undefined = $state();
  let foldersForm: HTMLFormElement | undefined = $state();
  let connectHeading: HTMLHeadingElement | undefined = $state();
  let foldersHeading: HTMLHeadingElement | undefined = $state();

  function problem(field: SourceField): string | null {
    return refused.includes(field) ? FIELD_PROBLEMS[field] : null;
  }

  /** Marks the fields and moves focus to the first, so its line is heard. */
  async function refuse(
    fields: SourceField[],
    form: HTMLFormElement | undefined,
  ) {
    refused = fields;
    await tick();
    form
      ?.querySelector<HTMLElement>(
        '[aria-invalid="true"], fieldset[aria-describedby] input',
      )
      ?.focus();
  }

  async function connect(event: SubmitEvent) {
    event.preventDefault();
    // aria-disabled, not disabled, keeps focus on the button while it waits.
    if (busy) return;
    outcome = null;
    refused = [];
    if (!isValidName(name)) {
      await refuse(["name"], connectForm);
      return;
    }

    const details: Connection = {
      host: host.trim(),
      port: parsePort(port),
      security,
      userName,
      password,
    };
    busy = true;
    const result = await checkNewSource(details);
    busy = false;
    switch (result.kind) {
      case "ok":
        connected = details;
        folders = firstChoice(result.value);
        step = "folders";
        await tick();
        foldersHeading?.focus();
        return;
      case "session_ended":
        await goto(signInPath(page.url));
        return;
      case "invalid_fields":
        if (result.fields.length === 0) outcome = { kind: "unavailable" };
        else await refuse(result.fields, connectForm);
        return;
      default:
        outcome = result;
    }
  }

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (busy || connected === null) return;
    outcome = null;
    refused = [];
    const chosen = chosenNames(folders);
    if (chosen.length === 0) {
      await refuse(["folders"], foldersForm);
      return;
    }

    busy = true;
    const result = await addSource({ ...connected, name, folders: chosen });
    switch (result.kind) {
      case "ok":
        // Still busy until the source's page replaces this one.
        await goto(`/sources/${result.value.id}`);
        return;
      case "session_ended":
        await goto(signInPath(page.url));
        return;
      case "invalid_fields":
        busy = false;
        if (result.fields.length === 0) {
          outcome = { kind: "unavailable" };
        } else if (result.fields.every((field) => field === "folders")) {
          await refuse(result.fields, foldersForm);
        } else {
          step = "connect";
          await tick();
          await refuse(result.fields, connectForm);
        }
        return;
      default:
        busy = false;
        outcome = result;
    }
  }

  async function changeDetails() {
    if (busy) return;
    outcome = null;
    refused = [];
    step = "connect";
    await tick();
    connectHeading?.focus();
  }
</script>

<svelte:head>
  <title>Add a source · Sources · MailArkiv</title>
</svelte:head>

<main>
  <header>
    <a class="up" href="/sources">Sources</a>
    <ChevronRightIcon class="icon" />
    <h1>Add a source</h1>
  </header>

  <div class="body">
    {#if data.session.kind !== "ok"}
      <FailedLoad />
    {:else if step === "connect" || connected === null}
      <form
        bind:this={connectForm}
        aria-labelledby="connect-title"
        novalidate
        onsubmit={connect}
      >
        <h2 id="connect-title" bind:this={connectHeading} tabindex="-1">
          Connect to the mailbox
        </h2>
        <FormField
          label="Name"
          bind:value={name}
          problem={problem("name")}
          autocomplete="off"
        />
        <FormField
          label="Server"
          bind:value={host}
          problem={problem("host")}
          autocomplete="off"
          autocapitalize="none"
          spellcheck="false"
        />
        <SecurityField
          bind:security
          bind:port
          securityProblem={problem("security")}
          portProblem={problem("port")}
        />
        <!-- Off, so the browser doesn't offer the administrator's own
             MailArkiv sign-in for the mailbox. -->
        <FormField
          label="User name"
          bind:value={userName}
          problem={problem("user_name")}
          autocomplete="off"
          autocapitalize="none"
          spellcheck="false"
        />
        <FormField
          label="Password"
          type="password"
          bind:value={password}
          problem={problem("password")}
          autocomplete="off"
        />
        <button
          class="button large primary"
          type="submit"
          aria-disabled={busy ? "true" : undefined}
        >
          {busy ? "Connecting…" : "Connect"}
        </button>
        <FormOutcome {outcome} action="Connect" ending="Nothing was saved." />
      </form>
    {:else}
      <form
        bind:this={foldersForm}
        aria-labelledby="folders-title"
        novalidate
        onsubmit={save}
      >
        <h2 id="folders-title" bind:this={foldersHeading} tabindex="-1">
          Choose folders
        </h2>
        <div class="mailbox">
          <p>
            <UntrustedText value={connected.userName} /> on
            <UntrustedText value={connected.host} />, port {connected.port},
            {SECURITY_WORDS[connected.security]}
          </p>
          <button
            class="button"
            type="button"
            aria-disabled={busy ? "true" : undefined}
            onclick={changeDetails}
          >
            Change details
          </button>
        </div>
        <FolderChoice bind:options={folders} problem={problem("folders")} />
        <RemovalWarning />
        <button
          class="button large primary"
          type="submit"
          aria-disabled={busy ? "true" : undefined}
        >
          {busy ? "Saving…" : "Save"}
        </button>
        <FormOutcome {outcome} action="Save" ending="Nothing was saved." />
      </form>
    {/if}
  </div>
</main>

<style>
  header {
    display: flex;
    gap: var(--space-4);
    align-items: center;
    height: var(--header-height);
    padding: 0 var(--header-inset);
    border-bottom: 1px solid var(--line);
  }

  .up {
    flex: none;
    color: var(--text-2);
  }

  header :global(.icon) {
    flex: none;
    color: var(--text-3);
  }

  h1 {
    min-width: 0;
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
  }

  .body {
    max-width: var(--content-width);
    margin: 0 auto;
    padding: var(--space-24);
  }

  form {
    display: grid;
    gap: var(--space-16);
    max-width: var(--form-width-wide);
  }

  h2 {
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  /* Natural width at the start, as on a source's page. */
  form > .button {
    justify-self: start;
  }

  .mailbox {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-8) var(--space-12);
    min-width: 0;
  }

  .mailbox p {
    flex: 1 1 0;
    min-width: 0;
    color: var(--text-2);
  }

  /* DESIGN.md: 16 px gutters at 720 px and below. */
  @media (max-width: 720px) {
    header {
      padding: 0 var(--space-16);
    }

    .body {
      padding: var(--space-16);
    }
  }
</style>
