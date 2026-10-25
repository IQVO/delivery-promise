import { useState } from "react";
import { Card, DataTable } from "@warehouse/ui-kit";
import { listPromiseBooks } from "../api";
import { Form, FormRow, SubmitButton, TextField } from "../components/formkit";
import { PageHeader, Stack, StatePill } from "../components/layout";
import { RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { dateProblem, idProblem, utcLabel } from "../lib";

/**
 * The capacity ledger of one node (GET /promise-books?nodeId=&date=): one
 * book per pickup cutoff of the date, in the node's own time zone, with the
 * share and the provisional/confirmed holds on each.
 */
export function BooksScreen() {
  const [nodeId, setNodeId] = useState("");
  const [date, setDate] = useState("");
  const [applied, setApplied] = useState<[string, string] | null>(null);
  const node = nodeId.trim();
  const day = date.trim();
  const nodeProblem = node === "" ? null : idProblem("node", node);
  const dayProblem = day === "" ? null : dateProblem(day);
  const page = useRequest(
    applied === null ? null : () => listPromiseBooks(applied[0], applied[1]),
    `books|${applied?.[0]}|${applied?.[1]}`,
  );
  const apply = () => {
    if (nodeProblem === null && dayProblem === null && node !== "" && day !== "") setApplied([node, day]);
  };
  return (
    <Stack gap={5}>
      <PageHeader
        title="Promise books"
        subtitle="delivery-promise · the capacity ledger of one node's pickup cutoffs on one date"
      />
      <Card title="Pick a node and a date">
        <Form label="Book lookup" onSubmit={apply}>
          <FormRow>
            <TextField
              label="Node"
              value={nodeId}
              onChange={setNodeId}
              placeholder="N-AUS3"
              hint={nodeProblem ?? "The fulfillment node id"}
            />
            <TextField
              label="Date"
              value={date}
              onChange={setDate}
              placeholder="2026-10-22"
              type="date"
              hint={dayProblem ?? "The cutoff date in the node's own time zone"}
            />
            <SubmitButton disabled={node === "" || day === "" || nodeProblem !== null || dayProblem !== null}>
              View books
            </SubmitButton>
          </FormRow>
        </Form>
      </Card>
      {applied !== null && (
        <Card title={`Books of ${applied[0]} on ${applied[1]}`}>
          <RequestView
            state={page}
            what="the node's books"
            isEmpty={(p) => p.items.length === 0}
            empty={`${applied[0]} has no pickup cutoff on ${applied[1]} (or its books were never opened: a book opens lazily on the first hold).`}
          >
            {(p) => (
              <DataTable
                rowKey={(b) => b.bookId}
                rows={p.items}
                columns={[
                  { key: "book", header: "Book", render: (b) => b.bookId },
                  { key: "cutoff", header: "Cutoff at", render: (b) => utcLabel(b.cutoffAt) },
                  { key: "state", header: "State", render: (b) => <StatePill state={b.state} /> },
                  { key: "share", header: "Share", align: "right", render: (b) => String(b.capacity.share) },
                  { key: "provisional", header: "Provisional", align: "right", render: (b) => String(b.capacity.provisional) },
                  { key: "confirmed", header: "Confirmed", align: "right", render: (b) => String(b.capacity.confirmed) },
                  { key: "version", header: "Version", align: "right", render: (b) => String(b.version) },
                ]}
              />
            )}
          </RequestView>
        </Card>
      )}
    </Stack>
  );
}
