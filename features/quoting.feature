Feature: Quoting delivery offers
  A delivery offer is quoted only from a feasible witness path (node cutoff,
  scheduled departures, delivery wave) that has remaining capacity. The worked
  example: Thursday 14:05 in Austin, a shopper in postal prefix 787 asks for two
  units of ci-1001 stocked at the fulfilment node N-AUS3.

  Background:
    Given the worked-example network is projected

  Scenario: The next-day offer is quoted from the scheduled path
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then the command succeeds
    And the quote offers exactly the tiers "NEXT_DAY,TWO_DAY,STANDARD"
    And the NEXT_DAY offer is SCHEDULED from the node "N-AUS3" via the pickup "dep-AUS3-1800" and the wave "W-DAU1-1023-A"
    And the NEXT_DAY offer promises delivery between "2026-10-23T09:00:00-05:00" and "2026-10-23T21:00:00-05:00"
    And the NEXT_DAY offer is QUOTED
    And the tier SAME_DAY is unavailable with "DELIVERY_SLA_UNAVAILABLE"
    And the last command published the event "DeliveryOfferQuoted" 3 times

  Scenario: A destination without a delivery station cannot be served
    When I quote 2 of "ci-1001" for postal prefix "999"
    Then the command succeeds
    And every tier is unavailable with "DESTINATION_NOT_SERVED"

  Scenario: More units than any node holds cannot be quoted
    When I quote 38 of "ci-1001" for postal prefix "787"
    Then every tier is unavailable with "ITEM_QUANTITY_NOT_AVAILABLE"
    And the last command published no events

  Scenario: A discontinued item cannot be quoted
    Given the item "ci-1001" is discontinued
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the tier NEXT_DAY is unavailable with "ITEM_QUANTITY_NOT_AVAILABLE"

  Scenario: A tier past its order deadline is not offered
    Given the order deadline for NEXT_DAY in the region "R-SC" is "14:00" local time
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then the tier NEXT_DAY is unavailable with "PAST_ORDER_DEADLINE"
    And the quote offers exactly the tiers "TWO_DAY,STANDARD"
    And the quote carries the policy version "v1"

  Scenario: Invalid quote requests are rejected
    When I quote 0 of "ci-1001" for postal prefix "787"
    Then the command is refused with "ErrValidation"
    When I quote 2 of "ci-1001" for postal prefix "78 7"
    Then the command is refused with "ErrValidation"
