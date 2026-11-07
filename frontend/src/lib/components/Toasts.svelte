<script lang="ts">
  import CloseIcon from "~icons/lucide/x";

  import { page } from "$app/state";

  import FileName from "#lib/components/FileName.svelte";
  import { formatSize } from "#lib/format/size.js";
  import { historyHref } from "#lib/history.js";
  import {
    dismissToast,
    toastsIn,
    type Place,
  } from "#lib/toasts/store.svelte.js";
  import { toastWords } from "#lib/toasts/words.js";

  let { place }: { place: Place } = $props();

  const toasts = $derived(toastsIn(place));

  /** Space kept between the stack and a focused control. */
  const GAP = 8;

  let region: HTMLElement;
  let list: HTMLOListElement;

  function dismiss(key: string, button: HTMLElement) {
    // Focus would fall to the page with the toast, so it moves on purpose: to
    // the toast below, the one above, or the region.
    const item = button.closest("li");
    if (item?.contains(document.activeElement)) {
      const items = Array.from(list.children);
      const index = items.indexOf(item);
      const neighbour = items[index + 1] ?? items[index - 1];
      (neighbour?.querySelector<HTMLElement>("button, a") ?? region).focus();
    }
    dismissToast(key);
  }

  // WCAG 2.4.11: the stack must not hide a focused control. --toast-room tells
  // the page, or the drawer around this stack, how much room to keep clear at
  // the bottom, for padding and for scroll-padding.
  $effect(() => {
    const scroller =
      place === "page" ? document.documentElement : region.parentElement;
    if (scroller === null) return;
    const measure = () => {
      const height = list.getBoundingClientRect().height;
      const room =
        height === 0
          ? 0
          : place === "page"
            ? window.innerHeight - region.getBoundingClientRect().top + GAP
            : height + GAP * 2;
      scroller.style.setProperty("--toast-room", `${room}px`);
    };
    const observer = new ResizeObserver(measure);
    observer.observe(list);
    window.addEventListener("resize", measure);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", measure);
      scroller.style.removeProperty("--toast-room");
    };
  });
</script>

<!-- Always present, so screen readers hear each toast as it's added (FR-041). -->
<section
  bind:this={region}
  class="toasts {place}"
  aria-label="Notifications"
  tabindex="-1"
>
  <ol bind:this={list} aria-live="polite">
    {#each toasts as { key, toast } (key)}
      {@const words = toastWords(toast)}
      <li class="toast">
        <span class="dot {words.tone}"></span>
        <div class="text">
          <p class="state">
            <strong>{words.word}</strong>
            <span class="file"><FileName name={words.name} /></span>
            {#if words.size !== undefined}
              <span class="size">{formatSize(words.size)}</span>
            {/if}
          </p>
          {#if words.what !== undefined}
            <p class="what">{words.what}</p>
          {/if}
          {#if words.next !== undefined}
            <p class="next">{words.next}</p>
          {/if}
          {#if words.fraction !== undefined}
            {@const percent = Math.floor(words.fraction * 100)}
            <div class="progress">
              <div
                class="bar"
                role="progressbar"
                aria-label="Download"
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={percent}
              >
                <div class="sent" style:width="{percent}%"></div>
              </div>
              <!-- The bar gives screen readers the value, so this isn't read
                   again at every step. -->
              <span class="percent" aria-hidden="true">{percent}%</span>
            </div>
          {/if}
          {#if words.historyId !== undefined}
            <!-- Keeping focus here lets the drawer give it back to this link. -->
            <a
              href={historyHref(page.url, words.historyId)}
              data-sveltekit-reset="false">Open import history</a
            >
          {/if}
        </div>
        <!-- A running download ends by itself, in a save or a failure. -->
        {#if toast.kind !== "downloading"}
          <button
            class="button ghost icon-only dismiss"
            type="button"
            aria-label="Dismiss"
            onclick={(event) => dismiss(key, event.currentTarget)}
          >
            <CloseIcon />
          </button>
        {/if}
      </li>
    {/each}
  </ol>
</section>

<style>
  .toasts {
    z-index: 2;
  }

  /* Focused only when the last toast in reach was dismissed. */
  .toasts:focus {
    outline: none;
  }

  .toasts.page {
    position: fixed;
    right: var(--space-24);
    bottom: var(--space-24);
    width: min(var(--toast-width), calc(100vw - var(--space-32)));
  }

  /* Over the end of the drawer's body, just above its footer. */
  .toasts.drawer {
    position: absolute;
    right: var(--space-16);
    bottom: var(--space-12);
    left: var(--space-16);
  }

  ol {
    display: grid;
    gap: var(--space-8);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  /* The transparent border is the edge forced colours paint, since they
     remove the shadow. */
  .toast {
    display: grid;
    grid-template-columns: var(--dot-size) minmax(0, 1fr) auto;
    align-items: start;
    column-gap: var(--space-10);
    padding: var(--space-12) var(--space-8) var(--space-12) var(--space-16);
    border: 1px solid transparent;
    border-radius: var(--radius-large);
    background: var(--raised);
    box-shadow: var(--shadow-pop);
    animation: rise var(--motion-enter) var(--ease-enter);
  }

  @keyframes rise {
    from {
      opacity: 0;
      transform: translateY(var(--space-8));
    }
  }

  /* Centred on the first line. */
  .dot {
    width: var(--dot-size);
    height: var(--dot-size);
    margin-top: calc((var(--leading-body) - var(--dot-size)) / 2);
    border-radius: 50%;
  }

  .dot.ok {
    background: var(--state-ok);
  }

  .dot.neutral {
    background: var(--text-4);
  }

  .dot.warn {
    background: var(--state-warn);
  }

  .dot.bad {
    background: var(--state-bad);
  }

  .text {
    display: grid;
    justify-items: start;
    gap: var(--space-2);
    min-width: 0;
  }

  .state {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    max-width: 100%;
    min-width: 0;
  }

  .state strong {
    flex: none;
    font-weight: var(--weight-strong);
  }

  .file {
    display: flex;
    min-width: 0;
    color: var(--text-2);
    font-weight: var(--weight-medium);
  }

  .size {
    flex: none;
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
  }

  .what {
    color: var(--text-2);
  }

  .next {
    color: var(--text-3);
  }

  .text a {
    margin-top: var(--space-4);
  }

  .progress {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    width: 100%;
    margin-top: var(--space-6);
  }

  .bar {
    flex: 1;
    height: 4px;
    overflow: hidden;
    border-radius: 2px;
    background: var(--fill-3);
  }

  .sent {
    height: 100%;
    background: var(--text-1);
  }

  .percent {
    min-width: 4ch;
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    line-height: var(--leading-label);
    text-align: right;
  }

  /* Centres the button on the first line without making the toast taller. */
  .dismiss {
    margin: calc((var(--leading-body) - var(--control-height)) / 2) 0;
  }

  /* DESIGN.md: full width above the bottom tab bar, like the sign-out line. */
  @media (max-width: 720px) {
    .toasts.page {
      right: var(--space-16);
      bottom: calc(44px + var(--space-8));
      left: var(--space-16);
      width: auto;
    }

    .dismiss {
      margin: calc((var(--leading-body) - var(--control-height-touch)) / 2) 0;
    }
  }
</style>
