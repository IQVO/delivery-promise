Feature: Keeping the promise
  A confirmed promise is re-promised when the network breaks its witness path,
  becomes unkeepable when no path remains, and is judged KEPT or BROKEN from the
  facts of shipping-and-tracking: the group is judged on its LAST package
  delivered, or on the first one lost or returned.

  Background:
    Given the worked-example network is projected
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-1" group 1

  Scenario: A cancelled pickup revises the promise onto another departure
    Given an earlier pickup "dep-AUS3-1730" is scheduled
    When the departure "dep-AUS3-1800" is cancelled
    Then the promise is CONFIRMED
    And the promise has 1 revision with the reason "DEPARTURE_CANCELLED"
    And the promise ships from the pickup "dep-AUS3-1730"
    And the book "N-AUS3:dep-AUS3-1800" holds 0 provisional and 0 confirmed
    And the book "N-AUS3:dep-AUS3-1730" holds 0 provisional and 1 confirmed
    And the last command published the event "PromiseAtRisk" 1 time
    And the last command published the event "PromiseRevised" 1 time

  Scenario: A promise that cannot be revised is unkeepable once and repaired later
    Given the node "N-AUS3" is suspended
    When the departure "dep-AUS3-1800" is cancelled
    Then the promise is AT_RISK
    And the last command published the event "PromiseUnkeepable" 1 time
    When the sweeper re-promises the at-risk promises
    Then the last command published no events
    Given the node "N-AUS3" is activated
    And an earlier pickup "dep-AUS3-1730" is scheduled
    When the sweeper re-promises the at-risk promises
    Then the promise is CONFIRMED

  Scenario: The last package delivered inside the window keeps the promise
    Given the assignment "asg-1" of the order "ord-1" is locked and shipped with the packages "TRK-1,TRK-2"
    When the package "TRK-1" is delivered at "2026-10-23T12:00:00-05:00"
    Then the promise is CONFIRMED
    When the package "TRK-2" is delivered at "2026-10-23T15:00:00-05:00"
    Then the promise is KEPT
    And the last command published the event "PromiseKept" 1 time

  Scenario: A delivery after the window breaks the promise as late
    Given the assignment "asg-1" of the order "ord-1" is locked and shipped with the packages "TRK-1"
    When the package "TRK-1" is delivered at "2026-10-23T22:00:00-05:00"
    Then the promise is BROKEN
    And the judgement cause is "LATE"
    And the promise was late by 60 minutes
    And the last command published the event "PromiseBroken" 1 time

  Scenario: A lost package breaks the promise at once
    Given the assignment "asg-1" of the order "ord-1" is locked and shipped with the packages "TRK-1,TRK-2"
    When the package "TRK-2" is lost
    Then the promise is BROKEN
    And the judgement cause is "LOST"
    When the package "TRK-1" is delivered at "2026-10-23T11:00:00-05:00"
    Then the last command published no events

  Scenario: A package returned before delivery breaks the promise
    Given the assignment "asg-1" of the order "ord-1" is locked and shipped with the packages "TRK-1,TRK-2"
    When the package "TRK-2" is returned
    Then the promise is BROKEN
    And the judgement cause is "RETURNED"
