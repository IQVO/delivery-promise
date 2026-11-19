-- Promise coverage per region (ADR 0030). Additive: two columns on
-- offers_quoted, nothing dropped or rewritten, 0001 untouched.
--
--   region         the FIRST TWO characters of the destination postal prefix
--                  (computed by internal/analytics/report when the fact is
--                  applied: the third character is never stored). '' when the
--                  event carried no prefix, and for every offer projected
--                  before this migration: those are left out of the coverage.
--   promised_days  local calendar days from the quote day to the day of
--                  deliverByLatest (>= 0), NULL when the offer carried no
--                  latest delivery.
ALTER TABLE offers_quoted
    ADD COLUMN region        TEXT    NOT NULL DEFAULT '' CHECK (char_length(region) <= 2),
    ADD COLUMN promised_days INTEGER CHECK (promised_days >= 0);
