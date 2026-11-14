Feature: Capacity rules of the promise book and of the stock a quote sells
  A promise book counts the orders the network has promised onto one node
  pickup cutoff: provisional plus confirmed never exceed the share, a hold that
  reaches the share says so, the clock closes the book at the cutoff, and a
  released hold gives its place back. The stock a quote sells is the listing
  figure net of the units already held. Source: the aggregate canvas of
  PromiseBook (invariants, corrective policies, commands), the use cases and
  ADR 0002 (A3, A12-A15) and ADR 0015.

  Background:
    Given the worked-example network is projected

  # ---- BookShare (A12) ----------------------------------------------------

  Scenario: A slot of 3 orders at a 50 percent share holds 2 orders
    Given the node "N-AUS3" declares a cutoff slot of 3 orders
    And the promise policy is published with an offer TTL of 10 minutes, a hold TTL of 15 minutes, 1 package per order and a 50 percent share
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    When I make a promise from the NEXT_DAY offer
    Then the book "N-AUS3:dep-AUS3-1800" has a share of 2

  Scenario: A share is never below one order
    Given the node "N-AUS3" declares a cutoff slot of 3 orders
    And the promise policy is published with an offer TTL of 10 minutes, a hold TTL of 15 minutes, 1 package per order and a 1 percent share
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    When I make a promise from the NEXT_DAY offer
    Then the book "N-AUS3:dep-AUS3-1800" has a share of 1

  Scenario: Without a slot the hourly throughput cap over the cutoff window sets the share
    # 400 orders an hour over the 7 operating hours between the 11:00 and 18:00 cutoffs
    Given the node "N-AUS3" declares profile revision 3 with no order limit and a throughput cap of 400 per HOUR
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    When I make a promise from the NEXT_DAY offer
    Then the book "N-AUS3:dep-AUS3-1800" has a share of 2800

  Scenario: A daily throughput cap is spread over the operating minutes of the day, rounded up
    # 3000 a day x 420 of 960 operating minutes = 1312.5
    Given the node "N-AUS3" declares profile revision 3 with no order limit and a throughput cap of 3000 per DAY
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    When I make a promise from the NEXT_DAY offer
    Then the book "N-AUS3:dep-AUS3-1800" has a share of 1313

  Scenario: The smaller of the slot and the throughput bound sets the share
    Given the node "N-AUS3" declares profile revision 3 with a cutoff slot of 3000 orders
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    When I make a promise from the NEXT_DAY offer
    Then the book "N-AUS3:dep-AUS3-1800" has a share of 2800

  Scenario: A profile revision that is not newer than the one held is ignored
    Given the node "N-AUS3" declares profile revision 3 with a cutoff slot of 3 orders
    And the node "N-AUS3" declares profile revision 3 with a cutoff slot of 9 orders
    And the node "N-AUS3" declares profile revision 2 with a cutoff slot of 500 orders
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    When I make a promise from the NEXT_DAY offer
    Then the book "N-AUS3:dep-AUS3-1800" has a share of 3

  # ---- Hold, exhaustion, release ------------------------------------------

  Scenario: Only the hold that reaches the share raises the exhaustion signal
    Given the node "N-AUS3" declares a cutoff slot of 2 orders
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    When I make a promise from the NEXT_DAY offer
    Then the last command published the event "PromiseBookExhausted" 0 times
    And the book "N-AUS3:dep-AUS3-1800" holds 1 provisional and 0 confirmed
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    When I make a promise from the NEXT_DAY offer
    Then the last command published the event "PromiseBookExhausted" 1 time
    And the last command published a "PromiseBookExhausted" event whose "Share" is "2"
    And the last command published a "PromiseBookExhausted" event whose "BookID" is "N-AUS3:dep-AUS3-1800"
    And the last command published a "PromiseBookExhausted" event whose "PickupDepartureID" is "dep-AUS3-1800"
    And the book "N-AUS3:dep-AUS3-1800" holds 2 provisional and 0 confirmed

  Scenario: An offer quoted while the book had room is refused once the book filled up behind it
    Given the node "N-AUS3" declares a cutoff slot of 1 order
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And the NEXT_DAY offer is remembered as "first"
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the remembered offer "first"
    When I make a promise from the NEXT_DAY offer
    Then the command is refused with "ErrPromiseCapacityExhausted"
    And the NEXT_DAY offer is QUOTED
    And the book "N-AUS3:dep-AUS3-1800" holds 1 provisional and 0 confirmed
    And the last command published no events

  Scenario: A confirmed order keeps its place in the share
    Given the node "N-AUS3" declares a cutoff slot of 1 order
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the tier NEXT_DAY is unavailable with "NODE_CAPACITY_EXHAUSTED"
    And the book "N-AUS3:dep-AUS3-1800" holds 0 provisional and 1 confirmed

  Scenario: A released hold gives its place back and the book can be exhausted again
    Given the node "N-AUS3" declares a cutoff slot of 1 order
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And the last command published the event "PromiseBookExhausted" 1 time
    And the clock advances 15 minutes
    And the sweeper releases the expired holds
    And the book "N-AUS3:dep-AUS3-1800" holds 0 provisional and 0 confirmed
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is QUOTED
    When I make a promise from the NEXT_DAY offer
    Then the command succeeds
    And the last command published the event "PromiseBookExhausted" 1 time
    And the book "N-AUS3:dep-AUS3-1800" holds 1 provisional and 0 confirmed

  # ---- CloseBooksAtCutoff -------------------------------------------------

  Scenario: The sweeper closes a book at its cutoff and not a minute before
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And the clock is set to "2026-10-22T17:59:00-05:00"
    When the sweeper closes the due books
    Then the book "N-AUS3:dep-AUS3-1800" is OPEN
    Given the clock is set to "2026-10-22T18:00:00-05:00"
    When the sweeper closes the due books
    Then the book "N-AUS3:dep-AUS3-1800" is CLOSED
    When the sweeper closes the due books
    Then the book "N-AUS3:dep-AUS3-1800" is CLOSED
    And the last command published no events

  Scenario: An order placed after the close still confirms its hold and a cancellation still returns it
    Given the promise policy is published with an offer TTL of 10 minutes, a hold TTL of 400 minutes, 1 package per order and a 100 percent share
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And the clock is set to "2026-10-22T18:10:00-05:00"
    And the sweeper closes the due books
    When I confirm the promise for the order "ord-1" group 1
    Then the command succeeds
    And the promise is CONFIRMED
    And the book "N-AUS3:dep-AUS3-1800" is CLOSED
    And the book "N-AUS3:dep-AUS3-1800" holds 0 provisional and 1 confirmed
    When I cancel the promise on request
    Then the promise is CANCELLED
    And the book "N-AUS3:dep-AUS3-1800" is CLOSED
    And the book "N-AUS3:dep-AUS3-1800" holds 0 provisional and 0 confirmed

  # ---- Availability: what a quote may sell (A3, ADR 0015) -----------------

  Scenario: Units held by a provisional promise are not sold twice, and come back when the hold lapses
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    When I quote 36 of "ci-1001" for postal prefix "787"
    Then every tier is unavailable with "ITEM_QUANTITY_NOT_AVAILABLE"
    When I quote 35 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is QUOTED
    Given the clock advances 15 minutes
    And the sweeper releases the expired holds
    When I quote 36 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is QUOTED

  Scenario: Confirmed units stay netted until a stock figure computed two hours after the confirmation lands
    # confirmed at 14:05: a figure counts from 16:05
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    And the clock advances 1 minutes
    When I quote 36 of "ci-1001" for postal prefix "787"
    Then every tier is unavailable with "ITEM_QUANTITY_NOT_AVAILABLE"
    Given the listing "lst-1" reports 37 units as of "2026-10-22T16:04:00-05:00"
    When I quote 36 of "ci-1001" for postal prefix "787"
    Then every tier is unavailable with "ITEM_QUANTITY_NOT_AVAILABLE"
    Given the listing "lst-1" reports 37 units as of "2026-10-22T16:05:00-05:00"
    When I quote 36 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is QUOTED

  Scenario: A suppressed listing ships nothing until it is restored
    Given the listing "lst-1" is suppressed
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then every tier is unavailable with "ITEM_QUANTITY_NOT_AVAILABLE"
    Given the listing "lst-1" is restored
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is QUOTED

  Scenario: A stock figure older than the one held changes nothing
    Given the listing "lst-1" reports 0 units as of "2026-10-22T14:30:00-05:00"
    And the listing "lst-1" reports 37 units as of "2026-10-22T14:10:00-05:00"
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then every tier is unavailable with "ITEM_QUANTITY_NOT_AVAILABLE"
    Given the listing "lst-1" reports 10 units as of "2026-10-22T15:00:00-05:00"
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is QUOTED

  Scenario: Stock that vanished since the quote makes the group unshippable from the witness node
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And the listing "lst-1" reports 1 units as of "2026-10-22T14:30:00-05:00"
    When I make a promise from the NEXT_DAY offer
    Then the command is refused with "ErrGroupSpansNodes"
    And the NEXT_DAY offer is QUOTED
    And the last command published no events
