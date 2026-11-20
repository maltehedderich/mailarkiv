import { formatOffset } from "#lib/format/time.js";

/** A day from a date input: `2026-03-29`, or empty. */
const DAY = /^(\d{4})-(\d{2})-(\d{2})$/;

function twoDigits(value: number): string {
  return String(value).padStart(2, "0");
}

/**
 * The instant at the start of a day in this browser's time zone, `days`
 * after the given one, as RFC 3339 with the offset that applies then. Null
 * for an empty day.
 */
function midnight(day: string, days: number): string | null {
  const match = DAY.exec(day);
  if (match === null) return null;
  const [, year, month, date] = match;
  // The local constructor handles the month's end and the change of offset.
  const time = new Date(Number(year), Number(month) - 1, Number(date) + days);
  const yyyy = String(time.getFullYear()).padStart(4, "0");
  const clock = [time.getHours(), time.getMinutes(), time.getSeconds()]
    .map(twoDigits)
    .join(":");
  const offset = formatOffset(-time.getTimezoneOffset());
  return `${yyyy}-${twoDigits(time.getMonth() + 1)}-${twoDigits(time.getDate())}T${clock}.000${offset}`;
}

/**
 * The instants a range of days covers, both days included: midnight at the
 * start of the first day, and midnight at the start of the day after the
 * last. An empty day has no bound.
 */
export function dayRange(
  firstDay: string,
  lastDay: string,
): { listDateFrom: string | null; listDateBefore: string | null } {
  return {
    listDateFrom: midnight(firstDay, 0),
    listDateBefore: midnight(lastDay, 1),
  };
}
