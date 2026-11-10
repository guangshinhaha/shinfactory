import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SchedulerFeed } from "./SchedulerFeed";
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

vi.mock("@tanstack/react-router", () => ({
  Link: ({ to, children }: { to: string; children: React.ReactNode }) => (
    <a href={to}>{children}</a>
  ),
}));

beforeEach(() => {
  mockSnapshot = buildSnapshot();
  mockSend.mockClear();
  mockOpen.mockClear();
});

describe("SchedulerFeed", () => {
  it("renders scheduler header", () => {
    render(<SchedulerFeed />);
    expect(screen.getByText("Scheduler")).toBeInTheDocument();
  });

  it("renders empty state when no scheduler session", () => {
    render(<SchedulerFeed />);
    expect(screen.getByText(/The scheduler starts when you run/)).toBeInTheDocument();
    expect(screen.getByText("Learn how the loop works")).toBeInTheDocument();
  });

  it("renders input area", () => {
    render(<SchedulerFeed />);
    expect(screen.getByPlaceholderText("Enter instructions or critique...")).toBeInTheDocument();
    expect(screen.getByText("Send")).toBeInTheDocument();
  });

  it("shows bootstrap status message when schedule bootstrap session is running", () => {
    const bootstrapSession = buildSession({
      id: "bootstrap-schedule-123",
      task_key: "schedule",
      status: "running",
      current_action: "",
    });
    mockSnapshot = buildSnapshot({
      loop_state: "running",
      sessions: [bootstrapSession],
      active: [bootstrapSession],
    });

    render(<SchedulerFeed />);
    expect(screen.getByText("Bootstrapping schedule skill...")).toBeInTheDocument();
    expect(
      screen.getByText("Bootstrapping schedule skill. This usually takes a few seconds."),
    ).toBeInTheDocument();
  });

  it("shows bootstrap status message when schedule bootstrap is pending with no session", () => {
    mockSnapshot = buildSnapshot({
      loop_state: "running",
      sessions: [],
      orders: [
        {
          id: "schedule",
          status: "active",
          stages: [{ provider: "claude", model: "opus", status: "pending", task_key: "schedule" }],
        },
      ],
    });

    render(<SchedulerFeed />);
    expect(screen.getByText("Bootstrapping schedule skill...")).toBeInTheDocument();
    expect(
      screen.getByText("Bootstrapping schedule skill. This usually takes a few seconds."),
    ).toBeInTheDocument();
  });

  it("replaces the input with an Open in Orca button when the scheduler runs on orca", async () => {
    const session = buildSession({ id: "sess-sched", task_key: "schedule", runtime: "orca" });
    mockSnapshot = buildSnapshot({
      sessions: [session],
      orders: [
        {
          id: "schedule",
          status: "active",
          stages: [
            {
              provider: "claude",
              model: "opus",
              status: "active",
              task_key: "schedule",
              session_id: "sess-sched",
              dispatch_id: "ctx_sched",
            },
          ],
        },
      ],
    });

    render(<SchedulerFeed />);
    expect(screen.queryByPlaceholderText("Enter instructions or critique...")).toBeNull();
    await userEvent.setup().click(screen.getByText("Open in Orca"));
    expect(mockOpen).toHaveBeenCalledWith("ctx_sched");
    expect(mockSend).not.toHaveBeenCalled();
  });

  it("sends steer command on submit", async () => {
    render(<SchedulerFeed />);
    const user = userEvent.setup();
    const textarea = screen.getByPlaceholderText("Enter instructions or critique...");
    await user.type(textarea, "deploy to staging");
    await user.click(screen.getByText("Send"));
    expect(mockSend).toHaveBeenCalledWith({
      action: "steer",
      target: "schedule",
      prompt: "deploy to staging",
    });
  });

  it("sends steer command on Enter", async () => {
    render(<SchedulerFeed />);
    const user = userEvent.setup();
    const textarea = screen.getByPlaceholderText("Enter instructions or critique...");
    await user.type(textarea, "run tests{Enter}");
    expect(mockSend).toHaveBeenCalledWith({
      action: "steer",
      target: "schedule",
      prompt: "run tests",
    });
  });

  it("clears input after submit", async () => {
    render(<SchedulerFeed />);
    const user = userEvent.setup();
    const textarea = screen.getByPlaceholderText(
      "Enter instructions or critique...",
    ) as HTMLTextAreaElement;
    await user.type(textarea, "do something");
    await user.click(screen.getByText("Send"));
    expect(textarea.value).toBe("");
  });

  it("does not send empty input", async () => {
    render(<SchedulerFeed />);
    const user = userEvent.setup();
    await user.click(screen.getByText("Send"));
    expect(mockSend).not.toHaveBeenCalled();
  });

  it("shows idle prompt when loop is idle even if active list still includes schedule", () => {
    const session = buildSession({
      id: "scheduler-1",
      loop_state: "idle",
      task_key: "schedule",
    });
    mockSnapshot = buildSnapshot({
      loop_state: "idle",
      sessions: [session],
      active: [session],
    });

    render(<SchedulerFeed />);
    expect(screen.getByText("Talk to the scheduler...")).toBeInTheDocument();
    expect(screen.queryByText("Thinking…")).not.toBeInTheDocument();
  });

  it("shows idle prompt when scheduler session is active but has no current action", () => {
    const session = buildSession({
      id: "scheduler-2",
      loop_state: "running",
      task_key: "schedule",
      current_action: "",
    });
    mockSnapshot = buildSnapshot({
      loop_state: "running",
      sessions: [session],
      active: [session],
    });

    render(<SchedulerFeed />);
    expect(screen.getByText("Talk to the scheduler...")).toBeInTheDocument();
    expect(screen.queryByText("Thinking…")).not.toBeInTheDocument();
  });
});
