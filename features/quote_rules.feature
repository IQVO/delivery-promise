Feature: Quote rules of the promise search
  The documented rules of QuoteDeliveryOffers that decide whether, and how, a
  tier is offered: the scorecard handicap, the offer expiry, the policy knobs,
  which nodes may promise SAME_DAY, how a cart is split, which constraint blocks
  a tier and the order deadline per tier and region. Source: the business
  context, the use cases and the aggregate canvas of the context, with ADR 0002
  (A3-A11) for the numbers. The worked example is Thursday 2026-10-22 14:05 in
  Austin (CDT, UTC-5), ci-1001 stocked at N-AUS3, whose 18:00 pickup is the
  witness.

  Background:
    Given the worked-example network is projected

  # ---- ScorecardHandicap -------------------------------------------------

  Scenario: Grade B pushes the ship-by by a quarter of the handling time, rounded up to a minute
    # handling 95 min x 1.25 = 118.75, so 119: the 18:00 pickup is met only until 16:01
    Given the node "N-AUS3" is graded "B" in the 30d window
    And the clock is set to "2026-10-22T16:00:00-05:00"
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is SCHEDULED from the node "N-AUS3" via the pickup "dep-AUS3-1800" and the wave "W-DAU1-1023-A"
    And the NEXT_DAY offer expires at "2026-10-22T16:01:00-05:00"

  Scenario Outline: A node's grade decides whether the 18:00 pickup is still reachable at 16:01
    Given the node "N-AUS3" is graded "<grade>" in the 30d window
    And the clock is set to "2026-10-22T16:01:00-05:00"
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then the quote offers exactly the tiers "<tiers>"

    Examples:
      | grade       | tiers                     |
      | A           | NEXT_DAY,TWO_DAY,STANDARD |
      | PROVISIONAL | NEXT_DAY,TWO_DAY,STANDARD |
      | B           | STANDARD                  |

  Scenario Outline: Grade C skips one feasible pickup and grade D two
    Given an earlier pickup "dep-AUS3-1730" is scheduled
    And the node "N-AUS3" is graded "<grade>" in the 30d window
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is SCHEDULED from the node "N-AUS3" via the pickup "<pickup>" and the wave "W-DAU1-1023-A"

    Examples:
      | grade | pickup        |
      | A     | dep-AUS3-1730 |
      | B     | dep-AUS3-1730 |
      | C     | dep-AUS3-1800 |

  Scenario: Grade D with only two pickups left has no NEXT_DAY path
    Given an earlier pickup "dep-AUS3-1730" is scheduled
    And the node "N-AUS3" is graded "D" in the 30d window
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the tier NEXT_DAY is unavailable with "DELIVERY_SLA_UNAVAILABLE"

  Scenario: Only the 30-day scorecard is applied
    Given an earlier pickup "dep-AUS3-1730" is scheduled
    And the node "N-AUS3" is graded "D" in the 7d window
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is SCHEDULED from the node "N-AUS3" via the pickup "dep-AUS3-1730" and the wave "W-DAU1-1023-A"

  # ---- Offer expiry and policy knobs -------------------------------------

  Scenario: An offer never outlives the instant its pickup cutoff can still be met
    # 18:00 minus 95 minutes of handling is 16:25, sooner than the 10 minute TTL (16:34)
    Given the clock is set to "2026-10-22T16:24:00-05:00"
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer expires at "2026-10-22T16:25:00-05:00"

  Scenario: An offer whose pickup can no longer be met is not quoted
    Given the clock is set to "2026-10-22T16:26:00-05:00"
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the tier NEXT_DAY is unavailable with "DELIVERY_SLA_UNAVAILABLE"

  Scenario: The offer TTL, the hold TTL and the version come from the published policy
    Given the promise policy is published with an offer TTL of 12 minutes, a hold TTL of 20 minutes, 1 package per order and a 100 percent share
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer expires at "2026-10-22T14:17:00-05:00"
    And the quote carries the policy version "v1"
    When I make a promise from the NEXT_DAY offer
    Then the promise holds the hold expiry "2026-10-22T19:25:00Z"
    And the promise carries the policy version "v1"

  Scenario: A promise keeps the policy version of the quote it was made from
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And the promise policy is published with an offer TTL of 12 minutes, a hold TTL of 20 minutes, 1 package per order and a 100 percent share
    And the current promise policy is "v1"
    When I make a promise from the NEXT_DAY offer
    Then the promise carries the policy version "v0"
    And the promise holds the hold expiry "2026-10-22T19:25:00Z"

  Scenario: The sweeper leaves an offer consumed by a promise alone
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And the clock advances 11 minutes
    When the sweeper expires the quoted offers
    Then the NEXT_DAY offer is CONSUMED

  Scenario: The sweeper leaves an offer inside its TTL alone
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And the clock advances 5 minutes
    When the sweeper expires the quoted offers
    Then the NEXT_DAY offer is QUOTED

  # ---- Packages per order (A6) -------------------------------------------

  Scenario Outline: An order needs its packages of room on the pickup and on every linehaul departure
    Given the promise policy is published with an offer TTL of 10 minutes, a hold TTL of 15 minutes, 2 packages per order and a 100 percent share
    And the departure "<departure>" has 2 packages left
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is SCHEDULED from the node "N-AUS3" via the pickup "dep-AUS3-1800" and the wave "W-DAU1-1023-A"

    Examples:
      | departure          |
      | dep-AUS3-1800      |
      | dep-SAU1-DAU1-2200 |

  Scenario Outline: One package short on any departure of the path blocks the tier by capacity, not by schedule
    Given the promise policy is published with an offer TTL of 10 minutes, a hold TTL of 15 minutes, 2 packages per order and a 100 percent share
    And the departure "<departure>" has 1 package left
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the tier NEXT_DAY is unavailable with "NODE_CAPACITY_EXHAUSTED"

    Examples:
      | departure          |
      | dep-AUS3-1800      |
      | dep-SAU1-DAU1-2200 |

  Scenario: A departure that keeps exactly the packages an order needs still takes the promise
    Given the promise policy is published with an offer TTL of 10 minutes, a hold TTL of 15 minutes, 2 packages per order and a 100 percent share
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And the departure "dep-AUS3-1800" has 2 packages left
    When I make a promise from the NEXT_DAY offer
    Then the command succeeds
    And the promise is PROVISIONAL

  Scenario: A departure that lost a package since the quote refuses the promise
    Given the promise policy is published with an offer TTL of 10 minutes, a hold TTL of 15 minutes, 2 packages per order and a 100 percent share
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And the departure "dep-SAU1-DAU1-2200" has 1 package left
    When I make a promise from the NEXT_DAY offer
    Then the command is refused with "ErrPromiseCapacityExhausted"
    And the NEXT_DAY offer is QUOTED
    And the last command published no events

  Scenario: A delivery wave with a single stop left still takes the promise
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And the delivery wave "W-DAU1-1023-A" has 1 stop left
    When I make a promise from the NEXT_DAY offer
    Then the command succeeds
    And the promise is PROVISIONAL

  # ---- Which constraint blocks a tier (A11) -------------------------------

  Scenario Outline: A tier without an offer names what blocked it
    Given the network changes: <change>
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the tier NEXT_DAY is unavailable with "<constraint>"

    Examples:
      | change                              | constraint               |
      | the pickup departure is full        | NODE_CAPACITY_EXHAUSTED  |
      | the delivery wave is full           | NODE_CAPACITY_EXHAUSTED  |
      | the linehaul departure is cancelled | DELIVERY_SLA_UNAVAILABLE |
      | the delivery wave is cancelled      | DELIVERY_SLA_UNAVAILABLE |

  # ---- The delivery wave the path ends in (A8) ----------------------------

  Scenario: A package that reaches the station exactly at the induct cutoff still rides that wave
    Given the next day's delivery wave "W-DAU1-1024-B" is scheduled
    And the departure "dep-SAU1-DAU1-2200" is cancelled
    And a linehaul departure "dep-SAU1-DAU1-2300" is scheduled with the cutoff "2026-10-22T23:00:00-05:00" and the arrival "2026-10-23T06:00:00-05:00" for 140 packages
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is SCHEDULED from the node "N-AUS3" via the pickup "dep-AUS3-1800" and the wave "W-DAU1-1023-A"

  Scenario: A package that reaches the station a minute after the induct cutoff rides the next wave
    Given the next day's delivery wave "W-DAU1-1024-B" is scheduled
    And the departure "dep-SAU1-DAU1-2200" is cancelled
    And a linehaul departure "dep-SAU1-DAU1-2300" is scheduled with the cutoff "2026-10-22T23:00:00-05:00" and the arrival "2026-10-23T06:01:00-05:00" for 140 packages
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier TWO_DAY
    Then the TWO_DAY offer is SCHEDULED from the node "N-AUS3" via the pickup "dep-AUS3-1800" and the wave "W-DAU1-1024-B"
    And the TWO_DAY offer promises delivery between "2026-10-24T09:00:00-05:00" and "2026-10-24T21:00:00-05:00"
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the tier NEXT_DAY is unavailable with "DELIVERY_SLA_UNAVAILABLE"

  # ---- SAME_DAY (A5) ------------------------------------------------------

  Scenario Outline: SAME_DAY is promised from a micro or sub-same-day node that injects straight into the delivery station
    Given the <subtype> fulfilment node "N-MIC1" feeds the station "N-DAU1" directly for a same-day wave
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier SAME_DAY
    Then the SAME_DAY offer is SCHEDULED from the node "N-MIC1" via the pickup "dep-MIC1-1630" and the wave "W-DAU1-1022-E"
    And the SAME_DAY offer promises delivery between "2026-10-22T19:00:00-05:00" and "2026-10-22T21:00:00-05:00"

    Examples:
      | subtype      |
      | MICRO        |
      | SUB_SAME_DAY |

  Scenario: A sortable node on the same direct path earns NEXT_DAY but never SAME_DAY
    Given the SORTABLE fulfilment node "N-MIC1" feeds the station "N-DAU1" directly for a same-day wave
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then the NEXT_DAY offer is SCHEDULED from the node "N-MIC1" via the pickup "dep-MIC1-1630" and the wave "W-DAU1-1022-E"
    And the tier SAME_DAY is unavailable with "DELIVERY_SLA_UNAVAILABLE"

  # ---- ShipmentGrouping (A2) ----------------------------------------------

  Scenario: A cart no single node covers is split into the fewest groups
    Given the second fulfilment node "N-DFW7" is projected with a pickup at 18:00
    When I quote the cart "ci-1001:2,ci-2002:1" for postal prefix "787"
    Then the quote has 2 groups
    And the group 1 NEXT_DAY offer ships from the node "N-AUS3" and covers "ci-1001:2"
    And the group 2 NEXT_DAY offer ships from the node "N-DFW7" and covers "ci-2002:1"

  Scenario: A cart one node covers stays whole although another node stocks part of it
    Given the second fulfilment node "N-DFW7" is projected with a pickup at 18:00
    And the node "N-AUS3" stocks 5 of "ci-2002"
    When I quote the cart "ci-1001:2,ci-2002:1" for postal prefix "787"
    Then the quote has 1 group
    And the group 1 NEXT_DAY offer ships from the node "N-AUS3" and covers "ci-1001:2,ci-2002:1"

  # ---- TierOrderDeadline: region override, other regions, other tiers -----

  Scenario: A region's own deadline overrides the all-region one
    Given the NEXT_DAY order deadlines are "14:00" for every region and "15:00" for the region "R-SC"
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then the quote offers exactly the tiers "NEXT_DAY,TWO_DAY,STANDARD"

  Scenario: A region's own earlier deadline overrides a later all-region one
    Given the NEXT_DAY order deadlines are "15:00" for every region and "14:00" for the region "R-SC"
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then the tier NEXT_DAY is unavailable with "PAST_ORDER_DEADLINE"
    And the quote offers exactly the tiers "TWO_DAY,STANDARD"

  Scenario: Another region's deadline does not apply to the destination region
    Given the NEXT_DAY order deadlines are "15:00" for every region and "14:00" for the region "R-NW"
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then the quote offers exactly the tiers "NEXT_DAY,TWO_DAY,STANDARD"

  Scenario: A deadline binds only its own tier
    Given the TWO_DAY order deadline in the region "R-SC" is "14:00" local time
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then the tier TWO_DAY is unavailable with "PAST_ORDER_DEADLINE"
    And the quote offers exactly the tiers "NEXT_DAY,STANDARD"

  # ---- Node eligibility for the fast tiers (ADR 0022) ---------------------

  Scenario: A node ineligible for NEXT_DAY keeps offering the slower tiers
    Given the node "N-AUS3" is ineligible for NEXT_DAY as of "2026-10-22T10:00:00Z"
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then the tier NEXT_DAY is unavailable with "DELIVERY_SLA_UNAVAILABLE"
    And the quote offers exactly the tiers "TWO_DAY,STANDARD"

  Scenario: An older eligibility fact changes nothing and a newer one reinstates the node
    Given the node "N-AUS3" is ineligible for NEXT_DAY as of "2026-10-22T10:00:00Z"
    And the node "N-AUS3" is eligible for NEXT_DAY as of "2026-10-22T09:00:00Z"
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then the quote offers exactly the tiers "TWO_DAY,STANDARD"
    Given the node "N-AUS3" is eligible for NEXT_DAY as of "2026-10-22T11:00:00Z"
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then the quote offers exactly the tiers "NEXT_DAY,TWO_DAY,STANDARD"
