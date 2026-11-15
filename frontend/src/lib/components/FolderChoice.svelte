<script lang="ts">
  import { displayText } from "#lib/format/untrusted.js";
  import type { FolderOption } from "#lib/sources/form.js";

  import UntrustedText from "./UntrustedText.svelte";

  // A checklist of folders. The checkbox is named by the folder alone, with
  // its codes in place: the label text would gain a space where a long name
  // is cut. The count describes the checkbox.

  let {
    options = $bindable(),
    problem = null,
  }: {
    options: FolderOption[];
    problem?: string | null;
  } = $props();

  const id = $props.id();
  const numbers = new Intl.NumberFormat("en-US");
</script>

<fieldset aria-describedby={problem === null ? undefined : `${id}-problem`}>
  <legend>Folders</legend>
  <ul>
    {#each options as option, index (option.name)}
      <li>
        <label>
          <input
            type="checkbox"
            aria-label={displayText(option.name).text}
            bind:checked={options[index].chosen}
            aria-describedby={option.messages === null
              ? undefined
              : `${id}-count-${index}`}
          />
          <span class="name"><UntrustedText value={option.name} /></span>
        </label>
        {#if option.messages !== null}
          <span id="{id}-count-{index}" class="count">
            {numbers.format(option.messages)}
            {option.messages === 1 ? "message" : "messages"}
          </span>
        {/if}
      </li>
    {/each}
  </ul>
  {#if problem !== null}
    <p id="{id}-problem" class="problem">{problem}</p>
  {/if}
</fieldset>

<style>
  fieldset {
    display: grid;
    gap: var(--space-4);
    min-width: 0;
    margin: 0;
    padding: 0;
    border: 0;
  }

  /* As a field's label. */
  legend {
    padding: 0;
    margin-bottom: var(--space-4);
    color: var(--text-2);
    font-size: var(--size-label);
    font-weight: var(--weight-medium);
    line-height: var(--leading-label);
  }

  ul {
    display: grid;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  li {
    display: flex;
    align-items: center;
    gap: var(--space-12);
    min-width: 0;
    min-height: var(--control-height-large);
  }

  /* Takes the row, so the whole name is a target, and may shrink, so a long
     name is cut rather than widening the page. */
  label {
    display: flex;
    flex: auto;
    align-items: center;
    gap: var(--space-8);
    min-width: 0;
  }

  input {
    flex: none;
    width: 16px;
    height: 16px;
    margin: 0;
    accent-color: var(--text-1);
  }

  .name {
    display: flex;
    min-width: 0;
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  .count {
    flex: none;
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    line-height: var(--leading-label);
  }

  .problem {
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  @media (max-width: 720px) {
    li {
      min-height: var(--control-height-touch);
    }
  }
</style>
