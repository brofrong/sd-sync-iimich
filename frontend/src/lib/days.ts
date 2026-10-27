// Photos by the local day they were taken: the Gallery groups its tiles
// by it and the viewer counts a photo's place within its day.
import { parseTime } from "./backend";

/** dayOf returns the local midnight (ms) of a Go time, 0 if it is invalid. */
export function dayOf(takenAt: string | null | undefined): number {
  const at = parseTime(takenAt);
  if (at <= 0) return 0;
  const d = new Date(at);
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}
