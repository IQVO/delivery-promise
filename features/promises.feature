Feature: Making and confirming promises
  A promise holds capacity in the promise book of its pickup departure until the
  order is placed or the hold expires. MakePromise changes the offer, the
  promise and the book in one transaction (ADR 0002, A22).

  Background:
    Given the worked-example network is projected

  Scenario: Making a promise holds capacity and consumes the offer
    Given I quote 2 of "ci-1001" for postal prefix "787"
    And the clock advances 4 minutes
    When I make a promise from the NEXT_DAY offer
    Then the command succeeds
    And the promise is PROVISIONAL
    And the promise holds the hold expiry "2026-10-22T19:24:00Z"
    And the NEXT_DAY offer is CONSUMED
    And the book "N-AUS3:dep-AUS3-1800" holds 1 provisional and 0 confirmed
    And the book "N-AUS3:dep-AUS3-1800" has a share of 2500
    And the last command published the event "PromiseMade" 1 time

  Scenario: Making the same promise again is a replay
    Given I quote 2 of "ci-1001" for postal prefix "787"
    And a promise exists from the NEXT_DAY offer
    When I make a promise from the NEXT_DAY offer again
    Then the command succeeds
    And the second promise is the first promise
    And the book "N-AUS3:dep-AUS3-1800" holds 1 provisional and 0 confirmed

  Scenario: An expired offer cannot be promised
    Given I quote 2 of "ci-1001" for postal prefix "787"
    And the clock advances 11 minutes
    When I make a promise from the NEXT_DAY offer
    Then the command is refused with "ErrOfferNotQuotable"

  Scenario Outline: A witness path that is no longer feasible refuses the promise
    Given I quote 2 of "ci-1001" for postal prefix "787"
    And the network changes: <change>
    When I make a promise from the NEXT_DAY offer
    Then the command is refused with "ErrPromiseCapacityExhausted"
    And the last command published no events

    Examples:
      | change                                |
      | the linehaul departure is cancelled   |
      | the pickup departure is full          |
      | the delivery wave is full             |
      | the delivery wave is cancelled        |
      | the fulfilment node is suspended      |

  Scenario: The book fills up and is exhausted
    Given the node "N-AUS3" declares a cutoff slot of 1 order
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    Then the last command published the event "PromiseBookExhausted" 1 time
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the tier NEXT_DAY is unavailable with "NODE_CAPACITY_EXHAUSTED"

  Scenario: Confirming a promise moves the hold to confirmed capacity
    Given I quote 2 of "ci-1001" for postal prefix "787"
    And a promise exists from the NEXT_DAY offer
    When I confirm the promise for the order "ord-1" group 1
    Then the command succeeds
    And the promise is CONFIRMED
    And the book "N-AUS3:dep-AUS3-1800" holds 0 provisional and 1 confirmed
    And the last command published the event "PromiseConfirmed" 1 time
    When I confirm the promise for the order "ord-1" group 1
    Then the command succeeds
    And the last command published no events
    When I confirm the promise for the order "ord-2" group 1
    Then the command is refused with "ErrIllegalTransition"

  Scenario: Confirming after the hold lapsed is refused and the sweeper releases it
    Given I quote 2 of "ci-1001" for postal prefix "787"
    And a promise exists from the NEXT_DAY offer
    And the clock advances 15 minutes
    When I confirm the promise for the order "ord-1" group 1
    Then the command is refused with "ErrPromiseHoldExpired"
    When the sweeper releases the expired holds
    Then the promise is RELEASED
    And the book "N-AUS3:dep-AUS3-1800" holds 0 provisional and 0 confirmed
    And the last command published the event "PromiseReleased" 1 time
    When I confirm the promise for the order "ord-1" group 1
    Then the command is refused with "ErrPromiseClosed"

  Scenario: The order-placed event reconciles the confirm and is idempotent
    Given I quote 2 of "ci-1001" for postal prefix "787"
    And a promise exists from the NEXT_DAY offer
    When the order-placed event for the promise is delivered twice
    Then the second delivery changed nothing
    And the promise is CONFIRMED

  Scenario: Cancelling the order releases the confirmed capacity
    Given I quote 2 of "ci-1001" for postal prefix "787"
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    When the order "ord-1" is cancelled
    Then the promise is CANCELLED
    And the book "N-AUS3:dep-AUS3-1800" holds 0 provisional and 0 confirmed
    And the last command published the event "PromiseCancelled" 1 time
    When the order "ord-1" is cancelled
    Then the last command published no events
