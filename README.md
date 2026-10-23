# delivery-promise

Retail network, bounded context **Delivery Promise** (Core, commerce tier).

Tells a shopper, before and at purchase, the date a package will arrive, and makes that date true by construction: a delivery offer is quoted only from a feasible witness path (node cutoff, scheduled departures, delivery wave) that has remaining capacity, and a promise holds that capacity until the order is placed or the hold expires. Judges every promise kept or broken.

Status: under construction. The design is proposed in
[retail-network-docs](https://iqvo.github.io/retail-network-docs/contexts/delivery-promise); `model/model.yaml` there is the single source of truth.
This repository is a service of the retail network, not of the warehouse-systems fleet; the fleet reaches the network only through the
`vendor-connectivity` Vendor API.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.retailnetwork.commerce.delivery-promise.<entity>.<Event>`), transactional outbox.

See `docs/adr/` for decisions and `docs/before-first-release.md` for the packaging checklist.
