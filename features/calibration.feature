Feature: Calibrated promise confidence
  The service judges every promise it made. The lateness of the promises it
  judged, per tier, basis and node grade, becomes a buffer on the next quote of
  that segment: once the segment has the minimum observations the offer lands in
  a later fixed delivery window when its history says the earlier one is missed
  too often, and the offer says how sure it is. Below the minimum the quote is
  exactly what it was before calibration, and says nothing about its confidence.

  Background:
    Given the worked-example network is projected
    And the next day's delivery wave "W-DAU1-1024-B" is scheduled
    And the policy needs 10 judged promises before a segment is buffered

  Scenario: Nine late judgements are one short of the minimum
    Given 9 TWO_DAY promises are judged late by 400 minutes
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier TWO_DAY
    Then the TWO_DAY offer promises delivery between "2026-10-23T09:00:00-05:00" and "2026-10-23T21:00:00-05:00"
    And the TWO_DAY offer carries no confidence

  Scenario: A run of late judgements raises the buffer and moves the offer to the next window
    Given 10 TWO_DAY promises are judged late by 400 minutes
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier TWO_DAY
    Then the TWO_DAY offer promises delivery between "2026-10-24T09:00:00-05:00" and "2026-10-24T21:00:00-05:00"
    And the TWO_DAY offer carries the confidence 1
    And the last command published a DeliveryOfferQuoted event with the confidence 1

  Scenario: A run of promises kept leaves the window where it was and says how sure it is
    Given 10 TWO_DAY promises are judged on time
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier TWO_DAY
    Then the TWO_DAY offer promises delivery between "2026-10-23T09:00:00-05:00" and "2026-10-23T21:00:00-05:00"
    And the TWO_DAY offer carries the confidence 1

  Scenario: Another tier keeps its own history
    Given 10 TWO_DAY promises are judged late by 400 minutes
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer promises delivery between "2026-10-23T09:00:00-05:00" and "2026-10-23T21:00:00-05:00"
    And the NEXT_DAY offer carries no confidence

  Scenario: The promise made from a calibrated offer carries its confidence
    Given 10 TWO_DAY promises are judged late by 400 minutes
    And I quote 2 of "ci-1001" for postal prefix "787" on the tier TWO_DAY
    When I make a promise from the TWO_DAY offer
    Then the command succeeds
    And the promise carries the confidence 1
