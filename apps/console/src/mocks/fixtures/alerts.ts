import type { components } from "../../generated/aep-api";

type RcaAgentReport = components["schemas"]["RcaAgentReport"];
type RcaAgentReportList = components["schemas"]["RcaAgentReportList"];
type ApiError = components["schemas"]["Error"];

// Scenario switch (api-guidelines: mocks must produce empty AND error
// states). Toggle in the browser devtools:
//   localStorage.setItem('aep:mock:alerts', 'empty' | 'some' | 'error')
export type AlertsScenario = "empty" | "some" | "error";

export const emptyAlerts: RcaAgentReportList = { items: [] };

// Mirrors seedProjects: fixed content so screenshots/tests are deterministic.
export const seedAlerts: RcaAgentReport[] = [
  {
    id: "rca-1021",
    project: "demo-shop",
    component: "checkout-service",
    createdAt: "2026-07-09T14:02:00Z",
    title: "Checkout requests failing with 500 after payment-gateway timeout",
    summary:
      "Repeated ERROR logs show unhandled timeout exceptions from the payment-gateway client during checkout submission.",
    classification: "code-level",
    diagnosis:
      "## Diagnosis\n\nThe payment-gateway client has no timeout/retry policy, so a slow upstream response propagates as an unhandled exception, returning a 500 to the caller.\n\n## Remediation\n\nSuggested: wrap the payment-gateway client call with a bounded timeout and a single retry, and return a typed 502 on exhaustion instead of an unhandled 500.",
    issueNumber: 201,
    issueUrl: "https://github.com/acme-dev/demo-shop/issues/201",
    issueTitle: "checkout-service: add timeout/retry around payment-gateway client",
    issueExcerpt:
      "The payment-gateway client call in CheckoutController has no timeout, causing unhandled 500s under upstream latency...",
    dispatched: true,
    deployed: false,
  },
  {
    id: "rca-1020",
    project: "demo-shop",
    component: "checkout-service",
    createdAt: "2026-07-09T09:40:00Z",
    title: "Cart total miscalculated when a discount code is combined with tax",
    summary:
      "Discount is applied after tax instead of before, producing an incorrect total for orders using a discount code.",
    classification: "code-level",
    diagnosis:
      "## Diagnosis\n\nOrder-total calculation applies the discount after tax is computed, rather than before, inflating totals whenever a discount code is present.\n\n## Remediation\n\nSuggested: reorder the pricing pipeline so discounts apply before tax.",
    issueNumber: 198,
    issueUrl: "https://github.com/acme-dev/demo-shop/issues/198",
    issueTitle: "checkout-service: apply discount before tax in order-total calculation",
    issueExcerpt:
      "OrderTotalCalculator computes tax first, then discount, which is backwards from the pricing spec...",
    dispatched: true,
    deployed: true,
    deployedAt: "2026-07-09T12:15:00Z",
  },
  {
    id: "rca-1019",
    project: "demo-shop",
    component: "inventory-service",
    createdAt: "2026-07-08T22:10:00Z",
    title: "Inventory pod restarting on OOM under nightly batch sync",
    summary:
      "Memory usage climbs steadily during the nightly inventory sync job until the pod is OOMKilled.",
    classification: "config-level",
    diagnosis:
      "## Diagnosis\n\nThe inventory-service pod's memory limit (256Mi) is undersized for the nightly batch sync job's peak working set (~420Mi).\n\n## Remediation\n\nApplied: raised the pod's memory limit to 512Mi via a ResourceChange. No code change needed.",
    deployed: false,
  },
  {
    id: "rca-1018",
    project: "gym-tracker",
    component: "workout-api",
    createdAt: "2026-07-08T17:55:00Z",
    title: "Workout history endpoint returns stale data after a delete",
    summary:
      "GET /workouts continues returning a deleted entry for up to two minutes after DELETE succeeds.",
    classification: "mixed",
    diagnosis:
      "## Diagnosis\n\nThe read path serves from a cache with a 2-minute TTL that isn't invalidated on delete; a secondary config issue also left cache invalidation disabled in this environment.\n\n## Remediation\n\nApplied: re-enabled cache invalidation in config. Suggested: invalidate the specific cache key synchronously on delete, rather than relying on TTL expiry alone.",
    issueNumber: 189,
    issueUrl: "https://github.com/acme-dev/gym-tracker/issues/189",
    issueTitle: "workout-api: invalidate workout-history cache entry synchronously on delete",
    issueExcerpt:
      "DeleteWorkoutHandler removes the row but never calls cache.invalidate(key), leaving stale reads until TTL expiry...",
    dispatched: true,
    deployed: false,
  },
  {
    id: "rca-1017",
    project: "gym-tracker",
    component: "workout-api",
    createdAt: "2026-07-07T11:20:00Z",
    title: "Intermittent 502s from workout-api under load",
    summary:
      "Handoff classified this as code-level and created an issue; the coding agent hasn't been dispatched yet.",
    classification: "code-level",
    diagnosis:
      "## Diagnosis\n\nThe connection pool to the primary datastore is sized for steady-state load and exhausts under the observed traffic spike, causing upstream 502s.\n\n## Remediation\n\nSuggested: size the connection pool from load-test data and add a bounded queue instead of failing fast.",
    issueNumber: 185,
    issueUrl: "https://github.com/acme-dev/gym-tracker/issues/185",
    issueTitle: "workout-api: size the datastore connection pool for peak load",
    issueExcerpt:
      "ConnectionPoolConfig.maxSize is set to the steady-state estimate (10) with no headroom for traffic spikes...",
    // Not dispatched yet — issue-only/manual-dispatch mode gap (#155 decision).
    dispatched: false,
    deployed: false,
  },
  {
    id: "rca-1016",
    project: "invoice-hub",
    component: "pdf-export",
    createdAt: "2026-07-06T08:30:00Z",
    title: "PDF export times out for invoices with more than 200 line items",
    summary:
      "Export duration grows non-linearly with line-item count; large invoices exceed the request timeout.",
    classification: "code-level",
    diagnosis:
      "## Diagnosis\n\nThe PDF renderer re-lays-out the full document on every line item appended (O(n²)), rather than appending incrementally.\n\n## Remediation\n\nSuggested: switch to incremental page layout in the PDF renderer.",
    issueNumber: 172,
    issueUrl: "https://github.com/acme-dev/invoice-hub/issues/172",
    issueTitle: "pdf-export: fix O(n²) layout re-computation for large invoices",
    issueExcerpt:
      "PdfDocumentBuilder.appendLineItem() triggers a full relayout() call on every invocation...",
    dispatched: true,
    deployed: true,
    deployedAt: "2026-07-06T19:05:00Z",
  },
  {
    id: "rca-1015",
    project: "salon-booking",
    component: "booking-api",
    createdAt: "2026-07-04T13:45:00Z",
    title: "Double-booking possible when two requests race the same slot",
    summary:
      "Two concurrent booking requests for the same slot can both succeed due to a missing unique constraint.",
    classification: "code-level",
    diagnosis:
      "## Diagnosis\n\nThe bookings table has no unique constraint on (staff_id, slot_start), so two concurrent inserts can both commit.\n\n## Remediation\n\nSuggested: add a unique constraint and handle the resulting conflict as a 409.",
    issueNumber: 160,
    issueUrl: "https://github.com/acme-dev/salon-booking/issues/160",
    issueTitle: "booking-api: prevent double-booking with a unique slot constraint",
    issueExcerpt:
      "BookingRepository.create() has no uniqueness check before insert, allowing a race between concurrent requests...",
    dispatched: true,
    deployed: false,
  },
  {
    id: "rca-1014",
    project: "pet-adoption",
    component: "listings-api",
    createdAt: "2026-07-02T10:05:00Z",
    title: "Shelter search ignores the radius filter above 50 results",
    summary:
      "Pagination applied before the radius filter, so later pages include out-of-radius shelters.",
    classification: "code-level",
    diagnosis:
      "## Diagnosis\n\nThe query pipeline paginates before applying the radius filter, rather than after, so filtered-out shelters still occupy page slots.\n\n## Remediation\n\nSuggested: reorder the pipeline to filter by radius before paginating.",
    issueNumber: 151,
    issueUrl: "https://github.com/acme-dev/pet-adoption/issues/151",
    issueTitle: "listings-api: apply radius filter before pagination",
    issueExcerpt:
      "ShelterSearchQuery.execute() paginates the raw result set, then filters by radius on the current page only...",
    dispatched: true,
    deployed: true,
    deployedAt: "2026-07-02T20:40:00Z",
  },
  {
    id: "rca-1013",
    project: "recipe-box",
    component: "recipe-api",
    createdAt: "2026-06-29T09:15:00Z",
    title: "Recipe images occasionally served with the wrong content-type",
    summary:
      "A misconfigured storage bucket setting served a stale default content-type for a subset of uploads.",
    classification: "config-level",
    diagnosis:
      "## Diagnosis\n\nThe storage bucket's default content-type was left at its pre-migration value (application/octet-stream) for objects uploaded without an explicit content-type header.\n\n## Remediation\n\nApplied: corrected the bucket's default content-type policy. No code change needed.",
    deployed: false,
  },
  {
    id: "rca-1012",
    project: "event-radar",
    component: "events-api",
    createdAt: "2026-06-26T15:50:00Z",
    title: "Reminder notifications sent twice for events with a timezone offset",
    summary:
      "The reminder scheduler double-counts events crossing a DST boundary, sending duplicate notifications.",
    classification: "mixed",
    diagnosis:
      "## Diagnosis\n\nThe scheduler's DST-boundary handling double-schedules affected events; a stale feature flag also re-enabled the legacy scheduler path in this environment.\n\n## Remediation\n\nApplied: disabled the legacy scheduler flag. Suggested: fix DST-boundary handling in the reminder scheduler itself.",
    issueNumber: 140,
    issueUrl: "https://github.com/acme-dev/event-radar/issues/140",
    issueTitle: "events-api: fix duplicate reminders across a DST boundary",
    issueExcerpt:
      "ReminderScheduler.scheduleFor() computes the reminder window using local time, double-counting the repeated hour...",
    dispatched: true,
    deployed: false,
  },
  {
    id: "rca-1011",
    project: "fleet-watch",
    component: "tracking-api",
    createdAt: "2026-06-24T07:25:00Z",
    title: "Vehicle location updates delayed by up to 10 minutes under peak load",
    summary:
      "Handoff found no actionable remediation — the delay matches expected behavior for the current fleet size.",
    classification: "none",
    diagnosis:
      "## Diagnosis\n\nLocation-update latency during the observed window matches expected queuing behavior for the current fleet size and ingest rate; no defect found.\n\n## Remediation\n\nNone — informational only.",
    deployed: false,
  },
  {
    id: "rca-1010",
    project: "study-buddy",
    component: "flashcards-api",
    createdAt: "2026-06-20T12:00:00Z",
    title: "Spaced-repetition scheduling skips cards marked 'hard' twice in a row",
    summary:
      "A boundary condition in the scheduling algorithm skips re-queuing a card marked hard on consecutive reviews.",
    classification: "code-level",
    diagnosis:
      "## Diagnosis\n\nThe scheduler's interval-reset logic has an off-by-one boundary condition when a card is marked 'hard' on two consecutive reviews, skipping the re-queue.\n\n## Remediation\n\nSuggested: fix the boundary condition in the interval-reset calculation.",
    issueNumber: 122,
    issueUrl: "https://github.com/acme-dev/study-buddy/issues/122",
    issueTitle: "flashcards-api: fix consecutive-'hard' scheduling boundary condition",
    issueExcerpt:
      "SchedulingAlgorithm.onReview() resets the interval only when previousGrade !== 'hard', missing the consecutive case...",
    dispatched: true,
    deployed: true,
    deployedAt: "2026-06-21T09:30:00Z",
  },
  {
    id: "rca-1009",
    project: "plant-care",
    component: "care-api",
    createdAt: "2026-06-16T18:40:00Z",
    title: "Watering reminders not sent for plants added via bulk import",
    summary:
      "Bulk-imported plants skip the reminder-scheduling hook that runs on individual plant creation.",
    classification: "code-level",
    diagnosis:
      "## Diagnosis\n\nThe bulk-import path writes rows directly, bypassing the ORM hook that schedules the first watering reminder on create.\n\n## Remediation\n\nSuggested: call the reminder-scheduling hook explicitly at the end of bulk import.",
    issueNumber: 108,
    issueUrl: "https://github.com/acme-dev/plant-care/issues/108",
    issueTitle: "care-api: schedule watering reminders for bulk-imported plants",
    issueExcerpt:
      "BulkImportService.importPlants() uses a raw batch insert, which never invokes Plant.afterCreate()...",
    dispatched: true,
    deployed: false,
  },
];

// Mock server-side default page size when the client omits `limit`
// (#155's list). The bell (#154) always passes its own limit explicitly.
export const ALERTS_PAGE_SIZE = 6;

export const alertsError: ApiError = {
  code: "internal_error",
  message: "Mock error scenario for RCA-agent alert reports",
};

export const alertNotFoundError = (id: string): ApiError => ({
  code: "not_found",
  message: `RCA report ${id} not found`,
});
