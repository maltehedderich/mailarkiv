<script lang="ts">
  import { cubicOut } from "svelte/easing";
  import { prefersReducedMotion, Tween } from "svelte/motion";

  // The error page's drawing: a card catalogue built from the logo's tile, so
  // each drawer front is the mark. With `open`, one drawer slides out and
  // shows it holds nothing. Without it, the drawer sticks a little way out,
  // and tug() shows another pull on it.
  //
  // The drawer moves toward the viewer, so it is drawn in one-point
  // perspective from above and to its left. The cabinet's face is the plane
  // z = 0, and every point is projected on each frame, so the drawer stays
  // true as it moves.

  let { open }: { open: boolean } = $props();

  /** The mark's tile, from Logo.svelte, on its 24-unit grid. */
  const TILE_PATH =
    "M4.5 0H19.5A4.5 4.5 0 0 1 24 4.5V19.5A4.5 4.5 0 0 1 19.5 24H4.5A4.5 4.5 0 0 1 0 19.5V4.5A4.5 4.5 0 0 1 4.5 0ZM4.75 6H19.25A0.25 0.25 0 0 1 19.5 6.25V6.75A3 3 0 0 1 16.5 9.75H7.5A3 3 0 0 1 4.5 6.75V6.25A0.25 0.25 0 0 1 4.75 6Z";
  const TILE = 40;
  const GAP = 6;
  const PAD = 8;
  const COLS = 4;
  const ROWS = 3;
  const WIDTH = COLS * TILE + (COLS - 1) * GAP;
  const HEIGHT = ROWS * TILE + (ROWS - 1) * GAP;
  const TOP_DEPTH = 36;

  // The eye: left of the drawer, so its side shows, and high enough to see
  // the floor inside it.
  const EYE_X = 64;
  const EYE_Y = -150;
  const FOCAL = 300;

  /** How far the drawer comes out, open and stuck. */
  const OUT = 60;
  const STUCK = 6;

  // The drawer that moves, and its box inside the front.
  const X0 = 2 * (TILE + GAP);
  const Y0 = TILE + GAP;
  const WALL_LEFT = X0 + 3;
  const WALL_RIGHT = X0 + TILE - 3;
  const WALL_TOP = Y0 + 8;
  const FLOOR = Y0 + TILE - 3;

  type Point = readonly [number, number];

  function project(x: number, y: number, z: number): Point {
    const scale = FOCAL / (FOCAL + z);
    return [EYE_X + (x - EYE_X) * scale, EYE_Y + (y - EYE_Y) * scale];
  }

  function polygon(...points: Point[]): string {
    return points.map(([x, y]) => `${x.toFixed(2)},${y.toFixed(2)}`).join(" ");
  }

  const top = polygon(
    project(-PAD, -PAD, 0),
    project(WIDTH + PAD, -PAD, 0),
    project(WIDTH + PAD, -PAD, TOP_DEPTH),
    project(-PAD, -PAD, TOP_DEPTH),
  );

  // Fits the open drawer, the lowest and rightmost part of the drawing.
  const [, backTop] = project(-PAD, -PAD, TOP_DEPTH);
  const [right, lowest] = project(X0 + TILE, Y0 + TILE, -OUT);
  const VIEW_TOP = Math.floor(backTop) - 1;
  const VIEW_RIGHT = Math.max(right, WIDTH + PAD) + 1;
  const viewBox = `${-PAD - 1} ${VIEW_TOP} ${VIEW_RIGHT + PAD + 1} ${Math.ceil(Math.max(lowest, HEIGHT + PAD)) - VIEW_TOP + 1}`;

  const closed = Array.from({ length: COLS * ROWS }, (_, index) => [
    (index % COLS) * (TILE + GAP),
    Math.floor(index / COLS) * (TILE + GAP),
  ])
    .filter(([x, y]) => !(x === X0 && y === Y0))
    .map(([x, y]) => `translate(${x} ${y}) scale(${TILE / 24})`);

  const pull = new Tween(0);

  // The surfaces between the cabinet's face and the drawer's front: the
  // floor, the far wall's inner face, and the near wall's outer face.
  const box = $derived.by(() => {
    const z = -pull.current;
    const quad = (x1: number, y1: number, x2: number, y2: number) =>
      polygon(
        project(x1, y1, 0),
        project(x2, y2, 0),
        project(x2, y2, z),
        project(x1, y1, z),
      );
    return {
      floor: quad(WALL_LEFT, FLOOR, WALL_RIGHT, FLOOR),
      far: quad(WALL_RIGHT, WALL_TOP, WALL_RIGHT, FLOOR),
      near: quad(WALL_LEFT, WALL_TOP, WALL_LEFT, FLOOR),
    };
  });

  // A plane facing the viewer only grows and moves as it comes closer.
  const front = $derived.by(() => {
    const z = -pull.current;
    const [x, y] = project(X0, Y0, z);
    const scale = (FOCAL / (FOCAL + z)) * (TILE / 24);
    return `translate(${x.toFixed(2)} ${y.toFixed(2)}) scale(${scale.toFixed(4)})`;
  });

  function duration(ms: number): number {
    return prefersReducedMotion.current ? 0 : ms;
  }

  /** One more pull on the stuck drawer: out a little, then back. */
  export async function tug() {
    if (open) return;
    await pull.set(STUCK + 12, { duration: duration(110), easing: cubicOut });
    await pull.set(STUCK, { duration: duration(240), easing: cubicOut });
  }

  $effect(() => {
    if (open) {
      pull.set(OUT, {
        delay: duration(180),
        duration: duration(950),
        easing: cubicOut,
      });
    } else {
      void tug();
    }
  });
</script>

<svg class="drawing" {viewBox} aria-hidden="true">
  <polygon class="top" points={top} />
  <rect
    class="cabinet"
    x={-PAD}
    y={-PAD}
    width={WIDTH + 2 * PAD}
    height={HEIGHT + 2 * PAD}
    rx="7"
  />
  {#each closed as transform (transform)}
    <path class="closed" fill-rule="evenodd" d={TILE_PATH} {transform} />
  {/each}
  <!-- Twice: one --scrim is a hole in dark, and only a shade in light. -->
  {#each [0, 1] as layer (layer)}
    <rect class="opening" x={X0} y={Y0} width={TILE} height={TILE} rx="7.5" />
  {/each}
  <!-- Each surface is solid, then shaded, so the cabinet doesn't show
       through it. -->
  <polygon class="solid" points={box.floor} />
  <polygon class="lit" points={box.floor} />
  <polygon class="solid" points={box.far} />
  <polygon class="shade" points={box.far} />
  <polygon class="solid" points={box.near} />
  <polygon class="side" points={box.near} />
  <path class="front" fill-rule="evenodd" d={TILE_PATH} transform={front} />
</svg>

<style>
  .drawing {
    display: block;
    overflow: visible;
  }

  .top {
    fill: var(--fill-1);
  }

  .cabinet {
    fill: var(--fill-3);
  }

  .closed {
    fill: var(--panel);
  }

  .opening {
    fill: var(--scrim);
  }

  .solid {
    fill: var(--panel);
  }

  /* --scrim darkens in both themes, where the fills would lighten in dark. */
  .lit {
    fill: var(--fill-2);
  }

  .shade {
    fill: var(--scrim);
  }

  .side {
    fill: var(--fill-3);
  }

  .front {
    fill: var(--text-1);
  }
</style>
