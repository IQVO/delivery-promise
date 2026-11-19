Feature: Promise coverage per region in the quote-conversion report
  ADR 0030 (KPI baseline v2, F5): per UTC day and region, how many of the
  offers quoted carry a promise of 1, 2, 3 or 5 days or less. The region is
  the first two characters of the destination postal prefix (never more of
  the destination), the ten regions with most requests are listed and the
  rest fold into "other". The unit is the offer quoted (DeliveryOfferQuoted),
  the only own fact with both a region and a promised window. The report's
  existing day rows are unchanged.

  Scenario: Cumulative counts and shares per region
    Given these offers were quoted:
      | day        | prefix | days | count |
      | 2026-11-10 | 787    | 1    | 2     |
      | 2026-11-10 | 78701  | 3    | 1     |
      | 2026-11-10 | 78     | 5    | 1     |
      | 2026-11-10 | 100    | 2    | 1     |
      | 2026-11-10 | 100    | -    | 1     |
    When I read the quote-conversion report from "2026-11-10" to "2026-11-10"
    Then the promise coverage of "2026-11-10" lists the regions "78, 10"
    And region "78" of "2026-11-10" has 4 requests and 4 answered with a promise
    And region "78" of "2026-11-10" is within 1, 2, 3 and 5 days for 2, 2, 3 and 4 offers
    And the share of region "78" of "2026-11-10" within 3 days is 0.75
    And region "10" of "2026-11-10" has 2 requests and 1 answered with a promise
    And region "10" of "2026-11-10" is within 1, 2, 3 and 5 days for 0, 1, 1 and 1 offers
    And the share of region "10" of "2026-11-10" within 2 days is 0.5

  Scenario: Only the first two characters of the destination are reported
    Given these offers were quoted:
      | day        | prefix | days | count |
      | 2026-11-10 | 78701  | 1    | 1     |
      | 2026-11-10 | 7      | 1    | 1     |
    When I read the quote-conversion report from "2026-11-10" to "2026-11-10"
    Then the promise coverage of "2026-11-10" lists the regions "7, 78"
    And no region of the report is longer than 2 characters

  Scenario: Regions past the tenth fold into other
    Given 12 regions were quoted on "2026-11-10" with 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2 and 1 offers within 1 day
    When I read the quote-conversion report from "2026-11-10" to "2026-11-10"
    Then the promise coverage of "2026-11-10" lists 11 regions ending with "other"
    And region "other" of "2026-11-10" has 3 requests and 3 answered with a promise

  Scenario: Ten regions or fewer have no other row
    Given 10 regions were quoted on "2026-11-10" with 10, 9, 8, 7, 6, 5, 4, 3, 2 and 1 offers within 1 day
    When I read the quote-conversion report from "2026-11-10" to "2026-11-10"
    Then the promise coverage of "2026-11-10" lists 10 regions ending with "09"

  Scenario: A day without a quoted offer is empty
    Given these offers were quoted:
      | day        | prefix | days | count |
      | 2026-11-10 | 787    | 1    | 1     |
    When I read the quote-conversion report from "2026-11-11" to "2026-11-12"
    Then the promise coverage is an empty list
    And the day rows are an empty list

  Scenario: An empty projection reports an empty coverage, not null
    When I read the quote-conversion report from "2026-11-10" to "2026-11-10"
    Then the promise coverage is an empty list

  Scenario: Each day is ranked on its own
    Given these offers were quoted:
      | day        | prefix | days | count |
      | 2026-11-10 | 787    | 1    | 1     |
      | 2026-11-11 | 100    | 1    | 2     |
      | 2026-11-11 | 787    | 1    | 1     |
    When I read the quote-conversion report from "2026-11-10" to "2026-11-11"
    Then the promise coverage of "2026-11-10" lists the regions "78"
    And the promise coverage of "2026-11-11" lists the regions "10, 78"

  Scenario: The existing day rows are unchanged
    Given these offers were quoted:
      | day        | prefix | days | count |
      | 2026-11-10 | 787    | 1    | 3     |
    When I read the quote-conversion report from "2026-11-10" to "2026-11-10"
    Then the day rows list the tier "NEXT_DAY" on "2026-11-10" with 3 offers quoted and 0 promises made

  # --- Request-level fields (ADR 0032) -----------------------------------
  # Next to the offers-based fields above, each region row carries the
  # REQUEST-based ones: the quote and estimate requests answered, with or
  # without a promise (DeliveryRequestAnswered, best effort, only from the day
  # the flag was switched on). Nothing above is renamed or removed.

  Scenario: Request share and cumulative counts over the requests answered
    Given these requests were answered:
      | day        | prefix | kind     | days | count |
      | 2026-11-10 | 787    | QUOTE    | 1    | 2     |
      | 2026-11-10 | 78701  | ESTIMATE | 3    | 1     |
      | 2026-11-10 | 78     | ESTIMATE | 5    | 1     |
      | 2026-11-10 | 787    | QUOTE    | -    | 4     |
    When I read the quote-conversion report from "2026-11-10" to "2026-11-10"
    Then region "78" of "2026-11-10" answered 8 requests, 4 with a promise
    And the requests of region "78" of "2026-11-10" are within 1, 2, 3 and 5 days for 2, 2, 3 and 4 requests
    And the request share of region "78" of "2026-11-10" is 0.5
    And the request share of region "78" of "2026-11-10" within 3 days is 0.375

  Scenario: A request answered with no promise is counted and lowers the share
    Given these requests were answered:
      | day        | prefix | kind  | days | count |
      | 2026-11-10 | 100    | QUOTE | -    | 1     |
    When I read the quote-conversion report from "2026-11-10" to "2026-11-10"
    Then the promise coverage of "2026-11-10" lists the regions "10"
    And region "10" of "2026-11-10" answered 1 requests, 0 with a promise
    And the request share of region "10" of "2026-11-10" is 0
    And region "10" of "2026-11-10" has 0 requests and 0 answered with a promise

  Scenario: Offers-based and request-based fields sit side by side on the same region
    Given these offers were quoted:
      | day        | prefix | days | count |
      | 2026-11-10 | 787    | 1    | 2     |
    And these requests were answered:
      | day        | prefix | kind  | days | count |
      | 2026-11-10 | 787    | QUOTE | 1    | 1     |
      | 2026-11-10 | 787    | QUOTE | -    | 3     |
    When I read the quote-conversion report from "2026-11-10" to "2026-11-10"
    Then region "78" of "2026-11-10" has 2 requests and 2 answered with a promise
    And region "78" of "2026-11-10" answered 4 requests, 1 with a promise
    And the request share of region "78" of "2026-11-10" is 0.25

  Scenario: A day with offers and no request facts has a null request share
    Given these offers were quoted:
      | day        | prefix | days | count |
      | 2026-11-10 | 787    | 1    | 2     |
    When I read the quote-conversion report from "2026-11-10" to "2026-11-10"
    Then region "78" of "2026-11-10" answered 0 requests, 0 with a promise
    And the request share of region "78" of "2026-11-10" is null
    And the requests of region "78" of "2026-11-10" are within 1, 2, 3 and 5 days for 0, 0, 0 and 0 requests
    And the request share of region "78" of "2026-11-10" within 1 days is null

  Scenario: A day without a request fact or an offer is empty
    Given these requests were answered:
      | day        | prefix | kind  | days | count |
      | 2026-11-10 | 787    | QUOTE | 1    | 1     |
    When I read the quote-conversion report from "2026-11-11" to "2026-11-12"
    Then the promise coverage is an empty list

  Scenario: Request facts rank the regions of a day and the tail folds into other
    Given 12 regions were answered on "2026-11-10" with 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2 and 1 requests within 1 day
    When I read the quote-conversion report from "2026-11-10" to "2026-11-10"
    Then the promise coverage of "2026-11-10" lists 11 regions ending with "other"
    And region "other" of "2026-11-10" answered 3 requests, 3 with a promise
