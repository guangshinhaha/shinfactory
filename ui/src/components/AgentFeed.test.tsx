import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { AgentFeed } from "./AgentFeed";
import { buildSession, buildSnapshot } from "../test-utils";
import type { Snapshot } from "~/client";

let mockSnapshot: Snapshot = buildSnapshot();

vi.mock("~/client", async () => {
  const actual = await vi.importActual("~/client");
  return {
    ...(actual as Record<string, unknown>),
    useSuspenseSnapshot: () => ({ data: mockSnapshot }),
    useSendControl: () => ({ mutate: vi.fn(), isPending: false }),
    useSessionEvents: () => ({ data: [] }),
  };
});

vi.mock("@tanstack/react-router", () => ({
  Link: ({ to, children }: { to: string; children: React.ReactNode }) => (
    <a href={to}>{children}</a>
  ),
}));

function snapshotFor(runtime: string): Snapshot {
  const session = buildSession({ id: "s1", runtime, status: "running" });
  return buildSnapshot({ loop_state: "running", sessions: [session], active: [session] });
}

beforeEach(() => {
  mockSnapshot = buildSnapshot();
});

describe("AgentFeed stop button", () => {
  it("says it ends the worker on an orca stage", () => {
    mockSnapshot = snapshotFor("orca");
    render(<AgentFeed sessionId="s1" />);
    expect(screen.getByRole("button", { name: "Stop (ends worker)" })).toBeInTheDocument();
  });

  it("stays plain Stop on other runtimes", () => {
    mockSnapshot = snapshotFor("process");
    render(<AgentFeed sessionId="s1" />);
    expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
  });
});
