import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { buildSession, buildSnapshot } from "~/test-utils";
import type { ControlCommand, Snapshot } from "~/client";
import { TopBar } from "./TopBar";

let mockSnapshot: Snapshot = buildSnapshot();
let mockWS: "connected" | "connecting" | "disconnected" = "connected";
const sendWSControl = vi.fn();

vi.mock("~/client/ws", async () => {
  const actual = await vi.importActual<Record<string, unknown>>("~/client/ws");
  return {
    ...actual,
    sendWSControl: (cmd: ControlCommand) => sendWSControl(cmd),
    getWSStatus: () => mockWS,
  };
});

vi.mock("~/client/hooks", async () => {
  const actual = await vi.importActual<Record<string, unknown>>("~/client/hooks");
  return {
    ...actual,
    useSuspenseSnapshot: () => ({ data: mockSnapshot }),
    useWSStatus: () => mockWS,
  };
});

vi.mock("~/client/config", () => ({
  useConfig: () => ({
    data: { repo: "gantry", provider: "", model: "", mode: "", task_types: [] },
  }),
}));

function renderBar() {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <TopBar />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mockSnapshot = buildSnapshot();
  mockWS = "connected";
  sendWSControl.mockReset();
  sendWSControl.mockImplementation((cmd: ControlCommand) =>
    Promise.resolve({ id: cmd.id, action: cmd.action, status: "ok", at: "now" }),
  );
});

describe("TopBar rendering", () => {
  it.each(["running", "paused", "draining", "idle"] as const)(
    "shows loop state %s with repo, mode and slots",
    (state) => {
      mockSnapshot = buildSnapshot({
        loop_state: state,
        mode: "auto",
        max_concurrency: 3,
        active: [buildSession({ id: "a" }), buildSession({ id: "b" })],
      });
      renderBar();

      expect(screen.getByLabelText("Loop state")).toHaveTextContent(state);
      expect(screen.getByText("gantry")).toBeInTheDocument();
      expect(screen.getByText(/^port /)).toBeInTheDocument();
      expect(screen.getByLabelText("Mode")).toHaveTextContent("auto");
      expect(screen.getByLabelText("Slots")).toHaveTextContent("2/3 slots");
    },
  );

  it("toggles the first button between Pause and Resume", () => {
    mockSnapshot = buildSnapshot({ loop_state: "paused" });
    renderBar();
    expect(screen.getByRole("button", { name: "Resume" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Pause" })).not.toBeInTheDocument();
  });

  it("puts status in the tab title", () => {
    mockSnapshot = buildSnapshot({
      active: [buildSession({ id: "a" }), buildSession({ id: "b" })],
      pending_review_count: 1,
      action_needed: [],
    });
    renderBar();
    expect(document.title).toBe("gantry · 2 running · 1 needs Shin");
  });

  it("counts action_needed toward needs-Shin", () => {
    mockSnapshot = buildSnapshot({ pending_review_count: 1, action_needed: ["x", "y"] });
    renderBar();
    expect(document.title).toBe("gantry · 0 running · 3 needs Shin");
  });

  it("labels the websocket as the connection, apart from the loop state", () => {
    mockWS = "disconnected";
    mockSnapshot = buildSnapshot({ loop_state: "running" });
    renderBar();
    expect(screen.getByLabelText("Connection")).toHaveTextContent("connection: disconnected");
    expect(screen.getByLabelText("Loop state")).toHaveTextContent("running");
  });
});

describe("TopBar controls", () => {
  it.each([
    ["running", "Pause", "pause"],
    ["paused", "Resume", "resume"],
    ["running", "Drain", "drain"],
  ] as const)("%s: %s sends %s and shows the ack", async (state, label, action) => {
    const user = userEvent.setup();
    mockSnapshot = buildSnapshot({ loop_state: state });
    renderBar();

    await user.click(screen.getByRole("button", { name: label }));

    expect(sendWSControl).toHaveBeenCalledWith(expect.objectContaining({ action }));
    expect(await screen.findByLabelText("Control ack")).toHaveTextContent(`${action}: ok`);
  });

  it("Stop all sends stop-all after confirmation", async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(globalThis, "confirm").mockReturnValue(true);
    renderBar();

    await user.click(screen.getByRole("button", { name: "Stop all" }));

    expect(confirm).toHaveBeenCalled();
    expect(sendWSControl).toHaveBeenCalledWith(expect.objectContaining({ action: "stop-all" }));
    expect(await screen.findByLabelText("Control ack")).toHaveTextContent("stop-all: ok");
  });

  it("Stop all sends nothing when confirmation is declined", async () => {
    const user = userEvent.setup();
    vi.spyOn(globalThis, "confirm").mockReturnValue(false);
    renderBar();

    await user.click(screen.getByRole("button", { name: "Stop all" }));

    expect(sendWSControl).not.toHaveBeenCalled();
  });

  it("shows an error ack from the server", async () => {
    const user = userEvent.setup();
    sendWSControl.mockResolvedValue({
      id: "1",
      action: "drain",
      status: "error",
      message: "nope",
      at: "",
    });
    renderBar();

    await user.click(screen.getByRole("button", { name: "Drain" }));

    expect(await screen.findByLabelText("Control ack")).toHaveTextContent("drain: error — nope");
  });
});
