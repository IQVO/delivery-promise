import type { ReactNode } from "react";
import { DataTable, StatusPill } from "@warehouse/ui-kit";

export function PageHeader({ title, subtitle }: { title: string; subtitle: ReactNode }) {
  return (
    <div>
      <h1 style={{ fontSize: "var(--wh-font-size-2xl)", margin: 0 }}>{title}</h1>
      <p style={{ color: "var(--wh-color-text-muted)", marginTop: 4 }}>{subtitle}</p>
    </div>
  );
}

export function Stack({ children, gap = 4 }: { children: ReactNode; gap?: 3 | 4 | 5 }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: `var(--wh-space-${gap})` }}>
      {children}
    </div>
  );
}

/** A label/value list (dl) for a record's scalar fields. */
export function Facts({ items }: { items: { label: string; value: ReactNode }[] }) {
  return (
    <dl
      style={{
        display: "grid",
        gridTemplateColumns: "max-content 1fr",
        gap: "var(--wh-space-2) var(--wh-space-4)",
        margin: 0,
        fontSize: "var(--wh-font-size-sm)",
      }}
    >
      {items.map((item) => (
        <div key={item.label} style={{ display: "contents" }}>
          <dt style={{ color: "var(--wh-color-text-muted)" }}>{item.label}</dt>
          <dd style={{ margin: 0 }}>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

type Tone = "neutral" | "progress" | "success" | "warning" | "danger";

/** A promise's, offer's or book's lifecycle is delivery-promise's own
 *  vocabulary, so StatusPill's tone override carries it. */
const STATE_TONE: Record<string, Tone> = {
  // promises
  PROVISIONAL: "progress",
  CONFIRMED: "success",
  AT_RISK: "warning",
  KEPT: "success",
  BROKEN: "danger",
  RELEASED: "neutral",
  CANCELLED: "neutral",
  // offers
  QUOTED: "progress",
  EXPIRED: "warning",
  CONSUMED: "success",
  // books
  OPEN: "success",
  CLOSED: "neutral",
};

export function StatePill({ state }: { state: string }) {
  return <StatusPill status={state} tone={STATE_TONE[state] ?? "neutral"} size="sm" />;
}

/** The per-tier order deadlines of the policy, one row per tier+region. */
export function DeadlineTable({ rows }: { rows: { tier: string; regionId?: string; localTime: string }[] }) {
  return (
    <DataTable
      rowKey={(d) => `${d.tier}:${d.regionId ?? ""}`}
      rows={rows}
      columns={[
        { key: "tier", header: "Tier", render: (d) => d.tier },
        { key: "region", header: "Region", render: (d) => d.regionId ?? "every region" },
        { key: "localTime", header: "Order deadline (local)", render: (d) => d.localTime },
      ]}
    />
  );
}
