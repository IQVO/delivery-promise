import { describe, expect, it } from "vitest";
import { dateProblem, idProblem, linesLabel, utcLabel } from "./lib";

describe("utcLabel", () => {
  it("formats an RFC 3339 instant as UTC to the second", () => {
    expect(utcLabel("2026-10-23T02:00:00.500+05:00")).toBe("2026-10-22 21:00:00Z");
  });

  it("passes unparseable text through and renders NO_VALUE for absent", () => {
    expect(utcLabel("nonsense")).toBe("nonsense");
    expect(utcLabel(undefined)).not.toBe("nonsense");
  });
});

describe("idProblem", () => {
  it("accepts a normal id", () => {
    expect(idProblem("order", "ord-1001")).toBeNull();
  });

  it("flags empty, overlong and malformed ids", () => {
    expect(idProblem("order", "")).toMatch(/^Enter a order id/);
    expect(idProblem("order", "x".repeat(65))).toMatch(/at most 64 characters/);
    expect(idProblem("node", "N AUS3")).toMatch(/cannot contain spaces/);
    expect(idProblem("node", "a/b")).toMatch(/cannot contain spaces/);
  });
});

describe("dateProblem", () => {
  it("accepts a real calendar date in YYYY-MM-DD", () => {
    expect(dateProblem("2026-10-22")).toBeNull();
  });

  it("rejects empty, malformed and impossible dates", () => {
    expect(dateProblem("")).toMatch(/Enter a date/);
    expect(dateProblem("10/22/26")).toMatch(/YYYY-MM-DD/);
    expect(dateProblem("2026-02-30")).toMatch(/not a real calendar date/);
  });
});

describe("linesLabel", () => {
  it("joins the lines of a shipment group", () => {
    expect(linesLabel([{ catalogItemId: "ci-1001", quantity: 2 }, { catalogItemId: "ci-7", quantity: 1 }])).toBe(
      "ci-1001 ×2, ci-7 ×1",
    );
    expect(linesLabel([])).toBe("");
  });
});
