<script lang="ts">
  import { KEY_GUIDE_URL } from "#lib/sources/describe.js";
  import type { FormOutcome } from "#lib/sources/form.js";
  import { CATEGORY_WORDS } from "#lib/sources/words.js";

  import Facts from "./Facts.svelte";
  import UntrustedText from "./UntrustedText.svelte";

  // What an attempt that didn't succeed ended in, below the button that made
  // it. The status is always there, so a new outcome is announced.

  let {
    outcome,
    action,
    ending,
  }: {
    outcome: FormOutcome | null;
    /** The button's name, such as Connect. */
    action: string;
    /** What the attempt left as it was, such as "Nothing was saved." */
    ending: string;
  } = $props();
</script>

<div class="outcome" role="status">
  {#if outcome?.kind === "check_failed"}
    {@const words = CATEGORY_WORDS[outcome.category]}
    <p class="label"><span class="dot"></span>{words.label}</p>
    <Facts>
      <div>
        <dt>What happened</dt>
        <dd>{words.whatHappened}</dd>
      </div>
      <div>
        <dt>Next step</dt>
        <dd>{words.nextStep}</dd>
      </div>
      {#if outcome.reply !== null}
        <div>
          <dt>Server's reply</dt>
          <dd class="reply"><UntrustedText value={outcome.reply} full /></dd>
        </div>
      {/if}
    </Facts>
    <p class="ending">{ending}</p>
  {:else if outcome?.kind === "source_exists"}
    <p class="what">A source for this server and user name already exists.</p>
    <p><a href="/sources/{outcome.sourceId}">Open the existing source</a></p>
  {:else if outcome?.kind === "no_key"}
    <p class="what">
      Collection needs the server's key, and the server started without one.
      {ending}
    </p>
    <p class="next">
      Start the server with its key file, as the operator guide's
      <a href={KEY_GUIDE_URL}>section on the key</a> explains.
    </p>
  {:else if outcome?.kind === "timed_out"}
    <p class="problem">
      {action} didn't finish within 150 seconds. {ending} Try again. If this repeats,
      check the server log.
    </p>
  {:else if outcome?.kind === "unavailable"}
    <p class="problem">
      {action} didn't finish. {ending} Try again. If this repeats, check the server
      log.
    </p>
  {/if}
</div>

<style>
  .outcome {
    display: grid;
    gap: var(--space-4);
  }

  .label {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  .dot {
    flex: none;
    width: var(--dot-size);
    height: var(--dot-size);
    border-radius: 50%;
    background: var(--state-bad);
  }

  /* Evidence an administrator reads in full, so it keeps the body colour. */
  .reply {
    color: var(--text-1);
  }

  .what,
  .ending {
    color: var(--text-2);
  }

  .next {
    color: var(--text-3);
  }
</style>
