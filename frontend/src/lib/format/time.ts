const MONTHS = [
  "Jan",
  "Feb",
  "Mar",
  "Apr",
  "May",
  "Jun",
  "Jul",
  "Aug",
  "Sep",
  "Oct",
  "Nov",
  "Dec",
];

function twoDigits(value: number): string {
  return String(value).padStart(2, "0");
}

export function formatOffset(minutesEastOfUtc: number): string {
  const sign = minutesEastOfUtc < 0 ? "-" : "+";
  const minutes = Math.abs(minutesEastOfUtc);
  return `${sign}${twoDigits(Math.floor(minutes / 60))}:${twoDigits(minutes % 60)}`;
}

/** Formats an RFC 3339 time from the server in the viewer's time zone. */
export function formatTime(rfc3339: string): string {
  const time = new Date(rfc3339);
  const date = `${time.getDate()} ${MONTHS[time.getMonth()]} ${time.getFullYear()}`;
  const clock = [time.getHours(), time.getMinutes(), time.getSeconds()]
    .map(twoDigits)
    .join(":");
  return `${date}, ${clock} ${formatOffset(-time.getTimezoneOffset())}`;
}

/**
 * Like formatTime, but drops the date when the time falls on the viewer's
 * current day, so a list of today's times leads with the clock. The caller
 * passes `now` and must re-render at midnight, or yesterday's times would
 * read as today's.
 */
export function formatTimeToday(rfc3339: string, now: Date): string {
  const time = new Date(rfc3339);
  const sameDay =
    time.getFullYear() === now.getFullYear() &&
    time.getMonth() === now.getMonth() &&
    time.getDate() === now.getDate();
  if (!sameDay) return formatTime(rfc3339);
  const clock = [time.getHours(), time.getMinutes(), time.getSeconds()]
    .map(twoDigits)
    .join(":");
  return `${clock} ${formatOffset(-time.getTimezoneOffset())}`;
}

/** Milliseconds from `now` to the viewer's next midnight. */
export function untilMidnight(now: Date): number {
  const midnight = new Date(now);
  midnight.setHours(24, 0, 0, 0);
  return midnight.getTime() - now.getTime();
}
