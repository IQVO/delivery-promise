import { MemoryRouter, Route, Routes } from "react-router-dom";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import App from "./App";
import { json, mockApi } from "./test/fetchMock";
import { book, offer, policy, promise } from "./test/fixtures";

// The console mounts the remote under its own splat route; the remote's
// relative links must resolve against the mount point, not the full URL.
function renderUnderHost(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/delivery-promise/*" element={<App />} />
      </Routes>
    </MemoryRouter>,
  );
}

const ROUTES = {
  "GET /promises?orderId=ord-1001": json({ items: [promise()] }),
  "GET /promises/prm-1": json(promise()),
  "GET /delivery-offers/off-1": json(offer()),
  "GET /promise-books?nodeId=N-AUS3&date=2026-10-22": json({ items: [book()] }),
  "GET /promise-policies/current": json(policy()),
};

describe("App", () => {
  it("redirects the mount point to the promise lookup", async () => {
    mockApi(ROUTES);
    renderUnderHost("/delivery-promise");
    expect(await screen.findByRole("heading", { name: "Promises" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Promises" })).toHaveAttribute("href", "/delivery-promise/promises");
  });

  it("keeps the sub-nav at the mount point from a deep route", async () => {
    mockApi(ROUTES);
    renderUnderHost("/delivery-promise/promises/prm-1");
    await screen.findByRole("heading", { name: "Promise prm-1" });
    expect(screen.getByRole("link", { name: "Offers" })).toHaveAttribute("href", "/delivery-promise/offers");
    expect(screen.getByRole("link", { name: "Books" })).toHaveAttribute("href", "/delivery-promise/books");
    expect(screen.getByRole("link", { name: "Policy" })).toHaveAttribute("href", "/delivery-promise/policy");
  });

  it("navigates between the sections, into a promise and its offer", async () => {
    mockApi(ROUTES);
    renderUnderHost("/delivery-promise/promises");
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("Order"), "ord-1001");
    await user.click(screen.getByRole("button", { name: "View promises" }));
    await screen.findByRole("link", { name: "prm-1" }); // the order's promise row

    await user.click(screen.getByRole("link", { name: "prm-1" }));
    await screen.findByRole("heading", { name: "Promise prm-1" });
    await user.click(screen.getByRole("link", { name: "off-1" }));
    await screen.findByRole("heading", { name: "Offer off-1" });

    await user.click(screen.getByRole("link", { name: "Books" }));
    await user.type(screen.getByLabelText("Node"), "N-AUS3");
    // a date input takes its value in one change, not keystroke by keystroke
    fireEvent.change(screen.getByLabelText("Date"), { target: { value: "2026-10-22" } });
    await user.click(screen.getByRole("button", { name: "View books" }));
    expect(await screen.findByText("N-AUS3:dep-AUS3-1800")).toBeInTheDocument();

    await user.click(screen.getByRole("link", { name: "Policy" }));
    expect(await screen.findByRole("heading", { name: "Promise policy" })).toBeInTheDocument();
    expect(await screen.findByText("23:59")).toBeInTheDocument();
  });
});
