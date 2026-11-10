-- Back to one owner per tracking number: keep the lowest assignment id.
DELETE FROM rm_trackings t
 USING rm_trackings keep
 WHERE t.tracking_number = keep.tracking_number
   AND t.assignment_id > keep.assignment_id;
ALTER TABLE rm_trackings DROP CONSTRAINT rm_trackings_pkey;
ALTER TABLE rm_trackings ADD PRIMARY KEY (tracking_number);
