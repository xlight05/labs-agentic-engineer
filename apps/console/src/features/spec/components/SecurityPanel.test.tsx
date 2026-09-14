/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type {
  ProjectRole,
  ProjectRoleState,
  ProjectRolesLiveState,
} from "../api/roles";
import { serializeSecurityDesign, type SecurityDesign } from "../api/securityDesign";
import {
  EXPENSE_TRACKER_REFERENCES,
  EXPENSE_TRACKER_TEXT,
} from "../lib/securityTestFixtures";
import { SecurityPanel } from "./SecurityPanel";

afterEach(cleanup);

function role(name: string): SecurityDesign["roles"][number] {
  return {
    name,
    description: `What ${name} may do`,
    stories: [1],
    grants: ["orders:read"],
  };
}

function design(over: Partial<SecurityDesign> = {}): string {
  return serializeSecurityDesign({
    version: 2,
    permissions: [
      {
        resource: "orders",
        component: "orders-api",
        actions: [
          { handle: "read", ownership: "own", description: "See own orders" },
        ],
      },
    ],
    groups: [],
    roles: [role("Admin")],
    screens: [],
    testUsers: [],
    ...over,
  });
}

/** A complete version-1 document — the previous schema, not a half-written one. */
const V1_DOCUMENT = JSON.stringify({
  version: 1,
  coldStartRole: null,
  publicComponents: [],
  roles: [
    {
      name: "Admin",
      description: "What Admin may do",
      stories: [1],
      grantedBy: "an administrator",
      permissions: [{ component: "orders-api", actions: ["read"] }],
    },
  ],
  testUsers: [{ username: "ada", role: "Admin" }],
  thunder: { name: "orders-app", type: "browser" },
});

function liveRole(
  name: string,
  over: Partial<ProjectRoleState> = {},
): ProjectRoleState {
  return { name, platformCreated: true, ...over };
}

function live(
  over: Partial<ProjectRolesLiveState> = {},
): ProjectRolesLiveState {
  return {
    directoryAvailable: true,
    roles: [],
    projectRoles: [],
    testUsers: [],
    ...over,
  };
}

/** One role as the platform's own record has it, with its group assignments. */
function ownedRole(
  name: string,
  assignedTo: { group: string; projects: number }[],
): ProjectRole {
  return { name, resourceServer: RESOURCE_SERVER, assignedTo };
}

const RESOURCE_SERVER =
  "https://aep.wso2.com/orgs/acme/projects/expense-tracker";

function setup(props: Partial<React.ComponentProps<typeof SecurityPanel>> = {}) {
  render(
    <SecurityPanel securityJson={design()} live={undefined} {...props} />,
  );
}

/**
 * A stand-in for the collab room: the panel hands it an updater, it answers
 * with the text it is HOLDING, and it keeps whatever comes back.
 *
 * A plain `vi.fn()` would not do. The panel's contract is that the patch is
 * computed against the room's current text, and only a double that has a text
 * of its own — one a test can change between the render and the click — can
 * hold it to that.
 */
function room(initial: string) {
  const state = { text: initial, writes: [] as string[] };
  const write = vi.fn((update: (current: string) => string | null) => {
    const next = update(state.text);
    if (next === null) return;
    state.text = next;
    state.writes.push(next);
  });
  return { state, write };
}

/**
 * The Expense Tracker worked example, with a live room and a writer.
 *
 * `deliver` re-renders the SAME panel with a newer document, which is how the
 * room's echo arrives in life — a fresh mount would prove nothing about state
 * the panel is holding.
 */
function expenseTracker(
  props: Partial<React.ComponentProps<typeof SecurityPanel>> = {},
) {
  const initial = props.securityJson ?? EXPENSE_TRACKER_TEXT;
  const { state, write } = room(initial);
  const panel = (json: string) => (
    <SecurityPanel
      references={EXPENSE_TRACKER_REFERENCES}
      roomLive
      writeSecurityJson={write}
      {...props}
      securityJson={json}
    />
  );
  const view = render(panel(initial));
  return Object.assign(write, {
    room: state,
    deliver: (next: string) => view.rerender(panel(next)),
  });
}

/** One cell of the matrix, addressed the way a screen reader would. */
function cell(role: string, handle: string): HTMLElement {
  return screen.getByRole("checkbox", { name: `${role} grants ${handle}` });
}

describe("SecurityPanel — reading the document", () => {
  it("shows a spinner while the committed document is loading, not the empty copy", () => {
    setup({ securityJson: null, isPending: true });

    expect(screen.getByLabelText("Loading security")).toBeInTheDocument();
    expect(
      screen.queryByText(/This Security document is empty or incomplete/i),
    ).not.toBeInTheDocument();
  });

  it("surfaces a committed-document read failure", () => {
    setup({ securityJson: null, isError: true });

    expect(
      screen.getByText(/Failed to load the Security document/i),
    ).toBeInTheDocument();
  });

  it("explains an empty or null document with the mock info copy", () => {
    setup({ securityJson: null });

    expect(
      screen.getByText(
        /This Security document is empty or incomplete\. Ask in chat — the design agent can finish it\./,
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/Disposable accounts for agents, not for real people/i),
    ).not.toBeInTheDocument();
  });

  it("explains an empty JSON object with the same info copy", () => {
    setup({ securityJson: "{}" });

    expect(
      screen.getByText(
        /This Security document is empty or incomplete\. Ask in chat — the design agent can finish it\./,
      ),
    ).toBeInTheDocument();
  });

  // Mid-turn the room holds a PREFIX of the document, which is not a fault and
  // must not read like one — but it does cost the reader the warnings, and the
  // page has to say so rather than show an empty page that looks checked.
  it("says a half-written document is still being written, warnings included", () => {
    setup({ securityJson: '{"version": 2,' });

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(/still being written/i);
    expect(alert).toHaveTextContent(/warnings this page raises/i);
    expect(alert).not.toHaveTextContent(/Couldn't read/i);
  });

  it("says a broken document was not checked, rather than showing no warnings", () => {
    setup({ securityJson: '{"version": 2} and then some' });

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(/Couldn't read the Security document/i);
    expect(alert).toHaveTextContent(/Nothing on this page was checked/i);
  });

  // A project whose last design turn predates v2 has a complete v1 file. The
  // panel must say so — not render it as an unfinished draft.
  it("shows a version-1 document as an error naming what v2 removed", () => {
    setup({ securityJson: V1_DOCUMENT });

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(/Couldn't read the Security document/i);
    expect(alert).toHaveTextContent(/v1 is not accepted/i);
    expect(alert).toHaveTextContent(/coldStartRole/);
    expect(alert).not.toHaveTextContent(/still being written/i);
    expect(
      screen.queryByRole("heading", { name: "Roles & users" }),
    ).not.toBeInTheDocument();
  });

  it("shows the Security heading and subtitle", () => {
    setup();

    expect(screen.getByRole("heading", { name: "Security" })).toBeInTheDocument();
    expect(
      screen.getByText(
        /What this project protects, what each role may do with it, and the accounts the validation agent signs in with\./,
      ),
    ).toBeInTheDocument();
  });
});

// The worked example draws exactly this grid: two roles, two resources, seven
// handles, one warning.
describe("SecurityPanel — the permission matrix (Expense Tracker)", () => {
  it("heads a column per user role, in declaration order", () => {
    expenseTracker();

    const headers = screen.getAllByRole("columnheader").map((h) => h.textContent);
    expect(headers).toEqual(["Permission", "", "Employee", "Approver"]);
  });

  it("groups the rows by resource, each headed by the component that owns it", () => {
    expenseTracker();

    expect(screen.getByText("claims")).toBeInTheDocument();
    expect(screen.getByText("reports")).toBeInTheDocument();
    expect(screen.getAllByText("expense-api")).toHaveLength(2);
  });

  it("draws one row per catalog handle with its ownership mark", () => {
    expenseTracker();

    for (const handle of [
      "claims:read-all",
      "claims:submit",
      "claims:approve",
      "claims:reject",
      "reports:read",
      "reports:export",
    ]) {
      expect(screen.getByText(handle)).toBeInTheDocument();
    }
    // `claims:read` is also what the "My Claims" screen requires, so it appears
    // once in the grid and once in the screens list below it.
    expect(screen.getAllByText("claims:read")).toHaveLength(2);
    // The chip says what the schema's `own` / `any` MEAN — a reader of this
    // page has never seen the schema. own: read, submit. any: read-all,
    // approve, reject, reports:read, export.
    expect(screen.getAllByText("own records")).toHaveLength(2);
    expect(screen.getAllByText("all records")).toHaveLength(5);
    expect(screen.queryByText("own")).not.toBeInTheDocument();
    expect(screen.queryByText("any")).not.toBeInTheDocument();
    expect(screen.getByText("See own claims")).toBeInTheDocument();
    expect(screen.getByText("Download CSV")).toBeInTheDocument();
  });

  it("fills exactly the cells the design fills", () => {
    expenseTracker();

    const granted: [string, string][] = [
      ["Employee", "claims:read"],
      ["Employee", "claims:submit"],
      ["Approver", "claims:read"],
      ["Approver", "claims:read-all"],
      ["Approver", "claims:approve"],
      ["Approver", "claims:reject"],
      ["Approver", "reports:read"],
    ];
    const withheld: [string, string][] = [
      ["Employee", "claims:read-all"],
      ["Employee", "claims:approve"],
      ["Employee", "reports:read"],
      ["Employee", "reports:export"],
      ["Approver", "claims:submit"],
      ["Approver", "reports:export"],
    ];
    for (const [role, handle] of granted) expect(cell(role, handle)).toBeChecked();
    for (const [role, handle] of withheld) {
      expect(cell(role, handle)).not.toBeChecked();
    }
  });

  it("puts the baseline in the same grid — signed-in and public, operations and screens", () => {
    expenseTracker();

    // The screens block says "Any signed-in person" too, so the baseline row
    // is read out of the grid rather than off the whole page.
    const grid = screen.getByRole("table");
    const signedIn = within(grid).getByText("Any signed-in person").closest("tr")!;
    expect(within(signedIn).getByText(/GET \/me/)).toBeInTheDocument();
    expect(within(signedIn).getByText(/screen My account \(expense-spa\)/)).toBeInTheDocument();

    const open = within(grid).getByText("Open before sign-in").closest("tr")!;
    expect(within(open).getByText(/GET \/health/)).toBeInTheDocument();
  });

  // The three rows list different KINDS of thing and are a ladder of
  // decreasing protection; nothing on screen said either until the sub-header
  // and these labels did.
  it("heads the baseline rows and names each rung of the ladder", () => {
    expenseTracker({ dependencies: [] });

    const grid = screen.getByRole("table");
    const header = within(grid).getByText("Reachable without a permission");
    expect(header).toBeInTheDocument();
    for (const label of [
      "Any signed-in person",
      "Open before sign-in",
      "No sign-in at all",
    ]) {
      expect(within(grid).getByText(label)).toBeInTheDocument();
    }
    // In that order, under the header, at the foot of the same grid.
    const rows = [...header.closest("tbody")!.querySelectorAll("tr")];
    const index = (text: string) =>
      rows.findIndex((row) => row.textContent?.startsWith(text));
    expect(index("Reachable without a permission")).toBeLessThan(
      index("Any signed-in person"),
    );
    expect(index("Any signed-in person")).toBeLessThan(
      index("Open before sign-in"),
    );
    expect(index("Open before sign-in")).toBeLessThan(index("No sign-in at all"));
  });

  it("says each baseline bucket is empty rather than drawing a blank row", () => {
    setup({ dependencies: [] });

    expect(
      screen.getByText("Nothing — everything needs a permission."),
    ).toBeInTheDocument();
    expect(screen.getByText("Nothing is open before sign-in.")).toBeInTheDocument();
    expect(screen.getByText("Every component signs people in.")).toBeInTheDocument();
  });

  // The sub-header is not conditional on the third row: the first two always
  // render, so the header always has rows to head.
  it("keeps the sub-header when the dependency read has not answered", () => {
    setup();

    expect(
      screen.getByText("Reachable without a permission"),
    ).toBeInTheDocument();
    expect(screen.queryByText("No sign-in at all")).not.toBeInTheDocument();
  });

  // The fact belongs before the point of decision, not in a footnote after it,
  // and it says where the missing controls are rather than what we edit.
  it("says in the intro that a cell only moves a grant, and the rest comes from chat", () => {
    expenseTracker();

    expect(
      screen.getByText(
        "Tick a cell to move a grant between roles. New permissions, roles or screens come from chat.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/is a design conversation/i),
    ).not.toBeInTheDocument();
  });
});

describe("SecurityPanel — warnings on the row they name", () => {
  it("renders 'used nowhere' on the reports:export row, not above the grid", () => {
    expenseTracker();

    const row = screen.getByText("reports:export").closest("tr")!;
    expect(
      within(row).getByText(/declared, used nowhere/i),
    ).toBeInTheDocument();

    // And nowhere else on the page.
    expect(screen.getAllByText(/declared, used nowhere/i)).toHaveLength(1);
  });

  // The Expense Tracker raises both severities: `assign_to_directory_checked`
  // (Approver is assigned to "Finance", which groups[] does not declare) and
  // `handle_used_nowhere` (reports:export). The info one is a note saying
  // nothing is wrong, in the gate's own vocabulary — it is filtered out of the
  // page while the rule itself still runs for the design agent.
  it("keeps an info note off the page while the warnings stay", () => {
    expenseTracker();

    expect(
      screen.queryByTestId("finding-assign_to_directory_checked"),
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/is assigned to group "Finance"/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/build gate resolves it/i)).not.toBeInTheDocument();

    expect(screen.getByTestId("finding-handle_used_nowhere")).toBeInTheDocument();
  });

  // The fact itself is not lost: the role card renders it from the LIVE
  // directory, which is a better answer than the document could give.
  it("still shows what became of that group, from the directory", () => {
    expenseTracker({
      live: live({
        roles: [
          liveRole("Employees"),
          liveRole("Finance", { projects: 2, memberCount: 4 }),
        ],
      }),
    });

    expect(screen.getByText("Finance: Reused")).toBeInTheDocument();
    expect(screen.getByText("· holds roles in 2 projects")).toBeInTheDocument();
  });

  // The page reports the document the toggle produced; it does not refuse the
  // edit and does not silently repair it.
  it("shows the read-all-without-read error rather than blocking the grant", () => {
    const withoutRead = EXPENSE_TRACKER_TEXT.replace(
      '"claims:read",\n        "claims:read-all"',
      '"claims:read-all"',
    );
    expect(withoutRead).not.toBe(EXPENSE_TRACKER_TEXT);
    expenseTracker({ securityJson: withoutRead });

    const row = screen.getByText("claims:read-all").closest("tr")!;
    expect(within(row).getByText(/without "claims:read"/)).toBeInTheDocument();
    // The cell is still live: the author can fix it here.
    expect(cell("Approver", "claims:read")).toBeEnabled();
  });
});

describe("SecurityPanel — the grant toggle", () => {
  it("writes the patched document when a cell is clicked", () => {
    const write = expenseTracker();

    fireEvent.click(cell("Employee", "reports:read"));

    expect(write).toHaveBeenCalledTimes(1);
    const next = JSON.parse(write.room.writes[0]!) as SecurityDesign;
    expect(next.roles[0]!.grants).toEqual([
      "claims:read",
      "claims:submit",
      "reports:read",
    ]);
    // Nobody else moved.
    expect(next.roles[1]!.grants).toEqual([
      "claims:read",
      "claims:read-all",
      "claims:approve",
      "claims:reject",
      "reports:read",
    ]);
  });

  it("removes a grant when a filled cell is clicked", () => {
    const write = expenseTracker();

    fireEvent.click(cell("Employee", "claims:submit"));

    const next = JSON.parse(write.room.writes[0]!) as SecurityDesign;
    expect(next.roles[0]!.grants).toEqual(["claims:read"]);
  });

  // The click patches the text the ROOM is holding, never the text of the last
  // render. When the design agent's flush lands in between, a patch built on
  // the render is applied as a positional diff against a document it no longer
  // describes, and the agent's insertion disappears into the changed span.
  it("patches the room's current text, so an edit that lands mid-click survives", () => {
    const write = expenseTracker();

    // The agent adds a resource to the catalog while the click is in flight.
    const withNewResource = write.room.text.replace(
      '  "groups": [',
      `  "somethingTheAgentAdded": true,\n  "groups": [`,
    );
    expect(withNewResource).not.toBe(write.room.text);
    write.room.text = withNewResource;

    fireEvent.click(cell("Employee", "reports:read"));

    expect(write.room.text).toContain('"somethingTheAgentAdded": true');
    const next = JSON.parse(write.room.text) as SecurityDesign;
    expect(next.roles[0]!.grants).toContain("reports:read");
  });

  // Nothing is held optimistically: the room echoes the change back as a new
  // `securityJson`, and until it does the cell shows what the document says.
  it("does not move the mark itself", () => {
    expenseTracker();

    fireEvent.click(cell("Employee", "reports:read"));

    expect(cell("Employee", "reports:read")).not.toBeChecked();
  });

  it("disables every cell with an explanation when the room is not live", async () => {
    expenseTracker({ roomLive: false });

    expect(cell("Employee", "claims:read")).toBeDisabled();
    expect(cell("Approver", "reports:read")).toBeDisabled();

    fireEvent.mouseOver(cell("Employee", "claims:read").closest("span")!);
    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).toHaveTextContent(
      /showing the last committed copy, so cells are read-only/i,
    );
  });

  it("disables the cells when there is no writer at all", () => {
    expenseTracker({ writeSecurityJson: undefined });

    expect(cell("Employee", "claims:read")).toBeDisabled();
  });

  // One editability question, one answer. The cells and the handler used to
  // ask different ones — the cells asked "is the room live AND is there a
  // writer?", the handler only "is there a writer?" — so a click that reached
  // the handler with a stale room still wrote.
  it("writes nothing on a click the cells say is impossible", () => {
    const write = expenseTracker({ roomLive: false });

    expect(cell("Employee", "reports:read")).toBeDisabled();
    fireEvent.click(cell("Employee", "reports:read"));

    expect(write).not.toHaveBeenCalled();
    expect(write.room.writes).toEqual([]);
  });
});

/**
 * `roleSchema.grants` is `min(1)`, and `parseSecurityDesign` reports a schema
 * failure as "empty or incomplete" — so clearing a role's last grant used to
 * replace the whole page with an info box, taking away the cell that could
 * undo it, while the commit path still put the invalid document in git.
 */
describe("SecurityPanel — a role's last grant", () => {
  /** Employee down to one grant; everything else as the design has it. */
  const ONE_GRANT = EXPENSE_TRACKER_TEXT.replace(
    '"claims:read",\n        "claims:submit"',
    '"claims:submit"',
  );

  it("refuses the cell, and says why", async () => {
    expenseTracker({ securityJson: ONE_GRANT });

    const only = cell("Employee", "claims:submit");
    expect(only).toBeChecked();
    expect(only).toBeDisabled();

    fireEvent.mouseOver(only.closest("span")!);
    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).toHaveTextContent(
      /Every role must grant at least one permission/i,
    );
    expect(tooltip).toHaveTextContent(/all Employee has/i);
  });

  it("leaves every other cell of that role alone", () => {
    expenseTracker({ securityJson: ONE_GRANT });

    expect(cell("Employee", "claims:read")).toBeEnabled();
    expect(cell("Approver", "claims:read")).toBeEnabled();
  });

  // The cell is disabled from the RENDER's document. The document can move
  // under it, so the patch refuses the same edit and the page says so.
  it("refuses the patch and explains it when the document moved under the cell", () => {
    const write = expenseTracker();

    write.room.text = ONE_GRANT;
    fireEvent.click(cell("Employee", "claims:submit"));

    expect(write.room.text).toBe(ONE_GRANT);
    expect(
      screen.getByText(/every role must grant at least one permission/i),
    ).toHaveTextContent(/"Employee"/);
  });
});

describe("SecurityPanel — the patch-failure banner", () => {
  /** A room whose text the panel cannot read at all. */
  function unreadableRoom() {
    const write = expenseTracker();
    write.room.text = '{"version": 2,';
    fireEvent.click(cell("Employee", "reports:read"));
    return write;
  }

  it("reports a document it could not read", () => {
    unreadableRoom();

    expect(
      screen.getByText(/the document could not be read/i),
    ).toBeInTheDocument();
  });

  // It is a statement about ONE text. Once the room delivers another, the
  // banner is reporting a problem that is no longer on the page — and until
  // now only a LATER successful toggle took it down.
  it("clears itself when the document changes, with no second click", () => {
    const write = unreadableRoom();
    expect(screen.getByText(/could not be read/i)).toBeInTheDocument();

    // The room catches up and delivers a readable document.
    const caughtUp = EXPENSE_TRACKER_TEXT.replace(
      "Monthly totals",
      "Monthly totals, by team",
    );
    expect(caughtUp).not.toBe(EXPENSE_TRACKER_TEXT);
    write.deliver(caughtUp);

    expect(screen.queryByText(/could not be read/i)).not.toBeInTheDocument();
    // And the matrix is still the matrix, not a remount.
    expect(cell("Employee", "claims:read")).toBeChecked();
  });
});

describe("SecurityPanel — service-kind roles", () => {
  const withService = () =>
    design({
      roles: [
        role("Admin"),
        {
          name: "reconciliation-job",
          description: "Reconciles orders nightly",
          stories: [4],
          kind: "service",
          grants: ["orders:read"],
        },
      ],
    });

  it("is not a matrix column", () => {
    setup({ securityJson: withService() });

    const headers = screen.getAllByRole("columnheader").map((h) => h.textContent);
    expect(headers).toEqual(["Permission", "", "Admin"]);
    expect(
      screen.queryByRole("checkbox", { name: /reconciliation-job grants/ }),
    ).not.toBeInTheDocument();
  });

  it("is listed apart, with what it holds and the principal it waits on", () => {
    setup({ securityJson: withService() });

    expect(screen.getByText("Service principals")).toBeInTheDocument();
    expect(
      screen.getByText(/Held by an application, not by a person/i),
    ).toHaveTextContent(/the job forwards no token at all/i);
  });
});

describe("SecurityPanel — role cards", () => {
  it("shows description, stories, enrolment and who may hand the role out", () => {
    expenseTracker();

    expect(
      screen.getByText("Submits and follows their own claims"),
    ).toBeInTheDocument();
    expect(screen.getByText("Serves stories 1, 2.")).toBeInTheDocument();
    expect(screen.getByText("Serves story 3.")).toBeInTheDocument();
    expect(
      screen.getByText("Assigned to everyone in Employees."),
    ).toBeInTheDocument();
    expect(screen.getByText("Handed out by Approver.")).toBeInTheDocument();
  });

  it("names the test users, including one the platform will supply", () => {
    expenseTracker();

    expect(screen.getByText("test-employee")).toBeInTheDocument();
    expect(screen.getByText("test-approver")).toBeInTheDocument();
    expect(screen.queryByText("Platform-supplied")).not.toBeInTheDocument();

    cleanup();
    setup({ securityJson: design({ roles: [role("Compliance Admin")] }) });
    expect(screen.getByText("test-compliance-admin")).toBeInTheDocument();
    expect(screen.getByText("Platform-supplied")).toBeInTheDocument();
  });

  // The aggregation the designer needs before reusing an org group: how far the
  // grant they are about to make already reaches.
  it("shows a reused group with how many projects hold roles in it", () => {
    expenseTracker({
      live: live({
        roles: [
          liveRole("Employees"),
          liveRole("Finance", { projects: 2, memberCount: 4 }),
        ],
      }),
    });

    expect(screen.getByText("Finance: Reused")).toBeInTheDocument();
    expect(screen.getByText("· holds roles in 2 projects")).toBeInTheDocument();
    // A group this project introduces has no reach to report.
    expect(screen.getByText("Employees: Reused")).toBeInTheDocument();
    expect(screen.queryByText(/holds roles in 1 project\b/)).not.toBeInTheDocument();
  });

  // The count travels with the ASSIGNMENT, not with the group: "Finance" holds
  // roles in two projects is a fact about this binding, and the platform's own
  // record is where it is authoritative.
  it("prefers the platform record's per-assignment count over the group catalog's", () => {
    expenseTracker({
      live: live({
        roles: [liveRole("Employees"), liveRole("Finance", { projects: 9 })],
        projectRoles: [ownedRole("Approver", [{ group: "Finance", projects: 2 }])],
      }),
    });

    expect(screen.getByText("· holds roles in 2 projects")).toBeInTheDocument();
    expect(screen.queryByText(/9 projects/)).not.toBeInTheDocument();
  });

  it("says a self-service role is self-service instead of naming a group", () => {
    setup({
      securityJson: design({
        roles: [{ ...role("Shopper"), enrolment: "self-service" }],
      }),
    });

    expect(
      screen.getByText(
        "Self-service — the application assigns it when an account is created.",
      ),
    ).toBeInTheDocument();
    for (const label of [/Reused/, /New at Build/, /Not ours/]) {
      expect(screen.queryByText(label)).not.toBeInTheDocument();
    }
  });

  it("no longer repeats the grants the matrix already draws", () => {
    expenseTracker();

    // `claims:submit` is granted by Employee and required by no screen, so the
    // matrix row is the only place it can legitimately appear.
    expect(screen.getAllByText("claims:submit")).toHaveLength(1);
    expect(screen.getAllByText("claims:approve")).toHaveLength(1);
  });

  it("shows no Reveal / Rotate / Delete / Add / Hide controls", () => {
    expenseTracker();

    for (const name of [
      /Reveal/i,
      /Rotate/i,
      /^Delete$/i,
      /Add a test user/i,
      /^Hide$/i,
    ]) {
      expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
    }
  });
});

describe("SecurityPanel — a role's groups against the shared directory", () => {
  // The live half is the directory's GROUP catalog. A project role is not an
  // object on the directory — it reaches an app through the groups it is
  // assigned to — so every chip is about one `assignTo` group, never about the
  // role's own name.
  function assigned(...groups: string[]): string {
    return design({ roles: [{ ...role("Admin"), assignTo: groups }] });
  }

  it('reads "Reused" for an assignTo group the platform already created', () => {
    setup({
      securityJson: assigned("Administrators"),
      live: live({ roles: [liveRole("Administrators")] }),
    });

    expect(screen.getByText("Administrators: Reused")).toBeInTheDocument();
  });

  it('reads "New at Build" for an assignTo group the directory does not have', () => {
    setup({
      securityJson: assigned("Administrators"),
      live: live({ roles: [liveRole("Something Else")] }),
    });

    expect(screen.getByText("Administrators: New at Build")).toBeInTheDocument();
  });

  it('reads "Not ours" with the leave-alone tooltip', async () => {
    setup({
      securityJson: assigned("Administrators"),
      live: live({
        roles: [liveRole("Administrators", { platformCreated: false })],
      }),
    });

    const chip = screen.getByText("Administrators: Not ours");
    expect(screen.queryByText("Administrators: Reused")).not.toBeInTheDocument();

    fireEvent.mouseOver(chip);
    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).toHaveTextContent(
      /This group already exists and the platform did not create it, so it will be left alone\./,
    );
  });

  it("matches the design's group to the directory's case-insensitively", () => {
    setup({
      securityJson: assigned("Administrators"),
      live: live({ roles: [liveRole("administrators")] }),
    });

    expect(screen.getByText("Administrators: Reused")).toBeInTheDocument();
  });

  it("gives one chip per assignTo group, each judged on its own", () => {
    setup({
      securityJson: assigned("Administrators", "Finance"),
      live: live({ roles: [liveRole("Administrators")] }),
    });

    expect(screen.getByText("Administrators: Reused")).toBeInTheDocument();
    expect(screen.getByText("Finance: New at Build")).toBeInTheDocument();
  });

  // The role's own name is NOT a directory object: a project role that happens
  // to be spelled like an org group must not read as one already there.
  it("never judges the role's own name against the group catalog", () => {
    setup({
      securityJson: assigned("Administrators"),
      live: live({ roles: [liveRole("Admin"), liveRole("Administrators")] }),
    });

    expect(screen.queryByText("Admin: Reused")).not.toBeInTheDocument();
    expect(screen.getByText("Administrators: Reused")).toBeInTheDocument();
  });

  it("shows no directory chip for a role with no assignTo", () => {
    setup({
      securityJson: design({
        roles: [
          { ...role("Ledger Sync"), kind: "service" },
          { ...role("Shopper"), enrolment: "self-service" },
        ],
      }),
      live: live({ roles: [liveRole("Ledger Sync"), liveRole("Shopper")] }),
    });

    for (const label of [/Reused/, /New at Build/, /Not ours/]) {
      expect(screen.queryByText(label)).not.toBeInTheDocument();
    }
  });

  it("omits live chips when the directory is unreachable — no IDP alert", () => {
    setup({
      securityJson: assigned("Administrators"),
      live: live({
        directoryAvailable: false,
        roles: [liveRole("Administrators", { platformCreated: false })],
      }),
    });

    expect(
      screen.queryByText(/identity provider could not be reached/i),
    ).not.toBeInTheDocument();
    for (const label of [/Reused/, /New at Build/, /Not ours/]) {
      expect(screen.queryByText(label)).not.toBeInTheDocument();
    }
  });
});

describe("SecurityPanel — the resource server", () => {
  it("names the audience every grant of these roles is on", () => {
    expenseTracker({
      live: live({ projectRoles: [ownedRole("Approver", [])] }),
    });

    expect(screen.getByText("Resource server")).toBeInTheDocument();
    expect(screen.getByText(RESOURCE_SERVER)).toBeInTheDocument();
  });

  // Before the first Build there is no platform record to read it from, and a
  // URL the console guessed would be worse than no line at all.
  it("says nothing when the platform has no record of this project yet", () => {
    expenseTracker({ live: live({ projectRoles: [] }) });

    expect(screen.queryByText("Resource server")).not.toBeInTheDocument();
  });
});

describe("SecurityPanel — the rest of the page", () => {
  it("has no tabs; Roles & users is a heading", () => {
    setup();

    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Roles & users" }),
    ).toBeInTheDocument();
  });

  it("lists the org groups the project introduces, and nothing when it introduces none", () => {
    setup({
      securityJson: design({
        groups: [{ name: "Finance", description: "Approves what we pay for" }],
      }),
    });

    expect(screen.getByText(/Approves what we pay for/)).toBeInTheDocument();

    cleanup();
    setup();
    expect(screen.queryByText("New org groups")).not.toBeInTheDocument();
  });

  // It is how the platform works on every healthy project, so it is said once,
  // quietly, under the Roles & users heading — not as a warning alert that
  // would cry wolf on every project that has nothing wrong with it.
  it("says once, however many roles, that the accounts are disposable", () => {
    setup({
      securityJson: design({
        roles: [role("Admin"), role("Viewer"), role("Auditor")],
      }),
    });

    const notes = screen.getAllByText(
      /Disposable accounts for agents, not for real people/i,
    );
    expect(notes).toHaveLength(1);
    const block = notes[0]!.parentElement!;
    expect(block).toHaveTextContent(
      /passwords are shown on Deploy after Build publishes them/i,
    );
    expect(block).toHaveTextContent(/never name a real person/i);
    expect(block).not.toHaveTextContent(/roles gate ticket/i);
    // Under the heading it belongs to, and not borrowing an alert's severity.
    expect(block).toHaveTextContent(/Roles & users/);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows what each screen takes to reach, including public and signed-in", () => {
    setup({
      securityJson: design({
        screens: [
          { component: "storefront", screen: "Orders", requires: "orders:read" },
          { component: "storefront", screen: "Catalog", requires: "public" },
          { component: "storefront", screen: "My account", requires: null },
        ],
      }),
    });

    expect(screen.getByText("Orders")).toBeInTheDocument();
    expect(screen.getByText("Open to everyone, no sign-in")).toBeInTheDocument();
    // "Any signed-in person" is also the matrix's first baseline label now, so
    // this one is read off the screen row it belongs to.
    const myAccount = screen.getByText("My account").closest("div")!;
    expect(
      within(myAccount).getByText("Any signed-in person"),
    ).toBeInTheDocument();
  });

  it("omits the screens block for an API-only project", () => {
    setup();

    expect(screen.queryByText("Screens")).not.toBeInTheDocument();
  });
});
