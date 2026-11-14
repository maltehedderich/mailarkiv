<!-- Shown before saving a new source's folders or added folders, because
     what they collect stays. -->
<div class="notice">
  <p class="what">Archived mail can't be removed in this version.</p>
  <p class="next">
    Messages stay in the archive after you remove a folder or the source.
  </p>
</div>

<style>
  /* A box with a transparent edge, so forced colours still draw it. */
  .notice {
    display: grid;
    gap: var(--space-4);
    padding: var(--space-12) var(--space-16);
    border: 1px solid transparent;
    border-radius: var(--radius-medium);
    background: var(--fill-1);
  }

  .what {
    color: var(--text-2);
  }

  .next {
    color: var(--text-3);
  }
</style>
