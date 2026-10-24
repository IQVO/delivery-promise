import { NO_VALUE } from "@warehouse/ui-kit";

/** "2026-10-22 19:00:00Z" for an RFC 3339 instant; the raw text when unparseable. */
export function utcLabel(iso: string | undefined): string {
  if (!iso) return NO_VALUE;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const d = new Date(t).toISOString();
  return `${d.slice(0, 10)} ${d.slice(11, 19)}Z`;
}

/** The id rule shared by order, node and offer ids: 1..64 characters, no
 *  whitespace, control characters or "/". */
export function idProblem(what: string, id: string): string | null {
  if (id.length === 0) return `Enter a ${what} id.`;
  if (id.length > 64) return `A ${what} id is at most 64 characters.`;
  // eslint-disable-next-line no-control-regex
  if (/[\s/\u0000-\u001f\u007f]/.test(id)) return `A ${what} id cannot contain spaces, control characters or '/'.`;
  return null;
}

/** A date typed into the books filter, or null unless it is exactly YYYY-MM-DD. */
export function dateProblem(raw: string): string | null {
  if (raw === "") return "Enter a date.";
  if (!/^\d{4}-\d{2}-\d{2}$/.test(raw)) return "The date must be YYYY-MM-DD.";
  const [y, m, d] = raw.split("-").map(Number);
  const t = new Date(Date.UTC(y, m - 1, d));
  if (t.getUTCFullYear() !== y || t.getUTCMonth() !== m - 1 || t.getUTCDate() !== d) {
    return "That is not a real calendar date.";
  }
  return null;
}

/** The lines of one shipment group as "ci-1001 ×2", comma-joined. */
export function linesLabel(lines: { catalogItemId: string; quantity: number }[]): string {
  return lines.map((l) => `${l.catalogItemId} ×${l.quantity}`).join(", ");
}
