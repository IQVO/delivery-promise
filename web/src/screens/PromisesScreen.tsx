import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Card, DataTable } from "@warehouse/ui-kit";
import { getPromise, listPromisesByOrder } from "../api";
import { Form, FormRow, SubmitButton, TextField } from "../components/formkit";
import { Facts, PageHeader, Stack, StatePill } from "../components/layout";
import { RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { idProblem, linesLabel, utcLabel } from "../lib";
import type { Promise_ } from "../types";

/** `promises`: the promises of one customer order (GET /promises?orderId=). */
export function PromisesScreen() {
  const [orderId, setOrderId] = useState("");
  const [applied, setApplied] = useState("");
  const id = orderId.trim();
  const problem = id === "" ? null : idProblem("order", id);
  const page = useRequest(applied === "" ? null : () => listPromisesByOrder(applied), `promises|${applied}`);
  const apply = () => {
    if (id !== "" && problem === null) setApplied(id);
  };
  return (
    <Stack gap={5}>
      <PageHeader
        title="Promises"
        subtitle="delivery-promise · the confirmed commitments of one customer order, one per shipment group"
      />
      <Card title="Look up an order">
        <Form label="Order lookup" onSubmit={apply}>
          <FormRow>
            <TextField
              label="Order"
              value={orderId}
              onChange={setOrderId}
              placeholder="ord-1001"
              hint={problem ?? "The order id the promises were confirmed for"}
            />
            <SubmitButton disabled={id === "" || problem !== null}>View promises</SubmitButton>
          </FormRow>
        </Form>
      </Card>
      {applied !== "" && (
        <Card title={`Promises of ${applied}`}>
          <RequestView
            state={page}
            what="the order's promises"
            isEmpty={(p) => p.items.length === 0}
            empty={`No promises were confirmed for ${applied}.`}
          >
            {(p) => <PromiseTable promises={p.items} />}
          </RequestView>
        </Card>
      )}
    </Stack>
  );
}

function PromiseTable({ promises }: { promises: Promise_[] }) {
  return (
    <DataTable
      rowKey={(p) => p.promiseId}
      rows={promises}
      columns={[
        {
          key: "promise",
          header: "Promise",
          // Relative link: `..` is the parent route (this remote's mount
          // point), so it works under the console's /delivery-promise/* and
          // standalone at /.
          render: (p) => <Link to={`../promises/${encodeURIComponent(p.promiseId)}`}>{p.promiseId}</Link>,
        },
        { key: "group", header: "Group", align: "right", render: (p) => String(p.groupNo ?? "—") },
        { key: "tier", header: "Tier", render: (p) => p.terms.tier },
        { key: "state", header: "State", render: (p) => <StatePill state={p.state} /> },
        { key: "shipBy", header: "Ship by", render: (p) => utcLabel(p.terms.shipBy) },
        { key: "deliverBy", header: "Deliver by", render: (p) => utcLabel(p.terms.deliverByLatest) },
        { key: "lines", header: "Lines", render: (p) => linesLabel(p.lines) },
      ]}
    />
  );
}

/** The route element of `promises/:promiseId`. */
export function PromiseDetailRoute() {
  const { promiseId } = useParams();
  return promiseId ? <PromiseScreen promiseId={promiseId} /> : null;
}

/** One promise (GET /promises/{promiseId}): terms, witness path, revisions
 *  and judgement. A promise is never deleted; terminal states stay readable. */
export function PromiseScreen({ promiseId }: { promiseId: string }) {
  const promise = useRequest(() => getPromise(promiseId), `promise|${promiseId}`);
  return (
    <Stack gap={5}>
      <PageHeader title={`Promise ${promiseId}`} subtitle="Terms, witness path, revisions and judgement" />
      <RequestView state={promise} what="the promise">
        {(p) => <PromiseBody promise={p} />}
      </RequestView>
      <Link to="../promises">← Back to the order lookup</Link>
    </Stack>
  );
}

function PromiseBody({ promise }: { promise: Promise_ }) {
  return (
    <>
      <Card title="Promise">
        <Facts
          items={[
            { label: "State", value: <StatePill state={promise.state} /> },
            { label: "Offer", value: <Link to={`../offers/${encodeURIComponent(promise.offerId)}`}>{promise.offerId}</Link> },
            { label: "Order", value: promise.orderId ?? "not confirmed yet" },
            { label: "Group", value: promise.groupNo === undefined ? "—" : String(promise.groupNo) },
            { label: "Destination prefix", value: promise.destinationPostalPrefix },
            { label: "Lines", value: linesLabel(promise.lines) },
            { label: "Hold expires", value: utcLabel(promise.holdExpiresAt) },
            { label: "Policy version", value: promise.terms.policyVersion },
            { label: "Aggregate version", value: String(promise.version) },
            ...(promise.cancelReason ? [{ label: "Cancel reason", value: promise.cancelReason }] : []),
            ...(promise.releaseReason ? [{ label: "Release reason", value: promise.releaseReason }] : []),
            ...(promise.atRiskReason ? [{ label: "At-risk reason", value: promise.atRiskReason }] : []),
          ]}
        />
      </Card>
      <Card title="Terms">
        <Facts
          items={[
            { label: "Tier", value: promise.terms.tier },
            { label: "Basis", value: promise.terms.basis },
            { label: "Ship by (pickup cutoff)", value: utcLabel(promise.terms.shipBy) },
            { label: "Deliver by earliest", value: utcLabel(promise.terms.deliverByEarliest) },
            { label: "Deliver by latest", value: utcLabel(promise.terms.deliverByLatest) },
          ]}
        />
      </Card>
      <Card title="Witness path">
        <Facts
          items={[
            { label: "Node", value: promise.witnessPath.nodeId },
            { label: "Pickup departure", value: promise.witnessPath.pickupDepartureId ?? "—" },
            { label: "Legs", value: promise.witnessPath.legs.length === 0 ? "—" : promise.witnessPath.legs.join(" → ") },
            { label: "Delivery wave", value: promise.witnessPath.deliveryWaveId ?? "—" },
          ]}
        />
      </Card>
      <Card title="Revisions">
        {promise.revisions.length === 0 ? (
          "This promise was never revised."
        ) : (
          <DataTable
            rowKey={(r) => String(r.revisionNo)}
            rows={promise.revisions}
            columns={[
              { key: "no", header: "#", align: "right", render: (r) => String(r.revisionNo) },
              { key: "reason", header: "Reason", render: (r) => r.reason },
              { key: "previousTier", header: "Was", render: (r) => r.previousTerms.tier },
              { key: "revisedAt", header: "Revised at", render: (r) => utcLabel(r.revisedAt) },
            ]}
          />
        )}
      </Card>
      <Card title="Judgement">
        {promise.judgement ? (
          <Facts
            items={[
              { label: "Outcome", value: <StatePill state={promise.judgement.outcome} /> },
              ...(promise.judgement.deliveredAt
                ? [{ label: "Delivered at", value: utcLabel(promise.judgement.deliveredAt) }]
                : []),
              ...(promise.judgement.lateByMinutes !== undefined
                ? [{ label: "Late by", value: `${promise.judgement.lateByMinutes} min` }]
                : []),
              ...(promise.judgement.cause ? [{ label: "Cause", value: promise.judgement.cause }] : []),
            ]}
          />
        ) : (
          "Not judged yet: the verdict lands when the package is delivered."
        )}
      </Card>
    </>
  );
}
