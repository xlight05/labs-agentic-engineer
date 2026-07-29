import type { components } from "../../generated/aep-api";

type ApiError = components["schemas"]["Error"];
import { http, HttpResponse, type JsonBodyType } from "msw";
import {
  componentDeployments,
  componentOpenApi,
  projectBuildRuns,
  projectCycleBuilds,
  projectBuilds,
  projectComponents,
  projectSectionError,
  projectSpecFiles,
  projectStatuses,
  projectTags,
  projectTasks,
  specFileContent,
  specFileMetas,
  specFileNotFound,
  type ProjectScenario,
} from "../fixtures/project";
import {
  findTask,
  isSettledStatus,
  liveLine,
  streamFrames,
  taskDetailOf,
} from "../fixtures/task-log";
import {
  isTerminalRunState,
  runCycleLines,
  runHeartbeatLine,
} from "../fixtures/run-progress";

function scenario(): ProjectScenario {
  return (
    (localStorage.getItem("aep:mock:project") as ProjectScenario | null) ??
    "building"
  );
}

function respond<T extends JsonBodyType>(
  pick: (s: Exclude<ProjectScenario, "error">) => T,
) {
  const s = scenario();
  if (s === "error") {
    return HttpResponse.json(projectSectionError, {
      status: 500,
    });
  }
  return HttpResponse.json(pick(s));
}

// Project-scoped reads backing the overview page (issue #77). The project
// itself (GET /projects/:projectName) is served by handlers/projects.ts.
export const projectHandlers = [
  http.get("*/api/v1/projects/:projectName/status", () =>
    respond((s) => projectStatuses[s]),
  ),
  http.get("*/api/v1/projects/:projectName/components", () =>
    respond((s) => projectComponents[s]),
  ),
  // The component's OpenAPI contract for the in-app viewer dialog. Errors
  // follow the section scenario; otherwise a spec keyed to the component name.
  http.get(
    "*/api/v1/projects/:projectName/components/:componentName/openapi",
    ({ params }) => respond(() => componentOpenApi(String(params.componentName))),
  ),
  // Per-component release bindings — the overview's "Open app" link (#196)
  // and the Deployments board's fan-out (#216).
  http.get(
    "*/api/v1/projects/:projectName/components/:componentName/deployments",
    ({ params }) =>
      respond((s) => componentDeployments(s, String(params.componentName))),
  ),
  http.get("*/api/v1/projects/:projectName/tasks", ({ request }) => {
    // ?tag=vN scopes to one build's lineage, mirroring the aep:spec/<tag>
    // label read (#185); absent = all versions.
    const tag = new URL(request.url).searchParams.get("tag");
    return respond((s) =>
      tag
        ? projectTasks[s].filter((t) => t.lineage?.specTag === tag)
        : projectTasks[s],
    );
  }),
  // Builds page: the version ledger (also feeds the overview's version menu).
  http.get("*/api/v1/projects/:projectName/builds", () =>
    respond((s) => projectBuilds[s]),
  ),
  // …and one version's whole run story: run rows + cycle records, DB-only.
  http.get("*/api/v1/projects/:projectName/builds/:tag/runs", ({ params }) =>
    respond((s) => ({ ...projectBuildRuns[s], tag: String(params.tag) })),
  ),
  // A build session's fan-out. Derived from the cluster on the real server, so
  // the console only ever asks for a session whose merge landed — and asks per
  // session, which is why the fixture is keyed by scenario rather than by cycle.
  http.get(
    "*/api/v1/projects/:projectName/builds/:tag/cycles/:cycleId/builds",
    () => respond((s) => ({ items: projectCycleBuilds[s] })),
  ),
  // Cancel: 202 means the SIGNAL was sent — the run row flips to `cancelled`
  // when the supervisor acts on it, which is why there is no body to return.
  http.post("*/api/v1/projects/:projectName/runs/:runId/cancel", () => {
    if (scenario() === "error") {
      return HttpResponse.json(projectSectionError, { status: 503 });
    }
    return new HttpResponse(null, { status: 202 });
  }),
  // The run feed: ONE SSE stream for the whole run, frames grouped by cycle.
  // ONLY a terminal run settles it — a live run's stream stays open, which is
  // the property the console's reconnect logic is written against.
  http.get(
    "*/api/v1/projects/:projectName/runs/:runId/progress",
    ({ request }) => {
      const s = scenario();
      if (s === "error") {
        return HttpResponse.json(projectSectionError, { status: 500 });
      }
      const runs = projectBuildRuns[s].runs;
      const run = runs[0];
      const encoder = new TextEncoder();
      let timer: ReturnType<typeof setInterval> | undefined;

      const stream = new ReadableStream<Uint8Array>({
        async start(controller) {
          const send = (data: string) =>
            controller.enqueue(encoder.encode(`data: ${data}\n\n`));
          const delay = (ms: number) =>
            new Promise((resolve) => setTimeout(resolve, ms));

          let seq = 0;
          for (const [i, cycle] of (run?.cycles ?? []).entries()) {
            if (request.signal.aborted) return controller.close();
            send(JSON.stringify({ type: "cycle", cycle }));
            for (const line of runCycleLines(cycle, i, seq)) {
              if (request.signal.aborted) return controller.close();
              send(JSON.stringify({ type: "line", line }));
              seq = (line.seq ?? seq) + 1;
              await delay(120);
            }
          }
          if (!run || isTerminalRunState(run.state)) {
            send(JSON.stringify({ type: "done", state: run?.state ?? "succeeded" }));
            send("[DONE]");
            controller.close();
            return;
          }
          // Live run: heartbeat lines on the newest cycle until disconnect.
          const last = run.cycles[run.cycles.length - 1];
          let tick = 1;
          timer = setInterval(() => {
            if (request.signal.aborted || !last) {
              clearInterval(timer);
              controller.close();
              return;
            }
            send(
              JSON.stringify({
                type: "line",
                line: runHeartbeatLine(last, run.cycles.length - 1, seq++, tick++),
              }),
            );
          }, 4000);
        },
        cancel() {
          if (timer) clearInterval(timer);
        },
      });

      return new HttpResponse(stream, {
        headers: {
          "Content-Type": "text/event-stream",
          "Cache-Control": "no-cache",
        },
      });
    },
  ),
  // Task page (#173): one task with its execution history…
  http.get("*/api/v1/projects/:projectName/tasks/:issueNumber", ({ params }) => {
    const s = scenario();
    if (s === "error") {
      return HttpResponse.json(projectSectionError, {
        status: 500,
      });
    }
    const detail = taskDetailOf(s, Number(params.issueNumber));
    if (!detail) {
      return HttpResponse.json(
        {
          code: "not_found",
          message: `no task #${String(params.issueNumber)}`,
        } satisfies ApiError,
        { status: 404 },
      );
    }
    return HttpResponse.json(detail);
  }),
  // …and its SSE log: replay the timeline as TaskStreamEvent frames, then
  // either settle with `done` + [DONE] or keep ticking heartbeat lines for a
  // live task (matches the contract's wire format, keep-alives included).
  http.get(
    "*/api/v1/projects/:projectName/tasks/:issueNumber/log",
    ({ params, request }) => {
      const s = scenario();
      if (s === "error") {
        return HttpResponse.json(projectSectionError, {
          status: 500,
        });
      }
      const issueNumber = Number(params.issueNumber);
      const frames = streamFrames(s, issueNumber);
      const task = findTask(s, issueNumber);
      const settled = !task || isSettledStatus(task.derivedStatus);
      const encoder = new TextEncoder();
      let timer: ReturnType<typeof setInterval> | undefined;

      const stream = new ReadableStream<Uint8Array>({
        async start(controller) {
          const send = (data: string) =>
            controller.enqueue(encoder.encode(`data: ${data}\n\n`));
          const delay = (ms: number) =>
            new Promise((resolve) => setTimeout(resolve, ms));

          let seq = 0;
          for (const frame of frames) {
            if (request.signal.aborted) return controller.close();
            send(JSON.stringify(frame));
            if (frame.type === "line") seq = (frame.line?.seq ?? seq) + 1;
            await delay(120);
          }
          if (settled) {
            send("[DONE]");
            controller.close();
            return;
          }
          // Live task: heartbeat lines until the client disconnects.
          let tick = 1;
          timer = setInterval(() => {
            if (request.signal.aborted) {
              clearInterval(timer);
              controller.close();
              return;
            }
            send(
              JSON.stringify({
                type: "line",
                line: liveLine(issueNumber, seq++, tick++),
              }),
            );
          }, 4000);
        },
        cancel() {
          if (timer) clearInterval(timer);
        },
      });

      return new HttpResponse(stream, {
        headers: {
          "Content-Type": "text/event-stream",
          "Cache-Control": "no-cache",
        },
      });
    },
  ),
  http.get("*/api/v1/projects/:projectName/tags", () =>
    respond((s) => projectTags[s]),
  ),
  // Files API (#113): list-files metadata + per-file content reads, exactly
  // as aep-api serves them (repo-relative specs/ paths).
  http.get("*/api/v1/projects/:projectName/files", () =>
    respond((s) => specFileMetas(projectSpecFiles[s])),
  ),
  http.get("*/api/v1/projects/:projectName/files/*", ({ request }) => {
    const s = scenario();
    if (s === "error") {
      return HttpResponse.json(projectSectionError, {
        status: 500,
      });
    }
    const pathname = new URL(request.url).pathname;
    const path = decodeURIComponent(pathname.replace(/^.*\/files\//, ""));
    const file = specFileContent(projectSpecFiles[s], path);
    if (!file) {
      return HttpResponse.json(specFileNotFound(path), {
        status: 404,
      });
    }
    return HttpResponse.json(file);
  }),
];
