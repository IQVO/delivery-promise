import { Card } from "@warehouse/ui-kit";
import { getCurrentPolicy } from "../api";
import { DeadlineTable, Facts, PageHeader, Stack } from "../components/layout";
import { RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { utcLabel } from "../lib";

/**
 * The current promise policy (GET /promise-policies/current): how long an
 * offer lives, how long a provisional hold lasts, the packages-per-order
 * conversion, the capacity share and the per-tier order deadlines. v0 is the
 * built-in default that was never published; publishing is a REST operation.
 */
export function PolicyScreen() {
  const policy = useRequest(() => getCurrentPolicy(), "policy");
  return (
    <Stack gap={5}>
      <PageHeader
        title="Promise policy"
        subtitle="The versioned rules every new quote and promise is measured against"
      />
      <RequestView state={policy} what="the current policy">
        {(p) => (
          <>
            <Card title={`Policy ${p.policyVersion}`}>
              <Facts
                items={[
                  { label: "Policy version", value: p.policyVersion },
                  { label: "Published at", value: utcLabel(p.publishedAt) },
                  { label: "Offer TTL", value: `${p.offerTtlMinutes} min` },
                  { label: "Hold TTL", value: `${p.holdTtlMinutes} min` },
                  { label: "Packages per order", value: String(p.packagesPerOrder) },
                  { label: "Capacity share", value: `${p.capacitySharePercent}%` },
                ]}
              />
            </Card>
            <Card title="Order deadlines (destination-local)">
              {p.orderDeadlines.length === 0 ? (
                "No order deadline is configured: every tier stays orderable until the witness cutoff."
              ) : (
                <DeadlineTable rows={p.orderDeadlines} />
              )}
            </Card>
          </>
        )}
      </RequestView>
    </Stack>
  );
}
