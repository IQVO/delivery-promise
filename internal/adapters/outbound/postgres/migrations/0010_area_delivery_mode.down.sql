DROP TABLE rm_carrier_transit_open;
ALTER TABLE rm_areas
    DROP COLUMN carrier_options,
    DROP COLUMN delivery_mode;
