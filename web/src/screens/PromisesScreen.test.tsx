import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BrowserRouter, MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { PromiseDetailRoute, PromisesScreen } from "./PromisesScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { promise } from "../test/fixtures";

function renderAt(path: string) {
  window.history.replaceState({}, "", path);
  return render(
    <BrowserRouter>
      <PromisesScreen />
    </BrowserRouter>,
  );
}

// useParams needs a matched route, so the detail route element is mounted
// the way App mounts it.
function renderDetail(promiseId: string) {
  return render(
    <MemoryRouter initialEntries={[`/promises/${promiseId}`]}>
      <Routes>
        <Route path="/promises/:promiseId" element={<PromiseDetailRoute />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("PromisesScreen", () => {
  it("lists the promises of the order, newest group per row", async () => {
    mockApi({ "GET /promises?orderId=ord-1001": json({ items: [promise()] }) });
    renderAt("/");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Order"), "ord-1001");
    await user.click(screen.getByRole("button", { name: "View promises" }));
    expect(await screen.findByText("Promises of ord-1001")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "prm-1" })).toHaveAttribute("href", "/promises/prm-1");
    expect(screen.getByText("NEXT_DAY")).toBeInTheDocument();
    expect(screen.getByText("ci-1001 ×2")).toBeInTheDocument();
    expect(screen.getByText("2026-10-22 23:00:00Z")).toBeInTheDocument(); // the ship-by cutoff
  });

  it("asks for an order id before it calls anything", () => {
    renderAt("/");
    expect(screen.getByRole("button", { name: "View promises" })).toBeDisabled();
    expect(screen.queryByText(/No promises were confirmed/)).not.toBeInTheDocument();
  });

  it("explains an order with no promises", async () => {
    mockApi({ "GET /promises?orderId=ord-2": json({ items: [] }) });
    renderAt("/");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Order"), "ord-2");
    await user.click(screen.getByRole("button", { name: "View promises" }));
    expect(await screen.findByText("No promises were confirmed for ord-2.")).toBeInTheDocument();
  });

  it("refuses a malformed order id before it calls anything", async () => {
    mockApi({}); // a request would fail the test: unmocked
    renderAt("/");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Order"), "ord/1");
    expect(await screen.findByText(/cannot contain spaces/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "View promises" })).toBeDisabled();
  });
});

describe("PromiseScreen", () => {
  it("shows the terms, the witness path and the unjudged state", async () => {
    mockApi({ "GET /promises/prm-1": json(promise()) });
    renderDetail("prm-1");
    expect(await screen.findByRole("heading", { name: "Promise prm-1" })).toBeInTheDocument();
    expect(screen.getByText("Ship by (pickup cutoff)")).toBeInTheDocument();
    expect(screen.getByText("dep-AUS3-1800")).toBeInTheDocument();
    expect(screen.getByText("dep-SAU1-DAU1-2200")).toBeInTheDocument();
    expect(screen.getByText("W-DAU1-1023-A")).toBeInTheDocument();
    expect(screen.getByText("This promise was never revised.")).toBeInTheDocument();
    expect(screen.getByText(/Not judged yet/)).toBeInTheDocument();
  });

  it("renders a broken promise's judgement and late minutes", async () => {
    mockApi({
      "GET /promises/prm-2": json(
        promise({
          promiseId: "prm-2",
          state: "BROKEN",
          judgement: { outcome: "BROKEN", deliveredAt: "2026-10-24T03:30:00Z", lateByMinutes: 90, cause: "LATE" },
        }),
      ),
    });
    renderDetail("prm-2");
    expect(await screen.findByText("90 min")).toBeInTheDocument();
    expect(screen.getByText("LATE")).toBeInTheDocument();
  });

  it("renders the release reason of a released hold", async () => {
    mockApi({
      "GET /promises/prm-3": json(promise({ promiseId: "prm-3", state: "RELEASED", orderId: undefined, releaseReason: "hold expired" })),
    });
    renderDetail("prm-3");
    expect(await screen.findByText("hold expired")).toBeInTheDocument();
    expect(screen.getByText("not confirmed yet")).toBeInTheDocument();
  });

  it("surfaces a 404 problem", async () => {
    mockApi({ "GET /promises/prm-404": problem(404, "not-found", "Not found", "the promise does not exist") });
    renderDetail("prm-404");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Not found");
    expect(alert).toHaveTextContent("the promise does not exist");
  });
});
