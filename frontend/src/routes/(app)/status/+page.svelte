<script lang="ts">
  import { page } from "$app/state";

  import FailedLoad from "#lib/components/FailedLoad.svelte";
  import FullHash from "#lib/components/FullHash.svelte";
  import UntrustedText from "#lib/components/UntrustedText.svelte";
  import { formatBytes } from "#lib/format/size.js";
  import { formatTime } from "#lib/format/time.js";
  import { historyHref } from "#lib/history.js";
  import { problemTone } from "#lib/sources/describe.js";
  import { CATEGORY_WORDS } from "#lib/sources/words.js";
  import {
    CATEGORIES,
    RESTORED_NOTE,
    UNREFERENCED_MEANING,
    UNREFERENCED_NEXT_STEP,
  } from "#lib/status/categories.js";
  import { groupFailures } from "#lib/status/groups.js";

  import type { PageProps } from "./$types";

  let { data }: PageProps = $props();

  // This screen states what the server recorded and offers no action that
  // changes the archive. It never sums the facts up as healthy.

  // Separators, as the Status item's count shows them.
  const numbers = new Intl.NumberFormat("en-US");

  const loaded = $derived(
    data.session.kind === "ok" && data.status.kind === "ok",
  );

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
  <title>Status · MailArkiv</title>
</svelte:head>

<main>
  <header>
    <h1 bind:this={heading} tabindex="-1">Status</h1>
  </header>

  <div class="body">
    {#if data.session.kind === "ok" && data.status.kind === "ok"}
      {@const { startedAt, recovery, unreferenced, failures, sources } =
        data.status.value}
      {@const groups = groupFailures(failures)}
      <!-- What the Status item counts comes first, because that count is why
           most people open this screen. -->
      <section aria-labelledby="problems">
        <h2 id="problems">Problems</h2>
        <div class="content">
          {#if groups.length === 0 && unreferenced.length === 0 && sources.length === 0}
            <p class="value">
              No failures and no unreferenced originals since the latest start.
            </p>
          {:else}
            <ul class="groups">
              <!-- A source's problem lasts until a sync clears it, so these
                   come before what happened since the start. The name links
                   to the source's page, which has the folder and the reply. -->
              {#each sources as source (source.id)}
                <li class="group">
                  <h3 class="state">
                    <span
                      class="dot"
                      class:warn={problemTone(source.category) === "warn"}
                    ></span>
                    <a class="name" href="/sources/{source.id}"
                      ><UntrustedText value={source.name} /></a
                    >
                  </h3>
                  <p class="what">{CATEGORY_WORDS[source.category].label}</p>
                  <p class="when">
                    <span
                      >Since <time datetime={source.since}
                        >{formatTime(source.since)}</time
                      ></span
                    >
                    {#if source.failedAttempts !== null}
                      <span>
                        {numbers.format(source.failedAttempts)}
                        {source.failedAttempts === 1
                          ? "failed attempt"
                          : "failed attempts"}
                      </span>
                    {/if}
                  </p>
                </li>
              {/each}
              {#each groups as group (group.key)}
                {@const words = CATEGORIES[group.category]}
                <li class="group">
                  <h3 class="state">
                    <span class="dot" class:warn={group.restored}></span>
                    <span class="name">
                      {words.name}{group.restored ? ", restored" : ""}
                    </span>
                    {#if group.events.length > 1}
                      <span class="count">
                        {numbers.format(group.events.length)} times
                      </span>
                    {/if}
                  </h3>
                  <p class="what">{words.meaning}</p>
                  <p class="next">
                    {group.restored ? RESTORED_NOTE : words.nextStep}
                  </p>
                  <!-- A failure that names a message links to its history,
                       which has the file name and the full hash. A hash that
                       names nothing else shows in full. -->
                  <ol class="events">
                    {#each group.events as event, index (index)}
                      {@const full =
                        event.messageId === null && event.contentHash !== null}
                      <li class:full>
                        <p class="when">
                          <time datetime={event.at}>{formatTime(event.at)}</time
                          >
                          {#if event.source !== null}
                            <!-- A message event names its source, never the
                                 folder or the message. -->
                            <span class="source"
                              ><span class="visually-hidden">{"Source "}</span
                              ><UntrustedText value={event.source.name} /></span
                            >
                          {/if}
                          {#if event.messageId !== null}
                            <!-- The short hash has a fixed width, so it goes
                                 first and the links line up. -->
                            {#if event.contentHash !== null}
                              <span class="hash"
                                ><span class="visually-hidden">{"Hash "}</span
                                >{event.contentHash.slice(0, 8)}</span
                              >
                            {/if}
                            <a href={historyHref(page.url, event.messageId)}>
                              Message {event.messageId}
                            </a>
                          {/if}
                        </p>
                        {#if full && event.contentHash !== null}
                          <FullHash hash={event.contentHash} />
                        {/if}
                      </li>
                    {/each}
                  </ol>
                </li>
              {/each}
              {#if unreferenced.length > 0}
                <li class="group">
                  <h3 class="state">
                    <span class="dot warn"></span>
                    <span class="name">
                      {unreferenced.length === 1
                        ? "Unreferenced original"
                        : "Unreferenced originals"}
                    </span>
                    {#if unreferenced.length > 1}
                      <span class="count">
                        {numbers.format(unreferenced.length)}
                      </span>
                    {/if}
                  </h3>
                  <p class="what">{UNREFERENCED_MEANING}</p>
                  <p class="next">{UNREFERENCED_NEXT_STEP}</p>
                  <ul class="events">
                    {#each unreferenced as original (original.contentHash)}
                      <li class="full">
                        <p class="when">{formatBytes(original.size)}</p>
                        <FullHash hash={original.contentHash} />
                      </li>
                    {/each}
                  </ul>
                </li>
              {/if}
            </ul>
          {/if}
        </div>
      </section>

      <section aria-labelledby="recovery">
        <h2 id="recovery">Recovery</h2>
        <div class="content">
          {#if recovery.tempFilesRemoved === 0 && recovery.tempFilesNotRemoved === 0 && recovery.unreferencedFound === 0}
            <p class="value">
              Found no leftover temporary files and no unreferenced originals.
            </p>
          {:else}
            <dl class="counts">
              <div>
                <dt>Temporary files removed</dt>
                <dd>{numbers.format(recovery.tempFilesRemoved)}</dd>
              </div>
              <div>
                <dt>Temporary files not removed</dt>
                <dd>{numbers.format(recovery.tempFilesNotRemoved)}</dd>
              </div>
              <div>
                <dt>Unreferenced originals found</dt>
                <dd>{numbers.format(recovery.unreferencedFound)}</dd>
              </div>
            </dl>
          {/if}
          <p class="note">
            Finished at
            <time datetime={recovery.finishedAt}
              >{formatTime(recovery.finishedAt)}</time
            >
          </p>
        </div>
      </section>

      <!-- Scope and start time belong in a footnote: they qualify
           everything above rather than leading it. -->
      <p class="scope">
        This screen covers only the time since the server started at
        <time datetime={startedAt}>{formatTime(startedAt)}</time>. Older
        failures are in the server log.
        {#if sources.length > 0}
          A source stays listed until a sync clears its problem, even one that
          began before the start.
        {/if}
      </p>
    {:else}
      <FailedLoad />
    {/if}
  </div>
</main>

<style>
  header {
    display: flex;
    align-items: center;
    height: var(--header-height);
    padding: 0 var(--space-24);
    border-bottom: 1px solid var(--line);
  }

  h1 {
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
  }

  .body {
    max-width: var(--content-width);
    margin: 0 auto;
    padding: var(--space-24);
  }

  /* A 136 px label column, then the content, with a line between sections. */
  section {
    display: grid;
    grid-template-columns: 136px minmax(0, 1fr);
    gap: var(--space-8) var(--space-16);
    padding: var(--space-16) 0;
  }

  /* The view header's line already sits above the first section. */
  section:first-child {
    padding-top: 0;
  }

  section + section {
    border-top: 1px solid var(--line);
  }

  h2 {
    color: var(--text-2);
    font-weight: var(--weight-medium);
  }

  .content {
    display: grid;
    align-content: start;
    gap: var(--space-8);
    min-width: 0;
  }

  .value {
    color: var(--text-1);
  }

  time,
  dd,
  .count {
    font-variant-numeric: tabular-nums;
  }

  .note {
    color: var(--text-3);
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  ul,
  ol {
    display: grid;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .groups {
    gap: var(--space-24);
  }

  .group {
    display: grid;
    gap: var(--space-4);
  }

  /* The state word and its dot, with how many times it happened. */
  .state {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0 var(--space-8);
  }

  .name {
    font-weight: var(--weight-medium);
  }

  /* A source name, cut in its middle when the line is too narrow. */
  a.name {
    display: inline-flex;
    min-width: 0;
    max-width: 100%;
  }

  .count {
    color: var(--text-3);
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  .dot {
    flex: none;
    width: var(--dot-size);
    height: var(--dot-size);
    border-radius: 50%;
    background: var(--state-bad);
  }

  .dot.warn {
    background: var(--state-warn);
  }

  .what {
    color: var(--text-2);
  }

  .next {
    color: var(--text-3);
  }

  /* Each time it happened, one line each, so a long run of the same failure
     stays short. A full hash takes more room, so it gets some around it. */
  .events {
    gap: var(--space-4);
    margin-top: var(--space-8);
  }

  .events li {
    display: grid;
    gap: var(--space-4);
  }

  .events li.full:not(:first-child),
  .events li.full + li {
    margin-top: var(--space-8);
  }

  /* A source name is untrusted text in --text-1, cut in its middle when the
     line is too narrow. */
  .source {
    min-width: 0;
    max-width: 100%;
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  .hash {
    font-family: var(--font-mono);
    font-variant-ligatures: none;
  }

  .when {
    display: flex;
    flex-wrap: wrap;
    gap: 0 var(--space-12);
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    line-height: var(--leading-label);
  }

  dl {
    display: grid;
    gap: var(--space-4);
    margin: 0;
  }

  .counts > div {
    display: grid;
    grid-template-columns: 200px 40px;
    column-gap: var(--space-16);
  }

  dt {
    color: var(--text-3);
  }

  dd {
    margin: 0;
    color: var(--text-1);
    text-align: right;
  }

  /* Small, under a line, because it qualifies the sections above. */
  .scope {
    padding-top: var(--space-16);
    border-top: 1px solid var(--line);
    color: var(--text-3);
    font-size: var(--size-label);
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

    section {
      grid-template-columns: minmax(0, 1fr);
    }

    .counts > div {
      grid-template-columns: minmax(0, 1fr) 40px;
    }
  }
</style>
