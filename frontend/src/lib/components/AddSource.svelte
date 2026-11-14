<script lang="ts">
  import CloseIcon from "~icons/lucide/x";
  import { tick } from "svelte";

  import { beforeNavigate, goto } from "$app/navigation";
  import { page } from "$app/state";

  import {
    addSource,
    checkNewSource,
    type Connection,
    type Security,
    type SourceField,
  } from "#lib/api.js";
  import { signInPath } from "#lib/session.js";
  import { SECURITY_WORDS } from "#lib/sources/describe.js";
  import {
    chosenNames,
    DEFAULT_PORTS,
    FIELD_PROBLEMS,
    firstChoice,
    isValidName,
    NO_PASSWORD_MANAGER,
    parsePort,
    type FolderOption,
    type FormOutcome as Outcome,
  } from "#lib/sources/form.js";
  import { setDrawerOpen } from "#lib/toasts/store.svelte.js";

  import DiscardSource from "./DiscardSource.svelte";
  import FolderChoice from "./FolderChoice.svelte";
  import FormField from "./FormField.svelte";
  import FormOutcome from "./FormOutcome.svelte";
  import RemovalWarning from "./RemovalWarning.svelte";
  import SecurityField from "./SecurityField.svelte";
  import Toasts from "./Toasts.svelte";
  import UntrustedText from "./UntrustedText.svelte";

  // Adds a source in two steps over the Sources list: sign in to the mailbox,
  // then name it and choose its folders. Each opening starts empty.

  let {
    open,
    onclose,
  }: {
    open: boolean;
    /** Called once the drawer has closed by Escape, Close, Cancel, or Back. */
    onclose: () => void;
  } = $props();

  const id = $props.id();

  let host = $state("");
  let security: Security = $state("tls");
  let port = $state(String(DEFAULT_PORTS.tls));
  let userName = $state("");
  let password = $state("");
  let name = $state("");

  let step: "connect" | "folders" = $state("connect");
  /** The details Connect signed in with, which Save sends. */
  let connected: Connection | null = $state(null);
  let folders: FolderOption[] = $state([]);
  let busy: "connect" | "save" | null = $state(null);
  let outcome: Outcome | null = $state(null);
  /** The fields the last attempt refused, each with a line under it. */
  let refused: SourceField[] = $state([]);

  /** The name Connect filled in, replaced on a later Connect unless edited. */
  let suggestedName = "";
  /** Counts openings, so an answer to an earlier one is dropped. */
  let opening = 0;
  let allowLeave = false;
  const dirty = $derived(
    host !== "" ||
      userName !== "" ||
      password !== "" ||
      name !== "" ||
      security !== "tls" ||
      port !== String(DEFAULT_PORTS.tls) ||
      connected !== null,
  );

  let dialog: HTMLDialogElement;
  let discardDialog: DiscardSource;
  let body: HTMLDivElement;
  let outcomeBox: HTMLDivElement;
  let connectForm: HTMLFormElement | undefined = $state();
  let foldersForm: HTMLFormElement | undefined = $state();
  let foldersHeading: HTMLHeadingElement | undefined = $state();

  function reset() {
    allowLeave = false;
    host = "";
    security = "tls";
    port = String(DEFAULT_PORTS.tls);
    userName = "";
    password = "";
    name = "";
    suggestedName = "";
    step = "connect";
    connected = null;
    folders = [];
    busy = null;
    outcome = null;
    refused = [];
  }

  // The address decides whether the drawer is open, so Back and a direct
  // address work.
  $effect(() => {
    if (open && !dialog.open) {
      const asked = ++opening;
      reset();
      dialog.inert = false;
      dialog.removeAttribute("aria-hidden");
      dialog.showModal();
      setDrawerOpen(true);
      // showModal() would focus Close, the first control. The form starts at
      // its first field.
      void tick().then(() => {
        if (asked === opening && dialog.open) {
          dialog.querySelector<HTMLInputElement>("form input")?.focus();
        }
      });
    }
    if (!open && dialog.open) dialog.close();
  });

  // Leaving the page with the drawer open fires no close event.
  $effect(() => () => {
    opening += 1;
    setDrawerOpen(false);
  });

  beforeNavigate((navigation) => {
    // Session expiry must always be able to remove the signed-in page.
    if (
      !dialog?.open ||
      allowLeave ||
      navigation.to?.url.pathname === "/sign-in"
    )
      return;
    if (busy !== "save" && !dirty) return;
    navigation.cancel();
    // Leaving the document uses the browser's own unload confirmation.
    if (busy === "save" || navigation.type === "leave") return;
    discardDialog.open(() => {
      allowLeave = true;
      if (navigation.type === "popstate") {
        window.history.go(navigation.delta);
      } else if (navigation.to !== null) {
        if (navigation.willUnload)
          window.location.href = navigation.to.url.href;
        else void goto(navigation.to.url, { reset: false });
      }
    });
  });

  // The drawer is still on screen as it slides out, so it leaves screen
  // readers and the pointer now.
  function closed() {
    opening += 1;
    dialog.inert = true;
    dialog.setAttribute("aria-hidden", "true");
    setDrawerOpen(false);
    onclose();
  }

  // A save that ends unseen could add a source nobody knows about, so the
  // drawer stays until it's answered. A check saves nothing and may be left.
  function cancelled(event: Event) {
    event.preventDefault();
    close();
  }

  function close() {
    if (busy === "save") return;
    if (dirty) discardDialog.open(() => dialog.close());
    else dialog.close();
  }

  let pressedOnScrim = false;
  function onScrim(event: MouseEvent): boolean {
    const bounds = dialog.getBoundingClientRect();
    return (
      event.target === dialog &&
      (event.clientX < bounds.left ||
        event.clientX > bounds.right ||
        event.clientY < bounds.top ||
        event.clientY > bounds.bottom)
    );
  }
  function pressed(event: PointerEvent) {
    pressedOnScrim = onScrim(event);
  }
  function clicked(event: MouseEvent) {
    if (pressedOnScrim && onScrim(event)) close();
    pressedOnScrim = false;
  }

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

  /** Shows the outcome below the fields, where it appeared. */
  async function show(next: Outcome) {
    outcome = next;
    await tick();
    outcomeBox.scrollIntoView({ block: "nearest" });
  }

  async function connect(event: SubmitEvent) {
    event.preventDefault();
    // aria-disabled, not disabled, keeps focus on the button while it waits.
    if (busy !== null) return;
    outcome = null;
    refused = [];

    const details: Connection = {
      host: host.trim(),
      port: parsePort(port),
      security,
      userName,
      password,
    };
    const asked = opening;
    busy = "connect";
    const result = await checkNewSource(details);
    if (asked !== opening) return;
    busy = null;
    switch (result.kind) {
      case "ok":
        connected = details;
        folders = firstChoice(result.value);
        if ((name === "" || name === suggestedName) && isValidName(userName)) {
          name = suggestedName = userName;
        }
        step = "folders";
        await tick();
        body.scrollTop = 0;
        foldersHeading?.focus();
        return;
      case "session_ended":
        await goto(signInPath(page.url));
        return;
      case "invalid_fields":
        if (result.fields.length === 0) await show({ kind: "unavailable" });
        else await refuse(result.fields, connectForm);
        return;
      default:
        await show(result);
    }
  }

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (busy !== null || connected === null) return;
    outcome = null;
    refused = [];
    const nameRefused = !isValidName(name);
    const chosen = chosenNames(folders);
    if (nameRefused || chosen.length === 0) {
      await refuse(
        [
          ...(nameRefused ? (["name"] as const) : []),
          ...(chosen.length === 0 ? (["folders"] as const) : []),
        ],
        foldersForm,
      );
      return;
    }

    busy = "save";
    const asked = opening;
    const result = await addSource({ ...connected, name, folders: chosen });
    if (asked !== opening) return;
    switch (result.kind) {
      case "ok":
        // Still busy until the source's page replaces the list.
        allowLeave = true;
        await goto(`/sources/${result.value.id}`);
        return;
      case "session_ended":
        await goto(signInPath(page.url));
        return;
      case "invalid_fields":
        busy = null;
        if (result.fields.length === 0) {
          await show({ kind: "unavailable" });
        } else if (
          result.fields.every(
            (field) => field === "name" || field === "folders",
          )
        ) {
          await refuse(result.fields, foldersForm);
        } else {
          step = "connect";
          await tick();
          await refuse(result.fields, connectForm);
        }
        return;
      default:
        busy = null;
        await show(result);
    }
  }

  async function changeDetails() {
    if (busy !== null) return;
    outcome = null;
    refused = [];
    step = "connect";
    await tick();
    body.scrollTop = 0;
    connectForm?.querySelector<HTMLInputElement>("input")?.focus();
  }
</script>

<dialog
  bind:this={dialog}
  class="drawer"
  aria-labelledby="{id}-kind {id}-title"
  onclose={closed}
  oncancel={cancelled}
  onpointerdown={pressed}
  onclick={clicked}
>
  <header>
    <div class="titles">
      <p id="{id}-kind" class="kind">
        <span>Add a source</span>
        <span class="steps">Step {step === "connect" ? 1 : 2} of 2</span>
      </p>
      {#if step === "connect"}
        <h2 id="{id}-title">Connect to the mailbox</h2>
      {:else}
        <h2 id="{id}-title" bind:this={foldersHeading} tabindex="-1">
          Choose folders
        </h2>
      {/if}
    </div>
    <button
      class="button ghost icon-only"
      type="button"
      aria-label="Close"
      aria-disabled={busy === "save" ? "true" : undefined}
      onclick={close}
    >
      <CloseIcon />
    </button>
  </header>

  <div class="main">
    <div class="body" bind:this={body}>
      {#if step === "connect" || connected === null}
        <form
          id="{id}-connect"
          bind:this={connectForm}
          aria-labelledby="{id}-title"
          novalidate
          onsubmit={connect}
        >
          <p class="intro">
            Connect signs in and lists the mailbox's folders. Nothing is saved
            until you choose them.
          </p>
          <FormField
            label="Server"
            bind:value={host}
            problem={problem("host")}
            {...NO_PASSWORD_MANAGER}
            autocapitalize="none"
            spellcheck="false"
          />
          <SecurityField
            bind:security
            bind:port
            securityProblem={problem("security")}
            portProblem={problem("port")}
          />
          <!-- The mailbox's own sign-in, so a password manager may fill it. -->
          <FormField
            label="User name"
            bind:value={userName}
            problem={problem("user_name")}
            autocomplete="username"
            autocapitalize="none"
            spellcheck="false"
          />
          <FormField
            label="Password"
            type="password"
            bind:value={password}
            problem={problem("password")}
            autocomplete="current-password"
          />
        </form>
      {:else}
        <form
          id="{id}-folders"
          bind:this={foldersForm}
          aria-labelledby="{id}-title"
          novalidate
          onsubmit={save}
        >
          <div class="mailbox">
            <p>
              <UntrustedText value={connected.userName} /> on
              <span class="unit"><UntrustedText value={connected.host} />,</span
              >
              port {connected.port}, {SECURITY_WORDS[connected.security]}
            </p>
            <button
              class="button"
              type="button"
              aria-disabled={busy !== null ? "true" : undefined}
              onclick={changeDetails}
            >
              Change details
            </button>
          </div>
          <FormField
            label="Name"
            bind:value={name}
            hint="Shown in the source list and in each message's history."
            problem={problem("name")}
            autocomplete="off"
          />
          <FolderChoice bind:options={folders} problem={problem("folders")} />
        </form>
      {/if}
      <div class="result" bind:this={outcomeBox}>
        <FormOutcome
          {outcome}
          action={step === "connect" ? "Connect" : "Save"}
          ending="Nothing was saved."
        />
      </div>
    </div>
    <!-- The page's toasts are inert behind this modal, so the drawer has its
         own. -->
    <Toasts place="drawer" />
  </div>

  <footer>
    {#if step === "folders"}
      <RemovalWarning />
    {/if}
    <div class="buttons">
      <button
        class="button large"
        type="button"
        aria-disabled={busy === "save" ? "true" : undefined}
        onclick={close}
      >
        Cancel
      </button>
      {#if step === "connect"}
        <button
          class="button large primary"
          type="submit"
          form="{id}-connect"
          aria-disabled={busy !== null ? "true" : undefined}
        >
          {busy === "connect" ? "Connecting…" : "Connect"}
        </button>
      {:else}
        <button
          class="button large primary"
          type="submit"
          form="{id}-folders"
          aria-disabled={busy !== null ? "true" : undefined}
        >
          {busy === "save" ? "Saving…" : "Save"}
        </button>
      {/if}
    </div>
  </footer>
</dialog>

<DiscardSource bind:this={discardDialog} />

<style>
  dialog {
    grid-template-rows: auto minmax(0, 1fr) auto;
  }

  /* What the drawer does, small, over the step it's on, as the history
     drawer names what it shows over which message. */
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-12);
    min-height: var(--header-height);
    padding: var(--space-8) var(--space-12) var(--space-8) var(--space-16);
    border-bottom: 1px solid var(--line);
  }

  .titles {
    display: grid;
    min-width: 0;
  }

  .kind {
    color: var(--text-3);
    font-size: var(--size-label);
    font-weight: var(--weight-medium);
    line-height: var(--leading-label);
  }

  .steps {
    font-variant-numeric: tabular-nums;
  }

  /* Empty alt text, so screen readers don't say the dot. */
  .steps::before {
    content: "·" / "";
    margin-right: var(--space-4);
  }

  h2 {
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
  }

  /* Holds the scrolling body and the toasts over its end. */
  .main {
    position: relative;
    display: grid;
    min-height: 0;
  }

  /* --toast-room is set by the toasts, so their stack never hides the end of
     the form or a focused control in it. */
  .body {
    padding: var(--space-16);
    padding-bottom: calc(var(--space-16) + var(--toast-room, 0px));
    overflow-y: auto;
    overscroll-behavior: contain;
    scroll-padding-bottom: var(--toast-room, 0px);
  }

  form {
    display: grid;
    gap: var(--space-16);
    min-width: 0;
  }

  .intro {
    color: var(--text-3);
    text-wrap: pretty;
  }

  /* Empty until an attempt fails, and only then apart from the fields. */
  .result:has(:global(p)) {
    margin-top: var(--space-20);
  }

  .mailbox {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-8) var(--space-12);
    min-width: 0;
    padding-bottom: var(--space-16);
    border-bottom: 1px solid var(--line);
  }

  .mailbox p {
    flex: 1 1 0;
    min-width: 12em;
    color: var(--text-2);
    overflow-wrap: anywhere;
  }

  /* The server and its comma break together, and a long name still cuts in
     its middle. */
  .unit {
    display: inline-flex;
    max-width: 100%;
    min-width: 0;
    vertical-align: bottom;
  }

  /* The warning sits with Save, so a long folder list can't push it out of
     view. */
  footer {
    display: grid;
    gap: var(--space-12);
    padding: var(--space-12) var(--space-16);
    border-top: 1px solid var(--line);
  }

  .buttons {
    display: flex;
    justify-content: flex-end;
    gap: var(--space-8);
  }
</style>
