-- Promise calibration read model (ADR 0020): per segment (tier, basis, node
-- grade) the ring of the latest judged promises' lateBy minutes, fed only by
-- this service's own judgements inside the judgement's transaction. Rebuildable
-- from nothing: an empty table is a service that has judged nothing yet, and
-- every segment then quotes as it did before. The PromisePolicy fields of the
-- ADR live in the policy snapshot (promise_policies.snapshot), not in columns.
CREATE TABLE rm_promise_calibration (
    tier         text  NOT NULL,
    basis        text  NOT NULL,
    node_grade   text  NOT NULL,
    observations jsonb NOT NULL,
    PRIMARY KEY (tier, basis, node_grade)
);
