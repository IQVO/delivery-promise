import { PROMISE_API_BASE } from "./config";
import type { Book, BookList, Offer, Policy, PromiseList, Promise_ } from "./types";

/** RFC 9457 problem+json body every error response from delivery-promise
 *  returns (`type` = https://retailnetwork.example/problems/<slug>). */
export interface ProblemDetails {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  instance?: string;
}

/**
 * Every failed call -- an HTTP error with a problem+json body, an HTTP error
 * without one, or a network failure -- surfaces as one ApiError, so a screen
 * has a single shape to render: `title` (the problem's category) and `detail`
 * (what exactly was wrong), plus `slug` to branch on a specific problem
 * (e.g. `not-found`, `validation-failed`).
 */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: ProblemDetails | null;
  readonly title: string;
  readonly detail: string;
  /** Last path segment of problem.type (e.g. "not-found"); "" when unknown. */
  readonly slug: string;

  constructor(status: number, problem: ProblemDetails | null, fallbackTitle: string) {
    const title = problem?.title || fallbackTitle;
    const detail = problem?.detail ?? "";
    super(detail || title);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
    this.title = title;
    this.detail = detail;
    this.slug = problem?.type ? (problem.type.split("/").pop() ?? "") : "";
  }
}

/** Normalizes anything thrown by a request into an ApiError. */
export function toApiError(err: unknown): ApiError {
  if (err instanceof ApiError) return err;
  const message = err instanceof Error ? err.message : String(err);
  return new ApiError(0, { detail: message }, "Network error");
}

type Query = Record<string, string | number | boolean | undefined>;

export function withQuery(path: string, query?: Query): string {
  if (!query) return path;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `${path}?${qs}` : path;
}

/**
 * The one door to the service. Only this context's own API is ever called
 * (`PROMISE_API_BASE` = <apiOrigin>/api/delivery-promise): no sibling
 * context. This remote is read-only, so there is no PUT/POST here at all;
 * GETs send no custom header, so they stay simple CORS requests.
 */
async function request<T>(path: string): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${PROMISE_API_BASE}${path}`, { method: "GET" });
  } catch (err) {
    throw toApiError(err);
  }
  if (!res.ok) {
    let problem: ProblemDetails | null = null;
    try {
      problem = (await res.json()) as ProblemDetails;
    } catch {
      // non-JSON error body -- fall through with problem = null
    }
    throw new ApiError(res.status, problem, `${res.status} ${res.statusText}`.trim());
  }
  return (await res.json()) as T;
}

const seg = encodeURIComponent;

/** GET /delivery-offers/{offerId} (404 not-found once the retention ends). */
export async function getOffer(offerId: string): Promise<Offer> {
  return request<Offer>(`/delivery-offers/${seg(offerId)}`);
}

/** Pages followed per collection: a bound, so a producer that keeps returning a nextCursor cannot loop the screen forever. */
export const MAX_PAGES = 50;

/**
 * Reads a whole `{items,nextCursor}` collection: follows `nextCursor` (as the
 * `cursor` query parameter) to the last page (fleet REST guideline 5.2). A 200
 * that is not a page -- a bare array, `items` missing or null -- is REFUSED
 * with an ApiError, never rendered as "no promises"; so is a chain longer than
 * MAX_PAGES, rather than a silently truncated list.
 */
async function listAll<T>(path: string, query: Query): Promise<{ items: T[] }> {
  const items: T[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < MAX_PAGES; page++) {
    const body = await request<unknown>(withQuery(path, { ...query, cursor }));
    const rows: unknown = body !== null && typeof body === "object" && !Array.isArray(body) ? Reflect.get(body, "items") : undefined;
    if (!Array.isArray(rows)) {
      throw new ApiError(502, { detail: `GET ${path} answered 200 but not a {items,nextCursor} page` }, "Response is not a page");
    }
    items.push(...(rows as T[]));
    const next: unknown = Reflect.get(body as object, "nextCursor");
    if (typeof next !== "string" || next === "") return { items };
    cursor = next;
  }
  throw new ApiError(502, { detail: `GET ${path} still had a nextCursor after ${MAX_PAGES} pages` }, "Too many pages");
}

/** GET /promises?orderId= (422 validation-failed on a malformed order id); every page. */
export async function listPromisesByOrder(orderId: string): Promise<PromiseList> {
  return listAll<Promise_>("/promises", { orderId });
}

/** GET /promises/{promiseId} -- terminal states stay readable. */
export async function getPromise(promiseId: string): Promise<Promise_> {
  return request<Promise_>(`/promises/${seg(promiseId)}`);
}

/** GET /promise-books?nodeId=&date= (date is YYYY-MM-DD, the node's local date); every page. */
export async function listPromiseBooks(nodeId: string, date: string): Promise<BookList> {
  return listAll<Book>("/promise-books", { nodeId, date });
}

/** GET /promise-policies/current -- v0 is the built-in default. */
export async function getCurrentPolicy(): Promise<Policy> {
  return request<Policy>("/promise-policies/current");
}
