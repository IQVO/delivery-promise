import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PolicyScreen } from "./PolicyScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { policy } from "../test/fixtures";

function renderPolicy() {
  return render(<PolicyScreen />);
}

describe("PolicyScreen", () => {
  it("shows the published policy with its deadlines", async () => {
    mockApi({ "GET /promise-policies/current": json(policy()) });
    renderPolicy();
    expect(await screen.findByRole("heading", { name: "Promise policy" })).toBeInTheDocument();
    expect(screen.getByText("Policy v1")).toBeInTheDocument();
    expect(screen.getByText("5 min")).toBeInTheDocument(); // offer TTL
    expect(screen.getByText("20 min")).toBeInTheDocument(); // hold TTL
    expect(screen.getByText("50%")).toBeInTheDocument(); // capacity share
    expect(screen.getByText("R-SC")).toBeInTheDocument();
    expect(screen.getByText("23:59")).toBeInTheDocument();
  });

  it("labels the built-in v0 default", async () => {
    mockApi({
      "GET /promise-policies/current": json(policy({ policyVersion: "v0", version: 0, orderDeadlines: [] })),
    });
    renderPolicy();
    expect(await screen.findByText("Policy v0")).toBeInTheDocument();
    expect(screen.getByText(/No order deadline is configured/)).toBeInTheDocument();
  });

  it("renders the problem of a failing policy read", async () => {
    mockApi({ "GET /promise-policies/current": problem(500, "internal-error", "Internal server error", "boom") });
    renderPolicy();
    expect(await screen.findByRole("alert")).toHaveTextContent("Internal server error");
  });
});
