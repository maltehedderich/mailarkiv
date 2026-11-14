<script lang="ts">
  import { onMount, tick, untrack } from "svelte";

  import { goto } from "$app/navigation";
  import { page } from "$app/state";

  import {
    checkSource,
    editSource,
    type ConnectionChange,
    type Security,
    type SourceChange,
    type SourceDetail,
    type SourceField,
  } from "#lib/api.js";
  import { signInPath } from "#lib/session.js";
  import {
    chosenNames,
    currentChoice,
    FIELD_PROBLEMS,
    isValidName,
    NO_PASSWORD_MANAGER,
    parsePort,
    trimSourceField,
    widenChoice,
    type FolderOption,
    type FormOutcome as Outcome,
  } from "#lib/sources/form.js";

  import Facts from "./Facts.svelte";
  import FolderChoice from "./FolderChoice.svelte";
  import FormField from "./FormField.svelte";
  import FormOutcome from "./FormOutcome.svelte";
  import RemovalWarning from "./RemovalWarning.svelte";
  import SecurityField from "./SecurityField.svelte";
  import UntrustedText from "./UntrustedText.svelte";

  // Changes a source's name, connection, password, or folders. The server and
  // user name can't change. Only the fields that differ are sent, and an
  // empty password keeps the stored one.

  let {
    source,
    onsaved,
    oncancel,
  }: {
    source: SourceDetail;
    /** After the server saved the change. */
    onsaved: () => Promise<void>;
    oncancel: () => void;
  } = $props();

  // The form starts from the source as it was when Edit was pressed, so a
  // reload while editing doesn't replace what's typed.
  const before = untrack(() => ({
    id: String(source.id),
    name: source.name,
    port: source.port,
    security: source.security,
    folders: source.folders.map((folder) => folder.name),
  }));

  let name = $state(before.name);
  let security: Security = $state(before.security);
  let port = $state(String(before.port));
  let password = $state("");
  let folders: FolderOption[] = $state(currentChoice(before.folders));
  /** Set once Add folders has listed the server's folders. */
  let listed = $state(false);
  let listedLine = $state("");

  let busy: "check" | "save" | null = $state(null);
  let checkOutcome: Outcome | null = $state(null);
  let saveOutcome: Outcome | null = $state(null);
  let refused: SourceField[] = $state([]);

  const numbers = new Intl.NumberFormat("en-US");

  let form: HTMLFormElement;
  let heading: HTMLHeadingElement;

  onMount(() => heading.focus());

  function problem(field: SourceField): string | null {
    return refused.includes(field) ? FIELD_PROBLEMS[field] : null;
  }

  async function refuse(fields: SourceField[]) {
    refused = fields;
    await tick();
    form
      .querySelector<HTMLElement>(
        '[aria-invalid="true"], fieldset[aria-describedby] input',
      )
      ?.focus();
  }

  /** The connection fields that differ, which a sign-in checks. */
  function connectionChange(): ConnectionChange {
    port = trimSourceField(port);
    const change: ConnectionChange = {};
    const portNumber = parsePort(port);
    if (portNumber !== before.port) change.port = portNumber;
    if (security !== before.security) change.security = security;
    if (password !== "") change.password = password;
    return change;
  }

  function listedWords(added: number): string {
    if (added === 0) return "The server has no other folders.";
    return added === 1
      ? "1 more folder listed, unchecked."
      : `${numbers.format(added)} more folders listed, unchecked.`;
  }

  function reset() {
    listedLine = "";
    checkOutcome = null;
    saveOutcome = null;
    refused = [];
  }

  async function addFolders() {
    if (busy !== null) return;
    reset();
    busy = "check";
    const result = await checkSource(before.id, connectionChange());
    busy = null;
    switch (result.kind) {
      case "ok": {
        const widened = widenChoice(folders, result.value);
        listedLine = listedWords(widened.length - folders.length);
        folders = widened;
        listed = true;
        return;
      }
      case "session_ended":
        await goto(signInPath(page.url));
        return;
      case "invalid_fields":
        if (result.fields.length === 0) checkOutcome = { kind: "unavailable" };
        else await refuse(result.fields);
        return;
      default:
        checkOutcome = result;
    }
  }

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (busy !== null) return;
    reset();
    name = trimSourceField(name);
    if (!isValidName(name)) {
      await refuse(["name"]);
      return;
    }
    const chosen = chosenNames(folders);
    if (chosen.length === 0) {
      await refuse(["folders"]);
      return;
    }

    const change: SourceChange = connectionChange();
    if (name !== before.name) change.name = name;
    if (chosen.join("\n") !== before.folders.join("\n")) {
      change.folders = chosen;
    }
    busy = "save";
    const result = await editSource(before.id, change);
    switch (result.kind) {
      case "ok":
        // Still busy until the page has the saved source.
        await onsaved();
        return;
      case "session_ended":
        await goto(signInPath(page.url));
        return;
      case "invalid_fields":
        busy = null;
        if (result.fields.length === 0) saveOutcome = { kind: "unavailable" };
        else await refuse(result.fields);
        return;
      default:
        busy = null;
        saveOutcome = result;
    }
  }

  function cancel() {
    if (busy === "save") return;
    oncancel();
  }
</script>

<form bind:this={form} aria-labelledby="edit-title" novalidate onsubmit={save}>
  <h2 id="edit-title" bind:this={heading} tabindex="-1">Edit source</h2>
  <FormField
    label="Name"
    bind:value={name}
    problem={problem("name")}
    {...NO_PASSWORD_MANAGER}
  />
  <div class="fixed">
    <Facts>
      <div>
        <dt>Server</dt>
        <dd><UntrustedText value={source.host} /></dd>
      </div>
      <div>
        <dt>User name</dt>
        <dd><UntrustedText value={source.userName} /></dd>
      </div>
    </Facts>
    <p class="note">
      These can't change. To collect another mailbox, add a source.
    </p>
  </div>
  <SecurityField
    bind:security
    bind:port
    securityProblem={problem("security")}
    portProblem={problem("port")}
  />
  <FormField
    label="Password"
    type="password"
    bind:value={password}
    hint="Leave empty to keep the stored password."
    problem={problem("password")}
    {...NO_PASSWORD_MANAGER}
  />
  <div class="folders">
    <FolderChoice bind:options={folders} problem={problem("folders")} />
    <!-- Stays once the folders are listed, so focus stays on it. Another
         press lists them again. -->
    <button
      class="button large"
      type="button"
      aria-disabled={busy !== null ? "true" : undefined}
      onclick={addFolders}
    >
      {busy === "check" ? "Listing folders…" : "Add folders"}
    </button>
    <p class="note" role="status">{listedLine}</p>
    <FormOutcome
      outcome={checkOutcome}
      action="Add folders"
      ending="Nothing was changed."
    />
  </div>
  {#if listed}
    <RemovalWarning />
  {/if}
  <div class="buttons">
    <button
      class="button large primary"
      type="submit"
      aria-disabled={busy !== null ? "true" : undefined}
    >
      {busy === "save" ? "Saving…" : "Save"}
    </button>
    <button
      class="button large"
      type="button"
      aria-disabled={busy === "save" ? "true" : undefined}
      onclick={cancel}
    >
      Cancel
    </button>
  </div>
  <FormOutcome
    outcome={saveOutcome}
    action="Save"
    ending="Nothing was changed."
  />
</form>

<style>
  form {
    display: grid;
    gap: var(--space-16);
    max-width: var(--form-width-wide);
  }

  h2 {
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  .fixed,
  .folders {
    display: grid;
    gap: var(--space-8);
  }

  .folders > .button {
    justify-self: start;
  }

  .note {
    color: var(--text-3);
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  .buttons {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-8);
  }
</style>
