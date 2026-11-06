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

/** Formats an RFC 3339 time from the server in the viewer's time zone (FR-043). */
export function formatTime(rfc3339: string): string {
  const time = new Date(rfc3339);
  const date = `${time.getDate()} ${MONTHS[time.getMonth()]} ${time.getFullYear()}`;
  const clock = [time.getHours(), time.getMinutes(), time.getSeconds()]
    .map(twoDigits)
    .join(":");
  return `${date}, ${clock} ${formatOffset(-time.getTimezoneOffset())}`;
}
