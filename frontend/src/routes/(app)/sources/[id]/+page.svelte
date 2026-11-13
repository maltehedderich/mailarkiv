<script lang="ts">
  import ChevronRightIcon from "~icons/lucide/chevron-right";

  import { tick } from "svelte";

  import { goto, invalidate } from "$app/navigation";
  import { page } from "$app/state";

  import {
    pauseSource,
    resumeSource,
    syncSource,
    type SourceState as State,
  } from "#lib/api.js";
  import CategoryFacts from "#lib/components/CategoryFacts.svelte";
  import EditSource from "#lib/components/EditSource.svelte";
  import Facts from "#lib/components/Facts.svelte";
  import FailedLoad from "#lib/components/FailedLoad.svelte";
  import ProblemMessages from "#lib/components/ProblemMessages.svelte";
  import RemoveSource from "#lib/components/RemoveSource.svelte";
  import SourceFacts from "#lib/components/SourceFacts.svelte";
  import SourceState from "#lib/components/SourceState.svelte";
  import UntrustedText from "#lib/components/UntrustedText.svelte";
  import { formatTime } from "#lib/format/time.js";
  import { signInPath } from "#lib/session.js";
  import { REFRESH_MS, SOURCES_DATA } from "#lib/sources/describe.js";
  import { CATEGORY_WORDS, STATE_WORDS } from "#lib/sources/words.js";

  import type { PageProps } from "./$types";

  let { data }: PageProps = $props();

  const numbers = new Intl.NumberFormat("en-US");

  const loaded = $derived(
    data.session.kind === "ok" && data.source.kind === "ok",
  );

  // First strong isolate and pop directional isolate around the name, so a
  // right-to-left name or a bidi control in it can't reorder the rest of the
  // title.
  const ISOLATE = String.fromCodePoint(0x2068);
  const END_ISOLATE = String.fromCodePoint(0x2069);
  const title = $derived(
    data.source.kind === "ok"
      ? `${ISOLATE}${data.source.value.name}${END_ISOLATE} · Sources · MailArkiv`
      : "Source · MailArkiv",
  );

  let editing = $state(false);

  // While the source syncs, reload so its archived count climbs. Each load
  // gives a new result, so the next timer starts only once the last load
  // has finished. Not while editing: a failed load would take the form.
  $effect(() => {
    const result = data.source;
    if (editing) return;
    if (result.kind !== "ok" || result.value.state !== "syncing") return;
    const timer = setTimeout(() => void invalidate(SOURCES_DATA), REFRESH_MS);
    return () => clearTimeout(timer);
  });

  type Action = "sync" | "pause" | "resume";
  const ACTION_NAMES: Record<Action, string> = {
    sync: "Sync now",
    pause: "Pause",
    resume: "Resume",
  };

  let busy: Action | null = $state(null);
  /** A refusal or failure of the last action, until the state changes. */
  let actionLine: string | null = $state(null);
  /** Said by the hidden status each time the state changes. */
  let announcement = $state("");
  let lastState: State | null = null;
  $effect(() => {
    const state = data.source.kind === "ok" ? data.source.value.state : null;
    if (state !== null && lastState !== null && state !== lastState) {
      announcement = `State: ${STATE_WORDS[state]}`;
      actionLine = null;
    }
    lastState = state;
  });

  // The answer carries the new state, but a reload already in flight may
  // still bring the old one. A reload started now supersedes it.
  async function act(action: Action, id: number) {
    if (busy !== null) return;
    busy = action;
    actionLine = null;
    const run = { sync: syncSource, pause: pauseSource, resume: resumeSource };
    const result = await run[action](String(id));
    switch (result.kind) {
      case "ok":
        await invalidate(SOURCES_DATA);
        break;
      case "session_ended":
        await goto(signInPath(page.url));
        return;
      case "already_syncing":
        actionLine = "A sync is already running.";
        break;
      case "paused":
        actionLine = "The source is paused. Resume it to sync.";
        break;
      case "no_key":
        actionLine =
          "Collection needs the server's key, and the server started without one.";
        break;
      case "unavailable":
        actionLine = `${ACTION_NAMES[action]} didn't finish. Try again. If this repeats, check the server log.`;
    }
    busy = null;
  }

  let editButton: HTMLButtonElement | undefined = $state();
  let removal: RemoveSource | undefined = $state();

  async function stopEditing() {
    editing = false;
    await tick();
    editButton?.focus();
  }

  async function saved() {
    await invalidate(SOURCES_DATA);
    await stopEditing();
  }

  // Try again went away with the failed-load block, so focus moves on purpose
  // instead of falling to the page.
  let heading: HTMLHeadingElement;
  let wasLoaded: boolean | null = null;
  $effect(() => {
    if (wasLoaded === false && loaded) {
      if (document.activeElement === document.body) heading.focus();
    }
    wasLoaded = loaded;
  });
</script>

<svelte:head>
  <title>{title}</title>
</svelte:head>

<main>
  <header>
    <a class="up" href="/sources">Sources</a>
    <ChevronRightIcon class="icon" />
    <h1 bind:this={heading} tabindex="-1">
      {#if data.source.kind === "ok"}
        <UntrustedText value={data.source.value.name} />
      {:else}
        Source
      {/if}
    </h1>
  </header>

  <div class="body">
    {#if data.session.kind === "ok" && data.source.kind === "ok"}
      {@const source = data.source.value}
      {@const unavailable = source.folders.some(
        (folder) => folder.unavailableSince !== null,
      )}
      {#if editing}
        <EditSource {source} onsaved={saved} oncancel={stopEditing} />
      {:else}
        <section aria-labelledby="state">
          <h2 id="state">State</h2>
          <div class="content">
            <SourceState {source} />
            {#if source.failure !== null}
              {@const failure = source.failure}
              <div class="failure">
                <h3>{CATEGORY_WORDS[failure.category].label}</h3>
                <Facts>
                  <CategoryFacts category={failure.category} />
                  <div>
                    <dt>Failing since</dt>
                    <dd>
                      <time datetime={failure.since}
                        >{formatTime(failure.since)}</time
                      >
                    </dd>
                  </div>
                  {#if failure.failedAttempts !== null}
                    <div>
                      <dt>Failed attempts</dt>
                      <dd>{numbers.format(failure.failedAttempts)}</dd>
                    </div>
                  {/if}
                  {#if failure.lastAt !== null}
                    <div>
                      <dt>Last attempt</dt>
                      <dd>
                        <time datetime={failure.lastAt}
                          >{formatTime(failure.lastAt)}</time
                        >
                      </dd>
                    </div>
                  {/if}
                  {#if failure.reply !== null}
                    <div>
                      <dt>Server's reply</dt>
                      <dd class="reply">
                        <UntrustedText value={failure.reply} full />
                      </dd>
                    </div>
                  {/if}
                </Facts>
              </div>
            {/if}
            <!-- Pause and Resume are one button, so focus stays on it when
               the state changes its label. -->
            <div class="actions">
              <button
                class="button"
                type="button"
                disabled={source.state === "paused"}
                aria-disabled={busy !== null ? "true" : undefined}
                onclick={() => act("sync", source.id)}
              >
                Sync now
              </button>
              <button
                class="button"
                type="button"
                aria-disabled={busy !== null ? "true" : undefined}
                onclick={() =>
                  act(
                    source.state === "paused" ? "resume" : "pause",
                    source.id,
                  )}
              >
                {source.state === "paused" ? "Resume" : "Pause"}
              </button>
            </div>
            <p class="action-line" role="status">{actionLine ?? ""}</p>
            <p class="visually-hidden" role="status">{announcement}</p>
          </div>
        </section>

        <section aria-labelledby="source">
          <h2 id="source">Source</h2>
          <div class="content">
            <SourceFacts {source} />
            <div class="actions">
              <button
                bind:this={editButton}
                class="button"
                type="button"
                onclick={() => (editing = true)}
              >
                Edit
              </button>
              <button
                class="button"
                type="button"
                onclick={() => removal?.open()}
              >
                Remove
              </button>
            </div>
            <RemoveSource
              bind:this={removal}
              id={source.id}
              name={source.name}
            />
          </div>
        </section>

        <section aria-labelledby="folders">
          <h2 id="folders">Folders</h2>
          <div class="content">
            <ul class="folders">
              {#each source.folders as folder (folder.name)}
                <li>
                  <p class="folder">
                    {#if folder.unavailableSince !== null}
                      <span class="dot warn"></span>
                    {/if}
                    <span class="name"
                      ><UntrustedText value={folder.name} /></span
                    >
                  </p>
                  {#if folder.unavailableSince !== null}
                    <p class="note">
                      Unavailable since
                      <time datetime={folder.unavailableSince}
                        >{formatTime(folder.unavailableSince)}</time
                      >
                    </p>
                  {/if}
                  {#if folder.renumberedAt !== null}
                    <p class="note">
                      Renumbered by the server
                      <time datetime={folder.renumberedAt}
                        >{formatTime(folder.renumberedAt)}</time
                      >, so MailArkiv collected it again
                    </p>
                  {/if}
                </li>
              {/each}
            </ul>
            {#if unavailable}
              <div class="failure">
                <h3>{CATEGORY_WORDS.folder_unavailable.label}</h3>
                <Facts><CategoryFacts category="folder_unavailable" /></Facts>
              </div>
            {/if}
          </div>
        </section>

        <ProblemMessages
          id="waiting"
          title="Waiting for retry"
          list={source.messages.waitingForRetry}
        />
        <ProblemMessages
          id="not-archived"
          title="Not archived"
          list={source.messages.notArchived}
        />
      {/if}
    {:else}
      <FailedLoad />
    {/if}
  </div>
</main>

<style>
  header {
    display: flex;
    gap: var(--space-4);
    align-items: center;
    height: var(--header-height);
    padding: 0 var(--space-24);
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
    display: inline-flex;
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

  /* A 136 px label column, then the content, with a line between sections,
     as on Status. The message lists are sections too. */
  .body > :global(section) {
    display: grid;
    grid-template-columns: 136px minmax(0, 1fr);
    gap: var(--space-8) var(--space-16);
    padding: var(--space-16) 0;
  }

  /* The view header's line already sits above the first section. */
  .body > :global(section:first-child) {
    padding-top: 0;
  }

  .body > :global(section + section) {
    border-top: 1px solid var(--line);
  }

  .body > :global(section > h2) {
    color: var(--text-2);
    font-weight: var(--weight-medium);
  }

  .body > :global(section > .content) {
    display: grid;
    align-content: start;
    gap: var(--space-8);
    min-width: 0;
  }

  .failure {
    display: grid;
    gap: var(--space-4);
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-8);
    margin-top: var(--space-4);
  }

  .action-line {
    color: var(--text-2);
  }

  h3 {
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  /* Evidence an administrator reads in full, so it keeps the body colour. */
  .reply {
    color: var(--text-1);
  }

  /* One track that may shrink, so a long folder name is cut, not widening
     the page. */
  .folders {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-4);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .folder {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    min-width: 0;
  }

  .name {
    display: inline-flex;
    min-width: 0;
    max-width: 100%;
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  .dot {
    flex: none;
    width: var(--dot-size);
    height: var(--dot-size);
    border-radius: 50%;
  }

  .dot.warn {
    background: var(--state-warn);
  }

  .note {
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    line-height: var(--leading-label);
  }

  /* DESIGN.md: 16 px gutters, and each label moves above its content. */
  @media (max-width: 720px) {
    header {
      padding: 0 var(--space-16);
    }

    .body {
      padding: var(--space-16);
    }

    .body > :global(section) {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
