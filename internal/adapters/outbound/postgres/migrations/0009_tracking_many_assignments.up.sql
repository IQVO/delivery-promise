-- A tracking number may belong to several assignments (ADR 0025). A merged
-- package carries one tracking number while every member keeps its own
-- assignment, so the key is the pair and a second assignment no longer
-- re-owns the number.
ALTER TABLE rm_trackings DROP CONSTRAINT rm_trackings_pkey;
ALTER TABLE rm_trackings ADD PRIMARY KEY (tracking_number, assignment_id);
