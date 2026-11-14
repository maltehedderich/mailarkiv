<script lang="ts">
  import type { Security } from "#lib/api.js";
  import { SECURITY_WORDS } from "#lib/sources/describe.js";
  import { NO_PASSWORD_MANAGER, portAfterSwitch } from "#lib/sources/form.js";

  import FormField from "./FormField.svelte";

  // Connection security, then the port, which follows the security until the
  // administrator types one.

  let {
    security = $bindable(),
    port = $bindable(),
    securityProblem = null,
    portProblem = null,
  }: {
    security: Security;
    /** The port field's text, as typed. */
    port: string;
    securityProblem?: string | null;
    portProblem?: string | null;
  } = $props();

  const id = $props.id();
  const MODES: Security[] = ["tls", "starttls"];

  function choose(next: Security) {
    port = portAfterSwitch(port, security, next);
    security = next;
  }
</script>

<div class="row">
  <fieldset
    aria-describedby={securityProblem === null ? undefined : `${id}-problem`}
  >
    <legend>Connection security</legend>
    <div class="choices">
      {#each MODES as mode (mode)}
        <label class="choice">
          <input
            type="radio"
            name="{id}-security"
            value={mode}
            checked={security === mode}
            onchange={() => choose(mode)}
          />
          {SECURITY_WORDS[mode]}
        </label>
      {/each}
    </div>
    {#if securityProblem !== null}
      <p id="{id}-problem" class="problem">{securityProblem}</p>
    {/if}
  </fieldset>
  <FormField
    label="Port"
    bind:value={port}
    problem={portProblem}
    inputmode="numeric"
    {...NO_PASSWORD_MANAGER}
    spellcheck="false"
  />
</div>

<style>
  /* The port beside the security it follows, sized for five digits. */
  .row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 88px;
    align-items: start;
    gap: var(--space-4) var(--space-16);
  }

  /* The port's own lines would wrap word by word in its column, so they take
     the row's width below both. */
  .row > :global(div.block) {
    display: contents;
  }

  .row > :global(div.block > :not(.field)) {
    grid-column: 1 / -1;
  }

  /* An input's intrinsic width would push past the column. */
  .row :global(.field),
  .row :global(.field > input) {
    min-width: 0;
  }

  .row :global(.field > input) {
    width: 100%;
  }

  fieldset {
    display: grid;
    gap: var(--space-6);
    min-width: 0;
    margin: 0;
    padding: 0;
    border: 0;
  }

  /* As a field's label. */
  legend {
    padding: 0;
    margin-bottom: var(--space-6);
    color: var(--text-2);
    font-size: var(--size-label);
    font-weight: var(--weight-medium);
    line-height: var(--leading-label);
  }

  .choices {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4) var(--space-20);
  }

  .choice {
    display: inline-flex;
    align-items: center;
    gap: var(--space-8);
    min-height: var(--control-height-large);
    color: var(--text-1);
  }

  input {
    flex: none;
    width: 16px;
    height: 16px;
    margin: 0;
    accent-color: var(--text-1);
  }

  .problem {
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  @media (max-width: 720px) {
    .choice {
      min-height: var(--control-height-touch);
    }
  }
</style>
