import { Navigate, NavLink, Outlet, Route, Routes } from "react-router-dom";
import { OfferLookupScreen, OfferRoute } from "./screens/OfferScreen";
import { PolicyScreen } from "./screens/PolicyScreen";
import { BooksScreen } from "./screens/BooksScreen";
import { PromiseDetailRoute, PromisesScreen } from "./screens/PromisesScreen";

const SUB_NAV = [
  { to: "promises", label: "Promises" },
  { to: "offers", label: "Offers" },
  { to: "books", label: "Books" },
  { to: "policy", label: "Policy" },
];

const linkStyle = ({ isActive }: { isActive: boolean }) => ({
  display: "inline-flex",
  padding: "6px 12px",
  borderRadius: "var(--wh-radius-pill)",
  fontSize: "var(--wh-font-size-sm)",
  fontWeight: isActive ? 600 : 500,
  color: isActive ? "var(--wh-color-text)" : "var(--wh-color-text-muted)",
  background: isActive ? "var(--wh-color-accent-muted)" : "transparent",
  textDecoration: "none",
});

/** The sub-nav, rendered by a layout route with the path "/".
 *
 *  The console mounts this component inside its own `<Route
 *  path="/delivery-promise/*">`. A relative `<NavLink>` rendered straight in
 *  that splat route resolves against the FULL current URL (react-router 7 uses
 *  the last path-contributing match's pathname, splat included), so from
 *  /delivery-promise/books the link `promises` would point at
 *  /delivery-promise/books/promises. A pathless layout route does not help
 *  (react-router drops pathless routes from relative resolution); a layout
 *  route WITH path "/" does: its own match is the mount point, under any
 *  prefix and standalone at `/`. (Same fix as the task-interleaving remote;
 *  App.test.tsx mounts under the host's splat route to prove it.) */
function PromiseLayout() {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <nav aria-label="Delivery promise sections" style={{ display: "flex", gap: "var(--wh-space-2)" }}>
        {SUB_NAV.map((item) => (
          <NavLink key={item.to} to={item.to} style={linkStyle}>
            {item.label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}

/** Exposed as delivery_promise_mfe/App via Module Federation. Takes NO props:
 *  the console mounts it under `/delivery-promise/*` and provides the
 *  BrowserRouter, the design tokens and `window.__WAREHOUSE_CONFIG__`. Routes
 *  here are RELATIVE, so the component works identically under that prefix (in
 *  the shell) or at / (standalone dev, see main.tsx). It calls ONLY this
 *  context's own API (/api/delivery-promise) and every screen is read-only.
 *
 *   - (index): redirects to promises.
 *   - promises: the promises of one order; promises/:promiseId: one promise.
 *   - offers: a lookup by offer id.
 *   - books: the capacity books of one node and date.
 *   - policy: the current promise policy. */
export default function App() {
  return (
    <Routes>
      <Route path="/" element={<PromiseLayout />}>
        <Route index element={<Navigate to="promises" replace />} />
        <Route path="promises" element={<PromisesScreen />} />
        <Route path="promises/:promiseId" element={<PromiseDetailRoute />} />
        <Route path="offers" element={<OfferLookupScreen />} />
        <Route path="offers/:offerId" element={<OfferRoute />} />
        <Route path="books" element={<BooksScreen />} />
        <Route path="policy" element={<PolicyScreen />} />
      </Route>
    </Routes>
  );
}
