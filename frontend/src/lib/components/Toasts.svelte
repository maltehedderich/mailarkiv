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

  // Newest first. The stack grows up from the screen's edge, so a new toast
  // never moves the ones already shown, and a stack taller than its cap shows
  // the newest without scrolling.
  const toasts = $derived(toastsIn(place).reverse());

  /** Space kept between the stack and a focused control. */
  const GAP = 8;

  /** The state line, which describes the toast's button. */
  const stateId = (key: string) => `toast-${place}-${key}`;

  let region: HTMLElement;
  let list: HTMLOListElement;

  // A completed transfer can remove its Cancel control without a click.
  // Move focus before that DOM node leaves.
  $effect.pre(() => {
    const visible = toasts;
    if (!list || !(document.activeElement instanceof HTMLElement)) return;
    const item =
      document.activeElement.closest<HTMLElement>("li[data-toast-key]");
    if (
      item?.parentElement === list &&
      !visible.some(({ key }) => key === item.dataset.toastKey)
    ) {
      region.focus();
    }
  });

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

<!-- Always present, so screen readers hear each toast as it's added. -->
<section
  bind:this={region}
  class="toasts {place}"
  aria-label="Notifications"
  tabindex="-1"
>
  <ol
    bind:this={list}
    class="stack"
    class:empty={toasts.length === 0}
    aria-live="polite"
  >
    {#each toasts as { key, toast } (key)}
      {@const words = toastWords(toast)}
      {@const cancellable =
        toast.kind === "downloading" || toast.kind === "download_waiting"}
      <li class="toast" data-toast-key={key}>
        <span class="dot {words.tone}"></span>
        <div class="text">
          <p class="state" id={stateId(key)}>
            <strong>{words.word}</strong>
            <span class="subject">
              <span class="file"><FileName name={words.name} /></span>
              {#if words.size !== undefined}
                <span class="size">{formatSize(words.size)}</span>
              {/if}
            </span>
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
        <!-- Keep the control mounted when Cancel becomes Dismiss, so keyboard
             focus stays with this result. The state line says which toast
             each Dismiss closes, since several can be open. -->
        <button
          class="button ghost dismiss"
          class:icon-only={!cancellable}
          type="button"
          aria-label={cancellable ? "Cancel" : "Dismiss"}
          aria-describedby={stateId(key)}
          onclick={(event) => {
            if (
              toast.kind === "downloading" ||
              toast.kind === "download_waiting"
            )
              toast.cancel();
            else dismiss(key, event.currentTarget);
          }}
        >
          {#if cancellable}Cancel{:else}<CloseIcon />{/if}
        </button>
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

  /* One raised surface with a hairline between toasts, so several read as one
     list and not a pile of cards. It takes at most half the screen's height
     and scrolls past that, so the page stays usable and no toast is pushed
     off the top, where it couldn't be read or dismissed. In the drawer, half
     the screen still fits between its header and footer. The transparent
     border is the edge forced colours paint, since they remove the shadow. */
  .stack {
    max-height: 50dvh;
    margin: 0;
    padding: 0;
    overflow-y: auto;
    overscroll-behavior: contain;
    border: 1px solid transparent;
    border-radius: var(--radius-large);
    background: var(--raised);
    box-shadow: var(--shadow-pop);
    list-style: none;
    animation: rise var(--motion-enter) var(--ease-enter);
  }

  /* Kept in the page for the live region, with no size, so it measures 0. */
  .stack.empty {
    border: 0;
    box-shadow: none;
    animation: none;
  }

  @keyframes rise {
    from {
      opacity: 0;
      transform: translateY(var(--space-8));
    }
  }

  /* The surface rises once. A later toast fades in where it lands. */
  .toast {
    display: grid;
    grid-template-columns: var(--dot-size) minmax(0, 1fr) auto;
    align-items: start;
    column-gap: var(--space-10);
    padding: var(--space-12) var(--space-8) var(--space-12) var(--space-16);
    animation: appear var(--motion-enter) var(--ease-enter);
  }

  .toast + .toast {
    border-top: 1px solid var(--line);
  }

  @keyframes appear {
    from {
      opacity: 0;
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

  /* Wraps like a status problem's state line. */
  .state {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0 var(--space-8);
    max-width: 100%;
    min-width: 0;
  }

  .state strong {
    flex: none;
    font-weight: var(--weight-strong);
  }

  /* The name and its size stay beside the state word while about 18
     characters fit there, or the whole name if it's shorter. Otherwise they
     take a line of their own, so a long state word beside Cancel at 360 px
     can't cut the name to a stub such as "s…ement-29.eml". */
  .subject {
    display: flex;
    flex: 1 1 18ch;
    align-items: center;
    gap: var(--space-8);
    max-width: max-content;
    min-width: 0;
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
      bottom: calc(var(--tab-bar-height) + var(--space-8));
      left: var(--space-16);
      width: auto;
    }

    .dismiss {
      margin: calc((var(--leading-body) - var(--control-height-touch)) / 2) 0;
    }
  }
</style>
