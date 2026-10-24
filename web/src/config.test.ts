import { describe, expect, it } from "vitest";
import { resolvePromiseApiBase } from "./config";

describe("resolvePromiseApiBase", () => {
  it("is this context's own /api/delivery-promise under the runtime origin", () => {
    expect(resolvePromiseApiBase({ apiOrigin: "http://localhost:8000" }, true)).toBe(
      "http://localhost:8000/api/delivery-promise",
    );
  });

  it("drops trailing slashes of the origin", () => {
    expect(resolvePromiseApiBase({ apiOrigin: "http://kong.local///" }, false)).toBe(
      "http://kong.local/api/delivery-promise",
    );
  });

  it("falls back to the developer port outside production", () => {
    expect(resolvePromiseApiBase({}, false)).toBe("http://localhost:8080");
  });

  it("refuses to guess in production", () => {
    expect(() => resolvePromiseApiBase({}, true)).toThrow(/apiOrigin is required/);
    expect(() => resolvePromiseApiBase({ apiOrigin: "" }, true)).toThrow(/apiOrigin is required/);
  });
});
