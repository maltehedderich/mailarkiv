<script lang="ts">
  import FailedLoad from "#lib/components/FailedLoad.svelte";
  import FullHash from "#lib/components/FullHash.svelte";
  import { formatBytes } from "#lib/format/size.js";
  import { formatTime } from "#lib/format/time.js";
  import {
    CATEGORIES,
    UNREFERENCED_NEXT_STEP,
  } from "#lib/status/categories.js";

  import type { PageProps } from "./$types";

  let { data }: PageProps = $props();

  // FR-027: this screen states what the server recorded and offers no action
  // that changes the archive. It never sums the facts up as healthy.
</script>

<svelte:head>
  <title>Status · MailArkiv</title>
</svelte:head>

<main>
  <header>
    <h1>Status</h1>
  </header>

  <div class="body">
    {#if data.session.kind === "ok" && data.status.kind === "ok"}
      {@const { startedAt, recovery, unreferenced, failures } =
        data.status.value}
      <p class="covers">
        This screen covers only the time since the server's latest start. Older
        failures are in the server log.
      </p>

      <section aria-labelledby="started">
        <h2 id="started">Started</h2>
        <div class="content">
          <p class="value">
            <time datetime={startedAt}>{formatTime(startedAt)}</time>
          </p>
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
                <dd>{recovery.tempFilesRemoved}</dd>
              </div>
              <div>
                <dt>Temporary files not removed</dt>
                <dd>{recovery.tempFilesNotRemoved}</dd>
              </div>
              <div>
                <dt>Unreferenced originals found</dt>
                <dd>{recovery.unreferencedFound}</dd>
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

      <section aria-labelledby="unreferenced">
        <h2 id="unreferenced">Unreferenced originals</h2>
        <div class="content">
          {#if unreferenced.length === 0}
            <p class="value">None since the latest start.</p>
          {:else}
            <ul>
              {#each unreferenced as original (original.contentHash)}
                <li>
                  <p class="state">
                    <span class="dot warn"></span>
                    <strong>Unreferenced original</strong>
                  </p>
                  <FullHash hash={original.contentHash} />
                  <p class="what">{formatBytes(original.size)}</p>
                  <p class="next">{UNREFERENCED_NEXT_STEP}</p>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      </section>

      <section aria-labelledby="failures">
        <h2 id="failures">Failures</h2>
        <div class="content">
          {#if failures.length === 0}
            <p class="value">No failures since the latest start.</p>
          {:else}
            <ul>
              {#each failures as failure, index (index)}
                {@const words = CATEGORIES[failure.category]}
                <li>
                  <p class="state">
                    <span class="dot" class:warn={failure.restored}></span>
                    <strong>{words.name}</strong>
                    <time datetime={failure.at}>{formatTime(failure.at)}</time>
                  </p>
                  <p class="what">{words.meaning}</p>
                  {#if failure.messageId !== null || failure.contentHash !== null}
                    <dl class="ids">
                      {#if failure.messageId !== null}
                        <div>
                          <dt>Message ID</dt>
                          <dd class="number">{failure.messageId}</dd>
                        </div>
                      {/if}
                      {#if failure.contentHash !== null}
                        <div>
                          <dt>Content hash</dt>
                          <dd><FullHash hash={failure.contentHash} /></dd>
                        </div>
                      {/if}
                    </dl>
                  {/if}
                  <p class="next">
                    {failure.restored
                      ? "The stored copy was restored from an imported file."
                      : words.nextStep}
                  </p>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      </section>
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

  .covers {
    padding-bottom: var(--space-16);
    color: var(--text-2);
  }

  /* A 136 px label column, then the content, with a line above each section. */
  section {
    display: grid;
    grid-template-columns: 136px minmax(0, 1fr);
    gap: var(--space-8) var(--space-16);
    padding: var(--space-16) 0;
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
  .number,
  dd {
    font-variant-numeric: tabular-nums;
  }

  .note {
    color: var(--text-3);
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  dl {
    display: grid;
    gap: var(--space-4);
    margin: 0;
  }

  dl > div {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    column-gap: var(--space-16);
  }

  dt {
    color: var(--text-3);
  }

  dd {
    margin: 0;
    color: var(--text-1);
  }

  .counts > div {
    grid-template-columns: 200px 40px;
  }

  .counts dd {
    text-align: right;
  }

  .ids > div {
    grid-template-columns: 96px minmax(0, 1fr);
  }

  ul {
    display: grid;
    gap: var(--space-20);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  li {
    display: grid;
    gap: var(--space-6);
  }

  /* The state word and its dot, with the time. */
  .state {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0 var(--space-8);
  }

  .state strong {
    font-weight: var(--weight-medium);
  }

  .state time {
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
