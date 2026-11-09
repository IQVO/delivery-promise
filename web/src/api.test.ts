import { describe, expect, it } from "vitest";
import { ApiError, getCurrentPolicy, getOffer, getPromise, listPromiseBooks, listPromisesByOrder, toApiError, withQuery } from "./api";
import { PROMISE_API_BASE } from "./config";
import { json, mockApi, problem } from "./test/fetchMock";
import { book, offer, policy, promise } from "./test/fixtures";

describe("withQuery", () => {
  it("drops empty values and encodes the rest", () => {
    expect(withQuery("/promise-books", { nodeId: "N-AUS3", date: "2026-10-22" })).toBe(
      "/promise-books?nodeId=N-AUS3&date=2026-10-22",
    );
    expect(withQuery("/promises", {})).toBe("/promises");
    expect(withQuery("/promises")).toBe("/promises");
  });
});

describe("the API base", () => {
  it("is this context's own /api/delivery-promise or the developer port, never a sibling", () => {
    expect(PROMISE_API_BASE).toBe("http://localhost:8080");
  });
});

/** Awaits a request that must fail and returns it as the one ApiError shape. */
async function failingAs(p: Promise<unknown>): Promise<ApiError> {
  try {
    await p;
  } catch (e) {
    return toApiError(e);
  }
  throw new Error("expected the request to fail");
}

describe("requests", () => {
  it("lists the promises of an order with the id as the only query parameter", async () => {
    const api = mockApi({ "GET /promises?orderId=ord-1001": json({ items: [promise()] }) });
    const page = await listPromisesByOrder("ord-1001");
    expect(page.items).toHaveLength(1);
    const call = api.calls[0];
    expect(call.url).toBe(`${PROMISE_API_BASE}/promises?orderId=ord-1001`);
    expect(call.method).toBe("GET");
    expect(call.headers).toEqual({});
  });

  it("follows nextCursor and returns the promises of every page", async () => {
    const api = mockApi({
      "GET /promises?orderId=ord-1": json({ items: [promise({ promiseId: "prm-1" })], nextCursor: "c1" }),
      "GET /promises?orderId=ord-1&cursor=c1": json({ items: [promise({ promiseId: "prm-2" })], nextCursor: "c2" }),
      "GET /promises?orderId=ord-1&cursor=c2": json({ items: [promise({ promiseId: "prm-3" })] }),
    });
    const page = await listPromisesByOrder("ord-1");
    expect(page.items.map((p) => p.promiseId)).toEqual(["prm-1", "prm-2", "prm-3"]);
    expect(api.calls).toHaveLength(3);
  });

  it("follows nextCursor for the books of a node and date", async () => {
    mockApi({
      "GET /promise-books?nodeId=N-AUS3&date=2026-10-22": json({ items: [book({ bookId: "b-1" })], nextCursor: "c1" }),
      "GET /promise-books?nodeId=N-AUS3&date=2026-10-22&cursor=c1": json({ items: [book({ bookId: "b-2" })] }),
    });
    const page = await listPromiseBooks("N-AUS3", "2026-10-22");
    expect(page.items.map((b) => b.bookId)).toEqual(["b-1", "b-2"]);
  });

  it.each([
    ["a missing items", {}],
    ["items null", { items: null }],
    ["a bare array", [{ promiseId: "prm-1" }]],
  ])("refuses %s instead of reading it as no promises", async (_n, body) => {
    mockApi({ "GET /promises?orderId=ord-1": json(body), "GET /promise-books?nodeId=N&date=2026-10-22": json(body) });
    expect((await failingAs(listPromisesByOrder("ord-1"))).title).toMatch(/page/i);
    expect((await failingAs(listPromiseBooks("N", "2026-10-22"))).title).toMatch(/page/i);
  });

  it("stops a runaway cursor chain with an error rather than a truncated list", async () => {
    mockApi({
      "GET /promises?orderId=ord-1": json({ items: [], nextCursor: "loop" }),
      "GET /promises?orderId=ord-1&cursor=loop": json({ items: [], nextCursor: "loop" }),
    });
    expect((await failingAs(listPromisesByOrder("ord-1"))).title).toMatch(/pages/i);
  });

  it("lists the books of a node and date", async () => {
    const api = mockApi({ "GET /promise-books?nodeId=N-AUS3&date=2026-10-22": json({ items: [book()] }) });
    const page = await listPromiseBooks("N-AUS3", "2026-10-22");
    expect(page.items[0].bookId).toBe("N-AUS3:dep-AUS3-1800");
    expect(api.calls[0].url.endsWith("/promise-books?nodeId=N-AUS3&date=2026-10-22")).toBe(true);
  });

  it("reads one promise and one offer by escaped id", async () => {
    const api = mockApi({
      "GET /promises/prm%201": json(promise({ promiseId: "prm 1" })),
      "GET /delivery-offers/off%201": json(offer({ offerId: "off 1" })),
    });
    expect((await getPromise("prm 1")).promiseId).toBe("prm 1");
    expect((await getOffer("off 1")).offerId).toBe("off 1");
    expect(api.calls.map((c) => c.path)).toEqual(["/promises/prm%201", "/delivery-offers/off%201"]);
  });

  it("reads the current policy, which takes no parameter at all", async () => {
    mockApi({ "GET /promise-policies/current": json(policy()) });
    expect((await getCurrentPolicy()).policyVersion).toBe("v1");
  });

  it("surfaces an RFC 9457 problem as one ApiError with the slug", async () => {
    mockApi({ "GET /promises/prm-404": problem(404, "not-found", "Not found", "the promise does not exist") });
    const err = await failingAs(getPromise("prm-404"));
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(404);
    expect(err.slug).toBe("not-found");
    expect(err.title).toBe("Not found");
    expect(err.detail).toBe("the promise does not exist");
  });

  it("wraps a network failure and an unparseable error body the same way", async () => {
    expect(toApiError(new Error("boom")).title).toBe("Network error");
    mockApi({
      "GET /promises/prm-1": new Response("gateway timeout", { status: 504 }),
    });
    const err = await failingAs(getPromise("prm-1"));
    expect(err.status).toBe(504);
    expect(err.slug).toBe("");
    expect(err.title).toContain("504");
  });
});
