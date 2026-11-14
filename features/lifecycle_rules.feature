Feature: Lifecycle rules of the promise
  A promise is PROVISIONAL until the order is placed, CONFIRMED afterwards, may
  go AT_RISK and be revised, and ends KEPT, BROKEN, RELEASED or CANCELLED. Every
  transition raises exactly one event, a terminal promise ignores later facts,
  and a redelivered fact commits nothing. Source: the aggregate canvas of
  Promise (state transitions, invariants, corrective policies, handled
  commands), the use cases ConfirmPromise, CancelPromise, FlagPromiseAtRisk,
  JudgePromise and ReleaseExpiredHolds, ADR 0021 (the late-risk sweep) and
  ADR 0025 (a tracking number shared by several assignments). The worked
  example is Thursday 2026-10-22 14:05 in Austin (CDT, UTC-5); the NEXT_DAY
  window ends Friday 21:00.

  Background:
    Given the worked-example network is projected

  # ---- ReleaseExpiredHolds and the hold deadline --------------------------

  Scenario: A hold is still confirmable one minute before it lapses and the sweeper leaves it alone
    # hold TTL 15 minutes: the hold lapses at 14:20
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And the clock advances 14 minutes
    When the sweeper releases the expired holds
    Then the promise is PROVISIONAL
    And the last command published no events
    When I confirm the promise for the order "ord-1" group 1
    Then the command succeeds
    And the promise is CONFIRMED

  Scenario: The sweeper releases a hold the minute it lapses and gives its place back
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And the clock advances 15 minutes
    When the sweeper releases the expired holds
    Then the promise is RELEASED
    And the last command published the event "PromiseReleased" 1 time
    And the book "N-AUS3:dep-AUS3-1800" holds 0 provisional and 0 confirmed
    When the sweeper releases the expired holds
    Then the last command published no events

  Scenario: The sweeper never releases a promise that was confirmed in time
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    And the clock advances 40 minutes
    When the sweeper releases the expired holds
    Then the promise is CONFIRMED
    And the last command published no events
    And the book "N-AUS3:dep-AUS3-1800" holds 0 provisional and 1 confirmed

  # ---- ConfirmPromise: one order, several groups --------------------------

  Scenario: An order placed with two groups confirms both promises and moves both holds
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And the NEXT_DAY offer is remembered as "first"
    And I quote 3 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And the NEXT_DAY offer is remembered as "second"
    And a promise exists from the remembered offer "first"
    And the promise is remembered as "p1"
    And a promise exists from the remembered offer "second"
    And the promise is remembered as "p2"
    When the order "ord-7" is placed with the promise "p1" as group 1 and the promise "p2" as group 2
    Then the remembered promise "p1" is CONFIRMED
    And the remembered promise "p2" is CONFIRMED
    And the last command published the event "PromiseConfirmed" 2 times
    And the book "N-AUS3:dep-AUS3-1800" holds 0 provisional and 2 confirmed

  Scenario: A group whose hold lapsed is logged, not retried, and the sweeper releases it
    # the first hold lapses at 14:20, the second lives until 14:30: the placement arrives at 14:26 (hotspot H6)
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And the NEXT_DAY offer is remembered as "first"
    And a promise exists from the remembered offer "first"
    And the promise is remembered as "p1"
    And the clock advances 10 minutes
    And I quote 3 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And the NEXT_DAY offer is remembered as "second"
    And a promise exists from the remembered offer "second"
    And the promise is remembered as "p2"
    And the clock advances 11 minutes
    When the order "ord-7" is placed with the promise "p1" as group 1 and the promise "p2" as group 2
    Then the command succeeds
    And the remembered promise "p1" is PROVISIONAL
    When the sweeper releases the expired holds
    Then the remembered promise "p1" is RELEASED
    And the last command published the event "PromiseReleased" 1 time

  # ---- CancelPromise -------------------------------------------------------

  Scenario: Cancelling one group of an order cancels only that promise and says why
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And the NEXT_DAY offer is remembered as "first"
    And I quote 3 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And the NEXT_DAY offer is remembered as "second"
    And a promise exists from the remembered offer "first"
    And the promise is remembered as "p1"
    And a promise exists from the remembered offer "second"
    And the promise is remembered as "p2"
    And the order "ord-7" is placed with the promise "p1" as group 1 and the promise "p2" as group 2
    When the order "ord-7" is cancelled for the groups "2" with the reason "CUSTOMER_CHANGED_MIND"
    Then the remembered promise "p1" is CONFIRMED
    And the remembered promise "p2" is CANCELLED
    And the last command published the event "PromiseCancelled" 1 time
    And the last command published a "PromiseCancelled" event whose "Reason" is "CUSTOMER_CHANGED_MIND"
    And the book "N-AUS3:dep-AUS3-1800" holds 0 provisional and 1 confirmed
    When the promise "p2" becomes the scenario's promise
    Then the promise was cancelled with the reason "CUSTOMER_CHANGED_MIND"

  Scenario: An at-risk promise that could not be re-promised is cancelled and returns its capacity
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    And the node "N-AUS3" is suspended
    And the departure "dep-AUS3-1800" is cancelled
    And the promise is AT_RISK
    When the order "ord-1" is cancelled
    Then the promise is CANCELLED
    And the last command published the event "PromiseCancelled" 1 time
    And the book "N-AUS3:dep-AUS3-1800" holds 0 provisional and 0 confirmed

  Scenario: A cancelled order leaves a promise that was already kept as it is
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    And the assignment "asg-1" of the order "ord-1" is locked and shipped with the packages "TRK-1"
    And the package "TRK-1" is delivered at "2026-10-23T12:00:00-05:00"
    When the order "ord-1" is cancelled
    Then the promise is KEPT
    And the last command published no events

  # ---- FlagPromiseAtRisk: who is and is not flagged ------------------------

  Scenario: A cancelled departure does not touch a promise whose order was never placed
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    When the departure "dep-SAU1-DAU1-2200" is cancelled
    Then the promise is PROVISIONAL
    And the last command published no events

  Scenario: A cancelled departure is ignored by a promise that was already judged
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    And the assignment "asg-1" of the order "ord-1" is locked and shipped with the packages "TRK-1"
    And the package "TRK-1" is delivered at "2026-10-23T12:00:00-05:00"
    When the departure "dep-SAU1-DAU1-2200" is cancelled
    Then the promise is KEPT
    And the last command published no events

  Scenario: A promise already at risk is not flagged a second time by another fact
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    And the node "N-AUS3" is suspended
    And the departure "dep-AUS3-1800" is cancelled
    And the last command published the event "PromiseAtRisk" 1 time
    When the departure "dep-SAU1-DAU1-2200" is cancelled
    Then the promise is AT_RISK
    And the last command published the event "PromiseAtRisk" 0 times

  Scenario: A redelivered departure cancellation re-promises once
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    And an earlier pickup "dep-AUS3-1730" is scheduled
    When the departure "dep-AUS3-1800" cancellation is delivered twice with the same CloudEvents id
    Then the second delivery changed nothing
    And the promise has 1 revision with the reason "DEPARTURE_CANCELLED"
    And the promise is CONFIRMED

  Scenario: A cancelled delivery wave puts the promise at risk and moves it to the next wave
    Given the next day's delivery wave "W-DAU1-1024-B" is scheduled
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    When the delivery wave "W-DAU1-1023-A" is cancelled
    Then the last command published the event "PromiseAtRisk" 1 time
    And the last command published a "PromiseAtRisk" event whose "Reason" is "WAVE_CANCELLED"
    And the last command published the event "PromiseRevised" 1 time
    And the promise is CONFIRMED
    And the promise has 1 revision with the reason "WAVE_CANCELLED"

  # ---- FlagPredictedLate (ADR 0021) ----------------------------------------

  Scenario: A load not scanned in more than 15 minutes after its scheduled arrival flags the promise
    # the pickup was due at the sort centre at 19:15: the grace ends at 19:30
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    And the clock is set to "2026-10-22T19:30:00-05:00"
    When the sweeper flags the promises predicted late
    Then the sweep flagged 0 promises
    And the promise is CONFIRMED
    Given the clock is set to "2026-10-22T19:31:00-05:00"
    When the sweeper flags the promises predicted late
    Then the sweep flagged 1 promise
    And the promise is at risk with the reason "PREDICTED_LATE"
    And the last command published the event "PromiseAtRisk" 1 time
    And the last command published a "PromiseAtRisk" event whose "Reason" is "PREDICTED_LATE"

  Scenario: A load that was scanned in does not flag the promise
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    And the load of the departure "dep-AUS3-1800" arrived at "2026-10-22T19:20:00-05:00"
    And the clock is set to "2026-10-22T19:31:00-05:00"
    When the sweeper flags the promises predicted late
    Then the sweep flagged 0 promises
    And the promise is CONFIRMED

  Scenario: A promise flagged predicted late whose window is still reachable keeps it and is flagged once
    # 16 minutes late at the first leg, the sort dwell and the last-mile allowance still fit before 21:00 on Friday
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    And the clock is set to "2026-10-22T19:31:00-05:00"
    And the sweeper flags the promises predicted late
    When the sweeper re-promises the at-risk promises
    Then the promise is AT_RISK
    And the last command published no events
    And the promise has 0 revisions with the reason "PREDICTED_LATE"
    When the sweeper flags the promises predicted late
    Then the sweep flagged 0 promises
    Given the load of the departure "dep-AUS3-1800" arrived at "2026-10-22T20:00:00-05:00"
    And the clock advances 5 minutes
    When the sweeper flags the promises predicted late
    Then the promise is AT_RISK

  # ---- JudgePromise --------------------------------------------------------

  Scenario: A delivery exactly at the end of the window keeps the promise
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    And the assignment "asg-1" of the order "ord-1" is locked and shipped with the packages "TRK-1"
    When the package "TRK-1" is delivered at "2026-10-23T21:00:00-05:00"
    Then the promise is KEPT
    And the last command published the event "PromiseKept" 1 time
    And the last command published a "PromiseKept" event whose "OrderID" is "ord-1"

  Scenario: A delivery one minute after the window breaks the promise
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    And the assignment "asg-1" of the order "ord-1" is locked and shipped with the packages "TRK-1"
    When the package "TRK-1" is delivered at "2026-10-23T21:01:00-05:00"
    Then the promise is BROKEN
    And the promise was late by 1 minutes
    And the judgement cause is "LATE"

  Scenario: A delivery redelivered with the same CloudEvents id judges once
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1
    And the assignment "asg-1" of the order "ord-1" is locked and shipped with the packages "TRK-1"
    When the delivery of the package "TRK-1" at "2026-10-23T12:00:00-05:00" is delivered twice with the same CloudEvents id
    Then the second delivery changed nothing
    And the promise is KEPT

  # ---- SharedTrackingJudgement (ADR 0025) ----------------------------------

  Scenario: One merged package is judged against the window of each promise that rides it
    # the first promise rides wave A (window ends Friday 21:00), the second rides wave B (ends Saturday 21:00)
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And the promise is remembered as "fast"
    And I confirm the promise for the order "ord-1" group 1
    And the assignment "asg-1" of the order "ord-1" is locked and shipped with the packages "TRK-9"
    And the delivery wave "W-DAU1-1023-A" has 0 stops left
    And the next day's delivery wave "W-DAU1-1024-B" is scheduled
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier TWO_DAY
    And a promise exists from the TWO_DAY offer
    And the promise is remembered as "slow"
    And I confirm the promise for the order "ord-2" group 1
    And the assignment "asg-2" of the order "ord-2" is locked and shipped with the packages "TRK-9"
    When the package "TRK-9" is delivered at "2026-10-24T12:00:00-05:00"
    Then the remembered promise "fast" is BROKEN
    And the remembered promise "slow" is KEPT
    And the last command published the event "PromiseBroken" 1 time
    And the last command published the event "PromiseKept" 1 time

  Scenario: A merged package lost breaks every promise that rides it
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And the promise is remembered as "fast"
    And I confirm the promise for the order "ord-1" group 1
    And the assignment "asg-1" of the order "ord-1" is locked and shipped with the packages "TRK-9"
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And the promise is remembered as "second"
    And I confirm the promise for the order "ord-2" group 1
    And the assignment "asg-2" of the order "ord-2" is locked and shipped with the packages "TRK-9"
    When the package "TRK-9" is lost at "2026-10-23T10:00:00-05:00"
    Then the remembered promise "fast" is BROKEN
    And the remembered promise "second" is BROKEN
    And the last command published the event "PromiseBroken" 2 times

  # ---- Read models: version guards and node state of the projections --------

  Scenario: A service-area assignment is applied only with a higher version
    Given the delivery station "N-DAU2" is registered
    And the postal prefix "787" is assigned to the station "N-DAU2" with the assignment version 1
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then the quote offers exactly the tiers "NEXT_DAY,TWO_DAY,STANDARD"
    Given the postal prefix "787" is assigned to the station "N-DAU2" with the assignment version 2
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then every tier is unavailable with "DELIVERY_SLA_UNAVAILABLE"

  Scenario: A retired node leaves the candidate set
    Given the node "N-AUS3" is retired
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then every tier is unavailable with "ITEM_QUANTITY_NOT_AVAILABLE"
