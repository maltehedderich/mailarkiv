const UNITS = ["B", "KB", "MB", "GB"];

/**
 * Formats a size in decimal units, as in "31 B", "48 KB", "1.2 MB", and
 * "150 MB". Values under 10 keep one decimal, larger ones are whole.
 */
export function formatSize(bytes: number): string {
  let unit = 0;
  let value = bytes;
  while (unit < UNITS.length - 1 && round(value) >= 1000) {
    value /= 1000;
    unit += 1;
  }
  return `${round(value)} ${UNITS[unit]}`;
}

function round(value: number): number {
  return value < 10 ? Math.round(value * 10) / 10 : Math.round(value);
}

const exact = new Intl.NumberFormat("en-US");

/** Formats an exact size, as in "150,000,000 bytes". */
export function formatBytes(bytes: number): string {
  return `${exact.format(bytes)} ${bytes === 1 ? "byte" : "bytes"}`;
}
