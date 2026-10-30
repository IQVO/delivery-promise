-- ADR 0011: deliberately keeps every Window.TimeZone in place.
--
-- 0002 adds no column; it only fills an optional snapshot key that binaries
-- before ADR 0011 ignore (json.Unmarshal drops unknown keys). Stripping it
-- would also erase the zones quoting wrote since, and re-running the up would
-- re-derive them from TODAY's area-to-station map, which may differ from the
-- one the offer was quoted under. So rolling back is a no-op on the data.
SELECT 1;
