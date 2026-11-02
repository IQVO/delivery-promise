Feature: The REST adapter answers the contract of apis/openapi.yaml
  Every operation of the OpenAPI document, served by the real chi router over
  the same in-memory doubles the use-case scenarios use: the happy answer, the
  main validation error and the documented problem slug of each route. The
  transactional Idempotency-Key middleware is Postgres-bound and proven by the
  integration suite; here the replay is the offer-level one of MakePromise
  (201 made, 200 replayed).

  Background:
    Given the worked-example network is projected

  Scenario: Quoting over REST answers the offers per tier
    When a POST request with the JSON body is sent to "/delivery-quotes":
      """json
      {"destinationPostalPrefix":"787","lines":[{"catalogItemId":"ci-1001","quantity":2}]}
      """
    Then the response status is 200
    And the response list "offers" has 3 items
    And the response field "offers.0.tier" is "NEXT_DAY"
    And the response field "offers.1.tier" is "TWO_DAY"
    And the response field "offers.2.basis" is "SCHEDULED"
    And the response field "unavailable.0.tier" is "SAME_DAY"
    And the response field "unavailable.0.constraint" is "DELIVERY_SLA_UNAVAILABLE"
    And the last command published the event "DeliveryOfferQuoted" 3 times

  Scenario Outline: An invalid quote request answers its documented problem
    When a POST request with the JSON body is sent to "/delivery-quotes":
      """json
      <body>
      """
    Then the response status is <status>
    And the response problem is "<slug>"

    Examples:
      | body                                                                                                        | status | slug               |
      | {"destinationPostalPrefix":"787","lines":[{"catalogItemId":"ci-1001","quantity":"two"}]}                     | 400    | malformed-request  |
      | {"destinationPostalPrefix":"","lines":[{"catalogItemId":"ci-1001","quantity":2}]}                             | 422    | validation-failed  |
      | {"destinationPostalPrefix":"787","lines":[{"catalogItemId":"ci-1001","quantity":0}]}                          | 422    | validation-failed  |

  Scenario: Making a promise over REST answers 201 and the replay answers 200
    Given I quote 2 of "ci-1001" for postal prefix "787"
    When a POST request with the JSON body is sent to "/promises":
      """json
      {"offerId":"{offerId:NEXT_DAY}","lines":[{"catalogItemId":"ci-1001","quantity":2}]}
      """
    Then the response status is 201
    And the response field "state" is "PROVISIONAL"
    And the response field "holdExpiresAt" is "2026-10-22T19:20:00Z"
    And the promise of the response becomes the scenario's promise
    When a POST request with the JSON body is sent to "/promises":
      """json
      {"offerId":"{offerId:NEXT_DAY}","lines":[{"catalogItemId":"ci-1001","quantity":2}]}
      """
    Then the response status is 200
    And the response field "promiseId" is "{promiseId}"
    And the response field "state" is "PROVISIONAL"

  Scenario: Making a promise with lines the offer never quoted is refused
    Given I quote 2 of "ci-1001" for postal prefix "787"
    When a POST request with the JSON body is sent to "/promises":
      """json
      {"offerId":"{offerId:NEXT_DAY}","lines":[{"catalogItemId":"ci-1001","quantity":5}]}
      """
    Then the response status is 422
    And the response problem is "validation-failed"

  Scenario: An unknown offer or promise answers not-found
    When a GET request is sent to "/delivery-offers/off-none"
    Then the response status is 404
    And the response problem is "not-found"
    When a GET request is sent to "/promises/prm-none"
    Then the response status is 404
    And the response problem is "not-found"

  Scenario: Reading a promise over REST
    Given I quote 2 of "ci-1001" for postal prefix "787"
    And a promise exists from the NEXT_DAY offer
    When a GET request is sent to "/promises/{promiseId}"
    Then the response status is 200
    And the response field "promiseId" is "{promiseId}"
    And the response field "state" is "PROVISIONAL"
    And the response field "terms.tier" is "NEXT_DAY"
    And the response field "witnessPath.pickupDepartureId" is "dep-AUS3-1800"

  Scenario: Confirming a lapsed hold over REST answers promise-hold-expired
    Given I quote 2 of "ci-1001" for postal prefix "787"
    And a promise exists from the NEXT_DAY offer
    And the clock advances 15 minutes
    When a POST request with the JSON body is sent to "/promises/{promiseId}/confirmations":
      """json
      {"orderId":"ord-1","groupNo":1}
      """
    Then the response status is 409
    And the response problem is "promise-hold-expired"

  Scenario: Cancelling a provisional promise over REST answers illegal-transition
    Given I quote 2 of "ci-1001" for postal prefix "787"
    And a promise exists from the NEXT_DAY offer
    When a POST request is sent to "/promises/{promiseId}/cancellations"
    Then the response status is 409
    And the response problem is "illegal-transition"

  Scenario: Listing the promises of an order over REST, one per group
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-9" group 1
    Given I quote 2 of "ci-1001" for postal prefix "787" on the tier NEXT_DAY
    And a promise exists from the NEXT_DAY offer
    And I confirm the promise for the order "ord-9" group 2
    When a GET request is sent to "/promises?orderId=ord-9"
    Then the response status is 200
    And the response list "items" has 2 items
    And the response field "items.0.groupNo" is 1
    And the response field "items.1.groupNo" is 2
    And the response field "items.0.orderId" is "ord-9"
    When a GET request is sent to "/promises?orderId=ord-9&limit=1"
    Then the response status is 200
    And the response list "items" has 1 item
    And the response field "items.0.groupNo" is 1
    When a GET request is sent to "/promises?orderId=ord-9&after=x"
    Then the response status is 400
    And the response problem is "unknown-query-parameter"
    When a GET request is sent to "/promises?orderId=ord-9&limit=0"
    Then the response status is 400
    And the response problem is "invalid-limit"
    When a GET request is sent to "/promises?orderId=ord-9&cursor=%21%21"
    Then the response status is 400
    And the response problem is "invalid-cursor"
    When a GET request is sent to "/promises?orderId="
    Then the response status is 422
    And the response problem is "validation-failed"

  Scenario: Listing the books of a node over REST
    Given I quote 2 of "ci-1001" for postal prefix "787"
    And a promise exists from the NEXT_DAY offer
    When a GET request is sent to "/promise-books?nodeId=N-AUS3&date=2026-10-22"
    Then the response status is 200
    And the response list "items" has 1 item
    And the response field "items.0.bookId" is "N-AUS3:dep-AUS3-1800"
    And the response field "items.0.state" is "OPEN"
    And the response field "items.0.cutoffAt" is "2026-10-22T23:00:00Z"
    And the response field "items.0.capacity.share" is 2500
    And the response field "items.0.capacity.provisional" is 1
    And the response field "items.0.capacity.confirmed" is 0
    When a GET request is sent to "/promise-books?nodeId=N-AUS3&date=22-10-2026"
    Then the response status is 422
    And the response problem is "validation-failed"

  Scenario: Publishing a policy over REST and reading the current one
    When a GET request is sent to "/promise-policies/current"
    Then the response status is 200
    And the response field "policyVersion" is "v0"
    And the response field "version" is 0
    When a POST request with the JSON body is sent to "/promise-policies":
      """json
      {"offerTtlMinutes":12,"holdTtlMinutes":20,"packagesPerOrder":1,"capacitySharePercent":80,"orderDeadlines":[{"tier":"NEXT_DAY","regionId":"R-SC","localTime":"13:00"}]}
      """
    Then the response status is 201
    And the response field "policyVersion" is "v1"
    And the response field "capacitySharePercent" is 80
    And the response field "orderDeadlines.0.localTime" is "13:00"
    And the last command published the event "PromisePolicyPublished" 1 time
    And the last command published a "PromisePolicyPublished" event whose "PolicyVersion" is "v1"
    When a GET request is sent to "/promise-policies/current"
    Then the response field "policyVersion" is "v1"
    When a POST request with the JSON body is sent to "/promise-policies":
      """json
      {"orderDeadlines":[{"tier":"NEXT_DAY","regionId":"R-SC","localTime":"9:05"}]}
      """
    Then the response status is 422
    And the response problem is "validation-failed"
