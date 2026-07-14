import { http, HttpResponse } from "msw";
import {
  ALERTS_PAGE_SIZE,
  alertNotFoundError,
  alertsError,
  emptyAlerts,
  seedAlerts,
  type AlertsScenario,
} from "../fixtures/alerts";

function scenario(): AlertsScenario {
  return (
    (localStorage.getItem("aep:mock:alerts") as AlertsScenario | null) ?? "some"
  );
}

// Newest first, matching the contract's "list ... newest first" ordering.
function currentAlerts() {
  if (scenario() === "empty") return [];
  return [...seedAlerts].sort((a, b) => (a.createdAt! < b.createdAt! ? 1 : -1));
}

export const alertsHandlers = [
  http.get("*/api/v1/rca-agent/reports", ({ request }) => {
    if (scenario() === "error") {
      return HttpResponse.json(alertsError, {
        status: 500,
      });
    }
    const all = currentAlerts();
    if (all.length === 0) {
      return HttpResponse.json(emptyAlerts);
    }
    const params = new URL(request.url).searchParams;
    // The cursor is opaque to the client; the mock's is a plain offset.
    const offset = Number(params.get("cursor") ?? 0) || 0;
    const limit = Number(params.get("limit")) || ALERTS_PAGE_SIZE;
    const items = all.slice(offset, offset + limit);
    const next = offset + limit;
    return HttpResponse.json({
      items,
      ...(next < all.length && { nextCursor: String(next) }),
    });
  }),

  http.get("*/api/v1/rca-agent/reports/:reportId", ({ params }) => {
    if (scenario() === "error") {
      return HttpResponse.json(alertsError, {
        status: 500,
      });
    }
    const report = currentAlerts().find((r) => r.id === params.reportId);
    if (!report) {
      return HttpResponse.json(alertNotFoundError(String(params.reportId)), {
        status: 404,
      });
    }
    return HttpResponse.json(report);
  }),
];
