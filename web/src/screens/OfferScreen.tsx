import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Card } from "@warehouse/ui-kit";
import { getOffer } from "../api";
import { Form, FormRow, SubmitButton, TextField } from "../components/formkit";
import { Facts, PageHeader, Stack, StatePill } from "../components/layout";
import { RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { idProblem, linesLabel, utcLabel } from "../lib";

/** `offers`: a lookup by offer id. */
export function OfferLookupScreen() {
  const [offerId, setOfferId] = useState("");
  const navigate = useNavigate();
  const id = offerId.trim();
  const problem = id === "" ? null : idProblem("offer", id);
  const go = () => {
    if (id !== "" && problem === null) navigate(encodeURIComponent(id));
  };
  return (
    <Stack gap={5}>
      <PageHeader title="Delivery offers" subtitle="The short-lived quotes a shopper chooses from, with their witness path" />
      <Card title="Look up an offer">
        <Form label="Offer lookup" onSubmit={go}>
          <FormRow>
            <TextField
              label="Offer"
              value={offerId}
              onChange={setOfferId}
              placeholder="off-1"
              hint={problem ?? "Offers stay readable for a retention period after they expire"}
            />
            <SubmitButton disabled={id === "" || problem !== null}>View offer</SubmitButton>
          </FormRow>
        </Form>
      </Card>
    </Stack>
  );
}

/** The route element of `offers/:offerId`. */
export function OfferRoute() {
  const { offerId } = useParams();
  return offerId ? <OfferScreen offerId={offerId} /> : null;
}

/** One offer (GET /delivery-offers/{offerId}) in any state: QUOTED, EXPIRED
 *  or CONSUMED. */
export function OfferScreen({ offerId }: { offerId: string }) {
  const offer = useRequest(() => getOffer(offerId), `offer|${offerId}`);
  return (
    <Stack gap={5}>
      <PageHeader title={`Offer ${offerId}`} subtitle="Tier, window and the path that proved it feasible" />
      <RequestView state={offer} what="the offer">
        {(o) => (
          <>
            <Card title="Offer">
              <Facts
                items={[
                  { label: "State", value: <StatePill state={o.state} /> },
                  { label: "Tier", value: o.tier },
                  { label: "Group", value: String(o.groupNo) },
                  { label: "Destination prefix", value: o.destinationPostalPrefix },
                  { label: "Lines", value: linesLabel(o.lines) },
                  { label: "Basis", value: o.basis },
                  { label: "Policy version", value: o.policyVersion },
                  { label: "Quoted at", value: utcLabel(o.quotedAt) },
                  { label: "Expires at", value: utcLabel(o.expiresAt) },
                ]}
              />
            </Card>
            <Card title="Delivery window">
              <Facts
                items={[
                  { label: "Earliest", value: utcLabel(o.window.earliest) },
                  { label: "Latest", value: utcLabel(o.window.latest) },
                ]}
              />
            </Card>
            <Card title="Witness path">
              <Facts
                items={[
                  { label: "Node", value: o.witnessPath.nodeId },
                  { label: "Pickup departure", value: o.witnessPath.pickupDepartureId ?? "—" },
                  { label: "Legs", value: o.witnessPath.legs.length === 0 ? "—" : o.witnessPath.legs.join(" → ") },
                  { label: "Delivery wave", value: o.witnessPath.deliveryWaveId ?? "—" },
                ]}
              />
            </Card>
          </>
        )}
      </RequestView>
      <Link to="../offers">← Look up another offer</Link>
    </Stack>
  );
}
