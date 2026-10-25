import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BrowserRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { BooksScreen } from "./BooksScreen";
import { json, mockApi } from "../test/fetchMock";
import { book } from "../test/fixtures";

function renderBooks() {
  return render(
    <BrowserRouter>
      <BooksScreen />
    </BrowserRouter>,
  );
}

describe("BooksScreen", () => {
  it("lists the node's books for the date with their holds", async () => {
    mockApi({ "GET /promise-books?nodeId=N-AUS3&date=2026-10-22": json({ items: [book()] }) });
    renderBooks();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Node"), "N-AUS3");
    fireEvent.change(screen.getByLabelText("Date"), { target: { value: "2026-10-22" } });
    await user.click(screen.getByRole("button", { name: "View books" }));
    expect(await screen.findByText("Books of N-AUS3 on 2026-10-22")).toBeInTheDocument();
    expect(screen.getByText("N-AUS3:dep-AUS3-1800")).toBeInTheDocument();
    expect(screen.getByText("2026-10-22 23:00:00Z")).toBeInTheDocument(); // the cutoff, UTC-rendered
    expect(screen.getByText("OPEN")).toBeInTheDocument();
    const headers = ["Share", "Provisional", "Confirmed"].map((h) => screen.getByText(h, { exact: false }));
    expect(headers).toHaveLength(3);
  });

  it("waits for both a node and a date", async () => {
    renderBooks();
    const user = userEvent.setup();
    expect(screen.getByRole("button", { name: "View books" })).toBeDisabled();
    await user.type(screen.getByLabelText("Node"), "N-AUS3");
    expect(screen.getByRole("button", { name: "View books" })).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Date"), { target: { value: "2026-02-30" } });
    expect(screen.getByRole("button", { name: "View books" })).toBeDisabled(); // not a real date
  });

  it("explains a date with no cutoff", async () => {
    mockApi({ "GET /promise-books?nodeId=N-AUS3&date=2026-10-23": json({ items: [] }) });
    renderBooks();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Node"), "N-AUS3");
    fireEvent.change(screen.getByLabelText("Date"), { target: { value: "2026-10-23" } });
    await user.click(screen.getByRole("button", { name: "View books" }));
    expect(await screen.findByText(/has no pickup cutoff on 2026-10-23/)).toBeInTheDocument();
  });

  it("renders a closed book, and refuses a malformed node id locally", async () => {
    mockApi({
      "GET /promise-books?nodeId=N-AUS4&date=2026-10-22": json({
        items: [book({ bookId: "N-AUS4:dep-1", nodeId: "N-AUS4", pickupDepartureId: "dep-1", state: "CLOSED" })],
      }),
    });
    renderBooks();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Node"), "N-AUS4");
    fireEvent.change(screen.getByLabelText("Date"), { target: { value: "2026-10-22" } });
    await user.click(screen.getByRole("button", { name: "View books" }));
    expect(await screen.findByText("CLOSED")).toBeInTheDocument();

    await user.clear(screen.getByLabelText("Node"));
    await user.type(screen.getByLabelText("Node"), "N/A");
    expect(await screen.findByText(/cannot contain spaces/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "View books" })).toBeDisabled();
  });
});
