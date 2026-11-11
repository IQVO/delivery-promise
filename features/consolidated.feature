Feature: The opt-in CONSOLIDATED tier
  A shopper who does not need a parcel soon may choose a local delivery day. The
  quote then adds a CONSOLIDATED offer ending on that day, and says whether the
  day leaves the network at least one local day to merge the order with others.
  It never says that another order exists. The classic quote stays as it was.

  Background:
    Given the worked-example network is projected
    And delivery waves are scheduled on the days 24 to 27

  Scenario: A default quote never offers CONSOLIDATED
    When I quote 2 of "ci-1001" for postal prefix "787"
    Then the command succeeds
    And the quote offers exactly the tiers "NEXT_DAY,TWO_DAY,STANDARD"

  Scenario: A chosen day adds the CONSOLIDATED offer to the classic tiers
    When I quote 2 of "ci-1001" for postal prefix "787" for the delivery day "2026-10-26"
    Then the command succeeds
    And the quote offers exactly the tiers "NEXT_DAY,TWO_DAY,STANDARD,CONSOLIDATED"
    And the CONSOLIDATED offer promises delivery between "2026-10-26T09:00:00-05:00" and "2026-10-26T21:00:00-05:00"
    And the CONSOLIDATED offer is consolidation eligible

  Scenario: The fastest day leaves nothing to hold
    When I quote 2 of "ci-1001" for postal prefix "787" for the delivery day "2026-10-21"
    Then the CONSOLIDATED offer promises delivery between "2026-10-23T09:00:00-05:00" and "2026-10-23T21:00:00-05:00"
    And the CONSOLIDATED offer is not consolidation eligible

  Scenario: The promise carries the flag
    When I quote 2 of "ci-1001" for postal prefix "787" for the delivery day "2026-10-27"
    And I make a promise from the CONSOLIDATED offer
    Then the command succeeds
    And the promise is PROVISIONAL
    And the promise is CONSOLIDATED and consolidation eligible

  Scenario: An invalid day is refused
    When I quote 2 of "ci-1001" for postal prefix "787" for the delivery day "next friday"
    Then the command is refused with "ErrValidation"
