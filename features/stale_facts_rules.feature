Feature: Facts that arrive out of order never overwrite a newer one
  warehouse-docs (delivery-promise domain-events): DepartureCapacityChanged,
  DeliveryWaveCapacityChanged, NodeActivated, NodeSuspended, ListingSuppressed
  and ListingRestored apply "newer time wins"; NodeRetired is terminal. An older
  fact delivered late (a retry, a replayed dead letter) must not bring back room
  that a newer fact has already taken away, nor a node or a listing that a newer
  fact has taken out of the candidate set. An equal instant is the later
  delivery of the same fact and applies (ADR 0022 sets that precedent for tier
  eligibility; ADR 0028 for capacity, ADR 0029 for node status and listings).

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

  Scenario: An activation older than the suspension delivered late does not bring the node back
    Given the node "N-AUS3" is suspended
    And the node "N-AUS3" is activated by an event that is 10 minutes older than the previous one
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then every tier is unavailable with "ITEM_QUANTITY_NOT_AVAILABLE"

  Scenario: A suspension older than the activation delivered late does not take the node out
    Given the node "N-AUS3" is activated
    And the node "N-AUS3" is suspended by an event that is 10 minutes older than the previous one
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is SCHEDULED from the node "N-AUS3" via the pickup "dep-AUS3-1800" and the wave "W-DAU1-1023-A"

  Scenario Outline: A node activation newer than or at the same instant as the suspension brings the node back
    Given the node "N-AUS3" is suspended
    And the node "N-AUS3" is activated by an event that is <minutes> minutes newer than the previous one
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is SCHEDULED from the node "N-AUS3" via the pickup "dep-AUS3-1800" and the wave "W-DAU1-1023-A"

    Examples:
      | minutes |
      | 10      |
      | 0       |

  Scenario Outline: A retired node is terminal: no later activation revives it
    Given the node "N-AUS3" is retired
    And the node "N-AUS3" is activated by an event that is <minutes> minutes <direction> than the previous one
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then every tier is unavailable with "ITEM_QUANTITY_NOT_AVAILABLE"

    Examples:
      | minutes | direction |
      | 10      | newer     |
      | 0       | newer     |
      | 10      | older     |

  Scenario: A retirement delivered late still retires the node
    Given the node "N-AUS3" is activated
    And the node "N-AUS3" is retired by an event that is 10 minutes older than the previous one
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then every tier is unavailable with "ITEM_QUANTITY_NOT_AVAILABLE"

  Scenario: A restore older than the suppression delivered late does not bring the listing back
    Given the listing "lst-1" is suppressed
    And the listing "lst-1" is restored by an event that is 10 minutes older than the previous one
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then every tier is unavailable with "ITEM_QUANTITY_NOT_AVAILABLE"

  Scenario: A suppression older than the restore delivered late does not take the listing out
    Given the listing "lst-1" is restored
    And the listing "lst-1" is suppressed by an event that is 10 minutes older than the previous one
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is QUOTED

  Scenario Outline: A restore newer than or at the same instant as the suppression brings the listing back
    Given the listing "lst-1" is suppressed
    And the listing "lst-1" is restored by an event that is <minutes> minutes newer than the previous one
    When I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    Then the NEXT_DAY offer is QUOTED

    Examples:
      | minutes |
      | 10      |
      | 0       |
