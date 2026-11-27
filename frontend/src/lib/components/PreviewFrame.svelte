<script lang="ts">
  // The HTML body, in a frame with every sandbox restriction and no allow-
  // token: no script, plugin, form, popup, top navigation, or access to
  // MailArkiv's origin. The server's policy for the frame's address says the
  // same, so the address opened in a tab is isolated too.

  let { src }: { src: string } = $props();

  let frame = $state<HTMLIFrameElement | null>(null);

  export function focus() {
    frame?.focus();
  }
</script>

<!-- A new frame for each address: changing a frame's src would add an entry
     to the tab's history, and Back would only reload the preview. -->
{#key src}
  <iframe
    bind:this={frame}
    sandbox=""
    referrerpolicy="no-referrer"
    title="Message body"
    {src}
  ></iframe>
{/key}

<style>
  /* A sandboxed frame can't report its height, so the content scrolls
     inside it. */
  iframe {
    display: block;
    width: 100%;
    height: 70vh;
    min-height: 320px;
    border: 1px solid var(--line);
    border-radius: var(--radius-medium);
    background: var(--mail-surface);
  }
</style>
