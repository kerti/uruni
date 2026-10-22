/**
 * The fund's time zone (ADR-041): Indonesia's three zones, none with DST. IANA names
 * are what the server stores and `Intl.DateTimeFormat({ timeZone })` takes; WIB, WITA
 * and WIT exist only as copy (`copy.timeZone`).
 *
 * One of three copies (with `internal/tz` and the CHECK on `fund.time_zone`);
 * `TestTimeZoneListsAgree` fails on drift. Keep the array literal and the default
 * on the lines below in the shape that test reads.
 */

export const TIME_ZONES = ['Asia/Jakarta', 'Asia/Makassar', 'Asia/Jayapura'] as const

export type TimeZone = (typeof TIME_ZONES)[number]

export const DEFAULT_TIME_ZONE: TimeZone = 'Asia/Jakarta'

export function isTimeZone(value: unknown): value is TimeZone {
  return typeof value === 'string' && (TIME_ZONES as readonly string[]).includes(value)
}

/**
 * The zone a device's UTC offset suggests: +7, +8 and +9 as WIB, WITA and WIT,
 * anything else as WIB. By offset, not the device's IANA name, which aliases and a
 * hardened browser's "UTC" would defeat. A starting value for Setup's field, never
 * the fund's zone.
 *
 * `offsetMinutes` has `Date.getTimezoneOffset()`'s sign: minutes WEST of UTC, so WIB is -420.
 */
export function zoneForDeviceOffset(offsetMinutes: number): TimeZone {
  switch (offsetMinutes) {
    case -480:
      return 'Asia/Makassar'
    case -540:
      return 'Asia/Jayapura'
    default:
      return DEFAULT_TIME_ZONE
  }
}
