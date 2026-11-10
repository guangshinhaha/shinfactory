import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AgentFeed } from "./AgentFeed";
import { buildSession, buildSnapshot } from "../test-utils";
import type { Snapshot } from "~/client";

const mockSend = vi.fn();
const mockOpen = vi.fn();
let mockSnapshot: Snapshot = buildSnapshot();

vi.mock("~/client", async () => {
  const actual = await vi.importActual("~/client");
  return {
    ...(actual as Record<string, unknown>),
    useSuspenseSnapshot: () => ({ data: mockSnapshot }),
    useSendControl: () => ({ mutate: mockSend, isPending: false }),
    useOpenInOrca: () => ({ mutate: mockOpen, isPending: false, isError: false, error: null }),
    useSessionEvents: () => ({ data: [] }),
  };
});

beforeEach(() => {
  mockSend.mockClear();
  mockOpen.mockClear();
});

function snapshotFor(runtime: string): Snapshot {
  return buildSnapshot({
    sessions: [buildSession({ id: "sess-1", runtime })],
    orders: [
      {
        id: "order-1",
        status: "active",
        stages: [
          {
            provider: "claude",
            model: "opus",
            status: "active",
            task_key: "execute",
            session_id: "sess-1",
            dispatch_id: "ctx_1",
          },
        ],
      },
    ],
  });
}

describe("AgentFeed steering", () => {
  it("keeps the steer box on a steerable runtime", () => {
    mockSnapshot = snapshotFor("process");
    render(<AgentFeed sessionId="sess-1" />);
    expect(screen.getByLabelText("Agent instructions")).toBeInTheDocument();
    expect(screen.queryByText("Open in Orca")).toBeNull();
  });

  it("swaps the steer box for Open in Orca on an orca session", async () => {
    mockSnapshot = snapshotFor("orca");
    render(<AgentFeed sessionId="sess-1" />);
    expect(screen.queryByLabelText("Agent instructions")).toBeNull();
    await userEvent.setup().click(screen.getByText("Open in Orca"));
    expect(mockOpen).toHaveBeenCalledWith("ctx_1");
    expect(mockSend).not.toHaveBeenCalled();
  });
});
