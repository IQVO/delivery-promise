Feature: Request-level promise coverage facts (ADR 0032)
  One analytics-only DeliveryRequestAnswered per quote or estimate answered
  with 200, with or without a promise, handed over AFTER the response to a
  bounded in-memory queue and written by a single worker as one outbox row in
  its own transaction. It is best effort by design and OFF unless
  DELIVERY_REQUEST_FACTS_ENABLED is true: a lost fact is counted and never
  changes a response. The region is the first two characters of the postal
  prefix and nothing more of the destination.

  Background:
    Given the worked-example network is projected

  Scenario: With the flag off a quote publishes no request fact
    When a POST request with the JSON body is sent to "/delivery-quotes":
      """json
      {"destinationPostalPrefix":"787","lines":[{"catalogItemId":"ci-1001","quantity":2}]}
      """
    Then the response status is 200
    And the last command published the event "DeliveryOfferQuoted" 3 times
    And the last command published the event "DeliveryRequestAnswered" 0 times

  Scenario: A quote answered with offers leaves one fact with its two-character region
    Given the request facts are enabled with a queue of 10
    When a POST request with the JSON body is sent to "/delivery-quotes":
      """json
      {"destinationPostalPrefix":"787","lines":[{"catalogItemId":"ci-1001","quantity":2}]}
      """
    Then the response status is 200
    And the response list "offers" has 3 items
    When the request facts worker drains the queue
    Then the outbox holds 1 DeliveryRequestAnswered row
    And the last request fact is a QUOTE for region "78" answered with a promise
    And 1 request fact was written

  Scenario: A quote answered with no offer is still a fact, answered with no promise
    Given the request facts are enabled with a queue of 10
    When a POST request with the JSON body is sent to "/delivery-quotes":
      """json
      {"destinationPostalPrefix":"999","lines":[{"catalogItemId":"ci-1001","quantity":2}]}
      """
    Then the response status is 200
    And the response list "offers" has 0 items
    When the request facts worker drains the queue
    Then the outbox holds 1 DeliveryRequestAnswered row
    And the last request fact is a QUOTE for region "99" answered with no promise

  Scenario: An estimate is one fact over all its items
    Given the request facts are enabled with a queue of 10
    When a POST request with the JSON body is sent to "/delivery-estimates":
      """json
      {"destinationPostalPrefix":"787","items":[{"catalogItemId":"ci-1001","quantity":2},{"catalogItemId":"ci-404","quantity":1}],"tiers":["NEXT_DAY","TWO_DAY","STANDARD"]}
      """
    Then the response status is 200
    When the request facts worker drains the queue
    Then the outbox holds 1 DeliveryRequestAnswered row
    And the last request fact is a ESTIMATE for region "78" answered with a promise

  Scenario: A refused request leaves no fact
    Given the request facts are enabled with a queue of 10
    When a POST request with the JSON body is sent to "/delivery-quotes":
      """json
      {"destinationPostalPrefix":"","lines":[{"catalogItemId":"ci-1001","quantity":2}]}
      """
    Then the response status is 422
    When the request facts worker drains the queue
    Then the outbox holds 0 DeliveryRequestAnswered rows

  Scenario: A full queue drops the newest fact, counts it and answers the request all the same
    Given the request facts are enabled with a queue of 1
    When a POST request with the JSON body is sent to "/delivery-estimates":
      """json
      {"destinationPostalPrefix":"787","items":[{"catalogItemId":"ci-1001","quantity":2}],"tiers":["NEXT_DAY"]}
      """
    Then the response status is 200
    When a POST request with the JSON body is sent to "/delivery-estimates":
      """json
      {"destinationPostalPrefix":"787","items":[{"catalogItemId":"ci-1001","quantity":2}],"tiers":["NEXT_DAY"]}
      """
    Then the response status is 200
    And 1 request fact was dropped as "queue_full"
    When the request facts worker drains the queue
    Then the outbox holds 1 DeliveryRequestAnswered row
    And 1 request fact was written

  Scenario: A failing outbox never changes the response, the fact is dropped and counted
    Given the request facts are enabled with a queue of 10 over an outbox that fails
    When a POST request with the JSON body is sent to "/delivery-quotes":
      """json
      {"destinationPostalPrefix":"787","lines":[{"catalogItemId":"ci-1001","quantity":2}]}
      """
    Then the response status is 200
    And the response list "offers" has 3 items
    And the response field "offers.0.tier" is "NEXT_DAY"
    When the request facts worker drains the queue
    Then the outbox holds 0 DeliveryRequestAnswered rows
    And 1 request fact was dropped as "write_failed"
    And 0 request facts were written
