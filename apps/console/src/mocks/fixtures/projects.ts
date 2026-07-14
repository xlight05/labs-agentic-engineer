import type { components } from "../../generated/aep-api";

type Project = components["schemas"]["Project"];
type ProjectList = components["schemas"]["ProjectList"];
type ApiError = components["schemas"]["Error"];

// Scenario switch (api-guidelines: mocks must produce empty AND error
// states). Toggle in the browser devtools:
//   localStorage.setItem('aep:mock:projects', 'empty' | 'some' | 'error')
export type ProjectsScenario = "empty" | "some" | "error";

export const emptyProjects: ProjectList = { items: [] };

export const seedProjects: Project[] = [
  {
    name: "demo-shop",
    displayName: "Demo Shop",
    description: "Sample storefront seeded by the mock layer",
    status: "active",
    createdAt: "2026-06-12T09:30:00Z",
    repoUrl: "https://github.com/acme-dev/demo-shop.git",
  },
  {
    name: "gym-tracker",
    displayName: "Gym Tracker",
    description: "Track workouts, sets, and progress over time",
    status: "active",
    createdAt: "2026-06-20T14:05:00Z",
    repoUrl: "https://github.com/acme-dev/gym-tracker.git",
  },
  // invoice-hub deliberately has NO repoUrl — exercises the delete dialog's
  // generic-copy variant (failed/absent repo provisioning, #107).
  {
    name: "invoice-hub",
    displayName: "Invoice Hub",
    description: "Small-business invoicing with PDF export",
    status: "active",
    createdAt: "2026-07-01T08:15:00Z",
  },
  {
    name: "salon-booking",
    displayName: "Salon Booking",
    description: "Appointments with staff calendars and SMS reminders",
    status: "active",
    createdAt: "2026-06-25T10:00:00Z",
  },
  {
    name: "pet-adoption",
    displayName: "Pet Adoption",
    description: "Browse shelters and apply to adopt",
    status: "active",
    createdAt: "2026-06-18T16:45:00Z",
  },
  {
    name: "recipe-box",
    displayName: "Recipe Box",
    description: "Save, tag, and share family recipes",
    status: "active",
    createdAt: "2026-06-15T11:20:00Z",
  },
  {
    name: "event-radar",
    displayName: "Event Radar",
    description: "Local events with reminders and friends' plans",
    status: "active",
    createdAt: "2026-06-22T09:10:00Z",
  },
  {
    name: "fleet-watch",
    displayName: "Fleet Watch",
    description: "Delivery fleet tracking and route history",
    status: "active",
    createdAt: "2026-06-28T13:40:00Z",
  },
  {
    name: "study-buddy",
    displayName: "Study Buddy",
    description: "Flashcards and spaced-repetition study plans",
    status: "active",
    createdAt: "2026-06-30T18:05:00Z",
  },
  {
    name: "plant-care",
    displayName: "Plant Care",
    description: "Watering schedules and plant health notes",
    status: "active",
    createdAt: "2026-06-10T08:00:00Z",
  },
  {
    name: "team-lunch",
    displayName: "Team Lunch",
    description: "Vote on where the team eats on Fridays",
    status: "active",
    createdAt: "2026-06-11T12:30:00Z",
  },
  {
    name: "book-club",
    displayName: "Book Club",
    description: "Reading lists, meetings, and discussion notes",
    status: "active",
    createdAt: "2026-06-14T19:15:00Z",
  },
  {
    name: "run-coach",
    displayName: "Run Coach",
    description: "Training plans and race-day countdowns",
    status: "active",
    createdAt: "2026-06-17T06:50:00Z",
  },
  {
    name: "expense-lens",
    displayName: "Expense Lens",
    description: "Snap receipts and categorize team spend",
    status: "active",
    createdAt: "2026-06-23T15:25:00Z",
  },
  {
    name: "ticket-desk",
    displayName: "Ticket Desk",
    description: "Lightweight internal helpdesk with SLAs",
    status: "active",
    createdAt: "2026-06-27T09:55:00Z",
  },
];

// Mock server-side page size for the projects list (View more pagination).
export const PROJECTS_PAGE_SIZE = 6;

export const projectsError: ApiError = {
  code: "internal_error",
  message: "Mock error scenario for the projects list",
};

export const duplicateProjectError: ApiError = {
  code: "conflict",
  message: "A project with this name already exists",
};

// Delete-failure scenario (#107): toggle in the browser devtools:
//   localStorage.setItem('aep:mock:projects:delete', 'error')
export const deleteProjectError: ApiError = {
  code: "internal_error",
  message: "Mock error scenario for project deletion",
};

// The /config mock (org-config projection) lives in mocks/fixtures/settings.ts
// and mocks/handlers/settings.ts (issue #96 owns that endpoint) — not
// duplicated here.
