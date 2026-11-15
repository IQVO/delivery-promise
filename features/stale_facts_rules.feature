Feature: Capacity facts that arrive out of order never reopen a full pickup or wave
  warehouse-docs (delivery-promise domain-events): DepartureCapacityChanged and
  DeliveryWaveCapacityChanged apply "newer time wins"; an older figure delivered
  late (a retry, a replayed dead letter) must not bring back room that a newer
  fact has already taken away. An equal instant is the later delivery of the same
  fact and applies (ADR 0022 sets that precedent for tier eligibility).

  Background:
    Given the worked-example network is projected

  Scenario Outline: An older capacity figure delivered late does not reopen what a newer fact filled
    Given the network changes: <full>
    And <late figure> that is 10 minutes older than the full one
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the tier NEXT_DAY is unavailable with "NODE_CAPACITY_EXHAUSTED"

    Examples:
      | full                         | late figure                                       |
      | the pickup departure is full | the pickup departure reports 40 packages left     |
      | the delivery wave is full    | the delivery wave reports 40 stops left           |

  Scenario Outline: A newer capacity figure reopens what an older fact filled
    Given the network changes: <full>
    And <newer figure> that is 10 minutes newer than the full one
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is SCHEDULED from the node "N-AUS3" via the pickup "dep-AUS3-1800" and the wave "W-DAU1-1023-A"

    Examples:
      | full                         | newer figure                                      |
      | the pickup departure is full | the pickup departure reports 40 packages left     |
      | the delivery wave is full    | the delivery wave reports 40 stops left           |

  Scenario Outline: A capacity figure with the same instant applies
    Given the network changes: <full>
    And <same figure> that is 0 minutes newer than the full one
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is SCHEDULED from the node "N-AUS3" via the pickup "dep-AUS3-1800" and the wave "W-DAU1-1023-A"

    Examples:
      | full                         | same figure                                       |
      | the pickup departure is full | the pickup departure reports 40 packages left     |
      | the delivery wave is full    | the delivery wave reports 40 stops left           |
