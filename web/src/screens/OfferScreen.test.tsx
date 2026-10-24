import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { OfferLookupScreen, OfferRoute } from "./OfferScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { offer } from "../test/fixtures";

// The lookup navigates to offers/:offerId on submit, so both routes are
// mounted the way App mounts them.
function renderLookup() {
  return render(
    <MemoryRouter initialEntries={["/offers"]}>
      <Routes>
        <Route path="/offers" element={<OfferLookupScreen />} />
        <Route path="/offers/:offerId" element={<OfferRoute />} />
      </Routes>
    </MemoryRouter>,
  );
}

// useParams needs a matched route, so the detail route element is mounted
// the way App mounts it.
function renderOfferRoute(offerId: string) {
  return render(
    <MemoryRouter initialEntries={[`/offers/${offerId}`]}>
      <Routes>
        <Route path="/offers/:offerId" element={<OfferRoute />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("OfferScreen", () => {
  it("looks an offer up and shows its witness path", async () => {
    mockApi({ "GET /delivery-offers/off-1": json(offer()) });
    window.history.replaceState({}, "", "/offers");
    renderLookup();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Offer"), "off-1");
    await user.click(screen.getByRole("button", { name: "View offer" }));
    expect(await screen.findByRole("heading", { name: "Offer off-1" })).toBeInTheDocument();
    expect(screen.getByText("N-AUS3")).toBeInTheDocument();
    expect(screen.getByText("dep-AUS3-1800")).toBeInTheDocument();
    expect(screen.getByText("W-DAU1-1023-A")).toBeInTheDocument();
    expect(screen.getByText("CONSUMED")).toBeInTheDocument();
  });

  it("renders the route element for a quoted offer's window", async () => {
        mockApi({
      "GET /delivery-offers/off-2": json(
        offer({ offerId: "off-2", state: "QUOTED", expiresAt: "2026-10-22T19:15:00Z" }),
      ),
    });
    renderOfferRoute("off-2");
    expect(await screen.findByText("Delivery window")).toBeInTheDocument();
    expect(screen.getByText("2026-10-23 14:00:00Z")).toBeInTheDocument();
    expect(screen.getByText("2026-10-24 02:00:00Z")).toBeInTheDocument();
    expect(screen.getByText("Expires at")).toBeInTheDocument();
  });

  it("shows the dashes of a NOMINAL standard-tier path (no departure, no wave)", async () => {
        mockApi({
      "GET /delivery-offers/off-3": json(
        offer({
          offerId: "off-3", tier: "STANDARD", basis: "NOMINAL", state: "QUOTED",
          witnessPath: { nodeId: "N-AUS3", legs: [] },
        }),
      ),
    });
    renderOfferRoute("off-3");
    expect(await screen.findByRole("heading", { name: "Offer off-3" })).toBeInTheDocument();
    expect(screen.getByText("NOMINAL")).toBeInTheDocument();
    const facts = screen.getAllByText("—");
    expect(facts.length).toBeGreaterThanOrEqual(3); // departure, legs, wave
  });

  it("surfaces a 404 problem for an expired-and-purged offer", async () => {
        mockApi({ "GET /delivery-offers/off-404": problem(404, "not-found", "Not found", "the offer does not exist") });
    renderOfferRoute("off-404");
    expect(await screen.findByRole("alert")).toHaveTextContent("the offer does not exist");
  });
});
