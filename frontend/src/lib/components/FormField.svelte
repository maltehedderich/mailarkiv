<script lang="ts">
  import type { HTMLInputAttributes } from "svelte/elements";

  // A labelled input with a hint and a problem line below it, each tied to
  // the input by aria-describedby. They sit outside the label, so the label
  // alone names the input.

  let {
    label,
    value = $bindable(""),
    hint = null,
    problem = null,
    ...input
  }: {
    label: string;
    value?: string;
    hint?: string | null;
    /** What the last attempt refused in this field. */
    problem?: string | null;
  } & Omit<
    HTMLInputAttributes,
    "value" | "aria-invalid" | "aria-describedby"
  > = $props();

  const id = $props.id();

  const describedBy = $derived(
    [
      hint === null ? null : `${id}-hint`,
      problem === null ? null : `${id}-problem`,
    ]
      .filter((part) => part !== null)
      .join(" ") || undefined,
  );
</script>

<div class="block">
  <label class="field">
    <span>{label}</span>
    <input
      {...input}
      bind:value
      aria-invalid={problem === null ? undefined : "true"}
      aria-describedby={describedBy}
    />
  </label>
  {#if hint !== null}
    <p id="{id}-hint" class="hint">{hint}</p>
  {/if}
  {#if problem !== null}
    <p id="{id}-problem" class="problem">{problem}</p>
  {/if}
</div>

<style>
  .block {
    display: grid;
    gap: var(--space-4);
  }

  .hint,
  .problem {
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  .hint {
    color: var(--text-3);
  }
</style>
